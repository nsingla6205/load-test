package gcp

import (
	"context"
	"fmt"
	"go.temporal.io/api/enums/v1"
	"strings"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
)

var getOntapProviderForHost = _getOntapProviderForHost

func _getOntapProviderForHost(ctx context.Context, details vsa.ProviderDetails) vsa.Provider {
	return vsa.NewProvider(ctx, details)
}

// CreateOntapHost persists a new host in CREATING state and starts async ONTAP onboarding.
func (o *GCPOrchestrator) CreateOntapHost(ctx context.Context, params *commonparams.CreateHostParams) (*datamodel.Host, string, error) {
	return createOntapHost(ctx, o.storage, o.temporal, params)
}

func createOntapHost(
	ctx context.Context,
	se database.Storage,
	temporal client.Client,
	params *commonparams.CreateHostParams,
) (*datamodel.Host, string, error) {
	logger := util.GetLogger(ctx)

	dbHost, err := prepareHostRecord(ctx, se, params)
	if err != nil {
		return nil, "", err
	}
	logger.Infof("Host record created: uuid=%s name=%s ip=%s state=%s", dbHost.UUID, dbHost.Name, dbHost.IPAddress, dbHost.State)

	var createdJob *datamodel.Job
	defer func() {
		if err != nil && dbHost != nil && dbHost.UUID != "" && dbHost.State == datamodel.LifeCycleStateCreating {
			dbHost.State = datamodel.LifeCycleStateError
			dbHost.StateDetails = datamodel.LifeCycleStateCreationErrorDetails
			if _, updateErr := se.UpdateHost(ctx, dbHost); updateErr != nil {
				logger.Errorf("Failed to mark host %s as ERROR after workflow start failure: %v", dbHost.UUID, updateErr)
			}
		}
	}()

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeCreateHost),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  dbHost.Name,
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: dbHost.UUID,
		},
	}
	createdJob, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Errorf("Failed to create host onboard job: %v", err)
		return nil, "", fmt.Errorf("failed to create host onboard job: %w", err)
	}
	logger.Infof("Host onboard job created: jobUUID=%s hostUUID=%s", createdJob.UUID, dbHost.UUID)

	if temporal == nil {
		return nil, "", fmt.Errorf("temporal client is not configured")
	}

	defer func() {
		if err != nil {
			if updateErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); updateErr != nil {
				logger.Errorf("Failed to update job status to error: jobID=%s error=%v", createdJob.UUID, updateErr)
			}
		}
	}()

	wfParams := &commonparams.OnboardHostWorkflowParams{
		HostUUID: dbHost.UUID,
		JobUUID:  createdJob.UUID,
		DbHost:   dbHost,
	}
	runTimeout := workflowengine.GetWorkflowGlobalTimeout()
	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    runTimeout,
		},
		workflows.OnboardHostWorkflow,
		wfParams,
	)
	if err != nil {
		logger.Errorf("Failed to start host onboard workflow: %v", err)
		return nil, "", fmt.Errorf("failed to start host onboard workflow: %w", err)
	}
	logger.Infof("Host onboard workflow started: workflowID=%s hostUUID=%s jobUUID=%s", createdJob.WorkflowID, dbHost.UUID, createdJob.UUID)

	return dbHost, createdJob.UUID, nil
}

func prepareHostRecord(ctx context.Context, se database.Storage, params *commonparams.CreateHostParams) (*datamodel.Host, error) {
	if params == nil {
		return nil, customerrors.NewBadRequestErr("request body is required")
	}
	if strings.TrimSpace(params.Name) == "" ||
		strings.TrimSpace(params.Username) == "" ||
		strings.TrimSpace(string(params.Password)) == "" {
		return nil, customerrors.NewBadRequestErr("name, username, and password are required")
	}

	encryptedPassword, err := utils.EncryptPassword(params.Password)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt admin password for host %q: %w", params.Name, err)
	}

	protocolInput := ""
	if params.Protocol != nil {
		protocolInput = *params.Protocol
	}
	portInput := 0
	if params.Port != nil {
		portInput = *params.Port
	}
	protocol, port, normErr := datamodel.NormalizeExternalClusterProtocolAndPort(protocolInput, portInput)
	if normErr != nil {
		return nil, customerrors.NewBadRequestErr(normErr.Error())
	}

	ipAddress := ""
	if params.IPAddress != nil {
		ipAddress = strings.TrimSpace(*params.IPAddress)
	}
	if ipAddress == "" {
		ipAddress = strings.TrimSpace(params.Name)
	}

	count, err := se.CountHostsWithIPAddress(ctx, ipAddress, false)
	if err != nil {
		return nil, fmt.Errorf("failed checking host ip uniqueness: %w", err)
	}
	if count > 0 {
		return nil, customerrors.NewConflictErr("IP address already assigned to host")
	}

	dbHost := &datamodel.Host{
		Name:             strings.TrimSpace(params.Name),
		VendorID:         utils.TrimOrNil(params.VendorID),
		Port:             &port,
		Protocol:         protocol,
		Username:         strings.TrimSpace(params.Username),
		Password:         *encryptedPassword,
		State:            datamodel.LifeCycleStateCreating,
		StateDetails:     datamodel.LifeCycleStateCreatingDetails,
		Label:            utils.TrimOrEmpty(params.Label),
		IPAddress:        ipAddress,
		EfficiencyFactor: datamodel.HostEfficiencyFactorOrDefault(params.EfficiencyFactor),
		Attributes: &datamodel.HostAttributes{
			MaintenanceMode: utils.BoolOrFalse(params.MaintenanceMode),
			PublicAccess:    utils.BoolOrFalse(params.PublicAccess),
			Version:         strings.TrimSpace(params.OntapVersion),
		},
	}


	createdHost, err := se.CreateHost(ctx, dbHost)
	if err != nil {
		if customerrors.IsConflictErr(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to create host: %w", err)
	}
	return createdHost, nil
}

