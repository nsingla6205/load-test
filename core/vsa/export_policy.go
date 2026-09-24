package vsa

import (
	"fmt"
	"strconv"
	"strings"

	ontaprestmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func convertStorageExportPolicyRuleToONTAP(rule ExportRule) *ontapRest.ExportRule {
	var protocols []string
	if rule.CIFS {
		protocols = append(protocols, utils.GetOntapValue(utils.ProtocolSMB))
	}
	if rule.NFSv3 {
		protocols = append(protocols, utils.GetOntapValue(utils.ProtocolNFSv3))
	}
	if rule.NFSv4 {
		protocols = append(protocols, utils.GetOntapValue(utils.ProtocolNFSv4))
	}
	var roRules, rwRules string
	roRules = models.ExportAuthenticationFlavorSys
	rwRules = models.ExportAuthenticationFlavorAny

	// Read-write access must be derived from the read-write flag only. A
	// read-only rule must not receive a read-write flavor, otherwise AUTH_SYS
	// clients would be granted write access on a read-only export.
	if rule.UnixReadWrite {
		roRules = models.ExportAuthenticationFlavorSys
		rwRules = models.ExportAuthenticationFlavorSys
	} else if rule.UnixReadOnly {
		roRules = models.ExportAuthenticationFlavorSys
		rwRules = models.ExportAuthenticationFlavorNever
	}
	if utils.IsRuleKerberosSupported(rule.NFSv4, rule.Kerberos5ReadWrite, rule.Kerberos5ReadOnly, rule.Kerberos5pReadWrite,
		rule.Kerberos5pReadOnly, rule.Kerberos5iReadOnly, rule.Kerberos5iReadWrite) {
		roRules, rwRules = convertStorageExportPolicyAuthenticationFlavorToONTAP(rule)
	}

	if !rule.CIFS && !rule.NFSv3 && !rule.NFSv4 {
		roRules = *nillable.ToPointer(models.ExportAuthenticationFlavorNever)
		rwRules = *nillable.ToPointer(models.ExportAuthenticationFlavorNever)
	}

	superUserRule := models.ExportAuthenticationFlavorNone
	if rule.Superuser {
		superUserRule = models.ExportAuthenticationFlavorAny
	}
	var anonUser string
	// When AllSquash is enabled, AnonUid takes precedence (even if it's 0, which is a valid root UID)
	// Validation ensures AnonUid is required when AllSquash is true, so we can trust it's explicitly set
	if utils.IsAllSquashEnabled && rule.AllSquash != nil && *rule.AllSquash {
		if rule.AnonUid != nil {
			anonUser = strconv.FormatInt(*rule.AnonUid, 10)
		}
		roRules = models.ExportAuthenticationFlavorNone
		rwRules = models.ExportAuthenticationFlavorNone
		superUserRule = models.ExportAuthenticationFlavorNone
	} else if rule.AnonymousUser != "" {
		anonUser = rule.AnonymousUser
	}

	// READ_NONE means the client should be denied all access; emit never for
	// both ro and rw regardless of protocol flags or other access settings.
	if rule.AccessType == models.ReadNone {
		roRules = models.ExportAuthenticationFlavorNever
		rwRules = models.ExportAuthenticationFlavorNever
	}

	chownMode := models.ChownModeRestricted
	if rule.ChownMode != "" {
		chownMode = rule.ChownMode
	}

	exportPolicyRule := &ontapRest.ExportRule{
		ClientMatch:      rule.AllowedClients,
		ChownMode:        chownMode,
		ReadOnlyRule:     roRules,
		ReadWriteRule:    rwRules,
		SuperUserRule:    superUserRule,
		Index:            int64(rule.Index),
		NtfsUnixSecurity: models.IgnoreNtfsUnixSecurity,
		Protocols:        protocols,
	}
	if anonUser != "" {
		exportPolicyRule.AnonymousUser = anonUser
	}
	return exportPolicyRule
}

func isDefaultRule(rule *ontaprestmodels.ExportRules) bool {
	// Satisfied default: AUTH_SYS for ro/rw so UNIX UIDs are honored; "none" maps clients to anonymous per ONTAP semantics.
	return len(rule.ExportRulesInlineClients) == 1 && *rule.ExportRulesInlineClients[0].Match == models.AllowedAllClients && int64(*rule.Index) == models.DefaultIndexExportPolicyRule &&
		*rule.ChownMode == models.ChownModeRestricted &&
		len(rule.Protocols) == 1 && *rule.Protocols[0] == utils.GetOntapValue(utils.ProtocolNFS) &&
		len(rule.ExportRulesInlineRoRule) == 1 && *rule.ExportRulesInlineRoRule[0] == ontaprestmodels.ExportAuthenticationFlavorSys &&
		len(rule.ExportRulesInlineRwRule) == 1 && *rule.ExportRulesInlineRwRule[0] == ontaprestmodels.ExportAuthenticationFlavorSys &&
		len(rule.ExportRulesInlineSuperuser) == 1 && *rule.ExportRulesInlineSuperuser[0] == ontaprestmodels.ExportAuthenticationFlavorNone
}

var convertStorageExportPolicyAuthenticationFlavorToONTAP = _convertStorageExportPolicyAuthenticationFlavorToONTAP

func _convertStorageExportPolicyAuthenticationFlavorToONTAP(rule ExportRule) (roRules, rwRules string) {
	if !rule.CIFS && !rule.NFSv3 && !rule.NFSv4 {
		return models.ExportAuthenticationFlavorNever, models.ExportAuthenticationFlavorAny
	}

	// Revert mutual exclusivity of read-only vs read-write
	accessUnixRead := rule.UnixReadOnly || rule.UnixReadWrite
	accessKerberos5Read := rule.Kerberos5ReadOnly || rule.Kerberos5ReadWrite
	accessKerberos5iRead := rule.Kerberos5iReadOnly || rule.Kerberos5iReadWrite
	accessKerberos5pRead := rule.Kerberos5pReadOnly || rule.Kerberos5pReadWrite

	return convertStorageExportPolicyAuthenticationFlavorAccessToONTAP(accessUnixRead, accessKerberos5Read, accessKerberos5iRead, accessKerberos5pRead),
		convertStorageExportPolicyAuthenticationFlavorAccessToONTAP(rule.UnixReadWrite, rule.Kerberos5ReadWrite, rule.Kerberos5iReadWrite, rule.Kerberos5pReadWrite)
}

var convertStorageExportPolicyAuthenticationFlavorAccessToONTAP = _convertStorageExportPolicyAuthenticationFlavorAccessToONTAP

func _convertStorageExportPolicyAuthenticationFlavorAccessToONTAP(unix, kerberos5, kerberos5i, kerberos5p bool) (rules string) {
	if !unix && !kerberos5 && !kerberos5i && !kerberos5p {
		return models.ExportAuthenticationFlavorNever
	}
	combinedRules := make([]string, 0)
	if unix {
		combinedRules = append(combinedRules, models.ExportAuthenticationFlavorSys)
	}
	if kerberos5 {
		combinedRules = append(combinedRules, models.ExportAuthenticationFlavorKrb5)
	}
	if kerberos5i {
		combinedRules = append(combinedRules, models.ExportAuthenticationFlavorKrb5i)
	}
	if kerberos5p {
		combinedRules = append(combinedRules, models.ExportAuthenticationFlavorKrb5p)
	}

	return strings.Join(combinedRules, ",")
}

// ExportPolicyEnsureDefault ensures default export policy
func (rc *OntapRestProvider) ExportPolicyEnsureDefault(svmName string) error {
	client, _ := getOntapClientFunc(rc.ClientParams)
	defaultExportPolicyRuleName := models.DefaultExportPolicyName
	resp, err := client.NAS().ExportPolicyGet(&ontapRest.ExportPolicyGetParams{
		Name:    &defaultExportPolicyRuleName,
		SvmName: &svmName,
	})
	if err != nil {
		return err
	}
	if resp == nil {
		return errors.NewNotFoundErr("Export policy", &defaultExportPolicyRuleName)
	}
	if len(resp.ExportPolicyInlineRules) == 1 &&
		isDefaultRule(resp.ExportPolicyInlineRules[0]) {
		return nil
	}

	modifyrules := make([]*ontapRest.ExportRule, 0)
	modifyrule := &ontapRest.ExportRule{
		Index:         models.DefaultIndexExportPolicyRule,
		ChownMode:     models.ChownModeRestricted,
		Protocols:     []string{utils.GetOntapValue(utils.ProtocolNFS)},
		ClientMatch:   models.AllowedAllClients,
		ReadOnlyRule:  models.ExportAuthenticationFlavorSys,
		ReadWriteRule: models.ExportAuthenticationFlavorSys,
		SuperUserRule: models.NoneAccessProtocol,
		AnonymousUser: models.RootAnonymousUser,
	}
	modifyrules = append(modifyrules, modifyrule)

	err = client.NAS().ExportPolicyModify(&ontapRest.ExportPolicyModifyParams{
		SvmName: svmName,
		ID:      *resp.ID,
		Rules:   modifyrules,
	})
	return err
}

func (rc *OntapRestProvider) CreateExportPolicy(params *ExportPolicy) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return fmt.Errorf("failed to get ONTAP client: %w", err)
	}
	ontapExportRules := make([]*ontapRest.ExportRule, 0)
	for _, rule := range params.ExportRules {
		ontapExportRule := convertStorageExportPolicyRuleToONTAP(*rule)
		rc.Logger.Info("Creating export policy rule", "vsaExportRule", *rule, "ontapExportRule", ontapExportRule)
		ontapExportRules = append(ontapExportRules, ontapExportRule)
	}
	err = rc.ExportPolicyEnsureDefault(params.SvmName)
	if err != nil {
		return fmt.Errorf("failed to ensure default export policy: %w", err)
	}
	_, err = client.NAS().ExportPolicyCreate(&ontapRest.ExportPolicyCreateParams{
		Name:    params.ExportPolicyName,
		SvmName: params.SvmName,
		Rules:   ontapExportRules,
	})
	if err != nil {
		return err
	}
	return nil
}

