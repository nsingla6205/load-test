package api

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// mockParseAndValidateRegionAndZone replaces the package-level parseAndValidateRegionAndZone
// for the duration of a test, restoring it on cleanup.
func mockParseAndValidateRegionAndZone(t *testing.T, region, zone string) {
	t.Helper()
	original := parseAndValidateRegionAndZone
	t.Cleanup(func() { parseAndValidateRegionAndZone = original })
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return region, zone, nil
	}
}

func TestV1UpdateVolumeDatabaseValue_SuccessStateAndIsDataProtection(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{
		State: oasgenserver.NewOptString("rw"),
	}
	req.VolumeAttributes.SetTo(oasgenserver.UpdateVolumeDatabaseValueVolumeAttributesV1{
		IsDataProtection: oasgenserver.NewOptBool(false),
	})

	mockOrch.EXPECT().
		UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.MatchedBy(func(patch *commonparams.UpdateVolumeDatabaseValueParams) bool {
			return patch != nil &&
				patch.State != nil && *patch.State == "rw" &&
				patch.StateDetails == nil &&
				!patch.DeletedAtSet &&
				patch.VolumeAttributes != nil &&
				patch.VolumeAttributes.IsDataProtection != nil && *patch.VolumeAttributes.IsDataProtection == false
		})).
		Return(&coremodels.Volume{
			BaseModel:             coremodels.BaseModel{UUID: volumeID},
			LifeCycleState:        "rw",
			LifeCycleStateDetails: "",
			IsDataProtection:      false,
		}, nil)

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	resp, ok := result.(*oasgenserver.UpdateVolumeDatabaseValueResponseV1)
	assert.True(t, ok, "expected UpdateVolumeDatabaseValueResponseV1")
	assert.Equal(t, volumeID, resp.VolumeId.Or(""))
	assert.Equal(t, "rw", resp.State.Or(""))
	assert.Equal(t, false, resp.IsDataProtection.Or(true))
}

func TestV1UpdateVolumeDatabaseValue_PartialFieldsOnly(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	// Only isDataProtection=true is provided; state/stateDetails/deletedAt and every other
	// volumeAttributes field must remain nil (i.e. "leave unchanged").
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{}
	req.VolumeAttributes.SetTo(oasgenserver.UpdateVolumeDatabaseValueVolumeAttributesV1{
		IsDataProtection: oasgenserver.NewOptBool(true),
	})

	mockOrch.EXPECT().
		UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.MatchedBy(func(patch *commonparams.UpdateVolumeDatabaseValueParams) bool {
			return patch != nil &&
				patch.State == nil &&
				patch.StateDetails == nil &&
				!patch.DeletedAtSet &&
				patch.VolumeAttributes != nil &&
				patch.VolumeAttributes.IsDataProtection != nil && *patch.VolumeAttributes.IsDataProtection == true
		})).
		Return(&coremodels.Volume{
			BaseModel:        coremodels.BaseModel{UUID: volumeID},
			IsDataProtection: true,
		}, nil)

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	resp, ok := result.(*oasgenserver.UpdateVolumeDatabaseValueResponseV1)
	assert.True(t, ok, "expected UpdateVolumeDatabaseValueResponseV1")
	assert.Equal(t, true, resp.IsDataProtection.Or(false))
}

func TestV1UpdateVolumeDatabaseValue_NilBody(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), nil, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: "f943038a-c2a1-4d96-8115-281ea02bd22d"})
	assert.NoError(t, err)

	_, ok := result.(*oasgenserver.V1UpdateVolumeDatabaseValueBadRequest)
	assert.True(t, ok, "expected V1UpdateVolumeDatabaseValueBadRequest")
}

func TestV1UpdateVolumeDatabaseValue_BadRequest(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{
		State: oasgenserver.NewOptString("dp"),
	}

	mockOrch.EXPECT().
		UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.Anything).
		Return(nil, errors.NewBadRequestErr("invalid state transition"))

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	_, ok := result.(*oasgenserver.V1UpdateVolumeDatabaseValueBadRequest)
	assert.True(t, ok, "expected V1UpdateVolumeDatabaseValueBadRequest")
}

func TestV1UpdateVolumeDatabaseValue_NotFound(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{
		State: oasgenserver.NewOptString("2ndr"),
	}

	mockOrch.EXPECT().
		UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.Anything).
		Return(nil, errors.NewNotFoundErr("volume", nil))

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	_, ok := result.(*oasgenserver.V1UpdateVolumeDatabaseValueNotFound)
	assert.True(t, ok, "expected V1UpdateVolumeDatabaseValueNotFound")
}

func TestV1UpdateVolumeDatabaseValue_StateDetailsOnly(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{
		StateDetails: oasgenserver.NewOptString("break failed: snapmirror busy"),
	}

	mockOrch.EXPECT().
		UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.MatchedBy(func(patch *commonparams.UpdateVolumeDatabaseValueParams) bool {
			return patch != nil &&
				patch.State == nil &&
				patch.StateDetails != nil && *patch.StateDetails == "break failed: snapmirror busy" &&
				!patch.DeletedAtSet &&
				patch.VolumeAttributes == nil
		})).
		Return(&coremodels.Volume{
			BaseModel:             coremodels.BaseModel{UUID: volumeID},
			LifeCycleStateDetails: "break failed: snapmirror busy",
		}, nil)

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	resp, ok := result.(*oasgenserver.UpdateVolumeDatabaseValueResponseV1)
	assert.True(t, ok, "expected UpdateVolumeDatabaseValueResponseV1")
	assert.Equal(t, "break failed: snapmirror busy", resp.StateDetails.Or(""))
}

func TestV1UpdateVolumeDatabaseValue_DeletedAtExplicitValueSoftDeletes(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	deletedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{}
	req.DeletedAt.SetTo(deletedAt)

	mockOrch.EXPECT().
		UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.MatchedBy(func(patch *commonparams.UpdateVolumeDatabaseValueParams) bool {
			return patch != nil && patch.DeletedAtSet && patch.DeletedAt != nil && patch.DeletedAt.Equal(deletedAt)
		})).
		Return(&coremodels.Volume{
			BaseModel: coremodels.BaseModel{UUID: volumeID, DeletedAt: &deletedAt},
		}, nil)

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	resp, ok := result.(*oasgenserver.UpdateVolumeDatabaseValueResponseV1)
	assert.True(t, ok, "expected UpdateVolumeDatabaseValueResponseV1")
	assert.True(t, resp.DeletedAt.Set, "expected DeletedAt to be set in the response")
	assert.False(t, resp.DeletedAt.Null)
	assert.True(t, deletedAt.Equal(resp.DeletedAt.Value))
}

