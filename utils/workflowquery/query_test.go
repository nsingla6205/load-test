package workflowquery

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enums "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
)

func TestNormalizedStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   enums.WorkflowExecutionStatus
		want WorkflowStatus
	}{
		{enums.WORKFLOW_EXECUTION_STATUS_COMPLETED, WorkflowStatusCompleted},
		{enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT, WorkflowStatusTimedOut},
		{enums.WORKFLOW_EXECUTION_STATUS_FAILED, WorkflowStatusFailed},
		{enums.WORKFLOW_EXECUTION_STATUS_CANCELED, WorkflowStatusFailed},
		{enums.WORKFLOW_EXECUTION_STATUS_TERMINATED, WorkflowStatusFailed},
		{enums.WORKFLOW_EXECUTION_STATUS_RUNNING, WorkflowStatusInProgress},
		{enums.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED, WorkflowStatusInProgress},
		{enums.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW, WorkflowStatusInProgress},
	}
	for _, tt := range tests {
		t.Run(string(tt.want), func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, normalizedStatus(tt.in))
		})
	}
}

func TestDecodeWorkflowInput(t *testing.T) {
	t.Parallel()
	t.Run("nil and empty", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, decodeWorkflowInput(nil))
		require.Nil(t, decodeWorkflowInput([]*commonpb.Payload{}))
		require.Nil(t, decodeWorkflowInput([]*commonpb.Payload{nil}))
		require.Nil(t, decodeWorkflowInput([]*commonpb.Payload{{Data: nil}}))
		require.Nil(t, decodeWorkflowInput([]*commonpb.Payload{{Data: []byte{}}}))
	})
	t.Run("raw JSON object", func(t *testing.T) {
		t.Parallel()
		raw := []byte(`{"k":"v","n":1}`)
		out := decodeWorkflowInput([]*commonpb.Payload{{Data: raw}})
		require.Equal(t, map[string]interface{}{"k": "v", "n": float64(1)}, out)
	})
	t.Run("base64-encoded JSON", func(t *testing.T) {
		t.Parallel()
		enc := base64.StdEncoding.EncodeToString([]byte(`{"x":true}`))
		out := decodeWorkflowInput([]*commonpb.Payload{{Data: []byte(enc)}})
		require.Equal(t, map[string]interface{}{"x": true}, out)
	})
	t.Run("invalid JSON", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, decodeWorkflowInput([]*commonpb.Payload{{Data: []byte("not-json")}}))
	})
}

func TestFormatFailureEvent(t *testing.T) {
	t.Parallel()
	t.Run("failed with application type cause and retry", func(t *testing.T) {
		t.Parallel()
		ev := &historypb.HistoryEvent{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionFailedEventAttributes{
				WorkflowExecutionFailedEventAttributes: &historypb.WorkflowExecutionFailedEventAttributes{
					Failure: &failurepb.Failure{
						Message: "top",
						FailureInfo: &failurepb.Failure_ApplicationFailureInfo{
							ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
								Type: "AppErr",
							},
						},
						Cause: &failurepb.Failure{Message: "underlying"},
					},
					RetryState: enums.RETRY_STATE_IN_PROGRESS,
				},
			},
		}
		werr := formatFailureEvent(ev)
		require.NotNil(t, werr)
		require.Equal(t, "underlying", werr.Cause)
		require.Contains(t, werr.Message, "[AppErr]")
		require.Contains(t, werr.Message, "top")
		require.Contains(t, werr.Message, "retry:")
		require.Contains(t, werr.Message, "InProgress")
	})
	t.Run("timed out", func(t *testing.T) {
		t.Parallel()
		ev := &historypb.HistoryEvent{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionTimedOutEventAttributes{
				WorkflowExecutionTimedOutEventAttributes: &historypb.WorkflowExecutionTimedOutEventAttributes{
					RetryState: enums.RETRY_STATE_NON_RETRYABLE_FAILURE,
				},
			},
		}
		werr := formatFailureEvent(ev)
		require.NotNil(t, werr)
		require.Contains(t, werr.Message, "workflow execution timed out")
		require.Contains(t, werr.Message, "retry:")
	})
	t.Run("canceled with details payload", func(t *testing.T) {
		t.Parallel()
		ev := &historypb.HistoryEvent{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionCanceledEventAttributes{
				WorkflowExecutionCanceledEventAttributes: &historypb.WorkflowExecutionCanceledEventAttributes{
					Details: &commonpb.Payloads{
						Payloads: []*commonpb.Payload{{Data: []byte("reason-xyz")}},
					},
				},
			},
		}
		werr := formatFailureEvent(ev)
		require.NotNil(t, werr)
		require.Contains(t, werr.Message, "reason-xyz")
	})
	t.Run("canceled without details", func(t *testing.T) {
		t.Parallel()
		ev := &historypb.HistoryEvent{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionCanceledEventAttributes{
				WorkflowExecutionCanceledEventAttributes: &historypb.WorkflowExecutionCanceledEventAttributes{},
			},
		}
		werr := formatFailureEvent(ev)
		require.NotNil(t, werr)
		require.Equal(t, "workflow execution canceled", werr.Message)
	})
	t.Run("terminated with reason", func(t *testing.T) {
		t.Parallel()
		ev := &historypb.HistoryEvent{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionTerminatedEventAttributes{
				WorkflowExecutionTerminatedEventAttributes: &historypb.WorkflowExecutionTerminatedEventAttributes{
					Reason: "admin stop",
				},
			},
		}
		werr := formatFailureEvent(ev)
		require.NotNil(t, werr)
		require.Equal(t, "admin stop", werr.Message)
	})
	t.Run("empty failed attributes returns nil", func(t *testing.T) {
		t.Parallel()
		ev := &historypb.HistoryEvent{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionFailedEventAttributes{
				WorkflowExecutionFailedEventAttributes: &historypb.WorkflowExecutionFailedEventAttributes{},
			},
		}
		require.Nil(t, formatFailureEvent(ev))
	})
}

