package vsa

import (
	"context"
	"fmt"
	"strconv"
	"time"

	ontapRestModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// Job polling defaults. Vars (not consts) so unit tests can shorten them via
// t.Cleanup without changing production defaults or env parsing.
var (
	jobPollingMaxDuration = 25 * time.Second
	jobPollingInterval    = 3 * time.Second
)

// getJobPollingMaxDuration returns the maximum polling duration from environment variable or default
func getJobPollingMaxDuration() time.Duration {
	if envDuration := env.GetString("JOB_POLLING_MAX_DURATION", ""); envDuration != "" {
		if duration, err := strconv.Atoi(envDuration); err == nil && duration > 0 {
			return time.Duration(duration) * time.Second
		}
	}
	return jobPollingMaxDuration
}

// getJobPollingInterval returns the polling interval from environment variable or default
func getJobPollingInterval() time.Duration {
	if envInterval := env.GetString("JOB_POLLING_INTERVAL", ""); envInterval != "" {
		if interval, err := strconv.Atoi(envInterval); err == nil && interval > 0 {
			return time.Duration(interval) * time.Second
		}
	}
	return jobPollingInterval
}

// GetClusterHealthStatus retrieves consolidated cluster health information including takeover state, takeover reasons, and JSWAP status for all nodes in one API call
func (rc *OntapRestProvider) GetClusterHealthStatus() (*ClusterHealthStatusResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	return rc.GetClusterHealthStatusWithClient(client)
}

// GetClusterHealthStatusWithClient retrieves consolidated cluster health information using a provided REST client
func (rc *OntapRestProvider) GetClusterHealthStatusWithClient(client ontapRest.RESTClient) (*ClusterHealthStatusResponse, error) {
	var resultNodes []NodeHealthStatus

	// Single API call to get all required fields
	err := client.Cluster().NodesGet(&ontapRest.NodesGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"name", "uuid", "ha.takeover", "ha.takeover_check", "nvlog"},
		},
	}, func(apiNodes []*ontapRest.Node) error {
		for _, apiNode := range apiNodes {
			nodeHealth := NodeHealthStatus{
				UUID: apiNode.UUID.String(),
				Name: nillable.FromPointer(apiNode.Name),
			}

			// Process HA information (takeover state and takeover check)
			if apiNode.Ha != nil {
				nodeHealth.Ha = &HAHealthInfo{}

				// Process takeover state
				if apiNode.Ha.Takeover != nil {
					nodeHealth.Ha.Takeover = &TakeoverState{
						State: nillable.FromPointer(apiNode.Ha.Takeover.State),
					}

					if apiNode.Ha.Takeover.Failure != nil {
						nodeHealth.Ha.Takeover.Failure = &TakeoverFailure{
							Message: nillable.FromPointer(apiNode.Ha.Takeover.Failure.Message),
							Code:    int(nillable.FromPointer(apiNode.Ha.Takeover.Failure.Code)),
						}
					}
				}

				// Process takeover check (reasons)
				if apiNode.Ha.TakeoverCheck != nil {
					nodeHealth.Ha.TakeoverCheck = &TakeoverCheck{
						TakeoverPossible: nillable.FromPointer(apiNode.Ha.TakeoverCheck.TakeoverPossible),
					}

					// Convert reasons from the API response
					if apiNode.Ha.TakeoverCheck.Reasons != nil {
						for _, reason := range apiNode.Ha.TakeoverCheck.Reasons {
							if reason != nil {
								nodeHealth.Ha.TakeoverCheck.Reasons = append(
									nodeHealth.Ha.TakeoverCheck.Reasons,
									*reason,
								)
							}
						}
					}
				}
			}

			// Process NVLOG (JSWAP) information
			if apiNode.Nvlog != nil {
				nodeHealth.NVLog = &NVLog{
					SwapMode:    nillable.FromPointer(apiNode.Nvlog.SwapMode),
					BackingType: nillable.FromPointer(apiNode.Nvlog.BackingType),
				}
			}

			resultNodes = append(resultNodes, nodeHealth)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return &ClusterHealthStatusResponse{
		Records:    resultNodes,
		NumRecords: len(resultNodes),
	}, nil
}

// TriggerTakeoverCheck triggers a takeover check for a specific node and polls until completion
func (rc *OntapRestProvider) TriggerTakeoverCheck(targetNodeUUID string) (bool, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return false, err
	}
	return rc.TriggerTakeoverCheckWithClient(targetNodeUUID, client)
}

