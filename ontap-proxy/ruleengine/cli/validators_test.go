package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	coreapi "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/core-api"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func Test_validateAggregateShow(t *testing.T) {
	tests := []struct {
		name         string
		serviceLevel string
		wantAllowed  bool
		wantReason   string
	}{
		{
			name:         "EXTREME pool is allowed",
			serviceLevel: commonparams.ServiceLevelExtreme,
			wantAllowed:  true,
		},
		{
			name:         "PREMIUM pool is allowed",
			serviceLevel: commonparams.ServiceLevelPremium,
			wantAllowed:  true,
		},
		{
			name:         "FLEX pool is denied",
			serviceLevel: models.ServiceLevelFLEX,
			wantAllowed:  false,
			wantReason:   "Aggregate listing not allowed",
		},
		{
			name:         "STANDARD pool is allowed",
			serviceLevel: "STANDARD",
			wantAllowed:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cacheKey := "aggregate-show-" + tt.name
			ctx := context.WithValue(t.Context(), models.AuthDataKey, cacheKey)
			cache.AddToAuthDataCache(cacheKey, &models.AuthData{ServiceLevel: tt.serviceLevel})
			t.Cleanup(func() { cache.RemoveFromAuthDataCache(cacheKey) })

			allowed, reason := _validateAggregateShow(ctx, &CLICommand{FullCommand: "storage aggregate show"})

			if allowed != tt.wantAllowed {
				t.Fatalf("allowed = %v, want %v", allowed, tt.wantAllowed)
			}
			if reason != tt.wantReason {
				t.Fatalf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}

	t.Run("missing auth context is denied", func(t *testing.T) {
		allowed, reason := _validateAggregateShow(t.Context(), &CLICommand{FullCommand: "storage aggregate show"})
		if allowed {
			t.Fatal("expected command to be denied without auth context")
		}
		if reason != "cache key not found in context" {
			t.Fatalf("reason = %q, want %q", reason, "cache key not found in context")
		}
	})
}

func Test_validateVolumeCreate(t *testing.T) {
	t.Run("WhenCoreSucceeds_ShouldSetStyleFlexvol", func(t *testing.T) {
		origSubmit := submitExpertModeVolumeOperation
		defer func() { submitExpertModeVolumeOperation = origSubmit }()
		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return nil
		}
		cacheKey := "test-pool-key-create-style"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume create",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
				"-size":    "10g",
			},
		}
		allowed, reason := _validateVolumeCreate(ctx, cmd)
		if !allowed {
			t.Errorf("Expected allowed, got reason = %q", reason)
		}
		if capturedReq == nil {
			t.Fatal("Expected request to be sent")
		}
		if capturedReq.Style != coreapi.ExpertModeVolumeV1StyleFlexvol {
			t.Errorf("Style = %v, want Flexvol", capturedReq.Style)
		}
	})

	t.Run("WhenCacheKeyMissing_ShouldReturnNotAllowed", func(t *testing.T) {
		ctx := context.Background()
		cmd := &CLICommand{
			FullCommand: "volume create",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
				"-size":    "10g",
			},
		}

		allowed, reason := _validateVolumeCreate(ctx, cmd)

		if allowed {
			t.Error("Expected not allowed when cache key missing")
		}
		if reason == "" {
			t.Error("Expected non-empty reason")
		}
		if reason != "cache key not found in context" {
			t.Errorf("Reason = %q, want cache key not found", reason)
		}
	})

	t.Run("WhenAuthDataNotFoundInCache_ShouldReturnNotAllowed", func(t *testing.T) {
		wrongKey := "wrong-pool-key"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, wrongKey)
		cmd := &CLICommand{
			FullCommand: "volume create",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
				"-size":    "10g",
			},
		}

		allowed, reason := _validateVolumeCreate(ctx, cmd)

		if allowed {
			t.Error("Expected not allowed when auth data not in cache")
		}
		if reason == "" {
			t.Error("Expected non-empty reason")
		}
		wantPrefix := "auth data not found in cache for key:"
		if !strings.HasPrefix(reason, wantPrefix) {
			t.Errorf("Reason = %q, want prefix %q", reason, wantPrefix)
		}
	})

	t.Run("WhenInvalidSize_ShouldReturnNotAllowed", func(t *testing.T) {
		cacheKey := "test-pool-key"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume create",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
				"-size":    "invalid",
			},
		}
		allowed, reason := _validateVolumeCreate(ctx, cmd)
		if allowed {
			t.Error("Expected not allowed for invalid size")
		}
		if reason == "" {
			t.Error("Expected non-empty reason")
		}
		if reason != `"invalid" is an invalid value for argument "-size"` {
			t.Errorf("Reason = %q", reason)
		}
	})

	t.Run("WhenCoreSucceeds_ShouldReturnAllowed", func(t *testing.T) {
		origSubmit := submitExpertModeVolumeOperation
		defer func() { submitExpertModeVolumeOperation = origSubmit }()
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		cacheKey := "test-pool-key-create-success"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume create",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
				"-size":    "10g",
			},
		}
		allowed, reason := _validateVolumeCreate(ctx, cmd)
		if !allowed {
			t.Errorf("Expected allowed when core succeeds, got reason = %q", reason)
		}
		if reason != "" {
			t.Errorf("Expected empty reason on success, got %q", reason)
		}
	})

	t.Run("WhenCoreReturnsError_ShouldReturnNotAllowed", func(t *testing.T) {
		origSubmit := submitExpertModeVolumeOperation
		defer func() { submitExpertModeVolumeOperation = origSubmit }()
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return errors.New("core validation failed")
		}
		cacheKey := "test-pool-key-create-core-err"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume create",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
				"-size":    "10g",
			},
		}
		allowed, reason := _validateVolumeCreate(ctx, cmd)
		if allowed {
			t.Error("Expected not allowed when core returns error")
		}
		if reason != "core validation failed" {
			t.Errorf("Reason = %q, want core validation failed", reason)
		}
	})
}

