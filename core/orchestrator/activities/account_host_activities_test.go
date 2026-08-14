package activities

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbdatamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/testsuite"
)

const (
	testAccountHostRoleName    = "gcnv_r_abcdefghij"
	testAccountHostServiceUser = "gcnv_u_abcdefghij"
)

func testProviderForHost(_ *testing.T) func(context.Context, *dbdatamodel.Host) (vsa.Provider, error) {
	return func(ctx context.Context, _ *dbdatamodel.Host) (vsa.Provider, error) {
		return vsa.NewProvider(ctx, vsa.ProviderDetails{IPAddress: "10.10.10.50"}), nil
	}
}

func generateAccountHostServicePasswordForTest(t *testing.T, cred *datamodel.ExpertModeCredential) (string, error) {
	t.Helper()
	return generateAccountHostServicePassword(context.Background(), cred)
}

func newAccountHostMonkeyMockAndPatch(t *testing.T) *monkeyMock {
	mm := newMonkeyMock(t)

	accountHostGetGCPService = mm.accountHostGetGCPService
	accountHostGetProviderForHost = mm.accountHostGetProviderForHost
	accountHostVerifyHostServiceCredentials = mm.accountHostVerifyHostServiceCredentials
	verifySAWithCertAuth = mm.verifySAWithCertAuth
	accountHostEnsureExpertModeRole = mm.accountHostEnsureExpertModeRole
	accountHostEnsureExpertModeAccount = mm.accountHostEnsureExpertModeAccount
	createSAWithCertAuth = mm.createSAWithCertAuth
	accountHostDeleteExpertModeAccount = mm.accountHostDeleteExpertModeAccount
	accountHostDeleteExpertModeRole = mm.accountHostDeleteExpertModeRole
	accountHostGetRoleOwnerUUID = mm.accountHostGetRoleOwnerUUID
	accountHostGetBucketFileContent = mm.accountHostGetBucketFileContent
	loadAccountHostRBACPrivileges = mm.loadAccountHostRBACPrivileges
	generateAccountHostServicePassword = mm.generateAccountHostServicePassword
	generateClientCertificate = mm.generateClientCertificate
	revokeAccountHostClientCertificate = mm.revokeAccountHostClientCertificate

	t.Cleanup(func() {
		accountHostGetGCPService = _accountHostGetGCPService
		accountHostGetProviderForHost = vsa.GetProviderForHost
		accountHostVerifyHostServiceCredentials = vsa.VerifyHostServiceCredentials
		verifySAWithCertAuth = vsa.VerifyHostServiceCredentialsWithCertificate
		accountHostEnsureExpertModeRole = vsa.EnsureExpertModeRole
		accountHostEnsureExpertModeAccount = vsa.EnsureExpertModeAccount
		createSAWithCertAuth = vsa.EnsureExpertModeSAWithCertAuth
		accountHostDeleteExpertModeAccount = vsa.DeleteExpertModeAccount
		accountHostDeleteExpertModeRole = vsa.DeleteExpertModeRole
		accountHostGetRoleOwnerUUID = getAccountHostRoleOwnerUUID
		accountHostGetBucketFileContent = _accountHostGetBucketFileContent
		loadAccountHostRBACPrivileges = _loadAccountHostRBACPrivilegesFromBucket
		generateAccountHostServicePassword = _generateAccountHostServicePassword
		generateClientCertificate = vsa.GenerateAndCreateCertificateForVSACluster
		revokeAccountHostClientCertificate = vsa.RevokeCertificateAndDeleteFromCacheAndSecretManager
		accountHostBuildCertificatePoolCredentials = orchcommon.BuildAccountHostCertificatePoolCredentials
	})

	return mm
}

func TestPrepareAccountHostMapping_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &AccountHostActivity{SE: mockStorage}

	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	baseHash := orchcommon.DeriveAccountHostBaseHash(mappingUUID)
	expectedRole := orchcommon.BuildAccountHostRoleName(baseHash)
	expectedUser := orchcommon.BuildAccountHostUsername(baseHash)

	mockStorage.On("GetAccountHost", mock.Anything, mappingUUID).Return(&datamodel.AccountHost{
		BaseModel: datamodel.BaseModel{UUID: mappingUUID},
		AccountID: 42,
		HostID:    hostUUID,
		Pinned:    false,
	}, nil)
	mockStorage.On("GetAccountByInternalID", mock.Anything, int64(42)).Return(&datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: accountUUID},
	}, nil)
	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(&datamodel.Host{
		BaseModel:  datamodel.BaseModel{UUID: hostUUID},
		IPAddress:  "10.10.10.50",
		Attributes: &datamodel.HostAttributes{Version: "9.18.1P2"},
	}, nil)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.PrepareAccountHostMapping)

	val, err := env.ExecuteActivity(activity.PrepareAccountHostMapping, mappingUUID)
	require.NoError(t, err)

	var prepared PreparedAccountHostMapping
	require.NoError(t, val.Get(&prepared))
	assert.Equal(t, mappingUUID, prepared.MappingUUID)
	assert.Equal(t, hostUUID, prepared.HostUUID)
	assert.Equal(t, expectedRole, prepared.RoleName)
	assert.Equal(t, expectedUser, prepared.AdminUsername)
	assert.Equal(t, orchcommon.BuildAccountHostSvmPattern(accountUUID), prepared.SvmPattern)
	assert.Equal(t, "9.18.1P2", prepared.OntapVersion)
}

func TestPrepareAccountHostMapping_ExtractsOntapVersionFromHostAttributes(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &AccountHostActivity{SE: mockStorage}

	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

	mockStorage.On("GetAccountHost", mock.Anything, mappingUUID).Return(&datamodel.AccountHost{
		BaseModel: datamodel.BaseModel{UUID: mappingUUID},
		AccountID: 42,
		HostID:    hostUUID,
		Pinned:    false,
	}, nil)
	mockStorage.On("GetAccountByInternalID", mock.Anything, int64(42)).Return(&datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: accountUUID},
	}, nil)
	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(&datamodel.Host{
		BaseModel: datamodel.BaseModel{UUID: hostUUID},
		IPAddress: "10.10.10.50",
		Attributes: &datamodel.HostAttributes{
			Version: "NetApp Release 9.17.1P9X3: Tue Jun 16 13:35:46 UTC 2026",
		},
	}, nil)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.PrepareAccountHostMapping)

	val, err := env.ExecuteActivity(activity.PrepareAccountHostMapping, mappingUUID)
	require.NoError(t, err)

	var prepared PreparedAccountHostMapping
	require.NoError(t, val.Get(&prepared))
	assert.Equal(t, "9.17.1P9X3", prepared.OntapVersion)
}

