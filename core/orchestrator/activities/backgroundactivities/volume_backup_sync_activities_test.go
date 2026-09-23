package backgroundactivities

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"gorm.io/gorm"
)

// VolumeBackupSyncActivityUnitTestSuite contains unit tests for all VolumeBackupSyncActivity methods
type VolumeBackupSyncActivityUnitTestSuite struct {
	suite.Suite
	mockStorage  *database.MockStorage
	mockProvider *vsa.MockProvider
	activity     *VolumeBackupSyncActivity
	ctx          context.Context
}

func TestVolumeBackupSyncActivityUnitTestSuite(t *testing.T) {
	suite.Run(t, new(VolumeBackupSyncActivityUnitTestSuite))
}

func (s *VolumeBackupSyncActivityUnitTestSuite) SetupTest() {
	s.mockStorage = database.NewMockStorage(s.T())
	s.mockProvider = &vsa.MockProvider{}
	s.activity = &VolumeBackupSyncActivity{SE: s.mockStorage}
	s.ctx = context.Background()
}

func (s *VolumeBackupSyncActivityUnitTestSuite) TearDownTest() {
	s.mockStorage.AssertExpectations(s.T())
	s.mockProvider.AssertExpectations(s.T())
}

// Test GetVolumeLatestBackupMapActivity
func (s *VolumeBackupSyncActivityUnitTestSuite) TestGetVolumeLatestBackupMapActivity() {
	tests := []struct {
		name           string
		setupMock      func()
		expectedResult map[int64]*datamodel.VolumeLatestBackup
		expectedError  bool
		errorContains  string
	}{
		{
			name: "Success - Returns volume backup map",
			setupMock: func() {
				volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
					1: {
						Volume: &datamodel.Volume{
							BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
							Name:      "test-volume-1",
						},
						LatestBackup: &datamodel.Backup{
							BaseModel: datamodel.BaseModel{UUID: "backup-uuid-1"},
							Name:      "test-backup-1",
						},
					},
				}
				s.mockStorage.On("GetVolumeLatestBackupMap", s.ctx).Return(volumeBackupMap, nil)
			},
			expectedResult: map[int64]*datamodel.VolumeLatestBackup{
				1: {
					Volume: &datamodel.Volume{
						BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
						Name:      "test-volume-1",
					},
					LatestBackup: &datamodel.Backup{
						BaseModel: datamodel.BaseModel{UUID: "backup-uuid-1"},
						Name:      "test-backup-1",
					},
				},
			},
			expectedError: false,
		},
		{
			name: "Success - Empty map",
			setupMock: func() {
				s.mockStorage.On("GetVolumeLatestBackupMap", s.ctx).Return(map[int64]*datamodel.VolumeLatestBackup{}, nil)
			},
			expectedResult: map[int64]*datamodel.VolumeLatestBackup{},
			expectedError:  false,
		},
		{
			name: "Error - Database connection failure",
			setupMock: func() {
				s.mockStorage.On("GetVolumeLatestBackupMap", s.ctx).Return(
					nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseConnectionClosed, errors.New("connection failed")))
			},
			expectedResult: nil,
			expectedError:  true,
		},
		{
			name: "Error - Database query error",
			setupMock: func() {
				s.mockStorage.On("GetVolumeLatestBackupMap", s.ctx).Return(
					nil, errors.New("database query failed"))
			},
			expectedResult: nil,
			expectedError:  true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			// Reset mocks for each test
			s.SetupTest()

			// Setup
			tt.setupMock()

			// Execute
			result, err := s.activity.GetVolumeLatestBackupMapActivity(s.ctx)

			// Verify
			if tt.expectedError {
				s.Error(err)
				s.Nil(result)
			} else {
				s.NoError(err)
				s.Equal(tt.expectedResult, result)
			}
		})
	}
}

