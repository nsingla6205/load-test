package vsa

import (
	"fmt"
	"strings"

	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

const (
	nameMappingPattern               = "(.+)\\$@"
	RealmAdminServerPort       int64 = 749
	RealmPasswordServerPort    int64 = 464
	RealmKdcPort               int64 = 88
	RealmClockSkew             int64 = 5
	RealmKdcVendor                   = "microsoft"
	nameMappingReplacementUser       = "root"
	nameMappingDirection             = "krb-unix"
)

// KerberosRealmCreateParams contains parameters for creating a Kerberos realm
type KerberosRealmCreateParams struct {
	SvmUUID            string
	Realm              string
	KdcIP              string
	AdName             string
	AdminServerIP      *string
	AdminServerPort    *int64
	PasswordServerIP   *string
	PasswordServerPort *int64
	ADServerIP         *string
	ADServerName       *string
	RealmKDCPort       *int64
	RealmClockSkew     *int64
	RealmKDCVendor     *string
}

// KerberosInterfaceModifyParams contains parameters for modifying a Kerberos interface
type KerberosInterfaceModifyParams struct {
	SvmUUID        string
	InterfaceUUID  string
	Spn            string
	MachineAccount string
	AdminUsername  string
	AdminPassword  string
	OU             string
}

func GetUniqueMachineAccount(fqdn string) string {
	// Generate a base machine account name
	baseName := fmt.Sprintf("NFS-%s", fqdn)

	// Remove any special characters and ensure the name is uppercase
	sanitizedBaseName := strings.ToUpper(strings.ReplaceAll(baseName, ".", "-"))

	// Truncate the name to 15 characters if it exceeds the limit
	if len(sanitizedBaseName) > 15 {
		sanitizedBaseName = sanitizedBaseName[:15]
	}

	// Return the machine account name
	return sanitizedBaseName
}

// CreateNameMappingForKerberos creates a name mapping for Kerberos to Unix user mapping
func (rc *OntapRestProvider) CreateNameMappingForKerberos(svmUUID, domain string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	pattern := nameMappingPattern + strings.ToUpper(domain)
	replacement := nameMappingReplacementUser
	direction := nameMappingDirection

	// Check if name mapping already exists
	nameMappings, err := client.NameServices().NameMappingCollectionGet(&ontapRest.NameMappingCollectionGetParams{
		SvmUUID:   &svmUUID,
		Pattern:   &pattern,
		Direction: &direction,
	})
	if err != nil && !errors.IsNotFoundErr(err) {
		return err
	}

	// Check if mapping already exists
	if nameMappings != nil && len(nameMappings) > 0 {
		return nil
	}

	// Find first available index
	allMappings, err := client.NameServices().NameMappingCollectionGet(&ontapRest.NameMappingCollectionGetParams{
		SvmUUID:   &svmUUID,
		Direction: &direction,
	})
	if err != nil && !errors.IsNotFoundErr(err) {
		return err
	}

	index := int64(1)
	if allMappings != nil && len(allMappings) > 0 {
		// Find the highest index and add 1
		maxIndex := int64(0)
		for _, mapping := range allMappings {
			if mapping.Index != nil && *mapping.Index > maxIndex {
				maxIndex = *mapping.Index
			}
		}
		index = maxIndex + 1
	}

	// Create name mapping
	if err := client.NameServices().NameMappingCreate(&ontapRest.NameMappingCreateParams{
		SvmUUID:     &svmUUID,
		Pattern:     &pattern,
		Replacement: &replacement,
		Direction:   &direction,
		Index:       index,
	}); err != nil {
		return err
	}

	return nil
}

// DoesKerberosRealmExist checks if a Kerberos realm exists
func (rc *OntapRestProvider) DoesKerberosRealmExist(svmUUID, realm string) (bool, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return false, err
	}

	realms, err := client.NAS().KerberosRealmGet(&ontapRest.KerberosRealmGetParams{
		SvmUUID: svmUUID,
		Realm:   realm,
	})
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return false, nil
		}
		return false, err
	}

	return realms != nil && len(realms) > 0, nil
}