func Test_validateVolumeDelete(t *testing.T) {
	t.Run("WhenCacheKeyMissing_ShouldReturnNotAllowed", func(t *testing.T) {
		ctx := context.Background()
		cmd := &CLICommand{
			FullCommand: "volume delete",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
			},
		}

		allowed, reason := _validateVolumeDelete(ctx, cmd)

		if allowed {
			t.Error("Expected not allowed when cache key missing")
		}
		if reason == "" {
			t.Error("Expected non-empty reason")
		}
		if reason != "cache key not found in context" {
			t.Errorf("Reason = %q, want cache key not found", reason)
		}
	})

	t.Run("WhenAuthDataNotFoundInCache_ShouldReturnNotAllowed", func(t *testing.T) {
		wrongKey := "wrong-pool-key-delete"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, wrongKey)
		cmd := &CLICommand{
			FullCommand: "volume delete",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
			},
		}

		allowed, reason := _validateVolumeDelete(ctx, cmd)

		if allowed {
			t.Error("Expected not allowed when auth data not in cache")
		}
		if reason == "" {
			t.Error("Expected non-empty reason")
		}
		wantPrefix := "auth data not found in cache for key:"
		if !strings.HasPrefix(reason, wantPrefix) {
			t.Errorf("Reason = %q, want prefix %q", reason, wantPrefix)
		}
	})

	t.Run("WhenCoreSucceeds_ShouldReturnAllowed", func(t *testing.T) {
		origSubmit := submitExpertModeVolumeOperation
		defer func() { submitExpertModeVolumeOperation = origSubmit }()
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return nil
		}
		cacheKey := "test-pool-key-delete-success"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume delete",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
			},
		}
		allowed, reason := _validateVolumeDelete(ctx, cmd)
		if !allowed {
			t.Errorf("Expected allowed when core succeeds, got reason = %q", reason)
		}
		if reason != "" {
			t.Errorf("Expected empty reason on success, got %q", reason)
		}
	})

	t.Run("WhenCoreReturnsError_ShouldReturnNotAllowed", func(t *testing.T) {
		origSubmit := submitExpertModeVolumeOperation
		defer func() { submitExpertModeVolumeOperation = origSubmit }()
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return errors.New("volume not found")
		}
		cacheKey := "test-pool-key-delete-core-err"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume delete",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
			},
		}
		allowed, reason := _validateVolumeDelete(ctx, cmd)
		if allowed {
			t.Error("Expected not allowed when core returns error")
		}
		if reason != "volume not found" {
			t.Errorf("Reason = %q, want volume not found", reason)
		}
	})
}

