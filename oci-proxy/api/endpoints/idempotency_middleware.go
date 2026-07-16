package api

import (
	"strings"

	"github.com/ogen-go/ogen/middleware"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type idempotencyBranch int

const (
	branchLookupError idempotencyBranch = iota
	branchTerminalFailure
	branchReplay
)

func (h *Handler) IdempotencyMiddleware() ociserver.Middleware {
	return func(req middleware.Request, next middleware.Next) (middleware.Response, error) {
		if !isIdempotencyGuardedOperation(req.OperationName) {
			return next(req)
		}

		opcWorkRequestID, err := opcWorkRequestIDFromContext(req.Context)
		if err != nil || strings.TrimSpace(opcWorkRequestID) == "" {
			return next(req)
		}

		logger := util.GetLogger(req.Context)
		existing, lookupErr := h.lookupExistingWorkflow(req.Context, opcWorkRequestID)

		var branch idempotencyBranch
		switch {
		case lookupErr != nil:
			logger.Error(req.OperationName+" idempotency lookup failed; failing closed", "workflowID", opcWorkRequestID, "error", lookupErr)
			branch = branchLookupError
		case !existing.Found:
			return next(req)
		case isTerminalFailure(existing.Status):
			logger.Info(req.OperationName+" idempotent replay of terminally-failed workflow; surfacing failure", "workflowID", opcWorkRequestID, "status", existing.Status)
			branch = branchTerminalFailure
		default:
			logger.Info(req.OperationName+" idempotent replay for existing workflow", "workflowID", opcWorkRequestID, "status", existing.Status)
			branch = branchReplay
		}

		return middleware.Response{Type: idempotencyResponse(req, branch, opcWorkRequestID, existing)}, nil
	}
}

func isIdempotencyGuardedOperation(op ociserver.OperationName) bool {
	switch op {
	case ociserver.CreatePoolOperation,
		ociserver.UpdatePoolOperation,
		ociserver.DeletePoolOperation,
		ociserver.UpgradePoolOperation,
		ociserver.RotateFabricPoolKeysOperation,
		ociserver.RbacRefreshPoolOperation,
		ociserver.StartMaintenanceOperation,
		ociserver.FinishMaintenanceOperation,
		ociserver.ExecuteMaintenanceOperation,
		ociserver.CreateSvmByPoolOperation,
		ociserver.UpdateSvmOperation,
		ociserver.DeleteSvmOperation:
		return true
	default:
		return false
	}
}

func idempotencyResponse(req middleware.Request, branch idempotencyBranch, opcWorkRequestID string, existing existingWorkflow) any {
	switch req.OperationName {
	case ociserver.CreatePoolOperation:
		poolOCID := createPoolRequestOCID(req)
		switch branch {
		case branchLookupError:
			return &ociserver.CreatePoolInternalServerError{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolOCID,
					ErrorMessage: "Internal server error",
				},
			}
		case branchTerminalFailure:
			return &ociserver.CreatePoolConflict{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(existing.Status),
					PoolOCID:     poolOCID,
					ErrorMessage: existing.failureMessage(),
				},
			}
		default:
			return newCreatePoolAccepted(opcWorkRequestID, poolOCID, opcWorkRequestID, existing.Status)
		}

	case ociserver.UpdatePoolOperation:
		poolOCID := pathParamString(req, "poolOCID")
		switch branch {
		case branchLookupError:
			return (*ociserver.UpdatePoolInternalServerError)(&ociserver.PoolOperationErrorResponseHeaders{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolOCID,
					ErrorMessage: "Internal server error",
				},
			})
		case branchTerminalFailure:
			return (*ociserver.UpdatePoolConflict)(&ociserver.PoolOperationErrorResponseHeaders{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(existing.Status),
					PoolOCID:     poolOCID,
					ErrorMessage: existing.failureMessage(),
				},
			})
		default:
			return newUpdatePoolAccepted(opcWorkRequestID, poolOCID, opcWorkRequestID, existing.Status)
		}

	case ociserver.DeletePoolOperation:
		poolOCID := pathParamString(req, "poolOCID")
		switch branch {
		case branchLookupError:
			return &ociserver.DeletePoolInternalServerError{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolOCID,
					ErrorMessage: "Internal server error",
				},
			}
		case branchTerminalFailure:
			return &ociserver.DeletePoolConflict{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(existing.Status),
					PoolOCID:     poolOCID,
					ErrorMessage: existing.failureMessage(),
				},
			}
		default:
			return &ociserver.DeletePoolAcceptedResponseHeaders{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.DeletePoolAcceptedResponse{
					Status:     string(existing.Status),
					WorkflowId: opcWorkRequestID,
					PoolOCID:   poolOCID,
				},
			}
		}

	case ociserver.UpgradePoolOperation:
		poolOCID := pathParamString(req, "poolOCID")
		switch branch {
		case branchLookupError:
			return createUpgradePoolErrorResponse(opcWorkRequestID, poolOCID, "Internal server error", 500)
		case branchTerminalFailure:
			return createUpgradePoolErrorResponse(opcWorkRequestID, poolOCID, existing.failureMessage(), 409)
		default:
			return &ociserver.UpgradePoolAcceptedResponseHeaders{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.UpgradePoolAcceptedResponse{
					Status:     string(existing.Status),
					WorkflowId: opcWorkRequestID,
					PoolOCID:   poolOCID,
				},
			}
		}

	case ociserver.RotateFabricPoolKeysOperation:
		poolOCID := pathParamString(req, "poolOCID")
		switch branch {
		case branchLookupError:
			return &ociserver.RotateFabricPoolKeysInternalServerError{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolOCID,
					ErrorMessage: "Internal server error",
				},
			}
		case branchTerminalFailure:
			return &ociserver.RotateFabricPoolKeysConflict{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(existing.Status),
					PoolOCID:     poolOCID,
					ErrorMessage: existing.failureMessage(),
				},
			}
		default:
			return &ociserver.RotateFabricPoolKeysAcceptedResponseHeaders{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.RotateFabricPoolKeysAcceptedResponse{
					Status:     rotateFabricPoolKeysReplayStatus(existing.Status),
					PoolOCID:   poolOCID,
					WorkflowId: ociserver.NewOptString(opcWorkRequestID),
				},
			}
		}

	case ociserver.CreateSvmByPoolOperation:
		svmOCID := createSvmRequestOCID(req)
		switch branch {
		case branchLookupError:
			return &ociserver.CreateSvmByPoolInternalServerError{
				OpcWorkRequestID: opcWorkRequestID,
				Response:         svmErrorResponse(svmOCID, "Internal server error"),
			}
		case branchTerminalFailure:
			return &ociserver.CreateSvmByPoolConflict{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.SvmOperationErrorResponse{
					Status:       string(existing.Status),
					SvmOCID:      svmOCID,
					ErrorMessage: existing.failureMessage(),
				},
			}
		default:
			return newCreateSvmAccepted(opcWorkRequestID, svmOCID, opcWorkRequestID, existing.Status)
		}

	case ociserver.UpdateSvmOperation:
		svmOCID := pathParamString(req, "svmOCID")
		switch branch {
		case branchLookupError:
			return &ociserver.UpdateSvmInternalServerError{
				OpcWorkRequestID: opcWorkRequestID,
				Response:         svmErrorResponse(svmOCID, "Internal server error"),
			}
		case branchTerminalFailure:
			return &ociserver.UpdateSvmConflict{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.SvmOperationErrorResponse{
					Status:       string(existing.Status),
					SvmOCID:      svmOCID,
					ErrorMessage: existing.failureMessage(),
				},
			}
		default:
			return newUpdateSvmAccepted(opcWorkRequestID, svmOCID, opcWorkRequestID, existing.Status)
		}

	case ociserver.DeleteSvmOperation:
		svmOCID := pathParamString(req, "svmOCID")
		switch branch {
		case branchLookupError:
			return &ociserver.DeleteSvmInternalServerError{
				OpcWorkRequestID: opcWorkRequestID,
				Response:         svmErrorResponse(svmOCID, "Internal server error"),
			}
		case branchTerminalFailure:
			return &ociserver.DeleteSvmConflict{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.SvmOperationErrorResponse{
					Status:       string(existing.Status),
					SvmOCID:      svmOCID,
					ErrorMessage: existing.failureMessage(),
				},
			}
		default:
			return newDeleteSvmAccepted(opcWorkRequestID, svmOCID, opcWorkRequestID, existing.Status)
		}

	case ociserver.RbacRefreshPoolOperation:
		poolOCID := pathParamString(req, "poolOCID")
		switch branch {
		case branchLookupError:
			return &ociserver.RbacRefreshPoolInternalServerError{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(workflowquery.WorkflowStatusFailed),
					PoolOCID:     poolOCID,
					ErrorMessage: "Internal server error",
				},
			}
		case branchTerminalFailure:
			return &ociserver.RbacRefreshPoolConflict{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.PoolOperationErrorResponse{
					Status:       string(existing.Status),
					PoolOCID:     poolOCID,
					ErrorMessage: existing.failureMessage(),
				},
			}
		default:
			return &ociserver.RbacRefreshAcceptedResponseHeaders{
				OpcWorkRequestID: opcWorkRequestID,
				Response: ociserver.RbacRefreshAcceptedResponse{
					Status:     string(existing.Status),
					WorkflowId: opcWorkRequestID,
					PoolOCID:   poolOCID,
				},
			}
		}

	case ociserver.StartMaintenanceOperation:
		poolOCID := pathParamString(req, "poolOCID")
		switch branch {
		case branchLookupError:
			return startMaintenanceErrorResponse(opcWorkRequestID, maintErrInternal, newMaintenanceErrorBody(poolOCID, "Internal server error"))
		case branchTerminalFailure:
			return startMaintenanceErrorResponse(opcWorkRequestID, maintErrConflict, newMaintenanceErrorBody(poolOCID, existing.failureMessage()))
		default:
			return maintenanceReplayAccepted(opcWorkRequestID, existing, ociserver.MaintenanceAcceptedResponseStatusTakeoverInProgress)
		}

	case ociserver.FinishMaintenanceOperation:
		poolOCID := pathParamString(req, "poolOCID")
		switch branch {
		case branchLookupError:
			return finishMaintenanceErrorResponse(opcWorkRequestID, maintErrInternal, newMaintenanceErrorBody(poolOCID, "Internal server error"))
		case branchTerminalFailure:
			return finishMaintenanceErrorResponse(opcWorkRequestID, maintErrConflict, newMaintenanceErrorBody(poolOCID, existing.failureMessage()))
		default:
			return maintenanceReplayAccepted(opcWorkRequestID, existing, ociserver.MaintenanceAcceptedResponseStatusGivebackInProgress)
		}

	case ociserver.ExecuteMaintenanceOperation:
		poolOCID := pathParamString(req, "poolOCID")
		switch branch {
		case branchLookupError:
			return executeMaintenanceErrorResponse(opcWorkRequestID, maintErrInternal, newMaintenanceErrorBody(poolOCID, "Internal server error"))
		case branchTerminalFailure:
			return executeMaintenanceErrorResponse(opcWorkRequestID, maintErrConflict, newMaintenanceErrorBody(poolOCID, existing.failureMessage()))
		default:
			return maintenanceReplayAccepted(opcWorkRequestID, existing, maintenanceStatus(executeMaintenanceRequestOperationType(req)))
		}
	}

	return nil
}