// UpdateExportPolicyRules updates the export policy rules for a volume
func (rc *OntapRestProvider) UpdateExportPolicyRules(params UpdateExportPolicyRulesParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return fmt.Errorf("failed to get ONTAP client: %w", err)
	}

	// Convert VSA export rules to ONTAP export rules
	ontapExportRules := make([]*ontapRest.ExportRule, 0)
	for _, rule := range params.ExportPolicy.ExportRules {
		ontapExportRule := convertStorageExportPolicyRuleToONTAP(*rule)
		ontapExportRules = append(ontapExportRules, ontapExportRule)
	}

	targetPolicy, err := client.NAS().ExportPolicyGet(&ontapRest.ExportPolicyGetParams{
		Name:    &params.ExportPolicy.ExportPolicyName,
		SvmName: &params.SvmName,
	})
	if err != nil {
		return fmt.Errorf("failed to get export policy %s: %w", params.ExportPolicy.ExportPolicyName, err)
	}
	if targetPolicy != nil {
		// Policy exists, update its rules
		err = client.NAS().ExportPolicyModify(&ontapRest.ExportPolicyModifyParams{
			SvmName: params.SvmName,
			ID:      *targetPolicy.ID,
			Rules:   ontapExportRules,
		})
		if err != nil {
			return fmt.Errorf("failed to update export policy %s rules: %w", params.ExportPolicy.ExportPolicyName, err)
		}
	}
	return nil
}

