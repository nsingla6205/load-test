package backgroundworkflows

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func expectRecalcVolumeChainBytesSync(mockStorage *database.MockStorage, volumeUUID string, sum int64, endpointUUID ...string) {
	recalcVol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volumeUUID}, DataProtection: &datamodel.DataProtection{}}
	mockStorage.On("GetVolume", mock.Anything, volumeUUID).Return(recalcVol, nil).Once()
	mockStorage.On("SumVolumeBackupChainBytes", mock.Anything, volumeUUID).Return(sum, nil).Once()
	mockStorage.On("UpdateVolumeFields", mock.Anything, volumeUUID, mock.MatchedBy(func(updates map[string]interface{}) bool {
		dp, ok := updates["data_protection"].(*datamodel.DataProtection)
		return ok && dp.BackupChainBytes != nil && *dp.BackupChainBytes == sum
	})).Return(nil).Once()
	if len(endpointUUID) > 0 && endpointUUID[0] != "" {
		mockStorage.On("UpdateBackupChainHistory", mock.Anything, volumeUUID, endpointUUID[0], sum).Return(nil).Once()
	}
}

func TestSyncLatestBackupLogicalSizeWorkflow(t *testing.T) {
	tests := []struct {
		name                 string
		featureFlagEnabled   bool
		enableVaultSwitching bool
		mockSetup            func(*testsuite.TestWorkflowEnvironment)
		expectedError        bool
		expectedWorkflowErr  string
	}{
		{
			name:               "Success - Feature flag disabled, workflow skips execution",
			featureFlagEnabled: false,
			mockSetup: func(env *testsuite.TestWorkflowEnvironment) {
				// No activities should be called when feature flag is disabled
			},
			expectedError: false,
		},
		{
			name:               "Success - Feature flag enabled, successful sync with volumes",
			featureFlagEnabled: true,
			mockSetup: func(env *testsuite.TestWorkflowEnvironment) {
				// Mock GetVolumeLatestBackupMapActivity
				volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
					1: {
						Volume: &datamodel.Volume{
							BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
							Name:      "test-volume-1",
						},
						LatestBackup: &datamodel.Backup{
							BaseModel: datamodel.BaseModel{UUID: "backup-uuid-1"},
							Name:      "test-backup-1",
							Attributes: &datamodel.BackupAttributes{
								ObjectStoreUUID: "object-store-uuid-1",
								EndpointUUID:    "endpoint-uuid-1",
							},
						},
					},
					2: {
						Volume: &datamodel.Volume{
							BaseModel: datamodel.BaseModel{ID: 2, UUID: "volume-uuid-2"},
							Name:      "test-volume-2",
						},
						LatestBackup: &datamodel.Backup{
							BaseModel: datamodel.BaseModel{UUID: "backup-uuid-2"},
							Name:      "test-backup-2",
							Attributes: &datamodel.BackupAttributes{
								ObjectStoreUUID: "object-store-uuid-2",
								EndpointUUID:    "endpoint-uuid-2",
							},
						},
					},
				}
				env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(volumeBackupMap, nil)

				// Mock GetObjectStoreEndpointInfoActivity for each volume
				objStoreEndpoint1 := &vsa.SmObjectStoreEndpointt{
					LogicalSize: nillable.ToPointer(int64(1024 * 1024 * 1024)), // 1GB
				}
				objStoreEndpoint2 := &vsa.SmObjectStoreEndpointt{
					LogicalSize: nillable.ToPointer(int64(2048 * 1024 * 1024)), // 2GB
				}
				env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[1]).Return(objStoreEndpoint1, nil)
				env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[2]).Return(objStoreEndpoint2, nil)

				// Mock UpdateBackupAndVolumeActivity for each volume
				env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[1], int64(1024*1024*1024)).Return(nil)
				env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[2], int64(2048*1024*1024)).Return(nil)
			},
			expectedError: false,
		},
		{
			name:               "Success - Feature flag enabled, no volumes to sync",
			featureFlagEnabled: true,
			mockSetup: func(env *testsuite.TestWorkflowEnvironment) {
				// Mock GetVolumeLatestBackupMapActivity returning empty map
				env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(map[int64]*datamodel.VolumeLatestBackup{}, nil)
			},
			expectedError: false,
		},
		{
			name:                 "Success - Backup vault switching enabled (summed sync skipped; TODO handle detached vaults later)",
			featureFlagEnabled:   true,
			enableVaultSwitching: true,
			mockSetup: func(env *testsuite.TestWorkflowEnvironment) {
				volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
					1: {
						Volume: &datamodel.Volume{
							BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
							Name:      "test-volume-1",
						},
						LatestBackup: &datamodel.Backup{
							BaseModel: datamodel.BaseModel{UUID: "backup-uuid-1"},
							Name:      "test-backup-1",
							Attributes: &datamodel.BackupAttributes{
								ObjectStoreUUID: "obj-uuid-1",
								EndpointUUID:    "ep-uuid-1",
							},
						},
					},
				}
				env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(volumeBackupMap, nil)
			},
			expectedError: false,
		},
		{
			name:               "Success - Feature flag enabled, volume with nil object store endpoint",
			featureFlagEnabled: true,
			mockSetup: func(env *testsuite.TestWorkflowEnvironment) {
				volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
					1: {
						Volume: &datamodel.Volume{
							BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
							Name:      "test-volume-1",
						},
						LatestBackup: &datamodel.Backup{
							BaseModel: datamodel.BaseModel{UUID: "backup-uuid-1"},
							Name:      "test-backup-1",
							Attributes: &datamodel.BackupAttributes{
								ObjectStoreUUID: "object-store-uuid-1",
								EndpointUUID:    "endpoint-uuid-1",
							},
						},
					},
				}
				env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(volumeBackupMap, nil)

				// Mock GetObjectStoreEndpointInfoActivity returning nil (skip case)
				env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[1]).Return(nil, nil)
			},
			expectedError: false,
		},
		{
			name:               "Success - Feature flag enabled, volume with nil logical size",
			featureFlagEnabled: true,
			mockSetup: func(env *testsuite.TestWorkflowEnvironment) {
				volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
					1: {
						Volume: &datamodel.Volume{
							BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
							Name:      "test-volume-1",
						},
						LatestBackup: &datamodel.Backup{
							BaseModel: datamodel.BaseModel{UUID: "backup-uuid-1"},
							Name:      "test-backup-1",
							Attributes: &datamodel.BackupAttributes{
								ObjectStoreUUID: "object-store-uuid-1",
								EndpointUUID:    "endpoint-uuid-1",
							},
						},
					},
				}
				env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(volumeBackupMap, nil)

				// Mock GetObjectStoreEndpointInfoActivity returning endpoint with nil logical size
				objStoreEndpoint := &vsa.SmObjectStoreEndpointt{
					LogicalSize: nil, // Nil logical size
				}
				env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[1]).Return(objStoreEndpoint, nil)
			},
			expectedError: false,
		},
		{
			name:               "Error - GetVolumeLatestBackupMapActivity fails",
			featureFlagEnabled: true,
			mockSetup: func(env *testsuite.TestWorkflowEnvironment) {
				env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(nil, errors.New("database connection error"))
			},
			expectedError:       true,
			expectedWorkflowErr: "An internal error occurred",
		},
		{
			name:               "Partial Success - GetObjectStoreEndpointInfoActivity fails for one volume",
			featureFlagEnabled: true,
			mockSetup: func(env *testsuite.TestWorkflowEnvironment) {
				volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
					1: {
						Volume: &datamodel.Volume{
							BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
							Name:      "test-volume-1",
						},
						LatestBackup: &datamodel.Backup{
							BaseModel: datamodel.BaseModel{UUID: "backup-uuid-1"},
							Name:      "test-backup-1",
							Attributes: &datamodel.BackupAttributes{
								ObjectStoreUUID: "object-store-uuid-1",
								EndpointUUID:    "endpoint-uuid-1",
							},
						},
					},
					2: {
						Volume: &datamodel.Volume{
							BaseModel: datamodel.BaseModel{ID: 2, UUID: "volume-uuid-2"},
							Name:      "test-volume-2",
						},
						LatestBackup: &datamodel.Backup{
							BaseModel: datamodel.BaseModel{UUID: "backup-uuid-2"},
							Name:      "test-backup-2",
							Attributes: &datamodel.BackupAttributes{
								ObjectStoreUUID: "object-store-uuid-2",
								EndpointUUID:    "endpoint-uuid-2",
							},
						},
					},
				}
				env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(volumeBackupMap, nil)

				// First volume fails
				env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[1]).Return(nil, errors.New("failed to get object store info"))

				// Second volume succeeds
				objStoreEndpoint2 := &vsa.SmObjectStoreEndpointt{
					LogicalSize: nillable.ToPointer(int64(2048 * 1024 * 1024)), // 2GB
				}
				env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[2]).Return(objStoreEndpoint2, nil)
				env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[2], int64(2048*1024*1024)).Return(nil)
			},
			expectedError: false, // Workflow continues despite individual volume failures
		},
		{
			name:               "Partial Success - UpdateBackupAndVolumeActivity fails for one volume",
			featureFlagEnabled: true,
			mockSetup: func(env *testsuite.TestWorkflowEnvironment) {
				volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
					1: {
						Volume: &datamodel.Volume{
							BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
							Name:      "test-volume-1",
						},
						LatestBackup: &datamodel.Backup{
							BaseModel: datamodel.BaseModel{UUID: "backup-uuid-1"},
							Name:      "test-backup-1",
							Attributes: &datamodel.BackupAttributes{
								ObjectStoreUUID: "object-store-uuid-1",
								EndpointUUID:    "endpoint-uuid-1",
							},
						},
					},
					2: {
						Volume: &datamodel.Volume{
							BaseModel: datamodel.BaseModel{ID: 2, UUID: "volume-uuid-2"},
							Name:      "test-volume-2",
						},
						LatestBackup: &datamodel.Backup{
							BaseModel: datamodel.BaseModel{UUID: "backup-uuid-2"},
							Name:      "test-backup-2",
							Attributes: &datamodel.BackupAttributes{
								ObjectStoreUUID: "object-store-uuid-2",
								EndpointUUID:    "endpoint-uuid-2",
							},
						},
					},
				}
				env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(volumeBackupMap, nil)

				// Both volumes get endpoint info successfully
				objStoreEndpoint1 := &vsa.SmObjectStoreEndpointt{
					LogicalSize: nillable.ToPointer(int64(1024 * 1024 * 1024)),
				}
				objStoreEndpoint2 := &vsa.SmObjectStoreEndpointt{
					LogicalSize: nillable.ToPointer(int64(2048 * 1024 * 1024)),
				}
				env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[1]).Return(objStoreEndpoint1, nil)
				env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[2]).Return(objStoreEndpoint2, nil)

				// First volume update fails
				env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[1], int64(1024*1024*1024)).Return(errors.New("database update failed"))

				// Second volume update succeeds
				env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[2], int64(2048*1024*1024)).Return(nil)
			},
			expectedError: false, // Workflow continues despite individual volume failures
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Override the getter function for testing
			originalGetter := getBackupLogicalSizeSyncEnabled
			getBackupLogicalSizeSyncEnabled = func() bool {
				return tt.featureFlagEnabled
			}
			defer func() {
				getBackupLogicalSizeSyncEnabled = originalGetter
			}()

			if tt.enableVaultSwitching {
				orig := utils.EnableBackupVaultSwitching
				defer utils.SetEnableBackupVaultSwitchingForTest(orig)
				utils.SetEnableBackupVaultSwitchingForTest(true)
			}

			// Setup test workflow environment
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestWorkflowEnvironment()
			env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

			// Setup header for logging
			encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
			mockHeader := &commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"logParam": encodedValue,
				},
			}
			env.SetHeader(mockHeader)

			// Register workflow and activities
			env.RegisterWorkflow(SyncLatestBackupLogicalSizeWorkflow)
			env.RegisterActivity(&backgroundactivities.VolumeBackupSyncActivity{})

			// Setup mocks
			tt.mockSetup(env)

			// Execute workflow
			env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

			// Verify results
			if tt.expectedError {
				assert.True(t, env.IsWorkflowCompleted())
				assert.Error(t, env.GetWorkflowError())
				if tt.expectedWorkflowErr != "" {
					assert.Contains(t, env.GetWorkflowError().Error(), tt.expectedWorkflowErr)
				}
			} else {
				assert.True(t, env.IsWorkflowCompleted())
				assert.NoError(t, env.GetWorkflowError())
			}
		})
	}
}

