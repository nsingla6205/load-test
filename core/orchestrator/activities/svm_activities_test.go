package activities_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	coremodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	oci "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/oci"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/testsuite"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// SaveSVMAndLifData
// ---------------------------------------------------------------------------

func Test_SaveSVMAndLifData_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.SvmActivity{SE: mockStorage}

	env.RegisterActivity(&activity)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, AccountID: 1}
	vlmConfig := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{DeploymentID: "test-deployment"},
		Svm: map[string]vlm.SvmConfig{
			"gcnv": {
				Svmname: "test-svm",
				Svmuuid: "test-uuid",
				SVMLIFs: map[vlm.VSALIFType][]vlm.LIFConfig{
					vlm.LIFTypeSan: {
						{IP: "192.168.1.1/24", Name: "lif1", HomeNode: "01"},
					},
					vlm.LIFTypeNas: {
						{IP: "192.168.1.1/24", Name: "lif2", HomeNode: "02"},
					},
				},
			},
		},
	}

	mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return(&datamodel.Svm{}, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "01"}, {BaseModel: datamodel.BaseModel{ID: 1}, Name: "02"},
	}, nil)
	mockStorage.On("CreateLif", mock.Anything, mock.Anything).Return(&datamodel.Lif{}, nil)

	_, err := env.ExecuteActivity(activity.SaveSVMAndLifData, pool, vlmConfig, "gcnv")

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

// Verify the SVM record persists the IPspace VLM provisioned at pool-creation time
// (e.g. "ocifsn" for OCI), rather than a hardcoded "Default".
func Test_SaveSVMAndLifData_PersistsCustIPSpaceFromVLMConfig(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.SvmActivity{SE: mockStorage}
	env.RegisterActivity(&activity)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, AccountID: 1}
	vlmConfig := &vlm.VLMConfig{
		VsaCluster: vlm.VsaClusterConfig{CustIPSpace: "ocifsn"},
		Svm: map[string]vlm.SvmConfig{
			"svm-name": {
				Svmname: "svm-name",
				Svmuuid: "svm-uuid",
				SVMLIFs: map[vlm.VSALIFType][]vlm.LIFConfig{
					vlm.LIFTypeSan: {{IP: "10.0.0.1/24", Name: "lif-a", HomeNode: "n1"}},
					vlm.LIFTypeNas: {{IP: "10.0.0.2/24", Name: "lif-b", HomeNode: "n2"}},
				},
			},
		},
	}

	var capturedSvm *datamodel.Svm
	mockStorage.On("CreateSVM", mock.Anything, mock.MatchedBy(func(s *datamodel.Svm) bool {
		capturedSvm = s
		return true
	})).Return(&datamodel.Svm{}, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1"}, {BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2"},
	}, nil)
	mockStorage.On("CreateLif", mock.Anything, mock.Anything).Return(&datamodel.Lif{}, nil)

	_, err := env.ExecuteActivity(activity.SaveSVMAndLifData, pool, vlmConfig, "svm-name")

	assert.NoError(t, err)
	require.NotNil(t, capturedSvm)
	require.NotNil(t, capturedSvm.SvmDetails)
	assert.Equal(t, "ocifsn", capturedSvm.SvmDetails.IPSpace)
	mockStorage.AssertExpectations(t)
}

// When VLM did not populate CustIPSpace (e.g. GCP / pre-OCI clusters), fall back
// to the hyperscaler-driven defaultIPSpace ("Default" on GCP, "ocifsn" on OCI;
// overridable via DEFAULT_IPSPACE). Tests run with HYPERSCALER unset (defaults
// to GCP), so the fallback resolves to "Default" here. The important property
// is that the activity used the resolved fallback rather than failing.
func Test_SaveSVMAndLifData_FallsBackToDefaultIPSpaceWhenVLMConfigEmpty(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.SvmActivity{SE: mockStorage}
	env.RegisterActivity(&activity)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 2}, AccountID: 2}
	vlmConfig := &vlm.VLMConfig{
		// VsaCluster.CustIPSpace deliberately left empty.
		Svm: map[string]vlm.SvmConfig{
			"gcp-svm": {
				Svmname: "gcp-svm",
				Svmuuid: "gcp-uuid",
				SVMLIFs: map[vlm.VSALIFType][]vlm.LIFConfig{
					vlm.LIFTypeSan: {{IP: "10.0.1.1/24", Name: "lif-x", HomeNode: "g1"}},
					vlm.LIFTypeNas: {{IP: "10.0.1.2/24", Name: "lif-y", HomeNode: "g2"}},
				},
			},
		},
	}

	var capturedSvm *datamodel.Svm
	mockStorage.On("CreateSVM", mock.Anything, mock.MatchedBy(func(s *datamodel.Svm) bool {
		capturedSvm = s
		return true
	})).Return(&datamodel.Svm{}, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 10}, Name: "g1"}, {BaseModel: datamodel.BaseModel{ID: 11}, Name: "g2"},
	}, nil)
	mockStorage.On("CreateLif", mock.Anything, mock.Anything).Return(&datamodel.Lif{}, nil)

	_, err := env.ExecuteActivity(activity.SaveSVMAndLifData, pool, vlmConfig, "gcp-svm")

	assert.NoError(t, err)
	require.NotNil(t, capturedSvm)
	require.NotNil(t, capturedSvm.SvmDetails)
	// Default hyperscaler in tests is GCP, so the fallback resolves to "Default".
	assert.Equal(t, "Default", capturedSvm.SvmDetails.IPSpace)
	mockStorage.AssertExpectations(t)
}

func Test_SaveSVMAndLifData_CreatesIlbNasLifs(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.SvmActivity{SE: mockStorage}

	env.RegisterActivity(&activity)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 42}, AccountID: 77}
	vlmConfig := &vlm.VLMConfig{
		Svm: map[string]vlm.SvmConfig{
			"svm-name": {
				Svmname: "svm-name",
				Svmuuid: "svm-uuid",
				SVMLIFs: map[vlm.VSALIFType][]vlm.LIFConfig{
					vlm.LIFTypeSan: {
						{IP: "10.0.0.1/24", Name: "san-lif", HomeNode: "node-san", Uuid: "san-uuid"},
					},
					vlm.LIFTypeNas: {
						{IP: "10.0.0.2/24", Name: "nas-lif", HomeNode: "node-nas", Uuid: "nas-uuid"},
					},
					vlm.LIFTypeIlbNas: {
						{IP: "10.0.0.3/24", Name: "ilb-lif", HomeNode: "node-ilb", Uuid: "ilb-uuid"},
					},
				},
			},
		},
	}

	mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return(&datamodel.Svm{}, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1, UUID: "node-san-uuid"}, Name: "node-san"},
		{BaseModel: datamodel.BaseModel{ID: 2, UUID: "node-nas-uuid"}, Name: "node-nas"},
		{BaseModel: datamodel.BaseModel{ID: 3, UUID: "node-ilb-uuid"}, Name: "node-ilb"},
	}, nil)

	var capturedLifs []*datamodel.Lif
	mockStorage.On("CreateLif", mock.Anything, mock.MatchedBy(func(lif *datamodel.Lif) bool {
		copied := *lif
		if lif.LifDetails != nil {
			detailsCopy := *lif.LifDetails
			copied.LifDetails = &detailsCopy
		}
		capturedLifs = append(capturedLifs, &copied)
		return true
	})).Return(&datamodel.Lif{}, nil).Times(3)

	encodedResult, err := env.ExecuteActivity(activity.SaveSVMAndLifData, pool, vlmConfig, "svm-name")
	assert.NoError(t, err)
	var svm *datamodel.Svm
	err = encodedResult.Get(&svm)

	assert.NoError(t, err)
	assert.NotNil(t, svm)
	require.Len(t, capturedLifs, 3)

	for _, lif := range capturedLifs {
		assert.NotContains(t, lif.IPAddress, "/")
		assert.Equal(t, pool.AccountID, lif.AccountID)
		assert.Equal(t, vsa.DefaultNetmask, lif.SubnetMask)
		require.NotNil(t, lif.LifDetails)
		require.NotEmpty(t, lif.LifDetails.ExternalUUID)
	}

	lifByName := map[string]*datamodel.Lif{}
	for _, lif := range capturedLifs {
		lifByName[lif.Name] = lif
	}

	require.Contains(t, lifByName, "ilb-lif")
	ilbLif := lifByName["ilb-lif"]
	assert.Equal(t, string(vlm.LIFTypeNas), ilbLif.LifDetails.ProtocolType)
	assert.Equal(t, int64(3), ilbLif.NodeID)
	assert.Equal(t, "10.0.0.3", ilbLif.IPAddress)
	assert.Equal(t, "ilb-uuid", ilbLif.LifDetails.ExternalUUID)

	require.Contains(t, lifByName, "san-lif")
	assert.Equal(t, string(vlm.LIFTypeSan), lifByName["san-lif"].LifDetails.ProtocolType)
	assert.Equal(t, int64(1), lifByName["san-lif"].NodeID)

	require.Contains(t, lifByName, "nas-lif")
	assert.Equal(t, string(vlm.LIFTypeNas), lifByName["nas-lif"].LifDetails.ProtocolType)
	assert.Equal(t, int64(2), lifByName["nas-lif"].NodeID)

	mockStorage.AssertExpectations(t)
}

