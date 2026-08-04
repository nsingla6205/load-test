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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// CreateNameMappingForKerberosActivity creates a name mapping for Kerberos to Unix user mapping
func (a ActiveDirectoryActivity) CreateNameMappingForKerberosActivity(ctx context.Context, node *models.Node, externalSVMUUID, domain string) error {
	logger := util.GetLogger(ctx)
	logger.Info("Creating name mapping for Kerberos", "svm", externalSVMUUID, "domain", domain)

	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for SVM %s Kerberos name mapping", externalSVMUUID))
	ontapProvider, err := getOntapRestProvider(ctx, node)
	if err != nil {
		logger.Error("Failed to get ONTAP client", "error", err.Error())
		return vsaerrors.WrapOntapError(err, vsaerrors.DomainKerberos)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating Kerberos name mapping for SVM %s in ONTAP", externalSVMUUID))
	if err := ontapProvider.CreateNameMappingForKerberos(externalSVMUUID, domain); err != nil {
		logger.Error("Failed to create name mapping for Kerberos", "error", err.Error())
		return vsaerrors.WrapOntapError(err, vsaerrors.DomainKerberos)
	}

	logger.Info("Successfully created name mapping for Kerberos")
	return nil
}

// CheckKerberosRealmExistsActivity checks if a Kerberos realm exists
func (a ActiveDirectoryActivity) CheckKerberosRealmExistsActivity(ctx context.Context, node *models.Node, externalSVMUUID, realm string) (bool, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Checking if Kerberos realm exists", "svm", externalSVMUUID, "realm", realm)

	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for SVM %s Kerberos realm check", externalSVMUUID))
	ontapProvider, err := getOntapRestProvider(ctx, node)
	if err != nil {
		logger.Error("Failed to get ONTAP client", "error", err.Error())
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Checking Kerberos realm for SVM %s in ONTAP", externalSVMUUID))
	realmExists, err := ontapProvider.DoesKerberosRealmExist(externalSVMUUID, realm)
	if err != nil {
		logger.Error("Failed to check Kerberos realm", "error", err.Error())
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("Kerberos realm check completed", "realm", realm, "exists", realmExists)
	return realmExists, nil
}

// CreateKerberosRealmActivity creates a Kerberos realm
func (a ActiveDirectoryActivity) CreateKerberosRealmActivity(ctx context.Context, node *models.Node, externalSVMUUID string, realmParams vsa.KerberosRealmCreateParams) error {
	logger := util.GetLogger(ctx)
	logger.Info("Creating Kerberos realm", "svm", externalSVMUUID, "realm", realmParams.Realm)

	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for SVM %s Kerberos realm creation", externalSVMUUID))
	ontapProvider, err := getOntapRestProvider(ctx, node)
	if err != nil {
		logger.Error("Failed to get ONTAP client", "error", err.Error())
		return vsaerrors.WrapOntapError(err, vsaerrors.DomainKerberos)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating Kerberos realm for SVM %s in ONTAP", externalSVMUUID))
	if err := ontapProvider.CreateKerberosRealm(realmParams); err != nil {
		logger.Error("Failed to create Kerberos realm", "error", err.Error())
		return vsaerrors.WrapOntapError(err, vsaerrors.DomainKerberos)
	}

	logger.Info("Successfully created Kerberos realm", "realm", realmParams.Realm)
	return nil
}

// GetDataLifsForSVMActivity gets all data LIFs for the SVM from ONTAP
func (a ActiveDirectoryActivity) GetDataLifsForSVMActivity(ctx context.Context, node *models.Node, externalSVMUUID, svmName string) ([]*ontapRest.IPInterface, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Getting data LIFs for SVM", "svm", externalSVMUUID, "svmName", svmName)

	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for SVM %s data LIF lookup", externalSVMUUID))
	ontapProvider, err := getOntapRestProvider(ctx, node)
	if err != nil {
		logger.Error("Failed to get ONTAP client", "error", err.Error())
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Getting data LIFs for SVM %s from ONTAP", externalSVMUUID))
	dataLifs, err := ontapProvider.GetDataLifsForSVM(externalSVMUUID, svmName)
	if err != nil {
		logger.Error("Failed to get data LIFs for SVM", "error", err.Error())
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("Successfully retrieved data LIFs", "count", len(dataLifs))
	return dataLifs, nil
}

// EnableKerberosOnInterfaceActivity enables Kerberos on a data LIF interface
func (a ActiveDirectoryActivity) EnableKerberosOnInterfaceActivity(ctx context.Context, node *models.Node, externalSVMUUID, svmName, dataLifName, dataLifIP, fqdn, realm string, ad *vsa.ActiveDirectory) error {
	logger := util.GetLogger(ctx)
	logger.Info("Enabling Kerberos on interface", "dataLifName", dataLifName, "dataLifIP", dataLifIP, "fqdn", fqdn, "realm", realm)

	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for data LIF %s Kerberos enablement", dataLifName))
	ontapProvider, err := getOntapRestProvider(ctx, node)
	if err != nil {
		logger.Error("Failed to get ONTAP client", "error", err.Error())
		return vsaerrors.WrapOntapError(err, vsaerrors.DomainKerberos)
	}

	// Generate SPN
	spn := fmt.Sprintf("nfs/%s@%s", fqdn, realm)

	// Generate machine account
	machineAccount := vsa.GetUniqueMachineAccount(fqdn)

	// Get Kerberos interface configuration
	recordHeartbeat(ctx, fmt.Sprintf("Getting Kerberos interface configuration for data LIF %s from ONTAP", dataLifName))
	kerberosInterfaces, err := ontapProvider.GetKerberosInterfaces(externalSVMUUID, svmName, dataLifName)
	if err != nil {
		logger.Error("Failed to get Kerberos interface", "error", err.Error(), "dataLifName", dataLifName)
		return vsaerrors.WrapOntapError(err, vsaerrors.DomainKerberos)
	}

	if kerberosInterfaces == nil || len(kerberosInterfaces) == 0 {
		logger.Error("No Kerberos interface found for data LIF", "dataLifName", dataLifName)
		return vsaerrors.WrapOntapError(fmt.Errorf("no Kerberos interface found for data LIF: %s", dataLifName), vsaerrors.DomainKerberos)
	}

	// Check if Kerberos is already enabled
	netif := kerberosInterfaces[0]
	if netif.Enabled != nil && *netif.Enabled {
		logger.Info("Kerberos already enabled on interface", "dataLifName", dataLifName)
		return nil
	}

	// Check if interface UUID is available
	if netif.Interface == nil || netif.Interface.UUID == nil {
		logger.Error("Interface UUID not found for data LIF", "dataLifName", dataLifName)
		return vsaerrors.WrapOntapError(fmt.Errorf("interface UUID not found for data LIF: %s", dataLifName), vsaerrors.DomainKerberos)
	}

	// Decrypt password
	decryptedPassword, err := utils.DecryptPassword(ad.Password)
	if err != nil {
		logger.Error("Failed to decrypt AD password", "error", err.Error())
		return vsaerrors.WrapOntapError(fmt.Errorf("failed to decrypt AD password: %w", err), vsaerrors.DomainKerberos)
	}

	// Enable Kerberos on interface
	interfaceUUID := *netif.Interface.UUID
	password := string(*decryptedPassword)
	logger.Info("Enabling Kerberos on interface", "dataLifName", dataLifName, "spn", spn, "machineAccount", machineAccount, "interfaceUUID", interfaceUUID)

	modifyParams := vsa.KerberosInterfaceModifyParams{
		SvmUUID:        externalSVMUUID,
		InterfaceUUID:  interfaceUUID,
		Spn:            spn,
		MachineAccount: machineAccount,
		AdminUsername:  ad.Username,
		AdminPassword:  password,
		OU:             ad.OrganizationalUnit,
	}

	recordHeartbeat(ctx, fmt.Sprintf("Enabling Kerberos on data LIF %s in ONTAP", dataLifName))
	err = ontapProvider.EnableKerberosOnInterface(modifyParams)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), strings.ToLower("Kerberos is already enabled")) {
		logger.Info("Kerberos already enabled on interface", "dataLifName", dataLifName)
		return nil
	} else if err != nil {
		logger.Error("Failed to enable Kerberos on interface", "error", err.Error(), "dataLifName", dataLifName)
		return vsaerrors.WrapOntapError(err, vsaerrors.DomainKerberos)
	}

	logger.Info("Successfully enabled Kerberos on interface", "dataLifName", dataLifName)
	return nil
}
