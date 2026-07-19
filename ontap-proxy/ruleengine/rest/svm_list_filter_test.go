package rules_v2

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
)

const testAccountUUID = "8ecd3d96-7a33-5b40-0b02-bef53efd2f03"

// testAccountPrefix is the expected data-SVM prefix for testAccountUUID.
const testAccountPrefix = "svm_8ecd3d967a335b400b02bef53efd2f03_"

// requestWithAuthData returns a request whose context references a freshly cached
// AuthData entry under a unique key.
func requestWithAuthData(t *testing.T, method, path string, authData *models.AuthData) *http.Request {
	t.Helper()
	cacheKey := t.Name()
	cache.AddToAuthDataCache(cacheKey, authData)
	t.Cleanup(func() { cache.RemoveFromAuthDataCache(cacheKey) })
	req := httptest.NewRequest(method, path, nil)
	ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
	return req.WithContext(ctx)
}

func newJSONResponse(req *http.Request, body string) *http.Response {
	return newJSONResponseWithStatus(req, http.StatusOK, body)
}

func newJSONResponseWithStatus(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func readBody(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("response body is not a JSON object: %v (%s)", err, string(b))
	}
	return data
}

func recordNames(data map[string]interface{}) []string {
	records, _ := data["records"].([]interface{})
	names := make([]string, 0, len(records))
	for _, r := range records {
		if m, ok := r.(map[string]interface{}); ok {
			names = append(names, svmRecordName(m))
		}
	}
	return names
}

func TestIsDedicatedPoolListing(t *testing.T) {
	t.Run("Dedicated_ReturnsTrue", func(t *testing.T) {
		req := requestWithAuthData(t, http.MethodGet, "/api/svm/svms",
			&models.AuthData{ServiceLevel: "STANDARD", AccountUUID: testAccountUUID})
		ok, _ := isDedicatedPoolListing(req)
		if !ok {
			t.Fatal("expected dedicated pool to be true")
		}
	})

	t.Run("Flex_ReturnsFalse", func(t *testing.T) {
		req := requestWithAuthData(t, http.MethodGet, "/api/svm/svms",
			&models.AuthData{ServiceLevel: models.ServiceLevelFLEX, AccountUUID: testAccountUUID})
		ok, _ := isDedicatedPoolListing(req)
		if ok {
			t.Fatal("expected flex pool to be false")
		}
	})

	t.Run("NoAuthData_ReturnsFalse", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/svm/svms", nil)
		ok, _ := isDedicatedPoolListing(req)
		if ok {
			t.Fatal("expected missing auth data to be false")
		}
	})
}

func TestDedicatedSvmListFilter_RESTShape(t *testing.T) {
	req := requestWithAuthData(t, http.MethodGet, "/api/svm/svms",
		&models.AuthData{ServiceLevel: "STANDARD", AccountUUID: testAccountUUID})
	body := `{"records":[` +
		`{"name":"` + testAccountPrefix + `data1","uuid":"u1"},` +
		`{"name":"svm_otheraccount_data","uuid":"u2"},` +
		`{"name":"svm-cluster-mgmt","uuid":"u3"}` +
		`],"num_records":3}`
	resp := newJSONResponse(req, body)

	if err := (DedicatedSvmListFilter{}).Apply(resp); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	data := readBody(t, resp)
	names := recordNames(data)
	if len(names) != 1 || names[0] != testAccountPrefix+"data1" {
		t.Fatalf("expected only this account's SVM, got %v", names)
	}
	if num, ok := data["num_records"].(float64); !ok || int(num) != 1 {
		t.Fatalf("expected num_records=1, got %v", data["num_records"])
	}
	if resp.Header.Get("Content-Length") == "" {
		t.Fatal("expected Content-Length header to be set")
	}
}

func TestDedicatedSvmListFilter_PrivateCLIShape(t *testing.T) {
	req := requestWithAuthData(t, http.MethodGet, "/api/private/cli/vserver",
		&models.AuthData{ServiceLevel: "PREMIUM", AccountUUID: testAccountUUID})
	body := `{"records":[` +
		`{"vserver":"` + testAccountPrefix + `data1"},` +
		`{"vserver":"` + testAccountPrefix + `data2"},` +
		`{"vserver":"svm_otheraccount_data"}` +
		`],"num_records":3}`
	resp := newJSONResponse(req, body)

	if err := (DedicatedSvmListFilter{}).Apply(resp); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	names := recordNames(readBody(t, resp))
	if len(names) != 2 {
		t.Fatalf("expected 2 account SVMs, got %v", names)
	}
}