// fakeHistoryFetcher implements historyFetcher for tests.
type fakeHistoryFetcher struct {
	pages [][]*historypb.HistoryEvent
	err   error

	calls         int
	lastNamespace string
}

func (f *fakeHistoryFetcher) GetWorkflowExecutionHistory(ctx context.Context, in *workflowservice.GetWorkflowExecutionHistoryRequest, opts ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
	f.calls++
	f.lastNamespace = in.GetNamespace()
	if f.err != nil {
		return nil, f.err
	}
	if f.calls > len(f.pages) {
		return &workflowservice.GetWorkflowExecutionHistoryResponse{History: &historypb.History{Events: nil}}, nil
	}
	events := f.pages[f.calls-1]
	token := []byte{}
	if f.calls < len(f.pages) {
		token = []byte("more")
	}
	return &workflowservice.GetWorkflowExecutionHistoryResponse{
		History:       &historypb.History{Events: events},
		NextPageToken: token,
	}, nil
}

func TestFetchAllHistory_Pagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fakeHistoryFetcher{
		pages: [][]*historypb.HistoryEvent{
			{{EventId: 1, EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED}},
			{{EventId: 2, EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED}},
		},
	}
	got, err := fetchAllHistory(ctx, f, "ns", "wf-1", "run-1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, int64(1), got[0].EventId)
	require.Equal(t, int64(2), got[1].EventId)
	require.Equal(t, 2, f.calls)
}

func TestFetchAllHistory_Error(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fakeHistoryFetcher{err: errors.New("rpc down")}
	_, err := fetchAllHistory(ctx, f, "ns", "wf", "run")
	require.Error(t, err)
	require.Contains(t, err.Error(), "rpc down")
}

func TestGetWorkflowFailureReason_HistoryError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fakeHistoryFetcher{err: errors.New("no history")}
	werr := getWorkflowFailureReason(ctx, f, "ns", "wf", "run")
	require.NotNil(t, werr)
	require.Contains(t, werr.Message, "could not fetch history")
}

func TestGetWorkflowFailureReason_LastFailureWins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	firstFail := &historypb.HistoryEvent{
		EventId:   10,
		EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionFailedEventAttributes{
			WorkflowExecutionFailedEventAttributes: &historypb.WorkflowExecutionFailedEventAttributes{
				Failure: &failurepb.Failure{Message: "first"},
			},
		},
	}
	secondFail := &historypb.HistoryEvent{
		EventId:   20,
		EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionFailedEventAttributes{
			WorkflowExecutionFailedEventAttributes: &historypb.WorkflowExecutionFailedEventAttributes{
				Failure: &failurepb.Failure{Message: "second"},
			},
		},
	}
	f := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{{firstFail, secondFail}}}
	werr := getWorkflowFailureReason(ctx, f, "ns", "wf", "run")
	require.NotNil(t, werr)
	require.Contains(t, werr.Message, "second")
}

func TestGetCompletedWorkflowMetadata_OCIPoolChildResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	childJSON := map[string]interface{}{
		"vlm_config": map[string]interface{}{
			"cloud": map[string]interface{}{
				"ha_pair": []interface{}{
					map[string]interface{}{
						"vm1": map[string]interface{}{
							"name":              "FsnIdocnv-vm-01",
							"serial_number":     "1234501",
							"vsa_management_ip": "150.136.212.147",
							"lifs": map[string]interface{}{
								"intercluster":     map[string]interface{}{"ip": "10.38.25.146"},
								"nodemgmtinternal": map[string]interface{}{"ip": "10.38.0.1"},
							},
						},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(childJSON)
	require.NoError(t, err)
	events := []*historypb.HistoryEvent{
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "OCICreatePoolWorkflow"},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionCompletedEventAttributes{
				ChildWorkflowExecutionCompletedEventAttributes: &historypb.ChildWorkflowExecutionCompletedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "vlm.CreateVSAClusterDeploymentWorkflow"},
					Result: &commonpb.Payloads{
						Payloads: []*commonpb.Payload{{Data: raw}},
					},
				},
			},
		},
	}
	f := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{events}}
	poolMeta, svmMeta := getCompletedWorkflowMetadata(ctx, f, "ns", "wf", "run")
	require.NotNil(t, poolMeta)
	require.Nil(t, svmMeta)
	require.NotEmpty(t, poolMeta.Vms)
	require.Equal(t, "FsnIdocnv-vm-01", poolMeta.Vms[0].Name)
	require.Equal(t, "1234501", poolMeta.Vms[0].SerialNumber)
	require.Equal(t, "150.136.212.147", poolMeta.Vms[0].VSAManagementIP)
}