// GetHost returns a single host by UUID.
func (o *GCPOrchestrator) GetHost(ctx context.Context, hostUUID string) (*datamodel.Host, error) {
	return o.storage.GetHost(ctx, hostUUID)
}

// ListHosts returns all active hosts.
func (o *GCPOrchestrator) ListHosts(ctx context.Context) ([]*datamodel.Host, error) {
	return o.storage.ListHosts(ctx)
}

// UpdateHost applies a full host spec update for v1_updateHost.
func (o *GCPOrchestrator) UpdateHost(ctx context.Context, params *commonparams.UpdateHostParams) (*datamodel.Host, error) {
	dbHost, err := o.storage.GetHost(ctx, params.HostUUID)
	if err != nil {
		return nil, err
	}

	if err := applyUpdateParamsToHost(dbHost, params); err != nil {
		return nil, err
	}

	if string(params.Password) != log.PasswordMask {
		encryptedPassword, encErr := utils.EncryptPassword(params.Password)
		if encErr != nil {
			return nil, fmt.Errorf("failed to encrypt admin password for host %q: %w", params.Name, encErr)
		}
		dbHost.Password = *encryptedPassword
	}

	provider := getOntapProviderForHost(ctx, vsa.ProviderDetails{
		IPAddress: dbHost.IPAddress,
		Hosts: map[string]string{
			dbHost.IPAddress: dbHost.IPAddress,
		},
		Password:           string(params.Password),
		InsecureSkipVerify: dbHost.Protocol == datamodel.ExternalClusterProtocolInsecureHTTPS,
	})
	client, err := provider.CreateRESTClient()
	if err != nil {
		return nil, customerrors.NewBadRequestErr(fmt.Sprintf("unable to connect to host: %v", err))
	}
	if err := vsa.DiscoverHostSystemInfo(ctx, dbHost, client); err != nil {
		return nil, customerrors.NewBadRequestErr(fmt.Sprintf("unable to fetch cluster details: %v", err))
	}

	return o.storage.UpdateHost(ctx, dbHost)
}

// DeleteHost soft-deletes a host by UUID.
func (o *GCPOrchestrator) DeleteHost(ctx context.Context, hostUUID string) (*datamodel.Host, error) {
	host, err := o.storage.GetHost(ctx, hostUUID)
	if err != nil {
		return nil, err
	}
	return o.storage.DeleteHost(ctx, host)
}

// applyUpdateParamsToHost applies the PUT full spec onto an existing host row.
func applyUpdateParamsToHost(host *datamodel.Host, params *commonparams.UpdateHostParams) error {
	protocolInput := host.Protocol
	if params.Protocol != nil && strings.TrimSpace(*params.Protocol) != "" {
		protocolInput = strings.TrimSpace(*params.Protocol)
	}
	portInput := 0
	if params.Port != nil {
		portInput = *params.Port
	} else if host.Port != nil {
		portInput = *host.Port
	}
	protocol, port, err := datamodel.NormalizeExternalClusterProtocolAndPort(protocolInput, portInput)
	if err != nil {
		return customerrors.NewBadRequestErr(err.Error())
	}

	ipAddress := host.IPAddress
	if params.IPAddress != nil {
		ipAddress = strings.TrimSpace(*params.IPAddress)
	}
	if ipAddress == "" {
		ipAddress = strings.TrimSpace(params.Name)
	}

	host.Name = strings.TrimSpace(params.Name)
	host.Username = strings.TrimSpace(params.Username)
	host.Protocol = protocol
	host.Port = &port
	host.IPAddress = ipAddress
	if params.ExternalUUID != "" {
		host.ExternalUUID = strings.TrimSpace(params.ExternalUUID)
	}
	if params.ExternalName != "" {
		host.ExternalName = strings.TrimSpace(params.ExternalName)
	}
	if params.VendorID != nil {
		host.VendorID = utils.TrimOrNil(params.VendorID)
	}
	if params.Label != nil {
		host.Label = utils.TrimOrEmpty(params.Label)
	}
	if host.Attributes == nil {
		host.Attributes = &datamodel.HostAttributes{}
	}
	if params.MaintenanceMode != nil {
		host.Attributes.MaintenanceMode = *params.MaintenanceMode
	}
	if params.PublicAccess != nil {
		host.Attributes.PublicAccess = *params.PublicAccess
	}
	if params.EfficiencyFactor != nil {
		host.EfficiencyFactor = *params.EfficiencyFactor
	}
	return nil
}