func TestBuildAccountHostSvmPattern(t *testing.T) {
	accountUUID := "414c2518-7ed2-569e-10c6-1df45d4db528"
	pattern := orchcommon.BuildAccountHostSvmPattern(accountUUID)
	assert.Equal(t, "svm_414c25187ed2569e10c61df45d4db528_*", pattern)
}

func TestCreateAccountHostRole_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)

	encryptedPassword, err := utils.EncryptPassword("admin-pass")
	require.NoError(t, err)

	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	roleName := testAccountHostRoleName
	roleOwnerUUID := "95be11a7-0000-0000-0000-000000000001"

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(&dbdatamodel.Host{
		BaseModel: dbdatamodel.BaseModel{UUID: hostUUID},
		Name:      "ontap-cluster",
		Password:  *encryptedPassword,
		Protocol:  dbdatamodel.ExternalClusterProtocolInsecureHTTPS,
		IPAddress: "10.10.10.50",
	}, nil)

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().loadAccountHostRBACPrivileges(mock.Anything, "9.18.1P2").Return([]orchcommon.ExpertModeRBACPrivilege{{
		Path:   "vserver modify",
		Access: "all",
		Query:  "-vserver <svm_name>",
	}}, nil)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).
		RunAndReturn(testProviderForHost(t))
	mm.EXPECT().accountHostEnsureExpertModeRole(mock.Anything, mock.Anything, testAccountHostRoleName, mock.Anything).
		RunAndReturn(func(_ context.Context, _ vsa.Provider, gotRoleName string, privileges []*vsa.RolePrivilege) error {
			assert.Equal(t, testAccountHostRoleName, gotRoleName)
			require.Len(t, privileges, 1)
			assert.Equal(t, "vserver modify", privileges[0].Path)
			return nil
		})
	mm.EXPECT().accountHostGetRoleOwnerUUID(mock.Anything, testAccountHostRoleName).Return(roleOwnerUUID, nil)

	activity := &AccountHostActivity{SE: mockStorage}

	prepared := &PreparedAccountHostMapping{
		MappingUUID:  mappingUUID,
		HostUUID:     hostUUID,
		RoleName:     roleName,
		SvmPattern:   orchcommon.BuildAccountHostSvmPattern(accountUUID),
		OntapVersion: "9.18.1P2",
	}

	var ts testsuite.WorkflowTestSuite
	roleEnv := ts.NewTestActivityEnvironment()
	roleEnv.RegisterActivity(activity.CreateAccountHostRole)

	val, err := roleEnv.ExecuteActivity(activity.CreateAccountHostRole, prepared)
	require.NoError(t, err)

	var updated PreparedAccountHostMapping
	require.NoError(t, val.Get(&updated))
	assert.Equal(t, roleOwnerUUID, updated.RoleOwnerUUID)
}

func TestCreateAccountHostRole_SubstitutesCLIQueriesWithAccountSvmPattern(t *testing.T) {
	mockStorage := database.NewMockStorage(t)

	encryptedPassword, err := utils.EncryptPassword("admin-pass")
	require.NoError(t, err)

	hostUUID := "f5fe77cc-c77e-8e51-2b45-e6b0729b180f"
	mappingUUID := "9fd615c9-2c8a-29a3-5dd3-a9aa97698256"
	accountUUID := "414c2518-7ed2-569e-10c6-1df45d4db528"
	roleName := "gcnv_r_8ed3a737"
	roleOwnerUUID := "b19f68eb-6f93-11f1-8ebf-4bc5bf846975"
	wantSvmPattern := orchcommon.BuildAccountHostSvmPattern(accountUUID)

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(&dbdatamodel.Host{
		BaseModel: dbdatamodel.BaseModel{UUID: hostUUID},
		Name:      "ext-cluster-34-41-242-212",
		Password:  *encryptedPassword,
		Protocol:  dbdatamodel.ExternalClusterProtocolInsecureHTTPS,
		IPAddress: "34.41.242.212",
	}, nil)

	var capturedPrivileges []*vsa.RolePrivilege
	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().loadAccountHostRBACPrivileges(mock.Anything, mock.Anything).
		Return([]orchcommon.ExpertModeRBACPrivilege{
			{Path: "job expunge", Access: "all", Query: "-vserver " + orchcommon.AccountHostRBACCLIQueryTemplate},
			{Path: "volume create", Access: "all", Query: "-vserver " + orchcommon.AccountHostRBACCLIQueryTemplate},
		}, nil)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).
		RunAndReturn(testProviderForHost(t))
	mm.EXPECT().accountHostEnsureExpertModeRole(mock.Anything, mock.Anything, roleName, mock.Anything).
		RunAndReturn(func(_ context.Context, _ vsa.Provider, gotRoleName string, privileges []*vsa.RolePrivilege) error {
			assert.Equal(t, roleName, gotRoleName)
			capturedPrivileges = privileges
			return nil
		})
	mm.EXPECT().accountHostGetRoleOwnerUUID(mock.Anything, mock.Anything).Return(roleOwnerUUID, nil)

	activity := &AccountHostActivity{SE: mockStorage}

	prepared := &PreparedAccountHostMapping{
		MappingUUID:  mappingUUID,
		HostUUID:     hostUUID,
		RoleName:     roleName,
		SvmPattern:   wantSvmPattern,
		OntapVersion: "9.18.1X29",
	}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostRole)

	_, err = env.ExecuteActivity(activity.CreateAccountHostRole, prepared)
	require.NoError(t, err)
	require.Len(t, capturedPrivileges, 2)
	assert.Equal(t, "-vserver "+wantSvmPattern, capturedPrivileges[0].Query)
	assert.Equal(t, "-vserver "+wantSvmPattern, capturedPrivileges[1].Query)
	assert.NotContains(t, capturedPrivileges[0].Query, orchcommon.AccountHostRBACCLIQueryTemplate)
}

