package expertmodeactivities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

type RBACActivityTestSuite struct {
	suite.Suite
	mockStorage *database.MockStorage
	activity    *RBACUpdateActivity
	ctx         context.Context
}

func TestRBACActivityTestSuite(t *testing.T) {
	suite.Run(t, new(RBACActivityTestSuite))
}

func (s *RBACActivityTestSuite) SetupTest() {
	s.mockStorage = database.NewMockStorage(s.T())
	s.activity = &RBACUpdateActivity{SE: s.mockStorage}
	mockLogger := log.NewLogger()
	s.ctx = context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)
}

func (s *RBACActivityTestSuite) TearDownTest() {
	s.mockStorage.AssertExpectations(s.T())
}

func (s *RBACActivityTestSuite) TestListActiveExpertModePools_Success() {
	expectedPools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
			Name:      "pool-1",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
			},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"},
			Name:      "pool-2",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
			},
		},
	}

	s.mockStorage.On("ListExpertModePools", s.ctx).Return(expectedPools, nil).Once()

	result, err := s.activity.ListActiveExpertModePools(s.ctx)

	assert.NoError(s.T(), err)
	assert.Equal(s.T(), expectedPools, result)
	assert.Len(s.T(), result, 2)
}

func (s *RBACActivityTestSuite) TestListActiveExpertModePools_StorageNil() {
	activity := &RBACUpdateActivity{SE: nil}

	result, err := activity.ListActiveExpertModePools(s.ctx)

	assert.Error(s.T(), err)
	assert.Nil(s.T(), result)
	assertTemporalApplicationError(s.T(), err, "storage is not configured for RBAC activity", vsaerrors.CustomErrorType, false)
}

func (s *RBACActivityTestSuite) TestListActiveExpertModePools_StorageError() {
	s.mockStorage.On("ListExpertModePools", s.ctx).Return(nil, errors.New("database error")).Once()

	result, err := s.activity.ListActiveExpertModePools(s.ctx)

	assert.Error(s.T(), err)
	assert.Nil(s.T(), result)
	assertTemporalApplicationError(s.T(), err, "database error", vsaerrors.CustomErrorType, false)
}

func (s *RBACActivityTestSuite) TestGetPoolsDetailsByOntapVersion_Success() {
	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
			Name:      "pool-1",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: "hash-1",
			},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"},
			Name:      "pool-2",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: "hash-2",
			},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-3"},
			Name:      "pool-3",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.19.1",
				RbacFileHash: "hash-3",
			},
		},
	}

	result, err := s.activity.GetPoolsDetailsByOntapVersion(s.ctx, pools)

	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), result)
	assert.Len(s.T(), result, 2) // Two ONTAP versions

	// Check 9.18.1 pools
	assert.Len(s.T(), result["9.18.1"], 2)
	assert.Equal(s.T(), "pool-uuid-1", result["9.18.1"][0].PoolUUID)
	assert.Equal(s.T(), "hash-1", result["9.18.1"][0].CurrentHash)
	assert.Equal(s.T(), "pool-uuid-2", result["9.18.1"][1].PoolUUID)
	assert.Equal(s.T(), "hash-2", result["9.18.1"][1].CurrentHash)

	// Check 9.19.1 pools
	assert.Len(s.T(), result["9.19.1"], 1)
	assert.Equal(s.T(), "pool-uuid-3", result["9.19.1"][0].PoolUUID)
	assert.Equal(s.T(), "hash-3", result["9.19.1"][0].CurrentHash)
}

func (s *RBACActivityTestSuite) TestGetPoolsDetailsByOntapVersion_EmptyPools() {
	result, err := s.activity.GetPoolsDetailsByOntapVersion(s.ctx, []*datamodel.Pool{})

	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), result)
	assert.Empty(s.T(), result)
}

func (s *RBACActivityTestSuite) TestGetPoolsDetailsByOntapVersion_PoolWithoutOntapVersion() {
	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
			Name:      "pool-1",
			BuildInfo: nil, // No BuildInfo
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"},
			Name:      "pool-2",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "", // Empty ONTAP version
			},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-3"},
			Name:      "pool-3",
			BuildInfo: &datamodel.PoolBuildInfo{
				OntapVersion: "9.18.1",
				RbacFileHash: "hash-3",
			},
		},
	}

	result, err := s.activity.GetPoolsDetailsByOntapVersion(s.ctx, pools)

	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), result)
	// Only pool-3 should be included
	assert.Len(s.T(), result, 1)
	assert.Len(s.T(), result["9.18.1"], 1)
	assert.Equal(s.T(), "pool-uuid-3", result["9.18.1"][0].PoolUUID)
}

