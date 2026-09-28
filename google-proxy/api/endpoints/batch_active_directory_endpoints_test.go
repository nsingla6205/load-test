package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func makeVCPAD(uuid, resourceID, state string) *models.ActiveDirectory {
	return &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: uuid, CreatedAt: time.Now()},
		AdName:    resourceID,
		Username:  "admin",
		State:     state,
		Domain:    "example.com",
		DNS:       "10.0.0.1",
		NetBIOS:   "NB",
		ActiveDirectoryAttributes: &models.ActiveDirectoryAttributes{
			OrganizationalUnit: "OU=Computers",
			Site:               "Default",
		},
	}
}

func TestV1betaBatchListActiveDirectories_Auth(t *testing.T) {
	t.Run("InvalidJWT_ReturnsUnauthorized", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(false)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		logger := log.NewLogger()
		ctx := context.WithValue(context.Background(), utilsmiddleware.ContextSLoggerKey, logger)
		ctx = context.WithValue(ctx, utilsmiddleware.HeaderContextKey, http.Header{
			"Authorization": []string{"invalid-jwt-token"},
		})

		req := &gcpgenserver.BatchActiveDirectoryUUIDListV1beta{ActiveDirectoryUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListActiveDirectoriesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListActiveDirectories(ctx, req, params)
		require.NoError(tt, err)
		unauthRes, ok := res.(*gcpgenserver.V1betaBatchListActiveDirectoriesUnauthorized)
		require.True(tt, ok, "expected Unauthorized response")
		assert.Equal(tt, float64(http.StatusUnauthorized), unauthRes.Code)
		assert.Equal(tt, "Authentication failure", unauthRes.Message)
	})

	t.Run("NilHTTPRequest_ReturnsUnauthorized", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchActiveDirectoryUUIDListV1beta{ActiveDirectoryUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListActiveDirectoriesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListActiveDirectories(context.Background(), req, params)
		require.NoError(tt, err)
		unauthRes, ok := res.(*gcpgenserver.V1betaBatchListActiveDirectoriesUnauthorized)
		require.True(tt, ok)
		assert.Equal(tt, float64(http.StatusUnauthorized), unauthRes.Code)
	})
}

func TestV1betaBatchListActiveDirectories_Validation(t *testing.T) {
	t.Run("InvalidLocation_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchActiveDirectoryUUIDListV1beta{ActiveDirectoryUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListActiveDirectoriesParams{LocationId: "invalid location!"}

		res, err := handler.V1betaBatchListActiveDirectories(authContext(), req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListActiveDirectoriesBadRequest)
		assert.True(tt, ok)
	})

	t.Run("NilActiveDirectoryUuids_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchActiveDirectoryUUIDListV1beta{ActiveDirectoryUuids: nil}
		params := gcpgenserver.V1betaBatchListActiveDirectoriesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListActiveDirectories(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListActiveDirectoriesBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "activeDirectoryUuids is required")
	})

	t.Run("EmptyActiveDirectoryUuids_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchActiveDirectoryUUIDListV1beta{ActiveDirectoryUuids: []string{}}
		params := gcpgenserver.V1betaBatchListActiveDirectoriesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListActiveDirectories(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListActiveDirectoriesBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "activeDirectoryUuids is required")
	})

	t.Run("TooManyUuids_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		uuids := make([]string, 1001)
		for i := range uuids {
			uuids[i] = "uuid"
		}
		req := &gcpgenserver.BatchActiveDirectoryUUIDListV1beta{ActiveDirectoryUuids: uuids}
		params := gcpgenserver.V1betaBatchListActiveDirectoriesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListActiveDirectories(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListActiveDirectoriesBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "at most 1000")
	})
}