func TestCreateAccountHostUser_Success(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)

	encryptedPassword, err := utils.EncryptPassword("admin-pass")
	require.NoError(t, err)

	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	roleName := testAccountHostRoleName
	expectedUsername := testAccountHostServiceUser
	roleOwnerUUID := "95be11a7-0000-0000-0000-000000000001"

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(&dbdatamodel.Host{
		BaseModel:    dbdatamodel.BaseModel{UUID: hostUUID},
		Name:         "ontap-cluster",
		ExternalName: "ontap-cluster",
		Password:     *encryptedPassword,
		Protocol:     dbdatamodel.ExternalClusterProtocolInsecureHTTPS,
		IPAddress:    "10.10.10.50",
		HostCredentials: &datamodel.PoolCredentials{
			AuthType: env.USER_CERTIFICATE,
			CaURI:    "project/pool/ca",
		},
	}, nil)
	mockStorage.On("UpdateAccountHostServiceCredentials", mock.Anything, mappingUUID, mock.MatchedBy(func(c *datamodel.ExpertModeCredential) bool {
		return c != nil && c.Username == testAccountHostServiceUser
	})).Return(nil)
	mockStorage.On("SetAccountHostPinned", mock.Anything, mappingUUID, true).Return(nil)

	cloudService := new(hyperscaler2.MockServices)
	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(cloudService, nil)
	mm.EXPECT().generateClientCertificate(
		cloudService, "ontap-cluster", expectedUsername, mock.Anything, false,
	).Return(&hyperscalermodels.CustomCertificateResponse{}, nil)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).
		RunAndReturn(testProviderForHost(t))
	mm.EXPECT().createSAWithCertAuth(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ vsa.ExpertModeAccountProvider, params vsa.CreateExpertModeAccountParams) error {
			assert.Equal(t, roleName, params.RoleName)
			assert.Equal(t, expectedUsername, params.Username)
			assert.Empty(t, params.Password)
			assert.Equal(t, roleOwnerUUID, params.ClusterUUID)
			return nil
		})
	mm.EXPECT().verifySAWithCertAuth(
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything,
	).RunAndReturn(func(
		_ context.Context,
		_ *dbdatamodel.Host,
		svmPattern, username string,
		_ *datamodel.PoolCredentials,
	) error {
		assert.Equal(t, orchcommon.BuildAccountHostSvmPattern(accountUUID), svmPattern)
		assert.Equal(t, expectedUsername, username)
		return nil
	})

	activity := &AccountHostActivity{SE: mockStorage}

	prepared := &PreparedAccountHostMapping{
		MappingUUID:   mappingUUID,
		HostUUID:      hostUUID,
		AdminUsername: expectedUsername,
		RoleName:      roleName,
		RoleOwnerUUID: roleOwnerUUID,
		SvmPattern:    orchcommon.BuildAccountHostSvmPattern(accountUUID),
	}

	var ts testsuite.WorkflowTestSuite
	userEnv := ts.NewTestActivityEnvironment()
	userEnv.RegisterActivity(activity.CreateAccountHostUser)

	_, err = userEnv.ExecuteActivity(activity.CreateAccountHostUser, prepared)
	require.NoError(t, err)
}

func TestCreateAccountHostUser_ExistingOntapUserRecovery(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)

	encryptedPassword, err := utils.EncryptPassword("admin-pass")
	require.NoError(t, err)

	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	expectedUsername := testAccountHostServiceUser
	roleOwnerUUID := "95be11a7-0000-0000-0000-000000000001"

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(&dbdatamodel.Host{
		BaseModel:    dbdatamodel.BaseModel{UUID: hostUUID},
		Name:         "ontap-cluster",
		ExternalName: "ontap-cluster",
		Password:     *encryptedPassword,
		Protocol:     dbdatamodel.ExternalClusterProtocolInsecureHTTPS,
		IPAddress:    "10.10.10.50",
		HostCredentials: &datamodel.PoolCredentials{
			AuthType: env.USER_CERTIFICATE,
			CaURI:    "project/pool/ca",
		},
	}, nil)
	mockStorage.On("UpdateAccountHostServiceCredentials", mock.Anything, mappingUUID, mock.MatchedBy(func(c *datamodel.ExpertModeCredential) bool {
		return c != nil && c.Username == testAccountHostServiceUser
	})).Return(nil)
	mockStorage.On("SetAccountHostPinned", mock.Anything, mappingUUID, true).Return(nil)

	verifyCalled := false
	cloudService := new(hyperscaler2.MockServices)
	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(cloudService, nil)
	mm.EXPECT().generateClientCertificate(
		cloudService, "ontap-cluster", expectedUsername, mock.Anything, false,
	).Return(&hyperscalermodels.CustomCertificateResponse{}, nil)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).
		RunAndReturn(testProviderForHost(t))
	mm.EXPECT().createSAWithCertAuth(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ vsa.ExpertModeAccountProvider, params vsa.CreateExpertModeAccountParams) error {
			assert.Equal(t, expectedUsername, params.Username)
			assert.Empty(t, params.Password)
			return nil
		})
	mm.EXPECT().verifySAWithCertAuth(
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything,
	).RunAndReturn(func(
		_ context.Context,
		_ *dbdatamodel.Host,
		svmPattern, username string,
		_ *datamodel.PoolCredentials,
	) error {
		verifyCalled = true
		assert.Equal(t, orchcommon.BuildAccountHostSvmPattern(accountUUID), svmPattern)
		assert.Equal(t, expectedUsername, username)
		return nil
	})

	activity := &AccountHostActivity{SE: mockStorage}

	prepared := &PreparedAccountHostMapping{
		MappingUUID:   mappingUUID,
		HostUUID:      hostUUID,
		AdminUsername: expectedUsername,
		RoleName:      testAccountHostRoleName,
		RoleOwnerUUID: roleOwnerUUID,
		SvmPattern:    orchcommon.BuildAccountHostSvmPattern(accountUUID),
	}

	var ts testsuite.WorkflowTestSuite
	userEnv := ts.NewTestActivityEnvironment()
	userEnv.RegisterActivity(activity.CreateAccountHostUser)

	_, err = userEnv.ExecuteActivity(activity.CreateAccountHostUser, prepared)
	require.NoError(t, err)
	assert.True(t, verifyCalled, "verify must run when ONTAP user already exists so credentials can be pinned")
}

type deleteRoleAlreadyAbsentProvider struct {
	*vsa.MockProvider
	clusterUUID string
}

func (p *deleteRoleAlreadyAbsentProvider) GetClusterUUID() (string, error) {
	return p.clusterUUID, nil
}

func (p *deleteRoleAlreadyAbsentProvider) GetRoleOwnerUUID(roleName string) (string, error) {
	return "", errors.New("SecurityRole not found")
}

func (p *deleteRoleAlreadyAbsentProvider) CreateSecurityAccount(params vsa.CreateSecurityAccountParams) error {
	return nil
}

func (p *deleteRoleAlreadyAbsentProvider) ModifySecurityAccount(params vsa.ModifySecurityAccountParams) error {
	return nil
}

func (p *deleteRoleAlreadyAbsentProvider) GetSecurityAccount(params vsa.GetSecurityAccountParams) (*vsa.SecurityAccount, error) {
	return nil, errors.New("SecurityAccount not found")
}

func (p *deleteRoleAlreadyAbsentProvider) DeleteSecurityAccount(params vsa.DeleteSecurityAccountParams) error {
	return nil
}

