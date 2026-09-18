package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func TestV1ExpertModeVolume(t *testing.T) {
	t.Run("SuccessWithCloneFieldsMapped", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		poolUUID := "550e8400-e29b-41d4-a716-446655440000"
		volumeName := "clone-volume"
		svmName := "svm-a"
		projectNumber := "123456789"
		parentVolUUID := "11111111-1111-1111-1111-111111111111"
		parentVolName := "src-vol"
		parentSnapUUID := "22222222-2222-2222-2222-222222222222"
		parentSnapName := "snap-1"

		req := &oasgenserver.ExpertModeVolumeV1{
			ProjectNumber: projectNumber,
			PoolUUID:      poolUUID,
			Action:        oasgenserver.ExpertModeVolumeV1ActionCreate,
			VolumeName:    volumeName,
			Style:         oasgenserver.ExpertModeVolumeV1StyleFlexvol,
			SvmName:       oasgenserver.NewOptString(svmName),
			Clone: oasgenserver.NewOptExpertModeVolumeV1Clone(oasgenserver.ExpertModeVolumeV1Clone{
				IsFlexclone: oasgenserver.NewOptBool(true),
				ParentVolume: oasgenserver.NewOptExpertModeVolumeV1CloneParentVolume(
					oasgenserver.ExpertModeVolumeV1CloneParentVolume{
						UUID: oasgenserver.NewOptString(parentVolUUID),
						Name: oasgenserver.NewOptString(parentVolName),
					},
				),
				ParentSnapshot: oasgenserver.NewOptExpertModeVolumeV1CloneParentSnapshot(
					oasgenserver.ExpertModeVolumeV1CloneParentSnapshot{
						UUID: oasgenserver.NewOptString(parentSnapUUID),
						Name: oasgenserver.NewOptString(parentSnapName),
					},
				),
			}),
		}

		expectedParams := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    poolUUID,
			Action:      string(oasgenserver.ExpertModeVolumeV1ActionCreate),
			VolumeName:  volumeName,
			Style:       string(oasgenserver.ExpertModeVolumeV1StyleFlexvol),
			SvmName:     svmName,
			AccountName: projectNumber,
			Clone: &commonparams.ExpertModeVolumeCloneParams{
				IsFlexclone: true,
				ParentVolume: &commonparams.ExpertModeVolumeCloneParent{
					UUID: parentVolUUID,
					Name: parentVolName,
				},
				ParentSnapshot: &commonparams.ExpertModeVolumeCloneParent{
					UUID: parentSnapUUID,
					Name: parentSnapName,
				},
			},
		}

		mockOrch.EXPECT().CreateExpertModeVolume(mock.Anything, expectedParams).Return(nil)

		result, err := handler.V1ExpertModeVolume(context.Background(), req, oasgenserver.V1ExpertModeVolumeParams{})
		assert.NoError(t, err)
		assert.IsType(t, &oasgenserver.V1ExpertModeVolumeOK{}, result)
		mockOrch.AssertExpectations(t)
	})

	t.Run("SuccessWithSvmUUID", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		poolUUID := "550e8400-e29b-41d4-a716-446655440000"
		volumeName := "my-expert-volume"
		svmUUID := "660e8400-e29b-41d4-a716-446655440001"
		sizeInBytes := 1099511627776.0
		projectNumber := "123456789"

		req := newExpertModeVolumeRequest(poolUUID, oasgenserver.ExpertModeVolumeV1ActionCreate, volumeName, sizeInBytes, oasgenserver.ExpertModeVolumeV1StyleFlexvol, &svmUUID, nil, projectNumber)
		params := oasgenserver.V1ExpertModeVolumeParams{}

		expectedParams := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    poolUUID,
			Action:      string(oasgenserver.ExpertModeVolumeV1ActionCreate),
			VolumeName:  volumeName,
			SizeInBytes: int64(sizeInBytes),
			Style:       string(oasgenserver.ExpertModeVolumeV1StyleFlexvol),
			SvmUuid:     svmUUID,
			SvmName:     "",
			AccountName: projectNumber,
		}

		// Set up expectations
		mockOrch.EXPECT().CreateExpertModeVolume(mock.Anything, expectedParams).Return(nil)

		// Execute
		ctx := context.Background()
		result, err := handler.V1ExpertModeVolume(ctx, req, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &oasgenserver.V1ExpertModeVolumeOK{}, result)

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})

	t.Run("SuccessWithSvmName", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		poolUUID := "550e8400-e29b-41d4-a716-446655440000"
		volumeName := "my-expert-volume"
		svmName := "my-svm"
		sizeInBytes := 1099511627776.0
		projectNumber := "123456789"

		req := newExpertModeVolumeRequest(poolUUID, oasgenserver.ExpertModeVolumeV1ActionCreate, volumeName, sizeInBytes, oasgenserver.ExpertModeVolumeV1StyleFlexgroup, nil, &svmName, projectNumber)
		params := oasgenserver.V1ExpertModeVolumeParams{}

		expectedParams := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    poolUUID,
			Action:      string(oasgenserver.ExpertModeVolumeV1ActionCreate),
			VolumeName:  volumeName,
			SizeInBytes: int64(sizeInBytes),
			Style:       string(oasgenserver.ExpertModeVolumeV1StyleFlexgroup),
			SvmUuid:     "",
			SvmName:     svmName,
			AccountName: projectNumber,
		}

		// Set up expectations
		mockOrch.EXPECT().CreateExpertModeVolume(mock.Anything, expectedParams).Return(nil)

		// Execute
		ctx := context.Background()
		result, err := handler.V1ExpertModeVolume(ctx, req, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &oasgenserver.V1ExpertModeVolumeOK{}, result)

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})

	t.Run("SuccessWithCorrelationID", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		poolUUID := "550e8400-e29b-41d4-a716-446655440000"
		volumeName := "my-expert-volume"
		svmUUID := "660e8400-e29b-41d4-a716-446655440001"
		sizeInBytes := 1099511627776.0
		correlationID := "test-correlation-id-12345"
		projectNumber := "123456789"

		req := newExpertModeVolumeRequest(poolUUID, oasgenserver.ExpertModeVolumeV1ActionCreate, volumeName, sizeInBytes, oasgenserver.ExpertModeVolumeV1StyleFlexvol, &svmUUID, nil, projectNumber)
		params := oasgenserver.V1ExpertModeVolumeParams{}

		expectedParams := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    poolUUID,
			Action:      string(oasgenserver.ExpertModeVolumeV1ActionCreate),
			VolumeName:  volumeName,
			SizeInBytes: int64(sizeInBytes),
			Style:       string(oasgenserver.ExpertModeVolumeV1StyleFlexvol),
			SvmUuid:     svmUUID,
			SvmName:     "",
			AccountName: projectNumber,
		}

		// Set up expectations
		mockOrch.EXPECT().CreateExpertModeVolume(mock.Anything, expectedParams).Return(nil)

		// Execute with correlation ID in context
		ctx := context.WithValue(context.Background(), "X-Correlation-ID", correlationID)
		result, err := handler.V1ExpertModeVolume(ctx, req, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &oasgenserver.V1ExpertModeVolumeOK{}, result)

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})

	t.Run("BadRequestError_SvmNotFound", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		poolUUID := "550e8400-e29b-41d4-a716-446655440000"
		volumeName := "my-expert-volume"
		svmUUID := "non-existent-svm-uuid"
		sizeInBytes := 1099511627776.0
		projectNumber := "123456789"

		req := newExpertModeVolumeRequest(poolUUID, oasgenserver.ExpertModeVolumeV1ActionCreate, volumeName, sizeInBytes, oasgenserver.ExpertModeVolumeV1StyleFlexvol, &svmUUID, nil, projectNumber)
		params := oasgenserver.V1ExpertModeVolumeParams{}

		expectedParams := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    poolUUID,
			Action:      string(oasgenserver.ExpertModeVolumeV1ActionCreate),
			VolumeName:  volumeName,
			SizeInBytes: int64(sizeInBytes),
			Style:       string(oasgenserver.ExpertModeVolumeV1StyleFlexvol),
			SvmUuid:     svmUUID,
			SvmName:     "",
			AccountName: projectNumber,
		}

		// Set up expectations - orchestrator returns bad request error
		svmNotFoundErr := customerrors.NewBadRequestErr("SVM with UUID 'non-existent-svm-uuid' not found in pool; the SVM for this pool is 'svm_po_01'")
		mockOrch.EXPECT().CreateExpertModeVolume(mock.Anything, expectedParams).Return(svmNotFoundErr)

		// Execute
		ctx := context.Background()
		result, err := handler.V1ExpertModeVolume(ctx, req, params)

		// Assert
		assert.NoError(t, err) // Handler converts error to response
		assert.NotNil(t, result)

		badRequest, ok := result.(*oasgenserver.V1ExpertModeVolumeBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusBadRequest), badRequest.Code)
		assert.Contains(t, badRequest.Message, "SVM with UUID")
		assert.Contains(t, badRequest.Message, "not found")
		assert.Contains(t, badRequest.Message, "the SVM for this pool is 'svm_po_01'")

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})

	t.Run("BadRequestError_InsufficientCapacity", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		poolUUID := "550e8400-e29b-41d4-a716-446655440000"
		volumeName := "my-expert-volume"
		svmUUID := "660e8400-e29b-41d4-a716-446655440001"
		sizeInBytes := 1099511627776.0
		projectNumber := "123456789"

		req := newExpertModeVolumeRequest(poolUUID, oasgenserver.ExpertModeVolumeV1ActionCreate, volumeName, sizeInBytes, oasgenserver.ExpertModeVolumeV1StyleFlexvol, &svmUUID, nil, projectNumber)
		params := oasgenserver.V1ExpertModeVolumeParams{}

		expectedParams := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    poolUUID,
			Action:      string(oasgenserver.ExpertModeVolumeV1ActionCreate),
			VolumeName:  volumeName,
			SizeInBytes: int64(sizeInBytes),
			Style:       string(oasgenserver.ExpertModeVolumeV1StyleFlexvol),
			SvmUuid:     svmUUID,
			SvmName:     "",
			AccountName: projectNumber,
		}

		// Set up expectations - orchestrator returns bad request error
		insufficientCapacityErr := customerrors.NewBadRequestErr("insufficient pool capacity for the requested volume size")
		mockOrch.EXPECT().CreateExpertModeVolume(mock.Anything, expectedParams).Return(insufficientCapacityErr)

		// Execute
		ctx := context.Background()
		result, err := handler.V1ExpertModeVolume(ctx, req, params)

		// Assert
		assert.NoError(t, err) // Handler converts error to response
		assert.NotNil(t, result)

		badRequest, ok := result.(*oasgenserver.V1ExpertModeVolumeBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusBadRequest), badRequest.Code)
		assert.Contains(t, badRequest.Message, "insufficient pool capacity")

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})

	t.Run("BadRequestError_MissingSvmIdentifier", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data - neither svmUUID nor svmName provided
		poolUUID := "550e8400-e29b-41d4-a716-446655440000"
		volumeName := "my-expert-volume"
		sizeInBytes := 1099511627776.0
		projectNumber := "123456789"

		req := newExpertModeVolumeRequest(poolUUID, oasgenserver.ExpertModeVolumeV1ActionCreate, volumeName, sizeInBytes, oasgenserver.ExpertModeVolumeV1StyleFlexvol, nil, nil, projectNumber)
		params := oasgenserver.V1ExpertModeVolumeParams{}

		expectedParams := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    poolUUID,
			Action:      string(oasgenserver.ExpertModeVolumeV1ActionCreate),
			VolumeName:  volumeName,
			SizeInBytes: int64(sizeInBytes),
			Style:       string(oasgenserver.ExpertModeVolumeV1StyleFlexvol),
			SvmUuid:     "",
			SvmName:     "",
			AccountName: projectNumber,
		}

		// Set up expectations - orchestrator returns bad request error
		missingSvmErr := customerrors.NewBadRequestErr("neither svmName nor svmUUID has been passed")
		mockOrch.EXPECT().CreateExpertModeVolume(mock.Anything, expectedParams).Return(missingSvmErr)

		// Execute
		ctx := context.Background()
		result, err := handler.V1ExpertModeVolume(ctx, req, params)

		// Assert
		assert.NoError(t, err) // Handler converts error to response
		assert.NotNil(t, result)

		badRequest, ok := result.(*oasgenserver.V1ExpertModeVolumeBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusBadRequest), badRequest.Code)
		assert.Contains(t, badRequest.Message, "neither svmName nor svmUUID")

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})

	t.Run("InternalServerError_PoolNotFound", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		poolUUID := "non-existent-pool-uuid"
		volumeName := "my-expert-volume"
		svmUUID := "660e8400-e29b-41d4-a716-446655440001"
		sizeInBytes := 1099511627776.0
		projectNumber := "123456789"

		req := newExpertModeVolumeRequest(poolUUID, oasgenserver.ExpertModeVolumeV1ActionCreate, volumeName, sizeInBytes, oasgenserver.ExpertModeVolumeV1StyleFlexvol, &svmUUID, nil, projectNumber)
		params := oasgenserver.V1ExpertModeVolumeParams{}

		expectedParams := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    poolUUID,
			Action:      string(oasgenserver.ExpertModeVolumeV1ActionCreate),
			VolumeName:  volumeName,
			SizeInBytes: int64(sizeInBytes),
			Style:       string(oasgenserver.ExpertModeVolumeV1StyleFlexvol),
			SvmUuid:     svmUUID,
			SvmName:     "",
			AccountName: projectNumber,
		}

		// Set up expectations - orchestrator returns generic error (not BadRequest)
		poolNotFoundErr := errors.New("pool not found")
		mockOrch.EXPECT().CreateExpertModeVolume(mock.Anything, expectedParams).Return(poolNotFoundErr)

		// Execute
		ctx := context.Background()
		result, err := handler.V1ExpertModeVolume(ctx, req, params)

		// Assert
		assert.NoError(t, err) // Handler converts error to response
		assert.NotNil(t, result)

		internalError, ok := result.(*oasgenserver.V1ExpertModeVolumeInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusInternalServerError), internalError.Code)
		assert.Equal(t, "pool not found", internalError.Message)

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})

	t.Run("InternalServerError_DatabaseError", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		poolUUID := "550e8400-e29b-41d4-a716-446655440000"
		volumeName := "my-expert-volume"
		svmUUID := "660e8400-e29b-41d4-a716-446655440001"
		sizeInBytes := 1099511627776.0
		projectNumber := "123456789"

		req := newExpertModeVolumeRequest(poolUUID, oasgenserver.ExpertModeVolumeV1ActionCreate, volumeName, sizeInBytes, oasgenserver.ExpertModeVolumeV1StyleFlexvol, &svmUUID, nil, projectNumber)
		params := oasgenserver.V1ExpertModeVolumeParams{}

		expectedParams := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    poolUUID,
			Action:      string(oasgenserver.ExpertModeVolumeV1ActionCreate),
			VolumeName:  volumeName,
			SizeInBytes: int64(sizeInBytes),
			Style:       string(oasgenserver.ExpertModeVolumeV1StyleFlexvol),
			SvmUuid:     svmUUID,
			SvmName:     "",
			AccountName: projectNumber,
		}

		// Set up expectations - orchestrator returns database error
		databaseErr := errors.New("database connection failed")
		mockOrch.EXPECT().CreateExpertModeVolume(mock.Anything, expectedParams).Return(databaseErr)

		// Execute
		ctx := context.Background()
		result, err := handler.V1ExpertModeVolume(ctx, req, params)

		// Assert
		assert.NoError(t, err) // Handler converts error to response
		assert.NotNil(t, result)

		internalError, ok := result.(*oasgenserver.V1ExpertModeVolumeInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusInternalServerError), internalError.Code)
		assert.Equal(t, "database connection failed", internalError.Message)

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})

	t.Run("Success_DeleteAction", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		poolUUID := "550e8400-e29b-41d4-a716-446655440000"
		volumeUUID := "770e8400-e29b-41d4-a716-446655440002"
		projectNumber := "123456789"

		req := &oasgenserver.ExpertModeVolumeV1{
			PoolUUID:      poolUUID,
			Action:        oasgenserver.ExpertModeVolumeV1ActionDelete,
			VolumeUUID:    oasgenserver.NewOptString(volumeUUID),
			SizeInBytes:   oasgenserver.NewOptFloat64(0),
			Style:         oasgenserver.ExpertModeVolumeV1StyleFlexvol,
			ProjectNumber: projectNumber,
		}
		params := oasgenserver.V1ExpertModeVolumeParams{}

		expectedParams := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    poolUUID,
			Action:      string(oasgenserver.ExpertModeVolumeV1ActionDelete),
			VolumeUUID:  volumeUUID,
			SizeInBytes: 0,
			Style:       string(oasgenserver.ExpertModeVolumeV1StyleFlexvol),
			AccountName: projectNumber,
		}

		// Set up expectations
		mockOrch.EXPECT().DeleteExpertModeVolume(mock.Anything, expectedParams).Return(nil)

		// Execute
		ctx := context.Background()
		result, err := handler.V1ExpertModeVolume(ctx, req, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &oasgenserver.V1ExpertModeVolumeOK{}, result)

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})

	t.Run("Success_UpdateAction", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		poolUUID := "550e8400-e29b-41d4-a716-446655440000"
		volumeUUID := "770e8400-e29b-41d4-a716-446655440002"
		volumeName := "my-expert-volume"
		sizeInBytes := 2199023255552.0
		projectNumber := "123456789"

		req := &oasgenserver.ExpertModeVolumeV1{
			PoolUUID:      poolUUID,
			Action:        oasgenserver.ExpertModeVolumeV1ActionUpdate,
			VolumeUUID:    oasgenserver.NewOptString(volumeUUID),
			VolumeName:    volumeName,
			SizeInBytes:   oasgenserver.NewOptFloat64(sizeInBytes),
			Style:         oasgenserver.ExpertModeVolumeV1StyleFlexvol,
			ProjectNumber: projectNumber,
		}
		params := oasgenserver.V1ExpertModeVolumeParams{}

		expectedParams := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    poolUUID,
			Action:      string(oasgenserver.ExpertModeVolumeV1ActionUpdate),
			VolumeUUID:  volumeUUID,
			VolumeName:  volumeName,
			SizeInBytes: int64(sizeInBytes),
			Style:       string(oasgenserver.ExpertModeVolumeV1StyleFlexvol),
			AccountName: projectNumber,
		}

		// Set up expectations
		mockOrch.EXPECT().UpdateExpertModeVolume(mock.Anything, expectedParams).Return(nil)

		// Execute
		ctx := context.Background()
		result, err := handler.V1ExpertModeVolume(ctx, req, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &oasgenserver.V1ExpertModeVolumeOK{}, result)

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})
}