// maintenanceReplayAccepted builds the 202 replay response for maintenance
// operations, mapping a completed prior workflow to the terminal "completed"
// status and any non-terminal status to the operation-specific in-progress
// status.
func maintenanceReplayAccepted(opcWorkRequestID string, existing existingWorkflow, inProgressStatus ociserver.MaintenanceAcceptedResponseStatus) *ociserver.MaintenanceAcceptedResponseHeaders {
	if isCompleted(existing.Status) {
		return newMaintenanceAccepted(opcWorkRequestID, opcWorkRequestID, ociserver.MaintenanceAcceptedResponseStatusCompleted)
	}
	return newMaintenanceAccepted(opcWorkRequestID, opcWorkRequestID, inProgressStatus)
}

func executeMaintenanceRequestOperationType(req middleware.Request) ociserver.MaintenanceRequestOperationType {
	if body, ok := req.Body.(*ociserver.MaintenanceRequest); ok && body != nil {
		return body.OperationType
	}
	return ""
}

func createPoolRequestOCID(req middleware.Request) string {
	if body, ok := req.Body.(*ociserver.CreatePoolRequest); ok && body != nil {
		return body.PoolOCID
	}
	return ""
}

func createSvmRequestOCID(req middleware.Request) string {
	if body, ok := req.Body.(*ociserver.CreateSvmRequest); ok && body != nil {
		return strings.TrimSpace(body.SvmOCID)
	}
	return ""
}

func rotateFabricPoolKeysReplayStatus(status workflowquery.WorkflowStatus) ociserver.RotateFabricPoolKeysAcceptedResponseStatus {
	if status == workflowquery.WorkflowStatusCompleted {
		return ociserver.RotateFabricPoolKeysAcceptedResponseStatusNoChange
	}
	return ociserver.RotateFabricPoolKeysAcceptedResponseStatusInProgress
}

func pathParamString(req middleware.Request, name string) string {
	if v, ok := req.Params.Path(name); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