func TestRollbackAccountHostOntapProvisioning_DeletesRoleAndUser(t *testing.T) {
	mockStorage := database.NewMockStorage(t)

	mappingUUID := "0fd5305c-e724-2598-2605-2f61e716cd63"
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	clusterUUID := "95be11a7-0000-0000-0000-000000000001"
	serviceUsername := testAccountHostServiceUser
	roleName := testAccountHostRoleName

	encryptedPassword, err := utils.EncryptPassword("admin-pass")
	require.NoError(t, err)

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(&dbdatamodel.Host{
		BaseModel: dbdatamodel.BaseModel{UUID: hostUUID},
		Name:      "ontap-cluster",
		Password:  *encryptedPassword,
		Protocol:  dbdatamodel.ExternalClusterProtocolInsecureHTTPS,
		IPAddress: "10.10.10.50",
	}, nil)

	provider := &deleteRoleAlreadyAbsentProvider{
		MockProvider: vsa.NewMockProvider(t),
		clusterUUID:  clusterUUID,
	}

	accountDeleteCalled := false
	roleDeleteCalled := false
	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).Return(provider, nil)
	mm.EXPECT().accountHostDeleteExpertModeAccount(mock.Anything, mock.Anything, serviceUsername, clusterUUID).
		RunAndReturn(func(_ context.Context, _ vsa.ExpertModeAccountProvider, username, ownerUUID string) error {
			assert.Equal(t, serviceUsername, username)
			assert.Equal(t, clusterUUID, ownerUUID)
			accountDeleteCalled = true
			return nil
		})
	mm.EXPECT().accountHostDeleteExpertModeRole(mock.Anything, mock.Anything, roleName, clusterUUID).
		RunAndReturn(func(_ context.Context, _ vsa.Provider, name, ownerUUID string) error {
			assert.Equal(t, roleName, name)
			assert.Equal(t, clusterUUID, ownerUUID)
			roleDeleteCalled = true
			return nil
		})

	activity := &AccountHostActivity{SE: mockStorage}

	prepared := &PreparedAccountHostMapping{
		MappingUUID:   mappingUUID,
		HostUUID:      hostUUID,
		AdminUsername: serviceUsername,
		RoleName:      roleName,
		RoleOwnerUUID: clusterUUID,
	}

	var ts testsuite.WorkflowTestSuite
	rollbackEnv := ts.NewTestActivityEnvironment()
	rollbackEnv.RegisterActivity(activity.RollbackAccountHostOntapProvisioning)

	_, err = rollbackEnv.ExecuteActivity(activity.RollbackAccountHostOntapProvisioning, prepared)
	require.NoError(t, err)
	assert.True(t, accountDeleteCalled)
	assert.True(t, roleDeleteCalled)
}

func TestRollbackAccountHostOntapProvisioning_SkipsWithoutRoleMetadata(t *testing.T) {
	roleDeleteCalled := false
	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostDeleteExpertModeRole(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(context.Context, vsa.Provider, string, string) {
			roleDeleteCalled = true
		}).Maybe()

	activity := &AccountHostActivity{}

	var ts testsuite.WorkflowTestSuite
	rollbackEnv := ts.NewTestActivityEnvironment()
	rollbackEnv.RegisterActivity(activity.RollbackAccountHostOntapProvisioning)

	_, err := rollbackEnv.ExecuteActivity(activity.RollbackAccountHostOntapProvisioning, &PreparedAccountHostMapping{
		MappingUUID: "mapping-uuid",
		HostUUID:    "host-uuid",
	})
	require.NoError(t, err)
	assert.False(t, roleDeleteCalled)
}

func TestPrepareAccountHostMapping_AlreadyProvisioned(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &AccountHostActivity{SE: mockStorage}
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"

	mockStorage.On("GetAccountHost", mock.Anything, mappingUUID).Return(&datamodel.AccountHost{
		BaseModel: datamodel.BaseModel{UUID: mappingUUID},
		Pinned:    false,
		OntapModeCredentials: &datamodel.ExpertModeCredential{
			AuthType: env.USERNAME_PWD_SEC_MGR,
			Username: "u_gadmin",
			SecretID: "secret",
		},
	}, nil)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.PrepareAccountHostMapping)

	_, err := env.ExecuteActivity(activity.PrepareAccountHostMapping, mappingUUID)
	require.Error(t, err)
}

func TestCompleteAccountHostMapping_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &AccountHostActivity{SE: mockStorage}
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	cred := &datamodel.ExpertModeCredential{Username: "svc-user", AuthType: env.USERNAME_PWD}

	mockStorage.On("UpdateAccountHostServiceCredentials", mock.Anything, mappingUUID, cred).Return(nil)
	mockStorage.On("SetAccountHostPinned", mock.Anything, mappingUUID, true).Return(nil)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CompleteAccountHostMapping)

	_, err := env.ExecuteActivity(activity.CompleteAccountHostMapping, mappingUUID, "svc-user", cred)
	require.NoError(t, err)
}

func TestGenerateExternalClusterProjectServicePassword_UsernamePwd(t *testing.T) {
	password, err := generateAccountHostServicePasswordForTest(t, &datamodel.ExpertModeCredential{
		AuthType: env.USERNAME_PWD,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, password)
}

func TestToVSARolePrivileges(t *testing.T) {
	privs := toVSARolePrivileges([]orchcommon.ExpertModeRBACPrivilege{{
		Path: "vserver modify", Access: "all", Query: "svm_*",
	}})
	require.Len(t, privs, 1)
	assert.Equal(t, "vserver modify", privs[0].Path)
}

func TestPrepareAccountHostMapping_MissingManagementIP(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &AccountHostActivity{SE: mockStorage}
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

	mockStorage.On("GetAccountHost", mock.Anything, mappingUUID).Return(&datamodel.AccountHost{
		BaseModel: datamodel.BaseModel{UUID: mappingUUID},
		AccountID: 42,
		HostID:    hostUUID,
		Pinned:    false,
	}, nil)
	mockStorage.On("GetAccountByInternalID", mock.Anything, int64(42)).Return(&datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: accountUUID},
	}, nil)
	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(&datamodel.Host{
		BaseModel: datamodel.BaseModel{UUID: hostUUID},
		IPAddress: "",
	}, nil)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.PrepareAccountHostMapping)

	_, err := env.ExecuteActivity(activity.PrepareAccountHostMapping, mappingUUID)
	require.Error(t, err)
}

func TestPrepareAccountHostMapping_MissingAccountUUID(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &AccountHostActivity{SE: mockStorage}
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"

	mockStorage.On("GetAccountHost", mock.Anything, mappingUUID).Return(&datamodel.AccountHost{
		BaseModel: datamodel.BaseModel{UUID: mappingUUID},
		AccountID: 42,
		HostID:    hostUUID,
		Pinned:    false,
	}, nil)
	mockStorage.On("GetAccountByInternalID", mock.Anything, int64(42)).Return(&datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: ""},
	}, nil)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.PrepareAccountHostMapping)

	_, err := env.ExecuteActivity(activity.PrepareAccountHostMapping, mappingUUID)
	require.Error(t, err)
}

