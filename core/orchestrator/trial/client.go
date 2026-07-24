// CCFE trial lookups (GetInternalTrial) for trial account sync.
package trial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// googleInternalTrialAPIBaseURL is the CCFE base URL for GetInternalTrial.
var googleInternalTrialAPIBaseURL = env.GetString("GCP_HYDRATE_BASE_URL", "")

// googleInternalTrialAPITimeout is the HTTP client timeout for GetInternalTrial (default 60s).
var googleInternalTrialAPITimeout = env.GetDuration("GOOGLE_INTERNAL_TRIAL_API_TIMEOUT", 60*time.Second)

const internalTrialResourceSegment = "trial"

// ErrTrialNotFound indicates the CCFE has no InternalTrial at the requested resource name.
// Callers (trial account sync) treat this as skip — VCP metadata is left unchanged.
var ErrTrialNotFound = errors.New("internal trial not found")

// Client calls the CCFE trial resource over GOOGLE_INTERNAL_TRIAL_API_BASE_URL.
type Client struct {
	baseURL    string
	httpClient *http.Client
	getToken   func(context.Context) (string, error)
}

// ClientOption configures Client.
type ClientOption func(*Client)

// WithHTTPClient sets a custom HTTP client (for tests).
func WithHTTPClient(c *http.Client) ClientOption {
	return func(client *Client) {
		client.httpClient = c
	}
}

// WithTokenGetter sets a custom token getter (for tests).
func WithTokenGetter(fn func(context.Context) (string, error)) ClientOption {
	return func(client *Client) {
		client.getToken = fn
	}
}

// WithBaseURL sets the base URL (for tests).
func WithBaseURL(url string) ClientOption {
	return func(client *Client) {
		client.baseURL = url
	}
}

// NewClient builds a client from GOOGLE_INTERNAL_TRIAL_API_* env and getToken (typically auth.GenerateCallbackToken).
func NewClient(getToken func(context.Context) (string, error), opts ...ClientOption) *Client {
	c := &Client{
		baseURL: googleInternalTrialAPIBaseURL,
		httpClient: &http.Client{
			Timeout: googleInternalTrialAPITimeout,
		},
		getToken: getToken,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// GetInternalTrial returns CCFE trial state for name.
//
// name must be projects/{projectNumber}/locations/{location}/trial (see datamodel.TrialResourceNameForAccount).
// The HTTP call is GET {GCP_HYDRATE_BASE_URL}/v1internal/projects/{projectNumber}/locations/{location}/trial.
// Google is source of truth for startTime, endTime, and exitReason when a trial ends.
func (c *Client) GetInternalTrial(ctx context.Context, name string) (*datamodel.InternalTrial, error) {
	logger := util.GetLogger(ctx)

	if name == "" {
		return nil, fmt.Errorf("internal trial resource name is required")
	}
	if c.baseURL == "" {
		return nil, fmt.Errorf("google internal trial API base URL is not configured")
	}

	projectNumber, location, err := datamodel.ParseInternalTrialResourceName(name)
	if err != nil {
		return nil, err
	}

	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	relPath := fmt.Sprintf("/v1internal/projects/%s/locations/%s/%s", projectNumber, location, internalTrialResourceSegment)
	reqURL := strings.TrimRight(c.baseURL, "/") + relPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create GetInternalTrial request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	logger.Infof("CCFE trial client: GetInternalTrial request resource=%s url=%s", name, reqURL)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call GetInternalTrial: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read GetInternalTrial response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrTrialNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GetInternalTrial returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var trial datamodel.InternalTrial
	if err := json.Unmarshal(respBody, &trial); err != nil {
		return nil, fmt.Errorf("unmarshal GetInternalTrial response: %w", err)
	}
	if trial.Name == "" {
		trial.Name = name
	}
	return &trial, nil
}
