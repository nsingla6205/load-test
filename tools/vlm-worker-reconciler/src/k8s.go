package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type k8sClientInterface interface {
	listVLMWorkerDeployments(ctx context.Context) ([]deploymentItem, error)
	scaleDeployment(ctx context.Context, name string, replicas int) error
}

type deploymentItem struct {
	Name     string
	Replicas int
	Labels   map[string]string
}

// k8sClient makes authenticated requests to the Kubernetes API using
// the pod's mounted service account token.
type k8sClient struct {
	http      *http.Client
	token     string
	namespace string
	baseURL   string // overridable for tests; production default set by newK8sClient
}

func newK8sClient(namespace string) (*k8sClient, error) {
	token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return nil, fmt.Errorf("read SA token: %w", err)
	}
	caCert, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCert)
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

func (c *k8sClient) listVLMWorkerDeployments(ctx context.Context) ([]deploymentItem, error) {
	url := fmt.Sprintf("%s/apis/apps/v1/namespaces/%s/deployments", c.baseURL, c.namespace)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list deployments: HTTP %d", resp.StatusCode)
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Replicas *int `json:"replicas"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode deployments: %w", err)
	}

	var out []deploymentItem
	for _, item := range result.Items {
		if !strings.HasPrefix(item.Metadata.Name, "vlm-worker-") {
			continue
		}
		replicas := 1
		if item.Spec.Replicas != nil {
			replicas = *item.Spec.Replicas
		}
		out = append(out, deploymentItem{
			Name:     item.Metadata.Name,
			Replicas: replicas,
			Labels:   item.Metadata.Labels,
		})
	}
	return out, nil
}

func (c *k8sClient) scaleDeployment(ctx context.Context, name string, replicas int) error {
	url := fmt.Sprintf("%s/apis/apps/v1/namespaces/%s/deployments/%s/scale",
		c.baseURL, c.namespace, name)
	body := strings.NewReader(fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas))
	req, _ := http.NewRequestWithContext(ctx, http.MethodPatch, url, body)
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
