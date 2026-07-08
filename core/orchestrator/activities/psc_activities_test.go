package activities_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	coremodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	hyperscaler3 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/testsuite"
)

func TestCreateClusterLogForwardingProvider_Success(t *testing.T) {
	// Arrange
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	originalGetClusterLogForwarding := activities.GetClusterLogForwarding
	defer func() {
		vsa.GetProviderByNode = originalGetProviderByNode
		activities.GetClusterLogForwarding = originalGetClusterLogForwarding
	}() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	// Mock GetClusterLogForwarding to return record not found error.
	activities.GetClusterLogForwarding = func(ctx context.Context, node *coremodel.Node, address string, port int64) error {
		return errors.New("record not found")
	}

	pscActivity := &activities.PSCActivity{
		SE: database.NewMockStorage(t),
	}

	node := &coremodel.Node{}
	mockProvider.On("CreateSecurityLogForwarding", mock.Anything).Return(nil, nil)

	// Run activity through Temporal test environment so RecordHeartbeat has a valid activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(pscActivity.CreateClusterLogForwarding)

	_, err := env.ExecuteActivity(pscActivity.CreateClusterLogForwarding, node, "test-address")
	assert.NoError(t, err)
}

func TestCreateClusterLogForwardingProvider_Failure(t *testing.T) {
	// Arrange
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	originalGetClusterLogForwarding := activities.GetClusterLogForwarding
	defer func() {
		vsa.GetProviderByNode = originalGetProviderByNode
		activities.GetClusterLogForwarding = originalGetClusterLogForwarding
	}() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	// Mock GetClusterLogForwarding to return record not found error.
	activities.GetClusterLogForwarding = func(ctx context.Context, node *coremodel.Node, address string, port int64) error {
		return errors.New("record not found")
	}

	pscActivity := &activities.PSCActivity{
		SE: database.NewMockStorage(t),
	}

	node := &coremodel.Node{}
	mockProvider.On("CreateSecurityLogForwarding", mock.Anything).Return(nil, errors.New("failed to get create cluster log forwarding"))

	// Run activity through Temporal test environment so RecordHeartbeat has a valid activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(pscActivity.CreateClusterLogForwarding)

	_, err := env.ExecuteActivity(pscActivity.CreateClusterLogForwarding, node, "test-address")
	assert.Error(t, err)
}

func TestCreateEMSEventForwarding_Success(t *testing.T) {
	// Arrange
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() {
		vsa.GetProviderByNode = originalGetProviderByNode
	}() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	pscActivity := &activities.PSCActivity{
		SE: database.NewMockStorage(t),
	}

	node := &coremodel.Node{}
	mockProvider.On("CreateEMSEventForwarding", mock.Anything).Return(nil)

	// Run activity through Temporal test environment so RecordHeartbeat has a valid activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(pscActivity.CreateEMSEventForwarding)

	_, err := env.ExecuteActivity(pscActivity.CreateEMSEventForwarding, node, "35.239.71.238")
	assert.NoError(t, err)
	mockProvider.AssertExpectations(t)
}

func TestCreateEMSEventForwarding_Failure(t *testing.T) {
	// Arrange
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() {
		vsa.GetProviderByNode = originalGetProviderByNode
	}() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	pscActivity := &activities.PSCActivity{
		SE: database.NewMockStorage(t),
	}

	node := &coremodel.Node{}
	mockProvider.On("CreateEMSEventForwarding", mock.Anything).Return(errors.New("failed to create EMS event forwarding"))

	// Run activity through Temporal test environment so RecordHeartbeat has a valid activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(pscActivity.CreateEMSEventForwarding)

	_, err := env.ExecuteActivity(pscActivity.CreateEMSEventForwarding, node, "35.239.71.238")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create EMS event forwarding")
	mockProvider.AssertExpectations(t)
}

func TestCreateEMSEventForwarding_ProviderError(t *testing.T) {
	// Arrange
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() {
		vsa.GetProviderByNode = originalGetProviderByNode
	}() // Restore original function after test

	// Mock GetProviderByNode to return error
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return nil, errors.New("failed to get provider by node")
	}

	pscActivity := &activities.PSCActivity{
		SE: database.NewMockStorage(t),
	}

	node := &coremodel.Node{}

	// Run activity through Temporal test environment so RecordHeartbeat has a valid activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(pscActivity.CreateEMSEventForwarding)

	_, err := env.ExecuteActivity(pscActivity.CreateEMSEventForwarding, node, "35.239.71.238")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get provider by node")
}

func Test_getClusterLogForwarding_Success(t *testing.T) {
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	node := &coremodel.Node{}
	address := "test-address"
	port := int64(1009)
	mockProvider.On("GetSecurityLogForwarding", mock.Anything).Return(nil)

	err := activities.GetClusterLogForwarding(ctx, node, address, port)

	assert.NoError(t, err)
}

func Test_getClusterLogForwarding_Error(t *testing.T) {
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	node := &coremodel.Node{}
	address := "test-address"
	port := int64(1009)
	mockProvider.On("GetSecurityLogForwarding", mock.Anything).Return(errors.New("record not found"))

	err := activities.GetClusterLogForwarding(ctx, node, address, port)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "record not found")
}

func Test_getClusterLogForwarding_ProviderError(t *testing.T) {
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return nil, errors.New("failed to get provider by node")
	}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	node := &coremodel.Node{}
	address := "test-address"
	port := int64(1009)

	err := activities.GetClusterLogForwarding(ctx, node, address, port)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get provider by node")
}

