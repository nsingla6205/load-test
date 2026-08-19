package activities

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ontapmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/activity"
)

// poGetProviderForHost is overridden in tests to avoid live ONTAP connections.
var poGetProviderForHost = vsa.GetProviderForHost

// PO (Performance Offering) pool ONTAP-on-cluster activities.
//
// These activities provision an SVM, data LIFs and the fixed Extreme-tier QoS
// offering against a pre-existing ONTAP cluster (the PO path) instead of
// deploying VSA VMs via VLM. Cluster connectivity is resolved from the hosts
// table (management IP, protocol, and encrypted admin credentials).

// ontapRestPortTypeVlan is the ONTAP port "type" value used to filter VLAN ports.
const ontapRestPortTypeVlan = "vlan"

// PO data LIF service policy for NFS file access.
const POServicePolicyNfs = "default-data-files"

// isAlreadyExistsErr reports whether err looks like an ONTAP "already exists"
// / conflict error, which the idempotent VLAN-port creation can safely ignore.
func isAlreadyExistsErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "already been") ||
		strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "conflict")
}

// POClusterClient is the narrow set of ONTAP operations the PO pool path needs
// against a pre-existing cluster. It is implemented over core/vsa +
// core/ontap-rest and is injected as a factory so the activities can be unit
// tested without a live cluster.
type POClusterClient interface {
	GetNodeNames() ([]string, error)
	EnsureSvm(in CreatePOSvmInput) (svmUUID string, err error)
	EnsureMgmtPolicies(svmUUID string) error
	DeleteSvmByName(svmName string) error
	EnsureIPSpace(name string) error
	EnsureBroadcastDomain(ipSpace, name string) error
	EnsureVlanNetPorts(ipSpace, broadcastDomain, basePort string, vlanTag int, nodeNames []string) error
	EnsureDataLif(in PODataLifSpec) error
	CreateDefaultRoute(svmName, gateway, ipSpace string) error
	ApplyQoSPolicy(in ApplyQoSClientParams) error
	ConfigureKmsOnSvm(in ConfigureKmsOnSvmInput) error
	CleanupPOAccountClusterNetwork(accountUUID, ipSpace string) error
	HasSVMsInIPSpace(ipSpace string) (bool, error)

	// Intercluster LIF operations.
	GetInterclusterLifs(servicePolicy string) ([]*vsa.InterclusterLif, error)
	CreateInterclusterLif(in POInterclusterLifSpec) (*POInterclusterLifResult, error)
	DeleteInterclusterLif(externalUUID string) error
	DeleteIPSpaceDefaultRoute(ipSpace, gateway string) error
	DeleteVlanNetPorts(ipSpace, broadcastDomain, basePort string, vlanTag int, nodeNames []string) error
	DeleteBroadcastDomain(ipSpace, name string) error
	DeleteIPSpace(name string) error
}

// ApplyQoSClientParams carries the already-computed QoS ceilings to the cluster
// client (the tier→ceiling calculation happens in the activity).
type ApplyQoSClientParams struct {
	SvmName       string
	SvmUUID       string
	PolicyName    string
	MaxThroughput int64
	MaxIOPS       int64
}

// POInterclusterLifSpec describes a single intercluster LIF to create.
type POInterclusterLifSpec struct {
	Name          string
	IPAddress     string
	Netmask       string
	HomeNode      string
	HomePort      string
	ServicePolicy string
	IPSpace       string
}

// POInterclusterLifResult is the ONTAP response for a created intercluster LIF.
type POInterclusterLifResult struct {
	Name         string
	ExternalUUID string
}

// POClusterClientFactory builds a POClusterClient for the PO host identified by hostUUID.
type POClusterClientFactory func(ctx context.Context, hostUUID string) (POClusterClient, error)

// POOntapActivity wraps ONTAP-on-cluster operations for the PO pool path.
type POOntapActivity struct {
	SE database.Storage
	// ClientFactory, when nil, defaults to a client built from cluster config.
	ClientFactory POClusterClientFactory
}

// CreatePOSvmInput is the input to CreatePOSvm.
type CreatePOSvmInput struct {
	HostUUID  string
	SvmName   string
	EnableNfs bool
	// IPSpace is the ONTAP ipspace the SVM must belong to (must match data LIF ports).
	IPSpace string
}

// CreatePOSvmOutput is the output of CreatePOSvm.
type CreatePOSvmOutput struct {
	SvmName string
	SvmUUID string
}

// EnsurePOSvmMgmtPoliciesInput is the input to EnsurePOSvmMgmtPolicies.
type EnsurePOSvmMgmtPoliciesInput struct {
	HostUUID string
	SvmUUID  string
}

// PODataLifSpec describes a single data LIF to create on the SVM.
type PODataLifSpec struct {
	SvmName       string
	Name          string
	IPAddress     string
	Netmask       string
	HomeNode      string
	HomePort      string
	ServicePolicy string
}

// CreatePODataLifsInput is the input to CreatePODataLifs.
type CreatePODataLifsInput struct {
	HostUUID string
	SvmName  string
	Lifs     []PODataLifSpec
}

// ApplyQoSPolicyInput is the input to ApplyQoSPolicy. The throughput/IOPS
// ceilings are derived per tier from offering (ServiceLevel) × pool size
// (SizeInBytes) inside the activity, rather than being fixed env values.
type ApplyQoSPolicyInput struct {
	HostUUID     string
	SvmName      string
	SvmUUID      string
	ServiceLevel string
	SizeInBytes  uint64
}

// ConfigureKmsOnSvmInput is the input to ConfigureKmsOnSvm.
type ConfigureKmsOnSvmInput struct {
	HostUUID          string
	SvmName           string
	KmsConfigId       string
	KeyName           string
	KeyRingName       string
	KeyRingLocation   string
	ProjectID         string
	PrivilegedAccount string
}

// DeletePOSvmInput is the input to DeletePOSvm.
type DeletePOSvmInput struct {
	HostUUID string
	SvmName  string
}

