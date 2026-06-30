package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
)

type mockScheduleHandle struct {
	client.ScheduleHandle
	id           string
	updateFunc   func(ctx context.Context, opts client.ScheduleUpdateOptions) error
	deleteFunc   func(ctx context.Context) error
	pauseFunc    func(ctx context.Context, opts client.SchedulePauseOptions) error
	unpauseFunc  func(ctx context.Context, opts client.ScheduleUnpauseOptions) error
	describeFunc func(ctx context.Context) (*client.ScheduleDescription, error)
}

func (m *mockScheduleHandle) GetID() string { return m.id }

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

func (m *mockScheduleHandle) Pause(ctx context.Context, opts client.SchedulePauseOptions) error {
	if m.pauseFunc != nil {
		return m.pauseFunc(ctx, opts)
	}
	return nil
}

func (m *mockScheduleHandle) Unpause(ctx context.Context, opts client.ScheduleUnpauseOptions) error {
	if m.unpauseFunc != nil {
		return m.unpauseFunc(ctx, opts)
	}
	return nil
}

func (m *mockScheduleHandle) Describe(ctx context.Context) (*client.ScheduleDescription, error) {
	if m.describeFunc != nil {
		return m.describeFunc(ctx)
	}
	return &client.ScheduleDescription{}, nil
}