func Test_updateSecurityAudit_Success(t *testing.T) {
	// Arrange
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	originalGetSecurityAudit := activities.GetSecurityAudit
	defer func() {
		vsa.GetProviderByNode = originalGetProviderByNode
		activities.GetSecurityAudit = originalGetSecurityAudit
	}() // Restore original function after test
	securityAudit := vsa.SecurityAudit{
		HTTP:   false,
		Cli:    false,
		Ontapi: false,
	}
	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	// Mock GetClusterLogForwarding to return record not found error.
	activities.GetSecurityAudit = func(ctx context.Context, node *coremodel.Node) (*vsa.SecurityAudit, error) {
		return &securityAudit, nil
	}

	pscActivity := &activities.PSCActivity{
		SE: database.NewMockStorage(t),
	}

	node := &coremodel.Node{}
	mockProvider.On("UpdateSecurityAudit", mock.Anything).Return(nil, nil)

	// Run activity through Temporal test environment so RecordHeartbeat has a valid activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(pscActivity.UpdateSecurityAudit)

	_, err := env.ExecuteActivity(pscActivity.UpdateSecurityAudit, node)
	assert.NoError(t, err)
}

func Test_updateSecurityAudit_Failure(t *testing.T) {
	// Arrange
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	originalGetSecurityAudit := activities.GetSecurityAudit
	defer func() {
		vsa.GetProviderByNode = originalGetProviderByNode
		activities.GetSecurityAudit = originalGetSecurityAudit
	}() // Restore original function after test
	securityAudit := vsa.SecurityAudit{
		HTTP:   false,
		Cli:    false,
		Ontapi: false,
	}
	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	// Mock GetClusterLogForwarding to return record not found error.
	activities.GetSecurityAudit = func(ctx context.Context, node *coremodel.Node) (*vsa.SecurityAudit, error) {
		return &securityAudit, nil
	}

	pscActivity := &activities.PSCActivity{
		SE: database.NewMockStorage(t),
	}

	node := &coremodel.Node{}
	mockProvider.On("UpdateSecurityAudit", mock.Anything).Return(nil, errors.New("failed to update"))

	// Run activity through Temporal test environment so RecordHeartbeat has a valid activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(pscActivity.UpdateSecurityAudit)

	_, err := env.ExecuteActivity(pscActivity.UpdateSecurityAudit, node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update")
}

func Test_updateSecurityAudit_ProviderError(t *testing.T) {
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return nil, errors.New("failed to get provider by node")
	}
	node := &coremodel.Node{}

	pscActivity := &activities.PSCActivity{
		SE: database.NewMockStorage(t),
	}

	// Run activity through Temporal test environment so RecordHeartbeat has a valid activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(pscActivity.UpdateSecurityAudit)

	_, err := env.ExecuteActivity(pscActivity.UpdateSecurityAudit, node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get provider by node")
}

func Test_GetSecurityAudit_Success(t *testing.T) {
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	node := &coremodel.Node{}
	expectedResponse := &vsa.SecurityAudit{
		Cli:    true,
		HTTP:   true,
		Ontapi: true,
	}
	mockProvider.On("GetSecurityAudit").Return(expectedResponse, nil)

	resp, err := activities.GetSecurityAudit(ctx, node)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, expectedResponse.Cli, resp.Cli)
}

func Test_GetSecurityAudit_Error(t *testing.T) {
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	node := &coremodel.Node{}

	mockProvider.On("GetSecurityAudit").Return(nil, errors.New("record not found"))
	mockProvider.On("GetSecurityAudit", mock.Anything).Return()

	resp, err := activities.GetSecurityAudit(ctx, node)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "record not found")
}

func Test_GetSecurityAudit_ProviderError(t *testing.T) {
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
		return nil, errors.New("failed to get provider by node")
	}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	node := &coremodel.Node{}

	resp, err := activities.GetSecurityAudit(ctx, node)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to get provider by node")
}

