package activities

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk"
	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk/cvnapi/networks"
	sdkmodels "github.com/greenqloud/cloud-volumes-network/app/pkg/sdk/models"
	cvnclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvn"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/activity"
)

// POICLifCVNClient is the narrow CVN SDK interface used by the PO intercluster
// LIF ensure activity. It is satisfied by sdk.CVN.
type POICLifCVNClient interface {
	ListNetworksV1(accountUUID *string, remoteAccountID *string) ([]*sdkmodels.NetworkV1, error)
	DescribeNetworkV1(uuid string) (*sdkmodels.NetworkV1, error)
	GetSecondaryIPRangeV1(secondaryIPRangeUUID string) (*sdkmodels.SecondaryIPRangeV1, error)
	CreateVXNetworkV1(networkUUID, microzoneUUID, ipRangeUUID string) (*sdkmodels.VXNetworkV1, error)
	DescribeVXNetworkV1(uuid string) (*sdkmodels.VXNetworkV1, error)
	DescribeJobV1(jobUUID string) (*sdkmodels.JobV1, error)
	GetBorderPollTimeout() time.Duration
	GetServicePollTimeout() time.Duration
	GetPollInterval() time.Duration
}

// CreateSecondaryIPRangeWithLifType creates a CVN secondary IP range with the
// given lifType. It is a package-level var so tests can swap in a double
// without needing to reach through the POICLifCVNClient interface.
var CreateSecondaryIPRangeWithLifType = defaultCreateSecondaryIPRangeWithLifType

// defaultCreateSecondaryIPRangeWithLifType calls the raw cvnapi.Networks
// client directly rather than sdk.CVN's convenience wrapper, so a new
// LifType-aware wrapper does not need to be released with every SDK update.
func defaultCreateSecondaryIPRangeWithLifType(
	ctx context.Context,
	accountUUID, remoteAccountID string,
	subnetMaskSize int64,
	lifType string,
) (*sdkmodels.SecondaryIPRangeV1, error) {
	api, err := cvnclient.NewCVNAPIFromContext(ctx, accountUUID)
	if err != nil {
		return nil, err
	}

	params := networks.NewCreateSecondaryIPRangeV1ParamsWithContext(ctx)
	body := &sdkmodels.CreateSecondaryIPRangeV1{
		AccountUUID:     &accountUUID,
		RemoteAccountID: &remoteAccountID,
		SubnetMaskSize:  &subnetMaskSize,
	}
	if lifType != "" {
		body.LifType = &lifType
	}
	params.Body = body
	resp, err := api.Networks.CreateSecondaryIPRangeV1(params)
	if err != nil {
		return nil, err
	}
	return resp.Payload, nil
}

// POICLifCVNClientFactory builds a POICLifCVNClient for the given account.
type POICLifCVNClientFactory func(ctx context.Context, accountUUID string) (POICLifCVNClient, error)

// POICVNActivity provisions the CVN network resources (secondary IP range + VX
// network) required for PO intercluster LIFs.
type POICVNActivity struct {
	SE            database.Storage
	ClientFactory POICLifCVNClientFactory
	Config        commonparams.POICLifConfig
}

