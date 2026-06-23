package database

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"gorm.io/gorm"
)

// isSvmExternalIdentifierUniqueViolation is the helper that maps both the
// GORM-typed duplicate-key sentinel and a raw "unique constraint" substring
// (Postgres / SQLite) to a true response, so the public CreateSvm... paths
// can surface a typed ConflictErr instead of a generic DB error. The unique
// index isn't always exposed as gorm.ErrDuplicatedKey on every driver, so the
// substring fallback is exercised explicitly below.
func TestIsSvmExternalIdentifierUniqueViolation(t *testing.T) {
	t.Run("DuplicatedKeySentinel", func(tt *testing.T) {
		assert.True(tt, isSvmExternalIdentifierUniqueViolation(gorm.ErrDuplicatedKey))
	})
	t.Run("UniqueConstraintSubstring", func(tt *testing.T) {
		assert.True(tt, isSvmExternalIdentifierUniqueViolation(errors.New("UNIQUE constraint failed: svms.svm_external_identifier")))
		assert.True(tt, isSvmExternalIdentifierUniqueViolation(errors.New("pq: duplicate key value violates unique constraint")))
	})
	t.Run("OtherError", func(tt *testing.T) {
		assert.False(tt, isSvmExternalIdentifierUniqueViolation(errors.New("connection refused")))
	})
}

// CreateSvmInCreatingState must reject a nil svm with BadRequestErr so the API
// layer can return 400 instead of allowing a nil-deref through the transaction
// path.
func TestCreateSvmInCreatingState_NilSvm(t *testing.T) {
	db, err := SetupTestDB()
	require.NoError(t, err)
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	_, err = store.CreateSvmInCreatingState(context.Background(), nil)
	assert.Error(t, err)
	assert.True(t, customerrors.IsBadRequestErr(err), "expected BadRequestErr, got %T: %v", err, err)
}

// TransitionSvmToDeleting must reject a nil svm with BadRequestErr for the
// same reason.
func TestTransitionSvmToDeleting_NilSvm(t *testing.T) {
	db, err := SetupTestDB()
	require.NoError(t, err)
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	_, err = store.TransitionSvmToDeleting(context.Background(), nil)
	assert.Error(t, err)
	assert.True(t, customerrors.IsBadRequestErr(err), "expected BadRequestErr, got %T: %v", err, err)
}

// When the underlying table is dropped, SvmExistsByExternalIdentifier must
// surface a typed VCPError rather than return a misleading (false, nil).
func TestSvmExistsByExternalIdentifier_DatabaseError(t *testing.T) {
	db, err := SetupTestDB()
	require.NoError(t, err)
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	require.NoError(t, ClearInMemoryDB(store.db.GORM()))

	require.NoError(t, store.db.GORM().Exec("DROP TABLE svms").Error)

	_, err = store.SvmExistsByExternalIdentifier(context.Background(), "ocid1.svm..a", 1)
	assert.Error(t, err)
	var vcpErr *vsaerrors.CustomError
	assert.True(t, errors.As(err, &vcpErr))
}

func TestGetSvmsByPoolID(t *testing.T) {
	t.Run("WhenSvmExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-svm-uuid",
			},
			Name:   "test_svm",
			PoolID: 1234,
		}
		err = store.db.Create(svm).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm: %v", err)
		}

		result, err := store.GetSvmsByPoolID(context.Background(), svm.PoolID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, result, 1)
		assert.Equal(tt, svm.Name, result[0].Name, "Expected svm name %v, got %v", svm.Name, result[0].Name)
	})
	t.Run("WhenSvmDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		result, err := store.GetSvmsByPoolID(context.Background(), 12)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Empty(tt, result, "Expected result to be empty, but got %v", result)
	})
}

func TestActiveSvmExistsByPoolID(t *testing.T) {
	t.Run("ReturnsTrueWhenActiveSvmExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		require.NoError(tt, err)
		store := NewDataStoreRepository(gormwrapper.New(db))
		require.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		require.NoError(tt, store.db.Create(&datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "active-svm-uuid"},
			Name:      "active-svm",
			PoolID:    1234,
			State:     datamodel.LifeCycleStateREADY,
		}).Error())

		exists, err := store.ActiveSvmExistsByPoolID(context.Background(), 1234)
		require.NoError(tt, err)
		assert.True(tt, exists)
	})

	t.Run("ReturnsFalseForDeletedAndSoftDeletedSvms", func(tt *testing.T) {
		db, err := SetupTestDB()
		require.NoError(tt, err)
		store := NewDataStoreRepository(gormwrapper.New(db))
		require.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		require.NoError(tt, store.db.Create(&datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "deleted-state-svm-uuid"},
			Name:      "deleted-state-svm",
			PoolID:    1234,
			State:     datamodel.LifeCycleStateDeleted,
		}).Error())
		require.NoError(tt, store.db.Create(&datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID:      "soft-deleted-svm-uuid",
				DeletedAt: &gorm.DeletedAt{Time: time.Now(), Valid: true},
			},
			Name:   "soft-deleted-svm",
			PoolID: 1234,
			State:  datamodel.LifeCycleStateREADY,
		}).Error())

		exists, err := store.ActiveSvmExistsByPoolID(context.Background(), 1234)
		require.NoError(tt, err)
		assert.False(tt, exists)
	})
}

func TestGetNextSVMIndexByPoolID(t *testing.T) {
	t.Run("WhenSvmsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create multiple SVMs for the same pool
		svm1 := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-svm-uuid-1",
			},
			Name:   "test_svm_1",
			PoolID: 1234,
		}
		err = store.db.Create(svm1).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm1: %v", err)
		}

		svm2 := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "test-svm-uuid-2",
			},
			Name:   "test_svm_2",
			PoolID: 1234,
		}
		err = store.db.Create(svm2).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm2: %v", err)
		}

		// Create SVM for different pool
		svm3 := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				ID:   3,
				UUID: "test-svm-uuid-3",
			},
			Name:   "test_svm_3",
			PoolID: 5678,
		}
		err = store.db.Create(svm3).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm3: %v", err)
		}

		result, err := store.GetNextSVMIndexByPoolID(context.Background(), 1234)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, int64(3), result, "Expected next index to be 3 (count 2 + 1), got %v", result)
	})
	t.Run("WhenSvmDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		result, err := store.GetNextSVMIndexByPoolID(context.Background(), 12)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, int64(1), result, "Expected next index to be 1 (count 0 + 1), got %v", result)
	})
	t.Run("WhenDeletedSvmsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create an SVM
		svm1 := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-svm-uuid-1",
			},
			Name:   "test_svm_1",
			PoolID: 1234,
		}
		err = store.db.Create(svm1).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm1: %v", err)
		}

		// Create another SVM
		svm2 := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "test-svm-uuid-2",
			},
			Name:   "test_svm_2",
			PoolID: 1234,
		}
		err = store.db.Create(svm2).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm2: %v", err)
		}

		// Delete one SVM (soft delete)
		err = store.db.Delete(svm2).Error()
		if err != nil {
			tt.Fatalf("Failed to delete svm2: %v", err)
		}

		// Count should include deleted SVMs for name uniqueness
		result, err := store.GetNextSVMIndexByPoolID(context.Background(), 1234)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, int64(3), result, "Expected next index to be 3 (count 2 including deleted SVM + 1), got %v", result)
	})
}