func TestDeleteForwardingRule(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	pool := &datamodel.Pool{
		AccountID: int64(12345),
		Network:   "tst-network",
		Account: &datamodel.Account{
			Name: "tst-account",
		},
		ClusterDetails: datamodel.ClusterDetails{
			RegionalTenantProject: "tst-project",
		},
	}
	t.Run("WhenListPoolsFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := activities.PSCActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteForwardingRule)

		mockStorage.On("ListPools", mock.Anything, mock.Anything).Return(nil, errors.New("failed to list pools"))

		_, err := env.ExecuteActivity(activity.DeleteForwardingRule, pool)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list pools")
	})
	t.Run("WhenListPoolsReturnsMoreThanOnePool", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := activities.PSCActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteForwardingRule)
		pool1 := datamodel.PoolView{
			Pool: datamodel.Pool{
				AccountID: int64(12345),
			},
		}
		pool2 := datamodel.PoolView{
			Pool: datamodel.Pool{
				AccountID: int64(54321),
			},
		}
		pools := []*datamodel.PoolView{}
		pools = append(pools, &pool1, &pool2)

		mockStorage.On("ListPools", mock.Anything, mock.Anything).Return(pools, nil)

		response, err := env.ExecuteActivity(activity.DeleteForwardingRule, pool)
		assert.Nil(t, err)
		assert.False(tt, response.HasValue())
	})
	t.Run("WhenGetGCPServiceFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := activities.PSCActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteForwardingRule)

		originalGetGCPService := hyperscaler2.ResolveCloudService
		defer func() {
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()

		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("failed to get GCP service")
		}

		mockStorage.On("ListPools", mock.Anything, mock.Anything).Return([]*datamodel.PoolView{}, nil)

		_, err := env.ExecuteActivity(activity.DeleteForwardingRule, pool)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get GCP service")
	})
	t.Run("WhenDeleteForwardingRuleFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := activities.PSCActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteForwardingRule)

		originalGetGCPService := hyperscaler2.ResolveCloudService
		originalDeleteForwardingRule := activities.DeleteForwardingRule
		defer func() {
			activities.DeleteForwardingRule = originalDeleteForwardingRule
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()

		activities.DeleteForwardingRule = func(service hyperscaler2.Services, consumerVpc, accountName, addressName string, clusterDetails datamodel.ClusterDetails) (string, error) {
			return "", errors.New("failed to delete forwarding rule")
		}
		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		mockStorage.On("ListPools", mock.Anything, mock.Anything).Return([]*datamodel.PoolView{}, nil)

		_, err := env.ExecuteActivity(activity.DeleteForwardingRule, pool)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete forwarding rule")
	})
	t.Run("WhenDeleteForwardingRuleSucceeds", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := activities.PSCActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteForwardingRule)

		originalGetGCPService := hyperscaler2.ResolveCloudService
		originalDeleteForwardingRule := activities.DeleteForwardingRule
		defer func() {
			activities.DeleteForwardingRule = originalDeleteForwardingRule
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()

		activities.DeleteForwardingRule = func(service hyperscaler2.Services, consumerVpc, accountName, addressName string, clusterDetails datamodel.ClusterDetails) (string, error) {
			return "tst-del-fwr", nil
		}
		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		mockStorage.On("ListPools", mock.Anything, mock.Anything).Return([]*datamodel.PoolView{}, nil)

		result, err := env.ExecuteActivity(activity.DeleteForwardingRule, pool)
		assert.Nil(t, err)
		assert.NotNil(t, result)

		var operations *[]commonparams.Operations
		err = result.Get(&operations)
		assert.NoError(t, err)
		assert.NotNil(t, operations)
		assert.Len(t, *operations, 1)

		op := (*operations)[0]
		assert.Contains(tt, op.OperationName, "tst-del-fwr")
		assert.False(t, op.IsDone)
		assert.Contains(tt, op.OperationType, "forwardingrule")
		assert.True(tt, op.IsRegionalResource)
		assert.Equal(tt, op.Project, "tst-project")
	})
}

func TestDeleteAddress(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	pool := &datamodel.Pool{
		AccountID: int64(12345),
		Network:   "tst-network",
		Account: &datamodel.Account{
			Name: "tst-account",
		},
		ClusterDetails: datamodel.ClusterDetails{
			RegionalTenantProject: "tst-project",
		},
	}
	t.Run("WhenListPoolsFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := activities.PSCActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteAddress)

		mockStorage.On("ListPools", mock.Anything, mock.Anything).Return(nil, errors.New("failed to list pools"))

		_, err := env.ExecuteActivity(activity.DeleteAddress, pool)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list pools")
	})
	t.Run("WhenListPoolsReturnsMoreThanOnePool", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := activities.PSCActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteAddress)
		pool1 := datamodel.PoolView{
			Pool: datamodel.Pool{
				AccountID: int64(12345),
			},
		}
		pool2 := datamodel.PoolView{
			Pool: datamodel.Pool{
				AccountID: int64(54321),
			},
		}
		pools := []*datamodel.PoolView{}
		pools = append(pools, &pool1, &pool2)

		mockStorage.On("ListPools", mock.Anything, mock.Anything).Return(pools, nil)

		response, err := env.ExecuteActivity(activity.DeleteAddress, pool)
		assert.Nil(t, err)
		assert.False(tt, response.HasValue())
	})
	t.Run("WhenGetGCPServiceFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := activities.PSCActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteAddress)

		originalGetGCPService := hyperscaler2.ResolveCloudService
		defer func() {
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()

		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("failed to get GCP service")
		}

		mockStorage.On("ListPools", mock.Anything, mock.Anything).Return([]*datamodel.PoolView{}, nil)

		_, err := env.ExecuteActivity(activity.DeleteAddress, pool)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get GCP service")
	})
	t.Run("WhenDeleteAddressFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := activities.PSCActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteAddress)

		originalGetGCPService := hyperscaler2.ResolveCloudService
		originalDeleteAddress := activities.DeleteAddress
		defer func() {
			activities.DeleteAddress = originalDeleteAddress
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()

		activities.DeleteAddress = func(service hyperscaler2.Services, consumerVpc, accountName, addressName string, clusterDetails datamodel.ClusterDetails) (string, error) {
			return "", errors.New("failed to delete address")
		}
		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		mockStorage.On("ListPools", mock.Anything, mock.Anything).Return([]*datamodel.PoolView{}, nil)

		_, err := env.ExecuteActivity(activity.DeleteAddress, pool)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete address")
	})
	t.Run("WhenDeleteAddressSucceeds", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := activities.PSCActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteAddress)

		originalGetGCPService := hyperscaler2.ResolveCloudService
		originalDeleteAddress := activities.DeleteAddress
		defer func() {
			activities.DeleteAddress = originalDeleteAddress
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()

		activities.DeleteAddress = func(service hyperscaler2.Services, consumerVpc, accountName, addressName string, clusterDetails datamodel.ClusterDetails) (string, error) {
			return "tst-del-addr", nil
		}
		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		mockStorage.On("ListPools", mock.Anything, mock.Anything).Return([]*datamodel.PoolView{}, nil)

		result, err := env.ExecuteActivity(activity.DeleteAddress, pool)
		assert.Nil(t, err)
		assert.NotNil(t, result)

		var operations *[]commonparams.Operations
		err = result.Get(&operations)
		assert.NoError(t, err)
		assert.NotNil(t, operations)
		assert.Len(t, *operations, 1)

		op := (*operations)[0]
		assert.Contains(tt, op.OperationName, "tst-del-addr")
		assert.False(t, op.IsDone)
		assert.Contains(tt, op.OperationType, "ipaddress")
		assert.True(tt, op.IsRegionalResource)
		assert.Equal(tt, op.Project, "tst-project")
	})
}

