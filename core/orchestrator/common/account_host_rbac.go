package common

import (
	"fmt"
	"os"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// ExpertModeRBACPrivilege describes one ONTAP RBAC privilege entry.
type ExpertModeRBACPrivilege struct {
	Path   string
	Access string
	Query  string
}

var (
	AccountHostRBACFileSourceBucketName = env.GetString(
		"ACCOUNT_HOST_RBAC_BUCKET_NAME",
		"gcnv-autopush-images-bucket",
	)
	AccountHostRBACFileSourcePath = env.GetString(
		"ACCOUNT_HOST_RBAC_FILE_PATH",
		"GCNV/%s/RBAC/gcnvadmin_create_cli",
	)
	readAccountHostRBACFile = os.ReadFile
)

// ParseAccountHostRBACPrivilegesFromConfig parses account-host RBAC privileges
// from either ONTAP CLI scripts or path|access|query REST rules.
func ParseAccountHostRBACPrivilegesFromConfig(config string, source string) ([]ExpertModeRBACPrivilege, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "account host RBAC source"
	}

	config = strings.TrimSpace(config)
	if config == "" {
		return nil, fmt.Errorf("%s must not be empty", source)
	}

	var (
		privileges []ExpertModeRBACPrivilege
		err        error
	)
	if LooksLikeONTAPRBACCLIFile(config) {
		privileges, err = parseAccountHostRBACPrivilegesFromCLI(config, source)
		if err != nil {
			return nil, err
		}
	} else {
		privileges, err = parseAccountHostRBACPrivileges(config)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", source, err)
		}
	}
	if err := validateAccountHostRBACPrivileges(privileges, source); err != nil {
		return nil, err
	}
	return privileges, nil
}

// ParseAccountHostRBACPrivilegesFromFile reads and parses account-host RBAC privileges from file.
func ParseAccountHostRBACPrivilegesFromFile(filePath string) ([]ExpertModeRBACPrivilege, error) {
	trimmedPath := strings.TrimSpace(filePath)
	if trimmedPath == "" {
		return nil, fmt.Errorf("account host RBAC file path must not be empty")
	}

	data, err := readAccountHostRBACFile(trimmedPath)
	if err != nil {
		return nil, fmt.Errorf("read account host RBAC file %q: %w", trimmedPath, err)
	}
	return ParseAccountHostRBACPrivilegesFromConfig(string(data), fmt.Sprintf("account host RBAC file %q", trimmedPath))
}

// parseAccountHostRBACPrivileges parses a comma-separated list of rules.
// Rule format:
//
//	<path>|<access>[|<query>]
//
// Example:
//
//	DEFAULT|none,volume create|all|-vserver <svm_name>
func parseAccountHostRBACPrivileges(config string) ([]ExpertModeRBACPrivilege, error) {
	var privileges []ExpertModeRBACPrivilege
	rawRules := strings.FieldsFunc(config, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	for i, rawRule := range rawRules {
		rule := strings.TrimSpace(rawRule)
		if rule == "" {
			continue
		}
		parts := strings.SplitN(rule, "|", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("rule %d: expected <path>|<access>[|<query>]", i)
		}
		privilege := ExpertModeRBACPrivilege{
			Path:   strings.TrimSpace(parts[0]),
			Access: strings.TrimSpace(parts[1]),
		}
		if len(parts) == 3 {
			privilege.Query = strings.TrimSpace(parts[2])
		}
		privileges = append(privileges, privilege)
	}
	return privileges, nil
}

// LooksLikeONTAPRBACCLIFile reports whether content is an ONTAP CLI RBAC script
// (security login role create ...) rather than path|access|query REST privilege rules.
func LooksLikeONTAPRBACCLIFile(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), "security login role create")
}

func validateAccountHostRBACPrivileges(privileges []ExpertModeRBACPrivilege, source string) error {
	if len(privileges) == 0 {
		return fmt.Errorf("%s must contain at least one privilege", source)
	}
	for i, privilege := range privileges {
		if privilege.Path == "" {
			return fmt.Errorf("%s[%d].path must not be empty", source, i)
		}
		if privilege.Access == "" {
			return fmt.Errorf("%s[%d].access must not be empty", source, i)
		}
	}
	return nil
}
