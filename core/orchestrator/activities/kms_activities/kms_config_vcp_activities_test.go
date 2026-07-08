package kms_activities

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/testsuite"
)

func TestGetShortRegion(t *testing.T) {
	t.Run("WhenGetShortRegionSuccess", func(t *testing.T) {
		shortRegion := utils.GetShortRegion("us-east1")
		assert.Equal(t, "usea1", shortRegion)
	})
	t.Run("WhenRegionWithoutNumber", func(t *testing.T) {
		shortRegion := utils.GetShortRegion("us-east")
		assert.Equal(t, "usea0", shortRegion)
	})
	t.Run("WhenRegionLengthIsTwo", func(t *testing.T) {
		shortRegion := utils.GetShortRegion("us")
		assert.Equal(t, "us0", shortRegion)
	})
	t.Run("WhenRegionWithoutHyphens", func(t *testing.T) {
		shortRegion := utils.GetShortRegion("europe")
		assert.Equal(t, "eu0", shortRegion)
	})
	t.Run("WhenRegionBlank", func(t *testing.T) {
		shortRegion := utils.GetShortRegion("")
		assert.Equal(t, "", shortRegion)
	})
	t.Run("DefaultOverrideForAsiaSoutheast", func(t *testing.T) {
		assert.Equal(t, "asse1", utils.GetShortRegion("asia-southeast1"))
		assert.Equal(t, "asse2", utils.GetShortRegion("asia-southeast2"))
	})
}

func TestGenerateVCPServiceAccountID(t *testing.T) {
	t.Run("GeneratesCorrectID", func(t *testing.T) {
		saID, err := _generateVCPServiceAccountID("123456789012", "us-east1")
		assert.NoError(t, err)
		assert.Equal(t, "cmek-usea1-123456789012", saID)
	})

	t.Run("ReturnsError_WhenRegionEmpty", func(t *testing.T) {
		_, err := _generateVCPServiceAccountID("123456789012", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to derive short region")
	})

	t.Run("ReturnsError_WhenIDExceeds30Chars", func(t *testing.T) {
		_, err := _generateVCPServiceAccountID("12345678901234567890", "us-east1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds GCP 30 char limit")
	})

	t.Run("GeneratesIDWithinLimit", func(t *testing.T) {
		saID, err := _generateVCPServiceAccountID("123456789012", "africa-south1")
		assert.NoError(t, err)
		assert.LessOrEqual(t, len(saID), 30)
		assert.Equal(t, "cmek-afso1-123456789012", saID)
	})

	t.Run("GeneratesIDForEuropeWest1", func(t *testing.T) {
		saID, err := _generateVCPServiceAccountID("123456789012", "europe-west1")
		assert.NoError(t, err)
		assert.Equal(t, "cmek-euwe1-123456789012", saID)
	})
}

func TestEnableGCPServiceAccountActivity(t *testing.T) {
	t.Run("SuccessfullyEnablesServiceAccount", func(t *testing.T) {
		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.EnableGCPServiceAccountActivity)

		origGetGcpService := getGcpService
		origEnable := gcpEnableServiceAccount
		defer func() {
			getGcpService = origGetGcpService
			gcpEnableServiceAccount = origEnable
		}()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		gcpEnableServiceAccount = func(gcpService hyperscaler2.Services, saEmail string) error {
			assert.Equal(t, "cmek-usea1-123456789@cmek-project.iam.gserviceaccount.com", saEmail)
			return nil
		}

		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes: &datamodel.KmsAttributes{
				VcpServiceAccountEmail: "cmek-usea1-123456789@cmek-project.iam.gserviceaccount.com",
			},
		}

		_, err := env.ExecuteActivity(activity.EnableGCPServiceAccountActivity, kmsConfig)
		assert.NoError(t, err)
	})

	t.Run("SkipsWhenKmsAttributesNil", func(t *testing.T) {
		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.EnableGCPServiceAccountActivity)

		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes: nil,
		}

		_, err := env.ExecuteActivity(activity.EnableGCPServiceAccountActivity, kmsConfig)
		assert.NoError(t, err)
	})

	t.Run("SkipsWhenVcpServiceAccountEmailEmpty", func(t *testing.T) {
		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.EnableGCPServiceAccountActivity)

		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes: &datamodel.KmsAttributes{
				VcpServiceAccountEmail: "",
			},
		}

		_, err := env.ExecuteActivity(activity.EnableGCPServiceAccountActivity, kmsConfig)
		assert.NoError(t, err)
	})

	t.Run("ReturnsErrorWhenGetGcpServiceFails", func(t *testing.T) {
		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.EnableGCPServiceAccountActivity)

		origGetGcpService := getGcpService
		defer func() { getGcpService = origGetGcpService }()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("gcp service unavailable")
		}

		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes: &datamodel.KmsAttributes{
				VcpServiceAccountEmail: "cmek-usea1-123456789@cmek-project.iam.gserviceaccount.com",
			},
		}

		_, err := env.ExecuteActivity(activity.EnableGCPServiceAccountActivity, kmsConfig)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Internal error while enabling KMS service account")
	})

	t.Run("ReturnsErrorWhenEnableServiceAccountFails", func(t *testing.T) {
		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.EnableGCPServiceAccountActivity)

		origGetGcpService := getGcpService
		origEnable := gcpEnableServiceAccount
		defer func() {
			getGcpService = origGetGcpService
			gcpEnableServiceAccount = origEnable
		}()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		gcpEnableServiceAccount = func(gcpService hyperscaler2.Services, saEmail string) error {
			return errors.New("IAM enable failed")
		}

		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes: &datamodel.KmsAttributes{
				VcpServiceAccountEmail: "cmek-usea1-123456789@cmek-project.iam.gserviceaccount.com",
			},
		}

		_, err := env.ExecuteActivity(activity.EnableGCPServiceAccountActivity, kmsConfig)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Internal error while enabling KMS service account")
	})
}