// TriggerTakeoverCheckWithClient triggers a takeover check for a specific node using a provided REST client
func (rc *OntapRestProvider) TriggerTakeoverCheckWithClient(targetNodeUUID string, client ontapRest.RESTClient) (bool, error) {
	// Use the NodeModifyParams with action=takeover_check
	updateParams := &ontapRest.NodeModifyParams{
		UUID:   targetNodeUUID,
		Action: NodeActionTakeoverCheck,
	}

	// Use the provider's context to respect task timeouts and cancellations
	ctx := rc.ClientParams.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	response, err := client.Cluster().ModifyNode(ctx, updateParams)
	if err != nil {
		return false, err
	}

	jobUUID := response.Payload.Job.UUID.String()

	// Poll the job status until completion using the existing polling function
	return rc.pollJobUntilCompletionWithContext(ctx, client, jobUUID)
}

// UpdateJSwapMode updates the JSWAP backing type for a specific node and polls until completion
func (rc *OntapRestProvider) UpdateJSwapMode(targetNodeUUID string, backingType JSWAPBackingType) (bool, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return false, err
	}
	return rc.UpdateJSwapModeWithClient(targetNodeUUID, backingType, client)
}

// UpdateJSwapModeWithClient updates the JSWAP backing type for a specific node using a provided REST client
func (rc *OntapRestProvider) UpdateJSwapModeWithClient(targetNodeUUID string, backingType JSWAPBackingType, client ontapRest.RESTClient) (bool, error) {
	updateParams := &ontapRest.NodeModifyParams{
		UUID: targetNodeUUID,
		Body: &ontapRest.NodeModifyBody{
			NVLog: &ontapRest.NVLogModify{
				BackingType: string(backingType),
			},
		},
	}

	// Use the provider's context to respect task timeouts and cancellations
	ctx := rc.ClientParams.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	response, err := client.Cluster().ModifyNode(ctx, updateParams)
	if err != nil {
		return false, err
	}

	jobUUID := response.Payload.Job.UUID.String()

	// Poll the job status until completion using the context-aware polling function
	return rc.pollJobUntilCompletionWithContext(ctx, client, jobUUID)
}

// pollJobUntilCompletion polls a job until it reaches a terminal state (success or failure)
func (rc *OntapRestProvider) pollJobUntilCompletion(client ontapRest.RESTClient, jobUUID string) (bool, error) {
	ctx := rc.ClientParams.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return rc.pollJobUntilCompletionWithContext(ctx, client, jobUUID)
}

// pollJobUntilCompletionWithContext polls a job until it reaches a terminal state, respecting context cancellation
func (rc *OntapRestProvider) pollJobUntilCompletionWithContext(ctx context.Context, client ontapRest.RESTClient, jobUUID string) (bool, error) {
	maxPollingDuration := getJobPollingMaxDuration()
	pollingInterval := getJobPollingInterval()
	startTime := time.Now()

	ticker := time.NewTicker(pollingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("job polling cancelled: %w", ctx.Err())
		default:
		}

		if time.Since(startTime) > maxPollingDuration {
			return false, fmt.Errorf("job polling timeout after %v", maxPollingDuration)
		}

		jobResponse, err := client.Cluster().GetJob(jobUUID)
		if err != nil {
			// Wait for next poll interval or context cancellation
			select {
			case <-ticker.C:
				continue
			case <-ctx.Done():
				return false, fmt.Errorf("job polling cancelled: %w", ctx.Err())
			}
		}

		if jobResponse.Payload.State == nil {
			select {
			case <-ticker.C:
				continue
			case <-ctx.Done():
				return false, fmt.Errorf("job polling cancelled: %w", ctx.Err())
			}
		}

		jobState := *jobResponse.Payload.State
		jobMessage := ""
		if jobResponse.Payload.Message != nil {
			jobMessage = *jobResponse.Payload.Message
		}

		switch jobState {
		case ontapRestModels.JobStateSuccess:
			return true, nil
		case ontapRestModels.JobStateFailure:
			return false, fmt.Errorf("job failed: %s", jobMessage)
		case ontapRestModels.JobStateQueued, ontapRestModels.JobStateRunning, ontapRestModels.JobStatePaused:
			select {
			case <-ticker.C:
				continue
			case <-ctx.Done():
				return false, fmt.Errorf("job polling cancelled: %w", ctx.Err())
			}
		default:
			select {
			case <-ticker.C:
				continue
			case <-ctx.Done():
				return false, fmt.Errorf("job polling cancelled: %w", ctx.Err())
			}
		}
	}
}
