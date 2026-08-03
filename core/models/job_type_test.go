package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

func TestJobTypeRotateKmsConfig(t *testing.T) {
	// Test that the new job type constant is defined correctly
	assert.Equal(t, datamodel.JobType("ROTATE_KMS_CONFIG"), datamodel.JobTypeRotateKmsConfig)
	assert.NotEmpty(t, string(datamodel.JobTypeRotateKmsConfig))
}

func TestJobTypeEncryptPool(t *testing.T) {
	assert.Equal(t, datamodel.JobType("ENCRYPT_POOL"), datamodel.JobTypeEncryptPool)
	assert.NotEmpty(t, string(datamodel.JobTypeEncryptPool))
}

func TestJobTypeConstants(t *testing.T) {
	// Test that all job types are unique strings
	jobTypes := []datamodel.JobType{
		datamodel.JobTypeCreatePool,
		datamodel.JobTypeCreateLargePool,
		datamodel.JobTypeUpdatePool,
		datamodel.JobTypeUpdateLargePool,
		datamodel.JobTypeDeletePool,
		datamodel.JobTypeDeleteLargePool,
		datamodel.JobTypeCreateSubnet,
		datamodel.JobTypeCreateLargeSubnet,
		datamodel.JobTypeCreateVolume,
		datamodel.JobTypeUpdateVolume,
		datamodel.JobTypeDeleteVolume,
		datamodel.JobTypeCreateSnapshot,
		datamodel.JobTypeDeleteSnapshot,
		datamodel.JobTypeCreateBackup,
		datamodel.JobTypeDeleteBackup,
		datamodel.JobTypeCreateBackupVault,
		datamodel.JobTypeDeleteBackupVault,
		datamodel.JobTypeCreateBackupPolicy,
		datamodel.JobTypeCreateKmsConfig,
		datamodel.JobTypeUpdateKmsConfig,
		datamodel.JobTypeDeleteKmsConfig,
		datamodel.JobTypeMigrateKmsConfig,
		datamodel.JobTypeRotateKmsConfig, // New job type
		datamodel.JobTypeEncryptPool,
	}

	// Check uniqueness
	seen := make(map[string]bool)
	for _, jobType := range jobTypes {
		strJobType := string(jobType)
		assert.False(t, seen[strJobType], "Duplicate job type found: %s", strJobType)
		seen[strJobType] = true
	}

	// Verify the new job type is in the list
	found := false
	for _, jobType := range jobTypes {
		if jobType == datamodel.JobTypeRotateKmsConfig {
			found = true
			break
		}
	}
	assert.True(t, found, "JobTypeRotateKmsConfig should be in the job types list")
}