func Test_SaveSVMAndLifDataDBCreationError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.SvmActivity{SE: mockStorage}
	env.RegisterActivity(&activity)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, AccountID: 1}

	vlmConfig := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{DeploymentID: "test-deployment"},
		Svm: map[string]vlm.SvmConfig{
			"gcnv": {
				Svmname: "test-svm",
				Svmuuid: "test-uuid",
				SVMLIFs: map[vlm.VSALIFType][]vlm.LIFConfig{
					vlm.LIFTypeSan: {
						{IP: "192.168.1.1/24", Name: "lif1"},
					},
					vlm.LIFTypeNas: {
						{IP: "192.168.1.1/24", Name: "lif2"},
					},
				},
			},
		},
	}

	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "01"}, {BaseModel: datamodel.BaseModel{ID: 2}, Name: "02"},
	}, nil)
	mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return(&datamodel.Svm{}, errors.New("connection error"))

	_, err := env.ExecuteActivity(activity.SaveSVMAndLifData, pool, vlmConfig, "gcnv")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection error")
	mockStorage.AssertExpectations(t)
}

func Test_SaveSVMAndLifData_CouldNotFetchNodes(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.SvmActivity{SE: mockStorage}
	env.RegisterActivity(&activity)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, AccountID: 1}
	vlmConfig := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{DeploymentID: "test-deployment"},
		Svm: map[string]vlm.SvmConfig{
			"gcnv": {
				Svmname: "test-svm",
				Svmuuid: "test-uuid",
			},
		},
	}

	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return(nil, gorm.ErrRecordNotFound)

	_, err := env.ExecuteActivity(activity.SaveSVMAndLifData, pool, vlmConfig, "gcnv")

	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func Test_SaveSVMAndLifData_NotEnoughNodes(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.SvmActivity{SE: mockStorage}

	env.RegisterActivity(&activity)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, AccountID: 1}
	vlmConfig := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{DeploymentID: "test-deployment"},
		Svm: map[string]vlm.SvmConfig{
			"gcnv": {
				Svmname: "test-svm",
				Svmuuid: "test-uuid",
			},
		},
	}

	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}},
	}, nil)

	_, err := env.ExecuteActivity(activity.SaveSVMAndLifData, pool, vlmConfig, "gcnv")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not enough nodes in the cluster")
	mockStorage.AssertExpectations(t)
}

func Test_SaveSVMAndLifData_FailsToCreateLif(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.SvmActivity{SE: mockStorage}

	env.RegisterActivity(&activity)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, AccountID: 1}
	vlmConfig := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{DeploymentID: "test-deployment"},
		Svm: map[string]vlm.SvmConfig{
			"gcnv": {
				Svmname: "test-svm",
				Svmuuid: "test-uuid",
				SVMLIFs: map[vlm.VSALIFType][]vlm.LIFConfig{
					vlm.LIFTypeSan: {
						{IP: "192.168.1.1/24", Name: "lif1", HomeNode: "01"},
					},
				},
			},
		},
	}

	mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return(&datamodel.Svm{}, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "01"}, {BaseModel: datamodel.BaseModel{ID: 1}, Name: "02"},
	}, nil)
	mockStorage.On("CreateLif", mock.Anything, mock.Anything).Return(nil, errors.New("failed to create LIF"))

	_, err := env.ExecuteActivity(activity.SaveSVMAndLifData, pool, vlmConfig, "gcnv")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create LIF")
	mockStorage.AssertExpectations(t)
}

func Test_SaveSVMAndLifData_NonExistentHomeNode(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := activities.SvmActivity{SE: mockStorage}
	env.RegisterActivity(&activity)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, AccountID: 1}
	vlmConfig := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{DeploymentID: "test-deployment"},
		Svm: map[string]vlm.SvmConfig{
			"gcnv": {
				Svmname: "test-svm",
				Svmuuid: "test-uuid",
				SVMLIFs: map[vlm.VSALIFType][]vlm.LIFConfig{
					vlm.LIFTypeSan: {
						{IP: "192.168.1.1/24", Name: "lif1", HomeNode: "non-existent-node"},
					},
				},
			},
		},
	}

	// Mock nodes that exist in the database
	mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return(&datamodel.Svm{}, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "existing-node"},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "another-node"},
	}, nil)

	_, err := env.ExecuteActivity(activity.SaveSVMAndLifData, pool, vlmConfig, "gcnv")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "LIF lif1 references non-existent home node non-existent-node")
	mockStorage.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// CreateQoSPolicyAndApplyToSVM
// ---------------------------------------------------------------------------