func TestGetCompletedWorkflowMetadata_PoolOCIDFromChildLabels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	childJSON := map[string]interface{}{
		"vlm_config": map[string]interface{}{
			"cloud": map[string]interface{}{
				"ha_pair": []interface{}{
					map[string]interface{}{
						"vm1": map[string]interface{}{
							"name":              "FsnIdocnv-vm-01",
							"serial_number":     "1234501",
							"vsa_management_ip": "150.136.212.147",
							"lifs": map[string]interface{}{
								"intercluster":     map[string]interface{}{"ip": "10.38.25.146"},
								"nodemgmtinternal": map[string]interface{}{"ip": "10.38.0.1"},
							},
						},
					},
				},
			},
			"deployment": map[string]interface{}{
				"labels": map[string]interface{}{
					"pool_ocid": "ocid1.pool.oc1.iad.testpool",
					"pool_uuid": "b5fb9baf-953b-9c65-19d5-31e3365cc2e3",
				},
			},
		},
	}
	raw, err := json.Marshal(childJSON)
	require.NoError(t, err)
	events := []*historypb.HistoryEvent{
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "OCICreatePoolWorkflow"},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionCompletedEventAttributes{
				ChildWorkflowExecutionCompletedEventAttributes: &historypb.ChildWorkflowExecutionCompletedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "vlm.CreateVSAClusterDeploymentWorkflow"},
					Result: &commonpb.Payloads{
						Payloads: []*commonpb.Payload{{Data: raw}},
					},
				},
			},
		},
	}
	f := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{events}}
	poolMeta, svmMeta := getCompletedWorkflowMetadata(ctx, f, "ns", "wf", "run")
	require.NotNil(t, poolMeta)
	require.Nil(t, svmMeta)
	require.Equal(t, "ocid1.pool.oc1.iad.testpool", poolMeta.PoolOCID,
		"PoolOCID must be propagated from VLM-config-derived child metadata into the parent OCICreatePoolMetadata; without this copy the GetWorkflow endpoint cannot echo poolOCID for OCICreatePoolWorkflow runs")
	require.Equal(t, "b5fb9baf-953b-9c65-19d5-31e3365cc2e3", poolMeta.PoolUUID,
		"PoolUUID must continue to round-trip alongside PoolOCID; both come from deployment.labels and must be propagated together")
}

func TestGetCompletedWorkflowMetadata_PoolCredentialsAndVMs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	credBody, err := json.Marshal(map[string]interface{}{
		"secret": map[string]interface{}{
			"external_identifier": "ocid1.vaultsecret.oc1..ontapadmin",
			"name":                "ontap-admin-secret",
			"version":             2,
		},
	})
	require.NoError(t, err)
	childBody, err := json.Marshal(map[string]interface{}{
		"vlm_config": map[string]interface{}{
			"cloud": map[string]interface{}{
				"ha_pair": []interface{}{
					map[string]interface{}{
						"vm1": map[string]interface{}{
							"name":              "vm-1",
							"serial_number":     "9551",
							"vsa_management_ip": "10.0.0.10",
							"lifs": map[string]interface{}{
								"intercluster":     map[string]interface{}{"ip": "10.0.0.11"},
								"nodemgmtinternal": map[string]interface{}{"ip": "10.0.0.12"},
							},
						},
						"mediator": map[string]interface{}{
							"name": "mediator-1",
							"lifs": map[string]interface{}{
								"rsm": map[string]interface{}{"ip": "10.20.27.35"},
							},
						},
					},
				},
			},
			"deployment": map[string]interface{}{
				"spconfig": map[string]interface{}{
					"iops": 15248,
					"tput": 954,
				},
			},
		},
	})
	require.NoError(t, err)

	events := []*historypb.HistoryEvent{
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "OCICreatePoolWorkflow"},
				},
			},
		},
		{
			EventId:   5,
			EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
				ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
					ActivityType: &commonpb.ActivityType{Name: "CreateOnTapCredentialsForOCI"},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
			Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
				ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{
					ScheduledEventId: 5,
					Result:           &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: credBody}}},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionCompletedEventAttributes{
				ChildWorkflowExecutionCompletedEventAttributes: &historypb.ChildWorkflowExecutionCompletedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "vlm.CreateVSAClusterDeploymentWorkflow"},
					Result:       &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: childBody}}},
				},
			},
		},
	}
	f := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{events}}
	poolMeta, svmMeta := getCompletedWorkflowMetadata(ctx, f, "ns", "wf", "run")
	require.Nil(t, svmMeta, "SVM metadata must remain nil for an OCICreatePoolWorkflow")
	require.NotNil(t, poolMeta)
	require.Len(t, poolMeta.Vms, 1)
	require.Equal(t, "vm-1", poolMeta.Vms[0].Name)
	require.Equal(t, int64(15248), poolMeta.IOPS,
		"pool-level IOPS from spconfig.iops must survive the child-metadata merge")
	require.Equal(t, MBpsToGBps(954), poolMeta.ThroughputGBps,
		"pool-level throughput from spconfig.tput must survive the child-metadata merge")
	require.NotNil(t, poolMeta.Mediator, "mediator must survive the child-metadata merge")
	require.Equal(t, "mediator-1", poolMeta.Mediator.Name,
		"mediator name must survive the child-metadata merge")
	require.Equal(t, "10.20.27.35", poolMeta.Mediator.IP,
		"mediator ip must survive the child-metadata merge")
	require.Equal(t, "ha_pair-1", poolMeta.Mediator.HAPair,
		"mediator haPair must survive the child-metadata merge")
	require.NotNil(t, poolMeta.Credentials)
	require.NotNil(t, poolMeta.Credentials.Secret)
	require.Equal(t, "ocid1.vaultsecret.oc1..ontapadmin", poolMeta.Credentials.Secret.Ocid)
	require.Equal(t, "2", poolMeta.Credentials.Secret.Version)
}

