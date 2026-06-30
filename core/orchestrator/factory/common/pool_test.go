package common

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"gorm.io/gorm"
)

func TestCreatePoolJob(t *testing.T) {
	tests := []struct {
		name          string
		params        *commonparams.CreatePoolParams
		account       *datamodel.Account
		dbPool        *datamodel.Pool
		correlationID string
		requestID     string
		expectedType  string
	}{
		{
			name: "Standard pool job creation",
			params: &commonparams.CreatePoolParams{
				Name:          "test-pool",
				LargeCapacity: false,
			},
			account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
			},
			dbPool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
			},
			correlationID: "test-correlation-id",
			requestID:     "test-request-id",
			expectedType:  "CREATE_POOL",
		},
		{
			name: "Large capacity pool job creation",
			params: &commonparams.CreatePoolParams{
				Name:          "test-large-pool",
				LargeCapacity: true,
			},
			account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 2},
			},
			dbPool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "test-large-uuid"},
			},
			correlationID: "test-correlation-id-2",
			requestID:     "test-request-id-2",
			expectedType:  "CREATE_LARGE_POOL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = context.WithValue(ctx, middleware.TemporalSLoggerKey, log.Fields{
				string(middleware.RequestID):            tt.requestID,
				string(middleware.RequestCorrelationID): tt.correlationID,
			})

			job := CreatePoolJob(ctx, tt.params, tt.account, tt.dbPool)

			assert.NotNil(t, job)
			assert.Equal(t, tt.expectedType, job.Type)
			assert.Equal(t, string(datamodel.JobsStateNEW), job.State)
			assert.Equal(t, tt.params.Name, job.ResourceName)
			assert.Equal(t, sql.NullInt64{Int64: tt.account.ID, Valid: true}, job.AccountID)
			assert.Equal(t, tt.correlationID, job.CorrelationID)
			assert.Equal(t, tt.requestID, job.RequestID)
			assert.NotNil(t, job.JobAttributes)
			assert.Equal(t, tt.dbPool.UUID, job.JobAttributes.ResourceUUID)
		})
	}
}

func TestHandleCreatePoolError(t *testing.T) {
	tests := []struct {
		name           string
		job            *datamodel.Job
		err            error
		updateJobError error
		expectError    bool
	}{
		{
			name:           "Successfully updates job to error state",
			job:            &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"}},
			err:            errors.New("test error"),
			updateJobError: nil,
			expectError:    false,
		},
		{
			name:           "Fails to update job state",
			job:            &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"}},
			err:            errors.New("test error"),
			updateJobError: errors.New("database error"),
			expectError:    false, // Function logs error but doesn't return it
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockLogger := log.NewLogger()
			ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

			mockStorage := database.NewMockStorage(t)
			mockStorage.EXPECT().
				UpdateJob(ctx, tt.job.UUID, string(datamodel.JobsStateERROR), 0, tt.err.Error()).
				Return(tt.updateJobError)

			HandleCreatePoolError(ctx, mockStorage, tt.job, tt.err)
		})
	}
}

func TestCleanupPoolOnError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		dbPool       *datamodel.Pool
		deleteError  error
		shouldDelete bool
	}{
		{
			name:         "Deletes pool when error exists",
			err:          errors.New("test error"),
			dbPool:       &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"}},
			deleteError:  nil,
			shouldDelete: true,
		},
		{
			name:         "Does not delete when no error",
			err:          nil,
			dbPool:       &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"}},
			deleteError:  nil,
			shouldDelete: false,
		},
		{
			name:         "Does not delete when pool is nil",
			err:          errors.New("test error"),
			dbPool:       nil,
			deleteError:  nil,
			shouldDelete: false,
		},
		{
			name:         "Handles delete error gracefully",
			err:          errors.New("test error"),
			dbPool:       &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"}},
			deleteError:  errors.New("delete failed"),
			shouldDelete: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockLogger := log.NewLogger()
			ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

			mockStorage := database.NewMockStorage(t)
			if tt.shouldDelete {
				mockStorage.EXPECT().
					DeletePool(ctx, tt.dbPool).
					Return(tt.deleteError)
			}

			CleanupPoolOnError(ctx, mockStorage, tt.dbPool, tt.err)
		})
	}
}