// Test getObjectStoreEndpointInfo
func (s *VolumeBackupSyncActivityUnitTestSuite) TestGetObjectStoreEndpointInfo() {
	tests := []struct {
		name               string
		objectStoreUUID    string
		endpointUUID       string
		volumeBackup       *datamodel.VolumeLatestBackup
		setupMock          func()
		expectedResult     *vsa.SmObjectStoreEndpointt
		expectedError      bool
		errorContains      string
		mockProviderNeeded bool
	}{
		{
			name:            "Success - Valid endpoint info",
			objectStoreUUID: "obj-store-uuid",
			endpointUUID:    "endpoint-uuid",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					PoolID: 1,
					Pool: &datamodel.Pool{
						DeploymentName: "test-deployment",
						PoolCredentials: &datamodel.PoolCredentials{
							Password:      "test-pass",
							SecretID:      "test-secret",
							CertificateID: "test-cert",
							AuthType:      1,
						},
					},
				},
			},
			setupMock: func() {
				nodes := []*datamodel.Node{
					{
						BaseModel:       datamodel.BaseModel{ID: 1},
						EndpointAddress: "192.168.1.1",
						PoolID:          1,
					},
				}
				s.mockStorage.On("GetNodesByPoolID", s.ctx, int64(1)).Return(nodes, nil)

				objStoreEndpoint := &vsa.SmObjectStoreEndpointt{
					LogicalSize: nillable.ToPointer(int64(1024 * 1024 * 1024)), // 1GB
				}
				s.mockProvider.On("ObjectStoreEndpointInfoGet", "obj-store-uuid", "endpoint-uuid").Return(objStoreEndpoint, nil)
			},
			expectedResult: &vsa.SmObjectStoreEndpointt{
				LogicalSize: nillable.ToPointer(int64(1024 * 1024 * 1024)),
			},
			expectedError:      false,
			mockProviderNeeded: true,
		},
		{
			name:            "Error - No nodes found for pool",
			objectStoreUUID: "obj-store-uuid",
			endpointUUID:    "endpoint-uuid",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					PoolID: 1,
					Pool: &datamodel.Pool{
						DeploymentName: "test-deployment",
						PoolCredentials: &datamodel.PoolCredentials{
							Password:      "test-pass",
							SecretID:      "test-secret",
							CertificateID: "test-cert",
							AuthType:      1,
						},
					},
				},
			},
			setupMock: func() {
				s.mockStorage.On("GetNodesByPoolID", s.ctx, int64(1)).Return([]*datamodel.Node{}, nil)
			},
			expectedResult: nil,
			expectedError:  true,
			errorContains:  "Node not found for the pool",
		},
		{
			name:            "Error - Database error getting nodes",
			objectStoreUUID: "obj-store-uuid",
			endpointUUID:    "endpoint-uuid",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					PoolID: 1,
					Pool: &datamodel.Pool{
						DeploymentName: "test-deployment",
						PoolCredentials: &datamodel.PoolCredentials{
							Password:      "test-pass",
							SecretID:      "test-secret",
							CertificateID: "test-cert",
							AuthType:      1,
						},
					},
				},
			},
			setupMock: func() {
				s.mockStorage.On("GetNodesByPoolID", s.ctx, int64(1)).Return(nil, errors.New("database error"))
			},
			expectedResult: nil,
			expectedError:  true,
		},
		{
			name:            "Error - Provider error",
			objectStoreUUID: "obj-store-uuid",
			endpointUUID:    "endpoint-uuid",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					PoolID: 1,
					Pool: &datamodel.Pool{
						DeploymentName: "test-deployment",
						PoolCredentials: &datamodel.PoolCredentials{
							Password:      "test-pass",
							SecretID:      "test-secret",
							CertificateID: "test-cert",
							AuthType:      1,
						},
					},
				},
			},
			setupMock: func() {
				nodes := []*datamodel.Node{
					{
						BaseModel:       datamodel.BaseModel{ID: 1},
						EndpointAddress: "192.168.1.1",
						PoolID:          1,
					},
				}
				s.mockStorage.On("GetNodesByPoolID", s.ctx, int64(1)).Return(nodes, nil)
				s.mockProvider.On("ObjectStoreEndpointInfoGet", "obj-store-uuid", "endpoint-uuid").Return(nil, errors.New("provider error"))
			},
			expectedResult:     nil,
			expectedError:      true,
			mockProviderNeeded: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			// Reset mocks for each test
			s.SetupTest()

			// Setup
			tt.setupMock()

			// Mock vsa.GetProviderByNode if needed
			var originalGetProviderByNode func(context.Context, *models.Node) (vsa.Provider, error)
			if tt.mockProviderNeeded {
				originalGetProviderByNode = vsa.GetProviderByNode
				vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
					return s.mockProvider, nil
				}
				defer func() {
					vsa.GetProviderByNode = originalGetProviderByNode
				}()
			}

			// Execute
			result, err := s.activity.getObjectStoreEndpointInfo(s.ctx, tt.objectStoreUUID, tt.endpointUUID, tt.volumeBackup)

			// Verify
			if tt.expectedError {
				s.Error(err)
				s.Nil(result)
			} else {
				s.NoError(err)
				s.Equal(tt.expectedResult, result)
			}
		})
	}
}

