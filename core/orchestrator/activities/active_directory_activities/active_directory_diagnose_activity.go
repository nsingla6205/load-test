// Package active_directory_activities contains Temporal activities backing the
// Active Directory lifecycle workflows. This file holds the activities used by
// DiagnoseActiveDirectoryWorkflow, which runs a connectivity check on a
// short-lived diagnosis SVM. SVM create/delete is delegated to the VLM child
// workflows; the activities here own AD-specific behavior.
package active_directory_activities

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

// cifsServerDeleter is the narrow provider surface DeleteDiagnosisCifsServer
// needs, defined locally so tests can stub it.
type cifsServerDeleter interface {
	DeleteCIFSServer(externalSVMUUID, adUsername, adPassword string) error
}

// kerberosDiagnosisCleaner is the narrow provider surface
// DeleteDiagnosisKerberosConfig needs, defined locally so tests can stub it.
type kerberosDiagnosisCleaner interface {
	GetKerberosInterfacesForSVM(svmUUID, svmName string) ([]*ontapRest.KerberosInterface, error)
	DisableKerberosOnInterface(params vsa.KerberosInterfaceModifyParams) error
}

// diagnosisSVMNamePrefix is the stable token used in every diagnosis SVM name
// so operators (and a future orphan reaper) can identify them at a glance.
const diagnosisSVMNamePrefix = "diag-"

// maxDiagnosisSVMNameLen mirrors the ONTAP SVM name cap enforced by
// VLM CreateVSASVMWorkflow.
const maxDiagnosisSVMNameLen = 40

// diagnosisAggrNamesForReducedLif returns exactly one aggregate from the pool's
// VLMConfig.DataAggr to scope the reduced-LIF SVM create. VLM requires
// aggr_names whenever NasLif is set, and placement is limited to that
// aggregate's home node.
func diagnosisAggrNamesForReducedLif(vlmConfig *vlm.VLMConfig) ([]string, error) {
	if vlmConfig == nil {
		return nil, errors.New("vlmConfig must not be nil")
	}
	for _, aggr := range vlmConfig.DataAggr {
		if name := strings.TrimSpace(aggr.Name); name != "" {
			return []string{name}, nil
		}
	}
	return nil, errors.New("vlmConfig.DataAggr has no named aggregates; required when NasLif is set for diagnosis SVM create")
}

// ActiveDirectoryDiagnoseActivity groups the Temporal activities used by
// DiagnoseActiveDirectoryWorkflow. Other AD activities (CreateOrModifyADDNS,
// GetOrCreateCifsService, Kerberos helpers) are reused from
// ActiveDirectoryActivity.
type ActiveDirectoryDiagnoseActivity struct {
	SE database.Storage
}

// BuildDiagnosisSvmName returns a deterministic SVM name for the diagnosis run
// on the given pool, so replays and the orphan reaper resolve the same SVM.
// Format: "diag-<first-8-chars-of-uuid>", capped at 40 chars.
func BuildDiagnosisSvmName(poolUUID string) string {
	cleaned := strings.ReplaceAll(poolUUID, "-", "")
	if len(cleaned) > 8 {
		cleaned = cleaned[:8]
	}
	name := diagnosisSVMNamePrefix + strings.ToLower(cleaned)
	if len(name) > maxDiagnosisSVMNameLen {
		name = name[:maxDiagnosisSVMNameLen]
	}
	return name
}

// LoadPoolForDiagnosis loads the VCP pool record by UUID. Returns a not-found
// error when the pool is absent so the workflow can surface a 404 to the
// caller before doing any further work.
func (a ActiveDirectoryDiagnoseActivity) LoadPoolForDiagnosis(ctx context.Context, poolUUID string) (*datamodel.Pool, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting LoadPoolForDiagnosis activity")

	if poolUUID == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("poolUUID must not be empty")))
	}

	pool, err := a.SE.GetPoolByUUID(ctx, poolUUID)
	if err != nil {
		if utilerrors.IsNotFoundErr(err) {
			logger.Errorf("Pool %s not found for AD diagnosis", poolUUID)
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
		}
		logger.Errorf("Failed to load pool %s for AD diagnosis: %v", poolUUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if pool == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			utilerrors.NewNotFoundErr("Pool", &poolUUID))
	}

	activity.RecordHeartbeat(ctx, "Finished LoadPoolForDiagnosis activity")
	return pool, nil
}