func TestGetForwardingRuleIPAddress(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.PSCActivity{SE: mockStorage}
	env.RegisterActivity(activity.GetForwardingRuleIPAddress)

	t.Run("WhenGetGCPServiceFails", func(tt *testing.T) {
		originalGetGCPService := hyperscaler2.ResolveCloudService
		defer func() {
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()

		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("failed to get GCP service")
		}

		_, err := env.ExecuteActivity(activity.GetForwardingRuleIPAddress, "test-project", "test-region", "test-address")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get GCP service")
	})
}

func Test_GetForwardingRuleIPAddress(t *testing.T) {
	project := "test-project"
	region := "test-region"
	privateAddressName := "test-address"
	t.Run("WhenGetForwardingRuleReturnsError", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		GetAddressURI := activities.GetAddressURI
		defer func() {
			activities.GetAddressURI = GetAddressURI
		}()
		errString := "unexpected error"
		mgs.On("GetForwardingRule", project, region, privateAddressName).Return(nil, errors.New(errString))

		_, err := activities.GetForwardingRuleIPAddress(mgs, project, region, privateAddressName)

		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.EqualError(tt, customErr.OriginalErr, "Error getting address/forwarding rule for project: test-project, address name: test-address. Error : "+errString)
		} else {
			tt.Fatalf("Expected a CustomError, got: %T", err)
		}
		mgs.AssertExpectations(tt)
	})
	t.Run("WhenGetForwardingRuleReturnsResponse", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		GetForwardingRuleIPAddress := activities.GetForwardingRuleIPAddress
		defer func() {
			activities.GetForwardingRuleIPAddress = GetForwardingRuleIPAddress
		}()
		forwardingRule := hyperscaler3.ForwardingRule{
			IPAddress: "1.2.3.4",
		}
		mgs.On("GetForwardingRule", project, region, privateAddressName).Return(&forwardingRule, nil)

		response, err := activities.GetForwardingRuleIPAddress(mgs, project, region, privateAddressName)

		assert.Nil(t, err)
		assert.NotNil(tt, response)
		assert.Contains(t, *response, "1.2.3.4")
		mgs.AssertExpectations(t)
	})
}

func TestGetAddressURI(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.PSCActivity{SE: mockStorage}
	env.RegisterActivity(activity.GetAddressURI)

	t.Run("WhenGetGCPServiceFails", func(tt *testing.T) {
		originalGetGCPService := hyperscaler2.ResolveCloudService
		defer func() {
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()

		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("failed to get GCP service")
		}

		_, err := env.ExecuteActivity(activity.GetAddressURI, "test-project", "test-region", "test-address")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get GCP service")
	})
}

func Test_GetAddressURI(t *testing.T) {
	project := "test-project"
	region := "test-region"
	privateAddressName := "test-address"
	t.Run("WhenGetAddressReturnsError", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		GetAddressURI := activities.GetAddressURI
		defer func() {
			activities.GetAddressURI = GetAddressURI
		}()
		errString := "unexpected error"
		mgs.On("GetAddress", project, region, privateAddressName).Return(nil, errors.New(errString))

		_, err := activities.GetAddressURI(mgs, project, region, privateAddressName)

		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.EqualError(tt, customErr.OriginalErr, "Error getting address/forwarding rule for project: test-project, address name: test-address. Error : "+errString)
		} else {
			tt.Fatalf("Expected a CustomError, got: %T", err)
		}
		mgs.AssertExpectations(tt)
	})
	t.Run("WhenGetAddressReturnsResponse", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		GetAddressURI := activities.GetAddressURI
		defer func() {
			activities.GetAddressURI = GetAddressURI
		}()

		address := hyperscaler3.Address{
			SelfLink: "address-link",
		}
		mgs.On("GetAddress", project, region, privateAddressName).Return(&address, nil)

		response, err := activities.GetAddressURI(mgs, project, region, privateAddressName)

		assert.Nil(t, err)
		assert.NotNil(tt, response)
		assert.Contains(t, *response, "address-link")
		mgs.AssertExpectations(t)
	})
}