// CleanupClusterNetworkInput is the input to CleanupPOAccountClusterNetwork.
type CleanupClusterNetworkInput struct {
	HostUUID    string
	AccountUUID string
	IPSpace     string
}

// CleanupClusterNetworkIfIPSpaceUnusedInput is the input to
// CleanupPOAccountClusterNetworkIfIPRangeUnused. When no active SVMs remain on the
// ipspace (in VCP and on ONTAP), the PO network objects for that ipspace (VLAN ports,
// broadcast domain, ipspace) are removed from the cluster.
type CleanupClusterNetworkIfIPSpaceUnusedInput struct {
	HostUUID    string
	AccountUUID string
	IPSpace     string
}

// IsLastActiveSVMOnIPSpace reports whether no active (non-deleted) SVMs remain on
// the given ONTAP ipspace. Used as the primary DB gate before tearing down VLAN/BD/IPspace
// during PO pool delete or create rollback.
func IsLastActiveSVMOnIPSpace(ctx context.Context, se database.Storage, ipSpace string) (bool, error) {
	if se == nil {
		return false, fmt.Errorf("storage is not configured")
	}
	ipSpace = strings.TrimSpace(ipSpace)
	if ipSpace == "" {
		return false, fmt.Errorf("ipspace must not be empty")
	}
	count, err := se.CountActiveSVMsByIPSpace(ctx, ipSpace)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

func validatePOHostUUID(hostUUID string) error {
	if strings.TrimSpace(hostUUID) == "" {
		return errors.New(commonparams.POValidationHostUUIDMsg)
	}
	return nil
}

func validatePOIPSpace(ipSpace string) error {
	if strings.TrimSpace(ipSpace) == "" {
		return errors.New("ipspace must not be empty for PO pool provisioning")
	}
	return nil
}

func (a *POOntapActivity) client(ctx context.Context, hostUUID string) (POClusterClient, error) {
	if err := validatePOHostUUID(hostUUID); err != nil {
		return nil, err
	}
	if a.ClientFactory != nil {
		return a.ClientFactory(ctx, hostUUID)
	}
	return a.defaultPOClusterClient(ctx, hostUUID)
}

// GetPONodeNamesInput is the input to GetPONodeNames.
type GetPONodeNamesInput struct {
	HostUUID string
}

// GetPONodeNames enumerates the data nodes of the pre-existing cluster so the
// workflow can spread one NFS data LIF per node (eager multi-LIF).
func (a *POOntapActivity) GetPONodeNames(ctx context.Context, in GetPONodeNamesInput) ([]string, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Enumerating nodes on ONTAP host %s", in.HostUUID))
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	nodes, err := client.GetNodeNames()
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if len(nodes) == 0 {
		return nil, vsaerrors.WrapAsTemporalApplicationError(errors.New("cluster returned no nodes"))
	}
	return nodes, nil
}

// EnsurePOIPSpaceInput is the input to EnsurePOIPSpace.
type EnsurePOIPSpaceInput struct {
	HostUUID string
	Name     string
}

// EnsurePOIPSpace ensures the ONTAP ipspace for a PO pool datapath exists on the
// cluster (idempotent).
func (a *POOntapActivity) EnsurePOIPSpace(ctx context.Context, in EnsurePOIPSpaceInput) error {
	if err := validatePOIPSpace(in.Name); err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Ensuring ipspace %s on ONTAP host %s", in.Name, in.HostUUID))
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	if err := client.EnsureIPSpace(in.Name); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// EnsurePOBroadcastDomainInput is the input to EnsurePOBroadcastDomain.
type EnsurePOBroadcastDomainInput struct {
	HostUUID string
	IPSpace  string
	Name     string
}

// EnsurePOBroadcastDomain ensures the broadcast domain used by the PO data LIFs
// exists on the cluster (idempotent).
func (a *POOntapActivity) EnsurePOBroadcastDomain(ctx context.Context, in EnsurePOBroadcastDomainInput) error {
	if in.Name == "" {
		return nil
	}
	if err := validatePOIPSpace(in.IPSpace); err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Ensuring broadcast domain %s on ONTAP host %s", in.Name, in.HostUUID))
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	if err := client.EnsureBroadcastDomain(in.IPSpace, in.Name); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// EnsurePOVlanPortsInput is the input to EnsurePOVlanPorts.
type EnsurePOVlanPortsInput struct {
	HostUUID        string
	IPSpace         string
	BroadcastDomain string
	// BasePort is the physical/LAG port each per-node VLAN port is created on.
	BasePort  string
	VlanTag   int
	NodeNames []string
}

// EnsurePOVlanPorts creates one VLAN port per node on BasePort, bound to the
// broadcast domain, so that data LIFs can be homed on the per-node VLAN
// interface (matching SDE's per-node VLAN model). It is idempotent: nodes that
// already have the VLAN port are skipped. No-op when BroadcastDomain is empty,
// VlanTag is zero, or there are no nodes.
func (a *POOntapActivity) EnsurePOVlanPorts(ctx context.Context, in EnsurePOVlanPortsInput) error {
	if in.BroadcastDomain == "" || in.VlanTag == 0 || len(in.NodeNames) == 0 {
		return nil
	}
	if err := validatePOIPSpace(in.IPSpace); err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Ensuring VLAN %d ports on %d node(s) of ONTAP host %s", in.VlanTag, len(in.NodeNames), in.HostUUID))
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	if err := client.EnsureVlanNetPorts(in.IPSpace, in.BroadcastDomain, in.BasePort, in.VlanTag, in.NodeNames); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// CreatePODefaultRouteInput is the input to CreatePODefaultRoute.
type CreatePODefaultRouteInput struct {
	HostUUID string
	SvmName  string
	Gateway  string
	IPSpace  string
}

// CreatePODefaultRoute creates the SVM's default (gateway) route from the CVN
// gateway. No-op when Gateway is empty.
func (a *POOntapActivity) CreatePODefaultRoute(ctx context.Context, in CreatePODefaultRouteInput) error {
	if in.Gateway == "" {
		return nil
	}
	if err := validatePOIPSpace(in.IPSpace); err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Creating default route via %s on SVM %s", in.Gateway, in.SvmName))
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	if err := client.CreateDefaultRoute(in.SvmName, in.Gateway, in.IPSpace); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// CreatePOSvm creates the SVM on the pre-existing cluster. It is idempotent by
// name: an existing SVM with the same name is reused.
func (a *POOntapActivity) CreatePOSvm(ctx context.Context, in CreatePOSvmInput) (*CreatePOSvmOutput, error) {
	if in.SvmName == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("svm name must not be empty")))
	}
	if err := validatePOIPSpace(in.IPSpace); err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Creating SVM %s on ONTAP host %s", in.SvmName, in.HostUUID))
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	svmUUID, err := client.EnsureSvm(in)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return &CreatePOSvmOutput{SvmName: in.SvmName, SvmUUID: svmUUID}, nil
}

// EnsurePOSvmMgmtPolicies enables CIFS and LDAP nsswitch sources on the SVM so
// default-mgmt-cifs and default-mgmt-ldap policies can be applied.
func (a *POOntapActivity) EnsurePOSvmMgmtPolicies(ctx context.Context, in EnsurePOSvmMgmtPoliciesInput) error {
	if in.SvmUUID == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("svm uuid must not be empty")))
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Ensuring mgmt policies on SVM %s", in.SvmUUID))
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	if err := client.EnsureMgmtPolicies(in.SvmUUID); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// CreatePODataLifs creates the data LIF(s) for the SVM from the CVN datapath
// outputs (VLAN / IP range / gateway).
func (a *POOntapActivity) CreatePODataLifs(ctx context.Context, in CreatePODataLifsInput) error {
	if len(in.Lifs) == 0 {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("at least one data LIF spec is required")))
	}
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	for _, lif := range in.Lifs {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Creating data LIF %s on SVM %s", lif.Name, in.SvmName))
		spec := lif
		if spec.SvmName == "" {
			spec.SvmName = in.SvmName
		}
		if err := client.EnsureDataLif(spec); err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}
	return nil
}