func TestV1UpdateVolumeDatabaseValue_ConflictErr(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{
		State: oasgenserver.NewOptString("rw"),
	}

	mockOrch.EXPECT().
		UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.Anything).
		Return(nil, errors.NewConflictErr("volume is busy with another operation"))

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	conflict, ok := result.(*oasgenserver.V1UpdateVolumeDatabaseValueConflict)
	assert.True(t, ok, "expected V1UpdateVolumeDatabaseValueConflict")
	assert.Equal(t, float64(409), conflict.Code)
}

func TestV1UpdateVolumeDatabaseValue_CustomErr_400(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{
		State: oasgenserver.NewOptString("rw"),
	}

	httpCode := 400
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrInputValidationError,
		Message:     "custom bad request",
		HttpCode:    &httpCode,
		OriginalErr: stderrors.New("custom bad request"),
	}

	mockOrch.EXPECT().UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.Anything).Return(nil, customErr)

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	badReq, ok := result.(*oasgenserver.V1UpdateVolumeDatabaseValueBadRequest)
	assert.True(t, ok, "expected V1UpdateVolumeDatabaseValueBadRequest for custom 400 error")
	assert.Equal(t, float64(400), badReq.Code)
	assert.Equal(t, "custom bad request", badReq.Message)
}

func TestV1UpdateVolumeDatabaseValue_CustomErr_404(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{
		State: oasgenserver.NewOptString("rw"),
	}

	httpCode := 404
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrResourceNotFound,
		Message:     "custom not found",
		HttpCode:    &httpCode,
		OriginalErr: stderrors.New("custom not found"),
	}

	mockOrch.EXPECT().UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.Anything).Return(nil, customErr)

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	notFound, ok := result.(*oasgenserver.V1UpdateVolumeDatabaseValueNotFound)
	assert.True(t, ok, "expected V1UpdateVolumeDatabaseValueNotFound for custom 404 error")
	assert.Equal(t, float64(404), notFound.Code)
	assert.Equal(t, "custom not found", notFound.Message)
}

func TestV1UpdateVolumeDatabaseValue_CustomErr_409(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{
		State: oasgenserver.NewOptString("rw"),
	}

	httpCode := 409
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrResourceStateConflictError,
		Message:     "custom conflict",
		HttpCode:    &httpCode,
		OriginalErr: stderrors.New("custom conflict"),
	}

	mockOrch.EXPECT().UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.Anything).Return(nil, customErr)

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	conflict, ok := result.(*oasgenserver.V1UpdateVolumeDatabaseValueConflict)
	assert.True(t, ok, "expected V1UpdateVolumeDatabaseValueConflict for custom 409 error")
	assert.Equal(t, float64(409), conflict.Code)
	assert.Equal(t, "custom conflict", conflict.Message)
}

func TestV1UpdateVolumeDatabaseValue_CustomErr_OtherHttpCode(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{
		State: oasgenserver.NewOptString("rw"),
	}

	httpCode := 503
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrInternalServerError,
		Message:     "service unavailable",
		HttpCode:    &httpCode,
		OriginalErr: stderrors.New("service unavailable"),
	}

	mockOrch.EXPECT().UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.Anything).Return(nil, customErr)

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	serverErr, ok := result.(*oasgenserver.V1UpdateVolumeDatabaseValueInternalServerError)
	assert.True(t, ok, "expected InternalServerError for unrecognized custom http code")
	assert.Equal(t, float64(500), serverErr.Code)
}

func TestV1UpdateVolumeDatabaseValue_CustomErr_NoHttpCode(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{
		State: oasgenserver.NewOptString("rw"),
	}

	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrInternalServerError,
		Message:     "custom error without http code",
		OriginalErr: stderrors.New("custom error without http code"),
	}

	mockOrch.EXPECT().UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.Anything).Return(nil, customErr)

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	serverErr, ok := result.(*oasgenserver.V1UpdateVolumeDatabaseValueInternalServerError)
	assert.True(t, ok, "expected InternalServerError for custom error without http code")
	assert.Equal(t, float64(500), serverErr.Code)
}

func TestV1UpdateVolumeDatabaseValue_InternalServerError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{
		State: oasgenserver.NewOptString("rw"),
	}

	mockOrch.EXPECT().
		UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.Anything).
		Return(nil, stderrors.New("internal error"))

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	serverErr, ok := result.(*oasgenserver.V1UpdateVolumeDatabaseValueInternalServerError)
	assert.True(t, ok, "expected InternalServerError for plain stdlib error")
	assert.Equal(t, float64(500), serverErr.Code)
}

func TestV1UpdateVolumeDatabaseValue_DeletedAtExplicitNullUndeletes(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	volumeID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	req := &oasgenserver.UpdateVolumeDatabaseValueRequestV1{}
	req.DeletedAt.SetToNull()

	mockOrch.EXPECT().
		UpdateVolumeDatabaseValue(mock.Anything, volumeID, mock.MatchedBy(func(patch *commonparams.UpdateVolumeDatabaseValueParams) bool {
			return patch != nil && patch.DeletedAtSet && patch.DeletedAt == nil
		})).
		Return(&coremodels.Volume{BaseModel: coremodels.BaseModel{UUID: volumeID}}, nil)

	result, err := handler.V1UpdateVolumeDatabaseValue(context.Background(), req, oasgenserver.V1UpdateVolumeDatabaseValueParams{VolumeId: volumeID})
	assert.NoError(t, err)

	_, ok := result.(*oasgenserver.UpdateVolumeDatabaseValueResponseV1)
	assert.True(t, ok, "expected UpdateVolumeDatabaseValueResponseV1")
}

func TestV1SplitStartVolume_InvalidLocation(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "invalid-location-no-region",
		VolumeId:      "test-volume-uuid",
	}

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	badReq, ok := result.(*oasgenserver.V1SplitStartVolumeBadRequest)
	assert.True(t, ok, "expected BadRequest response")
	assert.NotNil(t, badReq)
}