func TestDisableGCPServiceAccountActivity(t *testing.T) {
	t.Run("SuccessfullyDisablesServiceAccount", func(t *testing.T) {
		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.DisableGCPServiceAccountActivity)

		origGetGcpService := getGcpService
		origDisable := gcpDisableServiceAccount
		defer func() {
			getGcpService = origGetGcpService
			gcpDisableServiceAccount = origDisable
		}()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		gcpDisableServiceAccount = func(gcpService hyperscaler2.Services, saEmail string) error {
			assert.Equal(t, "cmek-usea1-123456789@cmek-project.iam.gserviceaccount.com", saEmail)
			return nil
		}

		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes: &datamodel.KmsAttributes{
				VcpServiceAccountEmail: "cmek-usea1-123456789@cmek-project.iam.gserviceaccount.com",
			},
		}

		_, err := env.ExecuteActivity(activity.DisableGCPServiceAccountActivity, kmsConfig)
		assert.NoError(t, err)
	})

	t.Run("SkipsWhenKmsAttributesNil", func(t *testing.T) {
		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.DisableGCPServiceAccountActivity)

		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes: nil,
		}

		_, err := env.ExecuteActivity(activity.DisableGCPServiceAccountActivity, kmsConfig)
		assert.NoError(t, err)
	})

	t.Run("SkipsWhenVcpServiceAccountEmailEmpty", func(t *testing.T) {
		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.DisableGCPServiceAccountActivity)

		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes: &datamodel.KmsAttributes{
				VcpServiceAccountEmail: "",
			},
		}

		_, err := env.ExecuteActivity(activity.DisableGCPServiceAccountActivity, kmsConfig)
		assert.NoError(t, err)
	})

	t.Run("ReturnsErrorWhenGetGcpServiceFails", func(t *testing.T) {
		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.DisableGCPServiceAccountActivity)

		origGetGcpService := getGcpService
		defer func() { getGcpService = origGetGcpService }()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("gcp service unavailable")
		}

		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes: &datamodel.KmsAttributes{
				VcpServiceAccountEmail: "cmek-usea1-123456789@cmek-project.iam.gserviceaccount.com",
			},
		}

		_, err := env.ExecuteActivity(activity.DisableGCPServiceAccountActivity, kmsConfig)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Internal error while disabling KMS service account")
	})

	t.Run("ReturnsErrorWhenDisableServiceAccountFails", func(t *testing.T) {
		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.DisableGCPServiceAccountActivity)

		origGetGcpService := getGcpService
		origDisable := gcpDisableServiceAccount
		defer func() {
			getGcpService = origGetGcpService
			gcpDisableServiceAccount = origDisable
		}()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		gcpDisableServiceAccount = func(gcpService hyperscaler2.Services, saEmail string) error {
			return errors.New("IAM disable failed")
		}

		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes: &datamodel.KmsAttributes{
				VcpServiceAccountEmail: "cmek-usea1-123456789@cmek-project.iam.gserviceaccount.com",
			},
		}

		_, err := env.ExecuteActivity(activity.DisableGCPServiceAccountActivity, kmsConfig)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Internal error while disabling KMS service account")
	})
}