// ApplyQoSPolicy applies the per-tier QoS offering (throughput/IOPS derived from
// offering × pool size) to the SVM.
func (a *POOntapActivity) ApplyQoSPolicy(ctx context.Context, in ApplyQoSPolicyInput) error {
	if in.SvmUUID == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("svm uuid must not be empty")))
	}
	qos, qErr := commonparams.ComputePOQoS(in.ServiceLevel, in.SizeInBytes)
	if qErr != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, qErr))
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Applying QoS policy %s to SVM %s", qos.PolicyName, in.SvmName))
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	if err := client.ApplyQoSPolicy(ApplyQoSClientParams{
		SvmName:       in.SvmName,
		SvmUUID:       in.SvmUUID,
		PolicyName:    qos.PolicyName,
		MaxThroughput: qos.MaxThroughput,
		MaxIOPS:       qos.MaxIOPS,
	}); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// ConfigureKmsOnSvm configures KMS/CMEK on the SVM when a KMS config is
// requested. No-op when KmsConfigId is empty.
func (a *POOntapActivity) ConfigureKmsOnSvm(ctx context.Context, in ConfigureKmsOnSvmInput) error {
	if in.KmsConfigId == "" {
		return nil
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Configuring KMS on SVM %s", in.SvmName))
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	if err := client.ConfigureKmsOnSvm(in); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// DeletePOSvm deletes the SVM from the pre-existing cluster. It is idempotent:
// a not-found SVM is treated as success.
func (a *POOntapActivity) DeletePOSvm(ctx context.Context, in DeletePOSvmInput) error {
	if in.SvmName == "" {
		return nil
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Deleting SVM %s on ONTAP host %s", in.SvmName, in.HostUUID))
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	if err := client.DeleteSvmByName(in.SvmName); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// CleanupPOAccountClusterNetwork deletes the PO VLAN ports, broadcast domain,
// and ipspace for the given account and ipspace on the ONTAP cluster.
func (a *POOntapActivity) CleanupPOAccountClusterNetwork(ctx context.Context, in CleanupClusterNetworkInput) error {
	if strings.TrimSpace(in.AccountUUID) == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("account uuid must not be empty")))
	}
	if strings.TrimSpace(in.IPSpace) == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("ipspace must not be empty")))
	}
	if err := commonparams.ValidatePOIPSpaceForAccount(in.AccountUUID, in.IPSpace); err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Cleaning up PO network objects for account %s ipspace %s on ONTAP host %s", in.AccountUUID, in.IPSpace, in.HostUUID))
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	if err := client.CleanupPOAccountClusterNetwork(in.AccountUUID, in.IPSpace); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// CleanupPOAccountClusterNetworkIfIPRangeUnused runs CleanupPOAccountClusterNetwork only
// when the ipspace has no remaining active SVMs in VCP or on ONTAP. The activity name
// is retained for Temporal compatibility with in-flight workflows.
func (a *POOntapActivity) CleanupPOAccountClusterNetworkIfIPRangeUnused(ctx context.Context, in CleanupClusterNetworkIfIPSpaceUnusedInput) error {
	return a.cleanupPOClusterNetworkIfIPSpaceUnused(ctx, in)
}

// CleanupPOAccountClusterNetworkIfIPRangeUnusedRollback is the rollback entry point for
// PO pool create. RollbackManager appends a trailing errorMessage argument (ignored here).
// Create rollback and delete both need the same ipspace-empty gated teardown, so this
// delegates to the same private helper as the forward activity.
func (a *POOntapActivity) CleanupPOAccountClusterNetworkIfIPRangeUnusedRollback(ctx context.Context, in CleanupClusterNetworkIfIPSpaceUnusedInput, _ string) error {
	return a.cleanupPOClusterNetworkIfIPSpaceUnused(ctx, in)
}