func TestV1betaBatchListActiveDirectories_Success(t *testing.T) {
	t.Run("WithFields", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		ad := makeVCPAD("ad-1", "my-ad", datamodel.LifeCycleStateREADY)
		mockOrch := factory.NewMockOrchestratorFactory(tt)
		mockOrch.On("BatchListActiveDirectories", mock.Anything, mock.MatchedBy(func(p *commonparams.BatchListADsParams) bool {
			return p != nil && p.LocationID == "us-east4" &&
				len(p.Fields) == 2 &&
				p.Fields[0] == "resourceId" &&
				p.Fields[1] == "activeDirectoryState"
		})).Return([]*models.ActiveDirectory{ad}, nil)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		req := &gcpgenserver.BatchActiveDirectoryUUIDListV1beta{ActiveDirectoryUuids: []string{"ad-1"}}
		params := gcpgenserver.V1betaBatchListActiveDirectoriesParams{
			LocationId: "us-east4",
			Fields: []gcpgenserver.V1betaBatchListActiveDirectoriesFieldsItem{
				gcpgenserver.V1betaBatchListActiveDirectoriesFieldsItemResourceId,
				gcpgenserver.V1betaBatchListActiveDirectoriesFieldsItemActiveDirectoryState,
			},
		}

		res, err := handler.V1betaBatchListActiveDirectories(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListActiveDirectoriesOK)
		require.True(tt, ok)
		require.Len(tt, okRes.ActiveDirectories, 1)
		assert.True(tt, okRes.ActiveDirectories[0].ActiveDirectoryId.Set)
		assert.Equal(tt, "ad-1", okRes.ActiveDirectories[0].ActiveDirectoryId.Value)
		assert.Equal(tt, "my-ad", okRes.ActiveDirectories[0].ResourceId.Value)
		assert.Equal(tt, gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryStateREADY,
			okRes.ActiveDirectories[0].ActiveDirectoryState.Value)
	})

	t.Run("NoFieldsRequested_ReturnsOnlyActiveDirectoryId", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		ad := makeVCPAD("ad-1", "my-ad", datamodel.LifeCycleStateREADY)
		mockOrch := factory.NewMockOrchestratorFactory(tt)
		mockOrch.On("BatchListActiveDirectories", mock.Anything, mock.Anything).
			Return([]*models.ActiveDirectory{ad}, nil)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		req := &gcpgenserver.BatchActiveDirectoryUUIDListV1beta{ActiveDirectoryUuids: []string{"ad-1"}}
		params := gcpgenserver.V1betaBatchListActiveDirectoriesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListActiveDirectories(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListActiveDirectoriesOK)
		require.True(tt, ok)
		require.Len(tt, okRes.ActiveDirectories, 1)
		assert.Equal(tt, "ad-1", okRes.ActiveDirectories[0].ActiveDirectoryId.Value)
		assert.False(tt, okRes.ActiveDirectories[0].ResourceId.Set)
		assert.False(tt, okRes.ActiveDirectories[0].ActiveDirectoryState.Set)
	})

	t.Run("OrchestratorFails_Returns500", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		mockOrch.On("BatchListActiveDirectories", mock.Anything, mock.Anything).
			Return(nil, errors.New("internal error"))
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		req := &gcpgenserver.BatchActiveDirectoryUUIDListV1beta{ActiveDirectoryUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListActiveDirectoriesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListActiveDirectories(ctx, req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListActiveDirectoriesInternalServerError)
		assert.True(tt, ok)
	})
}

