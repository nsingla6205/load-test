package vsa

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const (
	ldapSchemaMSAD   = "MS-AD-BIS"
	nsSwitchGroup    = "Group"
	nsSwitchNamemap  = "Namemap"
	nsSwitchPasswd   = "Passwd"
	nsSwitchNetgroup = "Netgroup"
)

var (
	ldapPort                = 389
	ldapRetrySleepInterval  = 1 * time.Second
	ldapMaxRetryCount       = 15
	ldapExtendedGroupSchema = "ldap-extended-group-schema"
	nameServiceDbName       = []string{"Group", "Passwd", "Netgroup", "Namemap"}
)

func getLdapInNsSwitch(client ontapRest.RESTClient) (map[string][]string, error) {
	dbNsSwitchMap := make(map[string][]string)
	getSvmCollectionParams := &ontapRest.SvmGetCollectionParams{BaseParams: ontapRest.BaseParams{Fields: []string{"nsswitch.group", "nsswitch.passwd", "nsswitch.netgroup", "nsswitch.namemap"}}}
	getSvmResp, err := client.SVM().SvmCollectionGet(getSvmCollectionParams)
	if err != nil {
		return nil, err
	}

	v := reflect.ValueOf(getSvmResp[0].Nsswitch)
	for _, name := range nameServiceDbName {
		dbValues := reflect.Indirect(v).FieldByName(name)
		dbValuesSlice := make([]string, dbValues.Len())
		for i := 0; i < dbValues.Len(); i++ {
			dbValuesSlice[i] = reflect.ValueOf(dbValues.Index(i).Elem().Interface()).String()
		}
		dbNsSwitchMap[name] = dbValuesSlice
	}
	return dbNsSwitchMap, nil
}

func modifyLdapInNsSwitch(client ontapRest.RESTClient, svmUUID string, dbNsSwitchMap map[string][]string) error {
	ldapModifyNsSwitchParams := &ontapRest.SvmModifyParams{
		NsSwitch: &ontapRest.NsSwitchSource{},
	}
	ldapModifyNsSwitchParams.SvmUUID = svmUUID
	for _, name := range nameServiceDbName {
		var nsSwitchSources []*models.NsswitchSource
		if val, ok := dbNsSwitchMap[name]; ok {
			for _, v := range val {
				nsSwitchDBValue := models.NsswitchSource(v)
				nsSwitchSources = append(nsSwitchSources, &nsSwitchDBValue)
			}
		}
		if name == nsSwitchGroup {
			ldapModifyNsSwitchParams.NsSwitch.NsSwitchSourceGroup = nsSwitchSources
		}
		if name == nsSwitchPasswd {
			ldapModifyNsSwitchParams.NsSwitch.NsSwitchSourcePasswd = nsSwitchSources
		}
		if name == nsSwitchNetgroup {
			ldapModifyNsSwitchParams.NsSwitch.NsSwitchSourceNetgroup = nsSwitchSources
		}
		if name == nsSwitchNamemap {
			ldapModifyNsSwitchParams.NsSwitch.NsSwitchSourceNamemap = nsSwitchSources
		}
	}

	_, accepted, err := client.SVM().SvmModify(ldapModifyNsSwitchParams)
	if err != nil {
		return err
	}

	if accepted != nil {
		return client.Poll(accepted.JobUUID)
	}

	return nil
}

func modifyExtendedGroupOrv4IdDomainForLdap(client ontapRest.RESTClient, svmUUID string, v4IdDomain *string, allowLocalNFSUsersWithLdap *bool) error {
	var extendedGroupLimit int64 = 1024
	nfsModifyParams := &ontapRest.NfsServiceModifyParams{}
	nfsModifyParams.SvmUUID = svmUUID
	nfsModifyParams.ExtendedGroupsLimit = &extendedGroupLimit
	if v4IdDomain != nil {
		nfsModifyParams.V4IDDomain = v4IdDomain
	}
	if allowLocalNFSUsersWithLdap != nil {
		// to allow local users auth-sys-extended-groups parameter needs to be disabled on SVM.
		allowLocalUsers := !*allowLocalNFSUsersWithLdap
		nfsModifyParams.AllowLocalNFSUsersWithLdap = &allowLocalUsers
	}
	err := client.NAS().NfsServiceModify(nfsModifyParams)
	if err != nil {
		return err
	}
	return nil
}