func (a *POOntapActivity) cleanupPOClusterNetworkIfIPSpaceUnused(ctx context.Context, in CleanupClusterNetworkIfIPSpaceUnusedInput) error {
	if a.SE == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, errors.New("storage is not configured for PO ONTAP activities")))
	}
	if strings.TrimSpace(in.IPSpace) == "" {
		activity.RecordHeartbeat(ctx, "Skipping PO network cleanup; ipspace empty")
		return nil
	}
	isLast, err := IsLastActiveSVMOnIPSpace(ctx, a.SE, in.IPSpace)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if !isLast {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Skipping PO network cleanup; other active SVMs remain on ipspace %s", in.IPSpace))
		return nil
	}
	// Re-check immediately before destructive deletes to reduce races with a concurrent
	// pool create that just attached an SVM to this ipspace.
	isLast, err = IsLastActiveSVMOnIPSpace(ctx, a.SE, in.IPSpace)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if !isLast {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Aborting PO network cleanup; new active SVM appeared on ipspace %s", in.IPSpace))
		return nil
	}
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	hasSVMs, err := client.HasSVMsInIPSpace(in.IPSpace)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if hasSVMs {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Skipping PO network cleanup; ONTAP still has SVMs on ipspace %s", in.IPSpace))
		return nil
	}
	return a.CleanupPOAccountClusterNetwork(ctx, CleanupClusterNetworkInput{
		HostUUID:    in.HostUUID,
		AccountUUID: in.AccountUUID,
		IPSpace:     in.IPSpace,
	})
}

// defaultPOClusterClient builds a POClusterClient over core/vsa for the PO host,
// using admin credentials from the hosts table (password decrypted via
// vsa.GetProviderForHost).
func (a *POOntapActivity) defaultPOClusterClient(ctx context.Context, hostUUID string) (POClusterClient, error) {
	hostUUID = strings.TrimSpace(hostUUID)
	if err := validatePOHostUUID(hostUUID); err != nil {
		return nil, err
	}
	if a.SE == nil {
		return nil, errors.New("storage is not configured for PO ONTAP activities")
	}

	host, err := a.SE.GetHost(ctx, hostUUID)
	if err != nil {
		return nil, fmt.Errorf("load PO host %q: %w", hostUUID, err)
	}

	provider, err := poGetProviderForHost(ctx, host)
	if err != nil {
		return nil, err
	}
	return &poClusterClient{provider: provider}, nil
}

// poClusterClient implements POClusterClient over a core/vsa provider.
type poClusterClient struct {
	provider vsa.Provider
}

func (c *poClusterClient) GetNodeNames() ([]string, error) {
	nodes, err := c.provider.GetNodes()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n != nil && n.Name != "" {
			names = append(names, n.Name)
		}
	}
	return names, nil
}

func (c *poClusterClient) EnsureIPSpace(name string) error {
	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return err
	}
	exists, err := restClient.Networking().IpspaceExists(name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return restClient.Networking().IpspaceCreate(name)
}

func (c *poClusterClient) EnsureBroadcastDomain(ipSpace, name string) error {
	if err := validatePOIPSpace(ipSpace); err != nil {
		return err
	}
	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return err
	}
	if _, getErr := restClient.Networking().NetworkEthernetBroadcastDomainGet(&ontapRest.NetworkEthernetBroadcastDomainsGetParams{
		Name:    name,
		IPSpace: ipSpace,
	}); getErr == nil {
		return nil
	} else if !utilErrors.IsNotFoundErr(getErr) {
		return getErr
	}
	return restClient.Networking().NetworkEthernetBroadcastDomainCreate(&ontapRest.NetworkEthernetBroadcastDomainCreateParams{
		Name:    name,
		IPSpace: ipSpace,
	})
}

func (c *poClusterClient) EnsureVlanNetPorts(ipSpace, broadcastDomain, basePort string, vlanTag int, nodeNames []string) error {
	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return err
	}
	net := restClient.Networking()

	// Filter out nodes that already have the VLAN port configured in this
	// ipspace (idempotent on retry / re-run).
	existing, err := net.NetworkEthernetPortsGet(&ontapRest.NetworkEthernetPortsGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"name", "node.name"}},
		IPSpace:    ipSpace,
		VlanTag:    vlanTag,
		Type:       ontapRestPortTypeVlan,
	})
	if err != nil {
		return err
	}
	configured := make(map[string]bool, len(existing))
	for _, p := range existing {
		if p != nil && p.Node != nil && p.Node.Name != nil {
			configured[*p.Node.Name] = true
		}
	}

	for _, node := range nodeNames {
		if configured[node] {
			continue
		}
		if err := net.NetworkEthernetPortCreateVLAN(&ontapRest.NetworkEthernetPortCreateVLANParams{
			IPSpace:         ipSpace,
			BroadcastDomain: broadcastDomain,
			Node:            node,
			BasePort:        basePort,
			VlanTag:         vlanTag,
		}); err != nil {
			// Tolerate "already exists" style conflicts: the VLAN port may have
			// been created concurrently or missed by the filter above.
			if !isAlreadyExistsErr(err) {
				return err
			}
		}
	}
	return nil
}

// CreateDefaultRoute creates a default (gateway) route in ipSpace. When
// svmName is empty the route is IPspace-scoped (shared by every SVM in the
// ipspace), matching the IC LIF ensure semantics. The "already exists"
// case is swallowed so retries and re-entrant ensure runs stay idempotent.
func (c *poClusterClient) CreateDefaultRoute(svmName, gateway, ipSpace string) error {
	if err := validatePOIPSpace(ipSpace); err != nil {
		return err
	}
	err := c.provider.CreateNetworkIpRoute(vsa.CreateNetworkIPRouteParams{
		SvmName: svmName,
		Gateway: gateway,
		IPSpace: ipSpace,
	})
	if err != nil {
		if isAlreadyExistsErr(err) {
			return nil
		}
		return err
	}
	return nil
}

func (c *poClusterClient) EnsureSvm(in CreatePOSvmInput) (string, error) {
	if err := validatePOIPSpace(in.IPSpace); err != nil {
		return "", err
	}
	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return "", err
	}
	existing, getErr := restClient.SVM().SvmGet(&ontapRest.SvmGetParams{SvmName: in.SvmName})
	if getErr == nil && existing != nil && existing.UUID != nil {
		return *existing.UUID, nil
	}
	if getErr != nil && !utilErrors.IsNotFoundErr(getErr) {
		return "", getErr
	}
	resp, err := c.provider.CreateSVM(vsa.CreateSvmParams{
		Name:      in.SvmName,
		Protocols: vsa.Protocols{EnableNfs: in.EnableNfs},
		IPSpace:   in.IPSpace,
	})
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", errors.New("empty response creating SVM")
	}
	return resp.ExternalUUID, nil
}

