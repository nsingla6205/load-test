package opsinventory

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
)

func temporalOf(t *testing.T, r response) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, r.status, r.raw)
	return r.body["temporal"].(map[string]any)
}

func TestGetJobTemporal(t *testing.T) {
	t.Run("COMPLETE maps only allowlisted fields", func(t *testing.T) {
		e := newEnv(t)
		e.desc.fn = describeWithPayloads
		r := e.get(M{"resourceType": "job", "identity": M{"uuid": "job-1"}})
		temporal := temporalOf(t, r)
		assert.Equal(t, "vcp_db_and_temporal", r.body["source"])
		assert.Equal(t, "COMPLETE", temporal["enrichmentStatus"])
		assert.Equal(t, "wf-1", temporal["workflowId"])
		assert.Equal(t, "run-1", temporal["runId"])
		assert.Equal(t, "CreatePoolWorkflow", temporal["workflowType"])
		assert.Equal(t, "customer-workflows", temporal["taskQueue"])
		assert.Equal(t, "RUNNING", temporal["status"])
		assert.Equal(t, float64(42), temporal["historyLength"])
		assert.Nil(t, temporal["closeTime"])
		assert.Equal(t, false, temporal["stateMismatch"])
		activity := temporal["pendingActivities"].([]any)[0].(map[string]any)
		assert.Equal(t, "CreateOntapVolume", activity["activityType"])
		assert.Equal(t, "STARTED", activity["state"])
		assert.Equal(t, float64(3), activity["attempt"])
		assert.Equal(t, "OntapError", activity["lastFailureType"])
		assert.Equal(t, "child-1", temporal["pendingChildren"].([]any)[0].(map[string]any)["workflowId"])
		assert.NotContains(t, r.raw, sentinel)
		assert.Equal(t, []string{"wf-1"}, e.desc.calls)
		f := e.logs.last(t)
		assert.Equal(t, "vcp_db_and_temporal", f["data_source"])
		assert.Equal(t, "COMPLETE", f["enrichment_status"])
	})

	t.Run("stateMismatch when the DB job is active but the run is closed", func(t *testing.T) {
		e := newEnv(t)
		e.desc.fn = func(_ context.Context, id string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
			return describe(id, enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED), nil
		}
		temporal := temporalOf(t, e.get(M{"resourceType": "job", "identity": M{"uuid": "job-1"}}))
		assert.Equal(t, "TERMINATED", temporal["status"])
		assert.Equal(t, true, temporal["stateMismatch"])
		assert.NotNil(t, temporal["closeTime"])
	})

	t.Run("WhenJobIsInAnotherProjectItIs404WithoutTemporal", func(t *testing.T) {
		e := newEnv(t)
		e.desc.fn = describeWithPayloads
		r := e.get(M{"resourceType": "job", "projectNumber": "2222", "identity": M{"uuid": "job-1"}})
		assert.Equal(t, http.StatusNotFound, r.status, r.raw)
		assert.Empty(t, e.desc.calls)
	})

	t.Run("SKIPPED when the job has no workflow id", func(t *testing.T) {
		e := newEnv(t)
		r := e.get(M{"resourceType": "job", "identity": M{"uuid": "job-admin"}})
		temporal := temporalOf(t, r)
		assert.Equal(t, M{"enrichmentStatus": "SKIPPED"}, M(temporal))
		assert.Equal(t, "vcp_db", r.body["source"])
		assert.Empty(t, e.desc.calls)
	})

	t.Run("NOT_FOUND has only status and workflow id", func(t *testing.T) {
		e := newEnv(t)
		temporal := temporalOf(t, e.get(M{"resourceType": "job", "identity": M{"uuid": "job-1"}}))
		assert.Equal(t, M{"enrichmentStatus": "NOT_FOUND", "workflowId": "wf-1"}, M(temporal))
		assert.NotContains(t, temporal, "stateMismatch")
		assert.Equal(t, []string{"wf-1"}, e.desc.calls, "PROCESSING jobs never check the sequencer")
	})

	t.Run("UNAVAILABLE on Temporal error, still 200", func(t *testing.T) {
		e := newEnv(t)
		e.desc.fn = func(context.Context, string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
			return nil, serviceerror.NewUnavailable("connection refused to " + sentinel)
		}
		r := e.get(M{"resourceType": "job", "identity": M{"uuid": "job-1"}})
		temporal := temporalOf(t, r)
		assert.Equal(t, "UNAVAILABLE", temporal["enrichmentStatus"])
		assert.Equal(t, "temporal describe failed: Unavailable", temporal["reason"])
		assert.NotContains(t, r.raw, sentinel)
		assert.NotContains(t, temporal, "stateMismatch")
	})
}