// GetActiveDirectoryRecordForPool returns the raw datamodel.ActiveDirectory row
// attached to the pool. Unlike ActiveDirectoryActivity.GetActiveDirectoryForPool
// (which decrypts credentials and returns a vsa.ActiveDirectory), this returns
// the DB record because the workflow needs the UUID / State / StateDetails to
// drive rollback (ErroredAD / RestoreADStatus).
func (a ActiveDirectoryDiagnoseActivity) GetActiveDirectoryRecordForPool(ctx context.Context, poolID int64) (*datamodel.ActiveDirectory, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting GetActiveDirectoryRecordForPool activity")

	ad, err := a.SE.GetActiveDirectoryForPoolByPoolID(ctx, poolID)
	if err != nil {
		logger.Errorf("Failed to fetch Active Directory for pool ID %d: %v", poolID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if ad == nil {
		logger.Errorf("No Active Directory attached to pool ID %d", poolID)
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("no Active Directory attached to pool %d", poolID)))
	}

	activity.RecordHeartbeat(ctx, "Finished GetActiveDirectoryRecordForPool activity")
	return ad, nil
}

// GetNodeForDiagnosis validates that the pool has at least one node and returns
// a *models.Node containing all endpoint addresses. It acts as a pre-flight
// check before the expensive VLM SVM create; the result is NOT used directly
// for ONTAP REST calls. GetIlbNasLifHomeNodeForDiagnosis (called after the SVM
// is created) pins the endpoint to the single node that hosts the ILB NAS LIF.
func (a ActiveDirectoryDiagnoseActivity) GetNodeForDiagnosis(ctx context.Context, pool *datamodel.Pool) (*models.Node, error) {
	activity.RecordHeartbeat(ctx, "Starting GetNodeForDiagnosis activity")

	if pool == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("pool must not be nil")))
	}

	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if len(dbNodes) == 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrUnexpectedNodeCountForPool,
				fmt.Errorf("no nodes found for pool %s", pool.UUID)))
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})

	activity.RecordHeartbeat(ctx, "Finished GetNodeForDiagnosis activity")
	return node, nil
}

// GetIlbNasLifHomeNodeForDiagnosis returns a *models.Node pinned to the ONTAP
// node that hosts the diagnosis SVM's ILB NAS LIF. CIFS join and Kerberos
// enrollment must target that node because ONTAP routes those operations
// through the LIF's home node. The home node name is read from the VLM response;
// if it is absent or does not match a DB node, it falls back to the all-node
// endpoint.
func (a ActiveDirectoryDiagnoseActivity) GetIlbNasLifHomeNodeForDiagnosis(
	ctx context.Context,
	pool *datamodel.Pool,
	svmName string,
	vlmConfig *vlm.VLMConfig,
	fallback *models.Node,
) (*models.Node, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting GetIlbNasLifHomeNodeForDiagnosis activity")

	homeNodeName := ilbNasLifHomeNodeName(svmName, vlmConfig)
	if homeNodeName == "" {
		logger.Warnf("ILB NAS LIF home node not found in VLM response for SVM %s; falling back to all-node endpoint", svmName)
		return fallback, nil
	}

	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	for _, n := range dbNodes {
		if n.Name == homeNodeName {
			node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
				Nodes:            []*datamodel.Node{n},
				DeploymentName:   pool.DeploymentName,
				OntapCredentials: pool.PoolCredentials,
			})
			logger.Infof("ILB NAS LIF for SVM %s is homed on node %s; pinning ONTAP REST endpoint", svmName, homeNodeName)
			activity.RecordHeartbeat(ctx, "Finished GetIlbNasLifHomeNodeForDiagnosis activity")
			return node, nil
		}
	}

	logger.Warnf("ILB NAS LIF home node %q not found among pool DB nodes for SVM %s; falling back to all-node endpoint", homeNodeName, svmName)
	return fallback, nil
}