func TestV1SplitStartVolume_NotFound(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "non-existent-volume",
	}

	mockOrch.EXPECT().SplitStartVolume(mock.Anything, mock.MatchedBy(func(p *commonparams.SplitStartVolumeParams) bool {
		return p.VolumeID == "non-existent-volume"
	})).Return(nil, "", errors.NewNotFoundErr("volume", nil))

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	notFound, ok := result.(*oasgenserver.V1SplitStartVolumeNotFound)
	assert.True(t, ok, "expected NotFound response")
	assert.NotNil(t, notFound)
	assert.Equal(t, float64(404), notFound.Code)
}

func TestV1SplitStartVolume_UserInputValidationErr(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	mockOrch.EXPECT().SplitStartVolume(mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("volume is not a thin clone"))

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	badReq, ok := result.(*oasgenserver.V1SplitStartVolumeBadRequest)
	assert.True(t, ok, "expected BadRequest response")
	assert.NotNil(t, badReq)
	assert.Equal(t, float64(400), badReq.Code)
}

func TestV1SplitStartVolume_BadRequestErr(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	mockOrch.EXPECT().SplitStartVolume(mock.Anything, mock.Anything).Return(nil, "", errors.NewBadRequestErr("bad request"))

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	badReq, ok := result.(*oasgenserver.V1SplitStartVolumeBadRequest)
	assert.True(t, ok, "expected BadRequest response")
	assert.NotNil(t, badReq)
	assert.Equal(t, float64(400), badReq.Code)
}

func TestV1SplitStartVolume_ConflictErr(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	mockOrch.EXPECT().SplitStartVolume(mock.Anything, mock.Anything).Return(nil, "", errors.NewConflictErr("split already in progress"))

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	conflict, ok := result.(*oasgenserver.V1SplitStartVolumeConflict)
	assert.True(t, ok, "expected Conflict response")
	assert.NotNil(t, conflict)
	assert.Equal(t, float64(409), conflict.Code)
}

func TestV1SplitStartVolume_CustomErr_400(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	httpCode := 400
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrInputValidationError,
		Message:     "custom bad request",
		HttpCode:    &httpCode,
		OriginalErr: stderrors.New("custom bad request"),
	}

	mockOrch.EXPECT().SplitStartVolume(mock.Anything, mock.Anything).Return(nil, "", customErr)

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	badReq, ok := result.(*oasgenserver.V1SplitStartVolumeBadRequest)
	assert.True(t, ok, "expected BadRequest response for custom 400 error")
	assert.NotNil(t, badReq)
	assert.Equal(t, float64(400), badReq.Code)
}

func TestV1SplitStartVolume_CustomErr_409(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	httpCode := 409
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrResourceStateConflictError,
		Message:     "custom conflict",
		HttpCode:    &httpCode,
		OriginalErr: stderrors.New("custom conflict"),
	}

	mockOrch.EXPECT().SplitStartVolume(mock.Anything, mock.Anything).Return(nil, "", customErr)

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	conflict, ok := result.(*oasgenserver.V1SplitStartVolumeConflict)
	assert.True(t, ok, "expected Conflict response for custom 409 error")
	assert.NotNil(t, conflict)
	assert.Equal(t, float64(409), conflict.Code)
}

func TestV1SplitStartVolume_InternalServerError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	mockOrch.EXPECT().SplitStartVolume(mock.Anything, mock.Anything).Return(nil, "", stderrors.New("internal error"))

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	serverErr, ok := result.(*oasgenserver.V1SplitStartVolumeInternalServerError)
	assert.True(t, ok, "expected InternalServerError response")
	assert.NotNil(t, serverErr)
	assert.Equal(t, float64(500), serverErr.Code)
}

func TestV1SplitStartVolume_Success_Creating(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	volume := &coremodels.Volume{
		BaseModel:      coremodels.BaseModel{UUID: "test-volume-uuid"},
		LifeCycleState: datamodel.LifeCycleStateCreating,
		DisplayName:    "test-volume",
		CreationToken:  "test-token",
		PoolID:         "test-pool-uuid",
		QuotaInBytes:   1073741824,
		ProtocolTypes:  []string{"NFSv3"},
	}

	mockOrch.EXPECT().SplitStartVolume(mock.Anything, mock.Anything).Return(volume, "job-uuid", nil)

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	op, ok := result.(*oasgenserver.OperationV1)
	assert.True(t, ok, "expected OperationV1 response")
	assert.NotNil(t, op)
	assert.Equal(t, false, op.Done.Or(true))
}

func TestV1SplitStartVolume_Success_Ready(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "us-east4-a")

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4-a",
		VolumeId:      "test-volume-uuid",
	}

	volume := &coremodels.Volume{
		BaseModel:      coremodels.BaseModel{UUID: "test-volume-uuid"},
		LifeCycleState: datamodel.LifeCycleStateREADY,
		DisplayName:    "test-volume",
		CreationToken:  "test-token",
		PoolID:         "test-pool-uuid",
		QuotaInBytes:   1073741824,
		ProtocolTypes:  []string{"NFSv3"},
	}

	mockOrch.EXPECT().SplitStartVolume(mock.Anything, mock.Anything).Return(volume, "job-uuid", nil)

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	op, ok := result.(*oasgenserver.OperationV1)
	assert.True(t, ok, "expected OperationV1 response")
	assert.NotNil(t, op)
	assert.Equal(t, true, op.Done.Or(false))
}

func TestConvertModelToVolumeResponse_Nil(t *testing.T) {
	result := convertModelToVolumeResponse(nil)
	assert.Nil(t, result)
}

func TestConvertModelToVolumeResponse_WithSnapshotPolicy(t *testing.T) {
	vol := &coremodels.Volume{
		BaseModel:      coremodels.BaseModel{UUID: "vol-uuid"},
		LifeCycleState: datamodel.LifeCycleStateREADY,
		SnapshotPolicy: &coremodels.SnapshotPolicy{
			IsEnabled: true,
			Schedules: []*coremodels.SnapshotPolicySchedule{
				{
					Count: 5,
					Schedule: &coremodels.Schedule{
						Minutes:     []int{30},
						Hours:       []int{2},
						DaysOfMonth: []int{1, 15},
					},
				},
				{
					Count: 4,
					Schedule: &coremodels.Schedule{
						Minutes:    []int{0},
						Hours:      []int{6},
						DaysOfWeek: []int{0, 6},
					},
				},
				{
					Count: 3,
					Schedule: &coremodels.Schedule{
						Minutes: []int{15},
						Hours:   []int{8},
					},
				},
				{
					Count: 2,
					Schedule: &coremodels.Schedule{
						Minutes: []int{45},
					},
				},
			},
		},
	}

	result := convertModelToVolumeResponse(vol)
	assert.NotNil(t, result)
	assert.NotNil(t, result.SnapshotPolicy)
	assert.True(t, result.SnapshotPolicy.Enabled)
	assert.Equal(t, float64(5), result.SnapshotPolicy.MonthlySchedule.SnapshotsToKeep)
	assert.Equal(t, float64(4), result.SnapshotPolicy.WeeklySchedule.SnapshotsToKeep)
	assert.Equal(t, float64(3), result.SnapshotPolicy.DailySchedule.SnapshotsToKeep)
	assert.Equal(t, float64(2), result.SnapshotPolicy.HourlySchedule.SnapshotsToKeep)
}