func (s *RBACActivityTestSuite) TestGetPoolsDetailsByOntapVersion_StorageNil() {
	activity := &RBACUpdateActivity{SE: nil}
	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
			Name:      "pool-1",
		},
	}

	result, err := activity.GetPoolsDetailsByOntapVersion(s.ctx, pools)

	assert.Error(s.T(), err)
	assert.Nil(s.T(), result)
	assertTemporalApplicationError(s.T(), err, "storage is not configured for RBAC activity", vsaerrors.CustomErrorType, false)
}

func (s *RBACActivityTestSuite) TestGetLatestRbacHashForAllOntapVersion_Success() {
	originalGetGCPService := hyperscaler2.GetGCPService
	originalGetBucketFile := activities.GetBucketFile
	defer func() {
		hyperscaler2.GetGCPService = originalGetGCPService
		activities.GetBucketFile = originalGetBucketFile
	}()

	poolDetails := map[string][]PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash-1"},
			{PoolUUID: "pool-uuid-2", CurrentHash: "old-hash-2"},
		},
		"9.19.1": {
			{PoolUUID: "pool-uuid-3", CurrentHash: "old-hash-3"},
		},
	}

	mockGCPService := &google.GcpServices{}
	hyperscaler2.GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		return mockGCPService, nil
	}

	activities.GetBucketFile = func(service hyperscaler2.Services, ctx context.Context, bucketName, fileName string) (*hyperscalermodels.BucketFileDetails, error) {
		// Return different hashes for different versions
		if fileName == "GCNV/9.18.1/RBAC/gcnvadmin_create_cli" {
			return &hyperscalermodels.BucketFileDetails{
				BucketName:     bucketName,
				FileUrl:        fileName,
				FileHashSHA256: "new-hash-9.18.1",
			}, nil
		}
		if fileName == "GCNV/9.19.1/RBAC/gcnvadmin_create_cli" {
			return &hyperscalermodels.BucketFileDetails{
				BucketName:     bucketName,
				FileUrl:        fileName,
				FileHashSHA256: "new-hash-9.19.1",
			}, nil
		}
		return nil, errors.New("file not found")
	}

	result, err := s.activity.GetLatestRbacHashForAllOntapVersion(s.ctx, poolDetails)

	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), result)
	assert.Len(s.T(), result, 3) // All pools need update

	// Check pool-uuid-1
	found1 := false
	for _, r := range result {
		if r.PoolUUID == "pool-uuid-1" {
			found1 = true
			assert.Equal(s.T(), "old-hash-1", r.CurrentHash)
			assert.Equal(s.T(), "new-hash-9.18.1", r.LatestRbacHash)
			assert.True(s.T(), r.NeedUpdate)
		}
	}
	assert.True(s.T(), found1)

	// Check pool-uuid-3
	found3 := false
	for _, r := range result {
		if r.PoolUUID == "pool-uuid-3" {
			found3 = true
			assert.Equal(s.T(), "old-hash-3", r.CurrentHash)
			assert.Equal(s.T(), "new-hash-9.19.1", r.LatestRbacHash)
			assert.True(s.T(), r.NeedUpdate)
		}
	}
	assert.True(s.T(), found3)
}