func TestV1ExpertModeVolumeRename(t *testing.T) {
	volumeName := "my-volume"
	newName := "my-renamed-volume"
	poolUUID := "550e8400-e29b-41d4-a716-446655440000"
	svmName := "my-svm"
	projectNumber := "123456789"

	t.Run("Success", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.ExpertModeVolumeRenameV1{
			Name:          newName,
			ProjectNumber: projectNumber,
			PoolUUID:      poolUUID,
			SvmName:       svmName,
		}
		params := oasgenserver.V1ExpertModeVolumeRenameParams{Name: volumeName}

		expectedParams := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volumeName,
			NewName:     newName,
			PoolUUID:    poolUUID,
			SvmName:     svmName,
			AccountName: projectNumber,
		}

		mockOrch.EXPECT().RenameExpertModeVolume(mock.Anything, expectedParams).Return(nil)

		ctx := context.Background()
		result, err := handler.V1ExpertModeVolumeRename(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &oasgenserver.V1ExpertModeVolumeRenameOK{}, result)
		mockOrch.AssertExpectations(t)
	})

	t.Run("BadRequestError_VolumeNotFound", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.ExpertModeVolumeRenameV1{
			Name:          newName,
			ProjectNumber: projectNumber,
			PoolUUID:      poolUUID,
			SvmName:       svmName,
		}
		params := oasgenserver.V1ExpertModeVolumeRenameParams{Name: volumeName}

		expectedParams := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volumeName,
			NewName:     newName,
			PoolUUID:    poolUUID,
			SvmName:     svmName,
			AccountName: projectNumber,
		}

		badRequestErr := customerrors.NewBadRequestErr("volume with name 'my-volume' not found in pool")
		mockOrch.EXPECT().RenameExpertModeVolume(mock.Anything, expectedParams).Return(badRequestErr)

		ctx := context.Background()
		result, err := handler.V1ExpertModeVolumeRename(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		badRequest, ok := result.(*oasgenserver.V1ExpertModeVolumeRenameBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusBadRequest), badRequest.Code)
		assert.Contains(t, badRequest.Message, "not found")
		mockOrch.AssertExpectations(t)
	})

	t.Run("BadRequestError_SvmNameMismatch", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.ExpertModeVolumeRenameV1{
			Name:          newName,
			ProjectNumber: projectNumber,
			PoolUUID:      poolUUID,
			SvmName:       svmName,
		}
		params := oasgenserver.V1ExpertModeVolumeRenameParams{Name: volumeName}

		expectedParams := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volumeName,
			NewName:     newName,
			PoolUUID:    poolUUID,
			SvmName:     svmName,
			AccountName: projectNumber,
		}

		badRequestErr := customerrors.NewBadRequestErr("SVM name does not match: expected my-svm")
		mockOrch.EXPECT().RenameExpertModeVolume(mock.Anything, expectedParams).Return(badRequestErr)

		ctx := context.Background()
		result, err := handler.V1ExpertModeVolumeRename(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		badRequest, ok := result.(*oasgenserver.V1ExpertModeVolumeRenameBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusBadRequest), badRequest.Code)
		assert.Contains(t, badRequest.Message, "SVM name")
		mockOrch.AssertExpectations(t)
	})

	t.Run("InternalServerError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.ExpertModeVolumeRenameV1{
			Name:          newName,
			ProjectNumber: projectNumber,
			PoolUUID:      poolUUID,
			SvmName:       svmName,
		}
		params := oasgenserver.V1ExpertModeVolumeRenameParams{Name: volumeName}

		expectedParams := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volumeName,
			NewName:     newName,
			PoolUUID:    poolUUID,
			SvmName:     svmName,
			AccountName: projectNumber,
		}

		internalErr := errors.New("workflow execution failed")
		mockOrch.EXPECT().RenameExpertModeVolume(mock.Anything, expectedParams).Return(internalErr)

		ctx := context.Background()
		result, err := handler.V1ExpertModeVolumeRename(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		internalError, ok := result.(*oasgenserver.V1ExpertModeVolumeRenameInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusInternalServerError), internalError.Code)
		assert.Equal(t, "workflow execution failed", internalError.Message)
		mockOrch.AssertExpectations(t)
	})

	t.Run("Success_WithCorrelationID", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.ExpertModeVolumeRenameV1{
			Name:          newName,
			ProjectNumber: projectNumber,
			PoolUUID:      poolUUID,
			SvmName:       svmName,
		}
		params := oasgenserver.V1ExpertModeVolumeRenameParams{
			Name:           volumeName,
			XCorrelationID: oasgenserver.NewOptString("correlation-123"),
		}

		expectedParams := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volumeName,
			NewName:     newName,
			PoolUUID:    poolUUID,
			SvmName:     svmName,
			AccountName: projectNumber,
		}

		mockOrch.EXPECT().RenameExpertModeVolume(mock.Anything, expectedParams).Return(nil)

		ctx := context.Background()
		result, err := handler.V1ExpertModeVolumeRename(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &oasgenserver.V1ExpertModeVolumeRenameOK{}, result)
		mockOrch.AssertExpectations(t)
	})
}