func TestCreateQoSPolicyAndApplyToSVM(t *testing.T) {
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1},
		Name:      "test-pool",
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 1000,
			Iops:            5000,
		},
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: env.USERNAME_PWD,
			Password: "test-password",
		},
	}
	svm := &datamodel.Svm{
		BaseModel: datamodel.BaseModel{ID: 1},
		Name:      "test-svm",
		SvmDetails: &datamodel.SvmDetails{
			ExternalUUID: "test-svm-uuid",
		},
	}
	node := &coremodel.Node{
		Name:                           "test-node",
		EndpointAddress:                "1.2.3.4",
		AuthType:                       env.USERNAME_PWD,
		EndpointAddressesToHostNameMap: make(map[string]string),
	}

	t.Run("WhenQoSPolicyDoesNotExist_ThenCreateAndApply", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(nil, errors.New("policy not found"))

		expectedQoSPolicy := &vsa.QoSGroupPolicyResponse{
			Name:          "test-svm-qos-policy",
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 1000,
			MaxIOPS:       5000,
		}

		isShared := true
		mockProvider.On("CreateQoSGroupPolicy", vsa.CreateQoSGroupPolicyParams{
			Name:          "test-svm-qos-policy",
			SvmName:       "test-svm",
			MaxThroughput: 1000,
			MaxIOPS:       5000,
			IsShared:      &isShared,
		}).Return(expectedQoSPolicy, nil)

		mockProvider.On("ModifySVMWithQoSPolicy", vsa.ModifySVMWithQoSPolicyParams{
			SvmUUID:       "test-svm-uuid",
			QoSPolicyName: "test-svm-qos-policy",
		}).Return(nil)

		activity := &activities.SvmActivity{}
		env.RegisterActivity(activity.CreateQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.CreateQoSPolicyAndApplyToSVM, pool, svm, node)

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("WhenQoSPolicyExistsWithSameValues_ThenSkipCreation", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		existingQoSPolicy := &vsa.QoSGroupPolicyResponse{
			Name:          "test-svm-qos-policy",
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 1000,
			MaxIOPS:       5000,
		}

		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(existingQoSPolicy, nil)

		mockProvider.On("ModifySVMWithQoSPolicy", vsa.ModifySVMWithQoSPolicyParams{
			SvmUUID:       "test-svm-uuid",
			QoSPolicyName: "test-svm-qos-policy",
		}).Return(nil)

		activity := &activities.SvmActivity{}
		env.RegisterActivity(activity.CreateQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.CreateQoSPolicyAndApplyToSVM, pool, svm, node)

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("WhenQoSPolicyExistsWithDifferentValues_ThenUpdateAndApply", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		existingQoSPolicy := &vsa.QoSGroupPolicyResponse{
			Name:          "test-svm-qos-policy",
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 500,
			MaxIOPS:       2500,
		}

		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(existingQoSPolicy, nil)

		mockProvider.On("UpdateQoSGroupPolicy", vsa.UpdateQoSGroupPolicyParams{
			UUID:          "test-qos-uuid",
			Name:          "",
			SvmName:       "test-svm",
			MaxThroughput: 1000,
			MaxIOPS:       5000,
		}).Return(nil)

		mockProvider.On("ModifySVMWithQoSPolicy", vsa.ModifySVMWithQoSPolicyParams{
			SvmUUID:       "test-svm-uuid",
			QoSPolicyName: "test-svm-qos-policy",
		}).Return(nil)

		activity := &activities.SvmActivity{}
		env.RegisterActivity(activity.CreateQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.CreateQoSPolicyAndApplyToSVM, pool, svm, node)

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("WhenUpdateQoSGroupPolicyFails_ThenReturnError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		existingQoSPolicy := &vsa.QoSGroupPolicyResponse{
			Name:          "test-svm-qos-policy",
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 500,
			MaxIOPS:       2500,
		}

		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(existingQoSPolicy, nil)

		mockProvider.On("UpdateQoSGroupPolicy", mock.Anything).Return(errors.New("update failed"))

		activity := &activities.SvmActivity{}
		env.RegisterActivity(activity.CreateQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.CreateQoSPolicyAndApplyToSVM, pool, svm, node)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "update failed")
		mockProvider.AssertExpectations(tt)
	})

	t.Run("WhenGetProviderByNodeFails_ThenReturnError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return nil, errors.New("provider error")
		}

		activity := &activities.SvmActivity{}
		env.RegisterActivity(activity.CreateQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.CreateQoSPolicyAndApplyToSVM, pool, svm, node)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "provider error")
	})

	t.Run("WhenFindQoSGroupPolicyFails_ThenCreateNew", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("FindQoSGroupPolicy", mock.Anything).Return(nil, errors.New("policy not found"))

		expectedQoSPolicy := &vsa.QoSGroupPolicyResponse{
			Name:          "test-svm-qos-policy",
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 1000,
			MaxIOPS:       5000,
		}

		mockProvider.On("CreateQoSGroupPolicy", mock.Anything).Return(expectedQoSPolicy, nil)
		mockProvider.On("ModifySVMWithQoSPolicy", mock.Anything).Return(nil)

		activity := &activities.SvmActivity{}
		env.RegisterActivity(activity.CreateQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.CreateQoSPolicyAndApplyToSVM, pool, svm, node)

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("WhenQoSPolicyCreationFails_ThenReturnError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("FindQoSGroupPolicy", mock.Anything).Return(nil, errors.New("policy not found"))
		mockProvider.On("CreateQoSGroupPolicy", mock.Anything).Return(nil, errors.New("qos creation failed"))

		activity := &activities.SvmActivity{}
		env.RegisterActivity(activity.CreateQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.CreateQoSPolicyAndApplyToSVM, pool, svm, node)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "qos creation failed")
		mockProvider.AssertExpectations(tt)
	})

	t.Run("WhenSVMModificationFails_ThenReturnError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("FindQoSGroupPolicy", mock.Anything).Return(nil, errors.New("policy not found"))

		expectedQoSPolicy := &vsa.QoSGroupPolicyResponse{
			Name:          "test-svm-qos-policy",
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 1000,
			MaxIOPS:       5000,
		}

		mockProvider.On("CreateQoSGroupPolicy", mock.Anything).Return(expectedQoSPolicy, nil)
		mockProvider.On("ModifySVMWithQoSPolicy", mock.Anything).Return(errors.New("svm modification failed"))

		activity := &activities.SvmActivity{}
		env.RegisterActivity(activity.CreateQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.CreateQoSPolicyAndApplyToSVM, pool, svm, node)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "svm modification failed")
		mockProvider.AssertExpectations(tt)
	})

	t.Run("WhenQoSPolicyNameIsGeneratedCorrectly", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("FindQoSGroupPolicy", mock.Anything).Return(nil, errors.New("policy not found"))

		isShared := true
		mockProvider.On("CreateQoSGroupPolicy", vsa.CreateQoSGroupPolicyParams{
			Name:          "test-svm-qos-policy",
			SvmName:       "test-svm",
			MaxThroughput: 1000,
			MaxIOPS:       5000,
			IsShared:      &isShared,
		}).Return(&vsa.QoSGroupPolicyResponse{
			Name:          "test-svm-qos-policy",
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 1000,
			MaxIOPS:       5000,
		}, nil)

		mockProvider.On("ModifySVMWithQoSPolicy", vsa.ModifySVMWithQoSPolicyParams{
			SvmUUID:       "test-svm-uuid",
			QoSPolicyName: "test-svm-qos-policy",
		}).Return(nil)

		activity := &activities.SvmActivity{}
		env.RegisterActivity(activity.CreateQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.CreateQoSPolicyAndApplyToSVM, pool, svm, node)

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("WhenQosTypeIsManual_ThenSkipCreationAndReturnEarly", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		testEnv := ts.NewTestActivityEnvironment()

		poolWithManualQoS := *pool
		poolWithManualQoS.QosType = utils.QosTypeManual

		activity := &activities.SvmActivity{}
		testEnv.RegisterActivity(activity.CreateQoSPolicyAndApplyToSVM)
		_, err := testEnv.ExecuteActivity(activity.CreateQoSPolicyAndApplyToSVM, &poolWithManualQoS, svm, node)

		assert.NoError(tt, err)
	})
}

// ---------------------------------------------------------------------------
// ModifyQoSPolicyAndApplyToSVM
// ---------------------------------------------------------------------------

