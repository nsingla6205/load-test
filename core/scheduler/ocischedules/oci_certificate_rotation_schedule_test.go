package ocischedules

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
)

// mockScheduleHandle is a minimal client.ScheduleHandle whose behaviour is
// driven by injected funcs.
type mockScheduleHandle struct {
	client.ScheduleHandle
	id           string
	describeFunc func(ctx context.Context) (*client.ScheduleDescription, error)
	updateFunc   func(ctx context.Context, opts client.ScheduleUpdateOptions) error
	deleteFunc   func(ctx context.Context) error
}

func (m *mockScheduleHandle) GetID() string { return m.id }

func (m *mockScheduleHandle) Describe(ctx context.Context) (*client.ScheduleDescription, error) {
	if m.describeFunc != nil {
		return m.describeFunc(ctx)
	}
	return &client.ScheduleDescription{}, nil
}

func (m *mockScheduleHandle) Update(ctx context.Context, opts client.ScheduleUpdateOptions) error {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, opts)
	}
	return nil
}

func (m *mockScheduleHandle) Delete(ctx context.Context) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx)
	}
	return nil
}

func setRotationEnabled(t *testing.T, enabled bool) {
	t.Helper()
	orig := env.EnableOCIVsaCertificateRotation
	env.EnableOCIVsaCertificateRotation = enabled
	t.Cleanup(func() { env.EnableOCIVsaCertificateRotation = orig })
}

// describeNotFound simulates a schedule that does not exist yet.
func describeNotFound(context.Context) (*client.ScheduleDescription, error) {
	return nil, errors.New("schedule not found")
}

// describeFound simulates an existing schedule.
func describeFound(context.Context) (*client.ScheduleDescription, error) {
	return &client.ScheduleDescription{}, nil
}

func TestEnsureOciCertificateRotationSchedule_Disabled_NoExistingSchedule(t *testing.T) {
	setRotationEnabled(t, false)
	ctx := context.Background()

	schedCl := &mocks.ScheduleClient{}
	handle := &mockScheduleHandle{id: OciCertificateRotationScheduleID, describeFunc: describeNotFound}
	schedCl.On("GetHandle", ctx, OciCertificateRotationScheduleID).Return(handle).Once()

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciCertificateRotationSchedule(ctx, cl, log.NewLogger())
	require.NoError(t, err)
	schedCl.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	schedCl.AssertExpectations(t)
}

func TestEnsureOciCertificateRotationSchedule_Disabled_DeletesExisting(t *testing.T) {
	setRotationEnabled(t, false)
	ctx := context.Background()

	var deleted bool
	handle := &mockScheduleHandle{
		id:           OciCertificateRotationScheduleID,
		describeFunc: describeFound,
		deleteFunc: func(context.Context) error {
			deleted = true
			return nil
		},
	}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, OciCertificateRotationScheduleID).Return(handle)

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciCertificateRotationSchedule(ctx, cl, log.NewLogger())
	require.NoError(t, err)
	require.True(t, deleted, "existing schedule must be deleted when rotation is disabled")
}

func TestEnsureOciCertificateRotationSchedule_Enabled_CreatesWhenMissing(t *testing.T) {
	setRotationEnabled(t, true)
	ctx := context.Background()

	handle := &mockScheduleHandle{id: OciCertificateRotationScheduleID, describeFunc: describeNotFound}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, OciCertificateRotationScheduleID).Return(handle).Once()
	schedCl.On("Create", ctx, mock.Anything).Return(handle, nil).Once()

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciCertificateRotationSchedule(ctx, cl, log.NewLogger())
	require.NoError(t, err)
	schedCl.AssertExpectations(t)
}

func TestEnsureOciCertificateRotationSchedule_Enabled_UpdatesWhenPresent(t *testing.T) {
	setRotationEnabled(t, true)
	ctx := context.Background()

	var updated bool
	handle := &mockScheduleHandle{
		id:           OciCertificateRotationScheduleID,
		describeFunc: describeFound,
		updateFunc: func(context.Context, client.ScheduleUpdateOptions) error {
			updated = true
			return nil
		},
	}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, OciCertificateRotationScheduleID).Return(handle)

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciCertificateRotationSchedule(ctx, cl, log.NewLogger())
	require.NoError(t, err)
	require.True(t, updated, "existing schedule must be synced when rotation is enabled")
	schedCl.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestEnsureOciCertificateRotationSchedule_Enabled_CreateAlreadyExistsTolerated(t *testing.T) {
	setRotationEnabled(t, true)
	ctx := context.Background()

	handle := &mockScheduleHandle{id: OciCertificateRotationScheduleID, describeFunc: describeNotFound}
	schedCl := &mocks.ScheduleClient{}
	schedCl.On("GetHandle", ctx, OciCertificateRotationScheduleID).Return(handle).Once()
	// Create retries internally before returning, so allow multiple invocations.
	schedCl.On("Create", ctx, mock.Anything).Return(nil, errors.New("schedule already running"))

	cl := &mocks.Client{}
	cl.On("ScheduleClient").Return(schedCl)

	err := EnsureOciCertificateRotationSchedule(ctx, cl, log.NewLogger())
	require.NoError(t, err, "a concurrent create race must be tolerated")
	schedCl.AssertExpectations(t)
}