// defaultPOICLifCVNClientFactory wraps cvnclient.NewFromContext and adapts sdk.CVN
// to the narrow POICLifCVNClient interface.
func defaultPOICLifCVNClientFactory(ctx context.Context, accountUUID string) (POICLifCVNClient, error) {
	c, err := cvnclient.NewFromContext(ctx, accountUUID)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (a *POICVNActivity) cvnClient(ctx context.Context, accountUUID string) (POICLifCVNClient, error) {
	factory := a.ClientFactory
	if factory == nil {
		factory = defaultPOICLifCVNClientFactory
	}
	return factory(ctx, accountUUID)
}

// EnsurePOICNetworkInput is the input to EnsurePOICNetwork.
type EnsurePOICNetworkInput struct {
	AccountUUID     string
	RemoteAccountID string
	MicrozoneUUID   string
	HostUUID        string
	NodeCount       int
}

// POICNetworkOutput is the result of EnsurePOICNetwork.
type POICNetworkOutput struct {
	NetworkUUID   string
	IPRangeUUID   string
	VXNetworkUUID string
	VlanID        int
	Gateway       string
	Netmask       string
}

// EnsurePOICNetwork ensures the CVN network resources for intercluster LIFs
// exist: a secondary IP range with lifType=interclusterLIF and a VX network in
// the pool datapath microzone. It is reuse-first: an existing interclusterLIF
// range with capacity is preferred over creating a new one.
func (a *POICVNActivity) EnsurePOICNetwork(ctx context.Context, in EnsurePOICNetworkInput) (*POICNetworkOutput, error) {
	if err := a.validateInput(in); err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	if a.SE == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("POICVNActivity storage is not configured")))
	}

	activity.RecordHeartbeat(ctx, fmt.Sprintf("Ensuring IC CVN network for account %s host %s", in.AccountUUID, in.HostUUID))

	client, err := a.cvnClient(ctx, in.AccountUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Step 1: List networks and require exactly one.
	networkUUID, err := a.resolveNetwork(ctx, client, in.AccountUUID, in.RemoteAccountID)
	if err != nil {
		return nil, err
	}

	// Steps 2-4: Resolve or create an IC IP range.
	ipRangeUUID, gateway, netmask, err := a.resolveIPRange(ctx, client, in)
	if err != nil {
		return nil, err
	}

	// Step 5: Ensure VX network.
	vxUUID, vlanID, err := a.ensureVXNetwork(ctx, client, networkUUID, in.MicrozoneUUID, ipRangeUUID)
	if err != nil {
		return nil, err
	}

	return &POICNetworkOutput{
		NetworkUUID:   networkUUID,
		IPRangeUUID:   ipRangeUUID,
		VXNetworkUUID: vxUUID,
		VlanID:        vlanID,
		Gateway:       gateway,
		Netmask:       netmask,
	}, nil
}

func (a *POICVNActivity) validateInput(in EnsurePOICNetworkInput) error {
	if strings.TrimSpace(in.AccountUUID) == "" {
		return fmt.Errorf("AccountUUID must not be empty")
	}
	if strings.TrimSpace(in.RemoteAccountID) == "" {
		return fmt.Errorf("RemoteAccountID must not be empty")
	}
	if strings.TrimSpace(in.MicrozoneUUID) == "" {
		return fmt.Errorf("MicrozoneUUID must not be empty")
	}
	if strings.TrimSpace(in.HostUUID) == "" {
		return fmt.Errorf("HostUUID must not be empty")
	}
	if in.NodeCount <= 0 {
		return fmt.Errorf("NodeCount must be positive, got %d", in.NodeCount)
	}
	return nil
}

func (a *POICVNActivity) resolveNetwork(ctx context.Context, client POICLifCVNClient, accountUUID, remoteAccountID string) (string, error) {
	activity.RecordHeartbeat(ctx, "Listing CVN networks")
	acct := accountUUID
	remote := remoteAccountID
	networks, err := client.ListNetworksV1(&acct, &remote)
	if err != nil {
		return "", vsaerrors.WrapAsTemporalApplicationError(
			fmt.Errorf("ListNetworksV1 failed: %w", err))
	}
	if len(networks) == 0 {
		return "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrCVNHostNotFound,
				fmt.Errorf("no CVN network found for account %s remoteAccountID %s", accountUUID, remoteAccountID)))
	}
	if len(networks) != 1 {
		return "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("expected exactly 1 CVN network for account %s, got %d", accountUUID, len(networks))))
	}
	return networks[0].UUID, nil
}