func (c *poClusterClient) EnsureMgmtPolicies(svmUUID string) error {
	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return err
	}
	allowed := true
	files := ontapmodels.NsswitchSource("files")
	ldapSrc := ontapmodels.NsswitchSource("ldap")
	_, _, err = restClient.SVM().SvmModify(&ontapRest.SvmModifyParams{
		SvmUUID:     svmUUID,
		CifsAllowed: &allowed,
		NsSwitch: &ontapRest.NsSwitchSource{
			NsSwitchSourcePasswd: []*ontapmodels.NsswitchSource{&files, &ldapSrc},
			NsSwitchSourceGroup:  []*ontapmodels.NsswitchSource{&files, &ldapSrc},
		},
	})
	return err
}

func (c *poClusterClient) DeleteSvmByName(svmName string) error {
	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return err
	}
	existing, getErr := restClient.SVM().SvmGet(&ontapRest.SvmGetParams{SvmName: svmName})
	if getErr != nil {
		if utilErrors.IsNotFoundErr(getErr) {
			return nil
		}
		return getErr
	}
	if existing == nil || existing.UUID == nil {
		return nil
	}
	done, job, err := restClient.SVM().SvmDelete(*existing.UUID)
	if err != nil {
		return err
	}
	if !done && job != nil && job.JobUUID != "" {
		return restClient.Poll(job.JobUUID)
	}
	return nil
}

func (c *poClusterClient) EnsureDataLif(in PODataLifSpec) error {
	homeNode := in.HomeNode
	if homeNode == "" {
		nodes, err := c.provider.GetNodes()
		if err != nil {
			return err
		}
		if len(nodes) == 0 {
			return errors.New("no nodes available on cluster to home the data LIF")
		}
		homeNode = nodes[0].Name
	}

	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return err
	}
	exists, err := c.dataLifExists(restClient, in.Name, in.SvmName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	_, err = c.provider.CreateDataLIF(vsa.CreateLifParams{
		Name:          in.Name,
		SvmName:       in.SvmName,
		IpAddress:     in.IPAddress,
		NodeName:      homeNode,
		HomePort:      in.HomePort,
		Netmask:       in.Netmask,
		ServicePolicy: in.ServicePolicy,
	})
	return err
}

func (c *poClusterClient) dataLifExists(restClient ontapRest.RESTClient, name, svmName string) (bool, error) {
	lifName := name
	lifSvm := svmName
	var found bool
	err := restClient.Networking().NetworkIPInterfacesGet(&ontapRest.NetworkIPInterfacesGetParams{
		Name:    &lifName,
		SvmName: &lifSvm,
	}, func(ifs []*ontapRest.IPInterface) error {
		if len(ifs) > 0 {
			found = true
		}
		return nil
	})
	return found, err
}

func (c *poClusterClient) ApplyQoSPolicy(in ApplyQoSClientParams) error {
	findParams := vsa.FindQoSGroupPolicyParams{Name: in.PolicyName, SvmName: in.SvmName}
	if _, err := c.provider.FindQoSGroupPolicy(findParams); err != nil {
		isShared := true
		if _, createErr := c.provider.CreateQoSGroupPolicy(vsa.CreateQoSGroupPolicyParams{
			Name:          in.PolicyName,
			SvmName:       in.SvmName,
			MaxThroughput: in.MaxThroughput,
			MaxIOPS:       in.MaxIOPS,
			IsShared:      &isShared,
		}); createErr != nil {
			return createErr
		}
	}
	return c.provider.ModifySVMWithQoSPolicy(vsa.ModifySVMWithQoSPolicyParams{
		SvmUUID:       in.SvmUUID,
		QoSPolicyName: in.PolicyName,
	})
}

func (c *poClusterClient) ConfigureKmsOnSvm(in ConfigureKmsOnSvmInput) error {
	_, err := c.provider.CreateKmsConfig(vsa.CreateKmsConfigParams{
		KeyName:           in.KeyName,
		KeyRingLocation:   in.KeyRingLocation,
		KeyRingName:       in.KeyRingName,
		ProjectID:         in.ProjectID,
		SvmName:           in.SvmName,
		PrivilegedAccount: in.PrivilegedAccount,
	})
	return err
}

// HasSVMsInIPSpace reports whether ONTAP still has any SVM in the named ipspace.
// Used as the ONTAP gate after the DB IsLastActiveSVMOnIPSpace check before teardown.
func (c *poClusterClient) HasSVMsInIPSpace(ipSpace string) (bool, error) {
	ipSpace = strings.TrimSpace(ipSpace)
	if ipSpace == "" {
		return false, errors.New("ipspace must not be empty")
	}
	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return false, err
	}
	svms, err := restClient.SVM().SvmCollectionGet(&ontapRest.SvmGetCollectionParams{
		BaseParams:  ontapRest.BaseParams{Fields: []string{"name", "uuid"}},
		IpspaceName: &ipSpace,
	})
	if err != nil {
		return false, err
	}
	return len(svms) > 0, nil
}