func TestTemporalScheduler_Create(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"
	mockHandle := &mockScheduleHandle{id: scheduleID}

	mockClient.On("Create", ctx, mock.Anything).Return(mockHandle, nil).Once()

	params := CreateScheduleParams{
		ScheduleParams: ScheduleParams{
			ScheduleID: scheduleID,
			Args:       []interface{}{"arg1"},
		},
		TemporalScheduleOptions: TemporalCreateScheduleParams{
			WorkflowID: "wf-id",
			Workflow:   "wf",
			Spec:       client.ScheduleSpec{},
		},
	}

	resp, err := scheduler.Create(ctx, params)
	require.NoError(t, err)
	require.Equal(t, scheduleID, resp.ID)
	require.Equal(t, ScheduleStateActive, resp.Status)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Create_DefaultsToBackgroundTaskQueue(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"
	mockHandle := &mockScheduleHandle{id: scheduleID}

	var captured client.ScheduleOptions
	mockClient.On("Create", ctx, mock.MatchedBy(func(opts client.ScheduleOptions) bool {
		captured = opts
		return true
	})).Return(mockHandle, nil).Once()

	params := CreateScheduleParams{
		ScheduleParams: ScheduleParams{ScheduleID: scheduleID},
		TemporalScheduleOptions: TemporalCreateScheduleParams{
			WorkflowID: "wf-id",
			Workflow:   "wf",
			Spec:       client.ScheduleSpec{},
			// TaskQueue intentionally left empty to exercise the default.
		},
	}

	_, err := scheduler.Create(ctx, params)
	require.NoError(t, err)
	action, ok := captured.Action.(*client.ScheduleWorkflowAction)
	require.True(t, ok)
	require.Equal(t, workflowengine.BackgroundTaskQueue, action.TaskQueue)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Create_HonorsExplicitTaskQueue(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"
	mockHandle := &mockScheduleHandle{id: scheduleID}

	var captured client.ScheduleOptions
	mockClient.On("Create", ctx, mock.MatchedBy(func(opts client.ScheduleOptions) bool {
		captured = opts
		return true
	})).Return(mockHandle, nil).Once()

	params := CreateScheduleParams{
		ScheduleParams: ScheduleParams{ScheduleID: scheduleID},
		TemporalScheduleOptions: TemporalCreateScheduleParams{
			WorkflowID: "wf-id",
			Workflow:   "wf",
			Spec:       client.ScheduleSpec{},
			TaskQueue:  workflowengine.CustomerTaskQueue,
		},
	}

	_, err := scheduler.Create(ctx, params)
	require.NoError(t, err)
	action, ok := captured.Action.(*client.ScheduleWorkflowAction)
	require.True(t, ok)
	require.Equal(t, workflowengine.CustomerTaskQueue, action.TaskQueue)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Update(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"
	mockHandle := &mockScheduleHandle{id: scheduleID}

	mockClient.On("GetHandle", ctx, scheduleID).Return(mockHandle).Once()

	params := UpdateScheduleParams{
		ScheduleParams: ScheduleParams{
			ScheduleID: scheduleID,
		},
		TemporalScheduleOptions: TemporalUpdateScheduleParams{
			Spec: client.ScheduleSpec{},
		},
	}

	resp, err := scheduler.Update(ctx, params)
	require.NoError(t, err)
	require.Equal(t, scheduleID, resp.ID)
	require.Equal(t, ScheduleStateActive, resp.Status)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Delete(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"
	mockHandle := &mockScheduleHandle{id: scheduleID}

	mockClient.On("GetHandle", ctx, scheduleID).Return(mockHandle).Once()

	params := DeleteScheduleParams{
		ScheduleParams: ScheduleParams{
			ScheduleID: scheduleID,
		},
	}

	resp, err := scheduler.Delete(ctx, params)
	require.NoError(t, err)
	require.Equal(t, scheduleID, resp.ID)
	require.Equal(t, ScheduleStateDeleted, resp.Status)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Create_Error(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"
	mockClient.On("Create", ctx, mock.Anything).Return(nil, errors.New("create error")).Times(3)

	params := CreateScheduleParams{
		ScheduleParams: ScheduleParams{
			ScheduleID: scheduleID,
			Args:       []interface{}{"arg1"},
		},
		TemporalScheduleOptions: TemporalCreateScheduleParams{
			WorkflowID: "wf-id",
			Workflow:   "wf",
			Spec:       client.ScheduleSpec{},
		},
	}

	resp, err := scheduler.Create(ctx, params)
	require.Error(t, err)
	require.Nil(t, resp)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Update_Error(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"
	mockHandle := &mockScheduleHandle{
		id: scheduleID,
		updateFunc: func(ctx context.Context, opts client.ScheduleUpdateOptions) error {
			return errors.New("update error")
		},
	}

	mockClient.On("GetHandle", ctx, scheduleID).Return(mockHandle).Times(3)

	params := UpdateScheduleParams{
		ScheduleParams: ScheduleParams{
			ScheduleID: scheduleID,
		},
		TemporalScheduleOptions: TemporalUpdateScheduleParams{
			Spec: client.ScheduleSpec{},
		},
	}

	resp, err := scheduler.Update(ctx, params)
	require.Error(t, err)
	require.Nil(t, resp)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Delete_Error(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"
	mockHandle := &mockScheduleHandle{
		id: scheduleID,
		deleteFunc: func(ctx context.Context) error {
			return errors.New("delete error")
		},
	}

	mockClient.On("GetHandle", ctx, scheduleID).Return(mockHandle).Times(3)

	params := DeleteScheduleParams{
		ScheduleParams: ScheduleParams{
			ScheduleID: scheduleID,
		},
	}

	resp, err := scheduler.Delete(ctx, params)
	require.Error(t, err)
	require.Nil(t, resp)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Pause(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"
	mockHandle := &mockScheduleHandle{id: scheduleID}

	mockClient.On("GetHandle", ctx, scheduleID).Return(mockHandle).Once()

	params := PauseScheduleParams{
		ScheduleParams: ScheduleParams{
			ScheduleID: scheduleID,
		},
	}

	resp, err := scheduler.Pause(ctx, params)
	require.NoError(t, err)
	require.Equal(t, scheduleID, resp.ID)
	require.Equal(t, ScheduleStatePaused, resp.Status)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Pause_Error(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"
	mockHandle := &mockScheduleHandle{
		id: scheduleID,
		pauseFunc: func(ctx context.Context, opts client.SchedulePauseOptions) error {
			return errors.New("pause error")
		},
	}

	mockClient.On("GetHandle", ctx, scheduleID).Return(mockHandle).Times(3)

	params := PauseScheduleParams{
		ScheduleParams: ScheduleParams{
			ScheduleID: scheduleID,
		},
	}

	resp, err := scheduler.Pause(ctx, params)
	require.Error(t, err)
	require.Nil(t, resp)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Unpause(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"
	mockHandle := &mockScheduleHandle{id: scheduleID}

	mockClient.On("GetHandle", ctx, scheduleID).Return(mockHandle).Once()

	params := UnpauseScheduleParams{
		ScheduleParams: ScheduleParams{
			ScheduleID: scheduleID,
		},
	}

	resp, err := scheduler.Unpause(ctx, params)
	require.NoError(t, err)
	require.Equal(t, scheduleID, resp.ID)
	require.Equal(t, ScheduleStateActive, resp.Status)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Unpause_Error(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"
	mockHandle := &mockScheduleHandle{
		id: scheduleID,
		unpauseFunc: func(ctx context.Context, opts client.ScheduleUnpauseOptions) error {
			return errors.New("unpause error")
		},
	}

	mockClient.On("GetHandle", ctx, scheduleID).Return(mockHandle).Times(3)

	params := UnpauseScheduleParams{
		ScheduleParams: ScheduleParams{
			ScheduleID: scheduleID,
		},
	}

	resp, err := scheduler.Unpause(ctx, params)
	require.Error(t, err)
	require.Nil(t, resp)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Describe(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"

	// Test describing an active schedule
	mockHandle := &mockScheduleHandle{
		id: scheduleID,
		describeFunc: func(ctx context.Context) (*client.ScheduleDescription, error) {
			return &client.ScheduleDescription{
				Schedule: client.Schedule{
					State: &client.ScheduleState{
						Paused: false,
					},
				},
			}, nil
		},
	}

	mockClient.On("GetHandle", ctx, scheduleID).Return(mockHandle).Once()

	params := DescribeScheduleParams{
		ScheduleParams: ScheduleParams{
			ScheduleID: scheduleID,
		},
	}

	resp, err := scheduler.Describe(ctx, params)
	require.NoError(t, err)
	require.Equal(t, scheduleID, resp.ID)
	require.False(t, resp.Paused)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Describe_Paused(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"

	// Test describing a paused schedule
	mockHandle := &mockScheduleHandle{
		id: scheduleID,
		describeFunc: func(ctx context.Context) (*client.ScheduleDescription, error) {
			return &client.ScheduleDescription{
				Schedule: client.Schedule{
					State: &client.ScheduleState{
						Paused: true,
					},
				},
			}, nil
		},
	}

	mockClient.On("GetHandle", ctx, scheduleID).Return(mockHandle).Once()

	params := DescribeScheduleParams{
		ScheduleParams: ScheduleParams{
			ScheduleID: scheduleID,
		},
	}

	resp, err := scheduler.Describe(ctx, params)
	require.NoError(t, err)
	require.Equal(t, scheduleID, resp.ID)
	require.True(t, resp.Paused)
	mockClient.AssertExpectations(t)
}

func TestTemporalScheduler_Describe_Error(t *testing.T) {
	mockClient := &mocks.ScheduleClient{}
	scheduler := NewTemporalScheduler(mockClient)

	ctx := context.Background()
	scheduleID := "test-schedule"

	mockHandle := &mockScheduleHandle{
		id: scheduleID,
		describeFunc: func(ctx context.Context) (*client.ScheduleDescription, error) {
			return nil, errors.New("describe error")
		},
	}

	mockClient.On("GetHandle", ctx, scheduleID).Return(mockHandle).Once()

	params := DescribeScheduleParams{
		ScheduleParams: ScheduleParams{
			ScheduleID: scheduleID,
		},
	}

	resp, err := scheduler.Describe(ctx, params)
	require.Error(t, err)
	require.Contains(t, err.Error(), "describe error")
	require.Nil(t, resp)
	mockClient.AssertExpectations(t)
}
