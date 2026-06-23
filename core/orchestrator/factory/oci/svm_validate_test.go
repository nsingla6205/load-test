package oci

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func TestIsValidOCID(t *testing.T) {
	valid := []string{
		"ocid1.pool.oc1..aabbccdd",
		"ocid1.svm.oc1..aabbccdd",
		"ocid1.tenancy.oc1..aabbccdd",
		"ocid1.secret.oc1.us-phoenix-1.aabbccdd",
		"ocid1.volume.oc1.us-ashburn-1.extra.aabbccdd",
	}
	for _, v := range valid {
		t.Run("Valid_"+v, func(tt *testing.T) {
			assert.True(tt, isValidOCID(v), "expected %q to be valid", v)
		})
	}

	invalid := []string{
		"",
		"tenancy",
		"not-an-ocid",
		"ocid1.pool..a",
		"ocid2.pool.oc1..aabb",
		"OCID1.pool.oc1..aabb",
	}
	for _, v := range invalid {
		t.Run("Invalid_"+v, func(tt *testing.T) {
			assert.False(tt, isValidOCID(v), "expected %q to be invalid", v)
		})
	}
}

func TestValidateSvmName(t *testing.T) {
	t.Run("EmptyName_ReturnsError", func(tt *testing.T) {
		err := validateSvmName("")
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "SVM name is required")
	})

	t.Run("TooLong_ReturnsError", func(tt *testing.T) {
		longName := strings.Repeat("a", svmNameMaxLength+1)
		err := validateSvmName(longName)
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), fmt.Sprintf("at most %d characters", svmNameMaxLength))
	})

	t.Run("InvalidChars_ReturnsError", func(tt *testing.T) {
		err := validateSvmName("svm name!")
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "letters, numbers, hyphens, and underscores")
	})

	t.Run("ValidName_NoError", func(tt *testing.T) {
		err := validateSvmName("my-svm_1")
		assert.NoError(tt, err)
	})

	t.Run("MaxLengthName_NoError", func(tt *testing.T) {
		maxName := strings.Repeat("x", svmNameMaxLength)
		err := validateSvmName(maxName)
		assert.NoError(tt, err)
	})

	t.Run("SingleChar_NoError", func(tt *testing.T) {
		err := validateSvmName("a")
		assert.NoError(tt, err)
	})
}

func TestRequiredDataLifCount(t *testing.T) {
	tests := []struct {
		name        string
		enableIscsi bool
		enableNfs   bool
		nodeCount   int
		expected    int
	}{
		{"NoProtocols_2Nodes", false, false, 2, 2},
		{"IscsiOnly_2Nodes", true, false, 2, 2},
		{"NfsOnly_2Nodes", false, true, 2, 2},
		{"BothProtocols_2Nodes", true, true, 2, 4},
		{"BothProtocols_0Nodes", true, true, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			result := requiredDataLifCount(tc.enableIscsi, tc.enableNfs, tc.nodeCount)
			assert.Equal(tt, tc.expected, result)
		})
	}
}

func TestValidateCreateSvm(t *testing.T) {
	ctx := context.Background()

	makeReadyPool := func(id int64) *datamodel.Pool {
		return &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: id},
			State:     string(datamodel.LifeCycleStateREADY),
			VLMConfig: "some-config",
		}
	}

	readyNodes := []*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", State: datamodel.LifeCycleStateREADY},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", State: datamodel.LifeCycleStateREADY},
	}

	makeValidParams := func(name string) *commonparams.CreateSvmParams {
		return &commonparams.CreateSvmParams{
			PoolOCID:              "ocid1.pool..a",
			SvmExternalIdentifier: "ocid1.svm..a",
			Name:                  name,
			AccountName:           "tenancy",
			EnableIscsi:           true,
		}
	}

	t.Run("ClusterCheckFails_PropagatesError", func(tt *testing.T) {
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			State:     "CREATING",
			VLMConfig: "cfg",
		}
		mockStorage := database.NewMockStorage(tt)
		err := validateCreateSvm(ctx, mockStorage, makeValidParams("svm1"), pool)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsConflictErr(err))
	})

	t.Run("SvmNameInvalid_PropagatesError", func(tt *testing.T) {
		pool := makeReadyPool(1)
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, int64(1)).Return(readyNodes, nil)
		err := validateCreateSvm(ctx, mockStorage, makeValidParams("bad name!"), pool)
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "letters, numbers, hyphens")
	})

	t.Run("NameAlreadyInUseInPool_ReturnsConflict", func(tt *testing.T) {
		pool := makeReadyPool(1)
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, int64(1)).Return(readyNodes, nil)
		mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(1)).
			Return(&datamodel.Svm{Name: "svm1", PoolID: 1, State: datamodel.LifeCycleStateREADY}, nil)
		err := validateCreateSvm(ctx, mockStorage, makeValidParams("svm1"), pool)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsConflictErr(err))
	})

	t.Run("AllChecksPass_ReturnsIPRequirementResult", func(tt *testing.T) {
		pool := makeReadyPool(1)
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, int64(1)).Return(readyNodes, nil)
		mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(1)).
			Return(nil, utilserrors.NewNotFoundErr("svm", nil))
		mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, int64(1)).Return(readyNodes, nil)
		err := validateCreateSvm(ctx, mockStorage, makeValidParams("svm1"), pool)
		assert.NoError(tt, err)
	})
}