// Helper function to create expert mode volume requests
func newExpertModeVolumeRequest(poolUUID string, action oasgenserver.ExpertModeVolumeV1Action, name string, sizeInBytes float64, style oasgenserver.ExpertModeVolumeV1Style, svmUUID *string, svmName *string, projectNumber string) *oasgenserver.ExpertModeVolumeV1 {
	req := &oasgenserver.ExpertModeVolumeV1{
		PoolUUID:      poolUUID,
		Action:        action,
		VolumeName:    name,
		SizeInBytes:   oasgenserver.NewOptFloat64(sizeInBytes),
		Style:         style,
		SvmUuid:       oasgenserver.OptString{},
		SvmName:       oasgenserver.OptString{},
		ProjectNumber: projectNumber,
	}

	if svmUUID != nil {
		req.SvmUuid = oasgenserver.NewOptString(*svmUUID)
	}

	if svmName != nil {
		req.SvmName = oasgenserver.NewOptString(*svmName)
	}

	return req
}

func TestV1ExpertModeVolumeFlexCloneSplit(t *testing.T) {
	volumeUUID := "f5d0bf36-5282-45c2-a837-ed7d8181c860"
	volumeName := "clone-vol-name"
	poolUUID := "9760acf5-4638-11e7-9bdb-020073ca7773"
	projectNumber := "123456789012"
	t.Run("Success", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.ExpertModeVolumeFlexCloneSplitV1{
			ProjectNumber: projectNumber,
			PoolUUID:      poolUUID,
			VolumeUUID:    oasgenserver.NewOptString(volumeUUID),
		}

		mockOrch.EXPECT().StartExpertModeFlexCloneSplit(mock.Anything, &commonparams.ExpertModeFlexCloneSplitParams{
			VolumeUUID:  volumeUUID,
			VolumeName:  "",
			PoolUUID:    poolUUID,
			AccountName: projectNumber,
		}).Return(nil)

		result, err := handler.V1ExpertModeVolumeFlexCloneSplit(context.Background(), req, oasgenserver.V1ExpertModeVolumeFlexCloneSplitParams{})
		assert.NoError(t, err)
		_, ok := result.(*oasgenserver.V1ExpertModeVolumeFlexCloneSplitAccepted)
		assert.True(t, ok)
		mockOrch.AssertExpectations(t)
	})

	t.Run("Success_WithVolumeName", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.ExpertModeVolumeFlexCloneSplitV1{
			ProjectNumber: projectNumber,
			PoolUUID:      poolUUID,
			VolumeName:    oasgenserver.NewOptString(volumeName),
		}

		mockOrch.EXPECT().StartExpertModeFlexCloneSplit(mock.Anything, &commonparams.ExpertModeFlexCloneSplitParams{
			VolumeUUID:  "",
			VolumeName:  volumeName,
			PoolUUID:    poolUUID,
			AccountName: projectNumber,
		}).Return(nil)

		result, err := handler.V1ExpertModeVolumeFlexCloneSplit(context.Background(), req, oasgenserver.V1ExpertModeVolumeFlexCloneSplitParams{})
		assert.NoError(t, err)
		_, ok := result.(*oasgenserver.V1ExpertModeVolumeFlexCloneSplitAccepted)
		assert.True(t, ok)
		mockOrch.AssertExpectations(t)
	})

	t.Run("BadRequest", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		req := &oasgenserver.ExpertModeVolumeFlexCloneSplitV1{
			ProjectNumber: projectNumber,
			PoolUUID:      poolUUID,
			VolumeUUID:    oasgenserver.NewOptString(volumeUUID),
		}
		mockOrch.EXPECT().StartExpertModeFlexCloneSplit(mock.Anything, mock.Anything).Return(customerrors.NewBadRequestErr("not a FlexClone"))

		result, err := handler.V1ExpertModeVolumeFlexCloneSplit(context.Background(), req, oasgenserver.V1ExpertModeVolumeFlexCloneSplitParams{})
		assert.NoError(t, err)
		br, ok := result.(*oasgenserver.V1ExpertModeVolumeFlexCloneSplitBadRequest)
		assert.True(t, ok)
		assert.Contains(t, br.Message, "not a FlexClone")
	})
}