func TestConvertDatastorePoolToModel(t *testing.T) {
	now := time.Now()
	deletedAt := &gorm.DeletedAt{Time: now, Valid: true}

	tests := []struct {
		name          string
		pool          *datamodel.PoolView
		accountName   string
		expectedState string
	}{
		{
			name: "Full pool conversion with all fields",
			pool: &datamodel.PoolView{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						UUID:      "test-uuid",
						CreatedAt: now,
						UpdatedAt: now,
						DeletedAt: deletedAt,
					},
					Name:             "test-pool",
					Description:      "test description",
					SizeInBytes:      1024 * 1024 * 1024, // 1 GiB
					State:            datamodel.LifeCycleStateAvailable,
					StateDetails:     "Available",
					AllowAutoTiering: true,
					Network:          "test-network",
					ServiceLevel:     "FLEX",
					QosType:          "auto",
					DeploymentName:   "test-deployment",
					LargeCapacity:    false,
					PoolAttributes: &datamodel.PoolAttributes{
						ThroughputMibps: 64,
						Iops:            1024,
						PrimaryZone:     "us-central1-a",
						SecondaryZone:   "us-central1-b",
						Labels:          &datamodel.JSONB{"env": "test", "team": "backend"},
						IsRegionalHA:    true,
						LdapEnabled:     false,
						IsTrueZonal:     false,
					},
					SatisfyZI:     true,
					SatisfyZS:     false,
					APIAccessMode: "READ_WRITE",
				},
				QuotaInBytes: 512 * 1024 * 1024, // 512 MiB
				VolumeCount:  5,
				Throughput:   32.5,
				Iops:         512,
			},
			accountName:   "test-account",
			expectedState: datamodel.LifeCycleStateAvailable,
		},
		{
			name: "Pool with auto tiering config",
			pool: &datamodel.PoolView{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						UUID:      "test-uuid-2",
						CreatedAt: now,
						UpdatedAt: now,
						DeletedAt: nil,
					},
					Name:        "test-pool-2",
					SizeInBytes: 2048 * 1024 * 1024,
					State:       datamodel.LifeCycleStateAvailable,
					PoolAttributes: &datamodel.PoolAttributes{
						ThroughputMibps: 128,
						Iops:            2048,
					},
					AutoTieringConfig: &datamodel.AutoTieringConfig{
						HotTierSizeInBytes:      512 * 1024 * 1024,
						EnableHotTierAutoResize: true,
						HotTierConsumption:      256 * 1024 * 1024,
						ColdTierConsumption:     128 * 1024 * 1024,
					},
				},
			},
			accountName:   "test-account-2",
			expectedState: datamodel.LifeCycleStateAvailable,
		},
		{
			name: "Pool with KMS config",
			pool: &datamodel.PoolView{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						UUID:      "test-uuid-3",
						CreatedAt: now,
						UpdatedAt: now,
						DeletedAt: nil,
					},
					Name:        "test-pool-3",
					SizeInBytes: 1024 * 1024 * 1024,
					State:       datamodel.LifeCycleStateAvailable,
					PoolAttributes: &datamodel.PoolAttributes{
						ThroughputMibps: 64,
						Iops:            1024,
					},
					KmsConfig: &datamodel.KmsConfig{
						BaseModel: datamodel.BaseModel{
							UUID:      "kms-uuid",
							CreatedAt: now,
							UpdatedAt: now,
							DeletedAt: deletedAt,
						},
						Name:              "test-kms",
						Description:       "test kms description",
						State:             datamodel.LifeCycleStateInUse,
						StateDetails:      "In use",
						KeyRing:           "test-keyring",
						KeyRingLocation:   "us-central1",
						KeyName:           "test-key",
						AccountID:         int64(123456),
						CustomerProjectID: "customer-project",
						KeyProjectID:      "key-project",
						ResourceID:        "kms-resource-id",
					},
				},
			},
			accountName:   "test-account-3",
			expectedState: datamodel.LifeCycleStateAvailable,
		},
		{
			name: "Pool with Active Directory",
			pool: &datamodel.PoolView{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						UUID:      "test-uuid-4",
						CreatedAt: now,
						UpdatedAt: now,
						DeletedAt: nil,
					},
					Name:        "test-pool-4",
					SizeInBytes: 1024 * 1024 * 1024,
					State:       datamodel.LifeCycleStateAvailable,
					PoolAttributes: &datamodel.PoolAttributes{
						ThroughputMibps: 64,
						Iops:            1024,
					},
					ActiveDirectory: &datamodel.ActiveDirectory{
						BaseModel: datamodel.BaseModel{
							UUID: "ad-uuid",
						},
						AdName: "test-ad",
					},
				},
			},
			accountName:   "test-account-4",
			expectedState: datamodel.LifeCycleStateAvailable,
		},
		{
			name: "Pool with nil labels",
			pool: &datamodel.PoolView{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						UUID:      "test-uuid-5",
						CreatedAt: now,
						UpdatedAt: now,
						DeletedAt: nil,
					},
					Name:        "test-pool-5",
					SizeInBytes: 1024 * 1024 * 1024,
					State:       datamodel.LifeCycleStateAvailable,
					PoolAttributes: &datamodel.PoolAttributes{
						ThroughputMibps: 64,
						Iops:            1024,
						Labels:          nil,
					},
				},
			},
			accountName:   "test-account-5",
			expectedState: datamodel.LifeCycleStateAvailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertDatastorePoolToModel(tt.pool, tt.accountName)

			assert.NotNil(t, result)
			assert.Equal(t, tt.pool.UUID, result.UUID)
			assert.Equal(t, tt.pool.CreatedAt, result.CreatedAt)
			assert.Equal(t, tt.pool.UpdatedAt, result.UpdatedAt)
			if tt.pool.DeletedAt != nil {
				assert.Equal(t, &tt.pool.DeletedAt.Time, result.DeletedAt)
			} else {
				assert.Nil(t, result.DeletedAt)
			}
			assert.Equal(t, tt.accountName, result.AccountName)
			assert.Equal(t, tt.pool.Name, result.Name)
			assert.Equal(t, tt.pool.Description, result.Description)
			assert.Equal(t, uint64(tt.pool.SizeInBytes), result.SizeInBytes)
			assert.Equal(t, tt.pool.State, result.State)
			assert.Equal(t, tt.pool.StateDetails, result.StateDetails)
			assert.Equal(t, tt.pool.AllowAutoTiering, result.AllowAutoTiering)
			assert.Equal(t, tt.pool.Network, result.VendorSubNetID)
			assert.Equal(t, tt.pool.ServiceLevel, result.ServiceLevel)
			assert.Equal(t, tt.pool.QosType, result.QosType)
			assert.Equal(t, tt.pool.DeploymentName, result.DeploymentName)
			assert.Equal(t, tt.pool.LargeCapacity, result.LargeCapacity)

			if tt.pool.PoolAttributes != nil {
				assert.NotNil(t, result.PoolAttributes)
				assert.Equal(t, float64(tt.pool.QuotaInBytes), result.PoolAttributes.AllocatedBytes)
				assert.Equal(t, tt.pool.VolumeCount, result.PoolAttributes.NumberOfVolumes)
				assert.Equal(t, tt.pool.PoolAttributes.PrimaryZone, result.PoolAttributes.PrimaryZone)
				assert.Equal(t, tt.pool.PoolAttributes.SecondaryZone, result.PoolAttributes.SecondaryZone)
				assert.Equal(t, tt.pool.PoolAttributes.IsRegionalHA, result.PoolAttributes.IsRegionalHA)
				assert.Equal(t, tt.pool.PoolAttributes.LdapEnabled, result.PoolAttributes.LdapEnabled)
				assert.Equal(t, tt.pool.PoolAttributes.IsTrueZonal, result.PoolAttributes.IsTrueZonal)

				if tt.pool.PoolAttributes.Labels != nil {
					expectedLabels := utils.ConvertJSONBToMap(tt.pool.PoolAttributes.Labels)
					assert.Equal(t, expectedLabels, result.PoolAttributes.Labels)
				} else {
					assert.Empty(t, result.PoolAttributes.Labels)
				}
			}

			if tt.pool.AutoTieringConfig != nil {
				assert.NotNil(t, result.AutoTieringConfig)
				assert.Equal(t, uint64(tt.pool.AutoTieringConfig.HotTierSizeInBytes), result.AutoTieringConfig.HotTierSizeInBytes)
				assert.Equal(t, tt.pool.AutoTieringConfig.EnableHotTierAutoResize, result.AutoTieringConfig.EnableHotTierAutoResize)
				assert.Equal(t, tt.pool.AutoTieringConfig.HotTierConsumption, result.AutoTieringConfig.HotTierConsumption)
				assert.Equal(t, tt.pool.AutoTieringConfig.ColdTierConsumption, result.AutoTieringConfig.ColdTierConsumption)
			}

			if tt.pool.KmsConfig != nil {
				assert.NotNil(t, result.KmsConfig)
				assert.Equal(t, tt.pool.KmsConfig.UUID, result.KmsConfig.UUID)
				assert.Equal(t, tt.pool.KmsConfig.Name, result.KmsConfig.Name)
				assert.Equal(t, tt.pool.KmsConfig.Description, result.KmsConfig.Description)
				assert.Equal(t, tt.pool.KmsConfig.State, result.KmsConfig.State)
				assert.Equal(t, tt.pool.KmsConfig.StateDetails, result.KmsConfig.StateDetails)
				assert.Equal(t, tt.pool.KmsConfig.KeyRing, result.KmsConfig.KeyRing)
				assert.Equal(t, tt.pool.KmsConfig.KeyRingLocation, result.KmsConfig.KeyRingLocation)
				assert.Equal(t, tt.pool.KmsConfig.KeyName, result.KmsConfig.KeyName)
				assert.Equal(t, tt.pool.KmsConfig.AccountID, result.KmsConfig.AccountID)
				assert.Equal(t, tt.pool.KmsConfig.CustomerProjectID, result.KmsConfig.CustomerProjectID)
				assert.Equal(t, tt.pool.KmsConfig.KeyProjectID, result.KmsConfig.KeyProjectID)
				assert.Equal(t, tt.pool.KmsConfig.ResourceID, result.KmsConfig.ResourceID)
			}

			if tt.pool.ActiveDirectory != nil {
				assert.NotNil(t, result.ActiveDirectoryConfigId)
				assert.Equal(t, tt.pool.ActiveDirectory.UUID, result.ActiveDirectoryConfigId)
				assert.Equal(t, tt.pool.ActiveDirectory.AdName, result.ActiveDirectoryResourceId)
			}
		})
	}
}