func TestCreateAccountHostRole_NilPrepared(t *testing.T) {
	activity := &AccountHostActivity{}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostRole)

	_, err := env.ExecuteActivity(activity.CreateAccountHostRole, (*PreparedAccountHostMapping)(nil))
	require.Error(t, err)
}

func TestCreateAccountHostRole_InvalidRBACConfig(t *testing.T) {
	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().loadAccountHostRBACPrivileges(mock.Anything, mock.Anything).
		Return(nil, errors.New("bad rbac json"))

	activity := &AccountHostActivity{}

	prepared := &PreparedAccountHostMapping{
		MappingUUID: "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2",
		RoleName:    testAccountHostRoleName,
		SvmPattern:  "svm_test_*",
	}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostRole)

	_, err := env.ExecuteActivity(activity.CreateAccountHostRole, prepared)
	require.Error(t, err)
}

func TestCreateAccountHostRole_EmptyPrivileges(t *testing.T) {
	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().loadAccountHostRBACPrivileges(mock.Anything, mock.Anything).
		Return([]orchcommon.ExpertModeRBACPrivilege{}, nil)

	activity := &AccountHostActivity{}

	prepared := &PreparedAccountHostMapping{
		RoleName: testAccountHostRoleName,
	}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostRole)

	_, err := env.ExecuteActivity(activity.CreateAccountHostRole, prepared)
	require.Error(t, err)
}

func TestLoadAccountHostRBACPrivilegesFromConfiguredSource(t *testing.T) {
	origBucketName := orchcommon.AccountHostRBACFileSourceBucketName
	origFilePath := orchcommon.AccountHostRBACFileSourcePath
	t.Cleanup(func() {
		orchcommon.AccountHostRBACFileSourceBucketName = origBucketName
		orchcommon.AccountHostRBACFileSourcePath = origFilePath
	})

	t.Run("When_bucket_file_is_valid", func(t *testing.T) {
		orchcommon.AccountHostRBACFileSourceBucketName = "rbac-bucket"
		orchcommon.AccountHostRBACFileSourcePath = "GCNV/%s/RBAC/project-onboard-rbac.txt"

		mockGoogle := hyperscaler2.NewMockServices(t)
		origGetGCPService := accountHostGetGCPService
		origGetBucketFileContent := accountHostGetBucketFileContent
		t.Cleanup(func() {
			accountHostGetGCPService = origGetGCPService
			accountHostGetBucketFileContent = origGetBucketFileContent
		})
		accountHostGetGCPService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGoogle, nil
		}
		accountHostGetBucketFileContent = func(_ hyperscaler2.Services, _ context.Context, bucketName, fileName string) (*hyperscalermodels.BucketFileDetails, error) {
			assert.Equal(t, "rbac-bucket", bucketName)
			assert.Equal(t, "GCNV/9.18.1P2/RBAC/project-onboard-rbac.txt", fileName)
			return &hyperscalermodels.BucketFileDetails{
				BucketName:     bucketName,
				FileUrl:        fileName,
				FileHashSHA256: "hash-value",
				FileContent:    "DEFAULT|none\nvolume create|all|-vserver <svm_name>",
			}, nil
		}

		activity := &AccountHostActivity{}

		privileges, err := activity.loadRBACPrivilegesFromConfiguredSource(context.Background(), "9.18.1P2")
		require.NoError(t, err)
		require.Len(t, privileges, 2)
		assert.Equal(t, "volume create", privileges[1].Path)
		assert.Equal(t, "-vserver <svm_name>", privileges[1].Query)
	})

	t.Run("When_bucket_file_is_CLI_format", func(t *testing.T) {
		orchcommon.AccountHostRBACFileSourceBucketName = "rbac-bucket"
		orchcommon.AccountHostRBACFileSourcePath = "GCNV/%s/RBAC/gcnvadmin_create_cli"

		mockGoogle := hyperscaler2.NewMockServices(t)
		origGetGCPService := accountHostGetGCPService
		origGetBucketFileContent := accountHostGetBucketFileContent
		t.Cleanup(func() {
			accountHostGetGCPService = origGetGCPService
			accountHostGetBucketFileContent = origGetBucketFileContent
		})
		accountHostGetGCPService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGoogle, nil
		}
		accountHostGetBucketFileContent = func(_ hyperscaler2.Services, _ context.Context, _, _ string) (*hyperscalermodels.BucketFileDetails, error) {
			return &hyperscalermodels.BucketFileDetails{
				FileContent: "security login role create -role gcnvadmin -cmddirname \"DEFAULT\" -access all\n",
			}, nil
		}

		activity := &AccountHostActivity{}

		privileges, err := activity.loadRBACPrivilegesFromConfiguredSource(context.Background(), "9.18.1X29")
		require.NoError(t, err)
		require.Len(t, privileges, 1)
		assert.Equal(t, "DEFAULT", privileges[0].Path)
		assert.Equal(t, "all", privileges[0].Access)
	})

	t.Run("When_bucket_file_content_is_empty", func(t *testing.T) {
		orchcommon.AccountHostRBACFileSourceBucketName = "rbac-bucket"
		orchcommon.AccountHostRBACFileSourcePath = "GCNV/%s/RBAC/project-onboard-rbac.txt"

		mockGoogle := hyperscaler2.NewMockServices(t)
		origGetGCPService := accountHostGetGCPService
		origGetBucketFileContent := accountHostGetBucketFileContent
		t.Cleanup(func() {
			accountHostGetGCPService = origGetGCPService
			accountHostGetBucketFileContent = origGetBucketFileContent
		})
		accountHostGetGCPService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGoogle, nil
		}
		accountHostGetBucketFileContent = func(_ hyperscaler2.Services, _ context.Context, _, _ string) (*hyperscalermodels.BucketFileDetails, error) {
			return &hyperscalermodels.BucketFileDetails{
				BucketName:  "rbac-bucket",
				FileUrl:     "GCNV/9.18.1P2/RBAC/project-onboard-rbac.txt",
				FileContent: "   ",
			}, nil
		}

		activity := &AccountHostActivity{}

		_, err := activity.loadRBACPrivilegesFromConfiguredSource(context.Background(), "9.18.1P2")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is empty")
	})

	t.Run("When_CLI_file_is_corrupt", func(t *testing.T) {
		orchcommon.AccountHostRBACFileSourceBucketName = "rbac-bucket"
		orchcommon.AccountHostRBACFileSourcePath = "GCNV/%s/RBAC/gcnvadmin_create_cli"

		mockGoogle := hyperscaler2.NewMockServices(t)
		origGetGCPService := accountHostGetGCPService
		origGetBucketFileContent := accountHostGetBucketFileContent
		t.Cleanup(func() {
			accountHostGetGCPService = origGetGCPService
			accountHostGetBucketFileContent = origGetBucketFileContent
		})
		accountHostGetGCPService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGoogle, nil
		}
		accountHostGetBucketFileContent = func(_ hyperscaler2.Services, _ context.Context, _, _ string) (*hyperscalermodels.BucketFileDetails, error) {
			return &hyperscalermodels.BucketFileDetails{
				FileContent: "security login role create -role gcnvadmin -cmddirname \"DEFAULT\"\n",
			}, nil
		}

		activity := &AccountHostActivity{}

		_, err := activity.loadRBACPrivilegesFromConfiguredSource(context.Background(), "9.18.1X29")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "account host RBAC file")
	})

	t.Run("When_gcp_service_is_unavailable", func(t *testing.T) {
		orchcommon.AccountHostRBACFileSourceBucketName = "rbac-bucket"
		orchcommon.AccountHostRBACFileSourcePath = "GCNV/%s/RBAC/project-onboard-rbac.txt"

		origGetGCPService := accountHostGetGCPService
		t.Cleanup(func() {
			accountHostGetGCPService = origGetGCPService
		})
		accountHostGetGCPService = func(context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("gcp not reachable")
		}

		activity := &AccountHostActivity{}

		_, err := activity.loadRBACPrivilegesFromConfiguredSource(context.Background(), "9.18.1P2")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "gcp not reachable")
	})

	t.Run("When_ontap_version_is_missing", func(t *testing.T) {
		orchcommon.AccountHostRBACFileSourcePath = "GCNV/%s/RBAC/gcnvadmin_create_cli"

		activity := &AccountHostActivity{}
		_, err := activity.loadRBACPrivilegesFromConfiguredSource(context.Background(), "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ONTAP version is required")
	})
}