// Test GetObjectStoreEndpointInfoActivity
func (s *VolumeBackupSyncActivityUnitTestSuite) TestGetObjectStoreEndpointInfoActivity() {
	tests := []struct {
		name               string
		volumeBackup       *datamodel.VolumeLatestBackup
		setupMock          func()
		expectedResult     *vsa.SmObjectStoreEndpointt
		expectedError      bool
		mockProviderNeeded bool
	}{
		{
			name: "Success - Valid backup with attributes",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					Name:   "test-volume",
					PoolID: 1,
					Pool: &datamodel.Pool{
						DeploymentName: "test-deployment",
						PoolCredentials: &datamodel.PoolCredentials{
							Password:      "test-pass",
							SecretID:      "test-secret",
							CertificateID: "test-cert",
							AuthType:      1,
						},
					},
				},
				LatestBackup: &datamodel.Backup{
					Name: "test-backup",
					Attributes: &datamodel.BackupAttributes{
						ObjectStoreUUID: "obj-store-uuid",
						EndpointUUID:    "endpoint-uuid",
					},
				},
			},
			setupMock: func() {
				nodes := []*datamodel.Node{
					{
						BaseModel:       datamodel.BaseModel{ID: 1},
						EndpointAddress: "192.168.1.1",
						PoolID:          1,
					},
				}
				s.mockStorage.On("GetNodesByPoolID", s.ctx, int64(1)).Return(nodes, nil)

				objStoreEndpoint := &vsa.SmObjectStoreEndpointt{
					LogicalSize: nillable.ToPointer(int64(2 * 1024 * 1024 * 1024)), // 2GB
				}
				s.mockProvider.On("ObjectStoreEndpointInfoGet", "obj-store-uuid", "endpoint-uuid").Return(objStoreEndpoint, nil)
			},
			expectedResult: &vsa.SmObjectStoreEndpointt{
				LogicalSize: nillable.ToPointer(int64(2 * 1024 * 1024 * 1024)),
			},
			expectedError:      false,
			mockProviderNeeded: true,
		},
		{
			name: "Success - No latest backup",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					Name: "test-volume",
				},
				LatestBackup: nil,
			},
			setupMock:          func() {},
			expectedResult:     nil,
			expectedError:      false,
			mockProviderNeeded: false,
		},
		{
			name: "Success - Backup missing attributes",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					Name: "test-volume",
				},
				LatestBackup: &datamodel.Backup{
					Name:       "test-backup",
					Attributes: nil,
				},
			},
			setupMock:          func() {},
			expectedResult:     nil,
			expectedError:      false,
			mockProviderNeeded: false,
		},
		{
			name: "Success - Backup missing ObjectStoreUUID",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					Name: "test-volume",
				},
				LatestBackup: &datamodel.Backup{
					Name: "test-backup",
					Attributes: &datamodel.BackupAttributes{
						ObjectStoreUUID: "",
						EndpointUUID:    "endpoint-uuid",
					},
				},
			},
			setupMock:          func() {},
			expectedResult:     nil,
			expectedError:      false,
			mockProviderNeeded: false,
		},
		{
			name: "Success - Backup missing EndpointUUID",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					Name: "test-volume",
				},
				LatestBackup: &datamodel.Backup{
					Name: "test-backup",
					Attributes: &datamodel.BackupAttributes{
						ObjectStoreUUID: "obj-store-uuid",
						EndpointUUID:    "",
					},
				},
			},
			setupMock:          func() {},
			expectedResult:     nil,
			expectedError:      false,
			mockProviderNeeded: false,
		},
		{
			name: "Error - getObjectStoreEndpointInfo fails",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					Name:   "test-volume",
					PoolID: 1,
					Pool: &datamodel.Pool{
						DeploymentName: "test-deployment",
						PoolCredentials: &datamodel.PoolCredentials{
							Password:      "test-pass",
							SecretID:      "test-secret",
							CertificateID: "test-cert",
							AuthType:      1,
						},
					},
				},
				LatestBackup: &datamodel.Backup{
					Name: "test-backup",
					Attributes: &datamodel.BackupAttributes{
						ObjectStoreUUID: "obj-store-uuid",
						EndpointUUID:    "endpoint-uuid",
					},
				},
			},
			setupMock: func() {
				s.mockStorage.On("GetNodesByPoolID", s.ctx, int64(1)).Return(nil, errors.New("database error"))
			},
			expectedResult: nil,
			expectedError:  true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			// Reset mocks for each test
			s.SetupTest()

			// Setup
			tt.setupMock()

			// Mock vsa.GetProviderByNode if needed
			var originalGetProviderByNode func(context.Context, *models.Node) (vsa.Provider, error)
			if tt.mockProviderNeeded {
				originalGetProviderByNode = vsa.GetProviderByNode
				vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
					return s.mockProvider, nil
				}
				defer func() {
					vsa.GetProviderByNode = originalGetProviderByNode
				}()
			}

			// Execute
			result, err := s.activity.GetObjectStoreEndpointInfoActivity(s.ctx, tt.volumeBackup)

			// Verify
			if tt.expectedError {
				s.Error(err)
				s.Nil(result)
			} else {
				s.NoError(err)
				s.Equal(tt.expectedResult, result)
			}
		})
	}
}