// ilbNasLifHomeNodeName extracts the home_node name of the first ILB NAS LIF
// from the VLM config returned by CreateVSASVM. Returns "" if absent.
func ilbNasLifHomeNodeName(svmName string, vlmConfig *vlm.VLMConfig) string {
	if vlmConfig == nil {
		return ""
	}
	svmEntry, ok := vlmConfig.Svm[svmName]
	if !ok {
		return ""
	}
	lifs := svmEntry.SVMLIFs[vlm.LIFTypeIlbNas]
	if len(lifs) == 0 {
		return ""
	}
	return lifs[0].HomeNode
}

// ilbNasServicePolicy is the ONTAP service policy required on the ILB NAS LIF
// so it can handle NFS/SMB data traffic and participate in AD domain operations.
const ilbNasServicePolicy = "default-data-files"

// SetIlbNasLifServicePolicy sets the service policy of the ILB NAS LIF on the
// diagnosis SVM to "default-data-files". VLM provisions the LIF with a
// restricted policy; without this modify the LIF cannot carry DNS/AD traffic.
// The LIF UUID is read from the VLM config — no extra ONTAP round-trip needed.
func (a ActiveDirectoryDiagnoseActivity) SetIlbNasLifServicePolicy(
	ctx context.Context,
	node *models.Node,
	svmName string,
	vlmConfig *vlm.VLMConfig,
) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting SetIlbNasLifServicePolicy activity")

	if vlmConfig == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("vlmConfig must not be nil")))
	}

	svmEntry, ok := vlmConfig.Svm[svmName]
	if !ok {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState,
				fmt.Errorf("SVM %q not found in VLM config", svmName)))
	}

	ilbNasLifs := svmEntry.SVMLIFs[vlm.LIFTypeIlbNas]
	if len(ilbNasLifs) == 0 {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState,
				fmt.Errorf("no ILB NAS LIF found in VLM config for SVM %q", svmName)))
	}

	lifUUID := ilbNasLifs[0].Uuid
	if lifUUID == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("ILB NAS LIF for SVM %q has empty UUID in VLM config", svmName)))
	}

	ontapProvider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	provider, ok := ontapProvider.(*vsa.OntapRestProvider)
	if !ok {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			fmt.Errorf("provider is not OntapRestProvider"))
	}

	if err := provider.ModifyNetworkInterfaceServicePolicy(lifUUID, ilbNasServicePolicy); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Set service policy %q on ILB NAS LIF %s (UUID %s) of diagnosis SVM %s",
		ilbNasServicePolicy, ilbNasLifs[0].Name, lifUUID, svmName)
	activity.RecordHeartbeat(ctx, "Finished SetIlbNasLifServicePolicy activity")
	return nil
}

// PrepareCreateDiagnosisSVMRequest builds the vlm.CreateSVMRequest used to
// provision the diagnosis SVM, using the pool's VLMConfig and credentials so
// the SVM lands on the same cluster.
//
// EnableIlbSupport is forced to true so VLM provisions the ILB NAS data LIF
// that CIFS/NFS and the Kerberos interface-enable step require; the pool's
// stored config may have it disabled. Reduced-LIF mode (NasLif=true,
// SanLif=false) is scoped by AggrNames, which must be real data-aggregate names.
func (a ActiveDirectoryDiagnoseActivity) PrepareCreateDiagnosisSVMRequest(
	ctx context.Context,
	pool *datamodel.Pool,
	ad *datamodel.ActiveDirectory,
	vlmConfig *vlm.VLMConfig,
	credentials *vlm.OntapCredentials,
) (*vlm.CreateSVMRequest, error) {
	activity.RecordHeartbeat(ctx, "Starting PrepareCreateDiagnosisSVMRequest activity")

	if pool == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("pool must not be nil")))
	}
	if ad == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("active directory must not be nil")))
	}
	if vlmConfig == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("vlmConfig must not be nil")))
	}
	if credentials == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("ontap credentials must not be nil")))
	}
	if ad.DNS == "" || ad.Domain == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				errors.New("active directory must have non-empty DNS and Domain to provision a diagnosis SVM")))
	}

	aggrNames, err := diagnosisAggrNamesForReducedLif(vlmConfig)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}

	// Force the ILB NAS LIF so the diagnosis SVM is never SAN-only; CIFS/NFS and
	// the Kerberos interface-enable step require a NAS data LIF.
	vlmConfig.Deployment.DevFlags.EnableIlbSupport = true

	req := &vlm.CreateSVMRequest{
		Name:             BuildDiagnosisSvmName(pool.UUID),
		VLMConfig:        *vlmConfig,
		DnsDomains:       ad.Domain,
		NameServers:      ad.DNS,
		OntapCredentials: *credentials,
		AggrNames:        aggrNames,
		NasLif:           true,
	}

	activity.RecordHeartbeat(ctx, "Finished PrepareCreateDiagnosisSVMRequest activity")
	return req, nil
}