func TestConvertModelToVolumeResponse_WithExportPolicy(t *testing.T) {
	allSquash := true
	anonUid := int64(65534)
	vol := &coremodels.Volume{
		BaseModel:      coremodels.BaseModel{UUID: "vol-uuid"},
		LifeCycleState: datamodel.LifeCycleStateREADY,
		FileProperties: &coremodels.FileProperties{
			ExportPolicy: &coremodels.ExportPolicy{
				ExportRules: []*coremodels.ExportRule{
					{
						AllowedClients:      "10.0.0.0/8",
						Superuser:           true,
						AccessType:          "ReadWrite",
						NFSv3:               true,
						NFSv4:               false,
						Kerberos5ReadOnly:   false,
						Kerberos5ReadWrite:  true,
						Kerberos5iReadOnly:  false,
						Kerberos5iReadWrite: false,
						Kerberos5pReadOnly:  false,
						Kerberos5pReadWrite: false,
						AllSquash:           &allSquash,
						AnonUid:             &anonUid,
					},
					nil,
				},
			},
		},
	}

	result := convertModelToVolumeResponse(vol)
	assert.NotNil(t, result)
	assert.NotNil(t, result.ExportPolicy)
	assert.Len(t, result.ExportPolicy.Rules, 1)
	assert.Equal(t, "true", result.ExportPolicy.Rules[0].HasRootAccess)
	assert.Equal(t, "10.0.0.0/8", result.ExportPolicy.Rules[0].AllowedClients)
	assert.True(t, result.ExportPolicy.Rules[0].Kerberos5ReadWrite)
}

func TestConvertModelToVolumeResponse_WithCloneDetails_SplitComplete(t *testing.T) {
	splitPercent := int64(100)
	vol := &coremodels.Volume{
		BaseModel:      coremodels.BaseModel{UUID: "vol-uuid"},
		LifeCycleState: datamodel.LifeCycleStateREADY,
		CloneParentInfo: &coremodels.CloneParentInfo{
			SplitCompletePercent: &splitPercent,
		},
	}

	result := convertModelToVolumeResponse(vol)
	assert.NotNil(t, result)
	assert.Nil(t, result.CloneDetails, "split-complete clones should not include CloneDetails")
}

func TestConvertModelToVolumeResponse_WithCloneDetails_Splitting(t *testing.T) {
	splitPercent := int64(50)
	parentVolumeId := "parent-vol-uuid"
	parentSnapshotId := "parent-snap-uuid"
	state := "SPLITTING"
	stateDetails := "split in progress"
	vol := &coremodels.Volume{
		BaseModel:        coremodels.BaseModel{UUID: "vol-uuid"},
		LifeCycleState:   datamodel.LifeCycleStateREADY,
		CloneSharedBytes: 512 * 1024 * 1024,
		CloneParentInfo: &coremodels.CloneParentInfo{
			SplitCompletePercent: &splitPercent,
			ParentVolumeId:       &parentVolumeId,
			ParentSnapshotId:     &parentSnapshotId,
			State:                &state,
			StateDetails:         &stateDetails,
		},
	}

	result := convertModelToVolumeResponse(vol)
	assert.NotNil(t, result)
	assert.NotNil(t, result.CloneDetails)
	assert.Equal(t, "parent-vol-uuid", result.CloneDetails.ParentVolumeId)
	assert.Equal(t, "parent-snap-uuid", result.CloneDetails.ParentSnapshotId)
	assert.Equal(t, "SPLITTING", result.CloneDetails.State)
	assert.Equal(t, "split in progress", result.CloneDetails.StateDetails)
	assert.Equal(t, float64(512*1024*1024), result.CloneDetails.SharedBytes)
}

func TestConvertModelToVolumeCloneDetails_Nil(t *testing.T) {
	result := convertModelToVolumeCloneDetails(nil, 0)
	assert.Nil(t, result)
}

func TestConvertModelToVolumeCloneDetails_EmptyOptionalFields(t *testing.T) {
	cp := &coremodels.CloneParentInfo{}
	result := convertModelToVolumeCloneDetails(cp, 1024)
	assert.NotNil(t, result)
	assert.Equal(t, float64(1024), result.SharedBytes)
	assert.Empty(t, result.ParentVolumeId)
	assert.Empty(t, result.ParentSnapshotId)
	assert.Empty(t, result.State)
	assert.Empty(t, result.StateDetails)
}

func TestConvertModelToVolumeCloneDetails_EmptyStateDetails(t *testing.T) {
	parentVolumeId := "pv-uuid"
	emptyStateDetails := ""
	cp := &coremodels.CloneParentInfo{
		ParentVolumeId: &parentVolumeId,
		StateDetails:   &emptyStateDetails,
	}
	result := convertModelToVolumeCloneDetails(cp, 0)
	assert.NotNil(t, result)
	assert.Equal(t, "pv-uuid", result.ParentVolumeId)
	assert.Empty(t, result.StateDetails)
}

func TestConvertModelToVolumeSnapshotPolicy_Nil(t *testing.T) {
	result := convertModelToVolumeSnapshotPolicy(nil)
	assert.Nil(t, result)
}

func TestConvertModelToVolumeSnapshotPolicy_NilScheduleEntry(t *testing.T) {
	pol := &coremodels.SnapshotPolicy{
		IsEnabled: true,
		Schedules: []*coremodels.SnapshotPolicySchedule{
			nil,
			{
				Count:    3,
				Schedule: nil,
			},
		},
	}
	result := convertModelToVolumeSnapshotPolicy(pol)
	assert.NotNil(t, result)
	assert.True(t, result.Enabled)
}

func TestConvertModelToVolumeExportPolicy_Nil(t *testing.T) {
	result := convertModelToVolumeExportPolicy(nil)
	assert.Nil(t, result)
}