func TestV1RefreshRbacForExpertModePoolById(t *testing.T) {
	poolId := "550e8400-e29b-41d4-a716-446655440000"

	t.Run("Success", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		jobID := "job-uuid-12345"
		params := oasgenserver.V1RefreshRbacForExpertModePoolByIdParams{PoolId: poolId}

		mockOrch.EXPECT().UpdateRbacForPoolById(mock.Anything, &commonparams.RefreshRbacForPoolParams{PoolID: poolId}).Return(jobID, nil)

		result, err := handler.V1RefreshRbacForExpertModePoolById(context.Background(), params)
		assert.NoError(t, err)
		assert.NotNil(t, result)

		operation, ok := result.(*oasgenserver.OperationV1)
		assert.True(t, ok)
		assert.False(t, operation.Done.Or(true))
		expectedName := fmt.Sprintf("/v1/expertMode/rbac/refresh/pool/%s/operations/%s", poolId, jobID)
		assert.Equal(t, expectedName, operation.Name.Or(""))
		mockOrch.AssertExpectations(t)
	})

	t.Run("BadRequestError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		params := oasgenserver.V1RefreshRbacForExpertModePoolByIdParams{PoolId: poolId}

		badRequestErr := customerrors.NewBadRequestErr("invalid pool id format")
		mockOrch.EXPECT().UpdateRbacForPoolById(mock.Anything, &commonparams.RefreshRbacForPoolParams{PoolID: poolId}).Return("", badRequestErr)

		result, err := handler.V1RefreshRbacForExpertModePoolById(context.Background(), params)
		assert.NoError(t, err)

		badRequest, ok := result.(*oasgenserver.V1RefreshRbacForExpertModePoolByIdBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusBadRequest), badRequest.Code)
		assert.Contains(t, badRequest.Message, "invalid pool id")
		mockOrch.AssertExpectations(t)
	})

	t.Run("NotFoundError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		params := oasgenserver.V1RefreshRbacForExpertModePoolByIdParams{PoolId: poolId}

		notFoundErr := customerrors.NewNotFoundErr("pool", &poolId)
		mockOrch.EXPECT().UpdateRbacForPoolById(mock.Anything, &commonparams.RefreshRbacForPoolParams{PoolID: poolId}).Return("", notFoundErr)

		result, err := handler.V1RefreshRbacForExpertModePoolById(context.Background(), params)
		assert.NoError(t, err)

		notFound, ok := result.(*oasgenserver.V1RefreshRbacForExpertModePoolByIdNotFound)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusNotFound), notFound.Code)
		mockOrch.AssertExpectations(t)
	})

	t.Run("InternalServerError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		params := oasgenserver.V1RefreshRbacForExpertModePoolByIdParams{PoolId: poolId}

		internalErr := errors.New("temporal connection failed")
		mockOrch.EXPECT().UpdateRbacForPoolById(mock.Anything, &commonparams.RefreshRbacForPoolParams{PoolID: poolId}).Return("", internalErr)

		result, err := handler.V1RefreshRbacForExpertModePoolById(context.Background(), params)
		assert.NoError(t, err)

		ise, ok := result.(*oasgenserver.V1RefreshRbacForExpertModePoolByIdInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusInternalServerError), ise.Code)
		assert.Contains(t, ise.Message, "temporal connection failed")
		mockOrch.AssertExpectations(t)
	})
}