func TestGetCompletedWorkflowMetadata_OtherActivitiesIgnored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	body, err := json.Marshal(map[string]string{"k": "v"})
	require.NoError(t, err)
	events := []*historypb.HistoryEvent{
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "OCICreatePoolWorkflow"},
				},
			},
		},
		{
			EventId:   5,
			EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
				ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
					ActivityType: &commonpb.ActivityType{Name: "SomeUnrelatedActivity"},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
			Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
				ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{
					ScheduledEventId: 5,
					Result:           &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: body}}},
				},
			},
		},
	}
	f := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{events}}
	poolMeta, svmMeta := getCompletedWorkflowMetadata(ctx, f, "ns", "wf", "run")
	require.Nil(t, poolMeta, "no metadata when only unrelated activities have completed")
	require.Nil(t, svmMeta)
}

func TestGetCompletedWorkflowMetadata_WrongParentOrChildSkipped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	raw, err := json.Marshal(map[string]string{"x": "y"})
	require.NoError(t, err)
	events := []*historypb.HistoryEvent{
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "OtherWorkflow"},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionCompletedEventAttributes{
				ChildWorkflowExecutionCompletedEventAttributes: &historypb.ChildWorkflowExecutionCompletedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "vlm.CreateVSAClusterDeploymentWorkflow"},
					Result:       &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: raw}}},
				},
			},
		},
	}
	f := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{events}}
	poolMeta, svmMeta := getCompletedWorkflowMetadata(ctx, f, "ns", "wf", "run")
	require.Nil(t, poolMeta, "no metadata when parent is not a pool workflow")
	require.Nil(t, svmMeta)
}

func TestGetWorkflowInputMetadata_OCIUpdatePoolChildResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	childJSON := map[string]interface{}{
		"vlm_config": map[string]interface{}{
			"cloud": map[string]interface{}{
				"ha_pair": []interface{}{
					map[string]interface{}{
						"vm1": map[string]interface{}{
							"name":              "FsnIdocnv-vm-01",
							"serial_number":     "9876501",
							"vsa_management_ip": "10.0.0.1",
							"lifs": map[string]interface{}{
								"intercluster":     map[string]interface{}{"ip": "10.38.25.200"},
								"nodemgmtinternal": map[string]interface{}{"ip": "10.38.0.10"},
							},
						},
					},
				},
			},
			"deployment": map[string]interface{}{
				"labels": map[string]interface{}{
					"pool_uuid": "update-pool-uuid-123",
				},
			},
		},
		"update_status": map[string]interface{}{},
	}
	raw, err := json.Marshal(childJSON)
	require.NoError(t, err)
	events := []*historypb.HistoryEvent{
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "OCIUpdatePoolWorkflow"},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionCompletedEventAttributes{
				ChildWorkflowExecutionCompletedEventAttributes: &historypb.ChildWorkflowExecutionCompletedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "vlm.UpdateVSAClusterDeploymentWorkflow"},
					Result: &commonpb.Payloads{
						Payloads: []*commonpb.Payload{{Data: raw}},
					},
				},
			},
		},
	}
	f := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{events}}
	poolMeta, svmMeta := getCompletedWorkflowMetadata(ctx, f, "ns", "wf", "run")
	require.NotNil(t, poolMeta)
	require.Nil(t, svmMeta)
	require.Equal(t, "update-pool-uuid-123", poolMeta.PoolUUID)
	require.NotEmpty(t, poolMeta.Vms)
	require.Equal(t, "FsnIdocnv-vm-01", poolMeta.Vms[0].Name)
	require.Equal(t, "9876501", poolMeta.Vms[0].SerialNumber)
	require.Equal(t, "10.0.0.1", poolMeta.Vms[0].VSAManagementIP)
}