func TestCreateSVM(t *testing.T) {
	t.Run("WhenSvmIsCreatedSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1234},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
			State:     datamodel.LifeCycleStateREADY,
		}
		err = store.db.Create(pool).Error()
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID: "test-svm-uuid",
			},
			Name:      "test_svm",
			AccountID: account.ID,
			PoolID:    pool.ID,
		}

		createdSvm, err := store.CreateSVM(context.Background(), svm)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, svm.Name, createdSvm.Name, "Expected svm name %v, got %v", svm.Name, createdSvm.Name)
	})
	t.Run("WhenSvmAlreadyExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1234},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
			State:     datamodel.LifeCycleStateREADY,
		}
		err = store.db.Create(pool).Error()
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID: "test-svm-uuid",
			},
			Name:   "test_svm",
			PoolID: 1234,
		}
		err = store.db.Create(svm).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm: %v", err)
		}

		_, err = store.CreateSVM(context.Background(), svm)
		var customErr *vsaerrors.CustomError
		if errors.As(err, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), "svm already exists")
		} else {
			tt.Fatalf("Expected a CustomError, got %v", err)
		}
	})
	t.Run("WhenDatabaseErrorOccursDuringCheck", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1234},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
			State:     datamodel.LifeCycleStateREADY,
		}
		err = store.db.Create(pool).Error()
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID: "test-svm-uuid",
			},
			Name:      "test_svm",
			AccountID: account.ID,
			PoolID:    pool.ID,
		}

		// Drop the table to simulate a database error during the First query
		err = store.db.GORM().Exec("DROP TABLE svms").Error
		assert.NoError(tt, err)

		_, err = store.CreateSVM(context.Background(), svm)
		assert.Error(tt, err, "Expected an error when database query fails")
		var vcpErr *vsaerrors.CustomError
		assert.True(tt, errors.As(err, &vcpErr), "Expected a CustomError")
		assert.Contains(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "no such table")
	})

	// CreateSVM is now responsible for finalizing rows that were pre-allocated in
	// CREATING state by the OCI flow's CreateSvmInCreatingState step. A second call
	// to CreateSVM (with VLM-derived SvmDetails) must upgrade the row to READY in
	// place rather than returning a conflict.
	t.Run("UpgradesCreatingRowToReady", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		// Step 1: pre-allocate row in CREATING with the OCID — mirrors the OCI
		// orchestrator factory, which always pre-allocates with the SVM's
		// svm_external_identifier set (the unique key under OCI semantics).
		preallocated, err := store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID, SvmExternalIdentifier: "ocid1.svm..a",
		})
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.LifeCycleStateCreating, preallocated.State)

		// Step 2: CreateSVM is invoked again with VLM-derived fields populated. This
		// must upgrade the existing row in place rather than fail with conflict.
		finalized, err := store.CreateSVM(context.Background(), &datamodel.Svm{
			Name:                  "svm-1",
			AccountID:             account.ID,
			PoolID:                pool.ID,
			SvmExternalIdentifier: "ocid1.svm..a",
			SvmDetails:            &datamodel.SvmDetails{ExternalUUID: "ext-uuid", IPSpace: "Default"},
		})
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.LifeCycleStateREADY, finalized.State)
		assert.Equal(tt, datamodel.LifeCycleStateAvailableDetails, finalized.StateDetails)
		assert.Equal(tt, "ocid1.svm..a", finalized.SvmExternalIdentifier)
		if assert.NotNil(tt, finalized.SvmDetails) {
			assert.Equal(tt, "ext-uuid", finalized.SvmDetails.ExternalUUID)
		}
		// Same row (idempotent UUID).
		assert.Equal(tt, preallocated.UUID, finalized.UUID)
	})

	// Temporal retry: an already-READY row must be returned idempotently rather
	// than producing a conflict, so a worker crash between row finalization and
	// activity completion does not strand the workflow.
	t.Run("ReturnsExistingRowWhenAlreadyReady", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		first, err := store.CreateSVM(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID,
		})
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.LifeCycleStateREADY, first.State)

		second, err := store.CreateSVM(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID,
		})
		assert.NoError(tt, err)
		assert.Equal(tt, first.UUID, second.UUID, "retry should return the same row")
	})

	// Regression: under OCI semantics, name is not unique within (account_id,
	// pool_id) — only svm_external_identifier is. If a stale row (from a
	// previous successful create with the same name) coexists with the new
	// pre-allocated CREATING row, finalize must target the CREATING row by
	// OCID, not blindly pick the first row matching (account_id, name, pool_id)
	// — which would silently no-op via the "already READY" branch and leave
	// the new row stuck in CREATING forever.
	t.Run("FinalizesByOCIDWhenStaleRowSharesNameAndPool", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		// Stale survivor from a previous successful create with the same name,
		// different OCID. Same (account_id, name, pool_id) as the new attempt.
		stale := &datamodel.Svm{
			BaseModel:             datamodel.BaseModel{UUID: "stale-uuid"},
			Name:                  "svm-1",
			AccountID:             account.ID,
			PoolID:                pool.ID,
			SvmExternalIdentifier: "ocid1.svm..stale",
			State:                 datamodel.LifeCycleStateREADY,
			StateDetails:          datamodel.LifeCycleStateAvailableDetails,
		}
		assert.NoError(tt, store.db.Create(stale).Error())

		// Pre-allocate the new CREATING row with a distinct OCID (mirrors
		// CreateSvmInCreatingState in the orchestrator factory).
		preallocated, err := store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name:                  "svm-1",
			AccountID:             account.ID,
			PoolID:                pool.ID,
			SvmExternalIdentifier: "ocid1.svm..fresh",
		})
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.LifeCycleStateCreating, preallocated.State)
		assert.NotEqual(tt, stale.UUID, preallocated.UUID)

		// Finalize: must target the pre-allocated row by OCID, not the stale
		// READY row that shares (account_id, name, pool_id).
		finalized, err := store.CreateSVM(context.Background(), &datamodel.Svm{
			Name:                  "svm-1",
			AccountID:             account.ID,
			PoolID:                pool.ID,
			SvmExternalIdentifier: "ocid1.svm..fresh",
			SvmDetails:            &datamodel.SvmDetails{ExternalUUID: "fresh-ext", IPSpace: "Default"},
		})
		assert.NoError(tt, err)
		assert.Equal(tt, preallocated.UUID, finalized.UUID, "finalize must target the pre-allocated row, not the stale one")
		assert.Equal(tt, datamodel.LifeCycleStateREADY, finalized.State)
		assert.Equal(tt, "ocid1.svm..fresh", finalized.SvmExternalIdentifier)
		if assert.NotNil(tt, finalized.SvmDetails) {
			assert.Equal(tt, "fresh-ext", finalized.SvmDetails.ExternalUUID)
		}

		// Stale row must be untouched.
		var staleAfter datamodel.Svm
		assert.NoError(tt, store.db.GORM().Where("uuid = ?", stale.UUID).First(&staleAfter).Error)
		assert.Equal(tt, datamodel.LifeCycleStateREADY, staleAfter.State)
		assert.Equal(tt, "ocid1.svm..stale", staleAfter.SvmExternalIdentifier)
	})
}