func TestCreateAccountHostUser_NilPrepared(t *testing.T) {
	activity := &AccountHostActivity{}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, (*PreparedAccountHostMapping)(nil))
	require.Error(t, err)
}

func TestCreateAccountHostUser_EmptyServiceUsername(t *testing.T) {
	activity := &AccountHostActivity{}
	prepared := &PreparedAccountHostMapping{
		MappingUUID:   "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2",
		RoleOwnerUUID: "95be11a7-0000-0000-0000-000000000001",
		AdminUsername: "   ",
	}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, prepared)
	require.Error(t, err)
}

func TestGenerateExternalClusterProjectServicePassword_UnsupportedAuthType(t *testing.T) {
	_, err := generateAccountHostServicePasswordForTest(t, &datamodel.ExpertModeCredential{
		AuthType: 999,
	})
	require.Error(t, err)
}

func TestGenerateExternalClusterProjectServicePassword_NilCredential(t *testing.T) {
	_, err := generateAccountHostServicePasswordForTest(t, nil)
	require.Error(t, err)
}

func testEncryptedAccountHostHost(t *testing.T, hostUUID string) *dbdatamodel.Host {
	t.Helper()
	encryptedPassword, err := utils.EncryptPassword("admin-pass")
	require.NoError(t, err)
	return &dbdatamodel.Host{
		BaseModel:    dbdatamodel.BaseModel{UUID: hostUUID},
		Name:         "ontap-cluster",
		ExternalName: "ontap-cluster",
		Password:     *encryptedPassword,
		Protocol:     dbdatamodel.ExternalClusterProtocolInsecureHTTPS,
		IPAddress:    "10.10.10.50",
		HostCredentials: &datamodel.PoolCredentials{
			AuthType: env.USER_CERTIFICATE,
			CaURI:    "project/pool/ca",
		},
	}
}

func testAccountHostUserPrepared(mappingUUID, hostUUID, accountUUID string) *PreparedAccountHostMapping {
	return &PreparedAccountHostMapping{
		MappingUUID:   mappingUUID,
		HostUUID:      hostUUID,
		AdminUsername: testAccountHostServiceUser,
		RoleName:      testAccountHostRoleName,
		RoleOwnerUUID: "95be11a7-0000-0000-0000-000000000001",
		SvmPattern:    orchcommon.BuildAccountHostSvmPattern(accountUUID),
	}
}

func TestCreateAccountHostUser_PasswordAuthSuccess(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USERNAME_PWD))
	mockStorage := database.NewMockStorage(t)

	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	roleOwnerUUID := "95be11a7-0000-0000-0000-000000000001"

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)
	mockStorage.On("UpdateAccountHostServiceCredentials", mock.Anything, mappingUUID, mock.MatchedBy(func(c *datamodel.ExpertModeCredential) bool {
		return c != nil && c.Username == testAccountHostServiceUser && c.Password != ""
	})).Return(nil)
	mockStorage.On("SetAccountHostPinned", mock.Anything, mappingUUID, true).Return(nil)

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().generateAccountHostServicePassword(mock.Anything, mock.Anything).
		RunAndReturn(_generateAccountHostServicePassword)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).
		RunAndReturn(testProviderForHost(t))
	mm.EXPECT().accountHostEnsureExpertModeAccount(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ vsa.ExpertModeAccountProvider, params vsa.CreateExpertModeAccountParams) error {
			assert.Equal(t, testAccountHostServiceUser, params.Username)
			assert.Equal(t, testAccountHostRoleName, params.RoleName)
			assert.Equal(t, roleOwnerUUID, params.ClusterUUID)
			assert.NotEmpty(t, params.Password)
			return nil
		})
	mm.EXPECT().accountHostVerifyHostServiceCredentials(
		mock.Anything, mock.Anything, orchcommon.BuildAccountHostSvmPattern(accountUUID),
		testAccountHostServiceUser, mock.Anything,
	).Return(nil)

	activity := &AccountHostActivity{SE: mockStorage}
	prepared := testAccountHostUserPrepared(mappingUUID, hostUUID, accountUUID)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, prepared)
	require.NoError(t, err)
}

func TestGenerateAccountHostServicePassword_CertificateAuthTypeRejected(t *testing.T) {
	_, err := generateAccountHostServicePasswordForTest(t, &datamodel.ExpertModeCredential{
		AuthType: env.USER_CERTIFICATE,
		SecretID: "account-host-secret-mapping-uuid",
	})
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "password generation is not used")
}

func TestCreateAccountHostUser_CertificateUsesHostNameWhenExternalNameEmpty(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)

	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	host := testEncryptedAccountHostHost(t, hostUUID)
	host.ExternalName = ""
	host.Name = "fallback-cluster-name"

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(host, nil)
	mockStorage.On("UpdateAccountHostServiceCredentials", mock.Anything, mappingUUID, mock.Anything).Return(nil)
	mockStorage.On("SetAccountHostPinned", mock.Anything, mappingUUID, true).Return(nil)

	cloudService := new(hyperscaler2.MockServices)
	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(cloudService, nil)
	mm.EXPECT().generateClientCertificate(
		cloudService, "fallback-cluster-name", testAccountHostServiceUser, mock.Anything, false,
	).Return(&hyperscalermodels.CustomCertificateResponse{}, nil)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).
		RunAndReturn(testProviderForHost(t))
	mm.EXPECT().createSAWithCertAuth(mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mm.EXPECT().verifySAWithCertAuth(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	activity := &AccountHostActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, testAccountHostUserPrepared(mappingUUID, hostUUID, accountUUID))
	require.NoError(t, err)
}

