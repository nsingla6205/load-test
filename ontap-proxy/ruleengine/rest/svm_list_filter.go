package rules_v2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// authDataFromRequest resolves the cached AuthData for a request using the auth
// data cache key stored in the request context by the credential middleware.
// Returns (nil, false) when no auth data is available.
func authDataFromRequest(r *http.Request) (*models.AuthData, bool) {
	if r == nil {
		return nil, false
	}
	cacheKey := cache.GetAuthDataKeyFromContext(r.Context())
	if cacheKey == "" {
		return nil, false
	}
	authData, exists := cache.GetFromAuthDataCache(cacheKey)
	if !exists || authData == nil {
		return nil, false
	}
	return authData, true
}

// isDedicatedPoolListing is a rule-engine Condition that is true only for
// dedicated-hardware (non-FLEX) pools. FLEX pools (and any request where auth
// data cannot be resolved) evaluate to false so the SVM-list flow is left
// completely unchanged for them.
//
// The returned reason string is unused in practice because the SVM-list rules
// always provide an IsFalse branch (a plain Allow); it exists only to satisfy
// the Condition signature.
func isDedicatedPoolListing(r *http.Request) (bool, string) {
	authData, ok := authDataFromRequest(r)
	if !ok {
		return false, "auth data not available"
	}
	if authData.ServiceLevel == models.ServiceLevelFLEX {
		return false, "flex pool"
	}
	return true, ""
}

// DedicatedSvmListFilter is a response Modification that restricts an ONTAP SVM
// listing to only the SVMs owned by the requesting account. It applies solely to
// dedicated-hardware (non-FLEX) pools; for FLEX pools or when the account UUID is
// not available it is a no-op, so shared-pool behaviour is never altered.
//
// It handles both the ONTAP REST shape (records keyed by "name") and the private
// CLI shape (records keyed by "vserver"), and recomputes "num_records" to match
// the filtered set.
type DedicatedSvmListFilter struct{}

func (f DedicatedSvmListFilter) Apply(target interface{}) error {
	resp, ok := target.(*http.Response)
	if !ok {
		return fmt.Errorf("DedicatedSvmListFilter: expected *http.Response, got %T", target)
	}
	if resp.Body == nil {
		return nil
	}

	authData, ok := authDataFromRequest(resp.Request)
	// Fail open: without auth data / account UUID, or for FLEX pools, do not touch
	// the response.
	if !ok || authData.ServiceLevel == models.ServiceLevelFLEX || authData.AccountUUID == "" {
		return nil
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	// Always hand downstream a fresh reader over whatever we read.
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("DedicatedSvmListFilter: failed to read response body: %w", err)
	}
	if closeErr != nil {
		util.GetLogger(resp.Request.Context()).WarnContext(resp.Request.Context(),
			"DedicatedSvmListFilter: response body Close failed after full read", "error", closeErr)
	}
	if len(body) == 0 {
		return nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		// A successful SVM listing must be a JSON object. Refuse to forward a 2xx
		// body we could not parse, since we cannot prove it is account-scoped.
		return fmt.Errorf("DedicatedSvmListFilter: successful SVM listing body was not a JSON object; refusing to forward unscoped response: %w", err)
	}

	rawRecords, hasRecords := data["records"]
	if !hasRecords {
		if numRecordsClaimsNonEmpty(data) {
			return fmt.Errorf("DedicatedSvmListFilter: listing reports records but has no \"records\" array; refusing to forward unscoped response")
		}
		return nil
	}

	records, ok := rawRecords.([]interface{})
	if !ok {
		// "records" is present but not an array: an unexpected shape we cannot
		// scope. Fail closed to avoid leaking SVMs owned by other accounts.
		return fmt.Errorf("DedicatedSvmListFilter: \"records\" field has unexpected type %T; refusing to forward unscoped SVM listing", rawRecords)
	}

	prefix := commonparams.GetSVMPrefix(authData.AccountUUID)
	filtered := make([]interface{}, 0, len(records))
	for _, record := range records {
		recordMap, ok := record.(map[string]interface{})
		if !ok {
			continue
		}
		if strings.HasPrefix(svmRecordName(recordMap), prefix) {
			filtered = append(filtered, record)
		}
	}

	data["records"] = filtered
	if _, present := data["num_records"]; present {
		data["num_records"] = len(filtered)
	}

	newBody, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("DedicatedSvmListFilter: failed to marshal response: %w", err)
	}

	resp.Body = io.NopCloser(bytes.NewReader(newBody))
	resp.ContentLength = int64(len(newBody))
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(newBody)))

	util.GetLogger(resp.Request.Context()).DebugContext(resp.Request.Context(),
		"DedicatedSvmListFilter: filtered SVM listing to account scope",
		"kept", len(filtered), "total", len(records))
	return nil
}

// numRecordsClaimsNonEmpty reports whether the listing's "num_records" field is
// present and greater than zero. It is used to detect an inconsistent shape where
// a listing claims records but provides no "records" array. Numbers decoded from
// JSON into interface{} are float64, so only that case can be non-empty.
func numRecordsClaimsNonEmpty(data map[string]interface{}) bool {
	if n, ok := data["num_records"].(float64); ok {
		return n > 0
	}
	return false
}

// svmRecordName returns the SVM name from an SVM listing record, handling both the
// ONTAP REST field ("name") and the private CLI field ("vserver").
func svmRecordName(record map[string]interface{}) string {
	if name, ok := record["name"].(string); ok && name != "" {
		return name
	}
	if vserver, ok := record["vserver"].(string); ok && vserver != "" {
		return vserver
	}
	return ""
}