// GetExportPolicy fetches an export policy and its rules from ONTAP.
func (rc *OntapRestProvider) GetExportPolicy(policyName, svmName string) (*ExportPolicy, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, fmt.Errorf("failed to get ONTAP client: %w", err)
	}

	exportPolicy, err := client.NAS().ExportPolicyGet(&ontapRest.ExportPolicyGetParams{
		Name:    &policyName,
		SvmName: &svmName,
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"name", "rules"},
		},
	})
	if err != nil {
		return nil, err
	}
	if exportPolicy == nil {
		return nil, errors.NewNotFoundErr("Export policy", &policyName)
	}

	rules := make([]*ExportRule, 0, len(exportPolicy.ExportPolicyInlineRules))
	for _, ontapRule := range exportPolicy.ExportPolicyInlineRules {
		if ontapRule == nil {
			continue
		}
		rules = append(rules, convertONTAPExportRuleToVSA(ontapRule))
	}

	return &ExportPolicy{
		ExportPolicyName: policyName,
		SvmName:          svmName,
		ExportRules:      rules,
	}, nil
}

func convertONTAPExportRuleToVSA(rule *ontaprestmodels.ExportRules) *ExportRule {
	vsaRule := &ExportRule{}

	if rule.ChownMode != nil {
		vsaRule.ChownMode = *rule.ChownMode
	}
	if rule.Index != nil {
		vsaRule.Index = int(*rule.Index)
	}

	vsaRule.AllowedClients = clientMatchesFromONTAP(rule.ExportRulesInlineClients)

	roFlavors := authFlavorsFromONTAP(rule.ExportRulesInlineRoRule)
	rwFlavors := authFlavorsFromONTAP(rule.ExportRulesInlineRwRule)
	superFlavors := authFlavorsFromONTAP(rule.ExportRulesInlineSuperuser)

	for _, protocol := range rule.Protocols {
		if protocol == nil {
			continue
		}
		switch strings.ToLower(*protocol) {
		case utils.GetOntapValue(utils.ProtocolNFSv3), "nfs":
			vsaRule.NFSv3 = true
		case utils.GetOntapValue(utils.ProtocolNFSv4):
			vsaRule.NFSv4 = true
		case utils.GetOntapValue(utils.ProtocolSMB):
			vsaRule.CIFS = true
		}
	}

	if utils.IsAllSquashEnabled && allAuthFlavorsAreNone(roFlavors, rwFlavors, superFlavors) {
		allSquash := true
		vsaRule.AllSquash = &allSquash
		// ONTAP allSquash rules use auth flavor "none"; API still requires accessType (READ_WRITE).
		vsaRule.AccessType = models.ReadWrite
		if rule.AnonymousUser != nil && *rule.AnonymousUser != "" {
			if anonUID, err := strconv.ParseInt(*rule.AnonymousUser, 10, 64); err == nil {
				vsaRule.AnonUid = &anonUID
			} else {
				vsaRule.AnonymousUser = *rule.AnonymousUser
			}
		}
		return vsaRule
	}

	if rule.AnonymousUser != nil {
		vsaRule.AnonymousUser = *rule.AnonymousUser
	}

	vsaRule.Superuser = containsAuthFlavor(superFlavors, models.ExportAuthenticationFlavorAny)
	populateAccessFlagsFromONTAP(vsaRule, roFlavors, rwFlavors)
	return vsaRule
}