// CleanupPOAccountClusterNetwork removes the PO VLAN ports, broadcast domain,
// and ipspace for the given account and ipspace. Idempotent: not-found deletes succeed.
func (c *poClusterClient) CleanupPOAccountClusterNetwork(accountUUID, ipSpace string) error {
	accountUUID = strings.TrimSpace(accountUUID)
	ipSpace = strings.TrimSpace(ipSpace)
	if accountUUID == "" {
		return errors.New("account uuid must not be empty")
	}
	if ipSpace == "" {
		return errors.New("ipspace must not be empty")
	}
	if err := commonparams.ValidatePOIPSpaceForAccount(accountUUID, ipSpace); err != nil {
		return err
	}

	bdName, err := commonparams.POBroadcastDomainFromIPSpace(ipSpace)
	if err != nil {
		return err
	}

	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return err
	}
	net := restClient.Networking()

	ports, err := net.NetworkEthernetPortsGet(&ontapRest.NetworkEthernetPortsGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"uuid", "type"}},
		IPSpace:    ipSpace,
		Type:       ontapRestPortTypeVlan,
	})
	if err != nil {
		return err
	}
	for _, port := range ports {
		if port == nil || port.UUID == nil || strings.TrimSpace(*port.UUID) == "" {
			continue
		}
		if delErr := deleteEthernetPort(restClient, *port.UUID); delErr != nil {
			return delErr
		}
	}

	if delErr := deleteBroadcastDomain(net, bdName); delErr != nil {
		return delErr
	}
	return deleteIPSpace(net, ipSpace)
}

func deleteEthernetPort(restClient ontapRest.RESTClient, portUUID string) error {
	jobUUID, err := restClient.Networking().NetworkEthernetPortDelete(&ontapRest.NetworkEthernetPortDeleteParams{UUID: portUUID})
	if err != nil {
		if utilErrors.IsNotFoundErr(err) {
			return nil
		}
		return err
	}
	if jobUUID != "" {
		return restClient.Poll(jobUUID)
	}
	return nil
}

func deleteBroadcastDomain(net ontapRest.NetworkingClient, name string) error {
	err := net.NetworkEthernetBroadcastDomainDelete(&ontapRest.NetworkEthernetBroadcastDomainDeleteParams{Name: name})
	if err != nil && !utilErrors.IsNotFoundErr(err) {
		return err
	}
	return nil
}

func deleteIPSpace(net ontapRest.NetworkingClient, name string) error {
	err := net.IpspaceDelete(&ontapRest.IpspaceDeleteParams{Name: name})
	if err != nil && !utilErrors.IsNotFoundErr(err) {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Intercluster LIF client methods
// ---------------------------------------------------------------------------

func (c *poClusterClient) GetInterclusterLifs(servicePolicy string) ([]*vsa.InterclusterLif, error) {
	return c.provider.GetInterclusterLIFs(servicePolicy)
}

func (c *poClusterClient) CreateInterclusterLif(in POInterclusterLifSpec) (*POInterclusterLifResult, error) {
	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return nil, err
	}
	params := &ontapRest.NetworkIPInterfacesCreateParams{
		Name:          in.Name,
		IPAddress:     in.IPAddress,
		Netmask:       in.Netmask,
		HomeNode:      in.HomeNode,
		HomePort:      in.HomePort,
		ServicePolicy: in.ServicePolicy,
		Scope:         ontapmodels.IPInterfaceScopeCluster,
		IPSpace:       in.IPSpace,
	}
	iface, err := restClient.Networking().NetworkIPInterfaceCreate(params)
	if err != nil {
		return nil, err
	}
	if iface == nil || iface.UUID == nil || iface.Name == nil {
		return nil, fmt.Errorf("ONTAP returned invalid intercluster LIF response")
	}
	return &POInterclusterLifResult{
		Name:         *iface.Name,
		ExternalUUID: *iface.UUID,
	}, nil
}

func (c *poClusterClient) DeleteInterclusterLif(externalUUID string) error {
	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return err
	}
	err = restClient.Networking().NetworkIPInterfaceDelete(externalUUID)
	if err != nil && !utilErrors.IsNotFoundErr(err) {
		return err
	}
	return nil
}

// DeleteIPSpaceDefaultRoute is a best-effort no-op today: the ONTAP REST API
// does not expose a single-route delete by (ipspace, gateway). IC LIF ensure
// still calls this from rollback for future compatibility, but the route
// will remain until the IPspace itself is deleted (which cascades). Retries
// stay safe because CreateDefaultRoute tolerates the "already exists" case.
func (c *poClusterClient) DeleteIPSpaceDefaultRoute(_, _ string) error {
	return nil
}

// DeleteVlanNetPorts removes every VLAN-typed ethernet port in the given
// ONTAP ipspace. The broadcast-domain, base-port, vlan-tag and node-name
// arguments are accepted for interface parity with EnsureVlanNetPorts but are
// not needed for the delete path: the IC LIF ipspace is dedicated per
// (account, IC VLAN) so scoping by ipspace alone is sufficient and covers
// any per-node VLAN ports the ensure step created.
func (c *poClusterClient) DeleteVlanNetPorts(ipSpace, _ string, _ string, _ int, _ []string) error {
	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return err
	}
	net := restClient.Networking()
	ports, err := net.NetworkEthernetPortsGet(&ontapRest.NetworkEthernetPortsGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"uuid", "type"}},
		IPSpace:    ipSpace,
		Type:       ontapRestPortTypeVlan,
	})
	if err != nil {
		return err
	}
	for _, port := range ports {
		if port == nil || port.UUID == nil || strings.TrimSpace(*port.UUID) == "" {
			continue
		}
		if delErr := deleteEthernetPort(restClient, *port.UUID); delErr != nil {
			return delErr
		}
	}
	return nil
}

func (c *poClusterClient) DeleteBroadcastDomain(ipSpace, name string) error {
	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return err
	}
	return deleteBroadcastDomain(restClient.Networking(), name)
}

func (c *poClusterClient) DeleteIPSpace(name string) error {
	restClient, err := c.provider.CreateRESTClient()
	if err != nil {
		return err
	}
	return deleteIPSpace(restClient.Networking(), name)
}

// ---------------------------------------------------------------------------
// Intercluster LIF Temporal Activities
// ---------------------------------------------------------------------------

// CreatePOInterclusterLifInput is the input to CreatePOInterclusterLif.
type CreatePOInterclusterLifInput struct {
	HostUUID string
	Lif      POInterclusterLifSpec
}