func Test_DeleteAddress(t *testing.T) {
	consumerVpc := "test-vpc"
	accountName := "test-project"
	addressName := "test-address"
	regionalTenantProject := "test-project"
	clusterDetails := datamodel.ClusterDetails{
		RegionalTenantProject: regionalTenantProject,
	}
	t.Run("WhenGetTenantProjectError", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		DeleteAddress := activities.DeleteAddress
		defer func() {
			activities.DeleteAddress = DeleteAddress
			activities.Region = env.GetString("LOCAL_REGION", "")
		}()

		activities.Region = "test-region"
		errString := "unexpected error"
		mgs.On("GetLogger").Return(util.GetLogger(context.Background()))
		mgs.On("GetTenantProject", consumerVpc, accountName, "test-region").Return("", errors.New(errString))

		_, err := activities.DeleteAddress(mgs, consumerVpc, accountName, addressName, datamodel.ClusterDetails{})

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), errString)
		mgs.AssertExpectations(tt)
	})
	t.Run("WhenGetAddressReturnsError", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		DeleteAddress := activities.DeleteAddress
		defer func() {
			activities.DeleteAddress = DeleteAddress
			activities.Region = env.GetString("LOCAL_REGION", "")
		}()

		activities.Region = "test-region"
		errString := "unexpected error"
		mgs.On("GetLogger").Return(util.GetLogger(context.Background()))
		mgs.On("GetAddress", regionalTenantProject, "test-region", addressName).Return(nil, errors.New(errString))

		response, err := activities.DeleteAddress(mgs, consumerVpc, accountName, addressName, clusterDetails)

		assert.Nil(tt, err)
		assert.Equal(tt, "", response)
		mgs.AssertExpectations(tt)
	})
	t.Run("WhenReleaseAddressReturnsError", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		DeleteAddress := activities.DeleteAddress
		defer func() {
			activities.DeleteAddress = DeleteAddress
			activities.Region = env.GetString("LOCAL_REGION", "")
		}()

		activities.Region = "test-region"
		errString := "unexpected error"
		mgs.On("GetLogger").Return(util.GetLogger(context.Background()))
		mgs.On("GetAddress", regionalTenantProject, "test-region", addressName).Return(nil, nil)
		mgs.On("ReleaseAddress", "test-region", regionalTenantProject, addressName).Return("", errors.New(errString))

		response, err := activities.DeleteAddress(mgs, consumerVpc, accountName, addressName, clusterDetails)

		assert.Nil(t, err)
		assert.Equal(tt, "", response)
		mgs.AssertExpectations(t)
	})
	t.Run("WhenReleaseAddressReturnsResponse", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		DeleteAddress := activities.DeleteAddress
		defer func() {
			activities.DeleteAddress = DeleteAddress
			activities.Region = env.GetString("LOCAL_REGION", "")
		}()

		activities.Region = "test-region"

		mgs.On("GetLogger").Return(util.GetLogger(context.Background()))
		mgs.On("GetAddress", regionalTenantProject, "test-region", addressName).Return(nil, nil)
		mgs.On("ReleaseAddress", "test-region", regionalTenantProject, addressName).Return("response-string", nil)

		response, err := activities.DeleteAddress(mgs, consumerVpc, accountName, addressName, clusterDetails)

		assert.Nil(t, err)
		assert.NotNil(tt, response)
		assert.Contains(t, response, "response-string")
		mgs.AssertExpectations(t)
	})
}

func Test_DeleteForwardingRule(t *testing.T) {
	consumerVpc := "test-vpc"
	accountName := "test-project"
	addressName := "test-address"
	regionalTenantProject := "test-project"
	clusterDetails := datamodel.ClusterDetails{
		RegionalTenantProject: regionalTenantProject,
	}
	t.Run("WhenGetTenantProjectError", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		DeleteForwardingRule := activities.DeleteForwardingRule
		defer func() {
			activities.DeleteForwardingRule = DeleteForwardingRule
			activities.Region = env.GetString("LOCAL_REGION", "")
		}()

		activities.Region = "test-region"
		errString := "unexpected error"
		mgs.On("GetLogger").Return(util.GetLogger(context.Background()))
		mgs.On("GetTenantProject", consumerVpc, accountName, "test-region").Return("", errors.New(errString))

		_, err := activities.DeleteForwardingRule(mgs, consumerVpc, accountName, addressName, datamodel.ClusterDetails{})

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), errString)
		mgs.AssertExpectations(tt)
	})
	t.Run("WhenGetForwardingRuleReturnsError", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		DeleteForwardingRule := activities.DeleteForwardingRule
		defer func() {
			activities.DeleteForwardingRule = DeleteForwardingRule
			activities.Region = env.GetString("LOCAL_REGION", "")
		}()

		activities.Region = "test-region"
		errString := "unexpected error"
		mgs.On("GetLogger").Return(util.GetLogger(context.Background()))
		mgs.On("GetForwardingRule", regionalTenantProject, "test-region", addressName).Return(nil, errors.New(errString))

		response, err := activities.DeleteForwardingRule(mgs, consumerVpc, accountName, addressName, clusterDetails)

		assert.Nil(tt, err)
		assert.Equal(tt, "", response)
		mgs.AssertExpectations(tt)
	})
	t.Run("WhenDeleteForwardingRuleReturnsError", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		DeleteForwardingRule := activities.DeleteForwardingRule
		defer func() {
			activities.DeleteForwardingRule = DeleteForwardingRule
			activities.Region = env.GetString("LOCAL_REGION", "")
		}()

		activities.Region = "test-region"
		errString := "unexpected error"
		mgs.On("GetLogger").Return(util.GetLogger(context.Background()))
		mgs.On("GetForwardingRule", regionalTenantProject, "test-region", addressName).Return(nil, nil)
		mgs.On("DeleteForwardingRule", "test-region", regionalTenantProject, addressName).Return("", errors.New(errString))

		response, err := activities.DeleteForwardingRule(mgs, consumerVpc, accountName, addressName, clusterDetails)

		assert.Nil(t, err)
		assert.Equal(tt, "", response)
		mgs.AssertExpectations(t)
	})
	t.Run("WhenDeleteForwardingRuleReturnsResponse", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		DeleteForwardingRule := activities.DeleteForwardingRule
		defer func() {
			activities.DeleteForwardingRule = DeleteForwardingRule
			activities.Region = env.GetString("LOCAL_REGION", "")
		}()

		activities.Region = "test-region"

		mgs.On("GetLogger").Return(util.GetLogger(context.Background()))
		mgs.On("GetForwardingRule", regionalTenantProject, "test-region", addressName).Return(nil, nil)
		mgs.On("DeleteForwardingRule", "test-region", regionalTenantProject, addressName).Return("response-string", nil)

		response, err := activities.DeleteForwardingRule(mgs, consumerVpc, accountName, addressName, clusterDetails)

		assert.Nil(t, err)
		assert.NotNil(tt, response)
		assert.Contains(t, response, "response-string")
		mgs.AssertExpectations(t)
	})
}