func clientMatchesFromONTAP(clients []*ontaprestmodels.ExportClients) string {
	if len(clients) == 0 {
		return ""
	}
	matches := make([]string, 0, len(clients))
	for _, client := range clients {
		if client != nil && client.Match != nil {
			matches = append(matches, *client.Match)
		}
	}
	return strings.Join(matches, ",")
}

func authFlavorsFromONTAP(flavors []*ontaprestmodels.ExportAuthenticationFlavor) []string {
	out := make([]string, 0, len(flavors))
	for _, flavor := range flavors {
		if flavor != nil {
			out = append(out, string(*flavor))
		}
	}
	return out
}

func allAuthFlavorsAreNone(roFlavors, rwFlavors, superFlavors []string) bool {
	return onlyAuthFlavor(roFlavors, models.ExportAuthenticationFlavorNone) &&
		onlyAuthFlavor(rwFlavors, models.ExportAuthenticationFlavorNone) &&
		onlyAuthFlavor(superFlavors, models.ExportAuthenticationFlavorNone)
}

func onlyAuthFlavor(flavors []string, want string) bool {
	if len(flavors) == 0 {
		return false
	}
	for _, flavor := range flavors {
		if !strings.EqualFold(flavor, want) {
			return false
		}
	}
	return true
}

func containsAuthFlavor(flavors []string, want string) bool {
	for _, flavor := range flavors {
		if strings.EqualFold(flavor, want) {
			return true
		}
	}
	return false
}

