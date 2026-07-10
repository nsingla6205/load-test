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

func (c *poClusterClient) CreateDefaultRoute(svmName, gateway, ipSpace string) error {
	if err := validatePOIPSpace(ipSpace); err != nil {
		return err
	}
	return c.provider.CreateNetworkIpRoute(vsa.CreateNetworkIPRouteParams{
		SvmName: svmName,
		Gateway: gateway,
		IPSpace: ipSpace,
	})
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
	if err != nil && isAlreadyExistsErr(err) {
		return nil
	}
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
