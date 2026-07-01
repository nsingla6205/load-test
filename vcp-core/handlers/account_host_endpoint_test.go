package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

const (
	testAccountName = "123456789"
	testHostUUIDA     = "9760acf5-4638-11e7-9bdb-020073ca3333"
	testHostUUIDB     = "7a8b1c0e-1234-5678-90ab-cdef12345678"
	testPinJobID      = "d3456789-01ab-cdef-0123-456789abcdef"
)

func TestV1SetAccountHosts(t *testing.T) {
	t.Run("WhenSuccessful", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.SetAccountHostsRequestV1{
			HostUuids: []string{testHostUUIDA, testHostUUIDB},
		}

		mockOrch.On("PinAccountHosts", mock.Anything, mock.MatchedBy(func(p *common.PinAccountHostsParams) bool {
			return p != nil && p.AccountName == testAccountName && len(p.HostUUIDs) == 2 &&
				p.HostUUIDs[0] == testHostUUIDA && p.HostUUIDs[1] == testHostUUIDB
		})).Return(&models.SetAccountHostsResponse{
			Status: string(datamodel.JobsStatePROCESSING),
			JobID:  testPinJobID,
		}, nil)

		result, err := handler.V1SetAccountHosts(context.Background(), req, oasgenserver.V1SetAccountHostsParams{
			AccountName: testAccountName,
		})
		require.NoError(t, err)
		res, ok := result.(*oasgenserver.SetAccountHostsResponseV1)
		require.True(t, ok)
		assert.Equal(t, testPinJobID, res.JobId)
		assert.Equal(t, oasgenserver.SetAccountHostsResponseV1Status(datamodel.JobsStatePROCESSING), res.Status)
	})

	t.Run("WhenEmptyHostUUIDsUnpinAll", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.SetAccountHostsRequestV1{HostUuids: []string{}}

		mockOrch.On("PinAccountHosts", mock.Anything, mock.MatchedBy(func(p *common.PinAccountHostsParams) bool {
			return p != nil && p.AccountName == testAccountName && len(p.HostUUIDs) == 0
		})).Return(&models.SetAccountHostsResponse{
			Status: string(datamodel.JobsStateDONE),
			JobID:  testPinJobID,
		}, nil)

		result, err := handler.V1SetAccountHosts(context.Background(), req, oasgenserver.V1SetAccountHostsParams{
			AccountName: testAccountName,
		})
		require.NoError(t, err)
		_, ok := result.(*oasgenserver.SetAccountHostsResponseV1)
		assert.True(t, ok)
	})

	t.Run("WhenNilBody", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		result, err := handler.V1SetAccountHosts(context.Background(), nil, oasgenserver.V1SetAccountHostsParams{
			AccountName: testAccountName,
		})
		require.NoError(t, err)
		_, ok := result.(*oasgenserver.V1SetAccountHostsBadRequest)
		assert.True(t, ok)
	})

	t.Run("WhenMissingAccountName", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.SetAccountHostsRequestV1{HostUuids: []string{testHostUUIDA}}
		result, err := handler.V1SetAccountHosts(context.Background(), req, oasgenserver.V1SetAccountHostsParams{})
		require.NoError(t, err)
		_, ok := result.(*oasgenserver.V1SetAccountHostsBadRequest)
		assert.True(t, ok)
	})

	t.Run("WhenHostNotFoundReturns404", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.SetAccountHostsRequestV1{HostUuids: []string{testHostUUIDA}}

		mockOrch.On("PinAccountHosts", mock.Anything, mock.MatchedBy(func(p *common.PinAccountHostsParams) bool {
			return p != nil && p.AccountName == testAccountName && len(p.HostUUIDs) == 1 && p.HostUUIDs[0] == testHostUUIDA
		})).Return(nil, customerrors.NewNotFoundErr("ExternalCluster", ptr(testHostUUIDA)))

		result, err := handler.V1SetAccountHosts(context.Background(), req, oasgenserver.V1SetAccountHostsParams{
			AccountName: testAccountName,
		})
		require.NoError(t, err)
		_, ok := result.(*oasgenserver.V1SetAccountHostsNotFound)
		assert.True(t, ok)
	})

	t.Run("WhenDuplicateAccountHostMappingReturns409", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.SetAccountHostsRequestV1{HostUuids: []string{testHostUUIDA}}
		dupErr := vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("account host mapping for account %d and host %q already exists", 42, testHostUUIDA))

		mockOrch.On("PinAccountHosts", mock.Anything, mock.MatchedBy(func(p *common.PinAccountHostsParams) bool {
			return p != nil && p.AccountName == testAccountName
		})).Return(nil, dupErr)

		result, err := handler.V1SetAccountHosts(context.Background(), req, oasgenserver.V1SetAccountHostsParams{
			AccountName: testAccountName,
		})
		require.NoError(t, err)
		res, ok := result.(*oasgenserver.V1SetAccountHostsConflict)
		require.True(t, ok)
		assert.Equal(t, float64(409), res.Code)
		assert.NotEmpty(t, res.Message)
	})

	t.Run("WhenOperationInProgressReturns409", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.SetAccountHostsRequestV1{HostUuids: []string{testHostUUIDA}}

		mockOrch.On("PinAccountHosts", mock.Anything, mock.MatchedBy(func(p *common.PinAccountHostsParams) bool {
			return p != nil && p.AccountName == testAccountName
		})).Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, errors.New("operation in progress")))

		result, err := handler.V1SetAccountHosts(context.Background(), req, oasgenserver.V1SetAccountHostsParams{
			AccountName: testAccountName,
		})
		require.NoError(t, err)
		_, ok := result.(*oasgenserver.V1SetAccountHostsConflict)
		assert.True(t, ok)
	})

	t.Run("WhenOrchestratorBadRequestReturns400", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.SetAccountHostsRequestV1{HostUuids: []string{testHostUUIDA}}

		mockOrch.On("PinAccountHosts", mock.Anything, mock.MatchedBy(func(p *common.PinAccountHostsParams) bool {
			return p != nil && p.AccountName == testAccountName
		})).Return(nil, customerrors.NewBadRequestErr("bad host"))

		result, err := handler.V1SetAccountHosts(context.Background(), req, oasgenserver.V1SetAccountHostsParams{
			AccountName: testAccountName,
		})
		require.NoError(t, err)
		_, ok := result.(*oasgenserver.V1SetAccountHostsBadRequest)
		assert.True(t, ok)
	})

	t.Run("WhenNotImplementedReturns500", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.SetAccountHostsRequestV1{HostUuids: []string{testHostUUIDA}}

		mockOrch.On("PinAccountHosts", mock.Anything, mock.MatchedBy(func(p *common.PinAccountHostsParams) bool {
			return p != nil && p.AccountName == testAccountName
		})).Return(nil, customerrors.NewNotImplementedYetErr())

		result, err := handler.V1SetAccountHosts(context.Background(), req, oasgenserver.V1SetAccountHostsParams{
			AccountName: testAccountName,
		})
		require.NoError(t, err)
		_, ok := result.(*oasgenserver.V1SetAccountHostsInternalServerError)
		assert.True(t, ok)
	})

	t.Run("WhenInternalErrorDoesNotLeakDetails", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		req := &oasgenserver.SetAccountHostsRequestV1{HostUuids: []string{testHostUUIDA}}

		mockOrch.On("PinAccountHosts", mock.Anything, mock.MatchedBy(func(p *common.PinAccountHostsParams) bool {
			return p != nil && p.AccountName == testAccountName
		})).Return(nil, errors.New("database connection refused: host=postgres.internal"))

		result, err := handler.V1SetAccountHosts(context.Background(), req, oasgenserver.V1SetAccountHostsParams{
			AccountName: testAccountName,
		})
		require.NoError(t, err)
		res, ok := result.(*oasgenserver.V1SetAccountHostsInternalServerError)
		require.True(t, ok)
		assert.Equal(t, "Failed to set account hosts", res.Message)
		assert.NotContains(t, res.Message, "postgres")
		assert.NotContains(t, res.Message, "database connection refused")
	})
}

func ptr(s string) *string { return &s }
