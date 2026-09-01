package vsa

import (
	"fmt"
	"regexp"
	"strings"

	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

var (
	cifsSidOperatorsMap = map[string]string{
		"Administrators":   "S-1-5-32-544",
		"Backup Operators": "S-1-5-32-551",
	}
	cifsServerNameRegExp         = regexp.MustCompile("^.+-[a-f0-9]{4}$")
	addSecurityPrivilegesForUser = _addSecurityPrivilegesForUser
	smbMultichannelEnabled       = env.GetBool("SMB_MULTICHANNEL_ENABLED", false)
	secureDDNS                   = env.GetBool("SECURE_DDNS", true)
	isLDAPOverTLS                = env.GetBool("CVS_LDAP_OVER_TLS_ENABLED", false)
	deleteCIFSServer             = _deleteCIFSServer
	updateCIFSShareProperties    = _updateCIFSShareProperties
)

// DiscoveryMode represents the CIFS domain controller discovery mode
type DiscoveryMode string

const (
	DiscoveryModeAll  DiscoveryMode = "all"
	DiscoveryModeSite DiscoveryMode = "site"
	DiscoveryModeNone DiscoveryMode = "none"
)

func (rc *OntapRestProvider) EnsureCIFSShare(params ConfigActiveDirectoryParams) (string, error) {
	var fqdn string
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return fqdn, fmt.Errorf("failed to get ONTAP client: %w", err)
	}

	// don't run this if ldapConfig is provided
	if err := ensureCifsServerNamePostFix(rc.Logger, client, params.ActiveDirectory, params.SVMName); err != nil {
		rc.Logger.Error("failed to ensure CIFS server name postfix", "error", err.Error())
		return fqdn, err
	}

	err = createOrModifyADDNS(rc, params.ExternalSVMUUID, params.ActiveDirectory)
	if err != nil {
		rc.Logger.Error("failed to create/modify ADDNS", "error", err.Error())
		return fqdn, err
	}

	cifs, err := client.NAS().CifsServiceGet(&ontapRest.CifsServiceGetParams{
		SvmUUID: &params.ExternalSVMUUID, SvmName: &params.SVMName, BaseParams: ontapRest.BaseParams{Fields: []string{"ad_domain", "name"}}})
	if err != nil {
		rc.Logger.Error("failed to get CIFS service", "error", err.Error())
		if !errors.IsNotFoundErr(err) {
			return fqdn, err
		}
		fqdn, err = createAndSetupCIFSServer(rc.Logger, client, params.ActiveDirectory, params.ExternalSVMUUID, params.SVMName)
		if err != nil {
			rc.Logger.Error("failed to createAndSetupCIFSServer", "error", err.Error())
			return fqdn, err
		}
	}

	if !isDDNSEnabled(rc.Logger, client, params.ExternalSVMUUID) && cifs.Name != nil && cifs.AdDomain != nil && cifs.AdDomain.Fqdn != nil {
		rc.Logger.Info("Enabling the ddns for cifs", "cifs", *cifs.Name, "svm", params.ExternalSVMUUID)
		fqdn = *cifs.Name + "." + *cifs.AdDomain.Fqdn // netBIOS + "." + ad.Domain
		if err = ddnsModify(client, params.ExternalSVMUUID, fqdn); err != nil {
			rc.Logger.Error("failed to update DDNS during createAndSetupCIFSServer", "error", err.Error(), "fqdn", fqdn)
			return fqdn, err
		}
	}

	rc.Logger.Info("Creating CIFS share", "svm", params.ExternalSVMUUID, "junctionPath", params.JunctionPath)
	if err = createJunctionPathForCifsShare(client, params.SVMName, params.JunctionPath); err != nil {
		rc.Logger.Error("failed to create junction path for CIFS share", "error", err.Error())
		return fqdn, err
	}
	return fqdn, nil
}

var isDDNSEnabled = _isDDNSEnabled

func _isDDNSEnabled(traceLog log.Logger, client ontapRest.RESTClient, svmUUID string) bool {
	traceLog.Info("Get DNS to check the status of DDNS")
	dns, err := getDns(client, svmUUID)
	if err != nil {
		traceLog.Error("Failed to get DNS details", "error", err, "svmUUID", svmUUID)
		return true
	}
	if dns == nil || dns.DynamicDNS == nil || dns.DynamicDNS.Enabled == nil {
		return false
	}
	traceLog.Debugf("dns.DynamicDNS.Enable:[%v]", *dns.DynamicDNS.Enabled)
	return *dns.DynamicDNS.Enabled
}