func TestCreateInternalInfraSubnet(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := activities.PSCActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateInternalInfraSubnet)
	project := "test-project"

	t.Run("WhenGetGCPServiceFails", func(tt *testing.T) {
		originalGetGCPService := hyperscaler2.ResolveCloudService
		defer func() {
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()

		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("failed to get GCP service")
		}
		_, err := env.ExecuteActivity(activity.CreateInternalInfraSubnet, project)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get GCP service")
	})
	t.Run("WhenInsertSubnetFails", func(tt *testing.T) {
		originalInsertSubnet := activities.InsertSubnet
		originalGetGCPService := hyperscaler2.ResolveCloudService
		defer func() {
			activities.InsertSubnet = originalInsertSubnet
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()

		activities.InsertSubnet = func(gService hyperscaler2.Services, projectName string, Region *string, subnetName string, vpcName string, ipCidrRange string) (string, error) {
			return "", errors.New("failed to insert subnet")
		}
		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		_, err := env.ExecuteActivity(activity.CreateInternalInfraSubnet, project)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to insert subnet")
	})
	t.Run("WhenInsertSubnetSucceeds", func(tt *testing.T) {
		originalInsertSubnet := activities.InsertSubnet
		originalGetGCPService := hyperscaler2.ResolveCloudService
		defer func() {
			activities.InsertSubnet = originalInsertSubnet
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()

		activities.InsertSubnet = func(gService hyperscaler2.Services, projectName string, Region *string, subnetName string, vpcName string, ipCidrRange string) (string, error) {
			return "tst-subnet", nil
		}
		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		result, err := env.ExecuteActivity(activity.CreateInternalInfraSubnet, project)
		assert.Nil(t, err)
		assert.NotNil(t, result)

		var operations *[]commonparams.Operations
		err = result.Get(&operations)
		assert.NoError(t, err)
		assert.NotNil(t, operations)
		assert.Len(t, *operations, 1)

		op := (*operations)[0]
		assert.Contains(tt, op.OperationName, "tst-subnet")
		assert.False(t, op.IsDone)
		assert.Contains(tt, op.OperationType, "subnet")
		assert.True(tt, op.IsRegionalResource)
		assert.Equal(tt, project, op.Project)
	})
}
func TestPoolActivity_CreateAddressForPSCEndpoint(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := activities.PSCActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateAddressForPSCEndpoint)

	region := "us-central1"
	project := "test-project"
	addressName := "test-address"
	t.Run("WhenGetGCPServiceFails", func(tt *testing.T) {
		originalGetGCPService := hyperscaler2.ResolveCloudService
		defer func() {
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()

		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("failed to get GCP service")
		}
		_, err := env.ExecuteActivity(activity.CreateAddressForPSCEndpoint, project, region, addressName)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get GCP service")
	})
	t.Run("WhenCreateAddressFails", func(tt *testing.T) {
		originalGetGCPService := hyperscaler2.ResolveCloudService
		originalCreateAddress := activities.CreateAddress
		defer func() {
			activities.CreateAddress = originalCreateAddress
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()
		activities.CreateAddress = func(gService hyperscaler2.Services, projectName, region string, subNetwork, privateAddressName string) (string, error) {
			return "", errors.New("failed to create address")
		}
		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		_, err := env.ExecuteActivity(activity.CreateAddressForPSCEndpoint, project, region, addressName)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create address")
	})
	t.Run("WhenCreateAddressSucceeds", func(tt *testing.T) {
		originalGetGCPService := hyperscaler2.ResolveCloudService
		originalCreateAddress := activities.CreateAddress
		defer func() {
			activities.CreateAddress = originalCreateAddress
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()
		activities.CreateAddress = func(gService hyperscaler2.Services, projectName, region string, subNetwork, privateAddressName string) (string, error) {
			return "tst-adr", nil
		}
		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}

		result, err := env.ExecuteActivity(activity.CreateAddressForPSCEndpoint, project, region, addressName)
		assert.Nil(t, err)
		assert.NotNil(t, result)

		var operations *[]commonparams.Operations
		err = result.Get(&operations)
		assert.NoError(t, err)
		assert.NotNil(t, operations)
		assert.Len(t, *operations, 1)

		op := (*operations)[0]
		assert.Contains(tt, op.OperationName, "tst-adr")
		assert.False(t, op.IsDone)
		assert.Contains(tt, op.OperationType, "ipaddress")
		assert.True(tt, op.IsRegionalResource)
		assert.Equal(tt, project, op.Project)
	})
}

func TestPoolActivity_CreateAddress(t *testing.T) {
	projectName := "test-project"
	region := "us-central1"
	subnetName := "test-subnet"
	addressName := "test-address"
	subnet := &hyperscaler3.Subnet{
		SelfLink: "subnet-self-link",
	}
	subnet2 := &hyperscaler3.Subnet{}
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	t.Run("WhenGetGetAddressReturnsNonNotFoundError", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		CreateAddress := activities.CreateAddress
		defer func() {
			activities.CreateAddress = CreateAddress
		}()
		mgs.On("GetLogger").Return(logger)
		mgs.On("GetAddress", projectName, region, addressName).Return(nil, errors.New("Other issues"))

		_, err := activities.CreateAddress(mgs, projectName, region, subnetName, addressName)

		assert.Error(tt, errors.New("Other issues"), err)
		mgs.AssertExpectations(tt)
	})

	t.Run("WhenGetGetSubnetworkFails", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)
		errString := "failed to get subnetwork"
		CreateAddress := activities.CreateAddress
		defer func() {
			activities.CreateAddress = CreateAddress
		}()
		mgs.On("GetLogger").Return(logger)
		mgs.On("GetAddress", projectName, region, addressName).Return(nil, errors.New("not found"))
		mgs.On("GetSubnetwork", projectName, region, subnetName).Return(nil, errors.New(errString))

		_, err := activities.CreateAddress(mgs, projectName, region, subnetName, addressName)
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), fmt.Sprintf("Error getting subnet for project: %s, vpc name: , subnet name: %s. Error : %s", projectName, subnetName, errString))
		} else {
			tt.Fatalf("Expected a CustomError, got: %T", err)
		}
		mgs.AssertExpectations(tt)
	})

	t.Run("WhenGetSubnetworkReturnsNil", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		CreateAddress := activities.CreateAddress
		defer func() {
			activities.CreateAddress = CreateAddress
		}()
		mgs.On("GetLogger").Return(logger)
		mgs.On("GetAddress", projectName, region, addressName).Return(nil, errors.New("not found"))
		mgs.On("GetSubnetwork", projectName, region, subnetName).Return(nil, nil)

		_, err := activities.CreateAddress(mgs, projectName, region, subnetName, addressName)
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), fmt.Sprintf("Error getting subnetwork for project : %s and subnetwork : %s. ", projectName, subnetName))
		} else {
			tt.Fatalf("Expected a CustomError, got: %T", err)
		}
		mgs.AssertExpectations(tt)
	})

	t.Run("WhenGetSubnetworkReturnsNilSelfLink", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		CreateAddress := activities.CreateAddress
		defer func() {
			activities.CreateAddress = CreateAddress
		}()
		mgs.On("GetLogger").Return(logger)
		mgs.On("GetAddress", projectName, region, addressName).Return(nil, errors.New("not found"))
		mgs.On("GetSubnetwork", projectName, region, subnetName).Return(subnet2, nil)

		_, err := activities.CreateAddress(mgs, projectName, region, subnetName, addressName)
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), fmt.Sprintf("Error getting subnetwork for project : %s and subnetwork : %s. ", projectName, subnetName))
		} else {
			tt.Fatalf("Expected a CustomError, got: %T", err)
		}
		mgs.AssertExpectations(tt)
	})

	t.Run("WhenCreateAddressFails", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)
		CreateAddress := activities.CreateAddress
		defer func() {
			activities.CreateAddress = CreateAddress
		}()
		mgs.On("GetLogger").Return(logger)
		mgs.On("GetAddress", projectName, region, addressName).Return(nil, errors.New("not found"))
		mgs.On("GetSubnetwork", projectName, region, subnetName).Return(subnet, nil)
		mgs.On("CreateAddressOperation", mock.Anything).Return("", errors.New("address creation failed"))

		_, err := activities.CreateAddress(mgs, projectName, region, subnetName, addressName)
		assert.Error(tt, errors.New("address creation failed"), err)
		mgs.AssertExpectations(tt)
	})

	t.Run("WhenCreateAddressFails", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)
		CreateAddress := activities.CreateAddress
		defer func() {
			activities.CreateAddress = CreateAddress
		}()
		mgs.On("GetLogger").Return(logger)
		mgs.On("GetAddress", projectName, region, addressName).Return(nil, errors.New("not found"))
		mgs.On("GetSubnetwork", projectName, region, subnetName).Return(subnet, nil)
		mgs.On("CreateAddressOperation", mock.Anything).Return("address", nil)

		response, err := activities.CreateAddress(mgs, projectName, region, subnetName, addressName)
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
		assert.Equal(tt, "address", response)

		mgs.AssertExpectations(tt)
	})
}