func Test_validateVolumeUpdate(t *testing.T) {
	t.Run("WhenSizeNotPresent_ShouldReturnAllowed", func(t *testing.T) {
		ctx := context.Background()
		cmd := &CLICommand{
			FullCommand: "volume modify",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
			},
		}

		allowed, reason := _validateVolumeUpdate(ctx, cmd)

		if !allowed {
			t.Errorf("Expected allowed when -size not present, got reason = %q", reason)
		}
		if reason != "" {
			t.Errorf("Expected empty reason when -size not present, got %q", reason)
		}
	})

	t.Run("WhenCacheKeyMissing_ShouldReturnNotAllowed", func(t *testing.T) {
		ctx := context.Background()
		cmd := &CLICommand{
			FullCommand: "volume modify",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
				"-size":    "10g",
			},
		}

		allowed, reason := _validateVolumeUpdate(ctx, cmd)

		if allowed {
			t.Error("Expected not allowed when cache key missing")
		}
		if reason == "" {
			t.Error("Expected non-empty reason")
		}
		if reason != "cache key not found in context" {
			t.Errorf("Reason = %q, want cache key not found", reason)
		}
	})

	t.Run("WhenAuthDataNotFoundInCache_ShouldReturnNotAllowed", func(t *testing.T) {
		wrongKey := "wrong-pool-key-update"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, wrongKey)
		cmd := &CLICommand{
			FullCommand: "volume modify",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
				"-size":    "10g",
			},
		}

		allowed, reason := _validateVolumeUpdate(ctx, cmd)

		if allowed {
			t.Error("Expected not allowed when auth data not in cache")
		}
		if reason == "" {
			t.Error("Expected non-empty reason")
		}
		wantPrefix := "auth data not found in cache for key:"
		if !strings.HasPrefix(reason, wantPrefix) {
			t.Errorf("Reason = %q, want prefix %q", reason, wantPrefix)
		}
	})

	t.Run("WhenInvalidSize_ShouldReturnNotAllowed", func(t *testing.T) {
		cacheKey := "test-pool-key-update-invalid-size"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume modify",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
				"-size":    "invalid",
			},
		}
		allowed, reason := _validateVolumeUpdate(ctx, cmd)
		if allowed {
			t.Error("Expected not allowed for invalid size")
		}
		if reason == "" {
			t.Error("Expected non-empty reason")
		}
		if reason != `"invalid" is an invalid value for argument "-size"` {
			t.Errorf("Reason = %q", reason)
		}
	})

	t.Run("WhenNewSizePlus10g_ShouldReturnNotAllowed", func(t *testing.T) {
		cacheKey := "test-pool-key-update-plus-size"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume size",
			Arguments: map[string]string{
				"-vserver":  "vs1",
				"-volume":   "vol1",
				"-new-size": "+10g",
			},
		}
		allowed, reason := _validateVolumeUpdate(ctx, cmd)
		if allowed {
			t.Error("Expected not allowed for -new-size +10g")
		}
		if reason != `"+10g" is an invalid value for argument "-new-size"` {
			t.Errorf("Reason = %q", reason)
		}
	})

	t.Run("WhenNewSizeMinus10g_ShouldReturnNotAllowed", func(t *testing.T) {
		cacheKey := "test-pool-key-update-minus-size"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume size",
			Arguments: map[string]string{
				"-vserver":  "vs1",
				"-volume":   "vol1",
				"-new-size": "-10g",
			},
		}
		allowed, reason := _validateVolumeUpdate(ctx, cmd)
		if allowed {
			t.Error("Expected not allowed for -new-size -10g")
		}
		if reason != `"-10g" is an invalid value for argument "-new-size"` {
			t.Errorf("Reason = %q", reason)
		}
	})

	t.Run("WhenCoreSucceeds_ShouldReturnAllowed", func(t *testing.T) {
		origSubmit := submitExpertModeVolumeOperation
		defer func() { submitExpertModeVolumeOperation = origSubmit }()
		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return nil
		}
		cacheKey := "test-pool-key-update-success"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume modify",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
				"-size":    "200g",
			},
		}
		allowed, reason := _validateVolumeUpdate(ctx, cmd)
		if !allowed {
			t.Errorf("Expected allowed when core succeeds, got reason = %q", reason)
		}
		if reason != "" {
			t.Errorf("Expected empty reason on success, got %q", reason)
		}
		if capturedReq == nil {
			t.Fatal("Expected request to be sent to core")
		}
		if capturedReq.Action != coreapi.ExpertModeVolumeV1ActionUpdate {
			t.Errorf("Action = %v, want Update", capturedReq.Action)
		}
		if capturedReq.VolumeName != "vol1" {
			t.Errorf("VolumeName = %q, want vol1", capturedReq.VolumeName)
		}
		if !capturedReq.SizeInBytes.IsSet() {
			t.Error("Expected SizeInBytes to be set")
		}
	})

	t.Run("WhenCoreReturnsError_ShouldReturnNotAllowed", func(t *testing.T) {
		origSubmit := submitExpertModeVolumeOperation
		defer func() { submitExpertModeVolumeOperation = origSubmit }()
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return errors.New("update conflict")
		}
		cacheKey := "test-pool-key-update-core-err"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume modify",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "vol1",
				"-size":    "10g",
			},
		}
		allowed, reason := _validateVolumeUpdate(ctx, cmd)
		if allowed {
			t.Error("Expected not allowed when core returns error")
		}
		if reason != "update conflict" {
			t.Errorf("Reason = %q, want update conflict", reason)
		}
	})
}