func TestCheckActiveUpgradeJob(t *testing.T) {
	t.Run("ReturnsNilWhenNoJobs", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(ctx, "cluster-1").Return([]*datamodel.ClusterUpgradeJob{}, nil)

		result, err := CheckActiveUpgradeJob(ctx, mockStorage, "cluster-1")
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("ReturnsNilWhenAllCompleted", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		jobs := []*datamodel.ClusterUpgradeJob{
			{BaseModel: datamodel.BaseModel{UUID: "job-1"}, Status: string(models.UpgradeStatusCompleted)},
			{BaseModel: datamodel.BaseModel{UUID: "job-2"}, Status: string(models.UpgradeStatusFailed)},
		}
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(ctx, "cluster-1").Return(jobs, nil)

		result, err := CheckActiveUpgradeJob(ctx, mockStorage, "cluster-1")
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("ReturnsPendingJob", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		jobs := []*datamodel.ClusterUpgradeJob{
			{BaseModel: datamodel.BaseModel{UUID: "job-1"}, Status: string(models.UpgradeStatusCompleted)},
			{BaseModel: datamodel.BaseModel{UUID: "job-2"}, Status: string(models.UpgradeStatusPending)},
		}
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(ctx, "cluster-1").Return(jobs, nil)

		result, err := CheckActiveUpgradeJob(ctx, mockStorage, "cluster-1")
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "job-2", result.UUID)
	})

	t.Run("ReturnsInProgressJob", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		jobs := []*datamodel.ClusterUpgradeJob{
			{BaseModel: datamodel.BaseModel{UUID: "job-1"}, Status: string(models.UpgradeStatusInProgress)},
		}
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(ctx, "cluster-1").Return(jobs, nil)

		result, err := CheckActiveUpgradeJob(ctx, mockStorage, "cluster-1")
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "job-1", result.UUID)
	})

	t.Run("ReturnsErrorOnStorageFailure", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(ctx, "cluster-1").Return(nil, errors.New("db error"))

		result, err := CheckActiveUpgradeJob(ctx, mockStorage, "cluster-1")
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestHasActiveClusterUpgrade(t *testing.T) {
	t.Run("ReturnsFalseWhenNoActiveJob", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(ctx, "cluster-1").Return([]*datamodel.ClusterUpgradeJob{}, nil)

		has, err := HasActiveClusterUpgrade(ctx, mockStorage, "cluster-1")
		assert.NoError(t, err)
		assert.False(t, has)
	})

	t.Run("ReturnsTrueWhenActiveJobExists", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		jobs := []*datamodel.ClusterUpgradeJob{
			{BaseModel: datamodel.BaseModel{UUID: "job-1"}, Status: string(models.UpgradeStatusInProgress)},
		}
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(ctx, "cluster-1").Return(jobs, nil)

		has, err := HasActiveClusterUpgrade(ctx, mockStorage, "cluster-1")
		assert.NoError(t, err)
		assert.True(t, has)
	})
}