func TestPoolActivity_CreateForwardingRuleForPSCEndpoint(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := activities.PSCActivity{SE: mockStorage}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activity.CreateForwardingRuleForPSCEndpoint)

	region := "us-central1"
	project := "test-project"
	addressName := "test-address"
	addressURI := "test-address-uri"
	t.Run("WhenGetGCPServiceFails", func(tt *testing.T) {
		originalGetGCPService := hyperscaler2.ResolveCloudService
		defer func() {
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()

		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("failed to get GCP service")
		}
		_, err := env.ExecuteActivity(activity.CreateForwardingRuleForPSCEndpoint, project, region, addressName, addressURI)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get GCP service")
	})
	t.Run("WhenCreateForwardingRuleFails", func(tt *testing.T) {
		originalGetGCPService := hyperscaler2.ResolveCloudService
		originalCreateForwardingRule := activities.CreateForwardingRule
		defer func() {
			activities.CreateForwardingRule = originalCreateForwardingRule
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()
		activities.CreateForwardingRule = func(gService hyperscaler2.Services, projectName string, region string, privateAddressName string, vpcName string, addressURI string) (string, error) {
			return "", errors.New("failed to create forwarding rule")
		}
		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		_, err := env.ExecuteActivity(activity.CreateForwardingRuleForPSCEndpoint, project, region, addressName, addressURI)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create forwarding rule")
	})
	t.Run("WhenCreateForwardingRuleSucceeds", func(tt *testing.T) {
		originalGetGCPService := hyperscaler2.ResolveCloudService
		originalCreateForwardingRule := activities.CreateForwardingRule
		defer func() {
			activities.CreateForwardingRule = originalCreateForwardingRule
			hyperscaler2.ResolveCloudService = originalGetGCPService
		}()
		activities.CreateForwardingRule = func(gService hyperscaler2.Services, projectName string, region string, privateAddressName string, vpcName string, addressURI string) (string, error) {
			return "tst-fr", nil
		}
		hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}

		result, err := env.ExecuteActivity(activity.CreateForwardingRuleForPSCEndpoint, project, region, addressName, addressURI)
		assert.Nil(t, err)
		assert.NotNil(t, result)

		var operations *[]commonparams.Operations
		err = result.Get(&operations)
		assert.NoError(t, err)
		assert.NotNil(t, operations)
		assert.Len(t, *operations, 1)

		op := (*operations)[0]
		assert.Contains(tt, "tst-fr", op.OperationName)
		assert.False(t, op.IsDone)
		assert.Contains(tt, "forwardingrule", op.OperationType)
		assert.True(tt, op.IsRegionalResource)
		assert.Equal(tt, project, op.Project)
	})
}