func Test_validateVolumeRename(t *testing.T) {
	origSubmit := submitExpertModeVolumeRename
	defer func() { submitExpertModeVolumeRename = origSubmit }()

	t.Run("WhenCacheKeyMissing_ShouldReturnNotAllowed", func(t *testing.T) {
		ctx := context.Background()
		cmd := &CLICommand{
			FullCommand: "vol rename",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "reconcile004",
				"-newname": "reconcile_update004",
			},
		}

		allowed, reason := _validateVolumeRename(ctx, cmd)

		if allowed {
			t.Error("Expected not allowed when cache key missing")
		}
		if reason == "" {
			t.Error("Expected non-empty reason")
		}
		if reason != "cache key not found in context" {
			t.Errorf("Reason = %q, want cache key not found", reason)
		}
	})

	t.Run("WhenAuthDataNotFoundInCache_ShouldReturnNotAllowed", func(t *testing.T) {
		wrongKey := "wrong-pool-key-rename"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, wrongKey)
		cmd := &CLICommand{
			FullCommand: "vol rename",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "reconcile004",
				"-newname": "reconcile_update004",
			},
		}

		allowed, reason := _validateVolumeRename(ctx, cmd)

		if allowed {
			t.Error("Expected not allowed when auth data not in cache")
		}
		if reason == "" {
			t.Error("Expected non-empty reason")
		}
		wantPrefix := "auth data not found in cache for key:"
		if !strings.HasPrefix(reason, wantPrefix) {
			t.Errorf("Reason = %q, want prefix %q", reason, wantPrefix)
		}
	})

	t.Run("WhenCoreSucceeds_ShouldReturnAllowed", func(t *testing.T) {
		var capturedReq *coreapi.ExpertModeVolumeRenameV1
		var capturedParams coreapi.V1ExpertModeVolumeRenameParams
		submitExpertModeVolumeRename = func(ctx context.Context, req *coreapi.ExpertModeVolumeRenameV1, params coreapi.V1ExpertModeVolumeRenameParams, jwt string, logger log.Logger) error {
			capturedReq = req
			capturedParams = params
			return nil
		}
		cacheKey := "test-pool-key-rename-success"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "vol rename",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "reconcile004",
				"-newname": "reconcile_update004",
			},
		}
		allowed, reason := _validateVolumeRename(ctx, cmd)
		if !allowed {
			t.Errorf("Expected allowed when core succeeds, got reason = %q", reason)
		}
		if reason != "" {
			t.Errorf("Expected empty reason on success, got %q", reason)
		}
		if capturedReq == nil {
			t.Fatal("Expected rename request to be sent to core")
		}
		if capturedReq.Name != "reconcile_update004" {
			t.Errorf("Request.Name = %q, want reconcile_update004", capturedReq.Name)
		}
		if capturedReq.ProjectNumber != "test-account" || capturedReq.PoolUUID != "pool-uuid" || capturedReq.SvmName != "vs1" {
			t.Errorf("Request context: project=%q pool=%q svm=%q", capturedReq.ProjectNumber, capturedReq.PoolUUID, capturedReq.SvmName)
		}
		if capturedParams.Name != "reconcile004" {
			t.Errorf("Params.Name (current volume) = %q, want reconcile004", capturedParams.Name)
		}
	})

	t.Run("WhenCoreReturnsError_ShouldReturnNotAllowed", func(t *testing.T) {
		submitExpertModeVolumeRename = func(ctx context.Context, req *coreapi.ExpertModeVolumeRenameV1, params coreapi.V1ExpertModeVolumeRenameParams, jwt string, logger log.Logger) error {
			return errors.New("volume not found")
		}
		cacheKey := "test-pool-key-rename-core-err"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "vol rename",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "reconcile004",
				"-newname": "reconcile_update004",
			},
		}
		allowed, reason := _validateVolumeRename(ctx, cmd)
		if allowed {
			t.Error("Expected not allowed when core returns error")
		}
		if reason != "volume not found" {
			t.Errorf("Reason = %q, want volume not found", reason)
		}
	})
}