func TestConvertModelToVolumeExportPolicy_SuperuserFalse(t *testing.T) {
	ep := &coremodels.ExportPolicy{
		ExportRules: []*coremodels.ExportRule{
			{
				AllowedClients: "0.0.0.0/0",
				Superuser:      false,
			},
		},
	}
	result := convertModelToVolumeExportPolicy(ep)
	assert.NotNil(t, result)
	assert.Len(t, result.Rules, 1)
	assert.Equal(t, "false", result.Rules[0].HasRootAccess)
}

func TestEncodeVolumeResponse(t *testing.T) {
	volResp := &volumeResponse{
		VolumeId:      "test-uuid",
		ResourceId:    "test-volume",
		CreationToken: "test-token",
		Created:       time.Now(),
	}
	data, err := encodeVolumeResponse(volResp)
	assert.NoError(t, err)
	assert.NotNil(t, data)
	assert.Contains(t, string(data), "test-uuid")
}

func TestV1SplitStartVolume_WithZone(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "us-east4-a")

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4-a",
		VolumeId:      "test-volume-uuid",
	}

	volume := &coremodels.Volume{
		BaseModel:      coremodels.BaseModel{UUID: "test-volume-uuid"},
		LifeCycleState: datamodel.LifeCycleStateREADY,
		DisplayName:    "test-volume",
		CreationToken:  "test-token",
		PoolID:         "test-pool-uuid",
		QuotaInBytes:   1073741824,
	}

	mockOrch.EXPECT().SplitStartVolume(mock.Anything, mock.Anything).Return(volume, "job-uuid", nil)

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	op, ok := result.(*oasgenserver.OperationV1)
	assert.True(t, ok)
	assert.NotNil(t, op)
}

func TestV1SplitStartVolume_CustomErr_OtherHttpCode(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	httpCode := 503
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrInternalServerError,
		Message:     "service unavailable",
		HttpCode:    &httpCode,
		OriginalErr: stderrors.New("service unavailable"),
	}

	mockOrch.EXPECT().SplitStartVolume(mock.Anything, mock.Anything).Return(nil, "", customErr)

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	serverErr, ok := result.(*oasgenserver.V1SplitStartVolumeInternalServerError)
	assert.True(t, ok, "expected InternalServerError for unrecognized custom http code")
	assert.NotNil(t, serverErr)
	assert.Equal(t, float64(500), serverErr.Code)
}

func TestV1SplitStartVolume_CustomErr_NoHttpCode(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrInternalServerError,
		Message:     "custom error without http code",
		OriginalErr: stderrors.New("custom error without http code"),
	}

	mockOrch.EXPECT().SplitStartVolume(mock.Anything, mock.Anything).Return(nil, "", customErr)

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	serverErr, ok := result.(*oasgenserver.V1SplitStartVolumeInternalServerError)
	assert.True(t, ok, "expected InternalServerError for custom error without http code")
	assert.NotNil(t, serverErr)
	assert.Equal(t, float64(500), serverErr.Code)
}

func TestV1SplitStartVolume_ZoneSetInResponse(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStartVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	volume := &coremodels.Volume{
		BaseModel:      coremodels.BaseModel{UUID: "test-volume-uuid"},
		LifeCycleState: datamodel.LifeCycleStateREADY,
	}

	mockOrch.EXPECT().SplitStartVolume(mock.Anything, mock.Anything).Return(volume, "job-uuid", nil)

	ctx := context.Background()
	result, err := handler.V1SplitStartVolume(ctx, params)

	assert.NoError(t, err)
	op, ok := result.(*oasgenserver.OperationV1)
	assert.True(t, ok)
	assert.NotNil(t, op)
	// Response JSON should contain the region as zone when no zone suffix is present
	assert.Contains(t, string(op.Response), "us-east4")
}

func TestConvertModelToVolumeResponse_AllFields(t *testing.T) {
	labels := map[string]string{"env": "test"}
	vol := &coremodels.Volume{
		BaseModel:             coremodels.BaseModel{UUID: "vol-uuid"},
		DisplayName:           "my-volume",
		CreationToken:         "my-token",
		PoolID:                "pool-uuid",
		PoolName:              "pool-name",
		VendorSubnetID:        "subnet-id",
		UsedBytes:             2048,
		QuotaInBytes:          1073741824,
		SnapReserve:           10,
		SnapshotDirectory:     true,
		LifeCycleState:        datamodel.LifeCycleStateREADY,
		LifeCycleStateDetails: "all good",
		IsDataProtection:      true,
		ProtocolTypes:         []string{"NFSv3", "NFSv4"},
		Labels:                labels,
		KerberosEnabled:       true,
		LdapEnabled:           true,
		EncryptionType:        "DOUBLE",
		Description:           "test volume",
		LargeCapacity:         true,
	}

	result := convertModelToVolumeResponse(vol)
	assert.NotNil(t, result)
	assert.Equal(t, "vol-uuid", result.VolumeId)
	assert.Equal(t, "my-volume", result.ResourceId)
	assert.Equal(t, "my-token", result.CreationToken)
	assert.Equal(t, "pool-uuid", result.PoolId)
	assert.Equal(t, "pool-name", result.PoolResourceId)
	assert.Equal(t, "subnet-id", result.Network)
	assert.Equal(t, float64(2048), result.UsedBytes)
	assert.Equal(t, float64(1073741824), result.QuotaInBytes)
	assert.Equal(t, int64(10), result.SnapReserve)
	assert.True(t, result.SnapshotDirectory)
	assert.Equal(t, datamodel.LifeCycleStateREADY, result.VolumeState)
	assert.Equal(t, "all good", result.VolumeStateDetails)
	assert.True(t, result.IsDataProtection)
	assert.Equal(t, []string{"NFSv3", "NFSv4"}, result.Protocols)
	assert.Equal(t, labels, result.Labels)
	assert.True(t, result.KerberosEnabled)
	assert.True(t, result.LdapEnabled)
	assert.Equal(t, "DOUBLE", result.EncryptionType)
	assert.Equal(t, "test volume", result.Description)
	assert.True(t, result.LargeCapacity)
	assert.Nil(t, result.SnapshotPolicy)
	assert.Nil(t, result.ExportPolicy)
	assert.Nil(t, result.CloneDetails)
}