func TestValidateSvmNameNotInUseInPool(t *testing.T) {
	ctx := context.Background()

	t.Run("NoExisting_NoError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(7)).
			Return(nil, utilserrors.NewNotFoundErr("svm", nil))
		err := validateSvmNameNotInUseInPool(ctx, mockStorage, "svm1", 7)
		assert.NoError(tt, err)
	})

	t.Run("LiveSvmFound_ReturnsConflict", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(7)).
			Return(&datamodel.Svm{Name: "svm1", PoolID: 7, State: datamodel.LifeCycleStateREADY}, nil)
		err := validateSvmNameNotInUseInPool(ctx, mockStorage, "svm1", 7)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "already exists in this pool")
	})

	// CREATING is also a "live" row that must block a fresh create with the
	// same name in the same pool — a concurrent in-flight create wins, the
	// second request must be rejected.
	t.Run("CreatingSvmFound_ReturnsConflict", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(7)).
			Return(&datamodel.Svm{Name: "svm1", PoolID: 7, State: datamodel.LifeCycleStateCreating}, nil)
		err := validateSvmNameNotInUseInPool(ctx, mockStorage, "svm1", 7)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsConflictErr(err))
	})

	// Defense-in-depth: if a row exists with State=DELETED (e.g. the soft-delete
	// column was not populated for some reason), the name should still be
	// treated as free and creation must be allowed.
	t.Run("DeletedSvmFound_NoError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(7)).
			Return(&datamodel.Svm{Name: "svm1", PoolID: 7, State: datamodel.LifeCycleStateDeleted}, nil)
		err := validateSvmNameNotInUseInPool(ctx, mockStorage, "svm1", 7)
		assert.NoError(tt, err)
	})

	t.Run("DBError_PropagatesError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetSvmByNameAndPoolID(mock.Anything, "svm1", int64(7)).
			Return(nil, fmt.Errorf("db boom"))
		err := validateSvmNameNotInUseInPool(ctx, mockStorage, "svm1", 7)
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "db boom")
		assert.False(tt, utilserrors.IsConflictErr(err))
	})
}

func TestValidateCreateSvmClusterStateAndCapacity_GetNodesError(t *testing.T) {
	ctx := context.Background()
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1},
		State:     string(datamodel.LifeCycleStateREADY),
		VLMConfig: "some-config",
	}
	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, int64(1)).Return(nil, fmt.Errorf("db nodes error"))

	err := validateCreateSvmClusterStateAndCapacity(ctx, mockStorage, pool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db nodes error")
}

func TestValidateCreateSvmIPRequirements_GetNodesError(t *testing.T) {
	ctx := context.Background()
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}
	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, int64(1)).Return(nil, fmt.Errorf("db ip error"))
	params := &commonparams.CreateSvmParams{EnableIscsi: true}
	err := validateCreateSvmIPRequirements(ctx, mockStorage, params, pool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db ip error")
}

func TestValidateCreateSvmIPRequirements(t *testing.T) {
	ctx := context.Background()

	makePool := func(id int64) *datamodel.Pool {
		return &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: id}}
	}

	makeNodes := func(count int) []*datamodel.Node {
		nodes := make([]*datamodel.Node, count)
		for i := range nodes {
			nodes[i] = &datamodel.Node{
				BaseModel: datamodel.BaseModel{ID: int64(i + 1)},
				Name:      fmt.Sprintf("node%d", i+1),
				State:     datamodel.LifeCycleStateREADY,
			}
		}
		return nodes
	}

	t.Run("NoIpsProvided_NoError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := makePool(1)
		mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.AnythingOfType("int64")).Return(makeNodes(2), nil)

		params := &commonparams.CreateSvmParams{EnableIscsi: true, EnableNfs: true}
		err := validateCreateSvmIPRequirements(ctx, mockStorage, params, pool)
		assert.NoError(tt, err)
	})

	t.Run("CorrectIpCount_NoError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := makePool(1)
		mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.AnythingOfType("int64")).Return(makeNodes(2), nil)

		params := &commonparams.CreateSvmParams{
			EnableIscsi: true,
			EnableNfs:   true,
			Ips:         []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"},
		}
		err := validateCreateSvmIPRequirements(ctx, mockStorage, params, pool)
		assert.NoError(tt, err)
	})

	t.Run("WrongIpCount_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := makePool(1)
		mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.AnythingOfType("int64")).Return(makeNodes(2), nil)

		params := &commonparams.CreateSvmParams{
			EnableIscsi: true,
			EnableNfs:   true,
			Ips:         []string{"10.0.0.1"},
		}
		err := validateCreateSvmIPRequirements(ctx, mockStorage, params, pool)
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "Ips count must be")
	})
}