func (s *RBACActivityTestSuite) TestGetLatestRbacHashForAllOntapVersion_NoUpdateNeeded() {
	originalGetGCPService := hyperscaler2.GetGCPService
	originalGetBucketFile := activities.GetBucketFile
	defer func() {
		hyperscaler2.GetGCPService = originalGetGCPService
		activities.GetBucketFile = originalGetBucketFile
	}()

	poolDetails := map[string][]PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "current-hash"},
		},
	}

	mockGCPService := &google.GcpServices{}
	hyperscaler2.GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		return mockGCPService, nil
	}

	activities.GetBucketFile = func(service hyperscaler2.Services, ctx context.Context, bucketName, fileName string) (*hyperscalermodels.BucketFileDetails, error) {
		return &hyperscalermodels.BucketFileDetails{
			BucketName:     bucketName,
			FileUrl:        fileName,
			FileHashSHA256: "current-hash", // Same as current hash
		}, nil
	}

	result, err := s.activity.GetLatestRbacHashForAllOntapVersion(s.ctx, poolDetails)

	assert.NoError(s.T(), err)
	// When hashes match, no pools are added to result (empty slice)
	// The function initializes result as nil, but returns empty slice when no updates needed
	if result == nil {
		// If nil, that's also acceptable - means no updates needed
		result = []PoolDetailsWithRbacHash{}
	}
	assert.Empty(s.T(), result) // No pools need update
}

func (s *RBACActivityTestSuite) TestGetLatestRbacHashForAllOntapVersion_GetGCPServiceFails() {
	originalGetGCPService := hyperscaler2.GetGCPService
	defer func() {
		hyperscaler2.GetGCPService = originalGetGCPService
	}()

	poolDetails := map[string][]PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash"},
		},
	}

	hyperscaler2.GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		return nil, errors.New("GCP service initialization failed")
	}

	result, err := s.activity.GetLatestRbacHashForAllOntapVersion(s.ctx, poolDetails)

	assert.Error(s.T(), err)
	assert.Nil(s.T(), result)
	// The error is wrapped, so just check it's an error
	assertTemporalApplicationError(s.T(), err, "", "", false)
}

func (s *RBACActivityTestSuite) TestGetLatestRbacHashForAllOntapVersion_GetBucketFileFails() {
	originalGetGCPService := hyperscaler2.GetGCPService
	originalGetBucketFile := activities.GetBucketFile
	defer func() {
		hyperscaler2.GetGCPService = originalGetGCPService
		activities.GetBucketFile = originalGetBucketFile
	}()

	poolDetails := map[string][]PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash"},
		},
	}

	mockGCPService := &google.GcpServices{}
	hyperscaler2.GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		return mockGCPService, nil
	}

	activities.GetBucketFile = func(service hyperscaler2.Services, ctx context.Context, bucketName, fileName string) (*hyperscalermodels.BucketFileDetails, error) {
		return nil, errors.New("bucket file not found")
	}

	result, err := s.activity.GetLatestRbacHashForAllOntapVersion(s.ctx, poolDetails)

	assert.Error(s.T(), err)
	assert.Nil(s.T(), result)
	assertTemporalApplicationError(s.T(), err, "bucket file not found", vsaerrors.CustomErrorType, false)
}

func (s *RBACActivityTestSuite) TestGetLatestRbacHashForAllOntapVersion_BucketFileDetailsNil() {
	originalGetGCPService := hyperscaler2.GetGCPService
	originalGetBucketFile := activities.GetBucketFile
	defer func() {
		hyperscaler2.GetGCPService = originalGetGCPService
		activities.GetBucketFile = originalGetBucketFile
	}()

	poolDetails := map[string][]PoolDetailWithCurrentHash{
		"9.18.1": {
			{PoolUUID: "pool-uuid-1", CurrentHash: "old-hash"},
		},
	}

	mockGCPService := &google.GcpServices{}
	hyperscaler2.GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		return mockGCPService, nil
	}

	activities.GetBucketFile = func(service hyperscaler2.Services, ctx context.Context, bucketName, fileName string) (*hyperscalermodels.BucketFileDetails, error) {
		return nil, nil // Return nil without error
	}

	result, err := s.activity.GetLatestRbacHashForAllOntapVersion(s.ctx, poolDetails)

	assert.Error(s.T(), err)
	assert.Nil(s.T(), result)
	assertTemporalApplicationError(s.T(), err, "rbac file details not found for ontap version: 9.18.1", vsaerrors.CustomErrorType, false)
}

func (s *RBACActivityTestSuite) TestGetPoolByUUID_Success() {
	expectedPool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
	}

	s.mockStorage.On("GetPoolByUUID", s.ctx, "pool-uuid-1").Return(expectedPool, nil).Once()

	result, err := s.activity.GetPoolByUUID(s.ctx, "pool-uuid-1")

	assert.NoError(s.T(), err)
	assert.Equal(s.T(), expectedPool, result)
}