func TestSyncLatestBackupLogicalSizeToVolumeAndBackupWF_Setup(t *testing.T) {
	tests := []struct {
		name          string
		expectedError bool
	}{
		{
			name:          "Success - Setup completes successfully",
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test workflow environment
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestWorkflowEnvironment()
			env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

			// Setup header for logging
			encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
			mockHeader := &commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"logParam": encodedValue,
				},
			}
			env.SetHeader(mockHeader)

			// Create workflow instance
			wf := &SyncLatestBackupLogicalSizeToVolumeAndBackupWF{}

			// Test setup within a workflow context
			env.RegisterWorkflow(func(ctx workflow.Context) error {
				err := wf.Setup(ctx, nil)
				return err
			})

			env.ExecuteWorkflow(func(ctx workflow.Context) error {
				err := wf.Setup(ctx, nil)
				return err
			})

			// Verify results
			if tt.expectedError {
				assert.True(t, env.IsWorkflowCompleted())
				assert.Error(t, env.GetWorkflowError())
			} else {
				assert.True(t, env.IsWorkflowCompleted())
				assert.NoError(t, env.GetWorkflowError())

				// Verify workflow fields are set
				assert.NotEmpty(t, wf.ID)
				assert.Equal(t, "system", wf.CustomerID)
				assert.Equal(t, workflows.WorkflowStatusCreated, wf.Status)
				assert.NotNil(t, wf.Logger)
			}
		})
	}
}

func TestSyncLatestBackupLogicalSizeToVolumeAndBackupWF_Run(t *testing.T) {
	tests := []struct {
		name          string
		mockSetup     func(*testsuite.TestWorkflowEnvironment)
		expectedError bool
		errorContains string
	}{
		{
			name: "Success - Run completes successfully with volumes",
			mockSetup: func(env *testsuite.TestWorkflowEnvironment) {
				volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
					1: {
						Volume: &datamodel.Volume{
							BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
							Name:      "test-volume-1",
						},
						LatestBackup: &datamodel.Backup{
							BaseModel: datamodel.BaseModel{UUID: "backup-uuid-1"},
							Name:      "test-backup-1",
							Attributes: &datamodel.BackupAttributes{
								ObjectStoreUUID: "object-store-uuid-1",
								EndpointUUID:    "endpoint-uuid-1",
							},
						},
					},
				}
				env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(volumeBackupMap, nil)

				objStoreEndpoint := &vsa.SmObjectStoreEndpointt{
					LogicalSize: nillable.ToPointer(int64(1024 * 1024 * 1024)),
				}
				env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[1]).Return(objStoreEndpoint, nil)
				env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[1], int64(1024*1024*1024)).Return(nil)
			},
			expectedError: false,
		},
		{
			name: "Success - Run completes successfully with no volumes",
			mockSetup: func(env *testsuite.TestWorkflowEnvironment) {
				env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(map[int64]*datamodel.VolumeLatestBackup{}, nil)
			},
			expectedError: false,
		},
		{
			name: "Error - GetVolumeLatestBackupMapActivity fails",
			mockSetup: func(env *testsuite.TestWorkflowEnvironment) {
				env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(nil, errors.New("database error"))
			},
			expectedError: true,
			errorContains: "An internal error occurred",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test workflow environment
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestWorkflowEnvironment()
			env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

			// Setup header for logging
			encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
			mockHeader := &commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"logParam": encodedValue,
				},
			}
			env.SetHeader(mockHeader)

			// Register activities
			env.RegisterActivity(&backgroundactivities.VolumeBackupSyncActivity{})

			// Setup mocks
			tt.mockSetup(env)

			// Create and initialize workflow instance
			wf := &SyncLatestBackupLogicalSizeToVolumeAndBackupWF{}
			mockLogger := &log.MockLogger{}
			// Set up mock expectations for all logger methods that might be called
			mockLogger.On("Infof", mock.AnythingOfType("string"), mock.Anything).Return()
			mockLogger.On("Errorf", mock.AnythingOfType("string"), mock.Anything, mock.Anything, mock.Anything).Return()
			mockLogger.On("Error", mock.AnythingOfType("string"), mock.Anything, mock.Anything).Return()
			wf.Logger = mockLogger

			// Test Run within a workflow context
			env.RegisterWorkflow(func(ctx workflow.Context) error {
				_, err := wf.Run(ctx)
				if err != nil {
					return err
				}
				return nil
			})

			env.ExecuteWorkflow(func(ctx workflow.Context) error {
				_, err := wf.Run(ctx)
				if err != nil {
					return err
				}
				return nil
			})

			// Verify results
			if tt.expectedError {
				assert.True(t, env.IsWorkflowCompleted())
				assert.Error(t, env.GetWorkflowError())
				if tt.errorContains != "" {
					assert.Contains(t, env.GetWorkflowError().Error(), tt.errorContains)
				}
			} else {
				assert.True(t, env.IsWorkflowCompleted())
				assert.NoError(t, env.GetWorkflowError())
			}
		})
	}
}

// BackupLogicalSizeSyncRealDatabaseTestSuite tests with real activity implementations to verify database synchronization
type BackupLogicalSizeSyncRealDatabaseTestSuite struct {
	testsuite.WorkflowTestSuite
	suite.Suite
	env            *testsuite.TestWorkflowEnvironment
	mockStorage    *database.MockStorage
	mockProvider   *vsa.MockProvider
	originalGetter func() bool
	activity       *backgroundactivities.VolumeBackupSyncActivity
}

func TestBackupLogicalSizeSyncRealDatabaseTestSuite(t *testing.T) {
	suite.Run(t, new(BackupLogicalSizeSyncRealDatabaseTestSuite))
}

