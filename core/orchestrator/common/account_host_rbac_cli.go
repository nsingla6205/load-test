package common

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// AccountHostRBACCLIQueryTemplate is the placeholder in GCS RBAC CLI files (gcnvadmin_create_cli).
	// It is replaced at provisioning time with BuildAccountHostSvmPattern(accountUUID), e.g.
	// svm_414c25187ed2569e10c61df45d4db528_* — not used as the runtime SVM pattern itself.
	AccountHostRBACCLIQueryTemplate = "gcnv-*-svm-*"
	// AccountHostRBACCLIOwnerQueryTemplate is the owner-scope placeholder in RBAC CLI query strings.
	AccountHostRBACCLIOwnerQueryTemplate = "gcnv-*"
)

var (
	rbacCLICmddirnameRE        = regexp.MustCompile(`-cmddirname\s+"([^"]+)"`)
	rbacCLIAccessRE            = regexp.MustCompile(`-access\s+(\S+)`)
	rbacCLIQueryRE             = regexp.MustCompile(`-query\s+"([^"]+)"`)
	rbacCLIAccessControlRoleRE = regexp.MustCompile(`-access-control-role\s+\S+`)
)

// AccountHostRBACSubstitutionInput carries template replacements applied to
// privileges parsed from ONTAP CLI RBAC scripts before role provisioning.
type AccountHostRBACSubstitutionInput struct {
	TargetRoleName string
	SvmPattern     string
}

func parseAccountHostRBACPrivilegesFromCLI(config string, source string) ([]ExpertModeRBACPrivilege, error) {
	var privileges []ExpertModeRBACPrivilege
	lines := strings.Split(config, "\n")
	for lineNum, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		privilege, err := parseAccountHostRBACCLILine(line, lineNum+1)
		if err != nil {
			return nil, fmt.Errorf("parse %s line %d: %w", source, lineNum+1, err)
		}
		privileges = append(privileges, privilege)
	}
	return privileges, nil
}

func parseAccountHostRBACCLILine(line string, lineNum int) (ExpertModeRBACPrivilege, error) {
	if !strings.HasPrefix(line, "security login role create") {
		return ExpertModeRBACPrivilege{}, fmt.Errorf("expected security login role create command")
	}

	cmddirnameMatch := rbacCLICmddirnameRE.FindStringSubmatch(line)
	if len(cmddirnameMatch) < 2 {
		return ExpertModeRBACPrivilege{}, fmt.Errorf("missing -cmddirname")
	}
	accessMatch := rbacCLIAccessRE.FindStringSubmatch(line)
	if len(accessMatch) < 2 {
		return ExpertModeRBACPrivilege{}, fmt.Errorf("missing -access")
	}

	privilege := ExpertModeRBACPrivilege{
		Path:   strings.TrimSpace(cmddirnameMatch[1]),
		Access: strings.TrimSpace(accessMatch[1]),
	}
	if queryMatch := rbacCLIQueryRE.FindStringSubmatch(line); len(queryMatch) >= 2 {
		privilege.Query = strings.TrimSpace(queryMatch[1])
	}
	return privilege, nil
}

// ApplyAccountHostRBACSubstitutions rewrites CLI template values for account-host pinning.
func ApplyAccountHostRBACSubstitutions(
	privileges []ExpertModeRBACPrivilege,
	in AccountHostRBACSubstitutionInput,
) []ExpertModeRBACPrivilege {
	if len(privileges) == 0 {
		return privileges
	}
	targetRole := strings.TrimSpace(in.TargetRoleName)
	svmPattern := strings.TrimSpace(in.SvmPattern)

	out := make([]ExpertModeRBACPrivilege, len(privileges))
	for i, p := range privileges {
		out[i] = ExpertModeRBACPrivilege{
			Path:   p.Path,
			Access: p.Access,
			Query:  substituteAccountHostRBACQuery(p.Query, targetRole, svmPattern),
		}
	}
	return out
}

func substituteAccountHostRBACQuery(query, targetRole, svmPattern string) string {
	if query == "" {
		return query
	}
	out := query
	if svmPattern != "" {
		// Replace the GCS CLI template token anywhere in the query (-vserver, -source-paths, etc.).
		out = strings.ReplaceAll(out, AccountHostRBACCLIQueryTemplate, svmPattern)
		// Owner scope uses a shorter template token in shipped CLI files.
		out = strings.ReplaceAll(out, "-owner !"+AccountHostRBACCLIOwnerQueryTemplate, "-owner !"+svmPattern)
	}
	if targetRole != "" {
		out = rbacCLIAccessControlRoleRE.ReplaceAllString(out, "-access-control-role "+targetRole)
	}
	return out
}