// SaveDiagnosisSVMToDB persists the diagnosis SVM row and its NAS LIF row(s)
// after VLM CreateVSASVM returns the populated VLMConfig. Both are marked with
// IsDirectoryDiagnosis=true (SvmDetails / Lif column) so the orphan reaper can
// reclaim leaked diagnosis resources and mount/validation flows can skip them.
// Persisting after VLM create means the DB stores the VLM-returned identifiers
// and no orphan row is left if VLM fails first. The SVM row and LIF rows are
// both created idempotently (conflicts are treated as success) so the activity
// is safe to retry, and SvmDetails is rewritten from the VLM response on every
// run so a row that outlived a previous diagnosis can never keep pointing at
// the SVM that run deleted.
func (a ActiveDirectoryDiagnoseActivity) SaveDiagnosisSVMToDB(
	ctx context.Context,
	pool *datamodel.Pool,
	ad *datamodel.ActiveDirectory,
	svmName string,
	vlmConfig *vlm.VLMConfig,
) (*datamodel.Svm, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting SaveDiagnosisSVMToDB activity")

	if pool == nil || ad == nil || vlmConfig == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("pool, ad and vlmConfig must not be nil")))
	}
	if svmName == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("svmName must not be empty")))
	}

	svmEntry, ok := vlmConfig.Svm[svmName]
	if !ok {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState,
				fmt.Errorf("diagnosis SVM %q not found in VLM response config", svmName)))
	}

	// The VLM-reported UUID is the only identity the ONTAP calls and the teardown
	// path can use, so a missing one is unusable — fail before writing a row that
	// nothing could act on.
	if svmEntry.Svmuuid == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("diagnosis SVM %q has an empty UUID in the VLM response config", svmName)))
	}

	details := &datamodel.SvmDetails{
		ExternalUUID:         svmEntry.Svmuuid,
		IPSpace:              vlmConfig.VsaCluster.CustIPSpace,
		IsDirectoryDiagnosis: true,
	}

	// State is deliberately left unset: CreateSVM normalizes every new row to
	// READY (and returns an already-READY row untouched on retry), so a
	// transitional state here would never reach the DB. SvmDetails.IsDirectoryDiagnosis
	// is the discriminator every diagnosis-aware caller uses.
	rec := &datamodel.Svm{
		Name:              svmEntry.Svmname,
		AccountID:         pool.AccountID,
		PoolID:            pool.ID,
		ActiveDirectoryID: sql.NullInt64{Int64: ad.ID, Valid: ad.ID != 0},
		SvmDetails:        details,
	}

	created, err := a.SE.CreateSVM(ctx, rec)
	if err != nil {
		if !utilerrors.IsConflictErr(err) {
			logger.Errorf("Failed to persist diagnosis SVM row %s: %v", svmName, err)
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		logger.Warnf("Diagnosis SVM row already exists for %s, fetching existing", svmName)
		created, err = a.SE.GetSvmByNameAndPoolID(ctx, svmName, pool.ID)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}
	if created == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState,
				fmt.Errorf("no diagnosis SVM row available for %q after create and lookup", svmName)))
	}

	// A row surviving a previous run (its soft-delete is best-effort) is returned
	// as-is by both the CreateSVM already-READY short-circuit and the name lookup,
	// still carrying that run's SVM UUID. Overwrite the blob unconditionally so
	// the row describes the SVM VLM just created, never a deleted one.
	created, err = a.SE.UpdateSvmDetails(ctx, created, details)
	if err != nil {
		logger.Errorf("Failed to persist VLM identifiers on diagnosis SVM row %s: %v", svmName, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if created == nil || created.SvmDetails == nil || created.SvmDetails.ExternalUUID == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState,
				fmt.Errorf("diagnosis SVM row %q has no external UUID after persisting VLM identifiers", svmName)))
	}

	if err := a.saveDiagnosisLIFs(ctx, pool, created, svmName, vlmConfig); err != nil {
		return nil, err
	}

	activity.RecordHeartbeat(ctx, "Finished SaveDiagnosisSVMToDB activity")
	return created, nil
}

