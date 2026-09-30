package names

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_DataSubnetPrefixLength_Capacity_ReturnsStandardAndLVMasks(t *testing.T) {
	assert.Equal(t, 28, DataSubnetPrefixLength(false))
	assert.Equal(t, 26, DataSubnetPrefixLength(true))
}

func Test_GenerateDataSubnetPrefix_TenantIDs_IncludesTrailingHyphen(t *testing.T) {
	assert.Equal(t, "vsa-123-", GenerateDataSubnetPrefix("123"))
	assert.False(t, strings.HasPrefix("vsa-1234-bbbb", GenerateDataSubnetPrefix("123")))
	assert.True(t, strings.HasPrefix("vsa-123-cccc", GenerateDataSubnetPrefix("123")))
}

func Test_GenerateDataSubnetName_ConsecutiveCalls_ReturnUniqueNames(t *testing.T) {
	first := GenerateDataSubnetName("123456789012")
	second := GenerateDataSubnetName("123456789012")

	assert.NotEqual(t, first, second)
	assert.Contains(t, first, "vsa-123456789012-")
}

func Test_GenerateServiceAccountInfo_DeploymentNames_ReturnValidStableIDs(t *testing.T) {
	tests := []struct {
		name           string
		deploymentName string
		wantError      bool
	}{
		{name: "short name", deploymentName: "gcnv-test"},
		{name: "max length", deploymentName: "gcnv-123456789012345"},
		{name: "too long", deploymentName: "gcnv-1234567890123456", wantError: true},
		{name: "invalid uppercase", deploymentName: "GCNV-test", wantError: true},
		{name: "invalid character", deploymentName: "gcnv_test", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			firstID, firstEmail, err := GenerateServiceAccountInfo(test.deploymentName, "tenant-project")
			if test.wantError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			secondID, secondEmail, err := GenerateServiceAccountInfo(test.deploymentName, "tenant-project")
			require.NoError(t, err)
			assert.Equal(t, firstID, secondID)
			assert.Equal(t, firstEmail, secondEmail)
			assert.LessOrEqual(t, len(firstID), serviceAccountIDMax)
			assert.Equal(t, firstID+"@tenant-project.iam.gserviceaccount.com", firstEmail)
		})
	}
}

func Test_GenerateBucketName_Deployment_ReturnsStableName(t *testing.T) {
	got := GenerateBucketName("gcnv-8d0ff6a0b456f30")
	assert.Equal(t, "gcnv-8d0ff6a0b456f30-vcm-at-bucket", got)
	assert.LessOrEqual(t, len(got), 63)
}

func Test_ValidateDeploymentName_Inputs_ReturnExpectedResult(t *testing.T) {
	tests := []struct {
		name           string
		deploymentName string
		wantError      bool
	}{
		{name: "single char", deploymentName: "a"},
		{name: "twenty chars", deploymentName: strings.Repeat("a", MaxDeploymentNameLength)},
		{name: "twenty one chars", deploymentName: strings.Repeat("a", MaxDeploymentNameLength+1), wantError: true},
		{name: "leading hyphen", deploymentName: "-gcnv", wantError: true},
		{name: "trailing hyphen", deploymentName: "gcnv-", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateDeploymentName(test.deploymentName)
			if test.wantError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func Test_GenerateServiceAccountInfo_SimilarNames_ReturnDistinctIDs(t *testing.T) {
	firstID, _, err := GenerateServiceAccountInfo("gcnv-12345678901234a", "tenant-project")
	require.NoError(t, err)
	secondID, _, err := GenerateServiceAccountInfo("gcnv-12345678901234b", "tenant-project")
	require.NoError(t, err)

	assert.NotEqual(t, firstID, secondID)
}