func TestCreateSvmInCreatingState(t *testing.T) {
	t.Run("InsertsRowInCreatingState", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		svm, err := store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID, SvmExternalIdentifier: "ocid1.svm..a",
		})
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.LifeCycleStateCreating, svm.State)
		assert.Equal(tt, datamodel.LifeCycleStateCreatingDetails, svm.StateDetails)
		assert.Equal(tt, "ocid1.svm..a", svm.SvmExternalIdentifier)
		assert.NotEmpty(tt, svm.UUID)
	})

	// Legacy path (no SvmExternalIdentifier): idempotent retry by
	// (account_id, name, pool_id) is preserved for non-OCI callers so a
	// Temporal worker crash between insert and ack does not strand the create.
	t.Run("IdempotentReturnsExistingRowOnRetry", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		first, err := store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID,
		})
		assert.NoError(tt, err)

		second, err := store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID,
		})
		assert.NoError(tt, err)
		assert.Equal(tt, first.UUID, second.UUID)
		assert.Equal(tt, datamodel.LifeCycleStateCreating, second.State)
	})

	// External-identifier idempotency: a retry with the same OCID + name +
	// pool while still in CREATING returns the existing row so a Temporal
	// worker crash between insert and ack does not strand the create.
	t.Run("IdempotentReturnsExistingRowForSameExternalIdentifier", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		first, err := store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID, SvmExternalIdentifier: "ocid1.svm..a",
		})
		assert.NoError(tt, err)

		second, err := store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID, SvmExternalIdentifier: "ocid1.svm..a",
		})
		assert.NoError(tt, err)
		assert.Equal(tt, first.UUID, second.UUID)
		assert.Equal(tt, datamodel.LifeCycleStateCreating, second.State)
	})

	// Concurrency guard: a second insert that reuses an OCID already owned by
	// a different SVM (different name) must return ConflictErr so the API can
	// translate that into a synchronous 409. This is the case that previously
	// returned 202 IN_PROGRESS for both racing requests.
	t.Run("ReturnsConflictWhenExternalIdentifierTakenByDifferentSvm", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		_, err = store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID, SvmExternalIdentifier: "ocid1.svm..a",
		})
		assert.NoError(tt, err)

		_, err = store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-2", AccountID: account.ID, PoolID: pool.ID, SvmExternalIdentifier: "ocid1.svm..a",
		})
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsConflictErr(err), "expected ConflictErr, got %T: %v", err, err)
	})

	// Tombstones (soft-deleted rows) still occupy the partial unique index
	// slot for the OCID, so re-using a deleted SVM's OCID must also return
	// ConflictErr rather than silently inserting a second row.
	t.Run("ReturnsConflictWhenExternalIdentifierExistsOnSoftDeletedRow", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		existing := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID:      "existing-uuid",
				DeletedAt: &gorm.DeletedAt{Time: time.Now(), Valid: true},
			},
			Name:                  "svm-1",
			AccountID:             account.ID,
			PoolID:                pool.ID,
			SvmExternalIdentifier: "ocid1.svm..a",
			State:                 datamodel.LifeCycleStateDeleted,
		}
		assert.NoError(tt, store.db.GORM().Create(existing).Error)

		_, err = store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID, SvmExternalIdentifier: "ocid1.svm..a",
		})
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsConflictErr(err), "expected ConflictErr, got %T: %v", err, err)
	})

	// Matrix Section 4: a live row in READY state with the same OCID must
	// return ConflictErr (the partial unique index would reject the insert
	// anyway; this surfaces it as a typed 409 rather than a generic DB error).
	t.Run("ReturnsConflictWhenExternalIdentifierExistsOnReadyRow", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		existing := &datamodel.Svm{
			BaseModel:             datamodel.BaseModel{UUID: "existing-uuid"},
			Name:                  "svm-1",
			AccountID:             account.ID,
			PoolID:                pool.ID,
			SvmExternalIdentifier: "ocid1.svm..a",
			State:                 datamodel.LifeCycleStateREADY,
		}
		assert.NoError(tt, store.db.GORM().Create(existing).Error)

		_, err = store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID, SvmExternalIdentifier: "ocid1.svm..a",
		})
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsConflictErr(err), "expected ConflictErr, got %T: %v", err, err)
	})

	// Matrix Section 4: a row in ERROR state with the same OCID must also
	// return ConflictErr. ERROR is a dead-end state; the user must explicitly
	// DELETE the failed row before attempting to recreate with the same OCID.
	// There is no auto-recovery / reset path.
	t.Run("ReturnsConflictWhenExternalIdentifierExistsOnErrorRow", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		existing := &datamodel.Svm{
			BaseModel:             datamodel.BaseModel{UUID: "existing-uuid"},
			Name:                  "svm-1",
			AccountID:             account.ID,
			PoolID:                pool.ID,
			SvmExternalIdentifier: "ocid1.svm..a",
			State:                 datamodel.LifeCycleStateError,
		}
		assert.NoError(tt, store.db.GORM().Create(existing).Error)

		_, err = store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID, SvmExternalIdentifier: "ocid1.svm..a",
		})
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsConflictErr(err), "expected ConflictErr, got %T: %v", err, err)
	})

	// Same name + pool collision (legacy / non-OCI path with empty OCID):
	// when a live row with state READY exists for the same (account, name,
	// pool), the second pre-allocation must surface a typed ConflictErr
	// instead of silently returning the existing row as "idempotent".
	t.Run("ReturnsConflictWhenSameNameAndPoolExistsAsReady_NoOCID", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		existing := &datamodel.Svm{
			BaseModel:    datamodel.BaseModel{UUID: "existing-uuid"},
			Name:         "svm-1",
			AccountID:    account.ID,
			PoolID:       pool.ID,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
		}
		assert.NoError(tt, store.db.GORM().Create(existing).Error)

		_, err = store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID,
		})
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsConflictErr(err), "expected ConflictErr, got %T: %v", err, err)
	})

	// Same-name carve-out: only DELETED (soft-deleted) rows for the name
	// must NOT block a new pre-allocation, since the user can recreate
	// after a successful delete.
	t.Run("AllowsCreateWhenOnlyDeletedRowExistsForSameName", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		// Soft-deleted prior SVM with same name+pool but no OCID (legacy path).
		deleted := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID:      "deleted-uuid",
				DeletedAt: &gorm.DeletedAt{Time: time.Now(), Valid: true},
			},
			Name:      "svm-1",
			AccountID: account.ID,
			PoolID:    pool.ID,
			State:     datamodel.LifeCycleStateDeleted,
		}
		assert.NoError(tt, store.db.GORM().Create(deleted).Error)

		fresh, err := store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID,
		})
		assert.NoError(tt, err)
		if assert.NotNil(tt, fresh) {
			assert.Equal(tt, datamodel.LifeCycleStateCreating, fresh.State)
			assert.NotEqual(tt, deleted.UUID, fresh.UUID)
		}
	})

	// Matrix Section 4: a row in DELETING state with the same OCID must
	// return ConflictErr. The OCID slot is occupied by a delete-in-progress
	// row; the user must wait for the delete workflow to finish (which
	// soft-deletes the row, but the OCID slot is still held due to the
	// partial unique index — so this OCID is unusable from this point on).
	t.Run("ReturnsConflictWhenExternalIdentifierExistsOnDeletingRow", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		existing := &datamodel.Svm{
			BaseModel:             datamodel.BaseModel{UUID: "existing-uuid"},
			Name:                  "svm-1",
			AccountID:             account.ID,
			PoolID:                pool.ID,
			SvmExternalIdentifier: "ocid1.svm..a",
			State:                 datamodel.LifeCycleStateDeleting,
		}
		assert.NoError(tt, store.db.GORM().Create(existing).Error)

		_, err = store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID, SvmExternalIdentifier: "ocid1.svm..a",
		})
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsConflictErr(err), "expected ConflictErr, got %T: %v", err, err)
	})
}