func TestModifyQoSPolicyAndApplyToSVM(t *testing.T) {
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1},
		Name:      "test-pool",
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 1000,
			Iops:            5000,
		},
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: env.USERNAME_PWD,
			Password: "test-password",
		},
	}
	updateParams := &commonparams.UpdatePoolParams{
		TotalThroughputMibps: 2000,
		TotalIops:            nillable.ToPointer(int64(6000)),
	}
	node := &coremodel.Node{
		Name:                           "test-node",
		EndpointAddress:                "1.2.3.4",
		AuthType:                       env.USERNAME_PWD,
		EndpointAddressesToHostNameMap: make(map[string]string),
	}

	t.Run("WhenQoSPolicyNeedsUpdate_ThenUpdateAndApply", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-svm",
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "test-svm-uuid",
			},
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)

		existingQoSPolicy := &vsa.QoSGroupPolicyResponse{
			Name:          "test-svm-qos-policy",
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 1000,
			MaxIOPS:       5000,
		}

		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(existingQoSPolicy, nil)

		mockProvider.On("UpdateQoSGroupPolicy", vsa.UpdateQoSGroupPolicyParams{
			UUID:          "test-qos-uuid",
			Name:          "",
			SvmName:       "test-svm",
			MaxThroughput: 2000,
			MaxIOPS:       6000,
		}).Return(nil)

		mockProvider.On("ModifySVMWithQoSPolicy", vsa.ModifySVMWithQoSPolicyParams{
			SvmUUID:       "test-svm-uuid",
			QoSPolicyName: "test-svm-qos-policy",
		}).Return(nil)

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, pool, node, updateParams)

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenQoSPolicyNoChangeNeeded_ThenSkipUpdate", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-svm",
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "test-svm-uuid",
			},
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)

		existingQoSPolicy := &vsa.QoSGroupPolicyResponse{
			Name:          "test-svm-qos-policy",
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 2000,
			MaxIOPS:       6000,
		}

		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(existingQoSPolicy, nil)

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, pool, node, updateParams)

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenGetProviderByNodeFails_ThenReturnError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return nil, errors.New("provider error")
		}

		activity := &activities.SvmActivity{}
		env.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, pool, node, updateParams)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "provider error")
	})

	t.Run("WhenGetSvmForPoolIDFails_ThenReturnError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(nil, errors.New("SVM not found"))

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, pool, node, updateParams)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SVM not found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenFindQoSGroupPolicyFails_ThenReturnError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-svm",
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "test-svm-uuid",
			},
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)

		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(nil, errors.New("QoS policy not found"))

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, pool, node, updateParams)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "QoS policy not found")
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenManualToAuto_AndPolicyExists_ThenApplyToSVM", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-svm",
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "test-svm-uuid",
			},
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)

		poolManual := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-pool",
			QosType:   utils.QosTypeManual,
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 1000,
				Iops:            5000,
			},
		}
		paramsManualToAuto := &commonparams.UpdatePoolParams{
			QosType:              utils.QosTypeAuto,
			TotalThroughputMibps: 1000,
			TotalIops:            nillable.ToPointer(int64(5000)),
		}

		existingQoSPolicy := &vsa.QoSGroupPolicyResponse{
			Name:          "test-svm-qos-policy",
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 1000,
			MaxIOPS:       5000,
		}
		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(existingQoSPolicy, nil)
		mockProvider.On("ModifySVMWithQoSPolicy", vsa.ModifySVMWithQoSPolicyParams{
			SvmUUID:       "test-svm-uuid",
			QoSPolicyName: "test-svm-qos-policy",
		}).Return(nil)

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, poolManual, node, paramsManualToAuto)

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenManualToAuto_AndPolicyNotFound_ThenCreateAndApply", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-svm",
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "test-svm-uuid",
			},
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)

		poolManual := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-pool",
			QosType:   utils.QosTypeManual,
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 128,
				Iops:            2048,
			},
		}
		paramsManualToAuto := &commonparams.UpdatePoolParams{
			QosType:              utils.QosTypeAuto,
			TotalThroughputMibps: 128,
			TotalIops:            nillable.ToPointer(int64(2048)),
		}

		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(nil, utilErrors.NewNotFoundErr("QoS policy", nil))
		mockProvider.On("CreateQoSGroupPolicy", mock.MatchedBy(func(p vsa.CreateQoSGroupPolicyParams) bool {
			return p.Name == "test-svm-qos-policy" && p.SvmName == "test-svm" && p.MaxThroughput == 128 && p.MaxIOPS == 2048
		})).Return(&vsa.QoSGroupPolicyResponse{
			Name: "test-svm-qos-policy",
			UUID: "new-uuid",
		}, nil)
		mockProvider.On("ModifySVMWithQoSPolicy", vsa.ModifySVMWithQoSPolicyParams{
			SvmUUID:       "test-svm-uuid",
			QoSPolicyName: "test-svm-qos-policy",
		}).Return(nil)

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, poolManual, node, paramsManualToAuto)

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenManualToAuto_AndFindReturnsNonNotFoundError_ThenReturnErrorWithoutCreating", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		vsa.GetProviderByNode = func(ctx context.Context, n *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		svm := &datamodel.Svm{
			BaseModel:  datamodel.BaseModel{ID: 1},
			Name:       "test-svm",
			SvmDetails: &datamodel.SvmDetails{ExternalUUID: "test-svm-uuid"},
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)

		poolManual := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-pool",
			QosType:   utils.QosTypeManual,
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 128,
				Iops:            2048,
			},
		}
		paramsManualToAuto := &commonparams.UpdatePoolParams{
			QosType:              utils.QosTypeAuto,
			TotalThroughputMibps: 128,
			TotalIops:            nillable.ToPointer(int64(2048)),
		}

		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(nil, errors.New("transient API error"))

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, poolManual, node, paramsManualToAuto)

		assert.Error(tt, err)
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenUpdateParamsNilAndPoolHasPoolAttributes_ThenUsePoolThroughputAndIops", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
		vsa.GetProviderByNode = func(ctx context.Context, n *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		svm := &datamodel.Svm{
			BaseModel:  datamodel.BaseModel{ID: 1},
			Name:       "test-svm",
			SvmDetails: &datamodel.SvmDetails{ExternalUUID: "test-svm-uuid"},
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)

		poolWithAttrs := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-pool",
			QosType:   utils.QosTypeAuto,
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 100,
				Iops:            200,
			},
		}
		existingQoSPolicy := &vsa.QoSGroupPolicyResponse{
			Name:          "test-svm-qos-policy",
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 50,
			MaxIOPS:       100,
		}
		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(existingQoSPolicy, nil)
		mockProvider.On("UpdateQoSGroupPolicy", vsa.UpdateQoSGroupPolicyParams{
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 100,
			MaxIOPS:       200,
		}).Return(nil)
		mockProvider.On("ModifySVMWithQoSPolicy", vsa.ModifySVMWithQoSPolicyParams{
			SvmUUID:       "test-svm-uuid",
			QoSPolicyName: "test-svm-qos-policy",
		}).Return(nil)

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, poolWithAttrs, node, nil)

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenManualToAutoAndCreateQoSFails_ThenReturnError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
		vsa.GetProviderByNode = func(ctx context.Context, n *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		svm := &datamodel.Svm{
			BaseModel:  datamodel.BaseModel{ID: 1},
			Name:       "test-svm",
			SvmDetails: &datamodel.SvmDetails{ExternalUUID: "test-svm-uuid"},
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)

		poolManual := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-pool",
			QosType:   utils.QosTypeManual,
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 128,
				Iops:            2048,
			},
		}
		paramsManualToAuto := &commonparams.UpdatePoolParams{
			QosType:              utils.QosTypeAuto,
			TotalThroughputMibps: 128,
			TotalIops:            nillable.ToPointer(int64(2048)),
		}

		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(nil, utilErrors.NewNotFoundErr("QoS policy", nil))
		mockProvider.On("CreateQoSGroupPolicy", mock.Anything).Return(nil, errors.New("create QoS policy failed"))

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, poolManual, node, paramsManualToAuto)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "create QoS policy failed")
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenUpdateQoSGroupPolicyFails_ThenReturnError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-svm",
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "test-svm-uuid",
			},
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)

		existingQoSPolicy := &vsa.QoSGroupPolicyResponse{
			Name:          "test-svm-qos-policy",
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 1000,
			MaxIOPS:       5000,
		}

		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(existingQoSPolicy, nil)
		mockProvider.On("UpdateQoSGroupPolicy", vsa.UpdateQoSGroupPolicyParams{
			UUID:          "test-qos-uuid",
			Name:          "",
			SvmName:       "test-svm",
			MaxThroughput: 2000,
			MaxIOPS:       6000,
		}).Return(errors.New("update failed"))

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, pool, node, updateParams)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "update failed")
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenModifySVMWithQoSPolicyFails_ThenReturnError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, node *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-svm",
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "test-svm-uuid",
			},
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)

		existingQoSPolicy := &vsa.QoSGroupPolicyResponse{
			Name:          "test-svm-qos-policy",
			UUID:          "test-qos-uuid",
			SvmName:       "test-svm",
			MaxThroughput: 1000,
			MaxIOPS:       5000,
		}

		mockProvider.On("FindQoSGroupPolicy", vsa.FindQoSGroupPolicyParams{
			Name:    "test-svm-qos-policy",
			SvmName: "test-svm",
		}).Return(existingQoSPolicy, nil)
		mockProvider.On("UpdateQoSGroupPolicy", vsa.UpdateQoSGroupPolicyParams{
			UUID:          "test-qos-uuid",
			Name:          "",
			SvmName:       "test-svm",
			MaxThroughput: 2000,
			MaxIOPS:       6000,
		}).Return(nil)
		mockProvider.On("ModifySVMWithQoSPolicy", vsa.ModifySVMWithQoSPolicyParams{
			SvmUUID:       "test-svm-uuid",
			QoSPolicyName: "test-svm-qos-policy",
		}).Return(errors.New("SVM modification failed"))

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := env.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, pool, node, updateParams)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SVM modification failed")
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenQosTypeIsManual_ThenSkipModificationAndReturnEarly", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		testEnv := ts.NewTestActivityEnvironment()

		poolWithManualQoS := *pool
		poolWithManualQoS.QosType = utils.QosTypeManual

		updateParamsManualOnly := &commonparams.UpdatePoolParams{
			TotalThroughputMibps: updateParams.TotalThroughputMibps,
			TotalIops:            updateParams.TotalIops,
			QosType:              utils.QosTypeManual,
		}

		activity := &activities.SvmActivity{}
		testEnv.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := testEnv.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, &poolWithManualQoS, node, updateParamsManualOnly)

		assert.NoError(tt, err)
	})

	t.Run("WhenQosTypeIsManualAndParamsNil_ThenSkipAndLeaveQosTypeUnchanged", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		testEnv := ts.NewTestActivityEnvironment()
		poolWithManualQoS := *pool
		poolWithManualQoS.QosType = utils.QosTypeManual
		activity := &activities.SvmActivity{}
		testEnv.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := testEnv.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, &poolWithManualQoS, node, nil)
		assert.NoError(tt, err)
	})

	t.Run("WhenQosTypeIsManualAndParamsEmptyQosType_ThenSkipAndLeaveQosTypeUnchanged", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		testEnv := ts.NewTestActivityEnvironment()
		poolWithManualQoS := *pool
		poolWithManualQoS.QosType = utils.QosTypeManual
		paramsEmptyQos := &commonparams.UpdatePoolParams{
			TotalThroughputMibps: 100,
			TotalIops:            updateParams.TotalIops,
			QosType:              "",
		}
		activity := &activities.SvmActivity{}
		testEnv.RegisterActivity(activity.ModifyQoSPolicyAndApplyToSVM)
		_, err := testEnv.ExecuteActivity(activity.ModifyQoSPolicyAndApplyToSVM, &poolWithManualQoS, node, paramsEmptyQos)
		assert.NoError(tt, err)
	})
}

// ---------------------------------------------------------------------------
// RemoveQoSPolicyFromSVM
// ---------------------------------------------------------------------------