func (s *BackupLogicalSizeSyncRealDatabaseTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	// Setup header for logging
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	s.env.SetHeader(mockHeader)

	// Setup mocks
	s.mockStorage = database.NewMockStorage(s.T())
	s.mockProvider = &vsa.MockProvider{}

	// Create real activity with mocked storage
	s.activity = &backgroundactivities.VolumeBackupSyncActivity{
		SE: s.mockStorage,
	}

	// Register workflow and real activities
	s.env.RegisterWorkflow(SyncLatestBackupLogicalSizeWorkflow)
	s.env.RegisterActivity(s.activity)

	// Store original getter function
	s.originalGetter = getBackupLogicalSizeSyncEnabled
}

func (s *BackupLogicalSizeSyncRealDatabaseTestSuite) TearDownTest() {
	// Restore original getter function
	getBackupLogicalSizeSyncEnabled = s.originalGetter
}

// TestRealDatabaseSync_SingleVolumeSuccess tests a complete successful sync with real database calls
func (s *BackupLogicalSizeSyncRealDatabaseTestSuite) TestRealDatabaseSync_SingleVolumeSuccess() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Setup test data
	updatedLogicalSize := int64(1024 * 1024 * 1024) // 1GB

	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
				Name:      "test-volume",
				State:     datamodel.LifeCycleStateREADY,
				PoolID:    1,
				Pool: &datamodel.Pool{
					BaseModel:      datamodel.BaseModel{ID: 1},
					DeploymentName: "test-deployment",
					PoolCredentials: &datamodel.PoolCredentials{
						Password:      "test-password",
						SecretID:      "test-secret",
						CertificateID: "test-cert",
						AuthType:      1,
					},
				},
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(100 * 1024 * 1024)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "test-backup-uuid"},
				VolumeUUID: "test-volume-uuid",
				Name:       "test-backup",
				State:      datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "test-object-store-uuid",
					EndpointUUID:    "test-endpoint-uuid",
				},
			},
		},
	}

	// Mock the database calls that will be made by the real activities

	// 1. GetVolumeLatestBackupMapActivity will call GetVolumeLatestBackupMap
	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(volumeBackupMap, nil).Once()

	// 2. GetObjectStoreEndpointInfoActivity will call GetNodesByPoolID
	mockNodes := []*datamodel.Node{
		{
			BaseModel:       datamodel.BaseModel{ID: 1},
			EndpointAddress: "192.168.1.1",
		},
	}
	s.mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).Return(mockNodes, nil).Once()

	// 3. Setup hyperscaler provider mock
	objStoreEndpoint := &vsa.SmObjectStoreEndpointt{
		LogicalSize: &updatedLogicalSize,
	}

	// Mock the hyperscaler provider functions
	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return s.mockProvider, nil
	}
	defer func() {
		vsa.GetProviderByNode = originalGetProvider
	}()

	s.mockProvider.On("ObjectStoreEndpointInfoGet", "test-object-store-uuid", "test-endpoint-uuid").Return(objStoreEndpoint, nil).Once()

	// 4. UpdateBackupAndVolumeActivity will call UpdateBackupFields and UpdateVolumeFields
	s.mockStorage.On("UpdateBackupFields", mock.Anything, "test-backup-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
		// Verify the backup update contains the correct logical size
		logicalSize, exists := updates["latest_logical_backup_size"]
		return exists && logicalSize == updatedLogicalSize
	})).Return(nil).Once()

	expectRecalcVolumeChainBytesSync(s.mockStorage, "test-volume-uuid", updatedLogicalSize, "test-endpoint-uuid")

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completion
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify all database operations were called exactly as expected
	s.mockStorage.AssertExpectations(s.T())
	s.mockProvider.AssertExpectations(s.T())

	// Verify specific database operations occurred
	s.mockStorage.AssertCalled(s.T(), "GetVolumeLatestBackupMap", mock.Anything)
	s.mockStorage.AssertCalled(s.T(), "GetNodesByPoolID", mock.Anything, int64(1))
	s.mockStorage.AssertCalled(s.T(), "UpdateBackupFields", mock.Anything, "test-backup-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
		logicalSize, exists := updates["latest_logical_backup_size"]
		return exists && logicalSize == updatedLogicalSize
	}))
	s.mockStorage.AssertCalled(s.T(), "UpdateVolumeFields", mock.Anything, "test-volume-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
		dataProtection, exists := updates["data_protection"]
		if !exists {
			return false
		}
		dp, ok := dataProtection.(*datamodel.DataProtection)
		return ok && dp.BackupChainBytes != nil && *dp.BackupChainBytes == updatedLogicalSize
	}))
}

// TestRealDatabaseSync_MultipleVolumesWithFailures tests multiple volumes with some database failures
func (s *BackupLogicalSizeSyncRealDatabaseTestSuite) TestRealDatabaseSync_MultipleVolumesWithFailures() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Setup test data with 3 volumes
	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		// Volume 1: Will succeed completely
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-success-uuid"},
				Name:      "volume-success",
				State:     datamodel.LifeCycleStateREADY,
				PoolID:    1,
				Pool: &datamodel.Pool{
					BaseModel:      datamodel.BaseModel{ID: 1},
					DeploymentName: "test-deployment-1",
					PoolCredentials: &datamodel.PoolCredentials{
						Password:      "test-password-1",
						SecretID:      "test-secret-1",
						CertificateID: "test-cert-1",
						AuthType:      1,
					},
				},
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(100 * 1024 * 1024)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "backup-success-uuid"},
				VolumeUUID: "volume-success-uuid",
				Name:       "backup-success",
				State:      datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-success-uuid",
					EndpointUUID:    "endpoint-success-uuid",
				},
			},
		},
		// Volume 2: Will fail at backup update
		2: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 2, UUID: "volume-backup-fail-uuid"},
				Name:      "volume-backup-fail",
				State:     datamodel.LifeCycleStateREADY,
				PoolID:    2,
				Pool: &datamodel.Pool{
					BaseModel:      datamodel.BaseModel{ID: 2},
					DeploymentName: "test-deployment-2",
					PoolCredentials: &datamodel.PoolCredentials{
						Password:      "test-password-2",
						SecretID:      "test-secret-2",
						CertificateID: "test-cert-2",
						AuthType:      1,
					},
				},
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(200 * 1024 * 1024)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-fail-uuid"},
				Name:      "backup-fail",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-fail-uuid",
					EndpointUUID:    "endpoint-fail-uuid",
				},
			},
		},
		// Volume 3: Will fail at volume update
		3: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 3, UUID: "volume-vol-fail-uuid"},
				Name:      "volume-vol-fail",
				State:     datamodel.LifeCycleStateREADY,
				PoolID:    3,
				Pool: &datamodel.Pool{
					BaseModel:      datamodel.BaseModel{ID: 3},
					DeploymentName: "test-deployment-3",
					PoolCredentials: &datamodel.PoolCredentials{
						Password:      "test-password-3",
						SecretID:      "test-secret-3",
						CertificateID: "test-cert-3",
						AuthType:      1,
					},
				},
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(300 * 1024 * 1024)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-vol-fail-uuid"},
				Name:      "backup-vol-fail",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-vol-fail-uuid",
					EndpointUUID:    "endpoint-vol-fail-uuid",
				},
			},
		},
	}

	// Mock database calls

	// 1. GetVolumeLatestBackupMapActivity
	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(volumeBackupMap, nil).Once()

	// 2. GetNodesByPoolID for each volume
	mockNodes1 := []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}, EndpointAddress: "192.168.1.1"}}
	mockNodes2 := []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 2}, EndpointAddress: "192.168.1.2"}}
	mockNodes3 := []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 3}, EndpointAddress: "192.168.1.3"}}

	s.mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).Return(mockNodes1, nil).Once()
	s.mockStorage.On("GetNodesByPoolID", mock.Anything, int64(2)).Return(mockNodes2, nil).Once()
	s.mockStorage.On("GetNodesByPoolID", mock.Anything, int64(3)).Return(mockNodes3, nil).Once()

	// 3. Setup hyperscaler provider mock
	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return s.mockProvider, nil
	}
	defer func() {
		vsa.GetProviderByNode = originalGetProvider
	}()

	// Mock object store endpoint responses
	successLogicalSize := int64(1024 * 1024 * 1024) // 1GB
	failLogicalSize := int64(2048 * 1024 * 1024)    // 2GB
	volFailLogicalSize := int64(3072 * 1024 * 1024) // 3GB

	s.mockProvider.On("ObjectStoreEndpointInfoGet", "object-store-success-uuid", "endpoint-success-uuid").Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &successLogicalSize}, nil).Once()
	s.mockProvider.On("ObjectStoreEndpointInfoGet", "object-store-fail-uuid", "endpoint-fail-uuid").Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &failLogicalSize}, nil).Once()
	s.mockProvider.On("ObjectStoreEndpointInfoGet", "object-store-vol-fail-uuid", "endpoint-vol-fail-uuid").Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &volFailLogicalSize}, nil).Once()

	// 4. Database update calls with specific failure scenarios

	// Volume 1: Both backup and volume updates succeed
	s.mockStorage.On("UpdateBackupFields", mock.Anything, "backup-success-uuid", mock.Anything).Return(nil).Once()
	expectRecalcVolumeChainBytesSync(s.mockStorage, "volume-success-uuid", successLogicalSize, "endpoint-success-uuid")

	// Volume 2: Backup update fails
	s.mockStorage.On("UpdateBackupFields", mock.Anything, "backup-fail-uuid", mock.Anything).Return(errors.New("backup update failed")).Times(3) // Retry policy causes 3 attempts
	// Volume update should not be called for volume 2 since backup update fails first

	// Volume 3: Backup update succeeds but recalc volume update fails
	s.mockStorage.On("UpdateBackupFields", mock.Anything, "backup-vol-fail-uuid", mock.Anything).Return(nil).Times(3)
	recalcVolFail := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "volume-vol-fail-uuid"}, DataProtection: &datamodel.DataProtection{}}
	s.mockStorage.On("GetVolume", mock.Anything, "volume-vol-fail-uuid").Return(recalcVolFail, nil).Times(3)
	s.mockStorage.On("SumVolumeBackupChainBytes", mock.Anything, "volume-vol-fail-uuid").Return(volFailLogicalSize, nil).Times(3)
	s.mockStorage.On("UpdateVolumeFields", mock.Anything, "volume-vol-fail-uuid", mock.Anything).Return(errors.New("volume update failed")).Times(3)

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completion (should complete despite individual failures)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify all expected database operations were called
	s.mockStorage.AssertExpectations(s.T())
	s.mockProvider.AssertExpectations(s.T())

	// Verify specific call counts:
	// - 1 call to GetVolumeLatestBackupMap
	// - 3 calls to GetNodesByPoolID (one per volume)
	// - 7 calls to UpdateBackupFields (1 for volume 1, 3 retries for volume 2, 3 retries for volume 3)
	// - 4 calls to UpdateVolumeFields (1 for volume 1, 3 retries for volume 3)
	s.mockStorage.AssertNumberOfCalls(s.T(), "GetVolumeLatestBackupMap", 1)
	s.mockStorage.AssertNumberOfCalls(s.T(), "GetNodesByPoolID", 3)
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateBackupFields", 7)
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateVolumeFields", 4)
}

