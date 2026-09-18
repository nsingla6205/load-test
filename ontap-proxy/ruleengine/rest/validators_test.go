package rules_v2

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreapi "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/core-api"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestGetSizeRawFromVolumeBody(t *testing.T) {
	t.Run("NilBody_ReturnsNilAndEmptyPath", func(t *testing.T) {
		raw, path := getSizeRawFromVolumeBody(nil)
		if raw != nil || path != "" {
			t.Fatalf("getSizeRawFromVolumeBody(nil) = %v, %q; want nil, \"\"", raw, path)
		}
	})
	t.Run("TopLevelSize_ReturnsValueAndSizePath", func(t *testing.T) {
		body := map[string]interface{}{"size": float64(1024)}
		raw, path := getSizeRawFromVolumeBody(body)
		if path != "size" || raw == nil {
			t.Fatalf("getSizeRawFromVolumeBody(body with size) = %v, %q; want 1024, \"size\"", raw, path)
		}
		if r, ok := raw.(float64); !ok || r != 1024 {
			t.Fatalf("raw = %v; want 1024", raw)
		}
	})
	t.Run("SpaceSize_ReturnsValueAndSpaceSizePath", func(t *testing.T) {
		body := map[string]interface{}{"space": map[string]interface{}{"size": float64(2048)}}
		raw, path := getSizeRawFromVolumeBody(body)
		if path != "space.size" || raw == nil {
			t.Fatalf("getSizeRawFromVolumeBody(body with space.size) = %v, %q; want 2048, \"space.size\"", raw, path)
		}
		if r, ok := raw.(float64); !ok || r != 2048 {
			t.Fatalf("raw = %v; want 2048", raw)
		}
	})
	t.Run("BothTopLevelSizeAndSpaceSize_ReturnsSpaceSize", func(t *testing.T) {
		body := map[string]interface{}{
			"size":  float64(1024),
			"space": map[string]interface{}{"size": float64(2048)},
		}
		raw, path := getSizeRawFromVolumeBody(body)
		if path != "space.size" || raw == nil {
			t.Fatalf("getSizeRawFromVolumeBody(both) = %v, %q; want 2048, \"space.size\"", raw, path)
		}
		if r, ok := raw.(float64); !ok || r != 2048 {
			t.Fatalf("raw = %v; want 2048", raw)
		}
	})
	t.Run("NeitherSizeNorSpaceSize_ReturnsNilAndEmptyPath", func(t *testing.T) {
		body := map[string]interface{}{"name": "vol1"}
		raw, path := getSizeRawFromVolumeBody(body)
		if raw != nil || path != "" {
			t.Fatalf("getSizeRawFromVolumeBody(body without size) = %v, %q; want nil, \"\"", raw, path)
		}
	})
	t.Run("SizeKeyPresentNilValue_ReturnsNilAndSizePath", func(t *testing.T) {
		body := map[string]interface{}{"size": nil}
		raw, path := getSizeRawFromVolumeBody(body)
		if path != "size" {
			t.Fatalf("path = %q; want \"size\" (key present)", path)
		}
		if raw != nil {
			t.Fatalf("raw = %v; want nil", raw)
		}
	})
}

func TestParseSizeFromVolumeBody(t *testing.T) {
	t.Run("NilBody_ReturnsZeroAndFalse", func(t *testing.T) {
		size, found := parseSizeFromVolumeBody(nil)
		if size != 0 || found {
			t.Fatalf("parseSizeFromVolumeBody(nil) = %v, %v; want 0, false", size, found)
		}
	})
	t.Run("NoSizeField_ReturnsZeroAndFalse", func(t *testing.T) {
		body := map[string]interface{}{"name": "vol1"}
		size, found := parseSizeFromVolumeBody(body)
		if size != 0 || found {
			t.Fatalf("parseSizeFromVolumeBody(no size) = %v, %v; want 0, false", size, found)
		}
	})
	t.Run("ValidTopLevelSize_ReturnsParsedAndTrue", func(t *testing.T) {
		body := map[string]interface{}{"size": float64(1024)}
		size, found := parseSizeFromVolumeBody(body)
		if size != 1024 || !found {
			t.Fatalf("parseSizeFromVolumeBody(size:1024) = %v, %v; want 1024, true", size, found)
		}
	})
	t.Run("ValidSpaceSize_ReturnsParsedAndTrue", func(t *testing.T) {
		body := map[string]interface{}{"space": map[string]interface{}{"size": float64(2048)}}
		size, found := parseSizeFromVolumeBody(body)
		if size != 2048 || !found {
			t.Fatalf("parseSizeFromVolumeBody(space.size:2048) = %v, %v; want 2048, true", size, found)
		}
	})
	t.Run("BothSizeAndSpaceSize_PrefersSpaceSize", func(t *testing.T) {
		body := map[string]interface{}{
			"size":  float64(1024),
			"space": map[string]interface{}{"size": float64(2048)},
		}
		size, found := parseSizeFromVolumeBody(body)
		if size != 2048 || !found {
			t.Fatalf("parseSizeFromVolumeBody(both) = %v, %v; want 2048, true", size, found)
		}
	})
	t.Run("SizePresentButInvalid_ReturnsZeroAndTrue", func(t *testing.T) {
		body := map[string]interface{}{"size": "abc"}
		size, found := parseSizeFromVolumeBody(body)
		if size != 0 || !found {
			t.Fatalf("parseSizeFromVolumeBody(size:\"abc\") = %v, %v; want 0, true (found but invalid)", size, found)
		}
	})
}