func getDns(client ontapRest.RESTClient, svmUUID string) (*ontapRest.DNS, error) {
	return client.NameServices().DNSGet(&ontapRest.DNSGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"dynamic_dns"}},
		SvmUUID:    svmUUID,
	})
}

var createJunctionPathForCifsShare = _createJunctionPathForCifsShare

func _createJunctionPathForCifsShare(api ontapRest.RESTClient, svmName, junctionPath string) error {
	if junctionPath == "" || junctionPath[0] != '/' {
		return errors.New(fmt.Sprintf("Junction path is not correct [%s] for svm [%s]", junctionPath, svmName))
	}
	return api.NAS().CifsShareCreate(&ontapRest.CifsShareCreateParams{
		SvmName: &svmName,
		Path:    junctionPath,
		Name:    junctionPath[1:],
	})
}

var ensureCifsServerNamePostFix = _ensureCifsServerNamePostFix

func _ensureCifsServerNamePostFix(traceLog log.Logger, client ontapRest.RESTClient, ad *ActiveDirectory, svmName string) error {
	cifsService, err := client.NAS().CifsServiceList(&ontapRest.CifsServiceGetParams{SvmName: nillable.ToPointer(svmName), BaseParams: ontapRest.BaseParams{Fields: []string{"name", "svm.name"}}})
	if err != nil {
		traceLog.Error("failed to get CIFS service during ensureCifsServerNamePostFix", "error", err.Error())
		return err
	}

	var netBIOS string
	if len(ad.NetBIOS) <= 10 {
		netBIOS = strings.ToUpper(ad.NetBIOS) + "-"
	} else {
		netBIOS = strings.ToUpper(ad.NetBIOS[0:10]) + "-"
	}
	for _, cifs := range ad.CIFSServers {
		for _, otCifs := range cifsService {
			if cifs.ServerNamePostfix == "" && cifs.SVMName == *otCifs.Svm.Name {
				cifs.ServerNamePostfix = strings.ToLower(strings.Replace(*otCifs.Name, netBIOS, "", 1))
			}
		}
	}
	if ad.CIFSServers == nil {
		for _, otCifs := range cifsService {
			ad.CIFSServers = append(ad.CIFSServers, &CIFSServer{SVMUUID: *otCifs.Svm.UUID, ServerNamePostfix: strings.Replace(*otCifs.Name, netBIOS, "", 1)})
		}
	}
	return nil
}

var createOrModifyADDNS = _createOrModifyADDNS

func _createOrModifyADDNS(rc *OntapRestProvider, svmUUID string, ad *ActiveDirectory) error {
	if ad == nil {
		return errors.New("Active Directory is not specified")
	}
	if ad.DNS == "" {
		return errors.New(fmt.Sprintf("DNS is not specified for Active Directory for svmUUID [%s]", svmUUID))
	}
	dnsServersSlice := strings.Split(strings.Replace(ad.DNS, " ", "", -1), ",")
	domainsSlice := []string{ad.Domain}

	return createOrModifyDNS(rc, svmUUID, domainsSlice, dnsServersSlice)
}

var createOrModifyDNS = _createOrModifyDNS

func _createOrModifyDNS(rc *OntapRestProvider, svmUUID string, domains, dnsServers []string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return fmt.Errorf("failed to get ONTAP client: %w", err)
	}
	rc.Logger.Info("creating or modifying DNS", "domains", domains, "dnsServers", dnsServers, "svmUUID", svmUUID)
	dns, err := client.NameServices().DNSGet(&ontapRest.DNSGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"servers", "domains"}},
		SvmUUID:    svmUUID,
	})
	if err != nil && !errors.IsNotFoundErr(err) {
		rc.Logger.Error("failed to get DNS which is not found error", "error", err.Error(), "domains", domains, "dnsServers", dnsServers)
		return err
	}

	if dns == nil {
		_, err = client.NameServices().DnsCreate(&ontapRest.DNSCreateParams{
			SvmUUID:    svmUUID,
			Domains:    domains,
			DNSServers: dnsServers,
		})
		if err != nil {
			rc.Logger.Error("failed to create DNS", "error", err.Error(), "domains", domains, "dnsServers", dnsServers, "svmUUID", svmUUID)
			return err
		}
	} else {
		if !utils.ComparePointerStringSlices(dns.Servers, dnsServers) || !utils.ComparePointerStringSlices(dns.Domains, domains) {
			err := client.NameServices().DNSModify(&ontapRest.DNSModifyParams{
				SvmUUID:     svmUUID,
				Domains:     domains,
				NameServers: dnsServers,
			})
			if err != nil {
				rc.Logger.Error("failed to modify DNS", "error", err.Error(), "domains", domains, "dnsServers", dnsServers)
				return err
			}
		}
	}
	return nil
}