func seqWorkflowID(e *testEnv) string {
	return workflows.GenerateControlWorkflowID(e.fx.account.ID, "us-east4", e.fx.pool.Name)
}

func TestGetJobSequencer(t *testing.T) {
	t.Run("QUEUED_IN_SEQUENCER for a NEW sequencer-backed job", func(t *testing.T) {
		e := newEnv(t)
		seq := seqWorkflowID(e)
		e.desc.fn = func(_ context.Context, id string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
			if id == seq {
				return describe(id, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING), nil
			}
			return nil, serviceerror.NewNotFound("not found")
		}
		temporal := temporalOf(t, e.get(M{"resourceType": "job", "identity": M{"uuid": "job-seq"}}))
		assert.Equal(t, "QUEUED_IN_SEQUENCER", temporal["enrichmentStatus"])
		assert.Equal(t, seq, temporal["sequenceWorkflowId"])
		assert.Equal(t, "RUNNING", temporal["sequenceWorkflowStatus"])
		assert.Equal(t, []string{"wf-seq", seq}, e.desc.calls)
	})

	t.Run("NOT_FOUND when the sequencer is not running", func(t *testing.T) {
		e := newEnv(t)
		seq := seqWorkflowID(e)
		e.desc.fn = func(_ context.Context, id string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
			if id == seq {
				return describe(id, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED), nil
			}
			return nil, serviceerror.NewNotFound("not found")
		}
		temporal := temporalOf(t, e.get(M{"resourceType": "job", "identity": M{"uuid": "job-seq"}}))
		assert.Equal(t, "NOT_FOUND", temporal["enrichmentStatus"])
	})

	t.Run("NOT_FOUND when the sequencer describe fails", func(t *testing.T) {
		e := newEnv(t)
		temporal := temporalOf(t, e.get(M{"resourceType": "job", "identity": M{"uuid": "job-seq"}}))
		assert.Equal(t, "NOT_FOUND", temporal["enrichmentStatus"])
		assert.Equal(t, []string{"wf-seq", seqWorkflowID(e)}, e.desc.calls)
	})

	t.Run("only NEW jobs check the sequencer", func(t *testing.T) {
		e := newEnv(t)
		require.NoError(t, e.db.Model(&datamodel.Job{}).Where("uuid = ?", "job-seq").Update("state", "PROCESSING").Error)
		temporal := temporalOf(t, e.get(M{"resourceType": "job", "identity": M{"uuid": "job-seq"}}))
		assert.Equal(t, "NOT_FOUND", temporal["enrichmentStatus"])
		assert.Equal(t, []string{"wf-seq"}, e.desc.calls)
	})

	t.Run("not for job types outside the sequencer", func(t *testing.T) {
		e := newEnv(t)
		require.NoError(t, e.db.Model(&datamodel.Job{}).Where("uuid = ?", "job-seq").Update("type", "CREATE_POOL").Error)
		temporalOf(t, e.get(M{"resourceType": "job", "identity": M{"uuid": "job-seq"}}))
		assert.Equal(t, []string{"wf-seq"}, e.desc.calls)
	})
}