func TestRemoveQoSPolicyFromSVM(t *testing.T) {
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1},
		Name:      "test-pool",
	}
	node := &coremodel.Node{
		Name:                           "test-node",
		EndpointAddress:                "1.2.3.4",
		AuthType:                       env.USERNAME_PWD,
		EndpointAddressesToHostNameMap: make(map[string]string),
	}

	t.Run("WhenSuccess_ThenClearPolicyFromSVM", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, n *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-svm",
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "test-svm-uuid",
			},
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)
		mockProvider.On("ModifySVMWithQoSPolicy", vsa.ModifySVMWithQoSPolicyParams{
			SvmUUID:       "test-svm-uuid",
			QoSPolicyName: "",
		}).Return(nil)

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.RemoveQoSPolicyFromSVM)
		_, err := env.ExecuteActivity(activity.RemoveQoSPolicyFromSVM, pool, node)

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenGetSvmForPoolIDFails_ThenReturnError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(nil, errors.New("SVM not found"))

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.RemoveQoSPolicyFromSVM)
		_, err := env.ExecuteActivity(activity.RemoveQoSPolicyFromSVM, pool, node)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SVM not found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSvmDetailsNil_ThenReturnValidationError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(t)
		svmNoDetails := &datamodel.Svm{
			BaseModel:  datamodel.BaseModel{ID: 1},
			Name:       "test-svm",
			SvmDetails: nil,
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svmNoDetails, nil)

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.RemoveQoSPolicyFromSVM)
		_, err := env.ExecuteActivity(activity.RemoveQoSPolicyFromSVM, pool, node)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SVM or SvmDetails is nil")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenGetProviderByNodeFails_ThenReturnError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
		vsa.GetProviderByNode = func(ctx context.Context, n *coremodel.Node) (vsa.Provider, error) {
			return nil, errors.New("provider not found")
		}

		svm := &datamodel.Svm{
			BaseModel:  datamodel.BaseModel{ID: 1},
			Name:       "test-svm",
			SvmDetails: &datamodel.SvmDetails{ExternalUUID: "test-svm-uuid"},
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.RemoveQoSPolicyFromSVM)
		_, err := env.ExecuteActivity(activity.RemoveQoSPolicyFromSVM, pool, node)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "provider not found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenProviderClearFails_ThenReturnError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			vsa.GetProviderByNode = originalGetProviderByNode
		}()

		vsa.GetProviderByNode = func(ctx context.Context, n *coremodel.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-svm",
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "test-svm-uuid",
			},
		}
		mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)
		mockProvider.On("ModifySVMWithQoSPolicy", vsa.ModifySVMWithQoSPolicyParams{
			SvmUUID:       "test-svm-uuid",
			QoSPolicyName: "",
		}).Return(errors.New("ONTAP clear policy failed"))

		activity := &activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.RemoveQoSPolicyFromSVM)
		_, err := env.ExecuteActivity(activity.RemoveQoSPolicyFromSVM, pool, node)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "ONTAP clear policy failed")
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
}

// ---------------------------------------------------------------------------
// AllocateSVMName
// ---------------------------------------------------------------------------

func TestAllocateSVMName(t *testing.T) {
	t.Run("FirstSVMInPool", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(t)
		activity := activities.SvmActivity{SE: mockStorage}
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 123},
			DeploymentName: "gcnv",
		}

		mockStorage.On("GetNextSVMIndexByPoolID", mock.Anything, int64(123)).Return(int64(1), nil)

		env.RegisterActivity(activity.AllocateSVMName)
		val, err := env.ExecuteActivity(activity.AllocateSVMName, pool)

		assert.NoError(t, err)
		var result string
		err = val.Get(&result)
		assert.NoError(t, err)
		assert.Equal(t, "gcnv-svm-01", result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SecondSVMInPool", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(t)
		activity := activities.SvmActivity{SE: mockStorage}
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 123},
			DeploymentName: "gcnv",
		}

		mockStorage.On("GetNextSVMIndexByPoolID", mock.Anything, int64(123)).Return(int64(2), nil)

		env.RegisterActivity(activity.AllocateSVMName)
		val, err := env.ExecuteActivity(activity.AllocateSVMName, pool)

		assert.NoError(t, err)
		var result string
		err = val.Get(&result)
		assert.NoError(t, err)
		assert.Equal(t, "gcnv-svm-02", result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("TenthSVMInPool", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(t)
		activity := activities.SvmActivity{SE: mockStorage}
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 123},
			DeploymentName: "gcnv",
		}

		mockStorage.On("GetNextSVMIndexByPoolID", mock.Anything, int64(123)).Return(int64(10), nil)

		env.RegisterActivity(activity.AllocateSVMName)
		val, err := env.ExecuteActivity(activity.AllocateSVMName, pool)

		assert.NoError(t, err)
		var result string
		err = val.Get(&result)
		assert.NoError(t, err)
		assert.Equal(t, "gcnv-svm-10", result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("EleventhSVMInPool", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(t)
		activity := activities.SvmActivity{SE: mockStorage}
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 123},
			DeploymentName: "gcnv",
		}

		mockStorage.On("GetNextSVMIndexByPoolID", mock.Anything, int64(123)).Return(int64(11), nil)

		env.RegisterActivity(activity.AllocateSVMName)
		val, err := env.ExecuteActivity(activity.AllocateSVMName, pool)

		assert.NoError(t, err)
		var result string
		err = val.Get(&result)
		assert.NoError(t, err)
		assert.Equal(t, "gcnv-svm-11", result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("NinetyNinthSVMInPool", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(t)
		activity := activities.SvmActivity{SE: mockStorage}
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 123},
			DeploymentName: "gcnv",
		}

		mockStorage.On("GetNextSVMIndexByPoolID", mock.Anything, int64(123)).Return(int64(99), nil)

		env.RegisterActivity(activity.AllocateSVMName)
		val, err := env.ExecuteActivity(activity.AllocateSVMName, pool)

		assert.NoError(t, err)
		var result string
		err = val.Get(&result)
		assert.NoError(t, err)
		assert.Equal(t, "gcnv-svm-99", result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("HundredthSVMInPool", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(t)
		activity := activities.SvmActivity{SE: mockStorage}
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 123},
			DeploymentName: "gcnv",
		}

		mockStorage.On("GetNextSVMIndexByPoolID", mock.Anything, int64(123)).Return(int64(100), nil)

		env.RegisterActivity(activity.AllocateSVMName)
		val, err := env.ExecuteActivity(activity.AllocateSVMName, pool)

		assert.NoError(t, err)
		var result string
		err = val.Get(&result)
		assert.NoError(t, err)
		assert.Equal(t, "gcnv-svm-100", result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DifferentDeploymentName", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(t)
		activity := activities.SvmActivity{SE: mockStorage}
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 456},
			DeploymentName: "test-deployment",
		}

		mockStorage.On("GetNextSVMIndexByPoolID", mock.Anything, int64(456)).Return(int64(6), nil)

		env.RegisterActivity(activity.AllocateSVMName)
		val, err := env.ExecuteActivity(activity.AllocateSVMName, pool)

		assert.NoError(t, err)
		var result string
		err = val.Get(&result)
		assert.NoError(t, err)
		assert.Equal(t, "test-deployment-svm-06", result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DatabaseError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(t)
		activity := activities.SvmActivity{SE: mockStorage}
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 123},
			DeploymentName: "gcnv",
		}

		expectedError := vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, fmt.Errorf("database connection failed"))
		mockStorage.On("GetNextSVMIndexByPoolID", mock.Anything, int64(123)).Return(int64(0), expectedError)

		env.RegisterActivity(activity.AllocateSVMName)
		_, err := env.ExecuteActivity(activity.AllocateSVMName, pool)

		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
}

// ---------------------------------------------------------------------------
// MarkSvmDeleting / SoftDeleteSvm / MarkSvmAsErroredForDeletion
// ---------------------------------------------------------------------------

func TestMarkSvmDeleting(t *testing.T) {
	t.Run("Success", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.MarkSvmDeleting)

		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 1}, Name: "svm1"}
		mockStorage.On("DeletingSVM", mock.Anything, svm).Return(nil)

		_, err := env.ExecuteActivity(activity.MarkSvmDeleting, svm)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Fails", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.MarkSvmDeleting)

		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 1}, Name: "svm1"}
		mockStorage.On("DeletingSVM", mock.Anything, svm).Return(errors.New("state transition failed"))

		_, err := env.ExecuteActivity(activity.MarkSvmDeleting, svm)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "state transition failed")
		mockStorage.AssertExpectations(tt)
	})
}

func TestSoftDeleteSvm(t *testing.T) {
	t.Run("Success", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.SoftDeleteSvm)

		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 1}, Name: "svm1"}
		mockStorage.On("DeleteSVM", mock.Anything, svm).Return(nil)

		_, err := env.ExecuteActivity(activity.SoftDeleteSvm, svm)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Fails", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.SoftDeleteSvm)

		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 1}, Name: "svm1"}
		mockStorage.On("DeleteSVM", mock.Anything, svm).Return(errors.New("soft delete failed"))

		_, err := env.ExecuteActivity(activity.SoftDeleteSvm, svm)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "soft delete failed")
		mockStorage.AssertExpectations(tt)
	})
}

func TestMarkSvmAsErroredForDeletion(t *testing.T) {
	t.Run("WithErrMessage", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.MarkSvmAsErroredForDeletion)

		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 1}, Name: "svm1"}
		mockStorage.On("ErroredSVM", mock.Anything, svm, "soft delete failed").Return(nil)

		_, err := env.ExecuteActivity(activity.MarkSvmAsErroredForDeletion, svm, "soft delete failed")

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("EmptyErrMessageFallsBackToDefault", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.MarkSvmAsErroredForDeletion)

		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 1}, Name: "svm1"}
		mockStorage.On("ErroredSVM", mock.Anything, svm, datamodel.LifeCycleStateDeletionErrorDetails).Return(nil)

		_, err := env.ExecuteActivity(activity.MarkSvmAsErroredForDeletion, svm, "")

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ErroredSVMFails", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(activity.MarkSvmAsErroredForDeletion)

		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 1}, Name: "svm1"}
		mockStorage.On("ErroredSVM", mock.Anything, svm, "boom").Return(errors.New("db error"))

		_, err := env.ExecuteActivity(activity.MarkSvmAsErroredForDeletion, svm, "boom")

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "db error")
		mockStorage.AssertExpectations(tt)
	})
}