var createAndSetupCIFSServer = _createAndSetupCIFSServer

func _createAndSetupCIFSServer(tracelog log.Logger, api ontapRest.RESTClient, ad *ActiveDirectory, externalSVMUUID, svmName string) (string, error) {
	serverNamePostfix := ""
	// The code below might be too slow to support 65535 CIFS servers, 1024 should be enough for now
	if len(ad.CIFSServers) >= 1024 {
		return "", errors.New("Active Directory machine account limit exceeded") // Paranoia
	}

	serverNamePostfix = generateCIFSServerNamePostfix(ad.CIFSServers)

	var netBIOS string
	if len(ad.NetBIOS) <= 10 {
		netBIOS = ad.NetBIOS + "-" + serverNamePostfix
	} else {
		netBIOS = ad.NetBIOS[0:10] + "-" + serverNamePostfix
	}

	err := createCIFSServer(tracelog, api, externalSVMUUID, svmName, netBIOS, ad)
	if err != nil {
		tracelog.Error("failed to create CIFS server during createAndSetupCIFSServer", "error", err.Error())
		return "", err
	}

	err = cifsServerSetup(tracelog, api.NAS(), externalSVMUUID)
	if err != nil {
		tracelog.Error("failed to setup CIFS server during createAndSetupCIFSServer", "error", err.Error())
		return "", errors.NewCompositeErr(err, deleteCIFSServer(tracelog, api, externalSVMUUID, ad.Username, ad.Password.String()))
	}

	for group, users := range ad.Users {
		if group != utils.ActiveDirectorySeSecurityPrivilege {
			domainUsers := prependDomainToUsers(users, ad.Domain)
			if len(domainUsers) > 0 {
				err = api.NAS().CifsServiceAddMembers(&ontapRest.CifsServiceModifyGroupMembersParams{
					SvmUUID: externalSVMUUID,
					Sid:     getSidFromGroupName(group),
					Members: domainUsers,
				})
				if err != nil {
					if strings.Contains(err.Error(), "Reason: duplicate entry.") {
						tracelog.Warnf("Error while adding members to CIFS server: %s", err.Error())
					} else {
						return "", errors.NewCompositeErr(err, deleteCIFSServer(tracelog, api, externalSVMUUID, ad.Username, ad.Password.String()))
					}
				}
			}
		} else {
			for _, user := range users {
				err = addSecurityPrivilegesForUser(api, ad.Domain+"\\"+user, externalSVMUUID)
				if err != nil {
					tracelog.Error("failed to assign SeSecurity privilege to user during CA share creation", "error", err.Error(), "ad", ad, "user", user)
					return "", errors.NewCompositeErr(err, deleteCIFSServer(tracelog, api, externalSVMUUID, ad.Username, ad.Password.String()))
				}
			}
		}
	}

	fqdn := netBIOS + "." + ad.Domain
	if err = ddnsModify(api, externalSVMUUID, fqdn); err != nil {
		tracelog.Error("failed to update DDNS during createAndSetupCIFSServer", "error", err.Error(), "fqdn", fqdn)
		return "", err
	}

	ad.CIFSServers = append(ad.CIFSServers,
		&CIFSServer{
			SVMUUID:           externalSVMUUID,
			ServerNamePostfix: serverNamePostfix,
		},
	)

	return fqdn, nil
}

var ddnsModify = _ddnsModify

func _ddnsModify(api ontapRest.RESTClient, externalSVMUUID, fqdn string) error {
	return api.NameServices().DNSModify(&ontapRest.DNSModifyParams{
		SvmUUID: externalSVMUUID,
		DDNSModifyParams: ontapRest.DDNSModifyParams{
			UseSecure: &secureDDNS,
			Fqdn:      &fqdn,
			Enabled:   nillable.ToPointer(true),
		},
	})
}