// saveDiagnosisLIFs persists the diagnosis SVM's NAS LIF row(s) from the VLM
// response, linked to the diagnosis SVM via svm_id and flagged with
// Lif.IsDirectoryDiagnosis=true. Type=dataLIF allows SVM-scoped teardown to
// reclaim the row; the absent ip_range_id keeps it out of IP-range accounting.
// CreateLif conflicts are treated as success so the step is idempotent on retry.
func (a ActiveDirectoryDiagnoseActivity) saveDiagnosisLIFs(
	ctx context.Context,
	pool *datamodel.Pool,
	svm *datamodel.Svm,
	svmName string,
	vlmConfig *vlm.VLMConfig,
) error {
	logger := util.GetLogger(ctx)

	svmEntry, ok := vlmConfig.Svm[svmName]
	if !ok {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState,
				fmt.Errorf("diagnosis SVM %q not found in VLM response config", svmName)))
	}

	nodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	nodeIDByName := make(map[string]int64, len(nodes))
	for _, node := range nodes {
		if node != nil && node.Name != "" {
			nodeIDByName[node.Name] = node.ID
		}
	}

	svmID := sql.NullInt64{Int64: svm.ID, Valid: svm.ID != 0}
	protocolType := string(vlm.LIFTypeNas)

	// The diagnosis SVM is reduced-LIF NAS-only; persist the ILB NAS LIF (and
	// any plain NAS LIF the VLM response returned). No SAN LIFs are created.
	for _, lifType := range []vlm.VSALIFType{vlm.LIFTypeIlbNas, vlm.LIFTypeNas} {
		for _, lif := range svmEntry.SVMLIFs[lifType] {
			if lif.IP == "" {
				return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
						fmt.Errorf("diagnosis LIF %q has an empty IP address", lif.Name)))
			}
			nodeID, exists := nodeIDByName[lif.HomeNode]
			if !exists {
				return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState,
						fmt.Errorf("diagnosis LIF %q references non-existent home node %q", lif.Name, lif.HomeNode)))
			}

			lifRec := &datamodel.Lif{
				Name:       lif.Name,
				AccountID:  pool.AccountID,
				NodeID:     nodeID,
				SvmID:      svmID,
				IPAddress:  strings.Split(lif.IP, "/")[0],
				SubnetMask: vsa.DefaultNetmask,
				// Type=dataLIF lets the existing DeleteLifsBySvmID reclaim these
				// rows at teardown. It stays out of IP-range accounting because
				// no ip_range_id is set, and operational LIF queries exclude it
				// via the top-level IsDirectoryDiagnosis column.
				Type:                 database.LifTypeDataLIF,
				IsDirectoryDiagnosis: true,
				LifDetails: &datamodel.LifDetails{
					ExternalUUID: lif.Uuid,
					ProtocolType: protocolType,
				},
			}

			activity.RecordHeartbeat(ctx, "Creating diagnosis LIF record in database")
			if _, err := a.SE.CreateLif(ctx, lifRec); err != nil && !utilerrors.IsConflictErr(err) {
				logger.Errorf("Failed to persist diagnosis LIF %q for SVM %s: %v", lif.Name, svmName, err)
				return vsaerrors.WrapAsTemporalApplicationError(err)
			}
		}
	}

	return nil
}