func Test_validateFlexCacheCreate(t *testing.T) {
	t.Run("WhenCoreSucceeds_ShouldSetStyleFlexcache", func(t *testing.T) {
		origSubmit := submitExpertModeVolumeOperation
		defer func() { submitExpertModeVolumeOperation = origSubmit }()
		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return nil
		}
		cacheKey := "test-pool-key-fc-create-success"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume flexcache create",
			Arguments: map[string]string{
				"-vserver":       "vs1",
				"-volume":        "fc1",
				"-origin-volume": "orig1",
				"-size":          "400m",
			},
		}
		allowed, reason := _validateFlexCacheCreate(ctx, cmd)
		if !allowed {
			t.Errorf("Expected allowed, got reason = %q", reason)
		}
		if capturedReq == nil {
			t.Fatal("Expected request to be sent")
		}
		if capturedReq.Style != coreapi.ExpertModeVolumeV1StyleFlexcache {
			t.Errorf("Style = %v, want Flexcache", capturedReq.Style)
		}
		if capturedReq.Action != coreapi.ExpertModeVolumeV1ActionCreate {
			t.Errorf("Action = %v, want Create", capturedReq.Action)
		}
		if capturedReq.VolumeName != "fc1" {
			t.Errorf("VolumeName = %q, want fc1", capturedReq.VolumeName)
		}
	})

	t.Run("WhenCacheKeyMissing_ShouldReturnNotAllowed", func(t *testing.T) {
		ctx := context.Background()
		cmd := &CLICommand{
			FullCommand: "volume flexcache create",
			Arguments: map[string]string{
				"-vserver":       "vs1",
				"-volume":        "fc1",
				"-origin-volume": "orig1",
				"-size":          "400m",
			},
		}

		allowed, reason := _validateFlexCacheCreate(ctx, cmd)
		if allowed {
			t.Error("Expected not allowed when cache key missing")
		}
		if reason != "cache key not found in context" {
			t.Errorf("Reason = %q", reason)
		}
	})

	t.Run("WhenInvalidSize_ShouldReturnNotAllowed", func(t *testing.T) {
		cacheKey := "test-pool-key-fc-create-invalid-size"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume flexcache create",
			Arguments: map[string]string{
				"-vserver":       "vs1",
				"-volume":        "fc1",
				"-origin-volume": "orig1",
				"-size":          "invalid",
			},
		}
		allowed, reason := _validateFlexCacheCreate(ctx, cmd)
		if allowed {
			t.Error("Expected not allowed for invalid size")
		}
		if reason != `"invalid" is an invalid value for argument "-size"` {
			t.Errorf("Reason = %q", reason)
		}
	})

	t.Run("WhenCoreReturnsError_ShouldReturnNotAllowed", func(t *testing.T) {
		origSubmit := submitExpertModeVolumeOperation
		defer func() { submitExpertModeVolumeOperation = origSubmit }()
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return errors.New("flexcache validation failed")
		}
		cacheKey := "test-pool-key-fc-create-core-err"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume flexcache create",
			Arguments: map[string]string{
				"-vserver":       "vs1",
				"-volume":        "fc1",
				"-origin-volume": "orig1",
				"-size":          "400m",
			},
		}
		allowed, reason := _validateFlexCacheCreate(ctx, cmd)
		if allowed {
			t.Error("Expected not allowed when core returns error")
		}
		if reason != "flexcache validation failed" {
			t.Errorf("Reason = %q, want flexcache validation failed", reason)
		}
	})
}