func TestCreateAccountHostUser_CertificateMissingClusterName(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)

	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	host := testEncryptedAccountHostHost(t, hostUUID)
	host.ExternalName = ""
	host.Name = ""

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(host, nil)

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(new(hyperscaler2.MockServices), nil)

	activity := &AccountHostActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, testAccountHostUserPrepared(mappingUUID, hostUUID, accountUUID))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "external cluster name is required")
}

func TestEnsureSAWithCertAuth_BuildPoolCredentialsError(t *testing.T) {
	activity := &AccountHostActivity{}
	host := testEncryptedAccountHostHost(t, "host-uuid")
	prepared := testAccountHostUserPrepared("mapping-uuid", "host-uuid", "account-uuid")

	origBuild := accountHostBuildCertificatePoolCredentials
	accountHostBuildCertificatePoolCredentials = func(*datamodel.Host, *datamodel.ExpertModeCredential) (*datamodel.PoolCredentials, error) {
		return nil, fmt.Errorf("build pool credentials failed")
	}
	t.Cleanup(func() { accountHostBuildCertificatePoolCredentials = origBuild })

	err := activity.ensureSAWithCertAuth(
		context.Background(), prepared, host,
		&datamodel.ExpertModeCredential{AuthType: env.USER_CERTIFICATE, CertificateID: "cert", SecretID: "secret"},
		testAccountHostServiceUser,
	)
	require.Error(t, err)
}

func TestCreateAccountHostUser_CertificateGCPInitError(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(nil, errors.New("gcp unavailable"))

	activity := &AccountHostActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, testAccountHostUserPrepared(mappingUUID, hostUUID, accountUUID))
	require.Error(t, err)
}

func TestCreateAccountHostUser_CertificateGenerationError(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	cloudService := new(hyperscaler2.MockServices)

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(cloudService, nil)
	mm.EXPECT().generateClientCertificate(mock.Anything, mock.Anything, mock.Anything, mock.Anything, false).
		Return(nil, errors.New("cert mint failed"))

	activity := &AccountHostActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, testAccountHostUserPrepared(mappingUUID, hostUUID, accountUUID))
	require.Error(t, err)
}

func TestCreateAccountHostUser_CertificateProviderError(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	cloudService := new(hyperscaler2.MockServices)

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(cloudService, nil)
	mm.EXPECT().generateClientCertificate(mock.Anything, mock.Anything, mock.Anything, mock.Anything, false).
		Return(&hyperscalermodels.CustomCertificateResponse{}, nil)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).
		Return(nil, errors.New("provider unavailable"))

	activity := &AccountHostActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, testAccountHostUserPrepared(mappingUUID, hostUUID, accountUUID))
	require.Error(t, err)
}

func TestCreateAccountHostUser_CertificateProviderNotAccountCapable(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	cloudService := new(hyperscaler2.MockServices)

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(cloudService, nil)
	mm.EXPECT().generateClientCertificate(mock.Anything, mock.Anything, mock.Anything, mock.Anything, false).
		Return(&hyperscalermodels.CustomCertificateResponse{}, nil)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).
		Return(vsa.NewMockProvider(t), nil)

	activity := &AccountHostActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, testAccountHostUserPrepared(mappingUUID, hostUUID, accountUUID))
	require.Error(t, err)
}

func TestCreateAccountHostUser_CertificateEnsureAccountError(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	cloudService := new(hyperscaler2.MockServices)

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(cloudService, nil)
	mm.EXPECT().generateClientCertificate(mock.Anything, mock.Anything, mock.Anything, mock.Anything, false).
		Return(&hyperscalermodels.CustomCertificateResponse{}, nil)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).
		RunAndReturn(testProviderForHost(t))
	mm.EXPECT().createSAWithCertAuth(mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("ontap account create failed"))

	activity := &AccountHostActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, testAccountHostUserPrepared(mappingUUID, hostUUID, accountUUID))
	require.Error(t, err)
}

func TestCreateAccountHostUser_CertificateVerifyError(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	cloudService := new(hyperscaler2.MockServices)

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(cloudService, nil)
	mm.EXPECT().generateClientCertificate(mock.Anything, mock.Anything, mock.Anything, mock.Anything, false).
		Return(&hyperscalermodels.CustomCertificateResponse{}, nil)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).
		RunAndReturn(testProviderForHost(t))
	mm.EXPECT().createSAWithCertAuth(mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mm.EXPECT().verifySAWithCertAuth(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("certificate verify failed"))

	activity := &AccountHostActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, testAccountHostUserPrepared(mappingUUID, hostUUID, accountUUID))
	require.Error(t, err)
}

func TestCreateAccountHostUser_CertificateCompleteMappingError(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	cloudService := new(hyperscaler2.MockServices)

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)
	mockStorage.On("UpdateAccountHostServiceCredentials", mock.Anything, mappingUUID, mock.Anything).
		Return(errors.New("db update failed"))

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(cloudService, nil)
	mm.EXPECT().generateClientCertificate(mock.Anything, mock.Anything, mock.Anything, mock.Anything, false).
		Return(&hyperscalermodels.CustomCertificateResponse{}, nil)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).
		RunAndReturn(testProviderForHost(t))
	mm.EXPECT().createSAWithCertAuth(mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mm.EXPECT().verifySAWithCertAuth(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	activity := &AccountHostActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, testAccountHostUserPrepared(mappingUUID, hostUUID, accountUUID))
	require.Error(t, err)
}

func TestCreateAccountHostUser_PasswordGenerateError(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USERNAME_PWD))
	mockStorage := database.NewMockStorage(t)
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().generateAccountHostServicePassword(mock.Anything, mock.Anything).
		Return("", errors.New("password generation failed"))

	activity := &AccountHostActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, testAccountHostUserPrepared(mappingUUID, hostUUID, accountUUID))
	require.Error(t, err)
}

func TestEnsureSAWithPasswordAuth_ProviderNotAccountCapable(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USERNAME_PWD))
	mockStorage := database.NewMockStorage(t)
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mappingUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	accountUUID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().generateAccountHostServicePassword(mock.Anything, mock.Anything).
		RunAndReturn(_generateAccountHostServicePassword)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).
		Return(vsa.NewMockProvider(t), nil)

	activity := &AccountHostActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAccountHostUser)

	_, err := env.ExecuteActivity(activity.CreateAccountHostUser, testAccountHostUserPrepared(mappingUUID, hostUUID, accountUUID))
	require.Error(t, err)
}