func maintenanceChildResultPayload(poolUUID, vmName, instanceID string) []byte {
	childJSON := map[string]interface{}{
		"vlm_config": map[string]interface{}{
			"cloud": map[string]interface{}{
				"ha_pair": []interface{}{
					map[string]interface{}{
						"vm1": map[string]interface{}{
							"name":              vmName,
							"serial_number":     "1234501",
							"vsa_management_ip": "10.0.0.5",
							"vm_instance_id":    instanceID,
							"lifs": map[string]interface{}{
								"intercluster": map[string]interface{}{"ip": "10.38.25.201"},
							},
						},
						"mediator": map[string]interface{}{
							"name":           "mediator-1",
							"ip":             "10.0.0.99",
							"vm_instance_id": "ocid1.instance..mediator-new",
						},
					},
				},
			},
			"deployment": map[string]interface{}{
				"labels": map[string]interface{}{
					"pool_uuid": poolUUID,
				},
			},
		},
	}
	raw, _ := json.Marshal(childJSON)
	return raw
}

func TestGetWorkflowInputMetadata_OCIExecutePoolMaintenance_NodeChildResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	raw := maintenanceChildResultPayload("maint-pool-uuid", "maint-vm-01", "ocid1.instance..node-new")
	events := []*historypb.HistoryEvent{
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: ociExecutePoolMaintenanceWorkflowName},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionCompletedEventAttributes{
				ChildWorkflowExecutionCompletedEventAttributes: &historypb.ChildWorkflowExecutionCompletedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: maintenanceWorkflowName},
					Result:       &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: raw}}},
				},
			},
		},
	}
	f := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{events}}
	poolMeta, svmMeta := getCompletedWorkflowMetadata(ctx, f, "ns", "wf", "run")
	require.NotNil(t, poolMeta)
	require.Nil(t, svmMeta)
	require.Equal(t, "maint-pool-uuid", poolMeta.PoolUUID)
	require.Len(t, poolMeta.Vms, 1)
	require.Equal(t, "maint-vm-01", poolMeta.Vms[0].Name)
	require.Equal(t, "ocid1.instance..node-new", poolMeta.Vms[0].InstanceID)
	require.NotNil(t, poolMeta.Mediator)
	require.Equal(t, "ocid1.instance..mediator-new", poolMeta.Mediator.InstanceID)
}

func TestGetWorkflowInputMetadata_OCIExecutePoolMaintenance_MediatorChildResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	raw := maintenanceChildResultPayload("mediator-only-pool", "mediator-vm-01", "ocid1.instance..node-old")
	events := []*historypb.HistoryEvent{
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: ociExecutePoolMaintenanceWorkflowName},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionCompletedEventAttributes{
				ChildWorkflowExecutionCompletedEventAttributes: &historypb.ChildWorkflowExecutionCompletedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: mediatorMaintenanceRecreateWorkflowName},
					Result:       &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: raw}}},
				},
			},
		},
	}
	f := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{events}}
	poolMeta, svmMeta := getCompletedWorkflowMetadata(ctx, f, "ns", "wf", "run")
	require.NotNil(t, poolMeta)
	require.Nil(t, svmMeta)
	require.Equal(t, "mediator-only-pool", poolMeta.PoolUUID)
	require.NotNil(t, poolMeta.Mediator)
	require.Equal(t, "ocid1.instance..mediator-new", poolMeta.Mediator.InstanceID)
}

func TestGetWorkflowInputMetadata_OCIExecutePoolMaintenance_LastChildWins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mediatorRaw := maintenanceChildResultPayload("combo-pool", "combo-vm-old", "ocid1.instance..node-old")
	nodeRaw := maintenanceChildResultPayload("combo-pool", "combo-vm-new", "ocid1.instance..node-new")
	events := []*historypb.HistoryEvent{
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: ociExecutePoolMaintenanceWorkflowName},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionCompletedEventAttributes{
				ChildWorkflowExecutionCompletedEventAttributes: &historypb.ChildWorkflowExecutionCompletedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: mediatorMaintenanceRebootMigrateWorkflowName},
					Result:       &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: mediatorRaw}}},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionCompletedEventAttributes{
				ChildWorkflowExecutionCompletedEventAttributes: &historypb.ChildWorkflowExecutionCompletedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: maintenanceWorkflowName},
					Result:       &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: nodeRaw}}},
				},
			},
		},
	}
	f := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{events}}
	poolMeta, svmMeta := getCompletedWorkflowMetadata(ctx, f, "ns", "wf", "run")
	require.NotNil(t, poolMeta)
	require.Nil(t, svmMeta)
	require.Equal(t, "combo-vm-new", poolMeta.Vms[0].Name)
	require.Equal(t, "ocid1.instance..node-new", poolMeta.Vms[0].InstanceID)
}

