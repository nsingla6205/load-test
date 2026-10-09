package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return 0, "", err
	}
	return resp.StatusCode, strings.TrimSpace(string(body)), nil
}