// TestRealDatabaseSync_GetVolumeLatestBackupMapFailure tests behavior when initial database query fails
func (s *BackupLogicalSizeSyncRealDatabaseTestSuite) TestRealDatabaseSync_GetVolumeLatestBackupMapFailure() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Mock the initial database query to fail - need to account for retries
	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(
		nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, errors.New("failed to query volumes"))).Times(3) // Retry policy causes 3 attempts

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow fails when initial database query fails
	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())

	// Verify the database call was made 3 times due to retry policy
	s.mockStorage.AssertExpectations(s.T())
	s.mockStorage.AssertNumberOfCalls(s.T(), "GetVolumeLatestBackupMap", 3)
	s.mockStorage.AssertNumberOfCalls(s.T(), "GetNodesByPoolID", 0)
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateBackupFields", 0)
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateVolumeFields", 0)
}

// TestRealDatabaseSync_NoVolumes tests when no volumes are returned from database
func (s *BackupLogicalSizeSyncRealDatabaseTestSuite) TestRealDatabaseSync_NoVolumes() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Mock empty result from database
	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(
		map[int64]*datamodel.VolumeLatestBackup{}, nil).Once()

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completes successfully with no volumes to process
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify only the initial database call was made
	s.mockStorage.AssertExpectations(s.T())
	s.mockStorage.AssertNumberOfCalls(s.T(), "GetVolumeLatestBackupMap", 1)
	s.mockStorage.AssertNumberOfCalls(s.T(), "GetNodesByPoolID", 0)
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateBackupFields", 0)
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateVolumeFields", 0)
}

// TestRealDatabaseSync_BackupChainHistoryFailure tests when UpdateBackupChainHistory fails
// This should not fail the entire operation - it logs a warning and continues
func (s *BackupLogicalSizeSyncRealDatabaseTestSuite) TestRealDatabaseSync_BackupChainHistoryFailure() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Setup test data
	updatedLogicalSize := int64(1024 * 1024 * 1024) // 1GB

	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
				Name:      "test-volume",
				State:     datamodel.LifeCycleStateREADY,
				PoolID:    1,
				Pool: &datamodel.Pool{
					BaseModel:      datamodel.BaseModel{ID: 1},
					DeploymentName: "test-deployment",
					PoolCredentials: &datamodel.PoolCredentials{
						Password:      "test-password",
						SecretID:      "test-secret",
						CertificateID: "test-cert",
						AuthType:      1,
					},
				},
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(100 * 1024 * 1024)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "test-backup-uuid"},
				VolumeUUID: "test-volume-uuid",
				Name:       "test-backup",
				State:      datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "test-object-store-uuid",
					EndpointUUID:    "test-endpoint-uuid",
				},
			},
		},
	}

	// Mock database calls
	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(volumeBackupMap, nil).Once()

	mockNodes := []*datamodel.Node{
		{
			BaseModel:       datamodel.BaseModel{ID: 1},
			EndpointAddress: "192.168.1.1",
		},
	}
	s.mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).Return(mockNodes, nil).Once()

	// Setup hyperscaler provider mock
	objStoreEndpoint := &vsa.SmObjectStoreEndpointt{
		LogicalSize: &updatedLogicalSize,
	}

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return s.mockProvider, nil
	}
	defer func() {
		vsa.GetProviderByNode = originalGetProvider
	}()

	s.mockProvider.On("ObjectStoreEndpointInfoGet", "test-object-store-uuid", "test-endpoint-uuid").Return(objStoreEndpoint, nil).Once()

	s.mockStorage.On("UpdateBackupFields", mock.Anything, "test-backup-uuid", mock.Anything).Return(nil).Once()
	recalcVol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"}, DataProtection: &datamodel.DataProtection{}}
	s.mockStorage.On("GetVolume", mock.Anything, "test-volume-uuid").Return(recalcVol, nil).Once()
	s.mockStorage.On("SumVolumeBackupChainBytes", mock.Anything, "test-volume-uuid").Return(updatedLogicalSize, nil).Once()
	s.mockStorage.On("UpdateVolumeFields", mock.Anything, "test-volume-uuid", mock.Anything).Return(nil).Once()
	historyErr := errors.New("backup chain history update failed")
	s.mockStorage.On("UpdateBackupChainHistory", mock.Anything, "test-volume-uuid", "test-endpoint-uuid", updatedLogicalSize).Return(historyErr).Once()

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow still completes successfully despite UpdateBackupChainHistory failure
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify all expected calls were made
	s.mockStorage.AssertExpectations(s.T())
	s.mockProvider.AssertExpectations(s.T())
	s.mockStorage.AssertCalled(s.T(), "UpdateBackupChainHistory", mock.Anything, "test-volume-uuid", "test-endpoint-uuid", updatedLogicalSize)
}

// BackupLogicalSizeSyncIntegrationTestSuite provides comprehensive integration testing
// for the backup logical size sync feature across various real-world scenarios
type BackupLogicalSizeSyncIntegrationTestSuite struct {
	testsuite.WorkflowTestSuite
	suite.Suite
	env            *testsuite.TestWorkflowEnvironment
	mockStorage    *database.MockStorage
	mockProvider   *vsa.MockProvider
	originalGetter func() bool
}

func TestBackupLogicalSizeSyncIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(BackupLogicalSizeSyncIntegrationTestSuite))
}

func (s *BackupLogicalSizeSyncIntegrationTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	// Setup header for logging
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	s.env.SetHeader(mockHeader)

	// Register workflow and activities
	s.env.RegisterWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Setup mocks
	s.mockStorage = database.NewMockStorage(s.T())
	s.mockProvider = &vsa.MockProvider{}

	// Register activity with mocked dependencies for integration testing
	activity := &backgroundactivities.VolumeBackupSyncActivity{
		SE: s.mockStorage,
	}
	s.env.RegisterActivity(activity)

	// Store original getter function
	s.originalGetter = getBackupLogicalSizeSyncEnabled
}

func (s *BackupLogicalSizeSyncIntegrationTestSuite) TearDownTest() {
	// Restore original getter function
	getBackupLogicalSizeSyncEnabled = s.originalGetter
}

// Test Case 1: Feature flag enabled - workflow should execute with database verification
func (s *BackupLogicalSizeSyncIntegrationTestSuite) TestScenario1_FeatureFlagEnabled_WorkflowExecutes() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Setup: No volumes to sync for simplicity
	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(
		map[int64]*datamodel.VolumeLatestBackup{}, nil)

	// Execute workflow (no activity mocks needed for integration test)
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow execution
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify database operations: GetVolumeLatestBackupMap should be called once
	s.mockStorage.AssertNumberOfCalls(s.T(), "GetVolumeLatestBackupMap", 1)

	// Verify no update operations occur when no volumes are present
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateBackupFields", 0)
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateVolumeFields", 0)
}