// ---------------------------------------------------------------------------
// GetSvmAdminPasswordSecretForOCI
// ---------------------------------------------------------------------------

func TestGetSvmAdminPasswordSecretForOCI(t *testing.T) {
	act := &activities.SvmActivity{}

	origGetOCIService := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCIService }()

	svm := &datamodel.Svm{
		Name:                  "test-svm",
		SvmExternalIdentifier: "ocid1.svm.oc1..testsvm",
	}

	t.Run("nil svm returns non-retryable error", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		testEnv := testSuite.NewTestActivityEnvironment()
		testEnv.RegisterActivity(act.GetSvmAdminPasswordSecretForOCI)

		adminPw := &commonparams.OciAdminPassword{Ocid: "ocid1.vaultsecret.oc1..abc", Version: 1}
		_, err := testEnv.ExecuteActivity(act.GetSvmAdminPasswordSecretForOCI, nil, adminPw)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "svm must not be nil")
	})

	t.Run("GetOCIService fails", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		testEnv := testSuite.NewTestActivityEnvironment()
		testEnv.RegisterActivity(act.GetSvmAdminPasswordSecretForOCI)

		hyperscaler.GetOCIService = func(ctx context.Context) (*oci.OciServices, error) {
			return nil, fmt.Errorf("OCI client initialization failed")
		}

		adminPw := &commonparams.OciAdminPassword{Ocid: "ocid1.vaultsecret.oc1..abc", Version: 1}
		_, err := testEnv.ExecuteActivity(act.GetSvmAdminPasswordSecretForOCI, svm, adminPw)
		assert.Error(t, err)
	})

	t.Run("nil svmAdminPassword", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		testEnv := testSuite.NewTestActivityEnvironment()
		testEnv.RegisterActivity(act.GetSvmAdminPasswordSecretForOCI)

		hyperscaler.GetOCIService = func(ctx context.Context) (*oci.OciServices, error) {
			return &oci.OciServices{Ctx: context.Background(), Logger: util.GetLogger(context.Background())}, nil
		}

		var nilPw *commonparams.OciAdminPassword
		_, err := testEnv.ExecuteActivity(act.GetSvmAdminPasswordSecretForOCI, svm, nilPw)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "svmAdminPassword is required")
	})

	t.Run("empty Ocid in svmAdminPassword", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		testEnv := testSuite.NewTestActivityEnvironment()
		testEnv.RegisterActivity(act.GetSvmAdminPasswordSecretForOCI)

		hyperscaler.GetOCIService = func(ctx context.Context) (*oci.OciServices, error) {
			return &oci.OciServices{Ctx: context.Background(), Logger: util.GetLogger(context.Background())}, nil
		}

		adminPw := &commonparams.OciAdminPassword{Ocid: "", Version: 1}
		_, err := testEnv.ExecuteActivity(act.GetSvmAdminPasswordSecretForOCI, svm, adminPw)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "svmAdminPassword is required")
	})

	t.Run("success — admin password fetched from OCI Vault", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		testEnv := testSuite.NewTestActivityEnvironment()
		testEnv.RegisterActivity(act.GetSvmAdminPasswordSecretForOCI)

		origGetSecretVersion := oci.GetSecretVersion
		defer func() { oci.GetSecretVersion = origGetSecretVersion }()
		oci.GetSecretVersion = func(svc *oci.OciServices, secretID string, versionNumber ...int64) (*oci.OCICustomSecret, error) {
			return &oci.OCICustomSecret{
				Ocid:    secretID,
				Name:    "admin-password",
				Value:   "super-secret-pw",
				Version: 1,
			}, nil
		}

		mockSvc := newMockOCIServiceForTest(t, func(req *http.Request) (*http.Response, error) {
			return ociMockJSONResponse(200, `{
				"id": "ocid1.vaultsecret.oc1..testsvm",
				"secretName": "test-admin-password",
				"lifecycleState": "ACTIVE",
				"currentVersionNumber": 1
			}`), nil
		})
		hyperscaler.GetOCIService = func(ctx context.Context) (*oci.OciServices, error) {
			return mockSvc, nil
		}

		adminPw := &commonparams.OciAdminPassword{Ocid: "ocid1.vaultsecret.oc1..testsvm", Version: 1}
		encodedValue, err := testEnv.ExecuteActivity(act.GetSvmAdminPasswordSecretForOCI, svm, adminPw)
		assert.NoError(t, err)
		var creds *vlm.OntapCredentials
		err = encodedValue.Get(&creds)
		assert.NoError(t, err)
		assert.Equal(t, "super-secret-pw", creds.AdminPassword)
	})

	t.Run("vault GetSecret API error", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		testEnv := testSuite.NewTestActivityEnvironment()
		testEnv.RegisterActivity(act.GetSvmAdminPasswordSecretForOCI)

		mockSvc := newMockOCIServiceForTest(t, func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		})
		hyperscaler.GetOCIService = func(ctx context.Context) (*oci.OciServices, error) {
			return mockSvc, nil
		}

		adminPw := &commonparams.OciAdminPassword{Ocid: "ocid1.vaultsecret.oc1..testsvm", Version: 1}
		_, err := testEnv.ExecuteActivity(act.GetSvmAdminPasswordSecretForOCI, svm, adminPw)
		assert.Error(t, err)
	})

	t.Run("GetSecretVersion returns error", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		testEnv := testSuite.NewTestActivityEnvironment()
		testEnv.RegisterActivity(act.GetSvmAdminPasswordSecretForOCI)

		origGetSecretVersion := oci.GetSecretVersion
		defer func() { oci.GetSecretVersion = origGetSecretVersion }()
		oci.GetSecretVersion = func(svc *oci.OciServices, secretID string, versionNumber ...int64) (*oci.OCICustomSecret, error) {
			return nil, fmt.Errorf("vault connection timeout")
		}

		mockSvc := newMockOCIServiceForTest(t, func(req *http.Request) (*http.Response, error) {
			return ociMockJSONResponse(200, `{
				"id": "ocid1.vaultsecret.oc1..testsvm",
				"secretName": "test-admin-password",
				"lifecycleState": "ACTIVE",
				"currentVersionNumber": 1
			}`), nil
		})
		hyperscaler.GetOCIService = func(ctx context.Context) (*oci.OciServices, error) {
			return mockSvc, nil
		}

		adminPw := &commonparams.OciAdminPassword{Ocid: "ocid1.vaultsecret.oc1..testsvm", Version: 1}
		_, err := testEnv.ExecuteActivity(act.GetSvmAdminPasswordSecretForOCI, svm, adminPw)
		assert.Error(t, err)
	})

	t.Run("GetSecretVersion returns nil secret", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		testEnv := testSuite.NewTestActivityEnvironment()
		testEnv.RegisterActivity(act.GetSvmAdminPasswordSecretForOCI)

		origGetSecretVersion := oci.GetSecretVersion
		defer func() { oci.GetSecretVersion = origGetSecretVersion }()
		oci.GetSecretVersion = func(svc *oci.OciServices, secretID string, versionNumber ...int64) (*oci.OCICustomSecret, error) {
			return nil, nil
		}

		mockSvc := newMockOCIServiceForTest(t, func(req *http.Request) (*http.Response, error) {
			return ociMockJSONResponse(200, `{
				"id": "ocid1.vaultsecret.oc1..testsvm",
				"secretName": "test-admin-password",
				"lifecycleState": "ACTIVE",
				"currentVersionNumber": 1
			}`), nil
		})
		hyperscaler.GetOCIService = func(ctx context.Context) (*oci.OciServices, error) {
			return mockSvc, nil
		}

		adminPw := &commonparams.OciAdminPassword{Ocid: "ocid1.vaultsecret.oc1..testsvm", Version: 1}
		_, err := testEnv.ExecuteActivity(act.GetSvmAdminPasswordSecretForOCI, svm, adminPw)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "secret is inactive or pending deletion in OCI Vault")
	})
}

// ---------------------------------------------------------------------------
// SaveSVMAndLifDataWithOCID
// ---------------------------------------------------------------------------