func TestGetResourceJobType(t *testing.T) {
	t.Run("PoolOperations", func(t *testing.T) {
		// Test all pool operations with different categories
		testCases := []struct {
			name            string
			operation       ResourceOperation
			poolCategory    PoolCategory
			expectedJobType datamodel.JobType
		}{
			{"CreatePool_Standard", ResourceOperationCreate, PoolCategoryStandard, datamodel.JobTypeCreatePool},
			{"CreatePool_LargeCapacity", ResourceOperationCreate, PoolCategoryLargeCapacity, datamodel.JobTypeCreateLargePool},
			{"CreatePool_Default", ResourceOperationCreate, PoolCategoryDefault, datamodel.JobTypeCreatePool}, // Default maps to standard
			{"UpdatePool_Standard", ResourceOperationUpdate, PoolCategoryStandard, datamodel.JobTypeUpdatePool},
			{"UpdatePool_LargeCapacity", ResourceOperationUpdate, PoolCategoryLargeCapacity, datamodel.JobTypeUpdateLargePool},
			{"DeletePool_Standard", ResourceOperationDelete, PoolCategoryStandard, datamodel.JobTypeDeletePool},
			{"DeletePool_LargeCapacity", ResourceOperationDelete, PoolCategoryLargeCapacity, datamodel.JobTypeDeleteLargePool},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result := GetResourceJobType(ResourceTypePool, tc.operation, tc.poolCategory)
				assert.Equal(t, tc.expectedJobType, result,
					"Expected %s for pool %s operation with category=%s, got %s",
					tc.expectedJobType, tc.operation, tc.poolCategory, result)
			})
		}
	})

	t.Run("SubnetOperations", func(t *testing.T) {
		// Test subnet operations (CREATE and DELETE are supported)
		testCases := []struct {
			name            string
			operation       ResourceOperation
			poolCategory    PoolCategory
			expectedJobType datamodel.JobType
		}{
			{"CreateSubnet_Standard", ResourceOperationCreate, PoolCategoryStandard, datamodel.JobTypeCreateSubnet},
			{"CreateSubnet_LargeCapacity", ResourceOperationCreate, PoolCategoryLargeCapacity, datamodel.JobTypeCreateLargeSubnet},
			{"CreateSubnet_Default", ResourceOperationCreate, PoolCategoryDefault, datamodel.JobTypeCreateSubnet}, // Default maps to standard
			{"DeleteSubnet_Standard", ResourceOperationDelete, PoolCategoryStandard, datamodel.JobTypeDeleteSubnet},
			{"DeleteSubnet_LargeCapacity", ResourceOperationDelete, PoolCategoryLargeCapacity, datamodel.JobTypeCreatePool}, // Large capacity subnet delete not yet supported, falls back
			{"DeleteSubnet_Default", ResourceOperationDelete, PoolCategoryDefault, datamodel.JobTypeDeleteSubnet},           // Default maps to standard
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result := GetResourceJobType(ResourceTypeSubnet, tc.operation, tc.poolCategory)
				assert.Equal(t, tc.expectedJobType, result,
					"Expected %s for subnet %s operation with category=%s, got %s",
					tc.expectedJobType, tc.operation, tc.poolCategory, result)
			})
		}
	})

	t.Run("EdgeCases", func(t *testing.T) {
		// Test invalid resource types
		t.Run("InvalidResourceType", func(t *testing.T) {
			result := GetResourceJobType("INVALID_RESOURCE", ResourceOperationCreate, PoolCategoryStandard)
			assert.Equal(t, datamodel.JobTypeCreatePool, result, "Should fallback to JobTypeCreatePool for invalid resource type")
		})

		// Test invalid operations
		t.Run("InvalidOperation", func(t *testing.T) {
			result := GetResourceJobType(ResourceTypePool, "INVALID_OPERATION", PoolCategoryStandard)
			assert.Equal(t, datamodel.JobTypeCreatePool, result, "Should fallback to JobTypeCreatePool for invalid operation")
		})

		// Test invalid pool categories
		t.Run("InvalidPoolCategory", func(t *testing.T) {
			result := GetResourceJobType(ResourceTypePool, ResourceOperationCreate, "INVALID_CATEGORY")
			assert.Equal(t, datamodel.JobTypeCreatePool, result, "Should fallback to JobTypeCreatePool for invalid pool category")
		})

		// Test unsupported subnet operations
		t.Run("UnsupportedSubnetUpdate", func(t *testing.T) {
			result := GetResourceJobType(ResourceTypeSubnet, ResourceOperationUpdate, PoolCategoryStandard)
			assert.Equal(t, datamodel.JobTypeCreatePool, result, "Should fallback to JobTypeCreatePool for unsupported subnet update")
		})

		t.Run("SubnetDelete", func(t *testing.T) {
			result := GetResourceJobType(ResourceTypeSubnet, ResourceOperationDelete, PoolCategoryStandard)
			assert.Equal(t, datamodel.JobTypeDeleteSubnet, result, "Should return JobTypeDeleteSubnet for subnet delete")
		})

		// Test empty resource type
		t.Run("EmptyResourceType", func(t *testing.T) {
			result := GetResourceJobType("", ResourceOperationCreate, PoolCategoryStandard)
			assert.Equal(t, datamodel.JobTypeCreatePool, result, "Should fallback to JobTypeCreatePool for empty resource type")
		})

		// Test empty operation
		t.Run("EmptyOperation", func(t *testing.T) {
			result := GetResourceJobType(ResourceTypePool, "", PoolCategoryStandard)
			assert.Equal(t, datamodel.JobTypeCreatePool, result, "Should fallback to JobTypeCreatePool for empty operation")
		})
	})

	t.Run("CategoryMappingConsistency", func(t *testing.T) {
		// Test that the category-based approach is consistent across different operations
		operations := []ResourceOperation{
			ResourceOperationCreate,
			ResourceOperationUpdate,
			ResourceOperationDelete,
		}

		for _, operation := range operations {
			t.Run(string(operation)+"_CategoryMappingIsConsistent", func(t *testing.T) {
				// Test standard category
				standardResult := GetResourceJobType(ResourceTypePool, operation, PoolCategoryStandard)
				defaultResult := GetResourceJobType(ResourceTypePool, operation, PoolCategoryDefault)

				// Default should map to standard
				assert.Equal(t, standardResult, defaultResult, "Default category should map to standard category")

				// Test large capacity category
				largeResult := GetResourceJobType(ResourceTypePool, operation, PoolCategoryLargeCapacity)

				// Verify the result is a valid job type and not the fallback
				assert.NotEmpty(t, string(standardResult), "Should return a valid job type for standard")
				assert.NotEmpty(t, string(largeResult), "Should return a valid job type for large capacity")

				// Verify correct capacity handling
				assert.Contains(t, string(largeResult), "LARGE", "Large capacity should return job type containing 'LARGE'")
				assert.NotContains(t, string(standardResult), "LARGE", "Standard category should not return job type containing 'LARGE'")

				// Standard and large should be different (except for unsupported operations)
				if operation == ResourceOperationCreate || operation == ResourceOperationUpdate || operation == ResourceOperationDelete {
					assert.NotEqual(t, standardResult, largeResult, "Standard and large capacity should return different job types for %s", operation)
				}
			})
		}
	})

	t.Run("HelperFunctionConsistency", func(t *testing.T) {
		// Test that GetPoolCategory helper function works correctly
		t.Run("LargeCapacityMapping", func(t *testing.T) {
			category := GetPoolCategory(true)
			assert.Equal(t, PoolCategoryLargeCapacity, category, "Should return large capacity category for true")
		})

		t.Run("StandardCapacityMapping", func(t *testing.T) {
			category := GetPoolCategory(false)
			assert.Equal(t, PoolCategoryStandard, category, "Should return standard category for false")
		})

		t.Run("HelperFunctionMatchesManualMapping", func(t *testing.T) {
			// Test that the helper function produces the same result as manual mapping
			testCases := []bool{true, false}

			for _, isLarge := range testCases {
				helperResult := GetPoolCategory(isLarge)

				var manualResult PoolCategory
				if isLarge {
					manualResult = PoolCategoryLargeCapacity
				} else {
					manualResult = PoolCategoryStandard
				}

				assert.Equal(t, manualResult, helperResult,
					"Helper function should match manual mapping for isLarge=%t", isLarge)
			}
		})
	})

	t.Run("ConstantValidation", func(t *testing.T) {
		// Test that all resource type and operation constants are properly defined
		t.Run("ResourceTypeConstants", func(t *testing.T) {
			assert.Equal(t, ResourceType("POOL"), ResourceTypePool)
			assert.Equal(t, ResourceType("SUBNET"), ResourceTypeSubnet)
		})

		t.Run("ResourceOperationConstants", func(t *testing.T) {
			assert.Equal(t, ResourceOperation("CREATE"), ResourceOperationCreate)
			assert.Equal(t, ResourceOperation("UPDATE"), ResourceOperationUpdate)
			assert.Equal(t, ResourceOperation("DELETE"), ResourceOperationDelete)
		})

		t.Run("PoolCategoryConstants", func(t *testing.T) {
			assert.Equal(t, PoolCategory("standardPool"), PoolCategoryStandard)
			assert.Equal(t, PoolCategory("largeCapacityPool"), PoolCategoryLargeCapacity)
			assert.Equal(t, PoolCategory("default"), PoolCategoryDefault)
		})
	})

	t.Run("FunctionBehavior", func(t *testing.T) {
		// Test that the function is deterministic
		t.Run("Deterministic", func(t *testing.T) {
			// Same inputs should always return the same outputs
			operation := ResourceOperationCreate
			category := PoolCategoryLargeCapacity

			result1 := GetResourceJobType(ResourceTypePool, operation, category)
			result2 := GetResourceJobType(ResourceTypePool, operation, category)
			result3 := GetResourceJobType(ResourceTypePool, operation, category)

			assert.Equal(t, result1, result2, "Function should be deterministic")
			assert.Equal(t, result2, result3, "Function should be deterministic")
		})

		// Test pool category effect
		t.Run("PoolCategoryEffect", func(t *testing.T) {
			// For each resource type and operation, different categories should return different job types
			testCases := []struct {
				resourceType ResourceType
				operation    ResourceOperation
			}{
				{ResourceTypePool, ResourceOperationCreate},
				{ResourceTypePool, ResourceOperationUpdate},
				{ResourceTypePool, ResourceOperationDelete},
				{ResourceTypeSubnet, ResourceOperationCreate},
			}

			for _, tc := range testCases {
				t.Run(string(tc.resourceType)+"_"+string(tc.operation), func(t *testing.T) {
					standardResult := GetResourceJobType(tc.resourceType, tc.operation, PoolCategoryStandard)
					largeCapacityResult := GetResourceJobType(tc.resourceType, tc.operation, PoolCategoryLargeCapacity)
					defaultResult := GetResourceJobType(tc.resourceType, tc.operation, PoolCategoryDefault)

					assert.NotEqual(t, standardResult, largeCapacityResult,
						"Standard and large capacity categories should return different job types for %s %s",
						tc.resourceType, tc.operation)

					// Default should equal standard
					assert.Equal(t, standardResult, defaultResult,
						"Default category should return same result as standard category for %s %s",
						tc.resourceType, tc.operation)

					// Standard should not contain "LARGE"
					assert.NotContains(t, string(standardResult), "LARGE",
						"Standard category job type should not contain 'LARGE'")

					// Large capacity should contain "LARGE"
					assert.Contains(t, string(largeCapacityResult), "LARGE",
						"Large capacity category job type should contain 'LARGE'")
				})
			}
		})
	})
}