func TestParseVolumeRequestFields_SizeProvided(t *testing.T) {
	t.Run("NoSizeField_SizeProvidedFalse", func(t *testing.T) {
		body := map[string]interface{}{"name": "vol1"}
		fields := parseVolumeRequestFields(body)
		if fields.SizeProvided {
			t.Fatalf("parseVolumeRequestFields(no size): SizeProvided = true; want false")
		}
		if fields.SizeInBytes != 0 {
			t.Fatalf("SizeInBytes = %v; want 0", fields.SizeInBytes)
		}
	})
	t.Run("SizePresent_SizeProvidedTrue", func(t *testing.T) {
		body := map[string]interface{}{"name": "vol1", "size": float64(1024)}
		fields := parseVolumeRequestFields(body)
		if !fields.SizeProvided {
			t.Fatalf("parseVolumeRequestFields(size:1024): SizeProvided = false; want true")
		}
		if fields.SizeInBytes != 1024 {
			t.Fatalf("SizeInBytes = %v; want 1024", fields.SizeInBytes)
		}
	})
	t.Run("SpaceSizePresent_SizeProvidedTrue", func(t *testing.T) {
		body := map[string]interface{}{"name": "vol1", "space": map[string]interface{}{"size": float64(2048)}}
		fields := parseVolumeRequestFields(body)
		if !fields.SizeProvided {
			t.Fatalf("parseVolumeRequestFields(space.size:2048): SizeProvided = false; want true")
		}
		if fields.SizeInBytes != 2048 {
			t.Fatalf("SizeInBytes = %v; want 2048", fields.SizeInBytes)
		}
	})
	t.Run("BothSizeAndSpaceSize_PrefersSpaceSize", func(t *testing.T) {
		body := map[string]interface{}{
			"name":  "vol1",
			"size":  float64(1024),
			"space": map[string]interface{}{"size": float64(2048)},
		}
		fields := parseVolumeRequestFields(body)
		if !fields.SizeProvided {
			t.Fatalf("parseVolumeRequestFields(both): SizeProvided = false; want true")
		}
		if fields.SizeInBytes != 2048 {
			t.Fatalf("SizeInBytes = %v; want 2048 from space.size", fields.SizeInBytes)
		}
	})
}

func TestParseVolumeRequestFields_CloneUUIDFields(t *testing.T) {
	body := map[string]interface{}{
		"name": "clone-vol",
		"clone": map[string]interface{}{
			"is_flexclone": true,
			"parent_volume": map[string]interface{}{
				"uuid": "11111111-1111-1111-1111-111111111111",
			},
			"parent_snapshot": map[string]interface{}{
				"uuid": "22222222-2222-2222-2222-222222222222",
				"name": "snap-1",
			},
		},
	}

	fields := parseVolumeRequestFields(body)
	if !fields.Clone.IsSet() {
		t.Fatalf("expected clone to be set")
	}
	clone := fields.Clone.Value
	if !clone.ParentVolume.IsSet() || clone.ParentVolume.Value.UUID.Or("") == "" {
		t.Fatalf("expected parent volume uuid to be parsed")
	}
	if !clone.ParentSnapshot.IsSet() || clone.ParentSnapshot.Value.UUID.Or("") == "" {
		t.Fatalf("expected parent snapshot uuid to be parsed")
	}
	if clone.ParentSnapshot.Value.Name.Or("") != "snap-1" {
		t.Fatalf("expected parent snapshot name to be parsed")
	}
}