func (s *RBACActivityTestSuite) TestGetPoolByUUID_StorageNil() {
	activity := &RBACUpdateActivity{SE: nil}

	result, err := activity.GetPoolByUUID(s.ctx, "pool-uuid-1")

	assert.Error(s.T(), err)
	assert.Nil(s.T(), result)
	assertTemporalApplicationError(s.T(), err, "storage is not configured for RBAC activity", vsaerrors.CustomErrorType, false)
}

func (s *RBACActivityTestSuite) TestGetPoolByUUID_StorageError() {
	s.mockStorage.On("GetPoolByUUID", s.ctx, "pool-uuid-1").Return(nil, errors.New("pool not found")).Once()

	result, err := s.activity.GetPoolByUUID(s.ctx, "pool-uuid-1")

	assert.Error(s.T(), err)
	assert.Nil(s.T(), result)
	assertTemporalApplicationError(s.T(), err, "pool not found", vsaerrors.CustomErrorType, false)
}

// --- GetSinglePoolVersionDetails tests ---

func (s *RBACActivityTestSuite) TestGetSinglePoolVersionDetails_Success() {
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "hash-1",
		},
	}

	result, err := s.activity.GetSinglePoolVersionDetails(s.ctx, pool)

	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), result)
	assert.Len(s.T(), result, 1)
	assert.Len(s.T(), result["9.18.1"], 1)
	assert.Equal(s.T(), "pool-uuid-1", result["9.18.1"][0].PoolUUID)
	assert.Equal(s.T(), "hash-1", result["9.18.1"][0].CurrentHash)
}

func (s *RBACActivityTestSuite) TestGetSinglePoolVersionDetails_NilBuildInfo() {
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: nil,
	}

	result, err := s.activity.GetSinglePoolVersionDetails(s.ctx, pool)

	assert.Error(s.T(), err)
	assert.Nil(s.T(), result)
	assertTemporalApplicationError(s.T(), err, "Bad Request", vsaerrors.CustomErrorType, false)
}

func (s *RBACActivityTestSuite) TestGetSinglePoolVersionDetails_EmptyOntapVersion() {
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		Name:      "pool-1",
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "",
		},
	}

	result, err := s.activity.GetSinglePoolVersionDetails(s.ctx, pool)

	assert.Error(s.T(), err)
	assert.Nil(s.T(), result)
	assertTemporalApplicationError(s.T(), err, "Bad Request", vsaerrors.CustomErrorType, false)
}

// --- extractPoolVersionDetail tests ---

func TestExtractPoolVersionDetail_WithValidPool(t *testing.T) {
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "hash-1",
		},
	}

	version, detail := extractPoolVersionDetail(pool)

	assert.Equal(t, "9.18.1", version)
	assert.NotNil(t, detail)
	assert.Equal(t, "pool-uuid-1", detail.PoolUUID)
	assert.Equal(t, "hash-1", detail.CurrentHash)
}

func TestExtractPoolVersionDetail_NilBuildInfo(t *testing.T) {
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		BuildInfo: nil,
	}

	version, detail := extractPoolVersionDetail(pool)

	assert.Empty(t, version)
	assert.Nil(t, detail)
}

func TestExtractPoolVersionDetail_EmptyOntapVersion(t *testing.T) {
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "",
			RbacFileHash: "hash-1",
		},
	}

	version, detail := extractPoolVersionDetail(pool)

	assert.Empty(t, version)
	assert.Nil(t, detail)
}

func TestExtractPoolVersionDetail_EmptyRbacHash(t *testing.T) {
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		BuildInfo: &datamodel.PoolBuildInfo{
			OntapVersion: "9.18.1",
			RbacFileHash: "",
		},
	}

	version, detail := extractPoolVersionDetail(pool)

	assert.Equal(t, "9.18.1", version)
	assert.NotNil(t, detail)
	assert.Equal(t, "pool-uuid-1", detail.PoolUUID)
	assert.Equal(t, "", detail.CurrentHash)
}

// Helper function to assert Temporal application errors
func assertTemporalApplicationError(t *testing.T, err error, expectedMessage string, expectedType string, expectedRetryable bool) {
	assert.Error(t, err)
	if err != nil && expectedMessage != "" {
		assert.Contains(t, err.Error(), expectedMessage)
	}
}