// Test UpdateBackupAndVolumeActivity
func expectRecalcVolumeChainBytesForSync(mockStorage *database.MockStorage, ctx context.Context, volumeUUID string, sum int64, endpointUUID ...string) {
	mockStorage.On("SumVolumeBackupChainBytes", ctx, volumeUUID).Return(sum, nil).Once()
	mockStorage.On("GetExpertModeVolumeByExternalUUID", ctx, volumeUUID).Return(nil, errors.NewNotFoundErr("expert mode volume", nil)).Once()
	recalcVol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volumeUUID}, DataProtection: &datamodel.DataProtection{}}
	mockStorage.On("GetVolume", ctx, volumeUUID).Return(recalcVol, nil).Once()
	mockStorage.On("UpdateVolumeFields", ctx, volumeUUID, mock.MatchedBy(func(updates map[string]interface{}) bool {
		dp, ok := updates["data_protection"].(*datamodel.DataProtection)
		return ok && dp.BackupChainBytes != nil && *dp.BackupChainBytes == sum
	})).Return(nil).Once()
	if len(endpointUUID) > 0 && endpointUUID[0] != "" {
		mockStorage.On("UpdateBackupChainHistory", ctx, volumeUUID, endpointUUID[0], sum).Return(nil).Once()
	} else {
		mockStorage.On("UpdateBackupChainHistory", ctx, volumeUUID, "", sum).Return(nil).Once()
	}
}

