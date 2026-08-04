package active_directory_activities

import (
	"context"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	utilerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var getOntapRestProvider = _getOntapRestProvider

func _getOntapRestProvider(ctx context.Context, node *models.Node) (*vsa.OntapRestProvider, error) {
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	ontapProvider, ok := provider.(*vsa.OntapRestProvider)
	if !ok {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("provider is not OntapRestProvider"))
	}
	return ontapProvider, nil
}

// GetOrCreateCifsServiceResult contains the result of GetOrCreateCifsService
type GetOrCreateCifsServiceResult struct {
	FQDN            string // FQDN if service was created, empty if service existed
	NeedsDDNS       bool   // Whether DDNS needs to be enabled (only set if service existed)
	CifsServiceName string // Name of the CIFS service (for building FQDN)
	AdDomain        string // AD domain (for building FQDN)
}

// CreateOrModifyADDNS creates or modifies Active Directory DNS configuration
func (a ActiveDirectoryActivity) CreateOrModifyADDNS(ctx context.Context, node *models.Node, ad *vsa.ActiveDirectory, svmName, externalSVMUUID string) error {
	logger := util.GetLogger(ctx)

	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for SVM %s DNS configuration", externalSVMUUID))
	ontapProvider, err := getOntapRestProvider(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating ONTAP REST client for SVM %s DNS configuration", externalSVMUUID))
	client, err := ontapProvider.CreateRESTClient()
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get ONTAP client: %w", err))
	}

	recordHeartbeat(ctx, fmt.Sprintf("Ensuring CIFS server name for SVM %s", externalSVMUUID))
	if err := ontapProvider.EnsureCifsServerNamePostFix(client, ad, svmName); err != nil {
		logger.Error("failed to ensure CIFS server name postfix", "error", err.Error())
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	dnsServersSlice := strings.Split(strings.ReplaceAll(ad.DNS, " ", ""), ",")
	domainsSlice := []string{ad.Domain}

	logger.Info("creating or modifying DNS", "domains", domainsSlice, "dnsServers", dnsServersSlice, "svmUUID", externalSVMUUID)
	recordHeartbeat(ctx, fmt.Sprintf("Getting DNS configuration for SVM %s from ONTAP", externalSVMUUID))
	dns, err := client.NameServices().DNSGet(&ontapRest.DNSGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"servers", "domains"}},
		SvmUUID:    externalSVMUUID,
	})
	if err != nil && !utilerrors.IsNotFoundErr(err) {
		logger.Error("failed to get DNS which is not found error", "error", err.Error(), "domains", domainsSlice, "dnsServers", dnsServersSlice)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if dns == nil {
		recordHeartbeat(ctx, fmt.Sprintf("Creating DNS configuration for SVM %s in ONTAP", externalSVMUUID))
		_, err = client.NameServices().DnsCreate(&ontapRest.DNSCreateParams{
			SvmUUID:    externalSVMUUID,
			Domains:    domainsSlice,
			DNSServers: dnsServersSlice,
		})
		if err != nil {
			logger.Error("failed to create DNS", "error", err.Error(), "domains", domainsSlice, "dnsServers", dnsServersSlice, "svmUUID", externalSVMUUID)
			return vsaerrors.WrapOntapError(err, vsaerrors.DomainDNS)
		}
		return nil
	}

	if utils.ComparePointerStringSlices(dns.Servers, dnsServersSlice) && utils.ComparePointerStringSlices(dns.Domains, domainsSlice) {
		return nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("Modifying DNS configuration for SVM %s in ONTAP", externalSVMUUID))
	if err := client.NameServices().DNSModify(&ontapRest.DNSModifyParams{
		SvmUUID:     externalSVMUUID,
		Domains:     domainsSlice,
		NameServers: dnsServersSlice,
	}); err != nil {
		logger.Error("failed to modify DNS", "error", err.Error(), "domains", domainsSlice, "dnsServers", dnsServersSlice)
		return vsaerrors.WrapOntapError(err, vsaerrors.DomainDNS)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Finished DNS configuration for SVM %s", externalSVMUUID))
	return nil
}

func (a ActiveDirectoryActivity) GetCifsService(ctx context.Context, node *models.Node, svmName, externalSVMUUID string) (*ontapRest.CifsService, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for SVM %s CIFS lookup", externalSVMUUID))
	ontapProvider, err := getOntapRestProvider(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Getting CIFS service for SVM %s from ONTAP", externalSVMUUID))
	cifs, err := ontapProvider.GetCIFSService(svmName, externalSVMUUID)
	if err != nil {
		return nil, vsaerrors.WrapOntapError(err, vsaerrors.DomainSMB)
	}
	return cifs, nil
}

// GetOrCreateCifsService gets an existing CIFS service or creates one if it doesn't exist
// Returns information about the service state
func (a ActiveDirectoryActivity) GetOrCreateCifsService(ctx context.Context, node *models.Node, ad *vsa.ActiveDirectory, svmName, externalSVMUUID string) (*GetOrCreateCifsServiceResult, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Starting CIFS service setup for SVM %s", externalSVMUUID))

	decryptedPassword, err := utils.DecryptPassword(ad.Password)
	if err != nil {
		logger.Error("failed to decrypt AD password", "error", err.Error())
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to decrypt AD password: %w", err))
	}
	ad.Password = log.Secret(*decryptedPassword)

	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for SVM %s CIFS setup", externalSVMUUID))
	ontapProvider, err := getOntapRestProvider(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating ONTAP REST client for SVM %s CIFS setup", externalSVMUUID))
	client, err := ontapProvider.CreateRESTClient()
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get ONTAP client: %w", err))
	}

	recordHeartbeat(ctx, fmt.Sprintf("Ensuring CIFS server name for SVM %s", externalSVMUUID))
	if err := ontapProvider.EnsureCifsServerNamePostFix(client, ad, svmName); err != nil {
		logger.Error("failed to ensure CIFS server name postfix", "error", err.Error())
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Getting CIFS service for SVM %s from ONTAP", externalSVMUUID))
	cifs, err := client.NAS().CifsServiceGet(&ontapRest.CifsServiceGetParams{
		SvmUUID: &externalSVMUUID,
		SvmName: &svmName,
		BaseParams: ontapRest.BaseParams{Fields: []string{
			"ad_domain",
			"name",
		}},
	})
	if err != nil {
		if !utilerrors.IsNotFoundErr(err) {
			logger.Error("failed to get CIFS service", "error", err.Error())
			return nil, vsaerrors.WrapOntapError(err, vsaerrors.DomainSMB)
		}

		logger.Info("CIFS service not found, creating new CIFS service", "svm", externalSVMUUID)
		recordHeartbeat(ctx, fmt.Sprintf("Creating CIFS service for SVM %s in ONTAP", externalSVMUUID))
		fqdn, createErr := ontapProvider.CreateAndSetupCIFSServer(client, ad, externalSVMUUID, svmName)
		if createErr != nil {
			logger.Error("failed to createAndSetupCIFSServer", "error", createErr.Error())
			return nil, vsaerrors.WrapOntapError(createErr, vsaerrors.DomainAD)
		}
		return &GetOrCreateCifsServiceResult{FQDN: fqdn, NeedsDDNS: false}, nil
	}

	result := &GetOrCreateCifsServiceResult{}
	if cifs.Name != nil {
		result.CifsServiceName = *cifs.Name
	}
	if cifs.AdDomain != nil && cifs.AdDomain.Fqdn != nil {
		result.AdDomain = *cifs.AdDomain.Fqdn
	}

	recordHeartbeat(ctx, fmt.Sprintf("Checking DDNS status for SVM %s in ONTAP", externalSVMUUID))
	ddnsEnabled := ontapProvider.IsDDNSEnabled(client, externalSVMUUID)
	if !ddnsEnabled && cifs.Name != nil && cifs.AdDomain != nil && cifs.AdDomain.Fqdn != nil {
		result.NeedsDDNS = true
	}

	logger.Info("CIFS service already exists", "svm", externalSVMUUID, "name", cifs.Name, "needsDDNS", result.NeedsDDNS)
	recordHeartbeat(ctx, fmt.Sprintf("Finished CIFS service setup for SVM %s", externalSVMUUID))
	return result, nil
}

// DdnsModify enables DDNS (Dynamic DNS) for the CIFS service
func (a ActiveDirectoryActivity) DdnsModify(ctx context.Context, node *models.Node, externalSVMUUID, fqdn string) error {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for SVM %s DDNS update", externalSVMUUID))
	ontapProvider, err := getOntapRestProvider(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating ONTAP REST client for SVM %s DDNS update", externalSVMUUID))
	client, err := ontapProvider.CreateRESTClient()
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get ONTAP client: %w", err))
	}

	secureDDNS := true
	recordHeartbeat(ctx, fmt.Sprintf("Updating DDNS configuration for SVM %s in ONTAP", externalSVMUUID))
	if err := client.NameServices().DNSModify(&ontapRest.DNSModifyParams{
		SvmUUID: externalSVMUUID,
		DDNSModifyParams: ontapRest.DDNSModifyParams{
			UseSecure: &secureDDNS,
			Fqdn:      &fqdn,
			Enabled:   nillable.ToPointer(true),
		},
	}); err != nil {
		logger.Error("failed to update DDNS", "error", err.Error(), "fqdn", fqdn)
		return vsaerrors.WrapOntapError(err, vsaerrors.DomainDNS)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Finished DDNS update for SVM %s", externalSVMUUID))
	logger.Info("Successfully enabled DDNS", "svm", externalSVMUUID, "fqdn", fqdn)
	return nil
}

// CreateJunctionPathForCifsShare creates a CIFS share for the junction path
func (a ActiveDirectoryActivity) CreateJunctionPathForCifsShare(ctx context.Context, node *models.Node, svmName, junctionPath string, smbshareProperties []string) error {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for SVM %s CIFS share creation", svmName))
	ontapProvider, err := getOntapRestProvider(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating ONTAP REST client for SVM %s CIFS share creation", svmName))
	client, err := ontapProvider.CreateRESTClient()
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get ONTAP client: %w", err))
	}

	logger.Info("Creating CIFS share", "svm", svmName, "junctionPath", junctionPath, "shareProperties", smbshareProperties)
	recordHeartbeat(ctx, fmt.Sprintf("Creating CIFS share for SVM %s in ONTAP", svmName))
	if err := client.NAS().CifsShareCreate(&ontapRest.CifsShareCreateParams{
		SvmName:         &svmName,
		Path:            junctionPath,
		Name:            junctionPath[1:],
		ShareProperties: smbshareProperties,
	}); err != nil {
		if utilerrors.IsConflictErr(err) || strings.Contains(err.Error(), "duplicate entry") {
			logger.Infof("CIFS share already exists for SVM  %s", svmName)
		} else {
			logger.Error("failed to create junction path for CIFS share", "error", err.Error())
			return vsaerrors.WrapOntapError(err, vsaerrors.DomainSMB)
		}
	}
	recordHeartbeat(ctx, fmt.Sprintf("Finished CIFS share creation for SVM %s", svmName))
	logger.Info("Successfully created CIFS share", "svm", svmName, "junctionPath", junctionPath)
	return nil
}