func (a *POICVNActivity) resolveIPRange(ctx context.Context, client POICLifCVNClient, in EnsurePOICNetworkInput) (ipRangeUUID, gateway, netmask string, err error) {
	activity.RecordHeartbeat(ctx, "Checking existing IC IP ranges")

	existingRanges, dbErr := a.SE.GetInterclusterLifIPRanges(ctx, in.AccountUUID, in.RemoteAccountID)
	if dbErr != nil {
		return "", "", "", vsaerrors.WrapAsTemporalApplicationError(dbErr)
	}

	// Try reuse: iterate by created_at (already ordered by DB query).
	for _, candidate := range existingRanges {
		if candidate == nil || candidate.LifType != database.LifTypeInterclusterLIF {
			continue
		}
		if valErr := a.SE.ValidateIPAddressesAvailableInIPRange(ctx, candidate.UUID, in.NodeCount); valErr == nil {
			return candidate.UUID, candidate.Gateway, candidate.Netmask, nil
		}
	}

	// No reusable range: create a new one.
	activity.RecordHeartbeat(ctx, "Creating IC secondary IP range via CVN")
	subnetMask := int64(commonparams.POPoolSubnetMaskSize(a.Config.SubnetMaskSize))
	resp, createErr := CreateSecondaryIPRangeWithLifType(
		ctx, in.AccountUUID, in.RemoteAccountID, subnetMask, database.LifTypeInterclusterLIF)
	if createErr != nil {
		return "", "", "", vsaerrors.WrapAsTemporalApplicationError(
			fmt.Errorf("CreateSecondaryIPRangeWithLifTypeV1 failed: %w", createErr))
	}
	if resp == nil || resp.Job == nil {
		return "", "", "", vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError,
				fmt.Errorf("CVN returned nil secondary IP range or job")))
	}

	// Poll the job.
	if pollErr := a.pollJob(ctx, client, resp.Job.UUID, client.GetBorderPollTimeout()); pollErr != nil {
		return "", "", "", vsaerrors.WrapAsTemporalApplicationError(
			fmt.Errorf("IC secondary IP range job failed: %w", pollErr))
	}

	// Fetch the completed secondary range to get the iprangeUUID.
	getResp, getErr := client.GetSecondaryIPRangeV1(resp.UUID)
	if getErr != nil {
		return "", "", "", vsaerrors.WrapAsTemporalApplicationError(
			fmt.Errorf("GetSecondaryIPRangeV1 failed: %w", getErr))
	}
	if getResp == nil || getResp.IprangeUUID == "" {
		return "", "", "", vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError,
				fmt.Errorf("GetSecondaryIPRangeV1 returned empty iprangeUUID")))
	}

	// Load the persisted ip_ranges row and validate lifType.
	ipRange, loadErr := a.SE.GetIPRangeByUUID(ctx, getResp.IprangeUUID)
	if loadErr != nil {
		return "", "", "", vsaerrors.WrapAsTemporalApplicationError(loadErr)
	}
	if ipRange.LifType != database.LifTypeInterclusterLIF {
		return "", "", "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("ip range %s has lif_type %q, expected %q",
					ipRange.UUID, ipRange.LifType, database.LifTypeInterclusterLIF)))
	}

	if valErr := a.SE.ValidateIPAddressesAvailableInIPRange(ctx, ipRange.UUID, in.NodeCount); valErr != nil {
		return "", "", "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("newly created IP range %s cannot supply %d addresses: %w",
					ipRange.UUID, in.NodeCount, valErr)))
	}

	return ipRange.UUID, ipRange.Gateway, ipRange.Netmask, nil
}

func (a *POICVNActivity) ensureVXNetwork(ctx context.Context, client POICLifCVNClient, networkUUID, microzoneUUID, ipRangeUUID string) (vxUUID string, vlanID int, err error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Ensuring VX network for microzone %s ipRange %s", microzoneUUID, ipRangeUUID))

	// Look up the network to find existing VX networks.
	network, descErr := a.describeNetworkForVX(ctx, client, networkUUID)
	if descErr != nil {
		return "", 0, descErr
	}

	// Mirror the CVN SDK _ensureVXNetwork logic.
	for _, vx := range network.Vxnetworks {
		if vx == nil || vx.MicrozoneUUID == nil {
			continue
		}
		if *vx.MicrozoneUUID == microzoneUUID && vx.IprangeUUID == ipRangeUUID {
			switch vx.LifeCycleState {
			case sdkmodels.LifeCycleStateAvailable:
				return vx.UUID, int(vx.L2VlanID), nil
			case sdkmodels.LifeCycleStateCreating:
				return a.pollAndDescribeVX(ctx, client, vx)
			case sdkmodels.LifeCycleStateError, sdkmodels.LifeCycleStateDeleting:
				return a.createAndPollVX(ctx, client, networkUUID, microzoneUUID, ipRangeUUID)
			default:
				return "", 0, vsaerrors.WrapAsTemporalApplicationError(
					fmt.Errorf("unexpected VX lifecycle state %q for VX %s", vx.LifeCycleState, vx.UUID))
			}
		}
	}

	// No matching VX found: create.
	return a.createAndPollVX(ctx, client, networkUUID, microzoneUUID, ipRangeUUID)
}