func Test_validateFlexCacheDelete(t *testing.T) {
	t.Run("WhenCoreSucceeds_ShouldSetStyleFlexcache", func(t *testing.T) {
		origSubmit := submitExpertModeVolumeOperation
		defer func() { submitExpertModeVolumeOperation = origSubmit }()
		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return nil
		}
		cacheKey := "test-pool-key-fc-delete-success"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume flexcache delete",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "fc1",
			},
		}
		allowed, reason := _validateFlexCacheDelete(ctx, cmd)
		if !allowed {
			t.Errorf("Expected allowed, got reason = %q", reason)
		}
		if capturedReq == nil {
			t.Fatal("Expected request to be sent")
		}
		if capturedReq.Style != coreapi.ExpertModeVolumeV1StyleFlexcache {
			t.Errorf("Style = %v, want Flexcache", capturedReq.Style)
		}
		if capturedReq.Action != coreapi.ExpertModeVolumeV1ActionDelete {
			t.Errorf("Action = %v, want Delete", capturedReq.Action)
		}
		if capturedReq.VolumeName != "fc1" {
			t.Errorf("VolumeName = %q, want fc1", capturedReq.VolumeName)
		}
	})

	t.Run("WhenCacheKeyMissing_ShouldReturnNotAllowed", func(t *testing.T) {
		ctx := context.Background()
		cmd := &CLICommand{
			FullCommand: "volume flexcache delete",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "fc1",
			},
		}

		allowed, reason := _validateFlexCacheDelete(ctx, cmd)
		if allowed {
			t.Error("Expected not allowed when cache key missing")
		}
		if reason != "cache key not found in context" {
			t.Errorf("Reason = %q", reason)
		}
	})

	t.Run("WhenCoreReturnsError_ShouldReturnNotAllowed", func(t *testing.T) {
		origSubmit := submitExpertModeVolumeOperation
		defer func() { submitExpertModeVolumeOperation = origSubmit }()
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			return errors.New("flexcache not found")
		}
		cacheKey := "test-pool-key-fc-delete-core-err"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume flexcache delete",
			Arguments: map[string]string{
				"-vserver": "vs1",
				"-volume":  "fc1",
			},
		}
		allowed, reason := _validateFlexCacheDelete(ctx, cmd)
		if allowed {
			t.Error("Expected not allowed when core returns error")
		}
		if reason != "flexcache not found" {
			t.Errorf("Reason = %q, want flexcache not found", reason)
		}
	})
}