// CreatePOInterclusterLif creates a single intercluster LIF on ONTAP and returns
// its name and external UUID.
func (a *POOntapActivity) CreatePOInterclusterLif(ctx context.Context, in CreatePOInterclusterLifInput) (*POInterclusterLifResult, error) {
	if in.Lif.Name == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("intercluster LIF name must not be empty")))
	}
	if err := validatePOIPSpace(in.Lif.IPSpace); err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Creating intercluster LIF %s on ONTAP host %s", in.Lif.Name, in.HostUUID))
	client, err := a.client(ctx, in.HostUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	result, err := client.CreateInterclusterLif(in.Lif)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return result, nil
}

// RollbackPOInterclusterLifsInput is the input to RollbackPOInterclusterLifs.
type RollbackPOInterclusterLifsInput struct {
	HostUUID        string
	AccountID       int64
	HostID          int64
	IPSpace         string
	BroadcastDomain string
	BasePort        string
	VlanID          int
	Gateway         string
	NodeNames       []string
	CreatedLifs     []POInterclusterLifResult
}

// RollbackPOInterclusterLifs performs best-effort cleanup of IC LIF ONTAP and
// VCP resources in reverse dependency order. It never calls CVN delete APIs.
// Every step is attempted even if a prior step fails; errors are joined.
func (a *POOntapActivity) RollbackPOInterclusterLifs(ctx context.Context, in RollbackPOInterclusterLifsInput) error {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Rolling back IC LIFs for account %d on host %s", in.AccountID, in.HostUUID))

	var errs []error

	client, clientErr := a.client(ctx, in.HostUUID)
	if clientErr != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, clientErr))
	}

	// 1. Delete route. DeleteIPSpaceDefaultRoute is a no-op today (documented
	// at poClusterClient.DeleteIPSpaceDefaultRoute), so this stays safe to
	// invoke whenever the scaffolding fields are populated.
	if in.Gateway != "" && in.IPSpace != "" {
		if err := client.DeleteIPSpaceDefaultRoute(in.IPSpace, in.Gateway); err != nil {
			errs = append(errs, fmt.Errorf("delete route: %w", err))
		}
	}

	// 2. Delete ONTAP intercluster LIFs.
	for _, lif := range in.CreatedLifs {
		if lif.ExternalUUID == "" {
			continue
		}
		if err := client.DeleteInterclusterLif(lif.ExternalUUID); err != nil {
			errs = append(errs, fmt.Errorf("delete ONTAP LIF %s: %w", lif.ExternalUUID, err))
		}
	}

	// 3. Delete VCP intercluster LIF rows.
	if a.SE != nil && in.AccountID > 0 && in.HostID > 0 {
		if err := a.SE.DeleteInterclusterLifsForAccountHost(ctx, in.AccountID, in.HostID); err != nil {
			errs = append(errs, fmt.Errorf("delete VCP IC LIF rows: %w", err))
		}
	}

	// 4. Delete VLAN ports.
	if in.IPSpace != "" && in.VlanID != 0 && len(in.NodeNames) > 0 {
		if err := client.DeleteVlanNetPorts(in.IPSpace, in.BroadcastDomain, in.BasePort, in.VlanID, in.NodeNames); err != nil {
			errs = append(errs, fmt.Errorf("delete VLAN ports: %w", err))
		}
	}

	// 5. Delete broadcast domain.
	if in.BroadcastDomain != "" && in.IPSpace != "" {
		if err := client.DeleteBroadcastDomain(in.IPSpace, in.BroadcastDomain); err != nil {
			errs = append(errs, fmt.Errorf("delete broadcast domain: %w", err))
		}
	}

	// 6. Delete IPspace.
	if in.IPSpace != "" {
		if err := client.DeleteIPSpace(in.IPSpace); err != nil {
			errs = append(errs, fmt.Errorf("delete ipspace: %w", err))
		}
	}

	if len(errs) > 0 {
		joined := errors.Join(errs...)
		return vsaerrors.WrapAsTemporalApplicationError(
			fmt.Errorf("IC LIF rollback completed with errors: %w", joined))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Intercluster LIF DB Activities
// ---------------------------------------------------------------------------

// POICLifActivity manages PO intercluster LIF database operations.
//
// The optional ClientFactory is used by POICLifPrecheck to enumerate ONTAP
// cluster nodes. When nil, a default cluster client is built from the host
// row's admin credentials (mirroring POOntapActivity's factory pattern).
type POICLifActivity struct {
	SE            database.Storage
	ClientFactory POClusterClientFactory
}

// clusterClient returns a POClusterClient for the given host, using the
// injected factory when provided and otherwise building a default client
// from the host's admin credentials. Mirrors POOntapActivity.client so
// POICLifPrecheck can enumerate ONTAP nodes without depending on the
// sibling activity struct.
func (a *POICLifActivity) clusterClient(ctx context.Context, hostUUID string) (POClusterClient, error) {
	if err := validatePOHostUUID(hostUUID); err != nil {
		return nil, err
	}
	if a.ClientFactory != nil {
		return a.ClientFactory(ctx, hostUUID)
	}
	if a.SE == nil {
		return nil, errors.New("storage is not configured for PO IC LIF cluster client")
	}
	host, err := a.SE.GetHost(ctx, strings.TrimSpace(hostUUID))
	if err != nil {
		return nil, fmt.Errorf("load PO host %q: %w", hostUUID, err)
	}
	provider, err := poGetProviderForHost(ctx, host)
	if err != nil {
		return nil, err
	}
	return &poClusterClient{provider: provider}, nil
}

// POICLifPrecheckInput is the input to POICLifPrecheck.
type POICLifPrecheckInput struct {
	AccountID int64
	HostUUID  string
}

// POICLifPrecheckOutput is the result of POICLifPrecheck. Complete=true
// means the persisted IC LIF set already covers every current node, so
// executePOICLifEnsureChild can short-circuit the child workflow.
type POICLifPrecheckOutput struct {
	Complete bool
}

// POICLifPrecheck consolidates the reads the IC LIF ensure workflow does
// at its top (host id + cluster node listing + node id resolution +
// existing IC LIF read) into a single parent-scope activity, then reports
// whether the persisted IC LIF set already covers every current node
// exactly once. Callers invoke this before ExecuteChildWorkflow so a
// repeat pool create on an already-complete (account, host) never spawns
// the child (and therefore never collides on the derived child
// WorkflowID). The child workflow retains its own internal completeness
// check as defense-in-depth against a precheck→child-start race.
func (a *POICLifActivity) POICLifPrecheck(ctx context.Context, in POICLifPrecheckInput) (*POICLifPrecheckOutput, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Prechecking IC LIF completeness for account %d host %s", in.AccountID, in.HostUUID))
	if in.AccountID <= 0 || strings.TrimSpace(in.HostUUID) == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				errors.New("POICLifPrecheck requires accountID > 0 and hostUUID")))
	}
	if a.SE == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError,
				errors.New("POICLifActivity storage is not configured")))
	}

	host, err := a.SE.GetHost(ctx, in.HostUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	hostID := host.ID

	client, err := a.clusterClient(ctx, in.HostUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	nodeNames, err := client.GetNodeNames()
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if len(nodeNames) == 0 {
		return nil, vsaerrors.WrapAsTemporalApplicationError(errors.New("cluster returned no nodes"))
	}

	nodeIDs, err := a.SE.GetNodesByHostIDAndNames(ctx, hostID, nodeNames)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	existing, err := a.SE.GetInterclusterLifsForAccountHost(ctx, in.AccountID, hostID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return &POICLifPrecheckOutput{
		Complete: commonparams.IsICLifSetComplete(existing, nodeIDs),
	}, nil
}

// AllocatePOInterclusterLifIPsInput allocates IC LIF IP reservations.
type AllocatePOInterclusterLifIPsInput struct {
	IPRangeUUID  string
	AccountID    int64
	Reservations []POICLifReservation
}

// POICLifReservation is a single IC LIF reservation request.
type POICLifReservation struct {
	Name   string
	NodeID int64
}

// validatePOICLifReservations enforces the caller contract for IC LIF
// reservations: at least one reservation, non-empty names, positive account
// and node ids. Kept at the activity layer so the DB layer stays free of
// input validation.
func validatePOICLifReservations(accountID int64, reservations []POICLifReservation) error {
	if len(reservations) == 0 {
		return fmt.Errorf("at least one reservation is required")
	}
	if accountID <= 0 {
		return fmt.Errorf("accountID must be positive, got %d", accountID)
	}
	for i, r := range reservations {
		if strings.TrimSpace(r.Name) == "" {
			return fmt.Errorf("reservation[%d] name must not be empty", i)
		}
		if r.NodeID <= 0 {
			return fmt.Errorf("reservation[%d] node id must be positive", i)
		}
	}
	return nil
}

// AllocatePOInterclusterLifIPs reserves IC LIF IPs in the lifs table.
func (a *POICLifActivity) AllocatePOInterclusterLifIPs(ctx context.Context, in AllocatePOInterclusterLifIPsInput) ([]*POICLifAllocated, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Allocating %d IC LIF IPs from range %s", len(in.Reservations), in.IPRangeUUID))
	if a.SE == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("POICLifActivity storage is not configured")))
	}
	if err := validatePOICLifReservations(in.AccountID, in.Reservations); err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}

	reservations := make([]*datamodel.Lif, 0, len(in.Reservations))
	for _, r := range in.Reservations {
		reservations = append(reservations, &datamodel.Lif{
			Name:      r.Name,
			AccountID: in.AccountID,
			NodeID:    r.NodeID,
		})
	}

	created, err := a.SE.AllocateInterclusterLifIPs(ctx, in.IPRangeUUID, reservations)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	result := make([]*POICLifAllocated, 0, len(created))
	for _, lif := range created {
		if lif == nil {
			continue
		}
		result = append(result, &POICLifAllocated{
			UUID:       lif.UUID,
			Name:       lif.Name,
			IPAddress:  lif.IPAddress,
			SubnetMask: lif.SubnetMask,
			NodeID:     lif.NodeID,
		})
	}
	return result, nil
}