func TestCreateGCPServiceAccountActivity(t *testing.T) {
	t.Run("ReturnsNonRetryableErrorWhenCmekGlobalProjectIDEmpty", func(t *testing.T) {
		act := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(act.CreateGCPServiceAccountActivity)

		origCmekProjectID := utils.CmekGlobalProjectID
		defer func() { utils.CmekGlobalProjectID = origCmekProjectID }()
		utils.CmekGlobalProjectID = ""

		kmsConfig := &datamodel.KmsConfig{
			BaseModel:         datamodel.BaseModel{UUID: "kms-uuid"},
			CustomerProjectID: "123456789",
			KeyRingLocation:   "us-east1",
			KmsAttributes:     &datamodel.KmsAttributes{CreationMode: datamodel.KmsCreationModeVCP},
		}

		_, err := env.ExecuteActivity(act.CreateGCPServiceAccountActivity, kmsConfig)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "CMEK service configuration is invalid")
		assert.Contains(t, err.Error(), "CMEK_GLOBAL_PROJECT_ID is not configured")
		assert.Contains(t, err.Error(), "retryable: false")
	})

	t.Run("ReturnsNonRetryableErrorWhenSAIDGenerationFails", func(t *testing.T) {
		act := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(act.CreateGCPServiceAccountActivity)

		origCmekProjectID := utils.CmekGlobalProjectID
		origGenerateID := generateVCPServiceAccountID
		defer func() {
			utils.CmekGlobalProjectID = origCmekProjectID
			generateVCPServiceAccountID = origGenerateID
		}()
		utils.CmekGlobalProjectID = "cmek-project"
		generateVCPServiceAccountID = func(projectNumber, keyRingLocation string) (string, error) {
			return "", errors.New("region not found")
		}

		kmsConfig := &datamodel.KmsConfig{
			BaseModel:         datamodel.BaseModel{UUID: "kms-uuid"},
			CustomerProjectID: "123456789",
			KeyRingLocation:   "bad-region",
			KmsAttributes:     &datamodel.KmsAttributes{CreationMode: datamodel.KmsCreationModeVCP},
		}

		_, err := env.ExecuteActivity(act.CreateGCPServiceAccountActivity, kmsConfig)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Internal error while creating KMS service account")
		assert.Contains(t, err.Error(), "retryable: false")
	})

	t.Run("ReturnsRetryableErrorWhenGetGcpServiceFails", func(t *testing.T) {
		act := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(act.CreateGCPServiceAccountActivity)

		origCmekProjectID := utils.CmekGlobalProjectID
		origGenerateID := generateVCPServiceAccountID
		origGetGcpService := getGcpService
		defer func() {
			utils.CmekGlobalProjectID = origCmekProjectID
			generateVCPServiceAccountID = origGenerateID
			getGcpService = origGetGcpService
		}()
		utils.CmekGlobalProjectID = "cmek-project"
		generateVCPServiceAccountID = func(projectNumber, keyRingLocation string) (string, error) {
			return "cmek-usea1-123456789", nil
		}
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("gcp service unavailable")
		}

		kmsConfig := &datamodel.KmsConfig{
			BaseModel:         datamodel.BaseModel{UUID: "kms-uuid"},
			CustomerProjectID: "123456789",
			KeyRingLocation:   "us-east1",
			KmsAttributes:     &datamodel.KmsAttributes{CreationMode: datamodel.KmsCreationModeVCP},
		}

		_, err := env.ExecuteActivity(act.CreateGCPServiceAccountActivity, kmsConfig)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Internal error while creating KMS service account")
	})
}
