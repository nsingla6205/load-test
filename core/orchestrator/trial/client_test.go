package trial

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

func withGcpHydrateBaseURL(t *testing.T, baseURL string) {
	t.Helper()
	orig := googleInternalTrialAPIBaseURL
	googleInternalTrialAPIBaseURL = baseURL
	t.Cleanup(func() { googleInternalTrialAPIBaseURL = orig })
}

func TestNewClient_UsesGcpHydrateBaseURL(t *testing.T) {
	ctx := context.Background()
	resourceName := "projects/p/locations/us-central1/trial"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1internal/projects/p/locations/us-central1/trial:getInternalTrial", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"name": resourceName})
	}))
	defer server.Close()

	withGcpHydrateBaseURL(t, server.URL)

	c := NewClient(
		func(context.Context) (string, error) { return "tok", nil },
		WithHTTPClient(server.Client()),
	)
	got, err := c.GetInternalTrial(ctx, resourceName)
	require.NoError(t, err)
	assert.Equal(t, resourceName, got.Name)
}

func TestNewClient_WithBaseURLOverridesGcpHydrateBaseURL(t *testing.T) {
	ctx := context.Background()
	resourceName := "projects/p/locations/us-central1/trial"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"name": resourceName})
	}))
	defer server.Close()

	withGcpHydrateBaseURL(t, "https://ignored.example.com")

	c := NewClient(
		func(context.Context) (string, error) { return "tok", nil },
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	got, err := c.GetInternalTrial(ctx, resourceName)
	require.NoError(t, err)
	assert.Equal(t, resourceName, got.Name)
}

func TestClient_GetInternalTrial(t *testing.T) {
	ctx := context.Background()

	t.Run("empty name", func(t *testing.T) {
		c := NewClient(nil, WithBaseURL("https://producer.example.com"))
		_, err := c.GetInternalTrial(ctx, "")
		assert.Error(t, err)
	})

	t.Run("invalid resource name", func(t *testing.T) {
		c := NewClient(nil, WithBaseURL("https://producer.example.com"))
		_, err := c.GetInternalTrial(ctx, "bad-name")
		assert.Error(t, err)
	})

	t.Run("missing base URL", func(t *testing.T) {
		t.Run("explicit WithBaseURL empty", func(t *testing.T) {
			c := NewClient(nil, WithBaseURL(""))
			_, err := c.GetInternalTrial(ctx, "projects/p/locations/us-central1/trial")
			assert.Error(t, err)
		})

		t.Run("unset GCP_HYDRATE_BASE_URL", func(t *testing.T) {
			withGcpHydrateBaseURL(t, "")
			c := NewClient(func(context.Context) (string, error) { return "tok", nil })
			_, err := c.GetInternalTrial(ctx, "projects/p/locations/us-central1/trial")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "google internal trial API base URL is not configured")
		})
	})

	t.Run("success", func(t *testing.T) {
		start := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
		exit := "CONVERTED"
		resourceName := "projects/proj-1/locations/us-central1/trial"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "/v1internal/projects/proj-1/locations/us-central1/trial:getInternalTrial", r.URL.Path)
			assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":        resourceName,
				"start_time":  start.Format(time.RFC3339),
				"end_time":    end.Format(time.RFC3339),
				"exit_reason": exit,
			})
		}))
		defer server.Close()

		getToken := func(context.Context) (string, error) { return "test-token", nil }
		c := NewClient(getToken, WithBaseURL(server.URL), WithHTTPClient(server.Client()))

		got, err := c.GetInternalTrial(ctx, resourceName)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, resourceName, got.Name)
		assert.True(t, start.Equal(got.StartTime))
		assert.True(t, end.Equal(got.EndTime))
		require.NotNil(t, got.ExitReason)
		assert.Equal(t, datamodel.TrialExitReason(exit), *got.ExitReason)
	})

	t.Run("not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		getToken := func(context.Context) (string, error) { return "", nil }
		c := NewClient(getToken, WithBaseURL(server.URL), WithHTTPClient(server.Client()))

		_, err := c.GetInternalTrial(ctx, "projects/p/locations/us-central1/trial")
		assert.ErrorIs(t, err, ErrTrialNotFound)
	})

	t.Run("token getter error", func(t *testing.T) {
		getToken := func(context.Context) (string, error) {
			return "", assert.AnError
		}
		c := NewClient(getToken, WithBaseURL("https://producer.example.com"))
		_, err := c.GetInternalTrial(ctx, "projects/p/locations/us-central1/trial")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "get token")
	})

	t.Run("non-OK status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("upstream failure"))
		}))
		defer server.Close()

		c := NewClient(
			func(context.Context) (string, error) { return "tok", nil },
			WithBaseURL(server.URL),
			WithHTTPClient(server.Client()),
		)
		_, err := c.GetInternalTrial(ctx, "projects/p/locations/us-central1/trial")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status 500")
		assert.Contains(t, err.Error(), "upstream failure")
	})

	t.Run("malformed JSON body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not-json"))
		}))
		defer server.Close()

		c := NewClient(
			func(context.Context) (string, error) { return "tok", nil },
			WithBaseURL(server.URL),
			WithHTTPClient(server.Client()),
		)
		_, err := c.GetInternalTrial(ctx, "projects/p/locations/us-central1/trial")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal GetInternalTrial response")
	})

	t.Run("fills empty response name from request", func(t *testing.T) {
		resourceName := "projects/p/locations/us-central1/trial"
		start := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"start_time": start.Format(time.RFC3339),
				"end_time":   end.Format(time.RFC3339),
			})
		}))
		defer server.Close()

		c := NewClient(
			func(context.Context) (string, error) { return "tok", nil },
			WithBaseURL(server.URL),
			WithHTTPClient(server.Client()),
		)
		got, err := c.GetInternalTrial(ctx, resourceName)
		require.NoError(t, err)
		assert.Equal(t, resourceName, got.Name)
	})

	t.Run("trailing slash on base URL", func(t *testing.T) {
		resourceName := "projects/p/locations/us-central1/trial"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/v1internal/projects/p/locations/us-central1/trial:getInternalTrial", r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]any{"name": resourceName})
		}))
		defer server.Close()

		c := NewClient(
			func(context.Context) (string, error) { return "tok", nil },
			WithBaseURL(server.URL+"/"),
			WithHTTPClient(server.Client()),
		)
		got, err := c.GetInternalTrial(ctx, resourceName)
		require.NoError(t, err)
		assert.Equal(t, resourceName, got.Name)
	})

	t.Run("WithTokenGetter override", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer override-token", r.Header.Get("Authorization"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": "projects/p/locations/us-central1/trial",
			})
		}))
		defer server.Close()

		c := NewClient(
			func(context.Context) (string, error) { return "ignored", nil },
			WithBaseURL(server.URL),
			WithHTTPClient(server.Client()),
			WithTokenGetter(func(context.Context) (string, error) { return "override-token", nil }),
		)
		_, err := c.GetInternalTrial(ctx, "projects/p/locations/us-central1/trial")
		require.NoError(t, err)
	})
}