func TestRollbackAccountHostOntapProvisioning_RevokesCertificate(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)

	mappingUUID := "0fd5305c-e724-2598-2605-2f61e716cd63"
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	clusterUUID := "95be11a7-0000-0000-0000-000000000001"
	serviceUsername := testAccountHostServiceUser
	roleName := testAccountHostRoleName

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)

	provider := &deleteRoleAlreadyAbsentProvider{
		MockProvider: vsa.NewMockProvider(t),
		clusterUUID:  clusterUUID,
	}
	cloudService := new(hyperscaler2.MockServices)
	revokeCalled := false

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).Return(provider, nil)
	mm.EXPECT().accountHostDeleteExpertModeAccount(mock.Anything, mock.Anything, serviceUsername, clusterUUID).Return(nil)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(cloudService, nil)
	mm.EXPECT().revokeAccountHostClientCertificate(cloudService, mock.Anything).
		RunAndReturn(func(_ hyperscaler2.Services, poolCreds *datamodel.PoolCredentials) error {
			revokeCalled = true
			assert.Equal(t, env.USER_CERTIFICATE, poolCreds.AuthType)
			assert.Equal(t, "account-host-cert-"+mappingUUID, poolCreds.CertificateID)
			return nil
		})
	mm.EXPECT().accountHostDeleteExpertModeRole(mock.Anything, mock.Anything, roleName, clusterUUID).Return(nil)

	activity := &AccountHostActivity{SE: mockStorage}
	prepared := &PreparedAccountHostMapping{
		MappingUUID:   mappingUUID,
		HostUUID:      hostUUID,
		AdminUsername: serviceUsername,
		RoleName:      roleName,
		RoleOwnerUUID: clusterUUID,
	}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RollbackAccountHostOntapProvisioning)

	_, err := env.ExecuteActivity(activity.RollbackAccountHostOntapProvisioning, prepared)
	require.NoError(t, err)
	assert.True(t, revokeCalled)
}

func TestRollbackAccountHostOntapProvisioning_CertificateBuildError(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)

	mappingUUID := "0fd5305c-e724-2598-2605-2f61e716cd63"
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	clusterUUID := "95be11a7-0000-0000-0000-000000000001"

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)

	provider := &deleteRoleAlreadyAbsentProvider{
		MockProvider: vsa.NewMockProvider(t),
		clusterUUID:  clusterUUID,
	}

	origBuild := accountHostBuildCertificatePoolCredentials
	accountHostBuildCertificatePoolCredentials = func(*datamodel.Host, *datamodel.ExpertModeCredential) (*datamodel.PoolCredentials, error) {
		return nil, errors.New("build failed")
	}
	t.Cleanup(func() { accountHostBuildCertificatePoolCredentials = origBuild })

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).Return(provider, nil)
	mm.EXPECT().accountHostDeleteExpertModeAccount(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mm.EXPECT().accountHostDeleteExpertModeRole(mock.Anything, mock.Anything, testAccountHostRoleName, clusterUUID).Return(nil)

	activity := &AccountHostActivity{SE: mockStorage}
	prepared := &PreparedAccountHostMapping{
		MappingUUID:   mappingUUID,
		HostUUID:      hostUUID,
		AdminUsername: testAccountHostServiceUser,
		RoleName:      testAccountHostRoleName,
		RoleOwnerUUID: clusterUUID,
	}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RollbackAccountHostOntapProvisioning)

	_, err := env.ExecuteActivity(activity.RollbackAccountHostOntapProvisioning, prepared)
	require.NoError(t, err)
}

func TestRollbackAccountHostOntapProvisioning_CertificateGCPInitError(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)

	mappingUUID := "0fd5305c-e724-2598-2605-2f61e716cd63"
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	clusterUUID := "95be11a7-0000-0000-0000-000000000001"

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)

	provider := &deleteRoleAlreadyAbsentProvider{
		MockProvider: vsa.NewMockProvider(t),
		clusterUUID:  clusterUUID,
	}

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).Return(provider, nil)
	mm.EXPECT().accountHostDeleteExpertModeAccount(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(nil, errors.New("gcp init failed"))
	mm.EXPECT().accountHostDeleteExpertModeRole(mock.Anything, mock.Anything, testAccountHostRoleName, clusterUUID).Return(nil)

	activity := &AccountHostActivity{SE: mockStorage}
	prepared := &PreparedAccountHostMapping{
		MappingUUID:   mappingUUID,
		HostUUID:      hostUUID,
		AdminUsername: testAccountHostServiceUser,
		RoleName:      testAccountHostRoleName,
		RoleOwnerUUID: clusterUUID,
	}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RollbackAccountHostOntapProvisioning)

	_, err := env.ExecuteActivity(activity.RollbackAccountHostOntapProvisioning, prepared)
	require.NoError(t, err)
}

func TestRollbackAccountHostOntapProvisioning_CertificateRevokeError(t *testing.T) {
	t.Setenv("ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE", strconv.Itoa(env.USER_CERTIFICATE))
	mockStorage := database.NewMockStorage(t)

	mappingUUID := "0fd5305c-e724-2598-2605-2f61e716cd63"
	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	clusterUUID := "95be11a7-0000-0000-0000-000000000001"
	cloudService := new(hyperscaler2.MockServices)

	mockStorage.On("GetHost", mock.Anything, hostUUID).Return(testEncryptedAccountHostHost(t, hostUUID), nil)

	provider := &deleteRoleAlreadyAbsentProvider{
		MockProvider: vsa.NewMockProvider(t),
		clusterUUID:  clusterUUID,
	}

	mm := newAccountHostMonkeyMockAndPatch(t)
	mm.EXPECT().accountHostGetProviderForHost(mock.Anything, mock.Anything).Return(provider, nil)
	mm.EXPECT().accountHostDeleteExpertModeAccount(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mm.EXPECT().accountHostGetGCPService(mock.Anything).Return(cloudService, nil)
	mm.EXPECT().revokeAccountHostClientCertificate(cloudService, mock.Anything).Return(errors.New("revoke failed"))
	mm.EXPECT().accountHostDeleteExpertModeRole(mock.Anything, mock.Anything, testAccountHostRoleName, clusterUUID).Return(nil)

	activity := &AccountHostActivity{SE: mockStorage}
	prepared := &PreparedAccountHostMapping{
		MappingUUID:   mappingUUID,
		HostUUID:      hostUUID,
		AdminUsername: testAccountHostServiceUser,
		RoleName:      testAccountHostRoleName,
		RoleOwnerUUID: clusterUUID,
	}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RollbackAccountHostOntapProvisioning)

	_, err := env.ExecuteActivity(activity.RollbackAccountHostOntapProvisioning, prepared)
	require.NoError(t, err)
}