// TestCreateSvmInCreatingState_ParallelSameOCID locks in the race-loser
// contract: when two concurrent CreateSvmInCreatingState calls collide on
// the same OCID, the user-visible outcome must be exactly one success and
// one typed ConflictErr (never a generic DB-insert error). Internally the
// loser may be rejected either by the SELECT existence check or by the
// partial unique index at INSERT time — both paths must surface
// ConflictErr so the API layer can return HTTP 409 instead of 500. Uses
// the file-based SQLite DB so the partial unique index from the GORM
// model is actually created and enforced.
func TestCreateSvmInCreatingState_ParallelSameOCID(t *testing.T) {
	db, fileName, err := SetupTestFileDB()
	assert.NoError(t, err, "Failed to set up file-based test database")
	defer cleanupTestDBFile(db, fileName)
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	assert.NoError(t, ClearInMemoryDB(store.db.GORM()))

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
	assert.NoError(t, store.db.Create(account).Error())
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
	assert.NoError(t, store.db.Create(pool).Error())

	const sharedOCID = "ocid1.svm..race"
	const numParallel = 2
	var wg sync.WaitGroup
	errs := make([]error, numParallel)
	results := make([]*datamodel.Svm, numParallel)
	start := make(chan struct{})

	for i := 0; i < numParallel; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			svm, e := store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
				Name:                  "svm-race-" + string(rune('A'+idx)),
				AccountID:             account.ID,
				PoolID:                pool.ID,
				SvmExternalIdentifier: sharedOCID,
			})
			results[idx] = svm
			errs[idx] = e
		}(i)
	}
	close(start)
	wg.Wait()

	var successes, conflicts int
	for i, e := range errs {
		switch {
		case e == nil:
			successes++
			assert.NotNil(t, results[i], "goroutine %d returned nil row on success", i)
		case customerrors.IsConflictErr(e):
			conflicts++
		default:
			t.Errorf("goroutine %d returned unexpected error type %T: %v", i, e, e)
		}
	}
	assert.Equal(t, 1, successes, "exactly one goroutine should succeed")
	assert.Equal(t, 1, conflicts, "exactly one goroutine should get ConflictErr")

	var count int64
	assert.NoError(t, store.db.GORM().Unscoped().
		Model(&datamodel.Svm{}).
		Where("svm_external_identifier = ?", sharedOCID).
		Count(&count).Error)
	assert.Equal(t, int64(1), count, "exactly one row should exist for the shared OCID")
}

func TestSvmExistsByExternalIdentifier(t *testing.T) {
	// Quiet existence check: returns (false, nil) when no row matches so the
	// happy path of a fresh create does not log "record not found" at ERROR.
	t.Run("ReturnsFalseWhenNoRowMatches", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())

		exists, err := store.SvmExistsByExternalIdentifier(context.Background(), "ocid1.svm..none", account.ID)
		assert.NoError(tt, err)
		assert.False(tt, exists)
	})

	t.Run("ReturnsTrueForLiveRow", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		_, err = store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID, SvmExternalIdentifier: "ocid1.svm..a",
		})
		assert.NoError(tt, err)

		exists, err := store.SvmExistsByExternalIdentifier(context.Background(), "ocid1.svm..a", account.ID)
		assert.NoError(tt, err)
		assert.True(tt, exists)
	})

	// Soft-deleted rows still occupy the partial unique index slot, so the
	// existence check (which is unscoped) must report them as conflicts to
	// match what the DB will enforce on the subsequent insert.
	t.Run("ReturnsTrueForSoftDeletedRow", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		existing := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID:      "existing-uuid",
				DeletedAt: &gorm.DeletedAt{Time: time.Now(), Valid: true},
			},
			Name:                  "svm-1",
			AccountID:             account.ID,
			PoolID:                pool.ID,
			SvmExternalIdentifier: "ocid1.svm..a",
			State:                 datamodel.LifeCycleStateDeleted,
		}
		assert.NoError(tt, store.db.GORM().Create(existing).Error)

		exists, err := store.SvmExistsByExternalIdentifier(context.Background(), "ocid1.svm..a", account.ID)
		assert.NoError(tt, err)
		assert.True(tt, exists)
	})

	// Defensive guards for invalid inputs: the API layer should never call
	// with an empty OCID or zero accountID, but if it does, return (false,
	// nil) instead of doing an unbounded scan.
	t.Run("ReturnsFalseForEmptyExternalIdentifier", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		exists, err := store.SvmExistsByExternalIdentifier(context.Background(), "", 1)
		assert.NoError(tt, err)
		assert.False(tt, exists)
	})
}

