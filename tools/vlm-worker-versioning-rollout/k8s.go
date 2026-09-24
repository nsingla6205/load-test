package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
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
		return false, 0, nil
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

func (c *k8sClient) ensureDrainResources(ctx context.Context, target RolloutTarget) error {
	resourceLabels := map[string]string{
		"app":                          target.DrainDeployment,
		"vlm.netapp.io/role":           "drain",
		"vlm-ontap-version":            target.OntapVersion,
		"vlm-build-id":                 target.OldBuildID,
		"app.kubernetes.io/managed-by": "Helm",
	}
	podLabels := map[string]string{
		"app":                target.DrainDeployment,
		"vlm.netapp.io/role": "drain",
		"vlm-ontap-version":  target.OntapVersion,
		"vlm-build-id":       target.OldBuildID,
	}
	annotations := map[string]string{
		"meta.helm.sh/release-name":      target.ReleaseName,
		"meta.helm.sh/release-namespace": target.ReleaseNamespace,
	}
	configMap := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":        target.DrainConfigMap,
			"namespace":   c.namespace,
			"labels":      resourceLabels,
			"annotations": annotations,
		},
		"data": target.ConfigData,
	}
	if err := c.upsertResource(ctx, "/api/v1", "configmaps", target.DrainConfigMap, configMap); err != nil {
		return fmt.Errorf("ensure drain ConfigMap %q: %w", target.DrainConfigMap, err)
	}

	deployment := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":        target.DrainDeployment,
			"namespace":   c.namespace,
			"labels":      resourceLabels,
			"annotations": annotations,
		},
		"spec": map[string]any{
			"replicas": target.DrainReplicas,
			"strategy": map[string]any{
				"type": "RollingUpdate",
				"rollingUpdate": map[string]any{
					"maxUnavailable": 0,
					"maxSurge":       1,
				},
			},
			"selector": map[string]any{
				"matchLabels": map[string]string{"app": target.DrainDeployment},
			},
			"template": map[string]any{
				"metadata": map[string]any{"labels": podLabels},
				"spec": map[string]any{
					"serviceAccountName": target.ServiceAccountName,
					"containers": []any{map[string]any{
						"name":            target.DrainDeployment,
						"image":           target.DrainImage,
						"imagePullPolicy": target.DrainImagePullPolicy,
						"resources":       target.Resources,
						"envFrom": []any{
							map[string]any{"configMapRef": map[string]string{"name": target.DrainConfigMap}},
							map[string]any{"secretRef": map[string]string{"name": target.SecretName}},
						},
					}},
				},
			},
		},
	}
	if err := c.upsertResource(ctx, "/apis/apps/v1", "deployments", target.DrainDeployment, deployment); err != nil {
		return fmt.Errorf("ensure drain Deployment %q: %w", target.DrainDeployment, err)
	}
	return nil
}

func (c *k8sClient) upsertResource(
	ctx context.Context,
	apiPath string,
	resource string,
	name string,
	object map[string]any,
) error {
	payload, err := json.Marshal(object)
	if err != nil {
		return fmt.Errorf("marshal %s %q: %w", resource, name, err)
	}
	collectionURL := fmt.Sprintf("%s%s/namespaces/%s/%s", c.baseURL, apiPath, c.namespace, resource)
	status, responseBody, err := c.doJSON(ctx, http.MethodPatch, collectionURL+"/"+name, payload)
	if err != nil {
		return err
	}
	if status >= 200 && status < 300 {
		return nil
	}
	if status != http.StatusNotFound {
		return fmt.Errorf("patch %s %q: HTTP %d: %s", resource, name, status, responseBody)
	}

	status, responseBody, err = c.doJSON(ctx, http.MethodPost, collectionURL, payload)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("create %s %q: HTTP %d: %s", resource, name, status, responseBody)
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