func TestValidateCreateSvmClusterStateAndCapacity(t *testing.T) {
	ctx := context.Background()

	t.Run("PoolNotReady_ReturnsConflict", func(tt *testing.T) {
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			State:     "CREATING",
			VLMConfig: "some-config",
		}
		mockStorage := database.NewMockStorage(tt)

		err := validateCreateSvmClusterStateAndCapacity(ctx, mockStorage, pool)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsConflictErr(err))
	})

	t.Run("EmptyVLMConfig_ReturnsValidationError", func(tt *testing.T) {
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			State:     string(datamodel.LifeCycleStateREADY),
			VLMConfig: "",
		}
		mockStorage := database.NewMockStorage(tt)

		err := validateCreateSvmClusterStateAndCapacity(ctx, mockStorage, pool)
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "cluster config")
	})

	t.Run("NotEnoughNodes_ReturnsError", func(tt *testing.T) {
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			State:     string(datamodel.LifeCycleStateREADY),
			VLMConfig: "some-config",
		}
		mockStorage := database.NewMockStorage(tt)
		nodes := []*datamodel.Node{
			{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node1", State: datamodel.LifeCycleStateREADY},
		}
		mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.AnythingOfType("int64")).Return(nodes, nil)

		err := validateCreateSvmClusterStateAndCapacity(ctx, mockStorage, pool)
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "at least 2 nodes")
	})

	t.Run("NodeNotReady_ReturnsError", func(tt *testing.T) {
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			State:     string(datamodel.LifeCycleStateREADY),
			VLMConfig: "some-config",
		}
		mockStorage := database.NewMockStorage(tt)
		nodes := []*datamodel.Node{
			{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node1", State: datamodel.LifeCycleStateREADY},
			{BaseModel: datamodel.BaseModel{ID: 2}, Name: "node2", State: "CREATING"},
		}
		mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.AnythingOfType("int64")).Return(nodes, nil)

		err := validateCreateSvmClusterStateAndCapacity(ctx, mockStorage, pool)
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "node2 is not ready")
	})

	t.Run("AllNodesReady_NoError", func(tt *testing.T) {
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			State:     string(datamodel.LifeCycleStateREADY),
			VLMConfig: "some-config",
		}
		mockStorage := database.NewMockStorage(tt)
		nodes := []*datamodel.Node{
			{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node1", State: datamodel.LifeCycleStateREADY},
			{BaseModel: datamodel.BaseModel{ID: 2}, Name: "node2", State: datamodel.LifeCycleStateAvailable},
		}
		mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, mock.AnythingOfType("int64")).Return(nodes, nil)

		err := validateCreateSvmClusterStateAndCapacity(ctx, mockStorage, pool)
		assert.NoError(tt, err)
	})
}