// POICLifAllocated is the result of a single IC LIF IP allocation.
type POICLifAllocated struct {
	UUID       string
	Name       string
	IPAddress  string
	SubnetMask string
	NodeID     int64
}

// UpdatePOInterclusterLifExternalUUIDInput updates the ONTAP UUID on a VCP IC LIF row.
type UpdatePOInterclusterLifExternalUUIDInput struct {
	LifUUID      string
	ExternalUUID string
}

// UpdatePOInterclusterLifExternalUUID persists the ONTAP LIF UUID.
func (a *POICLifActivity) UpdatePOInterclusterLifExternalUUID(ctx context.Context, in UpdatePOInterclusterLifExternalUUIDInput) error {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Updating IC LIF %s external UUID to %s", in.LifUUID, in.ExternalUUID))
	if a.SE == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("POICLifActivity storage is not configured")))
	}
	if err := a.SE.UpdateInterclusterLifExternalUUID(ctx, in.LifUUID, in.ExternalUUID); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// GetPOInterclusterLifsForAccountHostInput is the input to GetPOInterclusterLifsForAccountHost.
type GetPOInterclusterLifsForAccountHostInput struct {
	AccountID int64
	HostID    int64
}

// GetPOInterclusterLifsForAccountHost loads active IC LIF rows for an account+host.
func (a *POICLifActivity) GetPOInterclusterLifsForAccountHost(ctx context.Context, in GetPOInterclusterLifsForAccountHostInput) ([]*datamodel.Lif, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Loading IC LIFs for account %d host %d", in.AccountID, in.HostID))
	if a.SE == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("POICLifActivity storage is not configured")))
	}
	lifs, err := a.SE.GetInterclusterLifsForAccountHost(ctx, in.AccountID, in.HostID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return lifs, nil
}

// ResolveHostID maps a host UUID to its database ID (hosts.id).
func (a *POICLifActivity) ResolveHostID(ctx context.Context, hostUUID string) (int64, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Resolving host ID for %s", hostUUID))
	if a.SE == nil {
		return 0, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("POICLifActivity storage is not configured")))
	}
	host, err := a.SE.GetHost(ctx, hostUUID)
	if err != nil {
		return 0, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return host.ID, nil
}