func Test_validateVolumeCloneCreate(t *testing.T) {
	t.Run("WhenCoreSucceedsWithParentVolumeAndSize_ShouldSubmitCloneCreate", func(t *testing.T) {
		origSubmit := submitExpertModeVolumeOperation
		defer func() { submitExpertModeVolumeOperation = origSubmit }()

		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return nil
		}

		cacheKey := "test-pool-key-clone-create-success"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume clone create",
			Arguments: map[string]string{
				"-vserver":         "vs1",
				"-flexclone":       "clone1",
				"-parent-volume":   "src1",
				"-parent-snapshot": "snap1",
				"-size":            "10g",
			},
		}

		allowed, reason := _validateVolumeCloneCreate(ctx, cmd)
		if !allowed {
			t.Fatalf("Expected allowed, got reason %q", reason)
		}
		if capturedReq == nil {
			t.Fatal("Expected request to be submitted")
		}
		if capturedReq.Action != coreapi.ExpertModeVolumeV1ActionCreate {
			t.Errorf("Action = %v, want Create", capturedReq.Action)
		}
		if capturedReq.VolumeName != "clone1" {
			t.Errorf("VolumeName = %q, want clone1", capturedReq.VolumeName)
		}
		if !capturedReq.Clone.IsSet() {
			t.Fatal("Expected Clone to be set")
		}
		clone := capturedReq.Clone.Value
		if !clone.IsFlexclone.IsSet() || !clone.IsFlexclone.Value {
			t.Errorf("IsFlexclone = %v, want true", clone.IsFlexclone)
		}
		if !clone.ParentVolume.IsSet() || !clone.ParentVolume.Value.Name.IsSet() || clone.ParentVolume.Value.Name.Value != "src1" {
			t.Errorf("ParentVolume.Name = %v, want src1", clone.ParentVolume)
		}
		if !clone.ParentSnapshot.IsSet() || !clone.ParentSnapshot.Value.Name.IsSet() || clone.ParentSnapshot.Value.Name.Value != "snap1" {
			t.Errorf("ParentSnapshot.Name = %v, want snap1", clone.ParentSnapshot)
		}
		if !capturedReq.SizeInBytes.IsSet() {
			t.Error("Expected SizeInBytes to be set when -size is provided")
		}
	})

	t.Run("WhenUsingParentVolumeAliasBAndNoSize_ShouldOmitSizeInRequest", func(t *testing.T) {
		origSubmit := submitExpertModeVolumeOperation
		defer func() { submitExpertModeVolumeOperation = origSubmit }()

		var capturedReq *coreapi.ExpertModeVolumeV1
		submitExpertModeVolumeOperation = func(ctx context.Context, req *coreapi.ExpertModeVolumeV1, jwt string, logger log.Logger) error {
			capturedReq = req
			return nil
		}

		cacheKey := "test-pool-key-clone-create-alias"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "vol clone create",
			Arguments: map[string]string{
				"-vserver":   "vs1",
				"-flexclone": "clone1",
				"-b":         "src1",
			},
		}

		allowed, reason := _validateVolumeCloneCreate(ctx, cmd)
		if !allowed {
			t.Fatalf("Expected allowed, got reason %q", reason)
		}
		if capturedReq == nil {
			t.Fatal("Expected request to be submitted")
		}
		if capturedReq.SizeInBytes.IsSet() {
			t.Error("Expected SizeInBytes to be omitted when -size is absent")
		}
		if !capturedReq.Clone.IsSet() || !capturedReq.Clone.Value.ParentVolume.IsSet() {
			t.Fatal("Expected clone parent volume to be set")
		}
		if capturedReq.Clone.Value.ParentVolume.Value.Name.Value != "src1" {
			t.Errorf("ParentVolume.Name = %q, want src1", capturedReq.Clone.Value.ParentVolume.Value.Name.Value)
		}
	})

	t.Run("WhenInvalidSize_ShouldReturnNotAllowed", func(t *testing.T) {
		cacheKey := "test-pool-key-clone-create-invalid-size"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume clone create",
			Arguments: map[string]string{
				"-vserver":       "vs1",
				"-flexclone":     "clone1",
				"-parent-volume": "src1",
				"-size":          "bad-size",
			},
		}

		allowed, reason := _validateVolumeCloneCreate(ctx, cmd)
		if allowed {
			t.Fatal("Expected not allowed for invalid size")
		}
		if reason != `"bad-size" is an invalid value for argument "-size"` {
			t.Errorf("Reason = %q", reason)
		}
	})
}