func TestV1betaBatchListActiveDirectories_SkipsDeletedADs(t *testing.T) {
	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()

	deletedAt := time.Now()
	activeAD := makeVCPAD("ad-active", "active-ad", datamodel.LifeCycleStateREADY)
	deletedAD := makeVCPAD("ad-deleted", "deleted-ad", datamodel.LifeCycleStateDeleted)
	deletedAD.DeletedAt = &deletedAt

	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.On("BatchListActiveDirectories", mock.Anything, mock.Anything).
		Return([]*models.ActiveDirectory{activeAD, deletedAD}, nil)
	handler := &Handler{Orchestrator: mockOrch}
	ctx := authContext()

	req := &gcpgenserver.BatchActiveDirectoryUUIDListV1beta{ActiveDirectoryUuids: []string{"ad-active", "ad-deleted"}}
	params := gcpgenserver.V1betaBatchListActiveDirectoriesParams{LocationId: "us-east4"}

	res, err := handler.V1betaBatchListActiveDirectories(ctx, req, params)
	require.NoError(t, err)
	okRes, ok := res.(*gcpgenserver.V1betaBatchListActiveDirectoriesOK)
	require.True(t, ok)
	require.Len(t, okRes.ActiveDirectories, 1, "deleted AD must be filtered out")
	assert.Equal(t, "ad-active", okRes.ActiveDirectories[0].ActiveDirectoryId.Value)
}

func TestConvertADToBatchAD_UnknownStateDefaultsToStateUnspecified(t *testing.T) {
	ad := makeVCPAD("ad-x", "res", "NOT_A_VALID_API_STATE")
	fieldSet := map[string]bool{
		"activeDirectoryState": true,
	}
	ba := convertADToBatchAD(ad, fieldSet)
	require.True(t, ba.ActiveDirectoryState.Set)
	assert.False(t, ba.ActiveDirectoryState.Null)
	assert.Equal(t, gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryStateSTATEUNSPECIFIED, ba.ActiveDirectoryState.Value)
}

func TestConvertADToBatchAD_EmptyStateWithFieldRequested_IsStateUnspecified(t *testing.T) {
	ad := makeVCPAD("ad-x", "res", "")
	fieldSet := map[string]bool{
		"activeDirectoryState": true,
	}
	ba := convertADToBatchAD(ad, fieldSet)
	require.True(t, ba.ActiveDirectoryState.Set)
	assert.False(t, ba.ActiveDirectoryState.Null)
	assert.Equal(t, gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryStateSTATEUNSPECIFIED, ba.ActiveDirectoryState.Value)
}

func TestBoolFields_DefaultFalseAndTrueHonored(t *testing.T) {
	t.Run("DefaultFalse", func(t *testing.T) {
		ad := makeVCPAD("ad-x", "res", datamodel.LifeCycleStateREADY)
		fieldSet := map[string]bool{
			"encryptDCConnections": true, "aesEncryption": true,
			"ldapSigning": true, "allowLocalNFSUsersWithLdap": true,
		}
		ba := convertADToBatchAD(ad, fieldSet)
		assert.True(t, ba.EncryptDCConnections.Set && !ba.EncryptDCConnections.Null)
		assert.False(t, ba.EncryptDCConnections.Value)
		assert.True(t, ba.AesEncryption.Set && !ba.AesEncryption.Null)
		assert.False(t, ba.AesEncryption.Value)
		assert.True(t, ba.LdapSigning.Set && !ba.LdapSigning.Null)
		assert.False(t, ba.LdapSigning.Value)
		assert.True(t, ba.AllowLocalNFSUsersWithLdap.Set && !ba.AllowLocalNFSUsersWithLdap.Null)
		assert.False(t, ba.AllowLocalNFSUsersWithLdap.Value)
	})

	t.Run("TrueBoolsReturnTrue", func(t *testing.T) {
		ad := makeVCPAD("ad-x", "res", datamodel.LifeCycleStateREADY)
		ad.ActiveDirectoryAttributes.EncryptDCConnections = true
		ad.ActiveDirectoryAttributes.AesEncryption = true
		ad.ActiveDirectoryAttributes.LdapSigning = true
		ad.ActiveDirectoryAttributes.AllowLocalNFSUsersWithLdap = true
		fieldSet := map[string]bool{
			"encryptDCConnections": true, "aesEncryption": true,
			"ldapSigning": true, "allowLocalNFSUsersWithLdap": true,
		}
		ba := convertADToBatchAD(ad, fieldSet)
		assert.True(t, ba.EncryptDCConnections.Set && !ba.EncryptDCConnections.Null)
		assert.True(t, ba.EncryptDCConnections.Value)
		assert.True(t, ba.AesEncryption.Set && !ba.AesEncryption.Null)
		assert.True(t, ba.AesEncryption.Value)
		assert.True(t, ba.LdapSigning.Set && !ba.LdapSigning.Null)
		assert.True(t, ba.LdapSigning.Value)
		assert.True(t, ba.AllowLocalNFSUsersWithLdap.Set && !ba.AllowLocalNFSUsersWithLdap.Null)
		assert.True(t, ba.AllowLocalNFSUsersWithLdap.Value)
	})
}