// prependDomainToUsers prepends the given domain to a list of users
func prependDomainToUsers(users []string, domain string) []string {
	var domainUsers []string
	for _, user := range users {
		if user == "Administrator" || user == "Domain Admins" || user == "AAD DC Administrators" {
			continue
		}
		domainUsers = append(domainUsers, prependDomainToUser(user, domain))
	}
	return domainUsers
}

var generateCIFSServerNamePostfix = _generateCIFSServerNamePostfix

func _generateCIFSServerNamePostfix(servers []*CIFSServer) string {
	seenPostfix := make(map[string]bool)
	for _, srv := range servers {
		seenPostfix[srv.ServerNamePostfix] = true
	}

	serverNamePostfix := ""
	for {
		serverNamePostfix = utils.GenerateRandomHex(4) // If you change the number of hex characters, it must be changed for the regex in function `CifsServerCreate` as well
		if seenPostfix[serverNamePostfix] == true {
			continue
		}
		break
	}

	return serverNamePostfix
}

var createCIFSServer = _createCIFSServer

func _createCIFSServer(tracelog log.Logger, api ontapRest.RESTClient, svmUUID, svmName, name string, ad *ActiveDirectory) (err error) {
	if isTLSRequired(ad) {
		secc := api.Security()
		_, getCertError := secc.ServerRootCACertificateGet(&ontapRest.ServerRootCAGetParams{
			SvmName:         &svmName,
			CertificateType: nillable.ToPointer("server-ca"),
		})
		if getCertError != nil && !errors.IsNotFoundErr(getCertError) {
			tracelog.Error("error getting server root CA certificate", "getCertError", getCertError.Error())
			return getCertError
		} else if errors.IsNotFoundErr(getCertError) {
			tracelog.Warnf("server root CA certificate not found, installing it")
			_, installCertError := secc.ServerRootCACertificateInstall(&ontapRest.ServerRootCAInstallParams{
				SvmName:         &svmName,
				Certificate:     ad.ServerRootCaCertificate,
				CertificateType: nillable.ToPointer("server-ca"),
			})
			if installCertError != nil {
				tracelog.Error("error installing server root CA certificate", "installCertError", installCertError.Error())
				return installCertError
			}
			defer func() {
				if err != nil {
					cert, getCertError := secc.ServerRootCACertificateGet(&ontapRest.ServerRootCAGetParams{
						SvmName:         &svmName,
						CertificateType: nillable.ToPointer("server-ca"),
					})
					if getCertError != nil && !errors.IsNotFoundErr(getCertError) {
						tracelog.Warn("error getting server root CA certificate", "getCertError", getCertError.Error())
						return
					}
					// attempt certificate deletion only when certificate is found
					if getCertError == nil {
						delCertErr := secc.ServerRootCACertificateDelete(&ontapRest.ServerRootCADeleteParams{
							SvmName:              &svmName,
							SerialNumber:         cert.SerialNumber,
							CommonName:           cert.CommonName,
							CertificateAuthority: cert.Ca,
						})
						if delCertErr != nil {
							tracelog.Warn("error deleting server root CA certificate", "delCertErr", delCertErr.Error())
							return
						}
					}
				}
			}()
		}
	}

	nasc := api.NAS()
	if ad.Site != nil && *ad.Site != "" {
		tracelog.Info("starting the nasc.CifsDomainModify with option site", "svnUUID", svmUUID)
		err = nasc.CifsDomainModify(&ontapRest.CifsDomainModifyParams{
			SvmUUID:       svmUUID,
			DiscoveryMode: nillable.ToPointer("site"),
		})
		if err != nil {
			tracelog.Errorf("failed to CifsDomainModify with discoveryMode site during createCIFSServer, error: %s", err.Error())
			return err
		}
		tracelog.Info("successfully performed nasc.CifsDomainModify with option site", "svnUUID", svmUUID)
	} else {
		tracelog.Info("starting the nasc.CifsDomainModify with option all", "svnUUID", svmUUID)
		err = nasc.CifsDomainModify(&ontapRest.CifsDomainModifyParams{
			SvmUUID:       svmUUID,
			DiscoveryMode: nillable.ToPointer("all"),
		})
		if err != nil {
			tracelog.Errorf("failed to CifsDomainModify with discoveryMode all during createCIFSServer, error: %s", err.Error())
			return err
		}
		tracelog.Info("successfully performed nasc.CifsDomainModify with option all", "svmUUID", svmUUID)
	}

	if !cifsServerNameRegExp.MatchString(name) {
		return errors.New("postfix is missing from CIFS server name")
	}

	username := &ad.Username
	password := nillable.ToPointer(string(ad.Password))

	tracelog.Info("starting the nasc.CifsServiceCreate")

	_, job, err := nasc.CifsServiceCreate(&ontapRest.CifsServiceCreateParams{
		SvmName:            &svmName,
		Name:               &name,
		Domain:             &ad.Domain,
		OrganizationalUnit: &ad.OrganizationalUnit,
		Username:           username,
		Password:           password,
		Force:              nillable.ToPointer(false),
		Site:               ad.Site,
	})
	if err != nil {
		tracelog.Errorf("failed to CifsServiceCreate, error: %s", err.Error())
		return err
	}
	if job != nil {
		if err = api.Poll(job.JobUUID); err != nil {
			return err
		}
	}
	tracelog.Info("successfully completed the nasc.CifsServiceCreate")
	tracelog.Info("starting the nasc.CifsServiceModify")
	if err = nasc.CifsServiceModify(&ontapRest.CifsServiceModifyParams{
		SvmUUID:                  &svmUUID,
		AesEncryptionEnabled:     ad.AesEncryption,
		TLSEnabled:               ad.LdapOverTLS,
		EncryptDCConnections:     ad.EncryptDCConnections,
		CompatibilityLevel:       nillable.ToPointer("ntlmv2_krb"),
		SessionSecurityForAdLdap: getSessionSecurity(*ad.LdapSigning),
		Username:                 username,
		Password:                 password,
	}); err != nil {
		tracelog.Errorf("failed to CifsServiceModify, error: %s", err.Error())
		return err
	}
	tracelog.Info("successfully completed the nasc.CifsServiceModify")
	azureAdminGroup := []string{ad.Domain + "\\AAD DC Administrators"}
	if aagerr := nasc.CifsServiceAddMembers(&ontapRest.CifsServiceModifyGroupMembersParams{
		SvmUUID: svmUUID,
		Sid:     getSidFromGroupName(utils.ActiveDirectoryGroupBuiltInAdministrators),
		Members: azureAdminGroup,
	}); aagerr != nil {
		subWarning := `\AAD DC Administrators to the "BUILTIN\Administrators" group`
		tracelog.Warn("Failed to add " + ad.Domain + subWarning)
	}

	return nasc.CifsDomainModify(&ontapRest.CifsDomainModifyParams{
		SvmUUID:         svmUUID,
		ScheduleEnabled: nillable.ToPointer(true),
	})
}

