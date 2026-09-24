package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testK8sClient builds a k8sClient pointed at the given test server.
func testK8sClient(t *testing.T, srv *httptest.Server, namespace string) *k8sClient {
	t.Helper()
	return &k8sClient{
		http:      srv.Client(),
		token:     "test-token",
		namespace: namespace,
		baseURL:   srv.URL,
	}
}

func TestListVLMWorkerDeployments_FiltersNonWorkerDeployments(t *testing.T) {
	deploymentList := map[string]any{
		"items": []map[string]any{
			{
				"metadata": map[string]any{
					"name":   "vlm-worker-9-12-1",
					"labels": map[string]any{"vlm.netapp.io/role": "primary"},
				},
				"spec": map[string]any{"replicas": 2},
			},
			{
				"metadata": map[string]any{"name": "other-service"},
				"spec":     map[string]any{"replicas": 1},
			},
			{
				"metadata": map[string]any{"name": "vlm-worker-9-13-0"},
				"spec":     map[string]any{"replicas": 1},
			},
			// nil replicas — should default to 1
			{
				"metadata": map[string]any{"name": "vlm-worker-9-11-0"},
				"spec":     map[string]any{},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(deploymentList)
	}))
	defer srv.Close()

	client := testK8sClient(t, srv, "test-ns")
	got, err := client.listVLMWorkerDeployments(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, deploymentItem{
		Name:     "vlm-worker-9-12-1",
		Replicas: 2,
		Labels:   map[string]string{"vlm.netapp.io/role": "primary"},
	}, got[0])
	assert.Equal(t, deploymentItem{Name: "vlm-worker-9-13-0", Replicas: 1}, got[1])
	assert.Equal(t, deploymentItem{Name: "vlm-worker-9-11-0", Replicas: 1}, got[2])
}

func TestListVLMWorkerDeployments_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client := testK8sClient(t, srv, "test-ns")
	_, err := client.listVLMWorkerDeployments(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 403")
}

func TestListVLMWorkerDeployments_NetworkError(t *testing.T) {
	// Point at a server that is immediately closed.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	srv.Close()

	client := testK8sClient(t, srv, "test-ns")
	_, err := client.listVLMWorkerDeployments(context.Background())

	require.Error(t, err)
}

func TestListVLMWorkerDeployments_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer srv.Close()

	client := testK8sClient(t, srv, "test-ns")
	got, err := client.listVLMWorkerDeployments(context.Background())

	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestScaleDeployment_SendsCorrectPatch(t *testing.T) {
	var capturedMethod, capturedPath, capturedBody, capturedContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := testK8sClient(t, srv, "test-ns")
	err := client.scaleDeployment(context.Background(), "vlm-worker-9-12-1", 0)

	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, capturedMethod)
	assert.Equal(t, "/apis/apps/v1/namespaces/test-ns/deployments/vlm-worker-9-12-1/scale", capturedPath)
	assert.Equal(t, "application/merge-patch+json", capturedContentType)
	assert.True(t, strings.Contains(capturedBody, `"replicas":0`), "body: %s", capturedBody)
}

func TestScaleDeployment_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer srv.Close()

	client := testK8sClient(t, srv, "test-ns")
	err := client.scaleDeployment(context.Background(), "vlm-worker-9-12-1", 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 422")
}