func (s *VolumeBackupSyncActivityUnitTestSuite) TestUpdateBackupAndVolumeActivity() {
	tests := []struct {
		name          string
		volumeBackup  *datamodel.VolumeLatestBackup
		logicalSize   int64
		setupMock     func()
		expectedError bool
		errorContains string
	}{
		{
			name: "Success - Update backup and volume",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
					Name:      "test-volume",
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: nillable.ToPointer(int64(512 * 1024 * 1024)), // 512MB
					},
				},
				LatestBackup: &datamodel.Backup{
					BaseModel:  datamodel.BaseModel{UUID: "backup-uuid"},
					Name:       "test-backup",
					VolumeUUID: "volume-uuid",
				},
			},
			logicalSize: int64(1024 * 1024 * 1024), // 1GB
			setupMock: func() {
				s.mockStorage.On("UpdateBackupFields", s.ctx, "backup-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
					return updates["latest_logical_backup_size"] == int64(1024*1024*1024)
				})).Return(nil)
				expectRecalcVolumeChainBytesForSync(s.mockStorage, s.ctx, "volume-uuid", int64(1024*1024*1024))
			},
			expectedError: false,
		},
		{
			name: "Success - No latest backup",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					Name: "test-volume",
				},
				LatestBackup: nil,
			},
			logicalSize:   int64(1024 * 1024 * 1024),
			setupMock:     func() {},
			expectedError: false,
		},
		{
			name: "Error - Backup update fails",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
					Name:      "test-volume",
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: nillable.ToPointer(int64(512 * 1024 * 1024)),
					},
				},
				LatestBackup: &datamodel.Backup{
					BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
					Name:      "test-backup",
				},
			},
			logicalSize: int64(1024 * 1024 * 1024),
			setupMock: func() {
				s.mockStorage.On("UpdateBackupFields", s.ctx, "backup-uuid", mock.Anything).Return(
					vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, errors.New("backup update failed")))
			},
			expectedError: true,
		},
		{
			name: "Error - Volume update fails",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
					Name:      "test-volume",
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: nillable.ToPointer(int64(512 * 1024 * 1024)),
					},
				},
				LatestBackup: &datamodel.Backup{
					BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
					Name:      "test-backup",
				},
			},
			logicalSize: int64(1024 * 1024 * 1024),
			setupMock: func() {
				s.mockStorage.On("UpdateBackupFields", s.ctx, "backup-uuid", mock.Anything).Return(nil)
				s.mockStorage.On("SumVolumeBackupChainBytes", s.ctx, "volume-uuid").Return(int64(1024*1024*1024), nil)
				s.mockStorage.On("GetExpertModeVolumeByExternalUUID", s.ctx, "volume-uuid").Return(nil, errors.NewNotFoundErr("expert mode volume", nil))
				recalcVol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "volume-uuid"}, DataProtection: &datamodel.DataProtection{}}
				s.mockStorage.On("GetVolume", s.ctx, "volume-uuid").Return(recalcVol, nil)
				s.mockStorage.On("UpdateVolumeFields", s.ctx, "volume-uuid", mock.Anything).Return(gorm.ErrInvalidTransaction)
			},
			expectedError: true,
		},
		{
			name: "Success - Zero logical size",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
					Name:      "test-volume",
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: nillable.ToPointer(int64(0)),
					},
				},
				LatestBackup: &datamodel.Backup{
					BaseModel:  datamodel.BaseModel{UUID: "backup-uuid"},
					Name:       "test-backup",
					VolumeUUID: "volume-uuid",
				},
			},
			logicalSize: int64(0),
			setupMock: func() {
				s.mockStorage.On("UpdateBackupFields", s.ctx, "backup-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
					return updates["latest_logical_backup_size"] == int64(0)
				})).Return(nil)
				expectRecalcVolumeChainBytesForSync(s.mockStorage, s.ctx, "volume-uuid", int64(0))
			},
			expectedError: false,
		},
		{
			name: "Success - Large logical size (TB scale)",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
					Name:      "test-volume",
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: nillable.ToPointer(int64(500 * 1024 * 1024 * 1024)), // 500GB
					},
				},
				LatestBackup: &datamodel.Backup{
					BaseModel:  datamodel.BaseModel{UUID: "backup-uuid"},
					Name:       "test-backup",
					VolumeUUID: "volume-uuid",
				},
			},
			logicalSize: int64(2 * 1024 * 1024 * 1024 * 1024), // 2TB
			setupMock: func() {
				s.mockStorage.On("UpdateBackupFields", s.ctx, "backup-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
					return updates["latest_logical_backup_size"] == int64(2*1024*1024*1024*1024)
				})).Return(nil)
				expectRecalcVolumeChainBytesForSync(s.mockStorage, s.ctx, "volume-uuid", int64(2*1024*1024*1024*1024))
			},
			expectedError: false,
		},
		{
			name: "Success - Expert mode backup updates expert mode volume",
			volumeBackup: &datamodel.VolumeLatestBackup{
				ExpertModeVolume: &datamodel.ExpertModeVolumes{
					BaseModel:    datamodel.BaseModel{ID: 10},
					Name:         "expert-volume",
					ExternalUUID: "external-volume-uuid",
					BackupConfig: &datamodel.DataProtection{
						BackupChainBytes: nillable.ToPointer(int64(256 * 1024 * 1024)),
					},
				},
				LatestBackup: &datamodel.Backup{
					BaseModel:  datamodel.BaseModel{UUID: "expert-backup-uuid"},
					Name:       "expert-backup",
					VolumeUUID: "external-volume-uuid",
					Attributes: &datamodel.BackupAttributes{IsExpertModeBackup: true},
				},
			},
			logicalSize: int64(1024 * 1024 * 1024),
			setupMock: func() {
				s.mockStorage.On("UpdateBackupFields", s.ctx, "expert-backup-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
					return updates["latest_logical_backup_size"] == int64(1024*1024*1024)
				})).Return(nil)
				s.mockStorage.On("SumVolumeBackupChainBytes", s.ctx, "external-volume-uuid").Return(int64(1024*1024*1024), nil)
				expertVol := &datamodel.ExpertModeVolumes{
					BaseModel:    datamodel.BaseModel{ID: 10},
					ExternalUUID: "external-volume-uuid",
					BackupConfig: &datamodel.DataProtection{
						BackupChainBytes: nillable.ToPointer(int64(256 * 1024 * 1024)),
					},
				}
				s.mockStorage.On("GetExpertModeVolumeByExternalUUID", s.ctx, "external-volume-uuid").Return(expertVol, nil)
				s.mockStorage.On("UpdateExpertModeVolumeFields", s.ctx, "external-volume-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
					dp, ok := updates["data_protection"].(*datamodel.DataProtection)
					return ok && dp.BackupChainBytes != nil && *dp.BackupChainBytes == int64(1024*1024*1024)
				})).Return(nil)
				s.mockStorage.On("UpdateBackupChainHistory", s.ctx, "external-volume-uuid", "", int64(1024*1024*1024)).Return(nil)
			},
			expectedError: false,
		},
		{
			name: "Success - UpdateBackupChainHistory error is swallowed (does not fail activity)",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
					Name:      "test-volume",
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: nillable.ToPointer(int64(512 * 1024 * 1024)),
					},
				},
				LatestBackup: &datamodel.Backup{
					BaseModel:  datamodel.BaseModel{UUID: "backup-uuid"},
					Name:       "test-backup",
					VolumeUUID: "volume-uuid",
				},
			},
			logicalSize: int64(1024 * 1024 * 1024),
			setupMock: func() {
				logicalSize := int64(1024 * 1024 * 1024)
				s.mockStorage.On("UpdateBackupFields", s.ctx, "backup-uuid", mock.Anything).Return(nil)
				s.mockStorage.On("SumVolumeBackupChainBytes", s.ctx, "volume-uuid").Return(logicalSize, nil)
				s.mockStorage.On("GetExpertModeVolumeByExternalUUID", s.ctx, "volume-uuid").Return(nil, errors.NewNotFoundErr("expert mode volume", nil))
				recalcVol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "volume-uuid"}, DataProtection: &datamodel.DataProtection{}}
				s.mockStorage.On("GetVolume", s.ctx, "volume-uuid").Return(recalcVol, nil)
				s.mockStorage.On("UpdateVolumeFields", s.ctx, "volume-uuid", mock.Anything).Return(nil)
				// Force ledger update to fail; the activity should still succeed because the error is swallowed.
				s.mockStorage.On("UpdateBackupChainHistory", s.ctx, "volume-uuid", "", logicalSize).Return(
					errors.New("simulated chain history update failure"))
			},
			expectedError: false,
		},
		{
			name: "Success - Expert mode backup with nil BackupConfig initialises it",
			volumeBackup: &datamodel.VolumeLatestBackup{
				ExpertModeVolume: &datamodel.ExpertModeVolumes{
					BaseModel:    datamodel.BaseModel{ID: 11},
					Name:         "expert-volume-2",
					ExternalUUID: "external-uuid-2",
					BackupConfig: nil,
				},
				LatestBackup: &datamodel.Backup{
					BaseModel:  datamodel.BaseModel{UUID: "expert-backup-uuid-2"},
					Name:       "expert-backup-2",
					VolumeUUID: "external-uuid-2",
					Attributes: &datamodel.BackupAttributes{IsExpertModeBackup: true},
				},
			},
			logicalSize: int64(512 * 1024 * 1024),
			setupMock: func() {
				s.mockStorage.On("UpdateBackupFields", s.ctx, "expert-backup-uuid-2", mock.Anything).Return(nil)
				s.mockStorage.On("SumVolumeBackupChainBytes", s.ctx, "external-uuid-2").Return(int64(512*1024*1024), nil)
				expertVol := &datamodel.ExpertModeVolumes{
					BaseModel:    datamodel.BaseModel{ID: 11},
					ExternalUUID: "external-uuid-2",
					BackupConfig: nil,
				}
				s.mockStorage.On("GetExpertModeVolumeByExternalUUID", s.ctx, "external-uuid-2").Return(expertVol, nil)
				s.mockStorage.On("UpdateExpertModeVolumeFields", s.ctx, "external-uuid-2", mock.MatchedBy(func(updates map[string]interface{}) bool {
					dp, ok := updates["data_protection"].(*datamodel.DataProtection)
					return ok && dp.BackupChainBytes != nil && *dp.BackupChainBytes == int64(512*1024*1024)
				})).Return(nil)
				s.mockStorage.On("UpdateBackupChainHistory", s.ctx, "external-uuid-2", "", int64(512*1024*1024)).Return(nil)
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			// Reset mocks for each test
			s.SetupTest()

			// Setup
			tt.setupMock()

			// Execute
			err := s.activity.UpdateBackupAndVolumeActivity(s.ctx, tt.volumeBackup, tt.logicalSize)

			// Verify
			if tt.expectedError {
				s.Error(err)
			} else {
				s.NoError(err)
			}
		})
	}
}