func TestV1RefreshRbacForExpertModePools(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		jobID := "test-job-uuid-12345"
		params := oasgenserver.V1RefreshRbacForExpertModePoolsParams{}

		// Set up expectations
		mockOrch.EXPECT().UpdateRbacForPools(mock.Anything).Return(jobID, nil)

		// Execute
		ctx := context.Background()
		result, err := handler.V1RefreshRbacForExpertModePools(ctx, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)

		operation, ok := result.(*oasgenserver.OperationV1)
		assert.True(t, ok, "Expected OperationV1 response")
		assert.False(t, operation.Done.Or(true), "Operation should not be done")
		expectedOperationName := fmt.Sprintf("/v1/expertMode/rbac/refresh/operations/%s", jobID)
		assert.Equal(t, expectedOperationName, operation.Name.Or(""))

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})

	t.Run("BadRequestError", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		params := oasgenserver.V1RefreshRbacForExpertModePoolsParams{}

		// Set up expectations - orchestrator returns bad request error
		badRequestErr := customerrors.NewBadRequestErr("invalid request: missing required parameters")
		mockOrch.EXPECT().UpdateRbacForPools(mock.Anything).Return("", badRequestErr)

		// Execute
		ctx := context.Background()
		result, err := handler.V1RefreshRbacForExpertModePools(ctx, params)

		// Assert
		assert.NoError(t, err) // Handler converts error to response
		assert.NotNil(t, result)

		badRequest, ok := result.(*oasgenserver.V1RefreshRbacForExpertModePoolsBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusBadRequest), badRequest.Code)
		assert.Contains(t, badRequest.Message, "invalid request")

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})

	t.Run("InternalServerError_WorkflowExecutionFailed", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		params := oasgenserver.V1RefreshRbacForExpertModePoolsParams{}

		// Set up expectations - orchestrator returns generic error (not BadRequest)
		workflowErr := errors.New("failed to start RBAC update workflow: temporal connection error")
		mockOrch.EXPECT().UpdateRbacForPools(mock.Anything).Return("", workflowErr)

		// Execute
		ctx := context.Background()
		result, err := handler.V1RefreshRbacForExpertModePools(ctx, params)

		// Assert
		assert.NoError(t, err) // Handler converts error to response
		assert.NotNil(t, result)

		internalError, ok := result.(*oasgenserver.V1RefreshRbacForExpertModePoolsInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusInternalServerError), internalError.Code)
		assert.Contains(t, internalError.Message, "failed to start RBAC update workflow")

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})

	t.Run("InternalServerError_JobCreationFailed", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		params := oasgenserver.V1RefreshRbacForExpertModePoolsParams{}

		// Set up expectations - orchestrator returns database error
		jobCreationErr := errors.New("failed to create job: database connection failed")
		mockOrch.EXPECT().UpdateRbacForPools(mock.Anything).Return("", jobCreationErr)

		// Execute
		ctx := context.Background()
		result, err := handler.V1RefreshRbacForExpertModePools(ctx, params)

		// Assert
		assert.NoError(t, err) // Handler converts error to response
		assert.NotNil(t, result)

		internalError, ok := result.(*oasgenserver.V1RefreshRbacForExpertModePoolsInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusInternalServerError), internalError.Code)
		assert.Contains(t, internalError.Message, "failed to create job")

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})

	t.Run("Success_WithCorrelationID", func(t *testing.T) {
		// Setup
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		// Test data
		jobID := "test-job-uuid-67890"
		correlationID := "test-correlation-id-12345"
		params := oasgenserver.V1RefreshRbacForExpertModePoolsParams{
			XCorrelationID: oasgenserver.NewOptString(correlationID),
		}

		// Set up expectations
		mockOrch.EXPECT().UpdateRbacForPools(mock.Anything).Return(jobID, nil)

		// Execute with correlation ID in params
		ctx := context.Background()
		result, err := handler.V1RefreshRbacForExpertModePools(ctx, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)

		operation, ok := result.(*oasgenserver.OperationV1)
		assert.True(t, ok, "Expected OperationV1 response")
		assert.False(t, operation.Done.Or(true), "Operation should not be done")
		expectedOperationName := fmt.Sprintf("/v1/expertMode/rbac/refresh/operations/%s", jobID)
		assert.Equal(t, expectedOperationName, operation.Name.Or(""))

		// Verify mock expectations
		mockOrch.AssertExpectations(t)
	})
}
