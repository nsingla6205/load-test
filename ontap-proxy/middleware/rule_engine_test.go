package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/ruleengine/dsl"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

func TestRuleEngineMiddleware(t *testing.T) {
	t.Run("WhenNoRuleMatch_ShouldPassThrough", func(t *testing.T) {
		// Setup
		originalExtract := extractOntapPath
		extractOntapPath = func(fullPath string) string {
			return "/api/unknown/path"
		}
		defer func() { extractOntapPath = originalExtract }()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		middleware := RuleEngineMiddleware()
		handler := middleware(nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/ontap/api/unknown/path", nil)
		w := httptest.NewRecorder()

		// Execute
		handler.ServeHTTP(w, req)

		// Verify
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("WhenRuleMatchesAndAllows_ShouldPassThrough", func(t *testing.T) {
		// Setup
		originalExtract := extractOntapPath
		extractOntapPath = func(fullPath string) string {
			return "/api/storage/volumes"
		}
		defer func() { extractOntapPath = originalExtract }()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		middleware := RuleEngineMiddleware()
		handler := middleware(nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/ontap/api/storage/volumes", nil)
		w := httptest.NewRecorder()

		// Execute
		handler.ServeHTTP(w, req)

		// Verify
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("WhenNoRuleForPrivatePath_ShouldPassThrough", func(t *testing.T) {
		// Setup - /api/private/something has no rule; only explicit private CLI paths are configured
		originalExtract := extractOntapPath
		extractOntapPath = func(fullPath string) string {
			return "/api/private/something"
		}
		defer func() { extractOntapPath = originalExtract }()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		middleware := RuleEngineMiddleware()
		handler := middleware(nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/ontap/api/private/something", nil)
		w := httptest.NewRecorder()

		// Execute
		handler.ServeHTTP(w, req)

		// Verify - no rule matches, so request passes through to next handler
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("WhenDenyActionConfigured_ShouldReturnBadRequestWithReason", func(t *testing.T) {
		// Setup
		originalExtract := extractOntapPath
		extractOntapPath = func(fullPath string) string {
			return "/api/storage/aggregates"
		}
		defer func() { extractOntapPath = originalExtract }()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		middleware := RuleEngineMiddleware()
		handler := middleware(nextHandler)

		// POST is denied for aggregates
		req := httptest.NewRequest(http.MethodPost, "/ontap/api/storage/aggregates", nil)
		w := httptest.NewRecorder()

		// Execute
		handler.ServeHTTP(w, req)

		// Verify - POST is configured with Deny action, returns 400 with reason
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Aggregate creation not allowed")
	})

	t.Run("WhenClusterCounterTablesGET_ShouldDenyWithReason", func(t *testing.T) {
		originalExtract := extractOntapPath
		extractOntapPath = func(fullPath string) string {
			return "/api/cluster/counter/tables"
		}
		defer func() { extractOntapPath = originalExtract }()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		middleware := RuleEngineMiddleware()
		handler := middleware(nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/ontap/api/cluster/counter/tables", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Cluster counter tables not allowed")
	})

	t.Run("WhenActionInContext_ShouldBeRetrievable", func(t *testing.T) {
		// Setup
		originalExtract := extractOntapPath
		extractOntapPath = func(fullPath string) string {
			return "/api/storage/volumes"
		}
		defer func() { extractOntapPath = originalExtract }()

		actionFound := false
		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ctx := r.Context().Value(models.RuleContextKey); ctx != nil {
				if _, ok := ctx.(dsl.IAction); ok {
					actionFound = true
				}
			}
			w.WriteHeader(http.StatusOK)
		})

		middleware := RuleEngineMiddleware()
		handler := middleware(nextHandler)

		req := httptest.NewRequest(http.MethodGet, "/ontap/api/storage/volumes", nil)
		w := httptest.NewRecorder()

		// Execute
		handler.ServeHTTP(w, req)

		// Verify
		assert.True(t, actionFound, "Action should be found in context")
	})
}

func TestFindMatchingRule(t *testing.T) {
	t.Run("WhenExactMatch_ShouldReturnRule", func(t *testing.T) {
		// Setup
		originalExtract := extractOntapPath
		extractOntapPath = func(fullPath string) string {
			return "/api/storage/aggregates"
		}
		defer func() { extractOntapPath = originalExtract }()

		logger := util.GetLogger(context.Background())

		// Execute
		rule, path, found := findMatchingRule("/ontap/api/storage/aggregates", logger)

		// Verify
		assert.True(t, found)
		assert.Equal(t, "/api/storage/aggregates", path)
		assert.NotNil(t, rule.GET)
	})

	t.Run("WhenPrivatePathWithNoExactRule_ShouldReturnNotFound", func(t *testing.T) {
		// Setup - no catch-all for /api/private/*; only explicit private CLI paths have rules
		originalExtract := extractOntapPath
		extractOntapPath = func(fullPath string) string {
			return "/api/private/nested/path"
		}
		defer func() { extractOntapPath = originalExtract }()

		logger := util.GetLogger(context.Background())

		// Execute
		_, _, found := findMatchingRule("/ontap/api/private/nested/path", logger)

		// Verify - no rule for this path, so not found
		assert.False(t, found)
	})

	t.Run("WhenNoMatch_ShouldReturnNotFound", func(t *testing.T) {
		// Setup
		originalExtract := extractOntapPath
		extractOntapPath = func(fullPath string) string {
			return "/api/unknown/path"
		}
		defer func() { extractOntapPath = originalExtract }()

		logger := util.GetLogger(context.Background())

		// Execute
		_, _, found := findMatchingRule("/ontap/api/unknown/path", logger)

		// Verify
		assert.False(t, found)
	})

	t.Run("WhenClusterCounterTablesBasePath_ShouldMatchWildcardRule", func(t *testing.T) {
		originalExtract := extractOntapPath
		extractOntapPath = func(fullPath string) string {
			return "/api/cluster/counter/tables"
		}
		defer func() { extractOntapPath = originalExtract }()

		logger := util.GetLogger(context.Background())

		rule, path, found := findMatchingRule("/ontap/api/cluster/counter/tables", logger)

		assert.True(t, found)
		assert.Equal(t, "/api/cluster/counter/tables/*", path)
		assert.NotNil(t, rule.GET)
	})

	t.Run("WhenTrailingSlash_ShouldMatchRuleWithoutSlash", func(t *testing.T) {
		originalExtract := extractOntapPath
		extractOntapPath = func(fullPath string) string {
			return "/api/storage/flexcache/flexcaches/"
		}
		defer func() { extractOntapPath = originalExtract }()

		logger := util.GetLogger(context.Background())

		rule, path, found := findMatchingRule("/ontap/api/storage/flexcache/flexcaches/", logger)

		assert.True(t, found, "trailing slash should match rule without slash")
		assert.Equal(t, "/api/storage/flexcache/flexcaches", path)
		assert.NotNil(t, rule)
	})

	t.Run("WhenClusterCounterTablesSubPath_ShouldMatchWildcardRule", func(t *testing.T) {
		originalExtract := extractOntapPath
		extractOntapPath = func(fullPath string) string {
			return "/api/cluster/counter/tables/qos_detail/rows"
		}
		defer func() { extractOntapPath = originalExtract }()

		logger := util.GetLogger(context.Background())

		rule, path, found := findMatchingRule("/ontap/api/cluster/counter/tables/qos_detail/rows", logger)

		assert.True(t, found)
		assert.Equal(t, "/api/cluster/counter/tables/*", path)
		assert.NotNil(t, rule.GET)
	})

	t.Run("WhenFPolicyPersistentStorePath_ShouldNormalizeSVMUUIDAndMatchRule", func(t *testing.T) {
		logger := util.GetLogger(context.Background())
		requestPath := "/ontap/api/protocols/fpolicy/4f643fb4-fd21-11e8-ae49-0050568e2c1e/persistent-stores"

		rule, path, found := findMatchingRule(requestPath, logger)

		assert.True(t, found)
		assert.Equal(t, "/api/protocols/fpolicy/{uuid}/persistent-stores", path)
		assert.NotNil(t, rule.POST)
	})
}