func onlyNeverAuthFlavors(flavors []string) bool {
	return onlyAuthFlavor(flavors, models.ExportAuthenticationFlavorNever)
}

func populateAccessFlagsFromONTAP(rule *ExportRule, roFlavors, rwFlavors []string) {
	if onlyNeverAuthFlavors(roFlavors) && onlyNeverAuthFlavors(rwFlavors) {
		rule.AccessType = models.ReadNone
		return
	}

	rule.UnixReadOnly = containsAuthFlavor(roFlavors, models.ExportAuthenticationFlavorSys) && onlyNeverAuthFlavors(rwFlavors)
	rule.UnixReadWrite = containsAuthFlavor(rwFlavors, models.ExportAuthenticationFlavorSys)
	rule.Kerberos5ReadOnly = containsAuthFlavor(roFlavors, models.ExportAuthenticationFlavorKrb5)
	rule.Kerberos5ReadWrite = containsAuthFlavor(rwFlavors, models.ExportAuthenticationFlavorKrb5)
	rule.Kerberos5iReadOnly = containsAuthFlavor(roFlavors, models.ExportAuthenticationFlavorKrb5i)
	rule.Kerberos5iReadWrite = containsAuthFlavor(rwFlavors, models.ExportAuthenticationFlavorKrb5i)
	rule.Kerberos5pReadOnly = containsAuthFlavor(roFlavors, models.ExportAuthenticationFlavorKrb5p)
	rule.Kerberos5pReadWrite = containsAuthFlavor(rwFlavors, models.ExportAuthenticationFlavorKrb5p)

	if rule.UnixReadWrite || rule.Kerberos5ReadWrite || rule.Kerberos5iReadWrite || rule.Kerberos5pReadWrite {
		rule.AccessType = models.ReadWrite
	} else if rule.UnixReadOnly || rule.Kerberos5ReadOnly || rule.Kerberos5iReadOnly || rule.Kerberos5pReadOnly {
		rule.AccessType = models.ReadOnly
	}
}

// GetExportPolicyProtocols fetches the export policy by name and SVM, returning
// the raw ONTAP protocol strings from all rules (e.g. "nfs3", "nfs4", "cifs", "any").
func (rc *OntapRestProvider) GetExportPolicyProtocols(policyName, svmName string) ([]string, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, fmt.Errorf("failed to get ONTAP client: %w", err)
	}
	exportPolicy, err := client.NAS().ExportPolicyGet(&ontapRest.ExportPolicyGetParams{
		Name:    &policyName,
		SvmName: &svmName,
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"rules.protocols"},
		},
	})
	if err != nil {
		return nil, err
	}

	var protocols []string
	for _, rule := range exportPolicy.ExportPolicyInlineRules {
		for _, p := range rule.Protocols {
			if p != nil {
				protocols = append(protocols, *p)
			}
		}
	}
	return protocols, nil
}

func (rc *OntapRestProvider) DeleteExportPolicy(params *ExportPolicy) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return fmt.Errorf("failed to get ONTAP client: %w", err)
	}
	err = client.NAS().ExportPolicyDelete(&ontapRest.ExportPolicyDeleteParams{
		Name:    params.ExportPolicyName,
		SvmName: params.SvmName,
	})
	if err != nil {
		return fmt.Errorf("failed to delete export policy %s: %w", params.ExportPolicyName, err)
	}
	return nil
}