// Test Case 2: Feature flag disabled - workflow should skip execution
func (s *BackupLogicalSizeSyncIntegrationTestSuite) TestScenario2_FeatureFlagDisabled_WorkflowSkips() {
	// Override getter function to return false
	getBackupLogicalSizeSyncEnabled = func() bool { return false }

	// No mocks needed as workflow should exit early

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completes without executing any activities
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test Case 3: No volumes / only deleted volumes - no size sync
func (s *BackupLogicalSizeSyncIntegrationTestSuite) TestScenario3_NoVolumesOrDeletedVolumes_NoSizeSync() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Setup: Empty volume backup map (no ready volumes with backups)
	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(
		map[int64]*datamodel.VolumeLatestBackup{}, nil)

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completes successfully with no sync operations
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test Case 4: Multiple volumes with no backups - no size sync
func (s *BackupLogicalSizeSyncIntegrationTestSuite) TestScenario4_MultipleVolumesNoBackups_NoSizeSync() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Setup: Volumes without backups (latest backup is nil)
	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
				Name:      "volume-no-backup-1",
				State:     datamodel.LifeCycleStateREADY,
			},
			LatestBackup: nil, // No backup available
		},
		2: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 2, UUID: "volume-uuid-2"},
				Name:      "volume-no-backup-2",
				State:     datamodel.LifeCycleStateREADY,
			},
			LatestBackup: nil, // No backup available
		},
	}

	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(volumeBackupMap, nil)

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completes successfully with no update operations
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test Case 5: Multiple volumes in different states with backups in different states
func (s *BackupLogicalSizeSyncIntegrationTestSuite) TestScenario5_MultipleVolumesAndBackupsInDifferentStates_OnlyLatestAvailableBackupsSync() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Setup complex scenario with volumes and backups in various states
	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		// Volume 1: READY state with AVAILABLE backup (should sync)
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-ready-1"},
				Name:      "volume-ready-1",
				State:     datamodel.LifeCycleStateREADY,
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(0)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-available-1"},
				Name:      "backup-available-1",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-uuid-1",
					EndpointUUID:    "endpoint-uuid-1",
				},
			},
		},
		// Volume 2: READY state with AVAILABLE backup (should sync)
		2: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 2, UUID: "volume-ready-2"},
				Name:      "volume-ready-2",
				State:     datamodel.LifeCycleStateREADY,
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(500 * 1024 * 1024)), // 500MB existing
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-available-2"},
				Name:      "backup-available-2",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-uuid-2",
					EndpointUUID:    "endpoint-uuid-2",
				},
			},
		},
		// Volume 3: READY state with backup missing required attributes (should skip)
		3: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 3, UUID: "volume-ready-3"},
				Name:      "volume-ready-3",
				State:     datamodel.LifeCycleStateREADY,
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-no-attrs-3"},
				Name:      "backup-no-attrs-3",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "", // Missing ObjectStoreUUID
					EndpointUUID:    "endpoint-uuid-3",
				},
			},
		},
	}

	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(volumeBackupMap, nil)

	// For this test, we'll use activity mocks to avoid complex provider setup
	// Mock GetObjectStoreEndpointInfoActivity for valid backups
	objStoreEndpoint1 := &vsa.SmObjectStoreEndpointt{
		LogicalSize: nillable.ToPointer(int64(1024 * 1024 * 1024)), // 1GB
	}
	objStoreEndpoint2 := &vsa.SmObjectStoreEndpointt{
		LogicalSize: nillable.ToPointer(int64(2048 * 1024 * 1024)), // 2GB
	}

	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[1]).Return(objStoreEndpoint1, nil)
	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[2]).Return(objStoreEndpoint2, nil)
	// Volume 3 will return nil due to missing attributes (activity logic handles this)
	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[3]).Return(nil, nil)

	// Mock UpdateBackupAndVolumeActivity for successful updates
	s.env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[1], int64(1024*1024*1024)).Return(nil)
	s.env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[2], int64(2048*1024*1024)).Return(nil)

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completion
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Since we're using activity mocks, database calls are not made
	// Verify only that the workflow executed successfully
}

// Test Case 6: Deleted volumes should not be considered for syncing
func (s *BackupLogicalSizeSyncIntegrationTestSuite) TestScenario6_DeletedVolumesNotConsidered_NoSync() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Setup: The GetVolumeLatestBackupMap should already filter out deleted volumes
	// This is handled by the database query that only includes volumes with state = READY
	// So this test verifies that deleted volumes don't appear in the map at all

	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		// Only READY volumes should appear in the map
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-ready-only"},
				Name:      "volume-ready-only",
				State:     datamodel.LifeCycleStateREADY,
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(0)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-for-ready-volume"},
				Name:      "backup-for-ready-volume",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-uuid",
					EndpointUUID:    "endpoint-uuid",
				},
			},
		},
		// Note: Deleted volumes should not appear in this map due to database filtering
	}

	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(volumeBackupMap, nil)

	// Mock successful endpoint info retrieval and update
	objStoreEndpoint := &vsa.SmObjectStoreEndpointt{
		LogicalSize: nillable.ToPointer(int64(512 * 1024 * 1024)), // 512MB
	}
	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[1]).Return(objStoreEndpoint, nil)
	s.env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[1], int64(512*1024*1024)).Return(nil)

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completes successfully and only processes ready volumes
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Since we're using activity mocks, database calls are not made directly
	// Verify only that the workflow executed successfully
}

// Test Case 7: Scenarios where size doesn't get synced
func (s *BackupLogicalSizeSyncIntegrationTestSuite) TestScenario7_SizeNotSynced_VariousFailureConditions() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		// Volume 1: Object store endpoint returns nil logical size
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-nil-size"},
				Name:      "volume-nil-size",
				State:     datamodel.LifeCycleStateREADY,
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-nil-size"},
				Name:      "backup-nil-size",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-uuid-1",
					EndpointUUID:    "endpoint-uuid-1",
				},
			},
		},
		// Volume 2: Object store endpoint info retrieval fails
		2: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 2, UUID: "volume-endpoint-fail"},
				Name:      "volume-endpoint-fail",
				State:     datamodel.LifeCycleStateREADY,
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-endpoint-fail"},
				Name:      "backup-endpoint-fail",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-uuid-2",
					EndpointUUID:    "endpoint-uuid-2",
				},
			},
		},
		// Volume 3: Update operation fails
		3: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 3, UUID: "volume-update-fail"},
				Name:      "volume-update-fail",
				State:     datamodel.LifeCycleStateREADY,
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(0)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-update-fail"},
				Name:      "backup-update-fail",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-uuid-3",
					EndpointUUID:    "endpoint-uuid-3",
				},
			},
		},
	}

	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(volumeBackupMap, nil)

	// Volume 1: Return endpoint with nil logical size
	objStoreEndpointNilSize := &vsa.SmObjectStoreEndpointt{
		LogicalSize: nil, // Nil logical size should cause skip
	}
	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[1]).Return(objStoreEndpointNilSize, nil)

	// Volume 2: Return error for endpoint info retrieval
	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[2]).Return(nil, errors.New("failed to get object store endpoint info"))

	// Volume 3: Return valid endpoint info but fail on update
	objStoreEndpointValid := &vsa.SmObjectStoreEndpointt{
		LogicalSize: nillable.ToPointer(int64(1024 * 1024 * 1024)), // 1GB
	}
	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[3]).Return(objStoreEndpointValid, nil)
	s.env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[3], int64(1024*1024*1024)).Return(errors.New("database update failed"))

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completes successfully despite individual failures
	// The workflow should continue processing even when individual volumes fail
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Since we're using activity mocks, database calls are not made directly
	// Verify only that the workflow executed successfully with proper error handling
}

// Test Case 8: Null pointer handling
func (s *BackupLogicalSizeSyncIntegrationTestSuite) TestScenario8_NullPointerHandling_SafeProcessing() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		// Volume 1: Null/nil backup attributes
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-nil-attrs"},
				Name:      "volume-nil-attrs",
				State:     datamodel.LifeCycleStateREADY,
			},
			LatestBackup: &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "backup-nil-attrs"},
				Name:       "backup-nil-attrs",
				State:      datamodel.LifeCycleStateAvailable,
				Attributes: nil, // Nil attributes
			},
		},
		// Volume 2: Null/nil latest backup
		2: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 2, UUID: "volume-nil-backup"},
				Name:      "volume-nil-backup",
				State:     datamodel.LifeCycleStateREADY,
			},
			LatestBackup: nil, // Nil latest backup
		},
		// Volume 3: Null/nil data protection
		3: {
			Volume: &datamodel.Volume{
				BaseModel:      datamodel.BaseModel{ID: 3, UUID: "volume-nil-dataprotection"},
				Name:           "volume-nil-dataprotection",
				State:          datamodel.LifeCycleStateREADY,
				DataProtection: nil, // Nil data protection
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-for-nil-dataprotection"},
				Name:      "backup-for-nil-dataprotection",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-uuid-3",
					EndpointUUID:    "endpoint-uuid-3",
				},
			},
		},
		// Volume 4: Valid scenario for comparison
		4: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 4, UUID: "volume-valid"},
				Name:      "volume-valid",
				State:     datamodel.LifeCycleStateREADY,
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(0)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-valid"},
				Name:      "backup-valid",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-uuid-4",
					EndpointUUID:    "endpoint-uuid-4",
				},
			},
		},
	}

	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(volumeBackupMap, nil)

	// Mock GetObjectStoreEndpointInfoActivity for different null scenarios
	// Volume 1: Nil attributes should return nil
	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[1]).Return(nil, nil)
	// Volume 2: Nil backup should return nil
	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[2]).Return(nil, nil)
	// Volume 3: Valid attributes should get endpoint info
	objStoreEndpoint3 := &vsa.SmObjectStoreEndpointt{
		LogicalSize: nillable.ToPointer(int64(512 * 1024 * 1024)), // 512MB
	}
	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[3]).Return(objStoreEndpoint3, nil)
	// The update for volume 3 might fail due to nil data protection, but should be handled gracefully
	s.env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[3], int64(512*1024*1024)).Return(nil)

	// Volume 4: Valid scenario should work normally
	objStoreEndpoint4 := &vsa.SmObjectStoreEndpointt{
		LogicalSize: nillable.ToPointer(int64(1024 * 1024 * 1024)), // 1GB
	}
	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[4]).Return(objStoreEndpoint4, nil)
	s.env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[4], int64(1024*1024*1024)).Return(nil)

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completes successfully without panics despite null pointers
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Since we're using activity mocks, database calls are not made directly
	// Verify only that the workflow executed successfully with proper null pointer handling
}

