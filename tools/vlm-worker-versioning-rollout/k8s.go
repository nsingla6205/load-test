package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

type k8sClient struct {
	http      *http.Client
	token     string
	namespace string
	baseURL   string
}

func newK8sClient(namespace string) (*k8sClient, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return nil, fmt.Errorf("read SA token: %w", err)
	}
	caCert, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("parse Kubernetes CA bundle: no certificates found")
	}
	return &k8sClient{
		http: &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
			Timeout:   30 * time.Second,
		},
		token:     strings.TrimSpace(string(token)),
		namespace: namespace,
		baseURL:   "https://kubernetes.default.svc",
	}, nil
}

func (c *k8sClient) deploymentReady(ctx context.Context, name string) (ready bool, replicas int, err error) {
	url := fmt.Sprintf("%s/apis/apps/v1/namespaces/%s/deployments/%s", c.baseURL, c.namespace, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return false, 0, errDeploymentNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return false, 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var dep struct {
		Metadata struct {
			Generation int64 `json:"generation"`
		} `json:"metadata"`
		Spec struct {
			Replicas *int `json:"replicas"`
		} `json:"spec"`
		Status struct {
			ObservedGeneration  int64 `json:"observedGeneration"`
			UpdatedReplicas     int   `json:"updatedReplicas"`
			ReadyReplicas       int   `json:"readyReplicas"`
			UnavailableReplicas int   `json:"unavailableReplicas"`
		} `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dep); err != nil {
		return false, 0, err
	}
	desired := 1
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}
	ready = desired > 0 &&
		dep.Status.ObservedGeneration >= dep.Metadata.Generation &&
		dep.Status.UpdatedReplicas >= desired &&
		dep.Status.ReadyReplicas >= desired &&
		dep.Status.UnavailableReplicas == 0
	return ready, desired, nil
}

func (c *k8sClient) scaleDeployment(ctx context.Context, name string, replicas int) error {
	url := fmt.Sprintf("%s/apis/apps/v1/namespaces/%s/deployments/%s/scale", c.baseURL, c.namespace, name)
	body := strings.NewReader(fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas))
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/merge-patch+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

const (
	cutoverRecordValue   = "done"
	initialCutoverMarker = "initial"
)

// cutoverConfigMapName is the ConfigMap the chart looks up to skip hook Jobs.
// The chart uses the same "<release>-versioning-cutover" name.
func cutoverConfigMapName(releaseName string) string {
	return strings.TrimSpace(releaseName) + "-versioning-cutover"
}

// cutoverRecordKey is one finished buildId and drainingBuilds pair.
// ConfigMap keys allow '.', '_', and '-'. The chart builds the same key.
func cutoverRecordKey(ontapVersion, newBuildID, oldBuildID string) string {
	return strings.TrimSpace(ontapVersion) + "__" + strings.TrimSpace(newBuildID) + "__" + strings.TrimSpace(oldBuildID)
}

// errDeploymentNotFound means the drain Deployment is not on the cluster yet.
// The pre-upgrade hook treats that as success: Helm creates the chart object.
// Creating it in the hook first makes Helm 3 fail the upgrade with
// "no ConfigMap with the name ... found" when the name is missing from the previous release.
var errDeploymentNotFound = errors.New("drain deployment not found")

func (c *k8sClient) recordFinishedCutover(ctx context.Context, releaseName, ontapVersion, newBuildID, oldBuildID string) error {
	name := cutoverConfigMapName(releaseName)
	key := cutoverRecordKey(ontapVersion, newBuildID, oldBuildID)
	if strings.TrimSpace(releaseName) == "" || strings.Contains(key, "____") || strings.HasPrefix(key, "__") || strings.HasSuffix(key, "__") {
		return fmt.Errorf("cutover record %q is incomplete", key)
	}
	patch, err := json.Marshal(map[string]any{
		"data": map[string]string{key: cutoverRecordValue},
	})
	if err != nil {
		return fmt.Errorf("marshal cutover record %q: %w", name, err)
	}
	collectionURL := fmt.Sprintf("%s/api/v1/namespaces/%s/configmaps", c.baseURL, c.namespace)
	status, responseBody, err := c.doJSON(ctx, http.MethodPatch, collectionURL+"/"+name, patch)
	if err != nil {
		return err
	}
	if status >= 200 && status < 300 {
		slog.Info("recorded finished cutover", "configMap", name, "key", key)
		return nil
	}
	if status != http.StatusNotFound {
		return fmt.Errorf("patch cutover ConfigMap %q: HTTP %d: %s", name, status, responseBody)
	}
	create, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": c.namespace,
			"labels": map[string]string{
				"vlm.netapp.io/role": "versioning-cutover",
			},
		},
		"data": map[string]string{key: cutoverRecordValue},
	})
	if err != nil {
		return fmt.Errorf("marshal cutover ConfigMap %q: %w", name, err)
	}
	status, responseBody, err = c.doJSON(ctx, http.MethodPost, collectionURL, create)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("create cutover ConfigMap %q: HTTP %d: %s", name, status, responseBody)
	}
	slog.Info("recorded finished cutover", "configMap", name, "key", key, "created", true)
	return nil
}

type jobEnv struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ensureBackgroundDrainJob starts modeDrainAndRetire in a Job that is not a Helm hook.
// An active or succeeded Job is left alone. A failed Job is replaced so the next upgrade can drain again.
func (c *k8sClient) ensureBackgroundDrainJob(ctx context.Context, name, image, serviceAccount string, env []jobEnv) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("drain job name is required")
	}
	if strings.TrimSpace(image) == "" {
		return fmt.Errorf("ROLLOUT_IMAGE is required to start the drain job")
	}
	if strings.TrimSpace(serviceAccount) == "" {
		return fmt.Errorf("SERVICE_ACCOUNT is required to start the drain job")
	}
	collectionURL := fmt.Sprintf("%s/apis/batch/v1/namespaces/%s/jobs", c.baseURL, c.namespace)
	status, body, err := c.doJSON(ctx, http.MethodGet, collectionURL+"/"+name, nil)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusNotFound:
		return c.createDrainJob(ctx, collectionURL, name, image, serviceAccount, env)
	case status < 200 || status >= 300:
		return fmt.Errorf("get drain job %q: HTTP %d: %s", name, status, body)
	}
	var existing struct {
		Status struct {
			Active    int `json:"active"`
			Succeeded int `json:"succeeded"`
			Failed    int `json:"failed"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &existing); err != nil {
		return fmt.Errorf("decode drain job %q: %w", name, err)
	}
	// Pending (no status yet) and succeeded jobs stay. Only a failed job is replaced.
	if existing.Status.Failed == 0 || existing.Status.Succeeded > 0 {
		slog.Info("drain job left in place", "job", name, "active", existing.Status.Active, "succeeded", existing.Status.Succeeded, "failed", existing.Status.Failed)
		return nil
	}
	slog.Info("drain job failed, replacing it", "job", name, "failed", existing.Status.Failed)
	jobURL := collectionURL + "/" + name
	// Background deletion is asynchronous. Wait until the name is gone before
	// creating again. A 409 against the terminating Job is not a new Job.
	delStatus, delBody, err := c.doJSON(ctx, http.MethodDelete, jobURL+"?propagationPolicy=Background", nil)
	if err != nil {
		return err
	}
	if delStatus != http.StatusNotFound && (delStatus < 200 || delStatus >= 300) {
		return fmt.Errorf("delete drain job %q: HTTP %d: %s", name, delStatus, delBody)
	}
	if err := c.waitForDrainJobGone(ctx, jobURL, name); err != nil {
		return err
	}
	return c.createDrainJob(ctx, collectionURL, name, image, serviceAccount, env)
}

// drainJobDeletePoll is how often to check that a deleted Job name is gone.
// Deletion often takes up to a minute, so one second would call the API for that whole wait.
const drainJobDeletePoll = 5 * time.Second

func (c *k8sClient) waitForDrainJobGone(ctx context.Context, jobURL, name string) error {
	ticker := time.NewTicker(drainJobDeletePoll)
	defer ticker.Stop()
	for {
		status, body, err := c.doJSON(ctx, http.MethodGet, jobURL, nil)
		if err != nil {
			return err
		}
		if status == http.StatusNotFound {
			return nil
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("get drain job %q while deleting: HTTP %d: %s", name, status, body)
		}
		slog.Info("drain job still terminating", "job", name)
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for drain job %q to be deleted: %w", name, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (c *k8sClient) createDrainJob(ctx context.Context, collectionURL, name, image, serviceAccount string, env []jobEnv) error {
	deadline := int64(envInt("DRAIN_TIMEOUT_MINUTES", 360)*60 + 1500)
	body, err := json.Marshal(map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]any{
			"name":      name,
			"namespace": c.namespace,
			"labels": map[string]string{
				"app":  "vlm-worker-versioning-rollout",
				"mode": modeDrainAndRetire,
			},
		},
		"spec": map[string]any{
			"backoffLimit":            0,
			"activeDeadlineSeconds":   deadline,
			"ttlSecondsAfterFinished": int64(86400),
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": map[string]string{"app": "vlm-worker-versioning-drain"},
					"annotations": map[string]string{
						"sidecar.istio.io/inject": "true",
						"proxy.istio.io/config":   `{ "holdApplicationUntilProxyStarts": true }`,
					},
				},
				"spec": map[string]any{
					"serviceAccountName": serviceAccount,
					"restartPolicy":      "Never",
					"containers": []map[string]any{{
						"name":            "rollout",
						"image":           image,
						"imagePullPolicy": "IfNotPresent",
						"env":             env,
						"resources": map[string]any{
							"requests": map[string]string{"cpu": "100m", "memory": "128Mi"},
							"limits":   map[string]string{"cpu": "500m", "memory": "256Mi"},
						},
					}},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal drain job: %w", err)
	}
	status, responseBody, err := c.doJSON(ctx, http.MethodPost, collectionURL, body)
	if err != nil {
		return err
	}
	if status == http.StatusConflict {
		// The name was missing when we looked. Another caller created it.
		slog.Info("drain job already exists", "job", name)
		return nil
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("create drain job %q: HTTP %d: %s", name, status, responseBody)
	}
	slog.Info("created drain job", "job", name, "namespace", c.namespace)
	return nil
}

func (c *k8sClient) doJSON(
	ctx context.Context,
	method string,
	url string,
	payload []byte,
) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(string(payload)))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/merge-patch+json")
	} else {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, "", err
	}
	return resp.StatusCode, strings.TrimSpace(string(body)), nil
}