func (a *POICVNActivity) describeNetworkForVX(_ context.Context, client POICLifCVNClient, networkUUID string) (*sdkmodels.NetworkV1, error) {
	network, err := client.DescribeNetworkV1(networkUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			fmt.Errorf("DescribeNetworkV1 for VX lookup failed: %w", err))
	}
	if network == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrCVNHostNotFound,
				fmt.Errorf("CVN network %s not found", networkUUID)))
	}
	return network, nil
}

func (a *POICVNActivity) createAndPollVX(ctx context.Context, client POICLifCVNClient, networkUUID, microzoneUUID, ipRangeUUID string) (string, int, error) {
	activity.RecordHeartbeat(ctx, "Creating VX network via CVN")
	vx, err := client.CreateVXNetworkV1(networkUUID, microzoneUUID, ipRangeUUID)
	if err != nil {
		return "", 0, vsaerrors.WrapAsTemporalApplicationError(
			fmt.Errorf("CreateVXNetworkV1 failed: %w", err))
	}
	return a.pollAndDescribeVX(ctx, client, vx)
}

func (a *POICVNActivity) pollAndDescribeVX(ctx context.Context, client POICLifCVNClient, vx *sdkmodels.VXNetworkV1) (string, int, error) {
	if vx == nil {
		return "", 0, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("nil VX network from CVN")))
	}
	if vx.Job != nil && vx.Job.UUID != "" {
		if pollErr := a.pollJob(ctx, client, vx.Job.UUID, client.GetServicePollTimeout()); pollErr != nil {
			return "", 0, vsaerrors.WrapAsTemporalApplicationError(
				fmt.Errorf("VX network job failed: %w", pollErr))
		}
	}

	described, err := client.DescribeVXNetworkV1(vx.UUID)
	if err != nil {
		return "", 0, vsaerrors.WrapAsTemporalApplicationError(
			fmt.Errorf("DescribeVXNetworkV1 failed: %w", err))
	}
	if described == nil {
		return "", 0, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError,
				fmt.Errorf("DescribeVXNetworkV1 returned nil for VX %s", vx.UUID)))
	}
	return described.UUID, int(described.L2VlanID), nil
}

func (a *POICVNActivity) pollJob(ctx context.Context, client POICLifCVNClient, jobUUID string, timeout time.Duration) error {
	interval := client.GetPollInterval()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Polling CVN job %s", jobUUID))
		job, err := client.DescribeJobV1(jobUUID)
		if err != nil {
			return err
		}
		if job == nil {
			return fmt.Errorf("DescribeJobV1 returned nil for job %s", jobUUID)
		}
		switch job.State {
		case "done":
			return nil
		case "error":
			return fmt.Errorf("CVN job %s failed: %s", jobUUID, job.StateDetails)
		case "ongoing", "scheduled":
			// Heartbeat before the wait so a hung DescribeJobV1 on the
			// next iteration still emitted at least one heartbeat this
			// iteration. Ctx cancellation returns the bare context error:
			// Temporal maps context.Canceled / context.DeadlineExceeded
			// on its own, and wrapping would look like a retriable failure.
			activity.RecordHeartbeat(ctx, fmt.Sprintf("polling CVN job %s status=%s", jobUUID, job.State))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(interval):
			}
			continue
		default:
			return fmt.Errorf("unknown CVN job state %q for job %s", job.State, jobUUID)
		}
	}
	return fmt.Errorf("CVN job %s timed out after %v", jobUUID, timeout)
}

// Compile-time assertion that sdk.CVN satisfies POICLifCVNClient.
var _ POICLifCVNClient = (sdk.CVN)(nil)