// Test Case 9: Large scale scenario - multiple volumes with mixed success/failure
func (s *BackupLogicalSizeSyncIntegrationTestSuite) TestScenario9_LargeScale_MixedSuccessFailure() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Create a large set of volumes with different scenarios
	volumeBackupMap := make(map[int64]*datamodel.VolumeLatestBackup)

	// Add 10 successful volumes
	for i := int64(1); i <= 10; i++ {
		volumeBackupMap[i] = &datamodel.VolumeLatestBackup{
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: i, UUID: fmt.Sprintf("volume-success-%d", i)},
				Name:      fmt.Sprintf("volume-success-%d", i),
				State:     datamodel.LifeCycleStateREADY,
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(100 * 1024 * 1024)), // 100MB
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: fmt.Sprintf("backup-success-%d", i)},
				Name:      fmt.Sprintf("backup-success-%d", i),
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: fmt.Sprintf("object-store-uuid-%d", i),
					EndpointUUID:    fmt.Sprintf("endpoint-uuid-%d", i),
				},
			},
		}
	}

	// Add 5 volumes that will fail at different stages
	for i := int64(11); i <= 15; i++ {
		volumeBackupMap[i] = &datamodel.VolumeLatestBackup{
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: i, UUID: fmt.Sprintf("volume-fail-%d", i)},
				Name:      fmt.Sprintf("volume-fail-%d", i),
				State:     datamodel.LifeCycleStateREADY,
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(200 * 1024 * 1024)), // 200MB
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: fmt.Sprintf("backup-fail-%d", i)},
				Name:      fmt.Sprintf("backup-fail-%d", i),
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: fmt.Sprintf("object-store-uuid-%d", i),
					EndpointUUID:    fmt.Sprintf("endpoint-uuid-%d", i),
				},
			},
		}
	}

	s.env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(volumeBackupMap, nil)

	// Mock successful scenarios (volumes 1-10)
	for i := int64(1); i <= 10; i++ {
		objStoreEndpoint := &vsa.SmObjectStoreEndpointt{
			LogicalSize: nillable.ToPointer(int64(i * 100 * 1024 * 1024)), // i * 100MB
		}
		s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[i]).Return(objStoreEndpoint, nil)
		s.env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[i], int64(i*100*1024*1024)).Return(nil)
	}

	// Mock failure scenarios (volumes 11-15)
	for i := int64(11); i <= 15; i++ {
		if i%2 == 1 {
			// Odd numbered volumes fail at endpoint info retrieval
			s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[i]).Return(nil, errors.New("endpoint info retrieval failed"))
		} else {
			// Even numbered volumes fail at update
			objStoreEndpoint := &vsa.SmObjectStoreEndpointt{
				LogicalSize: nillable.ToPointer(int64(i * 100 * 1024 * 1024)),
			}
			s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[i]).Return(objStoreEndpoint, nil)
			s.env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[i], int64(i*100*1024*1024)).Return(errors.New("update failed"))
		}
	}

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completes successfully despite partial failures
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test Case 10: Critical failure scenario - database connection failure
func (s *BackupLogicalSizeSyncIntegrationTestSuite) TestScenario10_CriticalFailure_DatabaseConnectionFailure() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Mock database connection failure
	s.env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(
		nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, errors.New("database connection failed")))

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow fails appropriately
	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "An internal error occurred")
}

// Test Case 11: Edge case - Empty object store UUIDs and endpoint UUIDs
func (s *BackupLogicalSizeSyncIntegrationTestSuite) TestScenario11_EdgeCase_EmptyUUIDs() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-empty-object-store"},
				Name:      "volume-empty-object-store",
				State:     datamodel.LifeCycleStateREADY,
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-empty-object-store"},
				Name:      "backup-empty-object-store",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "", // Empty ObjectStoreUUID
					EndpointUUID:    "endpoint-uuid-1",
				},
			},
		},
		2: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 2, UUID: "volume-empty-endpoint"},
				Name:      "volume-empty-endpoint",
				State:     datamodel.LifeCycleStateREADY,
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-empty-endpoint"},
				Name:      "backup-empty-endpoint",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-uuid-2",
					EndpointUUID:    "", // Empty EndpointUUID
				},
			},
		},
	}

	s.env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(volumeBackupMap, nil)

	// Both volumes should return nil due to missing required attributes
	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[1]).Return(nil, nil)
	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[2]).Return(nil, nil)

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completes successfully with skipped volumes
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// Test Case 12: Performance scenario - Verify workflow handles timeout gracefully
func (s *BackupLogicalSizeSyncIntegrationTestSuite) TestScenario12_Performance_TimeoutHandling() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Create scenario with single volume
	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-timeout-test"},
				Name:      "volume-timeout-test",
				State:     datamodel.LifeCycleStateREADY,
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(0)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-timeout-test"},
				Name:      "backup-timeout-test",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-uuid-timeout",
					EndpointUUID:    "endpoint-uuid-timeout",
				},
			},
		},
	}

	s.env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(volumeBackupMap, nil)

	// Simulate timeout scenario with context cancellation
	s.env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[1]).Return(
		nil, context.DeadlineExceeded)

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow handles timeout gracefully and continues
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// BackupLogicalSizeSyncDatabaseVerificationTestSuite focuses specifically on database synchronization verification
// This test suite ensures that all database operations are properly executed and verified during backup logical size sync
type BackupLogicalSizeSyncDatabaseVerificationTestSuite struct {
	testsuite.WorkflowTestSuite
	suite.Suite
	env            *testsuite.TestWorkflowEnvironment
	mockStorage    *database.MockStorage
	mockProvider   *vsa.MockProvider
	originalGetter func() bool
}

func TestBackupLogicalSizeSyncDatabaseVerificationTestSuite(t *testing.T) {
	suite.Run(t, new(BackupLogicalSizeSyncDatabaseVerificationTestSuite))
}

func (s *BackupLogicalSizeSyncDatabaseVerificationTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	// Setup header for logging
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	s.env.SetHeader(mockHeader)

	// Register workflow
	s.env.RegisterWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Setup mocks
	s.mockStorage = database.NewMockStorage(s.T())
	s.mockProvider = &vsa.MockProvider{}

	// Register activity with mocked dependencies for database verification testing
	activity := &backgroundactivities.VolumeBackupSyncActivity{
		SE: s.mockStorage,
	}
	s.env.RegisterActivity(activity)

	// Store original getter function
	s.originalGetter = getBackupLogicalSizeSyncEnabled
}

func (s *BackupLogicalSizeSyncDatabaseVerificationTestSuite) TearDownTest() {
	// Restore original getter function
	getBackupLogicalSizeSyncEnabled = s.originalGetter
}