var cifsServerSetup = _cifsServerSetup

func _cifsServerSetup(tracelog log.Logger, tnas ontapRest.NASClient, svmUUID string) error {
	err := tnas.CifsServiceModify(&ontapRest.CifsServiceModifyParams{
		SvmUUID:           &svmUUID,
		CopyOffload:       nillable.ToPointer(false),
		Multichannel:      &smbMultichannelEnabled,
		RestrictAnonymous: nillable.ToPointer("no_access"),
	})
	if err != nil {
		tracelog.Errorf("failed to CifsServiceModify during cifsServerSetup, error: %s", err.Error())
		return err
	}

	err = tnas.CifsShareACLDelete(&ontapRest.CifsShareACLDeleteParams{
		SvmUUID:   svmUUID,
		ShareName: `c$`,
		User:      utils.ActiveDirectoryGroupBuiltInAdministrators,
	})
	if err != nil && !strings.Contains(err.Error(), "entry doesn't exist") {
		tracelog.Errorf("failed to CifsShareACLDelete during cifsServerSetup, error: %s", err.Error())
		return err
	}

	return nil
}

func isTLSRequired(ad *ActiveDirectory) bool {
	return isLDAPOverTLS && *ad.LdapOverTLS
}

func _deleteCIFSServer(traceLog log.Logger, trc ontapRest.RESTClient, svmUUID, AdUsername, AdPassword string) error {
	traceLog.Infof("Deleting CIFS server %s, AdUsername: %s", svmUUID, AdUsername)
	err := trc.NameServices().DNSModify(&ontapRest.DNSModifyParams{DDNSModifyParams: ontapRest.DDNSModifyParams{
		UseSecure: nillable.ToPointer(false),
		Enabled:   nillable.ToPointer(false),
	}, SvmUUID: svmUUID})
	if err != nil {
		traceLog.Errorf("failed to update DDNS during deleteCIFSServer, error: %s", err.Error())
		return err
	}

	return trc.NAS().CifsServiceDelete(&ontapRest.CifsServiceDeleteParams{SvmUUID: svmUUID, AdminUsername: AdUsername, AdminPassword: AdPassword, Force: true})
}