func (rc *OntapRestProvider) CreateDns(params CreateDnsParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	dnsCreateParams := &ontapRest.DNSCreateParams{
		Domains:    params.Domains,
		DNSServers: params.Servers,
	}
	_, err = client.NameServices().DnsCreate(dnsCreateParams)
	if err != nil {
		return err
	}
	return nil
}

func (rc *OntapRestProvider) CreateLdap(ad *datamodel.ActiveDirectory, volume *datamodel.Volume) error {
	if volume == nil || volume.Svm == nil || volume.Svm.SvmDetails == nil {
		return errors.New("volume/svm details cannot be nil")
	}

	var protocols []string
	if volume.VolumeAttributes != nil {
		protocols = volume.VolumeAttributes.Protocols
	}

	return rc.CreateLdapForSVM(ad, volume.Svm.SvmDetails.ExternalUUID, protocols)
}

// CreateLdapForSVM is the SVM-scoped core of CreateLdap, configuring the LDAP
// client on the given SVM without requiring a *datamodel.Volume (used by flows
// with no associated volume, e.g. AD diagnosis). protocols only decides whether
// to set the NFSv4 v4-id-domain.
func (rc *OntapRestProvider) CreateLdapForSVM(ad *datamodel.ActiveDirectory, svmUUID string, protocols []string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	logger := util.GetLogger(context.Background())
	if err != nil {
		return err
	}

	if svmUUID == "" {
		return errors.New("svm uuid cannot be empty")
	}

	_, err = client.NameServices().LdapGet(&ontapRest.LdapGetParams{SvmUUID: svmUUID})
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			return err
		}
		var ldapSchema = ldapSchemaMSAD
		var ldapExtGroupSchema = ldapExtendedGroupSchema
		var maximumGroups int64 = 1024
		ldapSchemaCreateParams := &ontapRest.LdapSchemaCreateParams{
			Name:     &ldapExtGroupSchema,
			Template: &ldapSchema,
			SvmUUID:  &svmUUID,
		}
		err = client.NameServices().LdapSchemaCreate(ldapSchemaCreateParams)
		if err != nil {
			if errors.IsConflictErr(err) || strings.Contains(err.Error(), "duplicate entry") {
				logger.Infof("Ldap schema already exists: %s", err.Error())
			} else {
				return err
			}
		}

		ldapSchemaModifyParams := &ontapRest.LdapSchemaModifyParams{
			SchemaName:    ldapExtendedGroupSchema,
			MaximumGroups: &maximumGroups,
			SvmUUID:       svmUUID,
		}
		err = client.NameServices().LdapSchemaModify(ldapSchemaModifyParams)
		if err != nil {
			return err
		}

		var signing *string
		var ldapOverTLS bool
		var preferredServers []*string
		bindAsCifsShare := true

		domain := ad.Domain
		ldapOverTLS = ad.ActiveDirectoryAttributes.LdapOverTLS
		signing = getSessionSecurity(ad.ActiveDirectoryAttributes.LdapSigning)

		if ad.ActiveDirectoryAttributes.PreferredServersForLdapClient != "" {
			preferredServer := strings.Split(strings.ReplaceAll(ad.ActiveDirectoryAttributes.PreferredServersForLdapClient, " ", ""), ",")
			for i := range preferredServer {
				server := preferredServer[i]
				preferredServers = append(preferredServers, &server)
			}
		}
		var builder strings.Builder
		for _, val := range strings.Split(domain, ".") {
			builder.WriteString("DC=")
			builder.WriteString(val)
			builder.WriteString(",")
		}
		baseDN := strings.TrimSuffix(builder.String(), ",")
		portNo := int64(ldapPort)

		ldapCreateParams := &ontapRest.LdapCreateParams{
			DomainName:                    &ad.Domain,
			BaseDN:                        &baseDN,
			UserDn:                        &ad.ActiveDirectoryAttributes.UserDN,
			GroupDn:                       &ad.ActiveDirectoryAttributes.GroupDN,
			GroupMembershipFilter:         &ad.ActiveDirectoryAttributes.GroupMembershipFilter,
			LdapPort:                      &portNo,
			TLSEnabled:                    &ldapOverTLS,
			Schema:                        &ldapExtendedGroupSchema,
			SessionSecurity:               signing,
			SvmUUID:                       svmUUID,
			BindAsCifsServer:              &bindAsCifsShare,
			PreferredServersForLdapClient: preferredServers,
		}
		_, err = client.NameServices().LdapCreate(ldapCreateParams)
		// DC attribute sync: CIFS server creates machine account on one of the DC on AD, sync to other DC's can take upto 15 seconds so increasing retry count to take
		// care of auth failure case when ONTAP sends LDAP request to other DC where machine account is not present
		for retryCount := 1; retryCount <= ldapMaxRetryCount && err != nil; retryCount++ {
			time.Sleep(ldapRetrySleepInterval)
			_, err = client.NameServices().LdapCreate(ldapCreateParams)
		}
		if err != nil {
			return err
		}

		dbNsSwitchMap, err := getLdapInNsSwitch(client)
		if err != nil {
			return err
		}

		for _, name := range nameServiceDbName {
			if _, ok := dbNsSwitchMap[name]; ok {
				if !slices.Contains(dbNsSwitchMap[name], "ldap") {
					dbNsSwitchMap[name] = append([]string{"ldap"}, dbNsSwitchMap[name]...)
				}
			}
		}

		err = modifyLdapInNsSwitch(client, svmUUID, dbNsSwitchMap)
		if err != nil {
			return err
		}

		if !ad.ActiveDirectoryAttributes.AllowLocalNFSUsersWithLdap {
			enableLdapAuth := true
			nfsModifyParams := &ontapRest.NfsModifyParams{
				SvmUUID:                      svmUUID,
				AuthSysExtendedGroupsEnabled: &enableLdapAuth,
			}
			err = client.NAS().NfsModify(nfsModifyParams)
			if err != nil {
				return err
			}
		}

		var v4IdDomain *string
		var allowLocalNFSUsersWithLdap *bool
		// set v4IDDomain only for NFSv4 protocol
		for i := range protocols {
			protocol := protocols[i]
			if strings.Contains(protocol, utils.ProtocolNFSv4) {
				v4IdDomain = &ad.Domain
			}
		}

		allowLocalNFSUsersWithLdap = &ad.ActiveDirectoryAttributes.AllowLocalNFSUsersWithLdap
		err = modifyExtendedGroupOrv4IdDomainForLdap(client, svmUUID, v4IdDomain, allowLocalNFSUsersWithLdap)
		if err != nil {
			return err
		}
	}
	return nil
}

func (rc *OntapRestProvider) DeleteLdap(svmUUID string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	isLdapExistsInNSSwitch := false
	dbNsSwitchMap, err := getLdapInNsSwitch(client)
	if err != nil {
		return err
	}
	for _, name := range nameServiceDbName {
		if _, ok := dbNsSwitchMap[name]; ok {
			for index, source := range dbNsSwitchMap[name] {
				if source == "ldap" {
					dbNsSwitchMap[name] = append(dbNsSwitchMap[name][:index], dbNsSwitchMap[name][index+1:]...)
					isLdapExistsInNSSwitch = true
					break
				}
			}
		}
	}

	if isLdapExistsInNSSwitch {
		err = modifyLdapInNsSwitch(client, svmUUID, dbNsSwitchMap)
		if err != nil {
			return err
		}
	}

	err = client.NameServices().LdapDelete(&ontapRest.LdapDeleteParams{SvmUUID: svmUUID})
	if err != nil {
		return err
	}
	return nil
}