func TestTransitionSvmToDeleting(t *testing.T) {
	// Helper: seed account+pool+svm with the supplied initial state and
	// return the persisted SVM row.
	seed := func(tt *testing.T, store *DataStoreRepository, state string) *datamodel.Svm {
		tt.Helper()
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct"}, Name: "acct"}
		assert.NoError(tt, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, Name: "pool", AccountID: account.ID, State: datamodel.LifeCycleStateREADY}
		assert.NoError(tt, store.db.Create(pool).Error())

		svm, err := store.CreateSvmInCreatingState(context.Background(), &datamodel.Svm{
			Name: "svm-1", AccountID: account.ID, PoolID: pool.ID, SvmExternalIdentifier: "ocid1.svm..a",
		})
		assert.NoError(tt, err)
		// CreateSvmInCreatingState seeds CREATING; flip to the requested
		// state directly so each subtest can target a specific source state
		// without going through the production state machine.
		if state != datamodel.LifeCycleStateCreating {
			assert.NoError(tt, store.db.GORM().Model(svm).Update("state", state).Error)
			svm.State = state
		}
		return svm
	}

	t.Run("FlipsReadyToDeleting", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		svm := seed(tt, store, datamodel.LifeCycleStateREADY)

		updated, err := store.TransitionSvmToDeleting(context.Background(), svm)
		assert.NoError(tt, err)
		if assert.NotNil(tt, updated) {
			assert.Equal(tt, datamodel.LifeCycleStateDeleting, updated.State)
			assert.Equal(tt, datamodel.LifeCycleStateDeletingDetails, updated.StateDetails)
		}
	})

	// ERROR is a deletable state per validateSvmDeletionState (only DELETED,
	// DELETING, CREATING are rejected). The CAS predicate must allow it.
	t.Run("FlipsErrorToDeleting", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		svm := seed(tt, store, datamodel.LifeCycleStateError)

		updated, err := store.TransitionSvmToDeleting(context.Background(), svm)
		assert.NoError(tt, err)
		if assert.NotNil(tt, updated) {
			assert.Equal(tt, datamodel.LifeCycleStateDeleting, updated.State)
		}
	})

	// Concurrency guard: row is already DELETING (race winner already moved
	// it). The CAS must report ConflictErr instead of silently re-stamping.
	t.Run("ReturnsConflictWhenAlreadyDeleting", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		svm := seed(tt, store, datamodel.LifeCycleStateDeleting)

		_, err = store.TransitionSvmToDeleting(context.Background(), svm)
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsConflictErr(err), "expected ConflictErr, got %T: %v", err, err)
	})

	// DELETED is terminal — the CAS layer must mirror validateSvmDeletionState
	// and surface NotFoundErr ("svm deleted already"), not a misleading 409.
	t.Run("ReturnsNotFoundWhenAlreadyDeleted", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		svm := seed(tt, store, datamodel.LifeCycleStateDeleted)

		_, err = store.TransitionSvmToDeleting(context.Background(), svm)
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsNotFoundErr(err), "expected NotFoundErr, got %T: %v", err, err)
	})

	// Soft-deleted row (deleted_at set) must still be re-read via Unscoped()
	// and surface NotFoundErr — without Unscoped this would collapse to
	// "svm not found" and lose the "already deleted" signal.
	t.Run("ReturnsNotFoundWhenSoftDeleted", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		svm := seed(tt, store, datamodel.LifeCycleStateDeleted)
		assert.NoError(tt, store.db.GORM().Delete(&datamodel.Svm{}, svm.ID).Error)

		_, err = store.TransitionSvmToDeleting(context.Background(), svm)
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsNotFoundErr(err), "expected NotFoundErr, got %T: %v", err, err)
	})

	// Row was hard-deleted out from under us (no soft-delete tombstone).
	// The re-read finds nothing and we surface NotFoundErr("svm not found").
	t.Run("ReturnsNotFoundWhenRowMissing", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		svm := seed(tt, store, datamodel.LifeCycleStateREADY)
		assert.NoError(tt, store.db.GORM().Unscoped().Delete(&datamodel.Svm{}, svm.ID).Error)

		_, err = store.TransitionSvmToDeleting(context.Background(), svm)
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsNotFoundErr(err), "expected NotFoundErr, got %T: %v", err, err)
	})

	t.Run("ReturnsConflictWhenStillCreating", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		svm := seed(tt, store, datamodel.LifeCycleStateCreating)

		_, err = store.TransitionSvmToDeleting(context.Background(), svm)
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsConflictErr(err), "expected ConflictErr, got %T: %v", err, err)
	})
}

func TestDeleteSVM(t *testing.T) {
	t.Run("WhenSvmIsDeletedSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1234},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
			State:     datamodel.LifeCycleStateREADY,
		}
		err = store.db.Create(pool).Error()
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID: "test-svm-uuid",
			},
			Name:      "test_svm",
			AccountID: account.ID,
			PoolID:    pool.ID,
		}
		err = store.db.Create(svm).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm: %v", err)
		}

		err = store.DeleteSVM(context.Background(), svm)
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}

		deletedSvm := &datamodel.Svm{}
		err = store.db.GORM().First(deletedSvm, "uuid = ?", svm.UUID).Error
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			tt.Errorf("Expected record not found error, got %v", err)
		}
	})
}

func TestDeletingSVM(t *testing.T) {
	t.Run("UpdatesSvmStateToDeletingSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1234},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
			State:     datamodel.LifeCycleStateREADY,
		}
		err = store.db.Create(pool).Error()
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID: "test-svm-uuid",
			},
			Name:      "test_svm",
			AccountID: account.ID,
			PoolID:    pool.ID,
		}
		err = store.db.Create(svm).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm: %v", err)
		}

		err = store.DeletingSVM(context.Background(), svm)
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}

		updatedSvm := &datamodel.Svm{}
		err = store.db.GORM().First(updatedSvm, "uuid = ?", svm.UUID).Error
		if err != nil {
			tt.Fatalf("Failed to fetch updated svm: %v", err)
		}
		if updatedSvm.State != datamodel.LifeCycleStateDeleting {
			tt.Errorf("Expected state %v, got %v", datamodel.LifeCycleStateDeleting, updatedSvm.State)
		}
		if updatedSvm.StateDetails != datamodel.LifeCycleStateDeletingDetails {
			tt.Errorf("Expected state details %v, got %v", datamodel.LifeCycleStateDeletingDetails, updatedSvm.StateDetails)
		}
	})
}

func TestGetSvmForPoolID(t *testing.T) {
	t.Run("WhenSvmExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-svm-uuid",
			},
			Name:   "test_svm",
			PoolID: 1234,
		}
		err = store.db.Create(svm).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm: %v", err)
		}

		result, err := store.GetSvmForPoolID(context.Background(), svm.PoolID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, svm.Name, result.Name, "Expected svm name %v, got %v", svm.Name, result.Name)
		assert.Equal(tt, svm.PoolID, result.PoolID, "Expected svm pool id %v, got %v", svm.PoolID, result.PoolID)
	})
}

func TestGetSvmByKmsId(t *testing.T) {
	t.Run("WhenExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		if err != nil {
			tt.Fatalf("Failed to clean up test database: %v", err)
		}

		kms := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-kms-uuid",
			},
			Name: "test_kms",
		}

		svm := &datamodel.Svm{
			BaseModel:   datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:        "test_svm",
			KmsConfigID: sql.NullInt64{Int64: kms.ID, Valid: true},
		}
		err = store.db.Create(kms).Error()
		if err != nil {
			tt.Fatalf("Failed to create kms config: %v", err)
		}
		err = store.db.Create(svm).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm: %v", err)
		}

		result, err := store.GetSvmsByKmsConfigID(context.Background(), 1)
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}
		if result[0].Name != svm.Name {
			tt.Errorf("Expected svm name %v, got %v", svm.Name, result[0].Name)
		}
	})

	t.Run("WhenDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		if err != nil {
			tt.Fatalf("Failed to clean up test database: %v", err)
		}

		kms := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-kms-uuid",
			},
			Name: "test_kms",
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:      "test_svm",
		}
		err = store.db.Create(kms).Error()
		if err != nil {
			tt.Fatalf("Failed to create kms config: %v", err)
		}
		err = store.db.Create(svm).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm: %v", err)
		}

		svms, err := store.GetSvmsByKmsConfigID(context.Background(), 1)
		if err != nil {
			tt.Errorf("Expected nil, got error")
		}
		assert.Equal(tt, 0, len(svms), "Expected no SVMs to be returned when KMS ID does not match")
	})
}