func TestUpdateUpgradeJobStatus(t *testing.T) {
	t.Run("SetsErrorDetailsWhenMessageProvided", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		upgradeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "job-1"},
			Status:    string(models.UpgradeStatusPending),
		}
		mockStorage.EXPECT().GetClusterUpgradeJobByUUID(ctx, "job-1").Return(upgradeJob, nil)
		mockStorage.EXPECT().UpdateClusterUpgradeJob(ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(nil)

		err := UpdateUpgradeJobStatus(ctx, mockStorage, "job-1", string(models.UpgradeStatusFailed), "workflow crashed")
		assert.NoError(t, err)
		assert.Equal(t, string(models.UpgradeStatusFailed), upgradeJob.Status)
		assert.NotNil(t, upgradeJob.ErrorDetails)
		assert.Equal(t, "workflow crashed", upgradeJob.ErrorDetails.ErrorMessage)
	})

	t.Run("SetsCompletedAtForCompletedStatus", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		upgradeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "job-1"},
			Status:    string(models.UpgradeStatusInProgress),
		}
		mockStorage.EXPECT().GetClusterUpgradeJobByUUID(ctx, "job-1").Return(upgradeJob, nil)
		mockStorage.EXPECT().UpdateClusterUpgradeJob(ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(nil)

		err := UpdateUpgradeJobStatus(ctx, mockStorage, "job-1", string(models.UpgradeStatusCompleted), "")
		assert.NoError(t, err)
		assert.Equal(t, string(models.UpgradeStatusCompleted), upgradeJob.Status)
		assert.NotNil(t, upgradeJob.CompletedAt)
		assert.Nil(t, upgradeJob.ErrorDetails)
	})

	t.Run("SetsStartedAtForInProgressStatus", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		upgradeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "job-1"},
			Status:    string(models.UpgradeStatusPending),
		}
		mockStorage.EXPECT().GetClusterUpgradeJobByUUID(ctx, "job-1").Return(upgradeJob, nil)
		mockStorage.EXPECT().UpdateClusterUpgradeJob(ctx, mock.AnythingOfType("*datamodel.ClusterUpgradeJob")).Return(nil)

		err := UpdateUpgradeJobStatus(ctx, mockStorage, "job-1", string(models.UpgradeStatusInProgress), "")
		assert.NoError(t, err)
		assert.Equal(t, string(models.UpgradeStatusInProgress), upgradeJob.Status)
		assert.NotNil(t, upgradeJob.StartedAt)
	})

	t.Run("ReturnsErrorWhenJobNotFound", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		mockStorage.EXPECT().GetClusterUpgradeJobByUUID(ctx, "missing").Return(nil, gorm.ErrRecordNotFound)

		err := UpdateUpgradeJobStatus(ctx, mockStorage, "missing", string(models.UpgradeStatusFailed), "")
		assert.Error(t, err)
	})
}

func TestConvertMetadataToJSONB(t *testing.T) {
	t.Run("ReturnsNilForNilInput", func(t *testing.T) {
		result := ConvertMetadataToJSONB(nil)
		assert.Nil(t, result)
	})

	t.Run("ReturnsEmptyJSONBForEmptyMap", func(t *testing.T) {
		result := ConvertMetadataToJSONB(map[string]string{})
		assert.NotNil(t, result)
		assert.Empty(t, *result)
	})

	t.Run("ConvertsEntries", func(t *testing.T) {
		input := map[string]string{"env": "staging", "team": "storage"}
		result := ConvertMetadataToJSONB(input)
		assert.NotNil(t, result)
		assert.Len(t, *result, 2)
		assert.Equal(t, "staging", (*result)["env"])
		assert.Equal(t, "storage", (*result)["team"])
	})
}