// SoftDeleteDiagnosisSVMFromDB soft-deletes the diagnosis SVM's LIF rows and
// then the SVM row after VLM has reclaimed the on-cluster resources. Registered
// on cleanupManager so it runs on both success and failure paths. Best-effort
// and idempotent: already-deleted rows are a no-op and errors are logged and
// swallowed so the workflow stays unblocked (the orphan reaper reconciles any
// leftovers). The trailing string arg is the customer-facing errorMessage
// appended by RollbackManager.ExecuteRollback (always present, ignored here).
func (a ActiveDirectoryDiagnoseActivity) SoftDeleteDiagnosisSVMFromDB(ctx context.Context, svm *datamodel.Svm, _ string) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting SoftDeleteDiagnosisSVMFromDB activity")

	if svm == nil {
		logger.Info("Diagnosis SVM row is nil, nothing to soft-delete")
		return nil
	}

	// Soft-delete the diagnosis SVM's LIF rows first so no orphan LIF row is
	// left behind if the SVM row delete fails. The diagnosis SVM is dedicated,
	// so scoping by its svm_id only ever touches diagnosis rows.
	if svm.ID != 0 {
		if err := a.SE.DeleteLifsBySvmID(ctx, svm.ID); err != nil {
			logger.Warnf("Failed to soft-delete diagnosis LIF rows for SVM %s (continuing): %v", svm.Name, err)
		}
	}

	if err := a.SE.DeleteSVM(ctx, svm); err != nil {
		// Cleanup is best-effort; the orphan reaper will reconcile anything we
		// leave behind, and SaveDiagnosisSVMToDB rewrites SvmDetails so a row
		// that survives here cannot make the next run act on this deleted SVM.
		// Returning nil keeps the workflow from looping.
		logger.Warnf("Failed to soft-delete diagnosis SVM row %s (continuing): %v", svm.Name, err)
		return nil
	}
	return nil
}

