package ocicloudproxyclient

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestProxyRoundTripper(t *testing.T) {
	t.Run("sets json accept and identity encoding, no authorization", func(t *testing.T) {
		var gotAuth, gotAccept, gotEncoding string
		rt := proxyRoundTripper{
			next: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				gotAuth = req.Header.Get("Authorization")
				gotAccept = req.Header.Get("Accept")
				gotEncoding = req.Header.Get("Accept-Encoding")
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}
		req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
		require.NoError(t, err)

		_, err = rt.RoundTrip(req)
		require.NoError(t, err)
		assert.Empty(t, gotAuth)
		assert.Equal(t, "application/json", gotAccept)
		assert.Equal(t, "identity", gotEncoding)
	})

	t.Run("does not override a preset accept header", func(t *testing.T) {
		var gotAccept string
		rt := proxyRoundTripper{
			next: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				gotAccept = req.Header.Get("Accept")
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}
		req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
		require.NoError(t, err)
		req.Header.Set("Accept", "application/xml")

		_, err = rt.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, "application/xml", gotAccept)
	})
}

func TestNewProxyClient(t *testing.T) {
	client, err := NewProxyClient("https://oci-cloud-proxy.internal/v1", 30*time.Second, PathPrefixes{})
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestPathRewriteTransport(t *testing.T) {
	var gotPath string
	rt := pathRewriteTransport{
		next: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			gotPath = req.URL.Path
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		}),
		rewrites: pathRewrites(DefaultPathPrefixes(), PathPrefixes{
			Vault: "/v2/vault",
		}),
	}
	req, err := http.NewRequest(http.MethodPost, "https://proxy/20180608/secrets", nil)
	require.NoError(t, err)

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, "/v2/vault/secrets", gotPath)
}