// TestDatabaseSyncVerification_SingleVolumeUpdate verifies complete database synchronization for a single volume
func (s *BackupLogicalSizeSyncDatabaseVerificationTestSuite) TestDatabaseSyncVerification_SingleVolumeUpdate() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Setup: Single volume with backup that needs logical size sync
	initialLogicalSize := int64(100 * 1024 * 1024) // 100MB initial
	updatedLogicalSize := int64(500 * 1024 * 1024) // 500MB updated

	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
				Name:      "test-volume",
				State:     datamodel.LifeCycleStateREADY,
				PoolID:    1,
				Pool: &datamodel.Pool{
					BaseModel:      datamodel.BaseModel{ID: 1},
					DeploymentName: "test-deployment",
					PoolCredentials: &datamodel.PoolCredentials{
						Password:      "test-password",
						SecretID:      "test-secret",
						CertificateID: "test-cert",
						AuthType:      env.USERNAME_PWD,
					},
				},
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: &initialLogicalSize,
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "test-backup-uuid"},
				VolumeUUID: "test-volume-uuid",
				Name:       "test-backup",
				State:      datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "test-object-store-uuid",
					EndpointUUID:    "test-endpoint-uuid",
				},
			},
		},
	}

	// Mock vsa.GetProviderByNode function
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return s.mockProvider, nil
	}

	// Mock storage dependencies - GetVolumeLatestBackupMap
	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(volumeBackupMap, nil)

	// Mock storage dependencies - GetNodesByPoolID
	testNodes := []*datamodel.Node{{
		BaseModel:       datamodel.BaseModel{ID: 1},
		EndpointAddress: "test-node-1",
	}}
	s.mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).Return(testNodes, nil)

	// Mock provider dependencies - ObjectStoreEndpointInfoGet
	objStoreEndpoint := &vsa.SmObjectStoreEndpointt{
		LogicalSize: &updatedLogicalSize,
	}
	s.mockProvider.On("ObjectStoreEndpointInfoGet", "test-object-store-uuid", "test-endpoint-uuid").Return(objStoreEndpoint, nil)

	// Setup comprehensive database update mocks with precise verification
	s.mockStorage.On("UpdateBackupFields", mock.Anything, "test-backup-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
		// Verify the backup update contains the correct logical size
		logicalSize, exists := updates["latest_logical_backup_size"]
		return exists && logicalSize == updatedLogicalSize
	})).Return(nil).Once()

	expectRecalcVolumeChainBytesSync(s.mockStorage, "test-volume-uuid", updatedLogicalSize, "test-endpoint-uuid")

	// Execute workflow - NO activity mocking, let real activities run
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completion
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify exact database operations
	s.mockStorage.AssertExpectations(s.T())
	s.mockProvider.AssertExpectations(s.T())

	// Verify specific calls with exact parameters
	s.mockStorage.AssertCalled(s.T(), "UpdateBackupFields", mock.Anything, "test-backup-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
		logicalSize, exists := updates["latest_logical_backup_size"]
		return exists && logicalSize == updatedLogicalSize
	}))

	s.mockStorage.AssertCalled(s.T(), "UpdateVolumeFields", mock.Anything, "test-volume-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
		dataProtection, exists := updates["data_protection"]
		if !exists {
			return false
		}
		dp, ok := dataProtection.(*datamodel.DataProtection)
		return ok && dp.BackupChainBytes != nil && *dp.BackupChainBytes == updatedLogicalSize
	}))
}

// TestDatabaseSyncVerification_MultipleVolumesPartialUpdate verifies database operations for multiple volumes with mixed success
func (s *BackupLogicalSizeSyncDatabaseVerificationTestSuite) TestDatabaseSyncVerification_MultipleVolumesPartialUpdate() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Setup: Multiple volumes with different sync scenarios
	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		// Volume 1: Successful sync
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-success-uuid"},
				Name:      "volume-success",
				State:     datamodel.LifeCycleStateREADY,
				PoolID:    1,
				Pool: &datamodel.Pool{
					BaseModel:      datamodel.BaseModel{ID: 1},
					DeploymentName: "test-deployment-multi",
					PoolCredentials: &datamodel.PoolCredentials{
						Password:      "test-password",
						SecretID:      "test-secret",
						CertificateID: "test-cert",
						AuthType:      env.USERNAME_PWD,
					},
				},
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(100 * 1024 * 1024)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "backup-success-uuid"},
				VolumeUUID: "volume-success-uuid",
				Name:       "backup-success",
				State:      datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-success-uuid",
					EndpointUUID:    "endpoint-success-uuid",
				},
			},
		},
		// Volume 2: Backup update fails
		2: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 2, UUID: "volume-backup-fail-uuid"},
				Name:      "volume-backup-fail",
				State:     datamodel.LifeCycleStateREADY,
				PoolID:    1,
				Pool: &datamodel.Pool{
					BaseModel:      datamodel.BaseModel{ID: 1},
					DeploymentName: "test-deployment-multi",
					PoolCredentials: &datamodel.PoolCredentials{
						Password:      "test-password",
						SecretID:      "test-secret",
						CertificateID: "test-cert",
						AuthType:      env.USERNAME_PWD,
					},
				},
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(200 * 1024 * 1024)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-fail-uuid"},
				Name:      "backup-fail",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-fail-uuid",
					EndpointUUID:    "endpoint-fail-uuid",
				},
			},
		},
		// Volume 3: Volume update fails
		3: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 3, UUID: "volume-vol-fail-uuid"},
				Name:      "volume-vol-fail",
				State:     datamodel.LifeCycleStateREADY,
				PoolID:    1,
				Pool: &datamodel.Pool{
					BaseModel:      datamodel.BaseModel{ID: 1},
					DeploymentName: "test-deployment-multi",
					PoolCredentials: &datamodel.PoolCredentials{
						Password:      "test-password",
						SecretID:      "test-secret",
						CertificateID: "test-cert",
						AuthType:      env.USERNAME_PWD,
					},
				},
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(300 * 1024 * 1024)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{UUID: "backup-vol-fail-uuid"},
				Name:      "backup-vol-fail",
				State:     datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-vol-fail-uuid",
					EndpointUUID:    "endpoint-vol-fail-uuid",
				},
			},
		},
	}

	// Mock vsa.GetProviderByNode function
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return s.mockProvider, nil
	}

	// Mock storage dependencies - GetVolumeLatestBackupMap
	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(volumeBackupMap, nil)

	// Mock storage dependencies - GetNodesByPoolID
	testNodes := []*datamodel.Node{{
		BaseModel:       datamodel.BaseModel{ID: 1},
		EndpointAddress: "test-node-1",
	}}
	s.mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).Return(testNodes, nil)

	// Mock provider dependencies - ObjectStoreEndpointInfoGet with different responses for each volume
	successLogicalSize := int64(1024 * 1024 * 1024) // 1GB
	failLogicalSize := int64(2048 * 1024 * 1024)    // 2GB
	volFailLogicalSize := int64(3072 * 1024 * 1024) // 3GB

	// Return success for volume 1
	s.mockProvider.On("ObjectStoreEndpointInfoGet", "object-store-success-uuid", "endpoint-success-uuid").Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &successLogicalSize}, nil)

	// Return success for volume 2 (provider call succeeds, but database update will fail)
	s.mockProvider.On("ObjectStoreEndpointInfoGet", "object-store-fail-uuid", "endpoint-fail-uuid").Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &failLogicalSize}, nil)

	// Return success for volume 3 (provider call succeeds, but volume database update will fail)
	s.mockProvider.On("ObjectStoreEndpointInfoGet", "object-store-vol-fail-uuid", "endpoint-vol-fail-uuid").Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &volFailLogicalSize}, nil)

	// Setup database update mocks with specific failure scenarios
	// Volume 1: Both backup and volume updates succeed
	s.mockStorage.On("UpdateBackupFields", mock.Anything, "backup-success-uuid", mock.Anything).Return(nil)
	expectRecalcVolumeChainBytesSync(s.mockStorage, "volume-success-uuid", successLogicalSize, "endpoint-success-uuid")

	// Volume 2: Backup update fails (may be retried)
	s.mockStorage.On("UpdateBackupFields", mock.Anything, "backup-fail-uuid", mock.Anything).Return(errors.New("backup update failed"))
	// Volume update should not be called for volume 2 since backup update fails first

	// Volume 3: Backup update succeeds but recalc volume update fails (may be retried)
	s.mockStorage.On("UpdateBackupFields", mock.Anything, "backup-vol-fail-uuid", mock.Anything).Return(nil)
	recalcVolFail := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "volume-vol-fail-uuid"}, DataProtection: &datamodel.DataProtection{}}
	s.mockStorage.On("GetVolume", mock.Anything, "volume-vol-fail-uuid").Return(recalcVolFail, nil)
	s.mockStorage.On("SumVolumeBackupChainBytes", mock.Anything, "volume-vol-fail-uuid").Return(volFailLogicalSize, nil)
	s.mockStorage.On("UpdateVolumeFields", mock.Anything, "volume-vol-fail-uuid", mock.Anything).Return(errors.New("volume update failed"))

	// Execute workflow - NO activity mocking, let real activities run
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completion (should complete despite individual failures)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify exact database operation counts and calls
	s.mockStorage.AssertExpectations(s.T())
	s.mockProvider.AssertExpectations(s.T())

	// Verify that the right methods were called for each volume
	s.mockStorage.AssertCalled(s.T(), "UpdateBackupFields", mock.Anything, "backup-success-uuid", mock.Anything)
	s.mockStorage.AssertCalled(s.T(), "UpdateVolumeFields", mock.Anything, "volume-success-uuid", mock.Anything)
	s.mockStorage.AssertCalled(s.T(), "UpdateBackupFields", mock.Anything, "backup-fail-uuid", mock.Anything)
	s.mockStorage.AssertCalled(s.T(), "UpdateBackupFields", mock.Anything, "backup-vol-fail-uuid", mock.Anything)
	s.mockStorage.AssertCalled(s.T(), "UpdateVolumeFields", mock.Anything, "volume-vol-fail-uuid", mock.Anything)
}