func TestPoolActivity_CreateForwardingRule(t *testing.T) {
	projectName := "test-project"
	region := "us-central1"
	vpcName := "test-vpc"
	addressName := "test-address"
	addressURI := "test-address-uri"
	vpcNetwork := &hyperscaler3.VPCNetwork{
		SelfLink: "vpc-self-link",
	}
	vpcNetwork2 := &hyperscaler3.VPCNetwork{}
	ctx := context.TODO()
	logger := util.GetLogger(ctx)

	t.Run("WhenGetForwardingRuleReturnsNonNotFoundError", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		CreateForwardingRule := activities.CreateForwardingRule
		defer func() {
			activities.CreateForwardingRule = CreateForwardingRule
		}()
		mgs.On("GetLogger").Return(logger)
		mgs.On("GetForwardingRule", projectName, region, addressName).Return(nil, errors.New("Other issues"))

		_, err := activities.CreateForwardingRule(mgs, projectName, region, addressName, vpcName, addressURI)
		assert.Error(tt, errors.New("Other issues"), err)
		mgs.AssertExpectations(tt)
	})

	t.Run("WhenGetVPCNetworkFails", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		CreateForwardingRule := activities.CreateForwardingRule
		defer func() {
			activities.CreateForwardingRule = CreateForwardingRule
		}()
		mgs.On("GetLogger").Return(logger)
		mgs.On("GetForwardingRule", projectName, region, addressName).Return(nil, errors.New("not found"))
		mgs.On("GetVPCNetwork", projectName, vpcName).Return(nil, errors.New("not found"))

		_, err := activities.CreateForwardingRule(mgs, projectName, region, addressName, vpcName, addressURI)
		assert.Error(tt, errors.New("not found"), err)
		mgs.AssertExpectations(tt)
	})

	t.Run("WhenGetVPCNetworkReturnsNil", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		CreateForwardingRule := activities.CreateForwardingRule
		defer func() {
			activities.CreateForwardingRule = CreateForwardingRule
		}()
		mgs.On("GetLogger").Return(logger)
		mgs.On("GetForwardingRule", projectName, region, addressName).Return(nil, errors.New("not found"))
		mgs.On("GetVPCNetwork", projectName, vpcName).Return(nil, nil)

		_, err := activities.CreateForwardingRule(mgs, projectName, region, addressName, vpcName, addressURI)
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), fmt.Sprintf("Failed to GetNetwork %v in region %s for project %s", vpcName, region, projectName))
		} else {
			tt.Fatalf("Expected a CustomError, got: %T", err)
		}
		mgs.AssertExpectations(tt)
	})

	t.Run("WhenGetVPCNetworkReturnsNilSelfLink", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		CreateForwardingRule := activities.CreateForwardingRule
		defer func() {
			activities.CreateForwardingRule = CreateForwardingRule
		}()
		mgs.On("GetLogger").Return(logger)
		mgs.On("GetForwardingRule", projectName, region, addressName).Return(nil, errors.New("not found"))
		mgs.On("GetVPCNetwork", projectName, vpcName).Return(vpcNetwork2, nil)

		_, err := activities.CreateForwardingRule(mgs, projectName, region, addressName, vpcName, addressURI)
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), fmt.Sprintf("Failed to GetNetwork %v in region %s for project %s", vpcName, region, projectName))
		} else {
			tt.Fatalf("Expected a CustomError, got: %T", err)
		}
		mgs.AssertExpectations(tt)
	})

	t.Run("WhenCreateCreateForwardingRuleOperationFails", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		CreateForwardingRule := activities.CreateForwardingRule
		defer func() {
			activities.CreateForwardingRule = CreateForwardingRule
		}()
		mgs.On("GetLogger").Return(logger)
		mgs.On("GetForwardingRule", projectName, region, addressName).Return(nil, errors.New("not found"))
		mgs.On("GetVPCNetwork", projectName, vpcName).Return(vpcNetwork, nil)
		mgs.On("CreateForwardingRuleOperation", mock.Anything).Return("", errors.New("Error creating forwarding rule"))

		_, err := activities.CreateForwardingRule(mgs, projectName, region, addressName, vpcName, addressURI)
		assert.Error(tt, errors.New("Error creating forwarding rule"), err)
		mgs.AssertExpectations(tt)
	})
	t.Run("WhenCreateCreateForwardingRuleOperationSuccess", func(tt *testing.T) {
		mgs := hyperscaler2.NewMockServices(tt)

		CreateForwardingRule := activities.CreateForwardingRule
		defer func() {
			activities.CreateForwardingRule = CreateForwardingRule
		}()
		mgs.On("GetLogger").Return(logger)
		mgs.On("GetForwardingRule", projectName, region, addressName).Return(nil, errors.New("not found"))
		mgs.On("GetVPCNetwork", projectName, vpcName).Return(vpcNetwork, nil)
		mgs.On("CreateForwardingRuleOperation", mock.Anything).Return("forwardingRule", nil)

		response, err := activities.CreateForwardingRule(mgs, projectName, region, addressName, vpcName, addressURI)
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
		assert.Equal(tt, "forwardingRule", response)

		mgs.AssertExpectations(tt)
	})
}