func TestSaveSVMAndLifDataWithOCID(t *testing.T) {
	t.Run("persists svmOCID in SVM record", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(&activity)

		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, AccountID: 1}
		vlmConfig := &vlm.VLMConfig{
			Svm: map[string]vlm.SvmConfig{
				"svm-name": {
					Svmname: "svm-name",
					Svmuuid: "svm-uuid",
					SVMLIFs: map[vlm.VSALIFType][]vlm.LIFConfig{
						vlm.LIFTypeSan: {{IP: "10.0.0.1/24", Name: "san-lif", HomeNode: "node-a"}},
						vlm.LIFTypeNas: {{IP: "10.0.0.2/24", Name: "nas-lif", HomeNode: "node-b"}},
					},
				},
			},
		}

		var capturedSvm *datamodel.Svm
		mockStorage.On("CreateSVM", mock.Anything, mock.MatchedBy(func(s *datamodel.Svm) bool {
			capturedSvm = s
			return true
		})).Return(&datamodel.Svm{}, nil)
		mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{
			{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node-a"},
			{BaseModel: datamodel.BaseModel{ID: 2}, Name: "node-b"},
		}, nil)
		mockStorage.On("CreateLif", mock.Anything, mock.Anything).Return(&datamodel.Lif{}, nil)

		_, err := env.ExecuteActivity(activity.SaveSVMAndLifDataWithOCID, pool, vlmConfig, "svm-name", "ocid1.svm.oc1..abc")

		assert.NoError(tt, err)
		require.NotNil(tt, capturedSvm)
		assert.Equal(tt, "ocid1.svm.oc1..abc", capturedSvm.SvmExternalIdentifier)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("trims whitespace from svmOCID", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(&activity)

		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 2}, AccountID: 2}
		vlmConfig := &vlm.VLMConfig{
			Svm: map[string]vlm.SvmConfig{
				"svm-trim": {
					Svmname: "svm-trim",
					Svmuuid: "uuid-trim",
					SVMLIFs: map[vlm.VSALIFType][]vlm.LIFConfig{
						vlm.LIFTypeSan: {{IP: "10.0.1.1/24", Name: "lif-a", HomeNode: "n1"}},
						vlm.LIFTypeNas: {{IP: "10.0.1.2/24", Name: "lif-b", HomeNode: "n2"}},
					},
				},
			},
		}

		var capturedSvm *datamodel.Svm
		mockStorage.On("CreateSVM", mock.Anything, mock.MatchedBy(func(s *datamodel.Svm) bool {
			capturedSvm = s
			return true
		})).Return(&datamodel.Svm{}, nil)
		mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{
			{BaseModel: datamodel.BaseModel{ID: 10}, Name: "n1"},
			{BaseModel: datamodel.BaseModel{ID: 11}, Name: "n2"},
		}, nil)
		mockStorage.On("CreateLif", mock.Anything, mock.Anything).Return(&datamodel.Lif{}, nil)

		_, err := env.ExecuteActivity(activity.SaveSVMAndLifDataWithOCID, pool, vlmConfig, "svm-trim", "  ocid1.svm.oc1..padded  ")

		assert.NoError(tt, err)
		require.NotNil(tt, capturedSvm)
		assert.Equal(tt, "ocid1.svm.oc1..padded", capturedSvm.SvmExternalIdentifier)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("empty svmOCID stores empty string", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := activities.SvmActivity{SE: mockStorage}
		env.RegisterActivity(&activity)

		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 3}, AccountID: 3}
		vlmConfig := &vlm.VLMConfig{
			Svm: map[string]vlm.SvmConfig{
				"svm-empty": {
					Svmname: "svm-empty",
					Svmuuid: "uuid-empty",
					SVMLIFs: map[vlm.VSALIFType][]vlm.LIFConfig{
						vlm.LIFTypeSan: {{IP: "10.0.2.1/24", Name: "lif-x", HomeNode: "x1"}},
						vlm.LIFTypeNas: {{IP: "10.0.2.2/24", Name: "lif-y", HomeNode: "x2"}},
					},
				},
			},
		}

		var capturedSvm *datamodel.Svm
		mockStorage.On("CreateSVM", mock.Anything, mock.MatchedBy(func(s *datamodel.Svm) bool {
			capturedSvm = s
			return true
		})).Return(&datamodel.Svm{}, nil)
		mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{
			{BaseModel: datamodel.BaseModel{ID: 20}, Name: "x1"},
			{BaseModel: datamodel.BaseModel{ID: 21}, Name: "x2"},
		}, nil)
		mockStorage.On("CreateLif", mock.Anything, mock.Anything).Return(&datamodel.Lif{}, nil)

		_, err := env.ExecuteActivity(activity.SaveSVMAndLifDataWithOCID, pool, vlmConfig, "svm-empty", "")

		assert.NoError(tt, err)
		require.NotNil(tt, capturedSvm)
		assert.Equal(tt, "", capturedSvm.SvmExternalIdentifier)
		mockStorage.AssertExpectations(tt)
	})
}

// ---------------------------------------------------------------------------
// CreateSvmInCreatingState
// ---------------------------------------------------------------------------

func TestCreateSvmInCreatingState_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, AccountID: 11}
	expected := &datamodel.Svm{Name: "svm-1", PoolID: 7, AccountID: 11}

	var captured *datamodel.Svm
	mockStorage.On("CreateSvmInCreatingState", mock.Anything, mock.MatchedBy(func(s *datamodel.Svm) bool {
		captured = s
		return true
	})).Return(expected, nil)

	res, err := testEnv.ExecuteActivity(act.CreateSvmInCreatingState, pool, "svm-1", "  ocid1.svm.oc1..padded  ")

	require.NoError(t, err)
	var got *datamodel.Svm
	require.NoError(t, res.Get(&got))
	assert.Equal(t, expected.Name, got.Name)
	require.NotNil(t, captured)
	assert.Equal(t, "svm-1", captured.Name)
	assert.Equal(t, int64(7), captured.PoolID)
	assert.Equal(t, int64(11), captured.AccountID)
	assert.Equal(t, "ocid1.svm.oc1..padded", captured.SvmExternalIdentifier, "external identifier should be trimmed")
}

func TestCreateSvmInCreatingState_NilPool(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	_, err := testEnv.ExecuteActivity(act.CreateSvmInCreatingState, (*datamodel.Pool)(nil), "svm-1", "ocid1.svm.oc1..a")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pool must not be nil")
}

func TestCreateSvmInCreatingState_EmptyName(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, AccountID: 1}
	_, err := testEnv.ExecuteActivity(act.CreateSvmInCreatingState, pool, "", "ocid1.svm.oc1..a")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "svmName must not be empty")
}

func TestCreateSvmInCreatingState_DBError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, AccountID: 1}
	mockStorage.On("CreateSvmInCreatingState", mock.Anything, mock.Anything).Return((*datamodel.Svm)(nil), errors.New("db boom"))

	_, err := testEnv.ExecuteActivity(act.CreateSvmInCreatingState, pool, "svm-1", "ocid1.svm.oc1..a")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db boom")
}

// ---------------------------------------------------------------------------
// MarkSvmAsErroredForCreation
// ---------------------------------------------------------------------------

func TestMarkSvmAsErroredForCreation_PassesProvidedMessage(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	svm := &datamodel.Svm{Name: "svm-err"}
	mockStorage.On("ErroredSVM", mock.Anything, svm, "boom").Return(nil)

	_, err := testEnv.ExecuteActivity(act.MarkSvmAsErroredForCreation, svm, "boom")
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestMarkSvmAsErroredForCreation_DefaultsToCreationErrorDetailsWhenEmpty(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	svm := &datamodel.Svm{Name: "svm-err"}
	mockStorage.On("ErroredSVM", mock.Anything, svm, datamodel.LifeCycleStateCreationErrorDetails).Return(nil)

	_, err := testEnv.ExecuteActivity(act.MarkSvmAsErroredForCreation, svm, "")
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestMarkSvmAsErroredForCreation_PropagatesDBError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	svm := &datamodel.Svm{Name: "svm-err"}
	mockStorage.On("ErroredSVM", mock.Anything, svm, mock.Anything).Return(errors.New("db boom"))

	_, err := testEnv.ExecuteActivity(act.MarkSvmAsErroredForCreation, svm, "boom")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db boom")
}

// ---------------------------------------------------------------------------
// Nil-input validation for activity entry points
// ---------------------------------------------------------------------------

// Each activity must reject a nil required input with a non-retryable validation
// error so the Temporal worker fails fast instead of spinning on retries.

func TestRemoveQoSPolicyFromSVM_NilPool(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()
	act := &activities.SvmActivity{SE: database.NewMockStorage(t)}
	testEnv.RegisterActivity(act.RemoveQoSPolicyFromSVM)

	node := &coremodel.Node{Name: "node1"}
	_, err := testEnv.ExecuteActivity(act.RemoveQoSPolicyFromSVM, (*datamodel.Pool)(nil), node)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pool must not be nil")
}

func TestRemoveQoSPolicyFromSVM_NilNode(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()
	act := &activities.SvmActivity{SE: database.NewMockStorage(t)}
	testEnv.RegisterActivity(act.RemoveQoSPolicyFromSVM)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}
	_, err := testEnv.ExecuteActivity(act.RemoveQoSPolicyFromSVM, pool, (*coremodel.Node)(nil))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node must not be nil")
}