func TestConvertModelToVolumeResponse_FilePropertiesWithoutExportPolicy(t *testing.T) {
	vol := &coremodels.Volume{
		BaseModel:      coremodels.BaseModel{UUID: "vol-uuid"},
		LifeCycleState: datamodel.LifeCycleStateREADY,
		FileProperties: &coremodels.FileProperties{
			JunctionPath: "/vol/test",
		},
	}

	result := convertModelToVolumeResponse(vol)
	assert.NotNil(t, result)
	assert.Nil(t, result.ExportPolicy, "ExportPolicy should be nil when FileProperties has no ExportPolicy")
}

func TestConvertModelToVolumeResponse_CloneParentInfo_NilSplitPercent(t *testing.T) {
	parentVolumeId := "parent-vol-uuid"
	state := "CLONING"
	vol := &coremodels.Volume{
		BaseModel:        coremodels.BaseModel{UUID: "vol-uuid"},
		LifeCycleState:   datamodel.LifeCycleStateREADY,
		CloneSharedBytes: 1024,
		CloneParentInfo: &coremodels.CloneParentInfo{
			ParentVolumeId:       &parentVolumeId,
			SplitCompletePercent: nil,
			State:                &state,
		},
	}

	result := convertModelToVolumeResponse(vol)
	assert.NotNil(t, result)
	assert.NotNil(t, result.CloneDetails, "CloneDetails should be present when SplitCompletePercent is nil")
	assert.Equal(t, "parent-vol-uuid", result.CloneDetails.ParentVolumeId)
	assert.Nil(t, result.CloneDetails.SplitCompletePercent)
}

func TestConvertModelToVolumeCloneDetails_WithSplitCompletePercent(t *testing.T) {
	splitPercent := int64(75)
	parentVolumeId := "pv-uuid"
	parentSnapshotId := "ps-uuid"
	state := "SPLITTING"
	stateDetails := "75% done"
	cp := &coremodels.CloneParentInfo{
		ParentVolumeId:       &parentVolumeId,
		ParentSnapshotId:     &parentSnapshotId,
		SplitCompletePercent: &splitPercent,
		State:                &state,
		StateDetails:         &stateDetails,
	}
	result := convertModelToVolumeCloneDetails(cp, 2048)
	assert.NotNil(t, result)
	assert.Equal(t, float64(2048), result.SharedBytes)
	assert.Equal(t, "pv-uuid", result.ParentVolumeId)
	assert.Equal(t, "ps-uuid", result.ParentSnapshotId)
	assert.Equal(t, "SPLITTING", result.State)
	assert.Equal(t, "75% done", result.StateDetails)
	assert.NotNil(t, result.SplitCompletePercent)
	assert.Equal(t, int64(75), *result.SplitCompletePercent)
}

func TestConvertModelToVolumeSnapshotPolicy_MonthlySchedule_NoHours(t *testing.T) {
	pol := &coremodels.SnapshotPolicy{
		IsEnabled: true,
		Schedules: []*coremodels.SnapshotPolicySchedule{
			{
				Count: 6,
				Schedule: &coremodels.Schedule{
					Minutes:     []int{0},
					DaysOfMonth: []int{1, 15, 28},
				},
			},
		},
	}
	result := convertModelToVolumeSnapshotPolicy(pol)
	assert.NotNil(t, result)
	assert.Equal(t, float64(6), result.MonthlySchedule.SnapshotsToKeep)
	assert.Equal(t, "1,15,28", result.MonthlySchedule.DaysOfMonth)
	assert.Equal(t, float64(0), result.MonthlySchedule.Hour, "hour should default to 0 when no hours provided")
	assert.Equal(t, float64(0), result.MonthlySchedule.Minute)
}

func TestConvertModelToVolumeSnapshotPolicy_WeeklySchedule_NoHours(t *testing.T) {
	pol := &coremodels.SnapshotPolicy{
		IsEnabled: false,
		Schedules: []*coremodels.SnapshotPolicySchedule{
			{
				Count: 4,
				Schedule: &coremodels.Schedule{
					Minutes:    []int{30},
					DaysOfWeek: []int{1, 5},
				},
			},
		},
	}
	result := convertModelToVolumeSnapshotPolicy(pol)
	assert.NotNil(t, result)
	assert.Equal(t, float64(4), result.WeeklySchedule.SnapshotsToKeep)
	assert.Equal(t, "1,5", result.WeeklySchedule.Day)
	assert.Equal(t, float64(0), result.WeeklySchedule.Hour, "hour should default to 0 when no hours provided")
	assert.Equal(t, float64(30), result.WeeklySchedule.Minute)
}

func TestConvertModelToVolumeSnapshotPolicy_HourlySchedule_NoMinutes(t *testing.T) {
	pol := &coremodels.SnapshotPolicy{
		IsEnabled: true,
		Schedules: []*coremodels.SnapshotPolicySchedule{
			{
				Count:    2,
				Schedule: &coremodels.Schedule{},
			},
		},
	}
	result := convertModelToVolumeSnapshotPolicy(pol)
	assert.NotNil(t, result)
	assert.Equal(t, float64(2), result.HourlySchedule.SnapshotsToKeep)
	assert.Equal(t, float64(0), result.HourlySchedule.Minute, "minute should default to 0 when no minutes provided")
}

func TestConvertModelToVolumeExportPolicy_NilAllSquashAndAnonUid(t *testing.T) {
	ep := &coremodels.ExportPolicy{
		ExportRules: []*coremodels.ExportRule{
			{
				AllowedClients: "192.168.0.0/16",
				Superuser:      true,
				AccessType:     "ReadOnly",
				NFSv3:          true,
				NFSv4:          true,
				AllSquash:      nil,
				AnonUid:        nil,
			},
		},
	}
	result := convertModelToVolumeExportPolicy(ep)
	assert.NotNil(t, result)
	assert.Len(t, result.Rules, 1)
	assert.Equal(t, "true", result.Rules[0].HasRootAccess)
	assert.Equal(t, "192.168.0.0/16", result.Rules[0].AllowedClients)
	assert.Equal(t, "ReadOnly", result.Rules[0].AccessType)
	assert.True(t, result.Rules[0].Nfsv3)
	assert.True(t, result.Rules[0].Nfsv4)
	assert.Nil(t, result.Rules[0].AllSquash)
	assert.Nil(t, result.Rules[0].AnonUid)
}