func TestSvmResultFromPayloads(t *testing.T) {
	t.Parallel()
	t.Run("nil payloads returns nil", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, svmResultFromPayloads(nil))
	})
	t.Run("empty payloads returns nil", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, svmResultFromPayloads([]*commonpb.Payload{}))
	})
	t.Run("invalid JSON returns nil", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, svmResultFromPayloads([]*commonpb.Payload{{Data: []byte("not-json")}}))
	})
	t.Run("empty name and svmOCID returns nil", func(t *testing.T) {
		t.Parallel()
		raw, err := json.Marshal(map[string]string{"other": "field"})
		require.NoError(t, err)
		require.Nil(t, svmResultFromPayloads([]*commonpb.Payload{{Data: raw}}))
	})
	t.Run("valid metadata returns correct struct", func(t *testing.T) {
		t.Parallel()
		haPair := "ha_pair-1"
		input := OCICreateSVMMetadata{
			Name:    "svm1",
			SvmOCID: "ocid1.svm",
			Lifs: []OCICreateSVMLifMetadata{
				{Name: "lif1", IP: "10.0.0.1", Node: "node1", NodeUUID: "node-uuid-1", HaPair: &haPair, Protocols: []string{"nfs", "cifs", "s3"}},
			},
		}
		raw, err := json.Marshal(input)
		require.NoError(t, err)
		got := svmResultFromPayloads([]*commonpb.Payload{{Data: raw}})
		require.NotNil(t, got)
		require.Equal(t, "svm1", got.Name)
		require.Equal(t, "ocid1.svm", got.SvmOCID)
		require.Len(t, got.Lifs, 1)
		require.Equal(t, "lif1", got.Lifs[0].Name)
		require.Equal(t, "10.0.0.1", got.Lifs[0].IP)
		require.Equal(t, "node1", got.Lifs[0].Node)
		require.Equal(t, "node-uuid-1", got.Lifs[0].NodeUUID)
		require.NotNil(t, got.Lifs[0].HaPair)
		require.Equal(t, "ha_pair-1", *got.Lifs[0].HaPair)
		require.Equal(t, []string{"nfs", "cifs", "s3"}, got.Lifs[0].Protocols)
	})
}

func TestGetCompletedWorkflowMetadata_SVMWorkflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	haPair := "ha_pair-1"
	svmResult := OCICreateSVMMetadata{
		Name:    "svm1",
		SvmOCID: "ocid1.svm",
		Lifs: []OCICreateSVMLifMetadata{
			{Name: "lif1", IP: "10.0.0.1", Node: "node1", NodeUUID: "node-uuid-1", HaPair: &haPair, Protocols: []string{"nfs", "cifs", "s3"}},
		},
	}
	raw, err := json.Marshal(svmResult)
	require.NoError(t, err)
	events := []*historypb.HistoryEvent{
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "OCICreateSVMWorkflow"},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{
				WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{
					Result: &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: raw}}},
				},
			},
		},
	}
	f := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{events}}
	poolMeta, svmMeta := getCompletedWorkflowMetadata(ctx, f, "ns", "wf", "run")
	require.Nil(t, poolMeta)
	require.NotNil(t, svmMeta)
	require.Equal(t, "svm1", svmMeta.Name)
	require.Equal(t, "ocid1.svm", svmMeta.SvmOCID)
	require.Len(t, svmMeta.Lifs, 1)
	require.Equal(t, "lif1", svmMeta.Lifs[0].Name)
	require.Equal(t, "10.0.0.1", svmMeta.Lifs[0].IP)
	require.Equal(t, "node1", svmMeta.Lifs[0].Node)
	require.Equal(t, "node-uuid-1", svmMeta.Lifs[0].NodeUUID)
	require.NotNil(t, svmMeta.Lifs[0].HaPair)
	require.Equal(t, "ha_pair-1", *svmMeta.Lifs[0].HaPair)
	require.Equal(t, []string{"nfs", "cifs", "s3"}, svmMeta.Lifs[0].Protocols)
}

func TestGetCompletedWorkflowMetadata_HistoryError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fakeHistoryFetcher{err: errors.New("history unavailable")}
	poolMeta, svmMeta := getCompletedWorkflowMetadata(ctx, f, "ns", "wf", "run")
	require.Nil(t, poolMeta)
	require.Nil(t, svmMeta)
}

type fakeTemporalQuerier struct {
	describe *workflowservice.DescribeWorkflowExecutionResponse
	descErr  error
	hist     historyFetcher
}

func withTemporalNamespace(t *testing.T, ns string) {
	t.Helper()
	orig := temporalNamespace
	temporalNamespace = ns
	t.Cleanup(func() {
		temporalNamespace = orig
	})
}

func (f *fakeTemporalQuerier) DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	return f.describe, f.descErr
}

func (f *fakeTemporalQuerier) History() historyFetcher {
	return f.hist
}

func TestQueryWithClient_DescribeError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := &fakeTemporalQuerier{descErr: errors.New("not found")}
	_, err := queryWithClient(ctx, q, "wf", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestQueryWithClient_NoExecutionInfo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := &fakeTemporalQuerier{describe: &workflowservice.DescribeWorkflowExecutionResponse{}}
	res, err := queryWithClient(ctx, q, "wf", "")
	require.NoError(t, err)
	require.Equal(t, WorkflowStatusFailed, res.Status)
	require.NotNil(t, res.Error)
	require.Equal(t, "no workflow execution info", res.Error.Message)
}