func TestErroredSVM(t *testing.T) {
	t.Run("WhenSvmIsMarkedErroredSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-svm-uuid",
			},
			Name:      "test_svm",
			AccountID: int64(10),
			PoolID:    1234,
			State:     datamodel.LifeCycleStateREADY,
		}
		err = store.db.Create(svm).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm: %v", err)
		}

		errMsg := "error during svm update"
		err = store.ErroredSVM(context.Background(), svm, errMsg)
		assert.NoError(tt, err)

		updatedSvm := &datamodel.Svm{}
		err = store.db.GORM().First(updatedSvm, "uuid = ?", svm.UUID).Error
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.LifeCycleStateError, updatedSvm.State)
		assert.Equal(tt, errMsg, updatedSvm.StateDetails)
		assert.WithinDuration(tt, time.Now(), updatedSvm.UpdatedAt, 2*time.Second)
	})

	t.Run("WhenUpdatingSvmFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "test-svm-uuid-2",
			},
			Name:      "failing_svm",
			AccountID: int64(20),
			PoolID:    5678,
			State:     datamodel.LifeCycleStateREADY,
		}
		err = store.db.Create(svm).Error()
		if err != nil {
			tt.Fatalf("Failed to create svm: %v", err)
		}

		err = store.db.GORM().Exec("DROP TABLE svms").Error
		assert.NoError(tt, err)

		errMsg := "simulated update error"
		err = store.ErroredSVM(context.Background(), svm, errMsg)
		assert.Error(tt, err)
		var vcpErr *vsaerrors.CustomError
		assert.True(tt, errors.As(err, &vcpErr))
		assert.Contains(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "no such table")
	})
}

func TestGetSvmsByKmsConfigID(t *testing.T) {
	t.Run("UpdateSvmWithKmsConfigIDsReturnsErrorWhenKmsConfigNotFound", func(t *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err)

		svm := &datamodel.Svm{
			BaseModel:  datamodel.BaseModel{ID: 1, UUID: "svm-uuid"},
			Name:       "test_svm",
			SvmDetails: &datamodel.SvmDetails{},
		}
		err = store.db.Create(svm).Error()
		assert.NoError(t, err)

		updated, err := store.UpdateSvmWithKmsConfigIDs(context.Background(), svm, "non-existent-uuid", "external-uuid")
		assert.Error(t, err)
		assert.Nil(t, updated)
	})
	t.Run("UpdateSvmWithKmsConfigIDsReturnsErrorOnSaveFailure", func(t *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err)

		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{ID: 2, UUID: "kms-uuid"},
			Name:      "test_kms",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(t, err)

		svm := &datamodel.Svm{
			BaseModel:  datamodel.BaseModel{ID: 1, UUID: "svm-uuid"},
			Name:       "test_svm",
			SvmDetails: &datamodel.SvmDetails{},
		}
		err = store.db.Create(svm).Error()
		assert.NoError(t, err)

		// Simulate error by closing the DB connection
		sqlDB, _ := db.DB()
		err = sqlDB.Close()
		assert.NoError(t, err)

		updated, err := store.UpdateSvmWithKmsConfigIDs(context.Background(), svm, "kms-uuid", "external-uuid")
		assert.Error(t, err)
		assert.Nil(t, updated)
	})
}

func TestListSvmsWithAccountId(t *testing.T) {
	t.Run("WhenSoftDeletedSvmsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:      "test_svm",
			AccountID: account.ID,
		}
		assert.NoError(tt, store.db.Create(svm).Error())

		// soft delete
		svm.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
		assert.NoError(tt, store.db.GORM().Unscoped().Save(svm).Error)

		svms, err := store.ListSvmsWithAccountId(context.Background(), account.ID)
		assert.NoError(tt, err)
		// soft-deleted SVMs should not be returned by the non-unscoped listing
		assert.Len(tt, svms, 0)
	})

	t.Run("WhenNoSoftDeletedSvms", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		svms, err := store.ListSvmsWithAccountId(context.Background(), 9999)
		assert.NoError(tt, err)
		assert.Empty(tt, svms)
	})

	t.Run("WhenDatabaseErrorOccurs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		assert.NoError(tt, sqlDB.Close())

		svms, err := store.ListSvmsWithAccountId(context.Background(), 1)
		assert.Error(tt, err)
		assert.Nil(tt, svms)
	})
}

func TestGetSvmByExternalIdentifier(t *testing.T) {
	const externalID = "ocid1.svm.oc1..aaaa"

	t.Run("WhenSvmExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		svm := &datamodel.Svm{
			BaseModel:             datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:                  "test_svm",
			SvmExternalIdentifier: externalID,
			AccountID:             account.ID,
		}
		assert.NoError(tt, store.db.Create(svm).Error())

		got, err := store.GetSvmByExternalIdentifier(context.Background(), externalID, account.ID)
		assert.NoError(tt, err)
		assert.NotNil(tt, got)
		assert.Equal(tt, svm.UUID, got.UUID)
	})

	t.Run("WhenSoftDeleted_StillReturnsRow", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		svm := &datamodel.Svm{
			BaseModel:             datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:                  "test_svm",
			SvmExternalIdentifier: externalID,
			AccountID:             account.ID,
		}
		assert.NoError(tt, store.db.Create(svm).Error())

		svm.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
		assert.NoError(tt, store.db.GORM().Unscoped().Save(svm).Error)

		// GetSvmByExternalIdentifier returns rows in any state, including soft-deleted,
		// so callers can detect existing OCIDs (e.g. to reject duplicate creates).
		got, err := store.GetSvmByExternalIdentifier(context.Background(), externalID, account.ID)
		assert.NoError(tt, err)
		assert.NotNil(tt, got)
		assert.Equal(tt, svm.UUID, got.UUID)
		assert.True(tt, got.DeletedAt != nil && got.DeletedAt.Valid)
	})

	t.Run("WhenAccountMismatch_ReturnsNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		svm := &datamodel.Svm{
			BaseModel:             datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:                  "test_svm",
			SvmExternalIdentifier: externalID,
			AccountID:             account.ID,
		}
		assert.NoError(tt, store.db.Create(svm).Error())

		got, err := store.GetSvmByExternalIdentifier(context.Background(), externalID, 9999)
		assert.Error(tt, err)
		assert.Nil(tt, got)
	})

	t.Run("WhenSvmDoesNotExist_ReturnsNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		got, err := store.GetSvmByExternalIdentifier(context.Background(), externalID, 1)
		assert.Error(tt, err)
		assert.Nil(tt, got)
	})
}

func TestUnsetSvmActiveDirectoryID(t *testing.T) {
	t.Run("WhenSvmActiveDirectoryIsUnsetSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1234},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
			State:     datamodel.LifeCycleStateREADY,
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:      "test_svm",
			AccountID: account.ID,
			PoolID:    pool.ID,
			ActiveDirectoryID: sql.NullInt64{
				Int64: 1,
				Valid: true,
			},
		}
		assert.NoError(tt, store.db.Create(svm).Error())

		updatedSvm, err := store.UnsetSvmActiveDirectoryID(context.Background(), svm)
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedSvm)
		assert.False(tt, updatedSvm.ActiveDirectoryID.Valid)

		// Verify in database
		verifySvm := &datamodel.Svm{}
		err = store.db.GORM().First(verifySvm, "uuid = ?", svm.UUID).Error
		assert.NoError(tt, err)
		assert.False(tt, verifySvm.ActiveDirectoryID.Valid)
	})

	t.Run("WhenTransactionStartFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		// Close the database connection to simulate transaction start failure
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		assert.NoError(tt, sqlDB.Close())

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:      "test_svm",
		}

		updatedSvm, err := store.UnsetSvmActiveDirectoryID(context.Background(), svm)
		assert.Error(tt, err)
		assert.Nil(tt, updatedSvm)
	})

	t.Run("WhenSaveFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1234},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
			State:     datamodel.LifeCycleStateREADY,
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:      "test_svm",
			AccountID: account.ID,
			PoolID:    pool.ID,
		}
		assert.NoError(tt, store.db.Create(svm).Error())

		// Drop the table to simulate save failure
		err = store.db.GORM().Exec("DROP TABLE svms").Error
		assert.NoError(tt, err)

		updatedSvm, err := store.UnsetSvmActiveDirectoryID(context.Background(), svm)
		assert.Error(tt, err)
		assert.Nil(tt, updatedSvm)
		var vcpErr *vsaerrors.CustomError
		assert.True(tt, errors.As(err, &vcpErr))
	})
}