func TestEmptyArrayFields_SerializeAsNull(t *testing.T) {
	t.Run("EmptySlices", func(t *testing.T) {
		ad := makeVCPAD("ad-x", "res", datamodel.LifeCycleStateREADY)
		ad.ActiveDirectoryAttributes.BackupOperators = []string{}
		ad.ActiveDirectoryAttributes.SecurityOperators = []string{}
		ad.ActiveDirectoryAttributes.Administrators = []string{}
		fieldSet := map[string]bool{
			"backupOperators": true, "securityOperators": true, "administrators": true,
		}
		ba := convertADToBatchAD(ad, fieldSet)
		require.True(t, ba.BackupOperators.Set)
		require.True(t, ba.SecurityOperators.Set)
		require.True(t, ba.Administrators.Set)
		assert.True(t, ba.BackupOperators.Null)
		assert.True(t, ba.SecurityOperators.Null)
		assert.True(t, ba.Administrators.Null)
	})

	t.Run("NonEmptySlicesUnchanged", func(t *testing.T) {
		ad := makeVCPAD("ad-x", "res", datamodel.LifeCycleStateREADY)
		ad.ActiveDirectoryAttributes.BackupOperators = []string{"op1"}
		fieldSet := map[string]bool{"backupOperators": true}
		ba := convertADToBatchAD(ad, fieldSet)
		require.True(t, ba.BackupOperators.Set)
		assert.False(t, ba.BackupOperators.Null)
		assert.Equal(t, []string{"op1"}, ba.BackupOperators.Value)
	})
}

func TestConvertADToBatchAD_AllAttributeFields(t *testing.T) {
	ad := &models.ActiveDirectory{
		BaseModel:    models.BaseModel{UUID: "ad-full", CreatedAt: time.Now()},
		AdName:       "full-ad",
		Username:     "admin",
		State:        datamodel.LifeCycleStateREADY,
		StateDetails: "All good",
		Domain:       "example.com",
		DNS:          "10.0.0.1",
		NetBIOS:      "NB",
		ActiveDirectoryAttributes: &models.ActiveDirectoryAttributes{
			OrganizationalUnit:         "OU=Computers",
			Site:                       "Default",
			KdcIP:                      "10.0.0.2",
			KdcHostname:                "kdc.example.com",
			EncryptDCConnections:       true,
			AesEncryption:              true,
			LdapSigning:                true,
			AllowLocalNFSUsersWithLdap: true,
			Description:                "Full AD entry",
			BackupOperators:            []string{"backup-op1"},
			SecurityOperators:          []string{"sec-op1"},
			Administrators:             []string{"admin1"},
		},
	}
	fieldSet := map[string]bool{
		"resourceId": true, "username": true, "password": true,
		"domain": true, "DNS": true, "netBIOS": true,
		"organizationalUnit": true, "site": true,
		"kdcIP": true, "kdcHostname": true,
		"encryptDCConnections": true, "aesEncryption": true,
		"ldapSigning": true, "allowLocalNFSUsersWithLdap": true,
		"description":     true,
		"backupOperators": true, "securityOperators": true, "administrators": true,
		"activeDirectoryState": true, "activeDirectoryStateDetails": true, "createdAt": true,
	}

	ba := convertADToBatchAD(ad, fieldSet)

	assert.Equal(t, "10.0.0.2", ba.KdcIP.Value)
	assert.Equal(t, "kdc.example.com", ba.KdcHostname.Value)
	assert.Equal(t, "Full AD entry", ba.Description.Value)
	assert.Equal(t, []string{"sec-op1"}, ba.SecurityOperators.Value)
	assert.False(t, ba.SecurityOperators.Null)
	assert.Equal(t, []string{"admin1"}, ba.Administrators.Value)
	assert.False(t, ba.Administrators.Null)
	assert.Equal(t, []string{"backup-op1"}, ba.BackupOperators.Value)
	assert.False(t, ba.BackupOperators.Null)
}