func TestQueryWithClient_CompletedSetsWorkflowTypeAndMetadata(t *testing.T) {
	withTemporalNamespace(t, "test-ns")

	ctx := context.Background()
	childJSON := map[string]interface{}{
		"vlm_config": map[string]interface{}{
			"cloud": map[string]interface{}{
				"ha_pair": []interface{}{
					map[string]interface{}{
						"vm1": map[string]interface{}{
							"name":              "single-vm",
							"serial_number":     "9001",
							"vsa_management_ip": "10.0.0.3",
							"lifs": map[string]interface{}{
								"intercluster":     map[string]interface{}{"ip": "10.0.0.1"},
								"nodemgmtinternal": map[string]interface{}{"ip": "10.0.0.2"},
							},
						},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(childJSON)
	require.NoError(t, err)
	histEvents := []*historypb.HistoryEvent{
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "OCICreatePoolWorkflow"},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_ChildWorkflowExecutionCompletedEventAttributes{
				ChildWorkflowExecutionCompletedEventAttributes: &historypb.ChildWorkflowExecutionCompletedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "vlm.CreateVSAClusterDeploymentWorkflow"},
					Result:       &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: raw}}},
				},
			},
		},
	}
	hf := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{histEvents}}
	q := &fakeTemporalQuerier{
		describe: &workflowservice.DescribeWorkflowExecutionResponse{
			WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
				Status: enums.WORKFLOW_EXECUTION_STATUS_COMPLETED,
				Type:   &commonpb.WorkflowType{Name: "OCICreatePoolWorkflow"},
				Execution: &commonpb.WorkflowExecution{
					WorkflowId: "resolved-wf",
					RunId:      "run-abc",
				},
			},
		},
		hist: hf,
	}
	res, err := queryWithClient(ctx, q, "wf-in", "")
	require.NoError(t, err)
	require.Equal(t, WorkflowStatusCompleted, res.Status)
	require.Equal(t, "OCICreatePoolWorkflow", res.WorkflowType)
	require.Nil(t, res.Error)
	require.Equal(t, "test-ns", hf.lastNamespace)
	require.NotNil(t, res.PoolMetadata)
	require.NotEmpty(t, res.PoolMetadata.Vms)
	require.Equal(t, "single-vm", res.PoolMetadata.Vms[0].Name)
}

func TestQueryWithClient_FailedIncludesError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	failEv := &historypb.HistoryEvent{
		EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionFailedEventAttributes{
			WorkflowExecutionFailedEventAttributes: &historypb.WorkflowExecutionFailedEventAttributes{
				Failure: &failurepb.Failure{Message: "activity blew up"},
			},
		},
	}
	hf := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{{failEv}}}
	q := &fakeTemporalQuerier{
		describe: &workflowservice.DescribeWorkflowExecutionResponse{
			WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
				Status: enums.WORKFLOW_EXECUTION_STATUS_FAILED,
				Type:   &commonpb.WorkflowType{Name: "OCICreatePoolWorkflow"},
				Execution: &commonpb.WorkflowExecution{
					WorkflowId: "wf",
					RunId:      "r1",
				},
			},
		},
		hist: hf,
	}
	res, err := queryWithClient(ctx, q, "wf", "")
	require.NoError(t, err)
	require.Equal(t, WorkflowStatusFailed, res.Status)
	require.Equal(t, "OCICreatePoolWorkflow", res.WorkflowType)
	require.NotNil(t, res.Error)
	require.Contains(t, res.Error.Message, "activity blew up")
}

func TestQueryWithClient_TimedOut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ev := &historypb.HistoryEvent{
		EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionTimedOutEventAttributes{
			WorkflowExecutionTimedOutEventAttributes: &historypb.WorkflowExecutionTimedOutEventAttributes{},
		},
	}
	hf := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{{ev}}}
	q := &fakeTemporalQuerier{
		describe: &workflowservice.DescribeWorkflowExecutionResponse{
			WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
				Status: enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT,
				Type:   &commonpb.WorkflowType{Name: "SomeWorkflow"},
			},
		},
		hist: hf,
	}
	res, err := queryWithClient(ctx, q, "wf", "")
	require.NoError(t, err)
	require.Equal(t, WorkflowStatusTimedOut, res.Status)
	require.Equal(t, "SomeWorkflow", res.WorkflowType)
	require.NotNil(t, res.Error)
	require.Contains(t, res.Error.Message, "timed out")
}