func TestDataStoreRepository_GetSvmByExternalUUID(t *testing.T) {
	t.Run("WhenSvmExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-external-1"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "pool-external-1"},
			Name:           "test_pool",
			AccountID:      account.ID,
			State:          datamodel.LifeCycleStateREADY,
			DeploymentName: "deployment-1",
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		const externalUUID = "550e8400-e29b-41d4-a716-446655440000"
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "svm-external-1"},
			Name:      "test_svm",
			PoolID:    pool.ID,
			AccountID: account.ID,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: externalUUID,
				IPSpace:      "Default",
			},
		}
		assert.NoError(tt, store.db.Create(svm).Error())

		result, err := store.GetSvmByExternalUUID(context.Background(), externalUUID, pool.ID)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, svm.UUID, result.UUID)
		assert.Equal(tt, externalUUID, result.SvmDetails.ExternalUUID)
	})

	t.Run("WhenSvmDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		result, err := store.GetSvmByExternalUUID(context.Background(), "missing-external", 999)
		assert.Error(tt, err)
		assert.Nil(tt, result)
	})

	t.Run("WhenPoolDoesNotMatch", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-external-2"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		sourcePool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "pool-source"},
			Name:           "source_pool",
			AccountID:      account.ID,
			State:          datamodel.LifeCycleStateREADY,
			DeploymentName: "deployment-source",
		}
		assert.NoError(tt, store.db.Create(sourcePool).Error())

		targetPool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "pool-target"},
			Name:           "target_pool",
			AccountID:      account.ID,
			State:          datamodel.LifeCycleStateREADY,
			DeploymentName: "deployment-target",
		}
		assert.NoError(tt, store.db.Create(targetPool).Error())

		const externalUUID = "550e8400-e29b-41d4-a716-446655440001"
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "svm-external-2"},
			Name:      "test_svm",
			PoolID:    sourcePool.ID,
			AccountID: account.ID,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: externalUUID,
				IPSpace:      "Default",
			},
		}
		assert.NoError(tt, store.db.Create(svm).Error())

		result, err := store.GetSvmByExternalUUID(context.Background(), externalUUID, targetPool.ID)
		assert.Error(tt, err)
		assert.Nil(tt, result)
	})

	t.Run("WhenMultipleSvmsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-external-3"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "pool-external-3"},
			Name:           "test_pool",
			AccountID:      account.ID,
			State:          datamodel.LifeCycleStateREADY,
			DeploymentName: "deployment-3",
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		svm1 := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "svm-external-3a"},
			Name:      "svm-1",
			PoolID:    pool.ID,
			AccountID: account.ID,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "550e8400-e29b-41d4-a716-446655440010",
				IPSpace:      "Default",
			},
		}
		assert.NoError(tt, store.db.Create(svm1).Error())

		svm2 := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "svm-external-3b"},
			Name:      "svm-2",
			PoolID:    pool.ID,
			AccountID: account.ID,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "550e8400-e29b-41d4-a716-446655440011",
				IPSpace:      "Default",
			},
		}
		assert.NoError(tt, store.db.Create(svm2).Error())

		result1, err := store.GetSvmByExternalUUID(context.Background(), svm1.SvmDetails.ExternalUUID, pool.ID)
		assert.NoError(tt, err)
		assert.Equal(tt, svm1.UUID, result1.UUID)

		result2, err := store.GetSvmByExternalUUID(context.Background(), svm2.SvmDetails.ExternalUUID, pool.ID)
		assert.NoError(tt, err)
		assert.Equal(tt, svm2.UUID, result2.UUID)

		assert.NotEqual(tt, result1.UUID, result2.UUID)
	})
}

func TestUpdateSvmCurrentKmsKeyID(t *testing.T) {
	t.Run("WhenSvmExistsWithSvmDetails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1234},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
			State:     datamodel.LifeCycleStateREADY,
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:      "test_svm",
			AccountID: account.ID,
			PoolID:    pool.ID,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID:    "external-uuid",
				IPSpace:         "Default",
				CurrentKmsKeyID: "old-key-id",
			},
		}
		assert.NoError(tt, store.db.Create(svm).Error())

		newKeyID := "new-key-id"
		err = store.UpdateSvmCurrentKmsKeyID(context.Background(), svm.UUID, newKeyID)
		assert.NoError(tt, err)

		// Verify the update
		updatedSvm := &datamodel.Svm{}
		err = store.db.GORM().First(updatedSvm, "uuid = ?", svm.UUID).Error
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedSvm.SvmDetails)
		assert.Equal(tt, newKeyID, updatedSvm.SvmDetails.CurrentKmsKeyID)
		assert.WithinDuration(tt, time.Now(), updatedSvm.UpdatedAt, 2*time.Second)
	})

	t.Run("WhenSvmExistsWithoutSvmDetails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1234},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
			State:     datamodel.LifeCycleStateREADY,
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		svm := &datamodel.Svm{
			BaseModel:  datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:       "test_svm",
			AccountID:  account.ID,
			PoolID:     pool.ID,
			SvmDetails: nil,
		}
		assert.NoError(tt, store.db.Create(svm).Error())

		newKeyID := "new-key-id"
		err = store.UpdateSvmCurrentKmsKeyID(context.Background(), svm.UUID, newKeyID)
		assert.NoError(tt, err)

		// Verify the update - SvmDetails should be initialized
		updatedSvm := &datamodel.Svm{}
		err = store.db.GORM().First(updatedSvm, "uuid = ?", svm.UUID).Error
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedSvm.SvmDetails)
		assert.Equal(tt, newKeyID, updatedSvm.SvmDetails.CurrentKmsKeyID)
		assert.WithinDuration(tt, time.Now(), updatedSvm.UpdatedAt, 2*time.Second)
	})

	t.Run("WhenSvmNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		err = store.UpdateSvmCurrentKmsKeyID(context.Background(), "non-existent-uuid", "key-id")
		assert.Error(tt, err)
		var vcpErr *vsaerrors.CustomError
		assert.True(tt, errors.As(err, &vcpErr))
		assert.Equal(tt, vsaerrors.ErrDatabaseDataReadError, vcpErr.TrackingID)
	})

	t.Run("WhenTransactionStartFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		// Close the database connection to simulate transaction start failure
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		assert.NoError(tt, sqlDB.Close())

		err = store.UpdateSvmCurrentKmsKeyID(context.Background(), "test-uuid", "key-id")
		assert.Error(tt, err)
	})

	t.Run("WhenDatabaseErrorOccurs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		assert.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1234},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
			State:     datamodel.LifeCycleStateREADY,
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:      "test_svm",
			AccountID: account.ID,
			PoolID:    pool.ID,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "external-uuid",
			},
		}
		assert.NoError(tt, store.db.Create(svm).Error())

		// Drop the table to simulate database error
		// This causes First() to fail with ErrDatabaseDataReadError
		// (Save() would fail with ErrDatabaseDataUpdateError, but First() fails first)
		err = store.db.GORM().Exec("DROP TABLE svms").Error
		assert.NoError(tt, err)

		err = store.UpdateSvmCurrentKmsKeyID(context.Background(), svm.UUID, "key-id")
		assert.Error(tt, err)
		var vcpErr *vsaerrors.CustomError
		assert.True(tt, errors.As(err, &vcpErr))
		// When table is dropped, First() fails first, so we get ErrDatabaseDataReadError
		assert.Equal(tt, vsaerrors.ErrDatabaseDataReadError, vcpErr.TrackingID)
	})
}