func TestEnsureRequestedADFieldsPresent_SetsUnsetFieldsToNull(t *testing.T) {
	ba := gcpgenserver.BatchActiveDirectoryV1beta{}
	fieldSet := map[string]bool{
		"resourceId": true, "username": true, "password": true,
		"domain": true, "DNS": true, "netBIOS": true,
		"organizationalUnit": true, "site": true,
		"kdcIP": true, "kdcHostname": true,
		"activeDirectoryState": true, "activeDirectoryStateDetails": true,
		"createdAt":            true,
		"encryptDCConnections": true, "backupOperators": true,
		"aesEncryption": true, "ldapSigning": true,
		"securityOperators": true, "allowLocalNFSUsersWithLdap": true,
		"description": true, "administrators": true,
	}

	ensureRequestedADFieldsPresent(&ba, fieldSet)

	assert.True(t, ba.ResourceId.Set && ba.ResourceId.Null)
	assert.True(t, ba.Username.Set && ba.Username.Null)
	assert.True(t, ba.Password.Set && ba.Password.Null)
	assert.True(t, ba.Domain.Set && ba.Domain.Null)
	assert.True(t, ba.DNS.Set && ba.DNS.Null)
	assert.True(t, ba.NetBIOS.Set && ba.NetBIOS.Null)
	assert.True(t, ba.OrganizationalUnit.Set && ba.OrganizationalUnit.Null)
	assert.True(t, ba.Site.Set && ba.Site.Null)
	assert.True(t, ba.KdcIP.Set && ba.KdcIP.Null)
	assert.True(t, ba.KdcHostname.Set && ba.KdcHostname.Null)
	assert.True(t, ba.ActiveDirectoryState.Set && ba.ActiveDirectoryState.Null)
	assert.True(t, ba.ActiveDirectoryStateDetails.Set && ba.ActiveDirectoryStateDetails.Null)
	assert.True(t, ba.CreatedAt.Set && ba.CreatedAt.Null)
	assert.True(t, ba.EncryptDCConnections.Set && ba.EncryptDCConnections.Null)
	assert.True(t, ba.BackupOperators.Set && ba.BackupOperators.Null)
	assert.True(t, ba.AesEncryption.Set && ba.AesEncryption.Null)
	assert.True(t, ba.LdapSigning.Set && ba.LdapSigning.Null)
	assert.True(t, ba.SecurityOperators.Set && ba.SecurityOperators.Null)
	assert.True(t, ba.AllowLocalNFSUsersWithLdap.Set && ba.AllowLocalNFSUsersWithLdap.Null)
	assert.True(t, ba.Description.Set && ba.Description.Null)
	assert.True(t, ba.Administrators.Set && ba.Administrators.Null)
}

func TestEnsureRequestedADFieldsPresent_NilFieldSet(t *testing.T) {
	ba := gcpgenserver.BatchActiveDirectoryV1beta{
		ResourceId: gcpgenserver.NewOptNilString("should-stay"),
	}
	ensureRequestedADFieldsPresent(&ba, nil)
	assert.True(t, ba.ResourceId.Set)
	assert.Equal(t, "should-stay", ba.ResourceId.Value)
}