// TestUpdateBackupAndVolumeActivity_EndpointScoping verifies that UpdateBackupChainHistory
// is always called with the endpointUUID from Attributes, regardless of vault type.
func (s *VolumeBackupSyncActivityUnitTestSuite) TestUpdateBackupAndVolumeActivity_EndpointScoping() {
	tests := []struct {
		name             string
		volumeBackup     *datamodel.VolumeLatestBackup
		logicalSize      int64
		wantEndpointUUID string
	}{
		{
			name: "GCBDR_CrossProject_PassesEndpointUUID",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					BaseModel: datamodel.BaseModel{UUID: "vol-gcbdr-ep"},
					Name:      "vol-gcbdr-ep",
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: nillable.ToPointer(int64(0)),
					},
				},
				LatestBackup: &datamodel.Backup{
					BaseModel: datamodel.BaseModel{UUID: "bkp-gcbdr-ep"},
					BackupVault: &datamodel.BackupVault{
						ServiceType: datamodel.ServiceTypeCrossProject,
					},
					Attributes: &datamodel.BackupAttributes{
						EndpointUUID: "ep-gcbdr-123",
					},
				},
			},
			logicalSize:      int64(1024),
			wantEndpointUUID: "ep-gcbdr-123",
		},
		{
			name: "GCNV_PassesEndpointUUIDFromAttributes",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					BaseModel: datamodel.BaseModel{UUID: "vol-gcnv-ep"},
					Name:      "vol-gcnv-ep",
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: nillable.ToPointer(int64(0)),
					},
				},
				LatestBackup: &datamodel.Backup{
					BaseModel: datamodel.BaseModel{UUID: "bkp-gcnv-ep"},
					BackupVault: &datamodel.BackupVault{
						ServiceType: datamodel.ServiceTypeGCNV,
					},
					Attributes: &datamodel.BackupAttributes{
						EndpointUUID: "ep-gcnv-456",
					},
				},
			},
			logicalSize:      int64(512),
			wantEndpointUUID: "ep-gcnv-456",
		},
		{
			name: "NilAttributes_PassesEmptyEndpointUUID",
			volumeBackup: &datamodel.VolumeLatestBackup{
				Volume: &datamodel.Volume{
					BaseModel: datamodel.BaseModel{UUID: "vol-nil-attrs"},
					Name:      "vol-nil-attrs",
					DataProtection: &datamodel.DataProtection{
						BackupChainBytes: nillable.ToPointer(int64(0)),
					},
				},
				LatestBackup: &datamodel.Backup{
					BaseModel:  datamodel.BaseModel{UUID: "bkp-nil-attrs"},
					Attributes: nil,
				},
			},
			logicalSize:      int64(256),
			wantEndpointUUID: "",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()

			s.mockStorage.On("UpdateBackupFields", s.ctx, tt.volumeBackup.LatestBackup.UUID, mock.Anything).Return(nil)
			expectRecalcVolumeChainBytesForSync(s.mockStorage, s.ctx, tt.volumeBackup.Volume.UUID, tt.logicalSize, tt.wantEndpointUUID)

			err := s.activity.UpdateBackupAndVolumeActivity(s.ctx, tt.volumeBackup, tt.logicalSize)

			s.NoError(err)
			s.mockStorage.AssertCalled(s.T(), "UpdateBackupChainHistory",
				s.ctx, tt.volumeBackup.Volume.UUID, tt.wantEndpointUUID, tt.logicalSize)
		})
	}
}