func Test_validateVolumeCloneSplitStart(t *testing.T) {
	t.Run("WhenCoreSucceeds_ShouldSubmitFlexCloneSplitWithCloneName", func(t *testing.T) {
		origSubmit := submitExpertModeFlexCloneSplit
		defer func() {
			submitExpertModeFlexCloneSplit = origSubmit
		}()

		var capturedVolumeUUID, capturedVolumeName, capturedProject, capturedPool string
		submitExpertModeFlexCloneSplit = func(ctx context.Context, volumeUUID, volumeName, projectNumber, poolUUID, jwt string, logger log.Logger) error {
			capturedVolumeUUID = volumeUUID
			capturedVolumeName = volumeName
			capturedProject = projectNumber
			capturedPool = poolUUID
			return nil
		}

		cacheKey := "test-pool-key-clone-split-success"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume clone split start",
			Arguments: map[string]string{
				"-vserver":   "vs1",
				"-flexclone": "clone1",
			},
		}

		allowed, reason := _validateVolumeCloneSplitStart(ctx, cmd)
		if !allowed {
			t.Fatalf("Expected allowed, got reason %q", reason)
		}
		if capturedVolumeUUID != "" {
			t.Errorf("volume UUID = %q, want empty", capturedVolumeUUID)
		}
		if capturedVolumeName != "clone1" {
			t.Errorf("volume name = %q, want clone1", capturedVolumeName)
		}
		if capturedProject != "test-account" || capturedPool != "pool-uuid" {
			t.Errorf("project/pool = %q/%q, want test-account/pool-uuid", capturedProject, capturedPool)
		}
	})

	t.Run("WhenCoreReturnsError_ShouldReturnNotAllowed", func(t *testing.T) {
		origSubmit := submitExpertModeFlexCloneSplit
		defer func() { submitExpertModeFlexCloneSplit = origSubmit }()
		submitExpertModeFlexCloneSplit = func(ctx context.Context, volumeUUID, volumeName, projectNumber, poolUUID, jwt string, logger log.Logger) error {
			return errors.New("volume is not a FlexClone")
		}

		cacheKey := "test-pool-key-clone-split-not-found"
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
		})
		defer cache.RemoveFromAuthDataCache(cacheKey)

		cmd := &CLICommand{
			FullCommand: "volume clone split start",
			Arguments: map[string]string{
				"-vserver":   "vs1",
				"-flexclone": "clone1",
			},
		}

		allowed, reason := _validateVolumeCloneSplitStart(ctx, cmd)
		if allowed {
			t.Fatal("Expected not allowed when core returns split error")
		}
		if !strings.Contains(reason, "volume is not a FlexClone") {
			t.Errorf("Reason = %q", reason)
		}
	})
}