func TestValidateUpdateSvmParams(t *testing.T) {
	validParams := func() *commonparams.UpdateSvmParams {
		return &commonparams.UpdateSvmParams{
			PoolOCID:              "ocid1.pool.oc1..aabbccdd",
			SvmExternalIdentifier: "ocid1.svm.oc1..aabbccdd",
			AccountName:           "ocid1.tenancy.oc1..aabbccdd",
			SvmAdminPassword:      &commonparams.OciAdminPassword{Ocid: "ocid1.secret.oc1..aabbccdd", Version: 1},
		}
	}

	t.Run("EmptyPoolOCID_ReturnsError", func(tt *testing.T) {
		p := validParams()
		p.PoolOCID = "  "
		err := validateUpdateSvmParams(p)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "PoolOCID")
	})

	t.Run("InvalidPoolOCID_ReturnsError", func(tt *testing.T) {
		p := validParams()
		p.PoolOCID = "not-an-ocid"
		err := validateUpdateSvmParams(p)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "PoolOCID is not a valid OCID")
	})

	t.Run("EmptySvmOCID_ReturnsError", func(tt *testing.T) {
		p := validParams()
		p.SvmExternalIdentifier = ""
		err := validateUpdateSvmParams(p)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "svmOCID")
	})

	t.Run("InvalidSvmOCID_ReturnsError", func(tt *testing.T) {
		p := validParams()
		p.SvmExternalIdentifier = "bad-format"
		err := validateUpdateSvmParams(p)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "svmOCID is not a valid OCID")
	})

	t.Run("EmptyAccountName_ReturnsError", func(tt *testing.T) {
		p := validParams()
		p.AccountName = "  "
		err := validateUpdateSvmParams(p)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "Tenancy-Ocid")
	})

	t.Run("InvalidAccountName_ReturnsError", func(tt *testing.T) {
		p := validParams()
		p.AccountName = "plain-tenancy-name"
		err := validateUpdateSvmParams(p)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "Tenancy-Ocid is not a valid OCID")
	})

	t.Run("NilAdminPassword_ReturnsError", func(tt *testing.T) {
		p := validParams()
		p.SvmAdminPassword = nil
		err := validateUpdateSvmParams(p)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "svmAdminPassword")
	})

	t.Run("EmptyAdminPasswordOcid_ReturnsError", func(tt *testing.T) {
		p := validParams()
		p.SvmAdminPassword = &commonparams.OciAdminPassword{Ocid: "  ", Version: 1}
		err := validateUpdateSvmParams(p)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "svmAdminPassword")
	})

	t.Run("InvalidAdminPasswordOcid_ReturnsError", func(tt *testing.T) {
		p := validParams()
		p.SvmAdminPassword = &commonparams.OciAdminPassword{Ocid: "not-valid", Version: 1}
		err := validateUpdateSvmParams(p)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "svmAdminPassword.ocid is not a valid OCID")
	})

	t.Run("ValidParams_NoError", func(tt *testing.T) {
		err := validateUpdateSvmParams(validParams())
		assert.NoError(tt, err)
	})
}

func TestValidatePoolStateForSvmUpdate(t *testing.T) {
	t.Run("Ready_Allowed", func(tt *testing.T) {
		assert.NoError(tt, validatePoolStateForSvmUpdate(datamodel.LifeCycleStateREADY))
	})

	t.Run("Error_Allowed", func(tt *testing.T) {
		assert.NoError(tt, validatePoolStateForSvmUpdate(datamodel.LifeCycleStateError))
	})

	t.Run("Disabled_ReturnsConflict", func(tt *testing.T) {
		err := validatePoolStateForSvmUpdate(datamodel.LifeCycleStateDisabled)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "disabled")
	})

	t.Run("Deleted_ReturnsNotFound", func(tt *testing.T) {
		err := validatePoolStateForSvmUpdate(datamodel.LifeCycleStateDeleted)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsNotFoundErr(err))
	})

	t.Run("Creating_ReturnsConflict", func(tt *testing.T) {
		err := validatePoolStateForSvmUpdate(datamodel.LifeCycleStateCreating)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "in progress")
	})

	t.Run("Updating_ReturnsConflict", func(tt *testing.T) {
		err := validatePoolStateForSvmUpdate(datamodel.LifeCycleStateUpdating)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "in progress")
	})

	t.Run("Deleting_ReturnsConflict", func(tt *testing.T) {
		err := validatePoolStateForSvmUpdate(datamodel.LifeCycleStateDeleting)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "in progress")
	})

	t.Run("UnknownState_ReturnsConflict", func(tt *testing.T) {
		err := validatePoolStateForSvmUpdate("SOME_UNKNOWN")
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsConflictErr(err))
	})
}

func TestValidateSvmUpdateState(t *testing.T) {
	t.Run("Deleted_ReturnsNotFound", func(tt *testing.T) {
		svm := &datamodel.Svm{State: datamodel.LifeCycleStateDeleted}
		err := validateSvmUpdateState(svm)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsNotFoundErr(err))
	})

	t.Run("Deleting_ReturnsConflict", func(tt *testing.T) {
		svm := &datamodel.Svm{State: string(datamodel.LifeCycleStateDeleting)}
		err := validateSvmUpdateState(svm)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "deletion is in progress")
	})

	t.Run("Creating_ReturnsConflict", func(tt *testing.T) {
		svm := &datamodel.Svm{State: datamodel.LifeCycleStateCreating}
		err := validateSvmUpdateState(svm)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "creation is in progress")
	})

	t.Run("Updating_ReturnsConflict", func(tt *testing.T) {
		svm := &datamodel.Svm{State: datamodel.LifeCycleStateUpdating}
		err := validateSvmUpdateState(svm)
		require.Error(tt, err)
		assert.True(tt, utilserrors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "already in progress")
	})

	t.Run("Ready_NoError", func(tt *testing.T) {
		svm := &datamodel.Svm{State: string(datamodel.LifeCycleStateREADY)}
		err := validateSvmUpdateState(svm)
		assert.NoError(tt, err)
	})

	t.Run("Error_NoError", func(tt *testing.T) {
		svm := &datamodel.Svm{State: datamodel.LifeCycleStateError}
		err := validateSvmUpdateState(svm)
		assert.NoError(tt, err)
	})
}