// Standard unit tests (for individual functions)
func TestVolumeBackupSyncActivity_GetVolumeLatestBackupMapActivity(t *testing.T) {
	tests := []struct {
		name           string
		mockSetup      func(*database.MockStorage)
		expectedResult map[int64]*datamodel.VolumeLatestBackup
		expectedError  bool
		errorContains  string
	}{
		{
			name: "Success - Returns volume backup map",
			mockSetup: func(mockStorage *database.MockStorage) {
				volumeBackupMap := map[int64]*datamodel.VolumeLatestBackup{
					1: {
						Volume: &datamodel.Volume{
							BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
							Name:      "test-volume-1",
						},
						LatestBackup: &datamodel.Backup{
							BaseModel: datamodel.BaseModel{UUID: "backup-uuid-1"},
							Name:      "test-backup-1",
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
						},
					},
				}
				mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(volumeBackupMap, nil)
			},
			expectedResult: map[int64]*datamodel.VolumeLatestBackup{
				1: {
					Volume: &datamodel.Volume{
						BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid-1"},
						Name:      "test-volume-1",
					},
					LatestBackup: &datamodel.Backup{
						BaseModel: datamodel.BaseModel{UUID: "backup-uuid-1"},
						Name:      "test-backup-1",
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
					},
				},
			},
			expectedError: false,
		},
		{
			name: "Success - Empty result",
			mockSetup: func(mockStorage *database.MockStorage) {
				mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(map[int64]*datamodel.VolumeLatestBackup{}, nil)
			},
			expectedResult: map[int64]*datamodel.VolumeLatestBackup{},
			expectedError:  false,
		},
		{
			name: "Error - Database error",
			mockSetup: func(mockStorage *database.MockStorage) {
				mockStorage.On("GetVolumeLatestBackupMap", mock.Anything).Return(nil, errors.New("database error"))
			},
			expectedResult: nil,
			expectedError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			mockStorage := database.NewMockStorage(t)
			activity := &VolumeBackupSyncActivity{SE: mockStorage}
			ctx := context.Background()

			tt.mockSetup(mockStorage)

			// Execute
			result, err := activity.GetVolumeLatestBackupMapActivity(ctx)

			// Verify
			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}

			mockStorage.AssertExpectations(t)
		})
	}
}

// gcbdrLatestBackupEntry builds a VolumeLatestBackup whose latest backup belongs to a vault of the
// given service type / endpoint, with the given id and stored (old) logical size.
func gcbdrLatestBackupEntry(serviceType, endpointUUID string, id, oldSize int64) *datamodel.VolumeLatestBackup {
	return &datamodel.VolumeLatestBackup{
		LatestBackup: &datamodel.Backup{
			BaseModel:               datamodel.BaseModel{ID: id, UUID: "latest-backup-uuid"},
			Name:                    "latest-backup",
			VolumeUUID:              "volume-uuid",
			LatestLogicalBackupSize: oldSize,
			Attributes:              &datamodel.BackupAttributes{EndpointUUID: endpointUUID},
			BackupVault:             &datamodel.BackupVault{ServiceType: serviceType},
		},
	}
}