// TestDatabaseSyncVerification_GetVolumeLatestBackupMapFailure verifies behavior when initial database query fails
func (s *BackupLogicalSizeSyncDatabaseVerificationTestSuite) TestDatabaseSyncVerification_GetVolumeLatestBackupMapFailure() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Mock the initial database query to fail
	s.env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(
		nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, errors.New("failed to query volumes")))

	// Execute workflow
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow fails when initial database query fails
	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())

	// Verify no update operations are attempted when initial query fails
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateBackupFields", 0)
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateVolumeFields", 0)
}

// TestDatabaseSyncVerification_ZeroLogicalSize verifies handling of zero logical size
func (s *BackupLogicalSizeSyncDatabaseVerificationTestSuite) TestDatabaseSyncVerification_ZeroLogicalSize() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Setup: Volume with backup that has zero logical size
	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-zero-size-uuid"},
				Name:      "volume-zero-size",
				State:     datamodel.LifeCycleStateREADY,
				PoolID:    1,
				Pool: &datamodel.Pool{
					BaseModel:      datamodel.BaseModel{ID: 1},
					DeploymentName: "test-deployment-zero",
					PoolCredentials: &datamodel.PoolCredentials{
						Password:      "test-password",
						SecretID:      "test-secret",
						CertificateID: "test-cert",
						AuthType:      env.USERNAME_PWD,
					},
				},
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(100 * 1024 * 1024)),
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "backup-zero-size-uuid"},
				VolumeUUID: "volume-zero-size-uuid",
				Name:       "backup-zero-size",
				State:      datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-zero-uuid",
					EndpointUUID:    "endpoint-zero-uuid",
				},
			},
		},
	}

	// Mock vsa.GetProviderByNode function
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return s.mockProvider, nil
	}

	// Mock storage dependencies - GetVolumeLatestBackupMap
	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(volumeBackupMap, nil)

	// Mock storage dependencies - GetNodesByPoolID
	testNodes := []*datamodel.Node{{
		BaseModel:       datamodel.BaseModel{ID: 1},
		EndpointAddress: "test-node-1",
	}}
	s.mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).Return(testNodes, nil)

	// Mock provider dependencies - ObjectStoreEndpointInfoGet
	zeroLogicalSize := int64(0)
	s.mockProvider.On("ObjectStoreEndpointInfoGet", mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointt{
		LogicalSize: &zeroLogicalSize,
	}, nil)

	// Setup database update mocks for zero size
	s.mockStorage.On("UpdateBackupFields", mock.Anything, "backup-zero-size-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
		logicalSize, exists := updates["latest_logical_backup_size"]
		return exists && logicalSize == int64(0)
	})).Return(nil).Once()

	expectRecalcVolumeChainBytesSync(s.mockStorage, "volume-zero-size-uuid", int64(0), "endpoint-zero-uuid")

	// Execute workflow - NO activity mocking, let real activities run
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completion
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify database operations for zero size are handled correctly
	s.mockStorage.AssertExpectations(s.T())
	s.mockProvider.AssertExpectations(s.T())
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateBackupFields", 1)
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateVolumeFields", 1)
}

// TestDatabaseSyncVerification_LargeLogicalSize verifies handling of very large logical sizes
func (s *BackupLogicalSizeSyncDatabaseVerificationTestSuite) TestDatabaseSyncVerification_LargeLogicalSize() {
	// Override getter function to return true
	getBackupLogicalSizeSyncEnabled = func() bool { return true }

	// Setup: Volume with backup that has very large logical size (5TB)
	largeLogicalSize := int64(5 * 1024 * 1024 * 1024 * 1024) // 5TB

	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		1: {
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-large-size-uuid"},
				Name:      "volume-large-size",
				State:     datamodel.LifeCycleStateREADY,
				PoolID:    1,
				Pool: &datamodel.Pool{
					BaseModel:      datamodel.BaseModel{ID: 1},
					DeploymentName: "test-deployment-large",
					PoolCredentials: &datamodel.PoolCredentials{
						Password:      "test-password",
						SecretID:      "test-secret",
						CertificateID: "test-cert",
						AuthType:      env.USERNAME_PWD,
					},
				},
				DataProtection: &datamodel.DataProtection{
					BackupChainBytes: nillable.ToPointer(int64(1024 * 1024 * 1024 * 1024)), // 1TB initial
				},
			},
			LatestBackup: &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "backup-large-size-uuid"},
				VolumeUUID: "volume-large-size-uuid",
				Name:       "backup-large-size",
				State:      datamodel.LifeCycleStateAvailable,
				Attributes: &datamodel.BackupAttributes{
					ObjectStoreUUID: "object-store-large-uuid",
					EndpointUUID:    "endpoint-large-uuid",
				},
			},
		},
	}

	// Mock vsa.GetProviderByNode function
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return s.mockProvider, nil
	}

	// Mock storage dependencies - GetVolumeLatestBackupMap
	s.mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(volumeBackupMap, nil)

	// Mock storage dependencies - GetNodesByPoolID
	testNodes := []*datamodel.Node{{
		BaseModel:       datamodel.BaseModel{ID: 1},
		EndpointAddress: "test-node-1",
	}}
	s.mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).Return(testNodes, nil)

	// Mock provider dependencies - ObjectStoreEndpointInfoGet
	s.mockProvider.On("ObjectStoreEndpointInfoGet", mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointt{
		LogicalSize: &largeLogicalSize,
	}, nil)

	// Setup database update mocks for large size
	s.mockStorage.On("UpdateBackupFields", mock.Anything, "backup-large-size-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
		logicalSize, exists := updates["latest_logical_backup_size"]
		return exists && logicalSize == largeLogicalSize
	})).Return(nil).Once()

	expectRecalcVolumeChainBytesSync(s.mockStorage, "volume-large-size-uuid", largeLogicalSize, "endpoint-large-uuid")

	// Execute workflow - NO activity mocking, let real activities run
	s.env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

	// Verify workflow completion
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify database operations for large size are handled correctly
	s.mockStorage.AssertExpectations(s.T())
	s.mockProvider.AssertExpectations(s.T())
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateBackupFields", 1)
	s.mockStorage.AssertNumberOfCalls(s.T(), "UpdateVolumeFields", 1)

	// Verify the exact large size was persisted
	s.mockStorage.AssertCalled(s.T(), "UpdateBackupFields", mock.Anything, "backup-large-size-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
		logicalSize, exists := updates["latest_logical_backup_size"]
		return exists && logicalSize == largeLogicalSize
	}))
}

// TestSyncLatestBackupLogicalSizeWorkflow_GcbdrBackprop verifies the GCBDR back-propagation
// activity is invoked only when the GCBDR_BACKUP_CHAIN_BACKPROP_ENABLED flag is on.
func TestSyncLatestBackupLogicalSizeWorkflow_GcbdrBackprop(t *testing.T) {
	volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
		1: {
			Volume: &datamodel.Volume{BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"}, Name: "test-volume-1"},
			LatestBackup: &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "backup-uuid-1"},
				Name:       "test-backup-1",
				Attributes: &datamodel.BackupAttributes{ObjectStoreUUID: "obj-1", EndpointUUID: "ep-1"},
			},
		},
	}
	objStore := &vsa.SmObjectStoreEndpointt{LogicalSize: nillable.ToPointer(int64(80))}

	tests := []struct {
		name            string
		backpropEnabled bool
	}{
		{name: "backprop enabled - activity is called", backpropEnabled: true},
		{name: "backprop disabled - activity is not called", backpropEnabled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origSync := getBackupLogicalSizeSyncEnabled
			getBackupLogicalSizeSyncEnabled = func() bool { return true }
			defer func() { getBackupLogicalSizeSyncEnabled = origSync }()

			origBackprop := getGcbdrBackupChainBackpropEnabled
			getGcbdrBackupChainBackpropEnabled = func() bool { return tt.backpropEnabled }
			defer func() { getGcbdrBackupChainBackpropEnabled = origBackprop }()

			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestWorkflowEnvironment()
			env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

			encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
			env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})

			env.RegisterWorkflow(SyncLatestBackupLogicalSizeWorkflow)
			env.RegisterActivity(&backgroundactivities.VolumeBackupSyncActivity{})

			env.OnActivity("GetVolumeLatestBackupMapActivity", mock.Anything).Return(volumeBackupMap, nil)
			env.OnActivity("GetObjectStoreEndpointInfoActivity", mock.Anything, volumeBackupMap[1]).Return(objStore, nil)
			env.OnActivity("UpdateBackupAndVolumeActivity", mock.Anything, volumeBackupMap[1], int64(80)).Return(nil)
			if tt.backpropEnabled {
				env.OnActivity("BackPropagateGcbdrBackupChainSizesActivity", mock.Anything, volumeBackupMap[1], int64(80)).Return(nil).Once()
			}

			env.ExecuteWorkflow(SyncLatestBackupLogicalSizeWorkflow)

			assert.True(t, env.IsWorkflowCompleted())
			assert.NoError(t, env.GetWorkflowError())

			if tt.backpropEnabled {
				env.AssertCalled(t, "BackPropagateGcbdrBackupChainSizesActivity", mock.Anything, volumeBackupMap[1], int64(80))
			} else {
				env.AssertNotCalled(t, "BackPropagateGcbdrBackupChainSizesActivity", mock.Anything, mock.Anything, mock.Anything)
			}
		})
	}
}