func TestVailidateVolumeCreation(t *testing.T) {
	// Save and restore original function
	origSubmit := submitExpertModeVolumeOperation
	defer func() { submitExpertModeVolumeOperation = origSubmit }()

	const cacheKey = "unit-test-cache-key"

	// success
	t.Run("WhenSuccess", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{"name":"vol1","size":1024,"svm":{}}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenCloneCreateWithoutSize_ShouldSucceed", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(
			http.MethodPost,
			"/api/storage/volumes",
			bytes.NewBufferString(`{"name":"clone_vol1","clone":{"is_flexclone":true,"parent_volume":{"name":"src_vol1"}}}`),
		)
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if !ok || reason != "" {
			t.Fatalf("expected clone create without size to succeed, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenCloneCreateWithoutParent_ShouldForwardCloneFlag", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			if !req.Clone.IsSet() {
				t.Fatalf("expected clone to be forwarded when clone.is_flexclone=true")
			}
			if !req.Clone.Value.IsFlexclone.IsSet() || !req.Clone.Value.IsFlexclone.Value {
				t.Fatalf("expected clone.isFlexclone=true in forwarded core request")
			}
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(
			http.MethodPost,
			"/api/storage/volumes",
			bytes.NewBufferString(`{"name":"clone_flag_only","clone":{"is_flexclone":true}}`),
		)
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if !ok || reason != "" {
			t.Fatalf("expected validation pass and core to return payload error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenNonCloneCreateWithoutSize_ShouldFail", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{"name":"vol1"}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if ok || !strings.Contains(reason, "\"size\" is a required field") {
			t.Fatalf("expected missing size error for non-clone create, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenCloneCreateWithSize_ShouldFail", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(
			http.MethodPost,
			"/api/storage/volumes",
			bytes.NewBufferString(`{"name":"clone_vol2","size":1048576,"clone":{"is_flexclone":true,"parent_volume":{"name":"src_vol2"}}}`),
		)
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if ok || !strings.Contains(reason, "must not be provided for clone volume create") {
			t.Fatalf("expected clone create with size to fail, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenIsFlexcloneFalse_ParentRefsIgnoredForCoreRequest", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			if req.Clone.IsSet() {
				t.Fatalf("expected clone refs to be ignored when clone.is_flexclone=false")
			}
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(
			http.MethodPost,
			"/api/storage/volumes",
			bytes.NewBufferString(`{"name":"vol-nonclone","size":1048576,"clone":{"is_flexclone":false,"parent_volume":{"name":"src_vol"}}}`),
		)
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if !ok || reason != "" {
			t.Fatalf("expected request to succeed, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenParseError", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{invalid`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if ok || !strings.Contains(reason, "invalid JSON") {
			t.Fatalf("expected parse error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenCloneParentProvidedWithoutIsFlexclone_ShouldFailWithoutSize", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(
			http.MethodPost,
			"/api/storage/volumes",
			bytes.NewBufferString(`{"name":"clone_vol3","clone":{"parent_volume":{"name":"src_vol3"}}}`),
		)
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if ok || !strings.Contains(reason, "\"size\" is a required field") {
			t.Fatalf("expected missing size error when is_flexclone not set, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenMissingCacheKey", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{"name":"vol1","size":1024}`))
		ok, reason := _validateVolumeCreation(r)
		if ok || !strings.Contains(reason, "cache key not found") {
			t.Fatalf("expected cache key error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenMissingAuthdata", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.WithValue(context.Background(), models.AuthDataKey, "wrong-key")
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{"name":"vol1","size":1024}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if ok || !strings.Contains(reason, "auth data not found") {
			t.Fatalf("expected missing auth data error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenPersistFails", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return errors.New("persist failed")
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{"name":"vol1","size":1024}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if ok || !strings.Contains(reason, "persist failed") {
			t.Fatalf("expected persist failure, got ok=%v reason=%q", ok, reason)
		}
	})

	// invalid size cases
	t.Run("WhenInvalidSizeUnit", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{"name":"vol1","size":"100GiB"}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if ok || !strings.Contains(reason, `"100GiB" is an invalid value for field "size"`) {
			t.Fatalf("expected invalid size error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenEmptySize", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{"name":"vol1","size":""}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if ok || !strings.Contains(reason, `"" is an invalid value for field "size"`) {
			t.Fatalf("expected invalid size error for empty, got ok=%v reason=%q", ok, reason)
		}
	})

	// size from space.size (REST allows either size or space.size)
	t.Run("WhenSpaceSizeOnly_ShouldSucceedAndSubmitSizeFromSpaceSize", func(t *testing.T) {
		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{"name":"vol1","space":{"size":2048}}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validateVolumeCreation(r)
		if !ok || reason != "" {
			t.Fatalf("expected success with space.size, got ok=%v reason=%q", ok, reason)
		}
		if capturedReq == nil || capturedReq.SizeInBytes.Or(0) != 2048 {
			t.Fatalf("expected submitted SizeInBytes 2048 from space.size, got %v", capturedReq)
		}
	})

	t.Run("WhenBothSizeAndSpaceSize_ShouldSucceedAndSubmitSpaceSize", func(t *testing.T) {
		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{"name":"vol1","size":1024,"space":{"size":2048}}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validateVolumeCreation(r)
		if !ok || reason != "" {
			t.Fatalf("expected success when both size fields set, got ok=%v reason=%q", ok, reason)
		}
		if capturedReq == nil || capturedReq.SizeInBytes.Or(0) != 2048 {
			t.Fatalf("expected submitted SizeInBytes 2048 from space.size, got %v", capturedReq)
		}
	})

	// negative-path: invalid space.size on POST
	t.Run("WhenSpaceSizeEmptyString_ShouldFail", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{"name":"vol1","space":{"size":""}}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validateVolumeCreation(r)
		if ok || !strings.Contains(reason, "invalid value for field \"space.size\"") {
			t.Fatalf("expected invalid space.size error for empty string, got ok=%v reason=%q", ok, reason)
		}
		// Message must name "space.size" when that is the only size field supplied
		if strings.Contains(reason, "field \"size\"") && !strings.Contains(reason, "space.size") {
			t.Fatalf("error message must not claim field \"size\" when only space.size was supplied; got reason=%q", reason)
		}
		if !strings.Contains(reason, `""`) {
			t.Fatalf("expected reason to mention empty value, got reason=%q", reason)
		}
	})

	t.Run("WhenSpaceSizeNonNumeric_ShouldFail", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{"name":"vol1","space":{"size":"abc"}}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validateVolumeCreation(r)
		if ok || !strings.Contains(reason, "invalid value for field \"space.size\"") {
			t.Fatalf("expected invalid space.size error for non-numeric, got ok=%v reason=%q", ok, reason)
		}
		if !strings.Contains(reason, "abc") {
			t.Fatalf("expected reason to mention invalid value, got reason=%q", reason)
		}
	})

	t.Run("WhenSpaceSizeZero_ShouldFail", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{"name":"vol1","space":{"size":0}}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validateVolumeCreation(r)
		if ok || !strings.Contains(reason, "invalid value for field \"space.size\"") {
			t.Fatalf("expected invalid space.size error for zero, got ok=%v reason=%q", ok, reason)
		}
		if !strings.Contains(reason, "0") {
			t.Fatalf("expected reason to mention zero value, got reason=%q", reason)
		}
	})

	t.Run("WhenCrossSvmByName_ShouldReturnPoolSvmName", func(t *testing.T) {
		const coreErr = "SVM with name 'svm_other_01' not found in pool; the SVM for this pool is 'svm_po_01'"
		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return errors.New(coreErr)
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes",
			bytes.NewBufferString(`{"name":"vol1","size":1024,"svm":{"name":"svm_other_01"}}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if ok {
			t.Fatal("expected cross-SVM create to fail")
		}
		if reason != coreErr {
			t.Fatalf("reason = %q, want %q", reason, coreErr)
		}
		if capturedReq == nil || !capturedReq.SvmName.IsSet() || capturedReq.SvmName.Value != "svm_other_01" {
			t.Fatalf("expected svm.name svm_other_01 forwarded to core, got %+v", capturedReq)
		}
	})

	t.Run("WhenCrossSvmByUuid_ShouldReturnPoolSvmName", func(t *testing.T) {
		const coreErr = "SVM with UUID 'other-svm-uuid' not found in pool; the SVM for this pool is 'svm_po_01'"
		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return errors.New(coreErr)
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes",
			bytes.NewBufferString(`{"name":"vol1","size":1024,"svm":{"uuid":"other-svm-uuid"}}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeCreation(r)
		if ok {
			t.Fatal("expected cross-SVM create to fail")
		}
		if reason != coreErr {
			t.Fatalf("reason = %q, want %q", reason, coreErr)
		}
		if capturedReq == nil || !capturedReq.SvmUuid.IsSet() || capturedReq.SvmUuid.Value != "other-svm-uuid" {
			t.Fatalf("expected svm.uuid other-svm-uuid forwarded to core, got %+v", capturedReq)
		}
	})
}

func TestValidateVolumeModification(t *testing.T) {
	origSubmit := submitExpertModeVolumeOperation
	origFlexSplit := submitExpertModeFlexCloneSplit
	defer func() {
		submitExpertModeVolumeOperation = origSubmit
		submitExpertModeFlexCloneSplit = origFlexSplit
	}()
	const cacheKey = "unit-test-cache-key-mod"

	// success
	t.Run("WhenSuccess", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		// PATCH on specific volume UUID
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"name":"vol1","size":2048}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeModification(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
	})

	// parse error invalid json
	t.Run("WhenParseError", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{invalid`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeModification(r)
		if ok || !strings.Contains(reason, "invalid JSON") {
			t.Fatalf("expected parse error, got ok=%v reason=%q", ok, reason)
		}
	})

	// missing cache key
	t.Run("WhenMissingCacheKey", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"name":"vol1","size":2048}`))
		ok, reason := _validateVolumeModification(r)
		if ok || !strings.Contains(reason, "cache key not found") {
			t.Fatalf("expected cache key error, got ok=%v reason=%q", ok, reason)
		}
	})

	// missing auth data
	t.Run("WhenMissingAuthdata", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.WithValue(context.Background(), models.AuthDataKey, "wrong-key")
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"name":"vol1","size":2048}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeModification(r)
		if ok || !strings.Contains(reason, "auth data not found") {
			t.Fatalf("expected missing auth data error, got ok=%v reason=%q", ok, reason)
		}
	})

	// persist fails
	t.Run("WhenPersistFails", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return errors.New("persist failed")
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"name":"vol1","size":2048}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeModification(r)
		if ok || !strings.Contains(reason, "persist failed") {
			t.Fatalf("expected persist failure, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenInvalidSizeUnit", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"name":"vol1","size":"100GiB"}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeModification(r)
		if ok || !strings.Contains(reason, `"100GiB" is an invalid value for field "size"`) {
			t.Fatalf("expected invalid size error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenNonNumericSize", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"name":"vol1","size":"abc"}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeModification(r)
		if ok || !strings.Contains(reason, `"abc" is an invalid value for field "size"`) {
			t.Fatalf("expected invalid size error for abc, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenOnlyNameProvided_ShouldTriggerReconcile", func(t *testing.T) {
		reconcileCalled := false
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			reconcileCalled = true
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"name":"vol1"}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeModification(r)
		if !ok || reason != "" {
			t.Fatalf("expected success when only name provided, got ok=%v reason=%q", ok, reason)
		}
		if !reconcileCalled {
			t.Fatal("expected reconcile to be called when name is provided")
		}
	})

	t.Run("WhenNeitherNameNorSizeProvided_ShouldSucceedWithoutReconcile", func(t *testing.T) {
		reconcileCalled := false
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			reconcileCalled = true
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeModification(r)
		if !ok || reason != "" {
			t.Fatalf("expected success when neither name nor size provided, got ok=%v reason=%q", ok, reason)
		}
		if reconcileCalled {
			t.Fatal("expected reconcile NOT to be called when neither name nor size is provided")
		}
	})

	t.Run("WhenOnlySizeProvided_ShouldTriggerReconcile", func(t *testing.T) {
		reconcileCalled := false
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			reconcileCalled = true
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"size":2048}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeModification(r)
		if !ok || reason != "" {
			t.Fatalf("expected success when only size provided, got ok=%v reason=%q", ok, reason)
		}
		if !reconcileCalled {
			t.Fatal("expected reconcile to be called when size is provided")
		}
	})

	t.Run("WhenSizeProvided_ShouldTriggerReconcile", func(t *testing.T) {
		reconcileCalled := false
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			reconcileCalled = true
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		// PATCH with size field
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"name":"vol1","size":2048}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeModification(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
		if !reconcileCalled {
			t.Fatal("expected reconcile to be called when size is provided")
		}
	})

	t.Run("WhenSizeIsNull_ShouldFail", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		// PATCH with size explicitly set to null
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"name":"vol1","size":null}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeModification(r)
		if ok || !strings.Contains(reason, "invalid value for field \"size\"") {
			t.Fatalf("expected invalid size error for null, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenSizeIsZero_ShouldFail", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		// PATCH with size explicitly set to 0
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"name":"vol1","size":0}`))
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeModification(r)
		if ok || !strings.Contains(reason, "invalid value for field \"size\"") {
			t.Fatalf("expected invalid size error for zero, got ok=%v reason=%q", ok, reason)
		}
	})

	// PATCH can send size via space.size; validator should use it for expert-mode submit
	t.Run("WhenSpaceSizeOnly_ShouldTriggerReconcileWithSizeFromSpaceSize", func(t *testing.T) {
		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"space":{"size":4096}}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validateVolumeModification(r)
		if !ok || reason != "" {
			t.Fatalf("expected success with space.size on PATCH, got ok=%v reason=%q", ok, reason)
		}
		if capturedReq == nil || capturedReq.SizeInBytes.Or(0) != 4096 {
			t.Fatalf("expected submitted SizeInBytes 4096 from space.size, got %v", capturedReq)
		}
	})

	// negative-path: invalid space.size on PATCH
	t.Run("WhenSpaceSizeEmptyString_ShouldFail", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"space":{"size":""}}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validateVolumeModification(r)
		if ok || !strings.Contains(reason, "invalid value for field \"space.size\"") {
			t.Fatalf("expected invalid space.size error for empty string on PATCH, got ok=%v reason=%q", ok, reason)
		}
		if !strings.Contains(reason, `""`) {
			t.Fatalf("expected reason to mention empty value, got reason=%q", reason)
		}
	})

	t.Run("WhenSpaceSizeNonNumeric_ShouldFail", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"space":{"size":"xyz"}}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validateVolumeModification(r)
		if ok || !strings.Contains(reason, "invalid value for field \"space.size\"") {
			t.Fatalf("expected invalid space.size error for non-numeric on PATCH, got ok=%v reason=%q", ok, reason)
		}
		if !strings.Contains(reason, "xyz") {
			t.Fatalf("expected reason to mention invalid value, got reason=%q", reason)
		}
	})

	t.Run("WhenSpaceSizeZero_ShouldFail", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/abcd-1234", bytes.NewBufferString(`{"space":{"size":0}}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validateVolumeModification(r)
		if ok || !strings.Contains(reason, "invalid value for field \"space.size\"") {
			t.Fatalf("expected invalid space.size error for zero on PATCH, got ok=%v reason=%q", ok, reason)
		}
		if !strings.Contains(reason, "0") {
			t.Fatalf("expected reason to mention zero value, got reason=%q", reason)
		}
	})

	t.Run("WhenFlexCloneSplitInitiated_ShouldCallFlexCloneSplitAPI", func(t *testing.T) {
		var gotVolUUID, gotVolName, gotProj, gotPool string
		submitExpertModeFlexCloneSplit = func(ctx context.Context, volumeUUID, volumeName, projectNumber, poolUUID, jwt string, logger log.Logger) error {
			gotVolUUID, gotVolName, gotProj, gotPool = volumeUUID, volumeName, projectNumber, poolUUID
			return nil
		}
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			t.Fatal("volume operation submit must not be called for flexclone split PATCH")
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/vol-uuid-99", bytes.NewBufferString(`{"clone":{"split_initiated":true}}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validateVolumeModification(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
		if gotVolUUID != "vol-uuid-99" || gotVolName != "" || gotProj != "acc" || gotPool != "pool" {
			t.Fatalf("unexpected flexclone split args: volUUID=%q volName=%q proj=%q pool=%q", gotVolUUID, gotVolName, gotProj, gotPool)
		}
	})

	t.Run("WhenFlexCloneSplitWithName_ShouldReject", func(t *testing.T) {
		submitExpertModeFlexCloneSplit = func(ctx context.Context, volumeUUID, volumeName, projectNumber, poolUUID, jwt string, logger log.Logger) error {
			t.Fatal("flexclone split submit must not be called when combined with name")
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/vol-uuid-99", bytes.NewBufferString(`{"name":"v1","clone":{"split_initiated":true}}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validateVolumeModification(r)
		if ok || !strings.Contains(reason, "cannot be combined") {
			t.Fatalf("expected combined-request error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenFlexCloneSplitWithoutVolumeUUID_ShouldReject", func(t *testing.T) {
		submitExpertModeFlexCloneSplit = func(ctx context.Context, volumeUUID, volumeName, projectNumber, poolUUID, jwt string, logger log.Logger) error {
			t.Fatal("flexclone split submit must not be called without volume UUID in path")
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes", bytes.NewBufferString(`{"clone":{"split_initiated":true}}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validateVolumeModification(r)
		if ok || !strings.Contains(reason, "volume UUID is required") {
			t.Fatalf("expected missing UUID error, got ok=%v reason=%q", ok, reason)
		}
	})
}

func TestValidateVolumeDeletion(t *testing.T) {
	origSubmit := submitExpertModeVolumeOperation
	defer func() { submitExpertModeVolumeOperation = origSubmit }()
	const cacheKey = "unit-test-cache-key-del"

	// success
	t.Run("WhenSuccess", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodDelete, "/api/storage/volumes/abcd-1234", nil)
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeDeletion(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
	})

	// missing cache key
	t.Run("WhenMissingCacheKey", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		r := httptest.NewRequest(http.MethodDelete, "/api/storage/volumes/abcd-1234", nil)
		ok, reason := _validateVolumeDeletion(r)
		if ok || !strings.Contains(reason, "cache key not found") {
			t.Fatalf("expected cache key error, got ok=%v reason=%q", ok, reason)
		}
	})

	// missing auth data
	t.Run("WhenMissingAuthdata", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.WithValue(context.Background(), models.AuthDataKey, "wrong-key")
		r := httptest.NewRequest(http.MethodDelete, "/api/storage/volumes/abcd-1234", nil)
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeDeletion(r)
		if ok || !strings.Contains(reason, "auth data not found") {
			t.Fatalf("expected missing auth data error, got ok=%v reason=%q", ok, reason)
		}
	})

	// persist fails
	t.Run("WhenPersistFails", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return errors.New("persist failed")
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodDelete, "/api/storage/volumes/abcd-1234", nil)
		r = r.WithContext(ctx)
		ok, reason := _validateVolumeDeletion(r)
		if ok || !strings.Contains(reason, "persist failed") {
			t.Fatalf("expected persist failure, got ok=%v reason=%q", ok, reason)
		}
	})
}

func TestValidateFlexCacheCreation(t *testing.T) {
	origSubmit := submitExpertModeVolumeOperation
	defer func() { submitExpertModeVolumeOperation = origSubmit }()
	const cacheKey = "unit-test-cache-key-flexcache-create"

	t.Run("WhenSuccess_ShouldSubmitFlexcacheStyle", func(t *testing.T) {
		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/flexcache/flexcaches", bytes.NewBufferString(`{"name":"fc1","size":1024,"svm":{"name":"svm1"}}`))
		r = r.WithContext(ctx)
		ok, reason := _validateFlexCacheCreation(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
		if capturedReq == nil || capturedReq.Style != coreapi.ExpertModeVolumeV1StyleFlexcache {
			t.Fatalf("expected submitted style to be flexcache, got %v", capturedReq)
		}
	})

	t.Run("WhenInvalidSize_ShouldFail", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/storage/flexcache/flexcaches", bytes.NewBufferString(`{"name":"fc1","size":"bad","svm":{"name":"svm1"}}`))
		r = r.WithContext(ctx)
		ok, reason := _validateFlexCacheCreation(r)
		if ok || !strings.Contains(reason, "invalid value for field \"size\"") {
			t.Fatalf("expected invalid size error, got ok=%v reason=%q", ok, reason)
		}
	})
}

func TestValidateFlexCacheDeletion(t *testing.T) {
	origSubmit := submitExpertModeVolumeOperation
	defer func() { submitExpertModeVolumeOperation = origSubmit }()
	const cacheKey = "unit-test-cache-key-flexcache-delete"

	t.Run("WhenSuccess_ShouldSubmitFlexcacheStyle", func(t *testing.T) {
		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodDelete, "/api/storage/flexcache/flexcaches/abcd-1234", nil)
		r = r.WithContext(ctx)
		ok, reason := _validateFlexCacheDeletion(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
		if capturedReq == nil || capturedReq.Style != coreapi.ExpertModeVolumeV1StyleFlexcache {
			t.Fatalf("expected submitted style to be flexcache, got %v", capturedReq)
		}
	})

	t.Run("WhenSubmitFails_ShouldReturnError", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return errors.New("persist failed")
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodDelete, "/api/storage/flexcache/flexcaches/abcd-1234", nil)
		r = r.WithContext(ctx)
		ok, reason := _validateFlexCacheDeletion(r)
		if ok || !strings.Contains(reason, "persist failed") {
			t.Fatalf("expected persist failure, got ok=%v reason=%q", ok, reason)
		}
	})
}

func TestValidatePrivateCLIVolumeCreation(t *testing.T) {
	origSubmit := submitExpertModeVolumeOperation
	defer func() { submitExpertModeVolumeOperation = origSubmit }()

	const cacheKey = "unit-test-cache-key-priv-cli"

	t.Run("WhenSuccess", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume", bytes.NewBufferString(`{"volume":"vol1","vserver":"vs0","size":1024}`))
		r = r.WithContext(ctx)
		ok, reason := _validatePrivateCLIVolumeCreation(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenMissingCacheKey", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		r := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume", bytes.NewBufferString(`{"volume":"vol1","vserver":"vs0","size":1024}`))
		ok, reason := _validatePrivateCLIVolumeCreation(r)
		if ok || !strings.Contains(reason, "cache key not found") {
			t.Fatalf("expected cache key error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenInvalidSize", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume", bytes.NewBufferString(`{"volume":"vol1","vserver":"vs0","size":"100GiB"}`))
		r = r.WithContext(ctx)
		ok, reason := _validatePrivateCLIVolumeCreation(r)
		if ok || !strings.Contains(reason, "invalid value for field \"size\"") {
			t.Fatalf("expected invalid size error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenCrossSvm_ShouldReturnPoolSvmName", func(t *testing.T) {
		const coreErr = "SVM with name 'svm_other_01' not found in pool; the SVM for this pool is 'svm_po_01'"
		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return errors.New(coreErr)
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume",
			bytes.NewBufferString(`{"volume":"vol1","vserver":"svm_other_01","size":1024}`))
		r = r.WithContext(ctx)
		ok, reason := _validatePrivateCLIVolumeCreation(r)
		if ok {
			t.Fatal("expected cross-SVM create to fail")
		}
		if reason != coreErr {
			t.Fatalf("reason = %q, want %q", reason, coreErr)
		}
		if capturedReq == nil || !capturedReq.SvmName.IsSet() || capturedReq.SvmName.Value != "svm_other_01" {
			t.Fatalf("expected vserver svm_other_01 forwarded to core, got %+v", capturedReq)
		}
	})
}

func TestValidatePrivateCLIVolumeCloneCreate(t *testing.T) {
	origSubmit := submitExpertModeVolumeOperation
	defer func() { submitExpertModeVolumeOperation = origSubmit }()

	const cacheKey = "unit-test-cache-key-priv-cli-clone-create"

	t.Run("WhenSuccessWithParentVolumeAndSize", func(t *testing.T) {
		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return nil
		}

		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume/clone", bytes.NewBufferString(`{"vserver":"vs0","flexclone":"clone1","parent_volume":"src1","parent_snapshot":"snap1","size":1024}`))
		r = r.WithContext(ctx)
		ok, reason := _validatePrivateCLIVolumeCloneCreate(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
		if capturedReq == nil {
			t.Fatal("expected request to be submitted")
		}
		if capturedReq.VolumeName != "clone1" {
			t.Fatalf("expected clone name clone1, got %q", capturedReq.VolumeName)
		}
		if !capturedReq.Clone.IsSet() || !capturedReq.Clone.Value.ParentVolume.IsSet() {
			t.Fatal("expected clone parent volume to be set")
		}
		if capturedReq.Clone.Value.ParentVolume.Value.Name.Or("") != "src1" {
			t.Fatalf("expected parent volume src1, got %q", capturedReq.Clone.Value.ParentVolume.Value.Name.Or(""))
		}
		if !capturedReq.SizeInBytes.IsSet() || capturedReq.SizeInBytes.Value != 1024 {
			t.Fatalf("expected size 1024, got %+v", capturedReq.SizeInBytes)
		}
	})

	t.Run("WhenParentVolumeAliasB_ShouldSucceedWithoutSize", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			if req.SizeInBytes.IsSet() {
				t.Fatal("expected size to be omitted")
			}
			if req.Clone.Value.ParentVolume.Value.Name.Or("") != "src1" {
				t.Fatalf("expected alias b to map parent volume src1, got %q", req.Clone.Value.ParentVolume.Value.Name.Or(""))
			}
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume/clone", bytes.NewBufferString(`{"vserver":"vs0","flexclone":"clone1","b":"src1"}`))
		r = r.WithContext(ctx)
		ok, reason := _validatePrivateCLIVolumeCloneCreate(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenInvalidSize_ShouldFail", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume/clone", bytes.NewBufferString(`{"vserver":"vs0","flexclone":"clone1","parent_volume":"src1","size":"100GiB"}`))
		r = r.WithContext(ctx)
		ok, reason := _validatePrivateCLIVolumeCloneCreate(r)
		if ok || !strings.Contains(reason, "invalid value for field \"size\"") {
			t.Fatalf("expected invalid size error, got ok=%v reason=%q", ok, reason)
		}
	})
}

func TestValidatePrivateCLIVolumeCloneSplit(t *testing.T) {
	origSplitSubmit := submitExpertModeFlexCloneSplit
	defer func() { submitExpertModeFlexCloneSplit = origSplitSubmit }()

	const cacheKey = "unit-test-cache-key-priv-cli-clone-split"

	t.Run("WhenSuccess_ShouldSubmitCoreSplitWithCloneName", func(t *testing.T) {
		var gotVolUUID, gotVolName, gotProj, gotPool string
		submitExpertModeFlexCloneSplit = func(ctx context.Context, volumeUUID, volumeName, projectNumber, poolUUID, jwt string, logger log.Logger) error {
			gotVolUUID, gotVolName, gotProj, gotPool = volumeUUID, volumeName, projectNumber, poolUUID
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume/clone/split/start", bytes.NewBufferString(`{"vserver":"vs0","flexclone":"clone1"}`))
		r = r.WithContext(ctx)

		ok, reason := _validatePrivateCLIVolumeCloneSplit(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
		if gotVolUUID != "" || gotVolName != "clone1" || gotProj != "acc" || gotPool != "pool" {
			t.Fatalf("unexpected split submit args volUUID=%q volName=%q proj=%q pool=%q", gotVolUUID, gotVolName, gotProj, gotPool)
		}
	})

	t.Run("WhenMissingCacheKey_ShouldFail", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume/clone/split/start", bytes.NewBufferString(`{"vserver":"vs0","flexclone":"clone1"}`))
		ok, reason := _validatePrivateCLIVolumeCloneSplit(r)
		if ok || !strings.Contains(reason, "cache key not found") {
			t.Fatalf("expected cache key error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenMissingAuthData_ShouldFail", func(t *testing.T) {
		ctx := context.Background()
		ctx = context.WithValue(ctx, models.AuthDataKey, "missing-cache-key")
		r := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume/clone/split/start", bytes.NewBufferString(`{"vserver":"vs0","flexclone":"clone1"}`))
		r = r.WithContext(ctx)

		ok, reason := _validatePrivateCLIVolumeCloneSplit(r)
		if ok || !strings.Contains(reason, "auth data not found") {
			t.Fatalf("expected missing auth data error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenCoreSplitFails_ShouldFail", func(t *testing.T) {
		submitExpertModeFlexCloneSplit = func(ctx context.Context, volumeUUID, volumeName, projectNumber, poolUUID, jwt string, logger log.Logger) error {
			return errors.New("volume is not a FlexClone")
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume/clone/split/start", bytes.NewBufferString(`{"vserver":"vs0","flexclone":"clone1"}`))
		r = r.WithContext(ctx)

		ok, reason := _validatePrivateCLIVolumeCloneSplit(r)
		if ok || !strings.Contains(reason, "volume is not a FlexClone") {
			t.Fatalf("expected core split failure, got ok=%v reason=%q", ok, reason)
		}
	})
}

func TestValidatePrivateCLIVolumeModification(t *testing.T) {
	origSubmit := submitExpertModeVolumeOperation
	defer func() { submitExpertModeVolumeOperation = origSubmit }()

	const cacheKey = "unit-test-cache-key-priv-cli-mod"

	t.Run("WhenSuccess", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/private/cli/volume?vserver=vs1&volume=vol1", bytes.NewBufferString(`{"size":2048}`))
		r = r.WithContext(ctx)
		ok, reason := _validatePrivateCLIVolumeModification(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenMissingQueryParams", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/private/cli/volume", bytes.NewBufferString(`{"size":2048}`))
		r = r.WithContext(ctx)
		ok, reason := _validatePrivateCLIVolumeModification(r)
		if ok || !strings.Contains(reason, "missing required query parameter") {
			t.Fatalf("expected missing query param error, got ok=%v reason=%q", ok, reason)
		}
	})
}

func TestValidatePrivateCLIVolumeDeletion(t *testing.T) {
	origSubmit := submitExpertModeVolumeOperation
	defer func() { submitExpertModeVolumeOperation = origSubmit }()

	const cacheKey = "unit-test-cache-key-priv-cli-del"

	t.Run("WhenSuccess", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodDelete, "/api/private/cli/volume?vserver=vs1&volume=vol1", nil)
		r = r.WithContext(ctx)
		ok, reason := _validatePrivateCLIVolumeDeletion(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenMissingQueryParams", func(t *testing.T) {
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodDelete, "/api/private/cli/volume", nil)
		r = r.WithContext(ctx)
		ok, reason := _validatePrivateCLIVolumeDeletion(r)
		if ok || !strings.Contains(reason, "missing required query parameter") {
			t.Fatalf("expected missing query param error, got ok=%v reason=%q", ok, reason)
		}
	})
}

func TestValidatePrivateCLIVolumeRename(t *testing.T) {
	origSubmit := submitExpertModeVolumeRename
	defer func() { submitExpertModeVolumeRename = origSubmit }()

	const cacheKey = "unit-test-cache-key-priv-cli-rename"

	t.Run("WhenSuccess", func(t *testing.T) {
		submitExpertModeVolumeRename = func(ctx context.Context, req *coreapi.ExpertModeVolumeRenameV1, params coreapi.V1ExpertModeVolumeRenameParams, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/private/cli/volume/rename?vserver=vs1&volume=vol1", bytes.NewBufferString(`{"newname":"vol1_renamed"}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validatePrivateCLIVolumeRename(r)
		if !ok || reason != "" {
			t.Fatalf("expected success, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenMissingCacheKey", func(t *testing.T) {
		submitExpertModeVolumeRename = func(ctx context.Context, req *coreapi.ExpertModeVolumeRenameV1, params coreapi.V1ExpertModeVolumeRenameParams, jwt string, logger log.Logger) error {
			return nil
		}
		r := httptest.NewRequest(http.MethodPatch, "/api/private/cli/volume/rename?vserver=vs1&volume=vol1", bytes.NewBufferString(`{"newname":"vol1_renamed"}`))
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validatePrivateCLIVolumeRename(r)
		if ok || !strings.Contains(reason, "cache key not found") {
			t.Fatalf("expected cache key error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenMissingQueryParams", func(t *testing.T) {
		submitExpertModeVolumeRename = func(ctx context.Context, req *coreapi.ExpertModeVolumeRenameV1, params coreapi.V1ExpertModeVolumeRenameParams, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/private/cli/volume/rename", bytes.NewBufferString(`{"newname":"vol1_renamed"}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validatePrivateCLIVolumeRename(r)
		if ok || !strings.Contains(reason, "missing required query parameters") {
			t.Fatalf("expected missing query params error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenMissingNewname", func(t *testing.T) {
		submitExpertModeVolumeRename = func(ctx context.Context, req *coreapi.ExpertModeVolumeRenameV1, params coreapi.V1ExpertModeVolumeRenameParams, jwt string, logger log.Logger) error {
			return nil
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/private/cli/volume/rename?vserver=vs1&volume=vol1", bytes.NewBufferString(`{}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validatePrivateCLIVolumeRename(r)
		if ok || !strings.Contains(reason, "newname") {
			t.Fatalf("expected missing newname error, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("WhenSubmitFails", func(t *testing.T) {
		submitExpertModeVolumeRename = func(ctx context.Context, req *coreapi.ExpertModeVolumeRenameV1, params coreapi.V1ExpertModeVolumeRenameParams, jwt string, logger log.Logger) error {
			return errors.New("rename failed")
		}
		ctx := context.Background()
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{AccountName: "acc", PoolID: "pool"})
		ctx = context.WithValue(ctx, models.AuthDataKey, cacheKey)
		r := httptest.NewRequest(http.MethodPatch, "/api/private/cli/volume/rename?vserver=vs1&volume=vol1", bytes.NewBufferString(`{"newname":"vol1_renamed"}`))
		r = r.WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		ok, reason := _validatePrivateCLIVolumeRename(r)
		if ok || !strings.Contains(reason, "rename failed") {
			t.Fatalf("expected submit failure, got ok=%v reason=%q", ok, reason)
		}
	})
}

func TestIsCloneCreateRequest_NilBody(t *testing.T) {
	if isCloneCreateRequest(nil) {
		t.Fatal("isCloneCreateRequest(nil) = true; want false")
	}
}

func TestVolumePostCreateSizeFieldsCondition(t *testing.T) {
	t.Run("InvalidJSON_ReturnsParseError", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(`{invalid`))
		r.Header.Set("Content-Type", "application/json")
		ok, reason := volumePostCreateSizeFieldsCondition(r)
		if ok || reason == "" {
			t.Fatalf("expected parse failure, ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("CloneCreateWithTopLevelSize_Rejected", func(t *testing.T) {
		body := `{"name":"c1","clone":{"is_flexclone":true,"parent_volume":{"name":"p"}},"size":1}`
		r := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		ok, reason := volumePostCreateSizeFieldsCondition(r)
		if ok {
			t.Fatal("expected validation failure for clone with size")
		}
		if !strings.Contains(reason, "must not be provided for clone volume create") {
			t.Fatalf("reason %q", reason)
		}
	})
}