func TestGetResourceJobType_Comprehensive(t *testing.T) {
	// Comprehensive test matrix covering all combinations
	type testCase struct {
		resourceType    ResourceType
		operation       ResourceOperation
		poolCategory    PoolCategory
		expectedJobType datamodel.JobType
		shouldSucceed   bool
		description     string
	}

	testCases := []testCase{
		// Pool operations - all should succeed
		{ResourceTypePool, ResourceOperationCreate, PoolCategoryStandard, datamodel.JobTypeCreatePool, true, "Pool create standard"},
		{ResourceTypePool, ResourceOperationCreate, PoolCategoryLargeCapacity, datamodel.JobTypeCreateLargePool, true, "Pool create large capacity"},
		{ResourceTypePool, ResourceOperationCreate, PoolCategoryDefault, datamodel.JobTypeCreatePool, true, "Pool create default (maps to standard)"},
		{ResourceTypePool, ResourceOperationUpdate, PoolCategoryStandard, datamodel.JobTypeUpdatePool, true, "Pool update standard"},
		{ResourceTypePool, ResourceOperationUpdate, PoolCategoryLargeCapacity, datamodel.JobTypeUpdateLargePool, true, "Pool update large capacity"},
		{ResourceTypePool, ResourceOperationDelete, PoolCategoryStandard, datamodel.JobTypeDeletePool, true, "Pool delete standard"},
		{ResourceTypePool, ResourceOperationDelete, PoolCategoryLargeCapacity, datamodel.JobTypeDeleteLargePool, true, "Pool delete large capacity"},

		// Subnet operations - only CREATE or DELETE should succeed
		{ResourceTypeSubnet, ResourceOperationCreate, PoolCategoryStandard, datamodel.JobTypeCreateSubnet, true, "Subnet create standard"},
		{ResourceTypeSubnet, ResourceOperationCreate, PoolCategoryLargeCapacity, datamodel.JobTypeCreateLargeSubnet, true, "Subnet create large capacity"},
		{ResourceTypeSubnet, ResourceOperationCreate, PoolCategoryDefault, datamodel.JobTypeCreateSubnet, true, "Subnet create default (maps to standard)"},
		{ResourceTypeSubnet, ResourceOperationUpdate, PoolCategoryStandard, datamodel.JobTypeCreatePool, false, "Subnet update not supported"},
		{ResourceTypeSubnet, ResourceOperationDelete, PoolCategoryStandard, datamodel.JobTypeDeleteSubnet, true, "Subnet delete standard"},

		// Invalid cases - should fallback to JobTypeCreatePool
		{"INVALID", ResourceOperationCreate, PoolCategoryStandard, datamodel.JobTypeCreatePool, false, "Invalid resource type"},
		{ResourceTypePool, "INVALID", PoolCategoryStandard, datamodel.JobTypeCreatePool, false, "Invalid operation"},
		{ResourceTypePool, ResourceOperationCreate, "INVALID_CATEGORY", datamodel.JobTypeCreatePool, false, "Invalid pool category"},
		{"", ResourceOperationCreate, PoolCategoryStandard, datamodel.JobTypeCreatePool, false, "Empty resource type"},
		{ResourceTypePool, "", PoolCategoryStandard, datamodel.JobTypeCreatePool, false, "Empty operation"},
	}

	for i, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			result := GetResourceJobType(tc.resourceType, tc.operation, tc.poolCategory)
			assert.Equal(t, tc.expectedJobType, result,
				"Test case %d: %s - Expected %s, got %s",
				i+1, tc.description, tc.expectedJobType, result)

			// Additional validation for fallback behavior
			if !tc.shouldSucceed {
				// Invalid cases should always fallback to JobTypeCreatePool
				assert.Equal(t, datamodel.JobTypeCreatePool, result,
					"Invalid case should fallback to JobTypeCreatePool")
			}
			// Valid cases are already validated by the assert.Equal above
		})
	}
}