func TestConvertModelToVolumeExportPolicy_EmptyRules(t *testing.T) {
	ep := &coremodels.ExportPolicy{
		ExportRules: []*coremodels.ExportRule{},
	}
	result := convertModelToVolumeExportPolicy(ep)
	assert.NotNil(t, result)
	assert.Empty(t, result.Rules)
}

func TestEncodeVolumeResponse_Nil(t *testing.T) {
	data, err := encodeVolumeResponse(nil)
	assert.NoError(t, err)
	assert.NotNil(t, data)
	assert.Contains(t, string(data), "null")
}

func TestEncodeVolumeResponse_AllFields(t *testing.T) {
	allSquash := true
	anonUid := int64(65534)
	splitPct := int64(50)
	volResp := &volumeResponse{
		VolumeId:           "vol-uuid",
		ResourceId:         "my-volume",
		CreationToken:      "my-token",
		PoolId:             "pool-uuid",
		PoolResourceId:     "pool-name",
		Network:            "subnet-id",
		ServiceLevel:       "FLEX",
		UsedBytes:          1024,
		QuotaInBytes:       2048,
		SnapReserve:        10,
		SnapshotDirectory:  true,
		VolumeState:        "READY",
		VolumeStateDetails: "ok",
		IsDataProtection:   false,
		StorageClass:       "SOFTWARE",
		Protocols:          []string{"NFSv3"},
		Labels:             map[string]string{"k": "v"},
		KerberosEnabled:    false,
		LdapEnabled:        false,
		EncryptionType:     "SINGLE",
		Description:        "desc",
		Zone:               "us-east4-a",
		LargeCapacity:      false,
		Created:            time.Now(),
		SnapshotPolicy: &volumeSnapshotPolicy{
			Enabled: true,
			HourlySchedule: volumeHourlySchedule{
				SnapshotsToKeep: 4,
				Minute:          30,
			},
		},
		ExportPolicy: &volumeExportPolicy{
			Rules: []volumeExportRule{
				{
					AllowedClients: "0.0.0.0/0",
					HasRootAccess:  "false",
					AllSquash:      &allSquash,
					AnonUid:        &anonUid,
				},
			},
		},
		CloneDetails: &volumeCloneDetails{
			ParentVolumeId:       "parent-uuid",
			SharedBytes:          512,
			SplitCompletePercent: &splitPct,
		},
	}
	data, err := encodeVolumeResponse(volResp)
	assert.NoError(t, err)
	assert.NotNil(t, data)
	assert.Contains(t, string(data), "vol-uuid")
	assert.Contains(t, string(data), "my-volume")
	assert.Contains(t, string(data), "parent-uuid")
}

// ============================================================================
// V1SplitStopVolume handler tests
//
// Mirror the V1SplitStartVolume test matrix so every branch of the
// synchronous splitStop handler is covered:
//   - invalid LocationId -> 400 BadRequest
//   - orchestrator NotFound / 400 / Conflict / VCPError(400) /
//     VCPError(409) / VCPError(other) / VCPError(no httpCode) / raw 500
//   - success with explicit zone (LocationId is a zone)
//   - success with empty zone (LocationId is a region; Zone falls back)
// ============================================================================

func TestV1SplitStopVolume_InvalidLocation(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	params := oasgenserver.V1SplitStopVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "invalid-location-no-region",
		VolumeId:      "test-volume-uuid",
	}

	ctx := context.Background()
	result, err := handler.V1SplitStopVolume(ctx, params)

	assert.NoError(t, err)
	badReq, ok := result.(*oasgenserver.V1SplitStopVolumeBadRequest)
	assert.True(t, ok, "expected BadRequest response")
	assert.NotNil(t, badReq)
}

func TestV1SplitStopVolume_NotFound(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStopVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "non-existent-volume",
	}

	mockOrch.EXPECT().SplitStopVolume(mock.Anything, mock.MatchedBy(func(p *commonparams.SplitStopVolumeParams) bool {
		return p.VolumeID == "non-existent-volume" &&
			p.AccountName == "test-project" &&
			p.Region == "us-east4"
	})).Return(nil, errors.NewNotFoundErr("volume", nil))

	ctx := context.Background()
	result, err := handler.V1SplitStopVolume(ctx, params)

	assert.NoError(t, err)
	notFound, ok := result.(*oasgenserver.V1SplitStopVolumeNotFound)
	assert.True(t, ok, "expected NotFound response")
	assert.NotNil(t, notFound)
	assert.Equal(t, float64(404), notFound.Code)
}

func TestV1SplitStopVolume_UserInputValidationErr(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStopVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	mockOrch.EXPECT().SplitStopVolume(mock.Anything, mock.Anything).
		Return(nil, errors.NewUserInputValidationErr("volume is not a thin clone"))

	ctx := context.Background()
	result, err := handler.V1SplitStopVolume(ctx, params)

	assert.NoError(t, err)
	badReq, ok := result.(*oasgenserver.V1SplitStopVolumeBadRequest)
	assert.True(t, ok, "expected BadRequest response")
	assert.NotNil(t, badReq)
	assert.Equal(t, float64(400), badReq.Code)
}

func TestV1SplitStopVolume_BadRequestErr(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStopVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	mockOrch.EXPECT().SplitStopVolume(mock.Anything, mock.Anything).
		Return(nil, errors.NewBadRequestErr("bad request"))

	ctx := context.Background()
	result, err := handler.V1SplitStopVolume(ctx, params)

	assert.NoError(t, err)
	badReq, ok := result.(*oasgenserver.V1SplitStopVolumeBadRequest)
	assert.True(t, ok, "expected BadRequest response")
	assert.NotNil(t, badReq)
	assert.Equal(t, float64(400), badReq.Code)
}

func TestV1SplitStopVolume_ConflictErr(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStopVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	mockOrch.EXPECT().SplitStopVolume(mock.Anything, mock.Anything).
		Return(nil, errors.NewConflictErr("volume split is not in progress"))

	ctx := context.Background()
	result, err := handler.V1SplitStopVolume(ctx, params)

	assert.NoError(t, err)
	conflict, ok := result.(*oasgenserver.V1SplitStopVolumeConflict)
	assert.True(t, ok, "expected Conflict response")
	assert.NotNil(t, conflict)
	assert.Equal(t, float64(409), conflict.Code)
}