// CreateKerberosRealm creates a Kerberos realm
func (rc *OntapRestProvider) CreateKerberosRealm(params KerberosRealmCreateParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	// Set defaults if not provided
	kdcPort := RealmKdcPort
	if params.RealmKDCPort != nil {
		kdcPort = *params.RealmKDCPort
	}
	clockSkew := RealmClockSkew
	if params.RealmClockSkew != nil {
		clockSkew = *params.RealmClockSkew
	}
	kdcVendor := RealmKdcVendor
	if params.RealmKDCVendor != nil {
		kdcVendor = *params.RealmKDCVendor
	}
	adminPort := RealmAdminServerPort
	if params.AdminServerPort != nil {
		adminPort = *params.AdminServerPort
	}
	passwordPort := RealmPasswordServerPort
	if params.PasswordServerPort != nil {
		passwordPort = *params.PasswordServerPort
	}

	adminIP := params.KdcIP
	if params.AdminServerIP != nil {
		adminIP = *params.AdminServerIP
	}
	passwordIP := params.KdcIP
	if params.PasswordServerIP != nil {
		passwordIP = *params.PasswordServerIP
	}
	adIP := params.KdcIP
	if params.ADServerIP != nil {
		adIP = *params.ADServerIP
	}
	adName := params.AdName
	if params.ADServerName != nil {
		adName = *params.ADServerName
	}

	realmParams := &ontapRest.KerberosRealmCreateParams{
		SvmUUID:            params.SvmUUID,
		Realm:              params.Realm,
		KdcIP:              params.KdcIP,
		RealmKDCPort:       &kdcPort,
		RealmClockSkew:     &clockSkew,
		RealmKDCVendor:     &kdcVendor,
		AdminServerIP:      &adminIP,
		AdminServerPort:    &adminPort,
		PasswordServerIP:   &passwordIP,
		PasswordServerPort: &passwordPort,
		ADServerIP:         &adIP,
		ADServerName:       &adName,
	}

	return client.NAS().KerberosRealmCreate(realmParams)
}

// GetKerberosInterfaces gets Kerberos interface configurations for a data LIF
func (rc *OntapRestProvider) GetKerberosInterfaces(svmUUID, svmName, interfaceName string) ([]*ontapRest.KerberosInterface, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	interfaces, err := client.NAS().KerberosInterfaceCollectionGet(&ontapRest.KerberosInterfaceCollectionGetParams{
		SvmUUID:       &svmUUID,
		SvmName:       &svmName,
		InterfaceName: &interfaceName,
	})
	if err != nil {
		return nil, err
	}

	return interfaces, nil
}

// GetKerberosInterfacesForSVM returns every Kerberos interface on the SVM (not
// filtered by LIF name), with full fields requested so Enabled and
// Interface.UUID are populated. Used by teardown, which must not depend on a
// particular data-LIF naming convention.
func (rc *OntapRestProvider) GetKerberosInterfacesForSVM(svmUUID, svmName string) ([]*ontapRest.KerberosInterface, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	return client.NAS().KerberosInterfaceCollectionGet(&ontapRest.KerberosInterfaceCollectionGetParams{
		SvmUUID: &svmUUID,
		SvmName: &svmName,
		// Request all fields so "enabled" and interface.uuid are returned; the
		// default is key fields only.
		Fields: []string{"*"},
	})
}

// EnableKerberosOnInterface enables Kerberos on a data LIF interface
func (rc *OntapRestProvider) EnableKerberosOnInterface(params KerberosInterfaceModifyParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	kerberosEnabled := true
	interfaceUUID := params.InterfaceUUID
	if err := client.NAS().KerberosInterfaceModify(&ontapRest.KerberosInterfaceModifyParams{
		SvmUUID:           params.SvmUUID,
		InterfaceUUID:     &interfaceUUID,
		IsKerberosEnabled: &kerberosEnabled,
		Spn:               &params.Spn,
		MachineAccount:    &params.MachineAccount,
		AdminUsername:     &params.AdminUsername,
		AdminPassword:     &params.AdminPassword,
		OU:                &params.OU,
	}); err != nil {
		return err
	}

	return nil
}

// DisableKerberosOnInterface disables Kerberos on a data LIF interface. Force is
// always set so ONTAP removes the machine account and clears the interface even
// when the KDC/AD is unreachable; this is required before an SVM with a
// Kerberos-enabled LIF can be deleted.
func (rc *OntapRestProvider) DisableKerberosOnInterface(params KerberosInterfaceModifyParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	kerberosEnabled := false
	force := true
	interfaceUUID := params.InterfaceUUID
	if err := client.NAS().KerberosInterfaceModify(&ontapRest.KerberosInterfaceModifyParams{
		SvmUUID:           params.SvmUUID,
		InterfaceUUID:     &interfaceUUID,
		IsKerberosEnabled: &kerberosEnabled,
		MachineAccount:    &params.MachineAccount,
		AdminUsername:     &params.AdminUsername,
		AdminPassword:     &params.AdminPassword,
		OU:                &params.OU,
		Force:             &force,
	}); err != nil {
		return err
	}

	return nil
}

// GetDataLifsForSVM gets all data LIFs for the SVM from ONTAP
func (rc *OntapRestProvider) GetDataLifsForSVM(svmUUID, svmName string) ([]*ontapRest.IPInterface, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	var storageDLs []*ontapRest.IPInterface

	networkIPInterfacesGetParams := &ontapRest.NetworkIPInterfacesGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"name", "svm", "ip"}},
		SvmName:    &svmName,
		SvmUUID:    &svmUUID}
	err = client.Networking().NetworkIPInterfacesGet(networkIPInterfacesGetParams, func(ips []*ontapRest.IPInterface) error {
		storageDLs = append(storageDLs, ips...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return storageDLs, nil
}