// DeleteDiagnosisCifsServer unbinds the diagnosis SVM from Active Directory by
// deleting its CIFS server. Registered on cleanupManager so it fires on every
// exit, and it must run before the VLM SVM delete because VLM's ValidateSvmDelete
// refuses an SVM that still has an AD-joined CIFS server. It fetches the admin
// password and calls provider.DeleteCIFSServer, mirroring
// VolumeDeleteActivity.DeleteCifsServerIfUnused. NotFound is treated as success.
// The trailing string arg is the errorMessage appended by ExecuteRollback (unused).
func (a ActiveDirectoryDiagnoseActivity) DeleteDiagnosisCifsServer(
	ctx context.Context,
	node *models.Node,
	ad *datamodel.ActiveDirectory,
	externalSVMUUID string,
	_ string,
) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting DeleteDiagnosisCifsServer activity")

	if node == nil || ad == nil || externalSVMUUID == "" {
		logger.Info("DeleteDiagnosisCifsServer: missing node, ad or SVM UUID; nothing to do")
		return nil
	}
	if ad.CredentialPath == "" {
		logger.Warnf("DeleteDiagnosisCifsServer: empty CredentialPath for AD %s; skipping (orphan reaper will reconcile)", ad.UUID)
		return nil
	}

	password, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, ad.CredentialPath)
	if err != nil {
		logger.Errorf("DeleteDiagnosisCifsServer: failed to fetch AD password for %s: %v", ad.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	deleter, ok := provider.(cifsServerDeleter)
	if !ok {
		return vsaerrors.WrapAsTemporalApplicationError(
			errors.New("provider does not support CIFS server delete"))
	}

	if err := deleter.DeleteCIFSServer(externalSVMUUID, ad.Username, password); err != nil {
		if utilerrors.IsNotFoundErr(err) {
			logger.Infof("DeleteDiagnosisCifsServer: CIFS server already gone on SVM %s", externalSVMUUID)
			return nil
		}
		logger.Errorf("DeleteDiagnosisCifsServer: failed to delete CIFS server on SVM %s: %v", externalSVMUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("DeleteDiagnosisCifsServer: removed CIFS server on diagnosis SVM %s", externalSVMUUID)
	activity.RecordHeartbeat(ctx, "Finished DeleteDiagnosisCifsServer activity")
	return nil
}

// DeleteDiagnosisKerberosConfig disables Kerberos (with force) on every
// Kerberos-enabled LIF of the diagnosis SVM before the VLM SVM delete runs.
// Registered on cleanupManager only when the run configured Kerberos
// (!ManagedAD && AD.KdcIP != ""). ONTAP refuses to delete an SVM while any LIF
// still has Kerberos enabled, so this clears that blocker; the rest is reclaimed
// by the VLM SVM delete cascade. Like SDE's _deleteKRBConfiguration, it inspects
// the SVM's Kerberos interfaces directly rather than assuming a LIF name.
// NotFound (or no enabled interface) is a no-op. The trailing string arg is the
// errorMessage appended by ExecuteRollback (unused).
func (a ActiveDirectoryDiagnoseActivity) DeleteDiagnosisKerberosConfig(
	ctx context.Context,
	node *models.Node,
	ad *datamodel.ActiveDirectory,
	svmName string,
	externalSVMUUID string,
	_ string,
) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting DeleteDiagnosisKerberosConfig activity")

	if node == nil || ad == nil || svmName == "" || externalSVMUUID == "" {
		logger.Info("DeleteDiagnosisKerberosConfig: missing node, ad, svmName or SVM UUID; nothing to do")
		return nil
	}
	if ad.CredentialPath == "" {
		logger.Warnf("DeleteDiagnosisKerberosConfig: empty CredentialPath for AD %s; skipping (orphan reaper will reconcile)", ad.UUID)
		return nil
	}

	password, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, ad.CredentialPath)
	if err != nil {
		logger.Errorf("DeleteDiagnosisKerberosConfig: failed to fetch AD password for %s: %v", ad.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	cleaner, ok := provider.(kerberosDiagnosisCleaner)
	if !ok {
		return vsaerrors.WrapAsTemporalApplicationError(
			errors.New("provider does not support Kerberos interface teardown"))
	}

	kerberosInterfaces, err := cleaner.GetKerberosInterfacesForSVM(externalSVMUUID, svmName)
	if err != nil {
		if utilerrors.IsNotFoundErr(err) {
			logger.Infof("DeleteDiagnosisKerberosConfig: SVM %s already gone; nothing to disable", externalSVMUUID)
			return nil
		}
		logger.Errorf("DeleteDiagnosisKerberosConfig: failed to list Kerberos interfaces for SVM %s: %v", externalSVMUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Debugf("DeleteDiagnosisKerberosConfig: ONTAP returned %d Kerberos interface(s) for SVM %q (UUID %s)", len(kerberosInterfaces), svmName, externalSVMUUID)

	ou := ""
	if ad.ActiveDirectoryAttributes != nil {
		ou = ad.ActiveDirectoryAttributes.OrganizationalUnit
	}

	disabled := 0
	for i, netif := range kerberosInterfaces {
		if netif == nil {
			continue
		}

		ifaceName := ""
		if netif.Interface != nil && netif.Interface.Name != nil {
			ifaceName = *netif.Interface.Name
		}

		if netif.Enabled == nil || !*netif.Enabled {
			logger.Debugf("DeleteDiagnosisKerberosConfig: interface[%d] name=%q not Kerberos-enabled; skipping", i, ifaceName)
			continue
		}
		if netif.Interface == nil || netif.Interface.UUID == nil {
			logger.Warnf("DeleteDiagnosisKerberosConfig: Kerberos-enabled interface[%d] name=%q missing interface UUID; skipping", i, ifaceName)
			continue
		}

		if err := cleaner.DisableKerberosOnInterface(vsa.KerberosInterfaceModifyParams{
			SvmUUID:       externalSVMUUID,
			InterfaceUUID: *netif.Interface.UUID,
			AdminUsername: ad.Username,
			AdminPassword: password,
			OU:            ou,
		}); err != nil {
			logger.Errorf("DeleteDiagnosisKerberosConfig: failed to disable Kerberos on LIF %q (SVM %s): %v", ifaceName, externalSVMUUID, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		disabled++
		logger.Infof("DeleteDiagnosisKerberosConfig: disabled Kerberos on LIF %q (SVM %s)", ifaceName, externalSVMUUID)
	}

	logger.Infof("DeleteDiagnosisKerberosConfig: disabled Kerberos on %d LIF(s) for diagnosis SVM %s", disabled, externalSVMUUID)
	activity.RecordHeartbeat(ctx, "Finished DeleteDiagnosisKerberosConfig activity")
	return nil
}

// CleanupStaleDiagnosisSVMFromDB removes any leftover diagnosis-SVM row that
// matches the deterministic diagnosis SVM name for the pool. Retained as a
// safety net for orphan-reaper callers; the diagnose workflow itself no longer
// invokes this in the happy path because the cleanupManager now performs a
// proper teardown on every exit. Best-effort: any error is logged and
// swallowed so the workflow stays unblocked.
func (a ActiveDirectoryDiagnoseActivity) CleanupStaleDiagnosisSVMFromDB(ctx context.Context, pool *datamodel.Pool, svmName string) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting CleanupStaleDiagnosisSVMFromDB activity")

	if pool == nil || svmName == "" {
		logger.Info("CleanupStaleDiagnosisSVMFromDB: pool or svmName empty, nothing to do")
		return nil
	}
	existing, err := a.SE.GetSvmByNameAndPoolID(ctx, svmName, pool.ID)
	if err != nil {
		if utilerrors.IsNotFoundErr(err) {
			logger.Infof("CleanupStaleDiagnosisSVMFromDB: no stale row for %s/pool=%d", svmName, pool.ID)
			return nil
		}
		logger.Warnf("CleanupStaleDiagnosisSVMFromDB: lookup failed for %s/pool=%d (continuing): %v", svmName, pool.ID, err)
		return nil
	}
	if existing == nil {
		logger.Infof("CleanupStaleDiagnosisSVMFromDB: no stale row for %s/pool=%d", svmName, pool.ID)
		return nil
	}
	if delErr := a.SE.DeleteSVM(ctx, existing); delErr != nil {
		logger.Warnf("CleanupStaleDiagnosisSVMFromDB: soft-delete failed for %s (continuing): %v", svmName, delErr)
		return nil
	}
	logger.Infof("CleanupStaleDiagnosisSVMFromDB: removed stale row for %s (id=%d uuid=%s)", svmName, existing.ID, existing.UUID)
	return nil
}

// RestoreADStateFromSVMUsage transitions the AD row back to the state implied by
// its real SVM usage: IN_USE when a non-diagnosis SVM references it, READY
// otherwise. Called on every workflow exit so a connectivity check never leaves
// the shared AD stuck in "diagnosing" or in ERROR; a diagnosis failure is
// surfaced on the job/operation only (SDE parity). Diagnosis SVMs are filtered
// out so the short-lived SVM never makes the AD look IN_USE.
func (a ActiveDirectoryDiagnoseActivity) RestoreADStateFromSVMUsage(ctx context.Context, adUUID string) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting RestoreADStateFromSVMUsage activity")

	if adUUID == "" {
		logger.Warn("RestoreADStateFromSVMUsage called with empty adUUID; skipping")
		return nil
	}

	ad, err := a.SE.GetActiveDirectoryByUUID(ctx, adUUID)
	if err != nil {
		logger.Errorf("Failed to load AD %s for state restore: %v", adUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if ad == nil {
		logger.Warnf("AD %s not found; skipping state restore", adUUID)
		return nil
	}

	// Default to READY; promote to IN_USE only if a real SVM references the AD.
	// On a SVM lookup error, default to READY rather than leaving the AD stuck
	// in "diagnosing" (mirrors GetActiveDirectoryStateFromSVMUsage).
	state, details := datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails
	svms, err := a.SE.GetSVMsUsingActiveDirectory(ctx, ad.ID)
	if err != nil {
		logger.Warnf("Failed to fetch SVMs for AD %s, defaulting state to READY: %v", adUUID, err)
	} else if hasNonDiagnosisSVM(svms) {
		state, details = datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails
	}

	ad.State = state
	ad.StateDetails = details
	if _, err := a.SE.UpdateActiveDirectory(ctx, ad); err != nil {
		logger.Errorf("Failed to restore AD %s state to %q: %v", adUUID, state, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Restored AD %s state to %q after diagnosis", adUUID, state)
	activity.RecordHeartbeat(ctx, "Finished RestoreADStateFromSVMUsage activity")
	return nil
}

// hasNonDiagnosisSVM reports whether any SVM in the list is a real (non
// connectivity-check) SVM. Diagnosis SVMs are tagged via
// SvmDetails.IsDirectoryDiagnosis and are ignored so a teardown race does not
// make the AD look IN_USE.
func hasNonDiagnosisSVM(svms []*datamodel.Svm) bool {
	for _, svm := range svms {
		if svm == nil {
			continue
		}
		if svm.SvmDetails != nil && svm.SvmDetails.IsDirectoryDiagnosis {
			continue
		}
		return true
	}
	return false
}