func TestV1SplitStopVolume_CustomErr_400(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStopVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	httpCode := 400
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrInputValidationError,
		Message:     "custom bad request",
		HttpCode:    &httpCode,
		OriginalErr: stderrors.New("custom bad request"),
	}

	mockOrch.EXPECT().SplitStopVolume(mock.Anything, mock.Anything).Return(nil, customErr)

	ctx := context.Background()
	result, err := handler.V1SplitStopVolume(ctx, params)

	assert.NoError(t, err)
	badReq, ok := result.(*oasgenserver.V1SplitStopVolumeBadRequest)
	assert.True(t, ok, "expected BadRequest response for custom 400 error")
	assert.NotNil(t, badReq)
	assert.Equal(t, float64(400), badReq.Code)
	// Custom error path returns the CustomError.Message, NOT err.Error().
	assert.Equal(t, "custom bad request", badReq.Message)
}

func TestV1SplitStopVolume_CustomErr_409(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStopVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	httpCode := 409
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrResourceStateConflictError,
		Message:     "custom conflict",
		HttpCode:    &httpCode,
		OriginalErr: stderrors.New("custom conflict"),
	}

	mockOrch.EXPECT().SplitStopVolume(mock.Anything, mock.Anything).Return(nil, customErr)

	ctx := context.Background()
	result, err := handler.V1SplitStopVolume(ctx, params)

	assert.NoError(t, err)
	conflict, ok := result.(*oasgenserver.V1SplitStopVolumeConflict)
	assert.True(t, ok, "expected Conflict response for custom 409 error")
	assert.NotNil(t, conflict)
	assert.Equal(t, float64(409), conflict.Code)
	assert.Equal(t, "custom conflict", conflict.Message)
}

func TestV1SplitStopVolume_CustomErr_OtherHttpCode(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStopVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	// Anything other than 400/409 falls through to the default 500 branch.
	httpCode := 503
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrInternalServerError,
		Message:     "service unavailable",
		HttpCode:    &httpCode,
		OriginalErr: stderrors.New("service unavailable"),
	}

	mockOrch.EXPECT().SplitStopVolume(mock.Anything, mock.Anything).Return(nil, customErr)

	ctx := context.Background()
	result, err := handler.V1SplitStopVolume(ctx, params)

	assert.NoError(t, err)
	serverErr, ok := result.(*oasgenserver.V1SplitStopVolumeInternalServerError)
	assert.True(t, ok, "expected InternalServerError for unrecognized custom http code")
	assert.NotNil(t, serverErr)
	assert.Equal(t, float64(500), serverErr.Code)
}

func TestV1SplitStopVolume_CustomErr_NoHttpCode(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStopVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	// CustomError without an HttpCode must also fall through to 500.
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrInternalServerError,
		Message:     "custom error without http code",
		OriginalErr: stderrors.New("custom error without http code"),
	}

	mockOrch.EXPECT().SplitStopVolume(mock.Anything, mock.Anything).Return(nil, customErr)

	ctx := context.Background()
	result, err := handler.V1SplitStopVolume(ctx, params)

	assert.NoError(t, err)
	serverErr, ok := result.(*oasgenserver.V1SplitStopVolumeInternalServerError)
	assert.True(t, ok, "expected InternalServerError for custom error without http code")
	assert.NotNil(t, serverErr)
	assert.Equal(t, float64(500), serverErr.Code)
}

func TestV1SplitStopVolume_InternalServerError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStopVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	mockOrch.EXPECT().SplitStopVolume(mock.Anything, mock.Anything).
		Return(nil, stderrors.New("internal error"))

	ctx := context.Background()
	result, err := handler.V1SplitStopVolume(ctx, params)

	assert.NoError(t, err)
	serverErr, ok := result.(*oasgenserver.V1SplitStopVolumeInternalServerError)
	assert.True(t, ok, "expected InternalServerError response for plain stdlib error")
	assert.NotNil(t, serverErr)
	assert.Equal(t, float64(500), serverErr.Code)
}

func TestV1SplitStopVolume_Success_WithZone(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	// LocationId is a zone: parseAndValidateRegionAndZone returns both region and zone.
	mockParseAndValidateRegionAndZone(t, "us-east4", "us-east4-a")

	params := oasgenserver.V1SplitStopVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4-a",
		VolumeId:      "test-volume-uuid",
	}

	volume := &coremodels.Volume{
		BaseModel:      coremodels.BaseModel{UUID: "test-volume-uuid"},
		LifeCycleState: datamodel.LifeCycleStateREADY,
		DisplayName:    "test-volume",
		CreationToken:  "test-token",
		PoolID:         "test-pool-uuid",
		QuotaInBytes:   1073741824,
		ProtocolTypes:  []string{"NFSv3"},
	}

	mockOrch.EXPECT().SplitStopVolume(mock.Anything, mock.Anything).Return(volume, nil)

	ctx := context.Background()
	result, err := handler.V1SplitStopVolume(ctx, params)

	assert.NoError(t, err)
	op, ok := result.(*oasgenserver.OperationV1)
	assert.True(t, ok, "expected OperationV1 response")
	assert.NotNil(t, op)
	// Stop is synchronous; done must always be true.
	assert.Equal(t, true, op.Done.Or(false))
	// Operation name must embed the project+location for client correlation.
	assert.Contains(t, op.Name.Or(""), "/v1beta/projects/test-project/locations/us-east4-a/operations/")
	// When a zone suffix was parsed it must appear in the response payload.
	assert.Contains(t, string(op.Response), "us-east4-a")
}

func TestV1SplitStopVolume_Success_NoZone_FallsBackToRegion(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	// LocationId is a region only: zone returned is empty so the handler must
	// fall back to using the region as the response Zone.
	mockParseAndValidateRegionAndZone(t, "us-east4", "")

	params := oasgenserver.V1SplitStopVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-east4",
		VolumeId:      "test-volume-uuid",
	}

	volume := &coremodels.Volume{
		BaseModel:      coremodels.BaseModel{UUID: "test-volume-uuid"},
		LifeCycleState: datamodel.LifeCycleStateREADY,
		DisplayName:    "test-volume",
	}

	mockOrch.EXPECT().SplitStopVolume(mock.Anything, mock.Anything).Return(volume, nil)

	ctx := context.Background()
	result, err := handler.V1SplitStopVolume(ctx, params)

	assert.NoError(t, err)
	op, ok := result.(*oasgenserver.OperationV1)
	assert.True(t, ok, "expected OperationV1 response")
	assert.NotNil(t, op)
	assert.Equal(t, true, op.Done.Or(false))
	// Region fallback must surface in the response JSON.
	assert.Contains(t, string(op.Response), "us-east4")
}