func TestCreateQoSPolicyAndApplyToSVM_NilNode(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()
	act := &activities.SvmActivity{}
	testEnv.RegisterActivity(act.CreateQoSPolicyAndApplyToSVM)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, Name: "p"}
	svm := &datamodel.Svm{
		Name:       "svm",
		SvmDetails: &datamodel.SvmDetails{ExternalUUID: "uuid"},
	}
	_, err := testEnv.ExecuteActivity(act.CreateQoSPolicyAndApplyToSVM, pool, svm, (*coremodel.Node)(nil))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node must not be nil")
}

func TestModifyQoSPolicyAndApplyToSVM_NilPool(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()
	act := &activities.SvmActivity{}
	testEnv.RegisterActivity(act.ModifyQoSPolicyAndApplyToSVM)

	node := &coremodel.Node{Name: "node1"}
	_, err := testEnv.ExecuteActivity(act.ModifyQoSPolicyAndApplyToSVM, (*datamodel.Pool)(nil), node, &commonparams.UpdatePoolParams{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pool must not be nil")
}

func TestModifyQoSPolicyAndApplyToSVM_NilNode(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()
	act := &activities.SvmActivity{}
	testEnv.RegisterActivity(act.ModifyQoSPolicyAndApplyToSVM)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, Name: "p"}
	_, err := testEnv.ExecuteActivity(act.ModifyQoSPolicyAndApplyToSVM, pool, (*coremodel.Node)(nil), &commonparams.UpdatePoolParams{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node must not be nil")
}

func TestAllocateSVMName_NilPool(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()
	act := &activities.SvmActivity{SE: database.NewMockStorage(t)}
	testEnv.RegisterActivity(act.AllocateSVMName)

	_, err := testEnv.ExecuteActivity(act.AllocateSVMName, (*datamodel.Pool)(nil))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pool must not be nil")
}

func TestMarkSvmDeleting_NilSvm(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()
	act := &activities.SvmActivity{SE: database.NewMockStorage(t)}
	testEnv.RegisterActivity(act.MarkSvmDeleting)

	_, err := testEnv.ExecuteActivity(act.MarkSvmDeleting, (*datamodel.Svm)(nil))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "svm must not be nil")
}

func TestSoftDeleteSvm_NilSvm(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()
	act := &activities.SvmActivity{SE: database.NewMockStorage(t)}
	testEnv.RegisterActivity(act.SoftDeleteSvm)

	_, err := testEnv.ExecuteActivity(act.SoftDeleteSvm, (*datamodel.Svm)(nil))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "svm must not be nil")
}

func TestMarkSvmAsErroredForDeletion_NilSvm(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()
	act := &activities.SvmActivity{SE: database.NewMockStorage(t)}
	testEnv.RegisterActivity(act.MarkSvmAsErroredForDeletion)

	_, err := testEnv.ExecuteActivity(act.MarkSvmAsErroredForDeletion, (*datamodel.Svm)(nil), "msg")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "svm must not be nil")
}

func TestMarkSvmAsErroredForCreation_NilSvm(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()
	act := &activities.SvmActivity{SE: database.NewMockStorage(t)}
	testEnv.RegisterActivity(act.MarkSvmAsErroredForCreation)

	_, err := testEnv.ExecuteActivity(act.MarkSvmAsErroredForCreation, (*datamodel.Svm)(nil), "msg")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "svm must not be nil")
}

// ---------------------------------------------------------------------------
// TransitionSvmState
// ---------------------------------------------------------------------------

func TestTransitionSvmState_ErrorState_PassesProvidedMessage(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	svm := &datamodel.Svm{Name: "svm-err"}
	mockStorage.On("UpdateSvmState", mock.Anything, svm, datamodel.LifeCycleStateError, "update boom").Return(nil)

	_, err := testEnv.ExecuteActivity(act.TransitionSvmState, svm, datamodel.LifeCycleStateError, "update boom")
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestTransitionSvmState_ErrorState_DefaultsWhenEmpty(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	svm := &datamodel.Svm{Name: "svm-err"}
	mockStorage.On("UpdateSvmState", mock.Anything, svm, datamodel.LifeCycleStateError, datamodel.LifeCycleStateUpdateErrorDetails).Return(nil)

	_, err := testEnv.ExecuteActivity(act.TransitionSvmState, svm, datamodel.LifeCycleStateError, "")
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestTransitionSvmState_ErrorState_PropagatesDBError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	svm := &datamodel.Svm{Name: "svm-err"}
	mockStorage.On("UpdateSvmState", mock.Anything, svm, datamodel.LifeCycleStateError, mock.Anything).Return(errors.New("db boom"))

	_, err := testEnv.ExecuteActivity(act.TransitionSvmState, svm, datamodel.LifeCycleStateError, "msg")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db boom")
}

func TestTransitionSvmState_ReadyState_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	svm := &datamodel.Svm{Name: "svm-ready"}
	mockStorage.On("UpdateSvmState", mock.Anything, svm, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(nil)

	_, err := testEnv.ExecuteActivity(act.TransitionSvmState, svm, datamodel.LifeCycleStateREADY, "")
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestTransitionSvmState_ReadyState_WithCustomDetails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	svm := &datamodel.Svm{Name: "svm-ready"}
	mockStorage.On("UpdateSvmState", mock.Anything, svm, datamodel.LifeCycleStateREADY, "custom ready details").Return(nil)

	_, err := testEnv.ExecuteActivity(act.TransitionSvmState, svm, datamodel.LifeCycleStateREADY, "custom ready details")
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestTransitionSvmState_ReadyState_PropagatesDBError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	svm := &datamodel.Svm{Name: "svm-ready"}
	mockStorage.On("UpdateSvmState", mock.Anything, svm, datamodel.LifeCycleStateREADY, mock.Anything).Return(errors.New("db boom"))

	_, err := testEnv.ExecuteActivity(act.TransitionSvmState, svm, datamodel.LifeCycleStateREADY, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db boom")
}

func TestTransitionSvmState_AnyState_PassesThrough(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(&act)

	svm := &datamodel.Svm{Name: "svm-deleting"}
	mockStorage.On("UpdateSvmState", mock.Anything, svm, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(nil)

	_, err := testEnv.ExecuteActivity(act.TransitionSvmState, svm, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestTransitionSvmState_NilSvm(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()
	act := &activities.SvmActivity{SE: database.NewMockStorage(t)}
	testEnv.RegisterActivity(act.TransitionSvmState)

	_, err := testEnv.ExecuteActivity(act.TransitionSvmState, (*datamodel.Svm)(nil), datamodel.LifeCycleStateREADY, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "svm must not be nil")
}

// ---------------------------------------------------------------------------
// GetNodeUUIDsByNameForPool
// ---------------------------------------------------------------------------

// Happy path: returns a name->UUID map covering every node fetched for the
// pool. The OCI create-SVM workflow uses this map to enrich its response with
// stable node UUIDs for the LIFs it just persisted.
func TestGetNodeUUIDsByNameForPool_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := &activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.GetNodeUUIDsByNameForPool)

	const poolID int64 = 42
	mockStorage.On("GetNodesByPoolID", mock.Anything, poolID).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1, UUID: "uuid-node-1"}, Name: "node1"},
		{BaseModel: datamodel.BaseModel{ID: 2, UUID: "uuid-node-2"}, Name: "node2"},
	}, nil)

	encoded, err := testEnv.ExecuteActivity(act.GetNodeUUIDsByNameForPool, poolID)
	assert.NoError(t, err)

	var got map[string]string
	require.NoError(t, encoded.Get(&got))
	assert.Equal(t, map[string]string{
		"node1": "uuid-node-1",
		"node2": "uuid-node-2",
	}, got)
	mockStorage.AssertExpectations(t)
}

// DB lookup fails: the activity must surface the wrapped error so the
// workflow can log it and continue with an empty enrichment map (the lookup
// is best-effort and must not abort SVM creation).
func TestGetNodeUUIDsByNameForPool_StorageError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := &activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.GetNodeUUIDsByNameForPool)

	const poolID int64 = 7
	mockStorage.On("GetNodesByPoolID", mock.Anything, poolID).
		Return(nil, errors.New("db unavailable"))

	_, err := testEnv.ExecuteActivity(act.GetNodeUUIDsByNameForPool, poolID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db unavailable")
	mockStorage.AssertExpectations(t)
}

// A node row with an empty name indicates broken cluster bootstrap state — we
// refuse to build a partial map because callers index LIFs by HomeNode and a
// silent skip would yield missing UUIDs in the API response.
func TestGetNodeUUIDsByNameForPool_EmptyNodeNameFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := &activities.SvmActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.GetNodeUUIDsByNameForPool)

	const poolID int64 = 99
	mockStorage.On("GetNodesByPoolID", mock.Anything, poolID).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1, UUID: "uuid-node-1"}, Name: "node1"},
		{BaseModel: datamodel.BaseModel{ID: 2, UUID: "uuid-node-2"}, Name: ""},
	}, nil)

	_, err := testEnv.ExecuteActivity(act.GetNodeUUIDsByNameForPool, poolID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node name is empty for node ID 2")
	mockStorage.AssertExpectations(t)
}