func TestBackPropagateGcbdrBackupChainSizesActivity(t *testing.T) {
	const (
		volumeUUID   = "volume-uuid"
		endpointUUID = "endpoint-uuid"
		latestID     = int64(105)
		pivotID      = int64(103)
	)

	tests := []struct {
		name           string
		volumeBackup   *datamodel.VolumeLatestBackup
		newLogicalSize int64
		setupMock      func(*database.MockStorage)
		expectedError  bool
	}{
		{
			name:           "GCBDR drop with pivot reduces intermediates",
			volumeBackup:   gcbdrLatestBackupEntry(datamodel.ServiceTypeCrossProject, endpointUUID, latestID, 100),
			newLogicalSize: 80,
			setupMock: func(m *database.MockStorage) {
				m.On("GetMostRecentlyDeletedBackupForEndpoint", mock.Anything, volumeUUID, endpointUUID, mock.Anything).
					Return(&datamodel.Backup{BaseModel: datamodel.BaseModel{ID: pivotID}}, nil).Once()
				m.On("DecrementIntermediateBackupChainSizes", mock.Anything, volumeUUID, endpointUUID, pivotID, latestID, int64(20)).
					Return(int64(1), nil).Once()
			},
		},
		{
			name: "Expert-mode GCBDR drop is handled identically",
			volumeBackup: func() *datamodel.VolumeLatestBackup {
				vb := gcbdrLatestBackupEntry(datamodel.ServiceTypeCrossProject, endpointUUID, latestID, 100)
				vb.ExpertModeVolume = &datamodel.ExpertModeVolumes{ExternalUUID: "external-uuid"}
				return vb
			}(),
			newLogicalSize: 60,
			setupMock: func(m *database.MockStorage) {
				m.On("GetMostRecentlyDeletedBackupForEndpoint", mock.Anything, volumeUUID, endpointUUID, mock.Anything).
					Return(&datamodel.Backup{BaseModel: datamodel.BaseModel{ID: pivotID}}, nil).Once()
				m.On("DecrementIntermediateBackupChainSizes", mock.Anything, volumeUUID, endpointUUID, pivotID, latestID, int64(40)).
					Return(int64(2), nil).Once()
			},
		},
		{
			name:           "Non-CrossProject vault is a no-op",
			volumeBackup:   gcbdrLatestBackupEntry(datamodel.ServiceTypeGCNV, endpointUUID, latestID, 100),
			newLogicalSize: 80,
			setupMock:      func(m *database.MockStorage) {},
		},
		{
			name:           "No drop (delta<=0) is a no-op",
			volumeBackup:   gcbdrLatestBackupEntry(datamodel.ServiceTypeCrossProject, endpointUUID, latestID, 80),
			newLogicalSize: 80,
			setupMock:      func(m *database.MockStorage) {},
		},
		{
			name:           "Missing endpoint uuid is a no-op",
			volumeBackup:   gcbdrLatestBackupEntry(datamodel.ServiceTypeCrossProject, "", latestID, 100),
			newLogicalSize: 80,
			setupMock:      func(m *database.MockStorage) {},
		},
		{
			name:           "No recent pivot is a no-op",
			volumeBackup:   gcbdrLatestBackupEntry(datamodel.ServiceTypeCrossProject, endpointUUID, latestID, 100),
			newLogicalSize: 80,
			setupMock: func(m *database.MockStorage) {
				m.On("GetMostRecentlyDeletedBackupForEndpoint", mock.Anything, volumeUUID, endpointUUID, mock.Anything).
					Return(nil, nil).Once()
			},
		},
		{
			name:           "Nil latest backup is a no-op",
			volumeBackup:   &datamodel.VolumeLatestBackup{},
			newLogicalSize: 80,
			setupMock:      func(m *database.MockStorage) {},
		},
		{
			name:           "Nil volume backup is a no-op",
			volumeBackup:   nil,
			newLogicalSize: 80,
			setupMock:      func(m *database.MockStorage) {},
		},
		{
			name:           "Error from pivot lookup is returned",
			volumeBackup:   gcbdrLatestBackupEntry(datamodel.ServiceTypeCrossProject, endpointUUID, latestID, 100),
			newLogicalSize: 80,
			setupMock: func(m *database.MockStorage) {
				m.On("GetMostRecentlyDeletedBackupForEndpoint", mock.Anything, volumeUUID, endpointUUID, mock.Anything).
					Return(nil, errors.New("db error")).Once()
			},
			expectedError: true,
		},
		{
			name:           "Error from decrement is returned",
			volumeBackup:   gcbdrLatestBackupEntry(datamodel.ServiceTypeCrossProject, endpointUUID, latestID, 100),
			newLogicalSize: 80,
			setupMock: func(m *database.MockStorage) {
				m.On("GetMostRecentlyDeletedBackupForEndpoint", mock.Anything, volumeUUID, endpointUUID, mock.Anything).
					Return(&datamodel.Backup{BaseModel: datamodel.BaseModel{ID: pivotID}}, nil).Once()
				m.On("DecrementIntermediateBackupChainSizes", mock.Anything, volumeUUID, endpointUUID, pivotID, latestID, int64(20)).
					Return(int64(0), errors.New("db error")).Once()
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStorage := database.NewMockStorage(t)
			activity := &VolumeBackupSyncActivity{SE: mockStorage}
			ctx := context.Background()

			tt.setupMock(mockStorage)

			err := activity.BackPropagateGcbdrBackupChainSizesActivity(ctx, tt.volumeBackup, tt.newLogicalSize)

			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			mockStorage.AssertExpectations(t)
		})
	}
}