// TestGetCompletedWorkflowMetadata_CertificateAndSecretMapped exercises the
// full query path with a USER_CERTIFICATE-auth pool: the
// CreateOnTapCredentialsForOCI activity result carries BOTH secret and
// certificate refs. Both must round-trip into OCICreatePoolMetadata.Credentials
// without one shadowing the other.
func TestGetCompletedWorkflowMetadata_CertificateAndSecretMapped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	credBody, err := json.Marshal(map[string]interface{}{
		"secret": map[string]interface{}{
			"external_identifier": "ocid1.vaultsecret.oc1..ontapadmin",
			"name":                "ontap-admin-secret",
			"version":             4,
		},
		"certificate": map[string]interface{}{
			"external_identifier": "ocid1.certificate.oc1..ontapcert",
			"name":                "ontap-cert",
			"version":             9,
		},
	})
	require.NoError(t, err)

	events := []*historypb.HistoryEvent{
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "OCICreatePoolWorkflow"},
				},
			},
		},
		{
			EventId:   5,
			EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
				ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
					ActivityType: &commonpb.ActivityType{Name: "CreateOnTapCredentialsForOCI"},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
			Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
				ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{
					ScheduledEventId: 5,
					Result:           &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: credBody}}},
				},
			},
		},
	}
	f := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{events}}
	poolMeta, svmMeta := getCompletedWorkflowMetadata(ctx, f, "ns", "wf", "run")
	require.Nil(t, svmMeta)
	require.NotNil(t, poolMeta)
	require.NotNil(t, poolMeta.Credentials)
	require.NotNil(t, poolMeta.Credentials.Secret)
	require.Equal(t, "ocid1.vaultsecret.oc1..ontapadmin", poolMeta.Credentials.Secret.Ocid)
	require.Equal(t, "4", poolMeta.Credentials.Secret.Version)
	require.NotNil(t, poolMeta.Credentials.Certificate)
	require.Equal(t, "ocid1.certificate.oc1..ontapcert", poolMeta.Credentials.Certificate.Ocid)
	require.Equal(t, "9", poolMeta.Credentials.Certificate.Version)
}

// TestGetCompletedWorkflowMetadata_CertificateOnly verifies the certificate-only
// payload shape (USER_CERTIFICATE-auth pool where the secret block is absent)
// surfaces a Certificate but leaves Secret nil.
func TestGetCompletedWorkflowMetadata_CertificateOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	credBody, err := json.Marshal(map[string]interface{}{
		"certificate": map[string]interface{}{
			"external_identifier": "ocid1.certificate.oc1..onlycert",
			"name":                "ontap-cert",
			"version":             1,
		},
	})
	require.NoError(t, err)

	events := []*historypb.HistoryEvent{
		{
			EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: "OCICreatePoolWorkflow"},
				},
			},
		},
		{
			EventId:   5,
			EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
				ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
					ActivityType: &commonpb.ActivityType{Name: "CreateOnTapCredentialsForOCI"},
				},
			},
		},
		{
			EventType: enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
			Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
				ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{
					ScheduledEventId: 5,
					Result:           &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: credBody}}},
				},
			},
		},
	}
	f := &fakeHistoryFetcher{pages: [][]*historypb.HistoryEvent{events}}
	poolMeta, _ := getCompletedWorkflowMetadata(ctx, f, "ns", "wf", "run")
	require.NotNil(t, poolMeta)
	require.NotNil(t, poolMeta.Credentials)
	require.Nil(t, poolMeta.Credentials.Secret)
	require.NotNil(t, poolMeta.Credentials.Certificate)
	require.Equal(t, "ocid1.certificate.oc1..onlycert", poolMeta.Credentials.Certificate.Ocid)
	require.Equal(t, "1", poolMeta.Credentials.Certificate.Version)
}

// TestOciCreatePoolCredentialsFromPayloads_CertificateOnlyOcidEmitsOcidNoVersion
// pins behavior: when only the OCID is set (no version), the API surfaces the
// OCID without a "0" version placeholder.
func TestOciCreatePoolCredentialsFromPayloads_CertificateOnlyOcidEmitsOcidNoVersion(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(map[string]interface{}{
		"certificate": map[string]interface{}{
			"external_identifier": "ocid1.certificate.oc1..onlyocid",
			"name":                "only-name",
			"version":             0,
		},
	})
	require.NoError(t, err)
	got := ociCreatePoolCredentialsFromPayloads([]*commonpb.Payload{{Data: body}})
	require.NotNil(t, got)
	require.NotNil(t, got.Certificate)
	require.Equal(t, "ocid1.certificate.oc1..onlyocid", got.Certificate.Ocid)
	require.Equal(t, "", got.Certificate.Version)
}

// TestOciCreatePoolCredentialsFromPayloads_CertificateBase64Wrapped covers the
// base64-wrapped JSON unwrap path for certificate-bearing payloads.
func TestOciCreatePoolCredentialsFromPayloads_CertificateBase64Wrapped(t *testing.T) {
	t.Parallel()
	inner, err := json.Marshal(map[string]interface{}{
		"certificate": map[string]interface{}{
			"external_identifier": "ocid1.certificate.oc1..wrapped",
			"name":                "wrapped-cert",
			"version":             2,
		},
	})
	require.NoError(t, err)
	body := []byte(base64.StdEncoding.EncodeToString(inner))

	got := ociCreatePoolCredentialsFromPayloads([]*commonpb.Payload{{Data: body}})
	require.NotNil(t, got)
	require.NotNil(t, got.Certificate)
	require.Equal(t, "ocid1.certificate.oc1..wrapped", got.Certificate.Ocid)
	require.Equal(t, "2", got.Certificate.Version)
}