func TestDedicatedSvmListFilter_FlexIsNoOp(t *testing.T) {
	req := requestWithAuthData(t, http.MethodGet, "/api/svm/svms",
		&models.AuthData{ServiceLevel: models.ServiceLevelFLEX, AccountUUID: testAccountUUID})
	body := `{"records":[{"name":"svm_otheraccount_data","uuid":"u2"}],"num_records":1}`
	resp := newJSONResponse(req, body)

	if err := (DedicatedSvmListFilter{}).Apply(resp); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	names := recordNames(readBody(t, resp))
	if len(names) != 1 {
		t.Fatalf("flex pool must not be filtered, got %v", names)
	}
}

func TestDedicatedSvmListFilter_MissingAccountUUIDIsNoOp(t *testing.T) {
	req := requestWithAuthData(t, http.MethodGet, "/api/svm/svms",
		&models.AuthData{ServiceLevel: "STANDARD", AccountUUID: ""})
	body := `{"records":[{"name":"svm_otheraccount_data","uuid":"u2"}],"num_records":1}`
	resp := newJSONResponse(req, body)

	if err := (DedicatedSvmListFilter{}).Apply(resp); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	names := recordNames(readBody(t, resp))
	if len(names) != 1 {
		t.Fatalf("missing account UUID must not be filtered, got %v", names)
	}
}

func TestDedicatedSvmListFilter_NonJSONOn2xxFailsClosed(t *testing.T) {
	req := requestWithAuthData(t, http.MethodGet, "/api/svm/svms",
		&models.AuthData{ServiceLevel: "STANDARD", AccountUUID: testAccountUUID})
	resp := newJSONResponse(req, `not-json`)

	// A successful listing we cannot parse must fail closed rather than forward an
	// unscoped response to a dedicated-pool account.
	if err := (DedicatedSvmListFilter{}).Apply(resp); err == nil {
		t.Fatal("expected Apply to fail closed on a non-JSON 2xx body")
	}
}

func TestDedicatedSvmListFilter_ErrorBodyIsNoOp(t *testing.T) {
	req := requestWithAuthData(t, http.MethodGet, "/api/svm/svms",
		&models.AuthData{ServiceLevel: "STANDARD", AccountUUID: testAccountUUID})
	errorBody := `{"error":{"code":"13001","message":"entry doesn't exist"}}`
	resp := newJSONResponseWithStatus(req, http.StatusInternalServerError, errorBody)

	if err := (DedicatedSvmListFilter{}).Apply(resp); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	b, _ := io.ReadAll(resp.Body)
	if string(b) != errorBody {
		t.Fatalf("error body must be preserved untouched, got %q", string(b))
	}
}

func TestDedicatedSvmListFilter_RecordsWrongTypeFailsClosed(t *testing.T) {
	req := requestWithAuthData(t, http.MethodGet, "/api/svm/svms",
		&models.AuthData{ServiceLevel: "STANDARD", AccountUUID: testAccountUUID})
	// "records" present but an object rather than an array: an unexpected shape.
	resp := newJSONResponse(req, `{"records":{"name":"svm_otheraccount_data"},"num_records":1}`)

	if err := (DedicatedSvmListFilter{}).Apply(resp); err == nil {
		t.Fatal("expected Apply to fail closed when \"records\" is not an array")
	}
}

func TestDedicatedSvmListFilter_NumRecordsWithoutArrayFailsClosed(t *testing.T) {
	req := requestWithAuthData(t, http.MethodGet, "/api/svm/svms",
		&models.AuthData{ServiceLevel: "STANDARD", AccountUUID: testAccountUUID})
	// Claims records exist but provides no "records" array: inconsistent shape.
	resp := newJSONResponse(req, `{"num_records":3}`)

	if err := (DedicatedSvmListFilter{}).Apply(resp); err == nil {
		t.Fatal("expected Apply to fail closed when num_records>0 without a records array")
	}
}

func TestDedicatedSvmListFilter_EmptyCollectionIsNoOp(t *testing.T) {
	req := requestWithAuthData(t, http.MethodGet, "/api/svm/svms",
		&models.AuthData{ServiceLevel: "STANDARD", AccountUUID: testAccountUUID})
	// ONTAP omits "records" for an empty collection; num_records=0 is a safe no-op.
	resp := newJSONResponse(req, `{"num_records":0}`)

	if err := (DedicatedSvmListFilter{}).Apply(resp); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	// Should be untouched / still valid JSON object.
	_ = readBody(t, resp)
}
