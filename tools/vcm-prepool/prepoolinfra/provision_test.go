package prepoolinfra

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

func Test_CollectCleanupError_NilAndNonNil_JoinsFailures(t *testing.T) {
	var errs []error
	errs = collectCleanupError(errs, "delete VSA cluster", nil)
	errs = collectCleanupError(errs, "delete service account", errors.New("sa missing"))
	errs = collectCleanupError(errs, "delete auto tier bucket", nil)
	errs = collectCleanupError(errs, "delete ONTAP credentials", errors.New("secret missing"))

	joined := errors.Join(errs...)
	require.Error(t, joined)
	assert.ErrorContains(t, joined, "delete service account: sa missing")
	assert.ErrorContains(t, joined, "delete ONTAP credentials: secret missing")
	assert.NotContains(t, joined.Error(), "delete VSA cluster")
	assert.NotContains(t, joined.Error(), "delete auto tier bucket")
}

func Test_WaitForOperation_CompletedOperation_ReturnsResult(t *testing.T) {
	expected := &models.ComputeOperation{Done: true}

	actual, err := waitForOperation(context.Background(), "VPC", "operation-1", func() (*models.ComputeOperation, error) {
		return expected, nil
	})

	require.NoError(t, err)
	assert.Same(t, expected, actual)
}

func Test_WaitForOperation_CancelledContext_ReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	getterCalled := false

	_, err := waitForOperation(ctx, "subnet", "operation-1", func() (*models.ComputeOperation, error) {
		getterCalled = true
		return nil, nil
	})

	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, getterCalled)
}

func Test_WaitForOperation_ProviderFailure_ReturnsError(t *testing.T) {
	_, err := waitForOperation(context.Background(), "firewall", "operation-1", func() (*models.ComputeOperation, error) {
		return &models.ComputeOperation{Done: true, ErrorResponse: "permission denied"}, nil
	})

	assert.ErrorContains(t, err, "permission denied")
}

func Test_LabelFilter_RendersComputeFilterExpression(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		expect string
	}{
		{"empty", map[string]string{}, ""},
		{"single", map[string]string{"deploymentId": "gcnv-abc"}, `labels.deploymentId = "gcnv-abc"`},
		{
			"multiple sorted",
			map[string]string{"role": "node", "deploymentId": "gcnv-abc"},
			`labels.deploymentId = "gcnv-abc" AND labels.role = "node"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, labelFilter(tt.labels))
		})
	}
}

func Test_ResourceID_StripsSelfLink(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"short name", "australia-southeast1-c", "australia-southeast1-c"},
		{
			"zone self-link",
			"https://www.googleapis.com/compute/v1/projects/m453c8dff8a05cc69-tp/zones/australia-southeast1-c",
			"australia-southeast1-c",
		},
		{
			"region self-link",
			"https://www.googleapis.com/compute/v1/projects/m453c8dff8a05cc69-tp/regions/australia-southeast1",
			"australia-southeast1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resourceID(tt.input))
		})
	}
}

func Test_RetryTransient_InternalErrorThenSuccess_Succeeds(t *testing.T) {
	internalErr := &googleapi.Error{Code: http.StatusInternalServerError, Message: "An internal error occurred."}
	attempts := 0

	err := retryTransient(context.Background(), "attach role", func() error {
		attempts++
		if attempts == 1 {
			return internalErr
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
}

func Test_RetryTransient_NonRetryableError_FailsImmediately(t *testing.T) {
	forbidden := &googleapi.Error{Code: http.StatusForbidden, Message: "permission denied"}
	attempts := 0

	err := retryTransient(context.Background(), "attach role", func() error {
		attempts++
		return forbidden
	})

	assert.ErrorIs(t, err, forbidden)
	assert.Equal(t, 1, attempts)
}

func Test_RetryTransient_CancelledContext_StopsRetrying(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts := 0

	err := retryTransient(ctx, "attach role", func() error {
		attempts++
		return errors.New("unreachable")
	})

	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, attempts)
}

func Test_StartDeletion_BadGatewayThenNotFound_TreatsResourceAsDeleted(t *testing.T) {
	badGateway := &googleapi.Error{Code: http.StatusBadGateway, Message: "server error"}
	notFound := &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
	attempts := 0

	operation, err := startDeletion(context.Background(), "delete IP address addr-1", func() (*compute.Operation, error) {
		attempts++
		if attempts == 1 {
			return nil, badGateway
		}
		return nil, notFound
	})

	require.NoError(t, err)
	assert.Nil(t, operation)
	assert.Equal(t, 2, attempts)
}

func Test_StartDeletion_BadGatewayThenSuccess_ReturnsOperation(t *testing.T) {
	badGateway := &googleapi.Error{Code: http.StatusBadGateway, Message: "server error"}
	attempts := 0

	operation, err := startDeletion(context.Background(), "delete IP address addr-1", func() (*compute.Operation, error) {
		attempts++
		if attempts == 1 {
			return nil, badGateway
		}
		return &compute.Operation{Name: "operation-1"}, nil
	})

	require.NoError(t, err)
	require.NotNil(t, operation)
	assert.Equal(t, "operation-1", operation.Name)
}

func Test_PrePoolInfra_JSONProjectNumbers(t *testing.T) {
	raw, err := json.Marshal(PrePoolInfra{
		TenantProjectNumber: 402775639164,
		SNHostProjectNumber: 142516265915,
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, float64(402775639164), decoded["tenantProjectNumber"])
	assert.Equal(t, float64(142516265915), decoded["snHostProjectNumber"])
	_, hasOldSNHost := decoded["snHostProject"]
	assert.False(t, hasOldSNHost)
}

func Test_ParseProjectNumber(t *testing.T) {
	n, err := parseProjectNumber(" 402775639164 ")
	require.NoError(t, err)
	assert.Equal(t, int64(402775639164), n)

	_, err = parseProjectNumber("host-project-id")
	assert.Error(t, err)
}

func Test_ResolveProjectNumber_NumericSkipsLookup(t *testing.T) {
	n, err := (&provisioner{}).resolveProjectNumber("142516265915")
	require.NoError(t, err)
	assert.Equal(t, int64(142516265915), n)

	_, err = (&provisioner{}).resolveProjectNumber("  ")
	assert.ErrorContains(t, err, "project is empty")
}

func Test_StartDeletion_NonRetryableError_Fails(t *testing.T) {
	forbidden := &googleapi.Error{Code: http.StatusForbidden, Message: "permission denied"}

	operation, err := startDeletion(context.Background(), "delete IP address addr-1", func() (*compute.Operation, error) {
		return nil, forbidden
	})

	assert.ErrorIs(t, err, forbidden)
	assert.Nil(t, operation)
}