func TestUpdateSvmState(t *testing.T) {
	setupSvm := func(tt *testing.T, state, stateDetails, workflowID string) (*DataStoreRepository, *datamodel.Svm) {
		tt.Helper()
		db, err := SetupTestDB()
		require.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		require.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		svm := &datamodel.Svm{
			BaseModel:    datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:         "test_svm",
			PoolID:       1,
			State:        state,
			StateDetails: stateDetails,
			WorkflowID:   workflowID,
		}
		require.NoError(tt, store.db.Create(svm).Error())
		return store, svm
	}

	t.Run("UpdatesStateWithoutConditions", func(tt *testing.T) {
		store, svm := setupSvm(tt, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails, "")

		err := store.UpdateSvmState(context.Background(), svm, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails)
		require.NoError(tt, err)

		var updated datamodel.Svm
		require.NoError(tt, store.db.GORM().First(&updated, svm.ID).Error)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, updated.State)
		assert.Equal(tt, datamodel.LifeCycleStateUpdatingDetails, updated.StateDetails)
		assert.WithinDuration(tt, time.Now(), updated.UpdatedAt, 2*time.Second)
	})

	t.Run("UpdatesStateWithMatchingConditions", func(tt *testing.T) {
		store, svm := setupSvm(tt, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails, "")

		err := store.UpdateSvmState(context.Background(), svm,
			datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails,
			[]interface{}{"state = ?", datamodel.LifeCycleStateREADY},
		)
		require.NoError(tt, err)

		var updated datamodel.Svm
		require.NoError(tt, store.db.GORM().First(&updated, svm.ID).Error)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, updated.State)
	})

	t.Run("PersistsWorkflowIDWhenSet", func(tt *testing.T) {
		store, svm := setupSvm(tt, datamodel.LifeCycleStateREADY, "", "")
		svm.WorkflowID = "wf-12345"

		err := store.UpdateSvmState(context.Background(), svm, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails)
		require.NoError(tt, err)

		var updated datamodel.Svm
		require.NoError(tt, store.db.GORM().First(&updated, svm.ID).Error)
		assert.Equal(tt, "wf-12345", updated.WorkflowID)
	})

	t.Run("DoesNotOverwriteWorkflowIDWhenEmpty", func(tt *testing.T) {
		store, svm := setupSvm(tt, datamodel.LifeCycleStateREADY, "", "existing-wf")
		svm.WorkflowID = ""

		err := store.UpdateSvmState(context.Background(), svm, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails)
		require.NoError(tt, err)

		var updated datamodel.Svm
		require.NoError(tt, store.db.GORM().First(&updated, svm.ID).Error)
		assert.Equal(tt, "existing-wf", updated.WorkflowID)
	})

	t.Run("ReturnsNotFoundWhenRowMissing_NoConditions", func(tt *testing.T) {
		db, err := SetupTestDB()
		require.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		require.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 9999}}

		err = store.UpdateSvmState(context.Background(), svm, datamodel.LifeCycleStateUpdating, "")
		require.Error(tt, err)
		assert.True(tt, customerrors.IsNotFoundErr(err), "expected NotFoundErr, got %T: %v", err, err)
	})

	t.Run("ReturnsNotFoundWhenRowMissing_WithConditions", func(tt *testing.T) {
		db, err := SetupTestDB()
		require.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		require.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 9999}}

		err = store.UpdateSvmState(context.Background(), svm,
			datamodel.LifeCycleStateUpdating, "",
			[]interface{}{"state = ?", datamodel.LifeCycleStateREADY},
		)
		require.Error(tt, err)
		assert.True(tt, customerrors.IsNotFoundErr(err), "expected NotFoundErr, got %T: %v", err, err)
	})

	t.Run("ReturnsConflictWhenConditionsNotMet", func(tt *testing.T) {
		store, svm := setupSvm(tt, datamodel.LifeCycleStateError, "error occurred", "")

		err := store.UpdateSvmState(context.Background(), svm,
			datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails,
			[]interface{}{"state = ?", datamodel.LifeCycleStateREADY},
		)
		require.Error(tt, err)
		assert.True(tt, customerrors.IsConflictErr(err), "expected ConflictErr, got %T: %v", err, err)
		assert.Contains(tt, err.Error(), "current state is ERROR")
	})

	t.Run("ReturnsConflictWithINConditions", func(tt *testing.T) {
		store, svm := setupSvm(tt, datamodel.LifeCycleStateDeleting, "", "")

		err := store.UpdateSvmState(context.Background(), svm,
			datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails,
			[]interface{}{"state IN ?", []string{datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateError}},
		)
		require.Error(tt, err)
		assert.True(tt, customerrors.IsConflictErr(err), "expected ConflictErr, got %T: %v", err, err)
		assert.Contains(tt, err.Error(), "current state is DELETING")
	})

	t.Run("ReturnsDBErrorWhenUpdateFails", func(tt *testing.T) {
		store, svm := setupSvm(tt, datamodel.LifeCycleStateREADY, "", "")

		require.NoError(tt, store.db.GORM().Exec("DROP TABLE svms").Error)

		err := store.UpdateSvmState(context.Background(), svm, datamodel.LifeCycleStateUpdating, "")
		require.Error(tt, err)
		var vcpErr *vsaerrors.CustomError
		assert.True(tt, errors.As(err, &vcpErr))
		assert.Equal(tt, vsaerrors.ErrDatabaseDataUpdateError, vcpErr.TrackingID)
	})

	t.Run("ReturnsErrorWhenTransactionStartFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		require.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		sqlDB, err := db.DB()
		require.NoError(tt, err)
		require.NoError(tt, sqlDB.Close())

		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 1}}
		err = store.UpdateSvmState(context.Background(), svm, datamodel.LifeCycleStateUpdating, "")
		assert.Error(tt, err)
	})

	t.Run("SkipsEmptyConditionSlices", func(tt *testing.T) {
		store, svm := setupSvm(tt, datamodel.LifeCycleStateREADY, "", "")

		err := store.UpdateSvmState(context.Background(), svm,
			datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails,
			[]interface{}{},
		)
		require.NoError(tt, err)

		var updated datamodel.Svm
		require.NoError(tt, store.db.GORM().First(&updated, svm.ID).Error)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, updated.State)
	})
}