func getSidFromGroupName(group string) string {
	groupName := strings.SplitAfter(group, `\`)
	var sid string
	if len(groupName) > 1 {
		sid = cifsSidOperatorsMap[groupName[1]]
	}
	return sid
}

func _addSecurityPrivilegesForUser(api ontapRest.RESTClient, user, svmUUID string) error {
	userGroupPrivilegesCreateParams := &ontapRest.CifsServiceModifySecurityPrivilegeParams{
		Member:  user,
		SvmUUID: svmUUID,
	}
	err := api.NAS().CifsServiceAddSecurityPrivilege(userGroupPrivilegesCreateParams)
	if err != nil {
		return err
	}
	return err
}

// prependDomainToUser prepends the given domain to a single user
func prependDomainToUser(user, domain string) string {
	return domain + "\\" + user
}

func getSessionSecurity(ldapSigning bool) *string {
	sessionSecurity := "none"
	if ldapSigning {
		sessionSecurity = "sign"
	}
	return &sessionSecurity
}

// EnsureCifsServerNamePostFix ensures that CIFS server name has a postfix
// This is a public wrapper around the internal function for use in activities
func (rc *OntapRestProvider) EnsureCifsServerNamePostFix(client ontapRest.RESTClient, ad *ActiveDirectory, svmName string) error {
	return ensureCifsServerNamePostFix(rc.Logger, client, ad, svmName)
}

// CreateAndSetupCIFSServer creates and sets up a new CIFS server
// This is a public wrapper around the internal function for use in activities
func (rc *OntapRestProvider) CreateAndSetupCIFSServer(client ontapRest.RESTClient, ad *ActiveDirectory, externalSVMUUID, svmName string) (string, error) {
	return createAndSetupCIFSServer(rc.Logger, client, ad, externalSVMUUID, svmName)
}

// IsDDNSEnabled checks if DDNS is enabled for the SVM
// This is a public wrapper around the internal function for use in activities
func (rc *OntapRestProvider) IsDDNSEnabled(client ontapRest.RESTClient, svmUUID string) bool {
	return isDDNSEnabled(rc.Logger, client, svmUUID)
}

// DeleteCIFSServer removes the CIFS server associated with the provided SVM UUID.
// It disables DDNS before deleting the CIFS service to ensure a clean teardown.
func (rc *OntapRestProvider) DeleteCIFSServer(externalSVMUUID, adUsername, adPassword string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return fmt.Errorf("failed to get ONTAP client: %w", err)
	}

	return deleteCIFSServer(rc.Logger, client, externalSVMUUID, adUsername, adPassword)
}

func (rc *OntapRestProvider) UpdateCIFSServer(svmUUID, shareName string, shareProperties []string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return fmt.Errorf("failed to get ONTAP client: %w", err)
	}
	return updateCIFSShareProperties(rc.Logger, client, svmUUID, shareName, shareProperties)
}

func _updateCIFSShareProperties(traceLog log.Logger, trc ontapRest.RESTClient, svmUUID, shareName string, shareProperties []string) error {
	traceLog.Infof("Updating CIFS server %s, Share: %s, ShareProperties: %v", svmUUID, shareName, shareProperties)
	err := trc.NAS().CifsShareModify(&ontapRest.CifsShareModifyParams{
		SvmUUID:         svmUUID,
		ShareName:       shareName,
		ShareProperties: shareProperties})
	if err != nil {
		traceLog.Errorf("failed to update CIFS Server properties, error: %s", err.Error())
		return err
	}
	return nil
}

func (rc *OntapRestProvider) CifsShareCollectionGet(svmUUID, shareName string, fields []string) ([]string, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, fmt.Errorf("failed to get ONTAP client in CifsShareCollectionGet with error: %v", err)
	}
	rc.Logger.Infof("Getting CIFS ShareCollectionGet for svm: %s, Share: %s, fields: %v", svmUUID, shareName, fields)
	share, err := client.NAS().CifsShareCollectionGet(&ontapRest.CifsShareCollectionGetParams{
		SvmUUID:   svmUUID,
		ShareName: shareName,
		Fields:    fields,
	})
	if err != nil {
		rc.Logger.Errorf("failed to Get CifsShareCollectionGet, error: %s", err.Error())
		return nil, err
	}
	return share.ShareProperties, nil
}
