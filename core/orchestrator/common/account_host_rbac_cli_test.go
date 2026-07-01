package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testTargetRoleName = "gcnv_r_testrole01"
	testAccountUUID    = "414c2518-7ed2-569e-10c6-1df45d4db528"

	// Mirrors gcnvadmin_create_cli RBAC template used in production bucket files.
	sampleAccountHostRBACCLI = `security login role create -role template_role -cmddirname "DEFAULT" -access all
security login role create -role template_role -cmddirname "debug" -access none
security login role create -role template_role -cmddirname "job expunge" -access all -query "-vserver gcnv-*-svm-*"
security login role create -role template_role -cmddirname "event notification" -access all -query "-access-control-role template_role"
security login role create -role template_role -cmddirname "event notification destination" -access all -query "-access-control-role template_role"
security login role create -role template_role -cmddirname "event filter update-access-control-role" -access none
security login role create -role template_role -cmddirname "job schedule show-jobs" -access all -query "-owner !- -owner !gcnv-*"
security login role create -role template_role -cmddirname "network fcp adapter show" -query "-node !gcnv-*" -access readonly
security login role create -role template_role -cmddirname "vserver peer modify-local-name" -access all -query "-new-name gcnv-*-svm-*"
security login role create -role template_role -cmddirname "vserver peer transition" -access all -query "-local-vserver gcnv-*-svm-*"
security login role create -role template_role -cmddirname "network interface show" -access readonly -query "-role data|cluster_mgmt|intercluster"
security login role create -role template_role -cmddirname "volume create" -access all -query "-vserver gcnv-*-svm-*"
`
)

func testAccountHostSvmPattern(t *testing.T) string {
	t.Helper()
	return BuildAccountHostSvmPattern(testAccountUUID)
}

func TestParseAccountHostRBACPrivilegesFromConfig_CLI_SampleLine(t *testing.T) {
	cli := `security login role create -role template_role -cmddirname "job expunge" -access all -query "-vserver ` + AccountHostRBACCLIQueryTemplate + `"`
	privileges, err := ParseAccountHostRBACPrivilegesFromConfig(cli, "test")
	require.NoError(t, err)
	require.Len(t, privileges, 1)
	assert.Equal(t, ExpertModeRBACPrivilege{
		Path:   "job expunge",
		Access: "all",
		Query:  "-vserver " + AccountHostRBACCLIQueryTemplate,
	}, privileges[0])
}

func TestParseAccountHostRBACPrivilegesFromConfig_CLI_QueryBeforeAccess(t *testing.T) {
	cli := `security login role create -role template_role -cmddirname "network fcp adapter show" -query "-node !gcnv-*" -access readonly`
	privileges, err := ParseAccountHostRBACPrivilegesFromConfig(cli, "test")
	require.NoError(t, err)
	require.Len(t, privileges, 1)
	assert.Equal(t, "network fcp adapter show", privileges[0].Path)
	assert.Equal(t, "readonly", privileges[0].Access)
	assert.Equal(t, "-node !gcnv-*", privileges[0].Query)
}

func TestParseAccountHostRBACPrivilegesFromConfig_CLI_SampleFile(t *testing.T) {
	privileges, err := parseSampleAccountHostRBACCLIFile(t)
	require.NoError(t, err)
	require.Len(t, privileges, len(sampleAccountHostRBACCLIParseCases()))

	for i, want := range sampleAccountHostRBACCLIParseCases() {
		assert.Equal(t, want, privileges[i], "privilege index %d path=%q", i, want.Path)
	}
}

func TestApplyAccountHostRBACSubstitutions_SampleFile(t *testing.T) {
	privileges, err := parseSampleAccountHostRBACCLIFile(t)
	require.NoError(t, err)
	svmPattern := testAccountHostSvmPattern(t)

	substituted := ApplyAccountHostRBACSubstitutions(
		privileges,
		AccountHostRBACSubstitutionInput{
			TargetRoleName: testTargetRoleName,
			SvmPattern:     svmPattern,
		},
	)
	require.Len(t, substituted, len(sampleAccountHostRBACCLIExpectedRESTPrivileges(svmPattern)))

	for i, want := range sampleAccountHostRBACCLIExpectedRESTPrivileges(svmPattern) {
		assert.Equal(t, want, substituted[i], "substituted privilege index %d path=%q", i, want.Path)
	}
}

func TestAccountHostRBACCLI_SampleFile_EndToEndForONTRestRoleCreate(t *testing.T) {
	svmPattern := testAccountHostSvmPattern(t)

	privileges, err := ParseAccountHostRBACPrivilegesFromConfig(sampleAccountHostRBACCLI, "sample RBAC CLI fixture")
	require.NoError(t, err)

	restPrivileges := ApplyAccountHostRBACSubstitutions(
		privileges,
		AccountHostRBACSubstitutionInput{
			TargetRoleName: testTargetRoleName,
			SvmPattern:     svmPattern,
		},
	)

	expected := sampleAccountHostRBACCLIExpectedRESTPrivileges(svmPattern)
	require.Len(t, restPrivileges, len(expected))
	for i, want := range expected {
		got := restPrivileges[i]
		assert.Equal(t, want.Path, got.Path, "path mismatch at index %d", i)
		assert.Equal(t, want.Access, got.Access, "access mismatch at index %d path=%q", i, want.Path)
		assert.Equal(t, want.Query, got.Query, "query mismatch at index %d path=%q", i, want.Path)
	}

	for i, p := range restPrivileges {
		if p.Query == "" {
			continue
		}
		assert.NotContains(t, p.Query, AccountHostRBACCLIQueryTemplate, "index %d still has CLI template vserver pattern", i)
		assert.NotContains(t, p.Query, "-access-control-role template_role", "index %d still has template access-control-role", i)
		assert.NotContains(t, p.Query, "-owner !"+AccountHostRBACCLIOwnerQueryTemplate, "index %d still has CLI template owner pattern", i)
		if strings.Contains(p.Query, svmPattern) || strings.Contains(p.Query, testTargetRoleName) {
			continue
		}
		// Queries like -node !gcnv-* intentionally keep non-SVM template tokens.
		assert.NotContains(t, p.Query, AccountHostRBACCLIQueryTemplate)
	}
}

func TestApplyAccountHostRBACSubstitutions_MatchesBuildAccountHostSvmPattern(t *testing.T) {
	accountUUID := testAccountUUID
	wantPattern := BuildAccountHostSvmPattern(accountUUID)
	require.Equal(t, "svm_414c25187ed2569e10c61df45d4db528_*", wantPattern)

	cli := strings.Join([]string{
		`security login role create -role template_role -cmddirname "job expunge" -access all -query "-vserver ` + AccountHostRBACCLIQueryTemplate + `"`,
		`security login role create -role template_role -cmddirname "volume create" -access all -query "-vserver ` + AccountHostRBACCLIQueryTemplate + `"`,
		`security login role create -role template_role -cmddirname "job schedule show-jobs" -access all -query "-owner !- -owner !` + AccountHostRBACCLIOwnerQueryTemplate + `"`,
	}, "\n")

	privileges, err := ParseAccountHostRBACPrivilegesFromConfig(cli, "test")
	require.NoError(t, err)

	substituted := ApplyAccountHostRBACSubstitutions(privileges, AccountHostRBACSubstitutionInput{
		TargetRoleName: "gcnv_r_8ed3a737",
		SvmPattern:     wantPattern,
	})
	require.Len(t, substituted, 3)
	assert.Equal(t, "-vserver "+wantPattern, substituted[0].Query)
	assert.Equal(t, "-vserver "+wantPattern, substituted[1].Query)
	assert.Equal(t, "-owner !- -owner !"+wantPattern, substituted[2].Query)
}

func TestApplyAccountHostRBACSubstitutions_AnyAccessControlRoleName(t *testing.T) {
	svmPattern := testAccountHostSvmPattern(t)
	privileges := []ExpertModeRBACPrivilege{{
		Path:   "event notification",
		Access: "all",
		Query:  "-access-control-role custom_source_role",
	}}
	updated := ApplyAccountHostRBACSubstitutions(privileges, AccountHostRBACSubstitutionInput{
		TargetRoleName: testTargetRoleName,
		SvmPattern:     svmPattern,
	})
	assert.Equal(t, "-access-control-role "+testTargetRoleName, updated[0].Query)
}

func TestApplyAccountHostRBACSubstitutions_GlobalSvmWildcard(t *testing.T) {
	svmPattern := testAccountHostSvmPattern(t)
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "vserver prefix",
			query: "-vserver " + AccountHostRBACCLIQueryTemplate,
			want:  "-vserver " + svmPattern,
		},
		{
			name:  "volume file copy source vserver id",
			query: "-source-vserver-id " + AccountHostRBACCLIQueryTemplate,
			want:  "-source-vserver-id " + svmPattern,
		},
		{
			name:  "volume file copy compound paths",
			query: "-source-paths " + AccountHostRBACCLIQueryTemplate + " -destination-paths " + AccountHostRBACCLIQueryTemplate,
			want:  "-source-paths " + svmPattern + " -destination-paths " + svmPattern,
		},
		{
			name:  "hypothetical future query key",
			query: "-future-scope " + AccountHostRBACCLIQueryTemplate,
			want:  "-future-scope " + svmPattern,
		},
		{
			name:  "node exclusion unchanged",
			query: "-node !gcnv-*",
			want:  "-node !gcnv-*",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyAccountHostRBACSubstitutions(
				[]ExpertModeRBACPrivilege{{Path: "test", Access: "all", Query: tc.query}},
				AccountHostRBACSubstitutionInput{
					TargetRoleName: testTargetRoleName,
					SvmPattern:     svmPattern,
				},
			)
			assert.Equal(t, tc.want, got[0].Query)
		})
	}
}

func TestParseAccountHostRBACPrivilegesFromConfig_AutoDetectsCLI(t *testing.T) {
	cli := strings.Join([]string{
		`security login role create -role template_role -cmddirname "DEFAULT" -access all`,
		`security login role create -role template_role -cmddirname "job expunge" -access all -query "-vserver ` + AccountHostRBACCLIQueryTemplate + `"`,
	}, "\n")
	privileges, err := ParseAccountHostRBACPrivilegesFromConfig(cli, "test")
	require.NoError(t, err)
	require.Len(t, privileges, 2)
	assert.Equal(t, "job expunge", privileges[1].Path)
}

func TestParseAccountHostRBACPrivilegesFromConfig_CLI_InvalidLine(t *testing.T) {
	_, err := ParseAccountHostRBACPrivilegesFromConfig(
		"security login role create -role template_role -cmddirname \"DEFAULT\" -access all\nnot a valid line\n",
		"test",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected security login role create command")
}

func parseSampleAccountHostRBACCLIFile(t *testing.T) ([]ExpertModeRBACPrivilege, error) {
	t.Helper()
	return ParseAccountHostRBACPrivilegesFromConfig(sampleAccountHostRBACCLI, "sample RBAC CLI fixture")
}

func sampleAccountHostRBACCLIParseCases() []ExpertModeRBACPrivilege {
	template := AccountHostRBACCLIQueryTemplate
	return []ExpertModeRBACPrivilege{
		{Path: "DEFAULT", Access: "all"},
		{Path: "debug", Access: "none"},
		{Path: "job expunge", Access: "all", Query: "-vserver " + template},
		{Path: "event notification", Access: "all", Query: "-access-control-role template_role"},
		{Path: "event notification destination", Access: "all", Query: "-access-control-role template_role"},
		{Path: "event filter update-access-control-role", Access: "none"},
		{Path: "job schedule show-jobs", Access: "all", Query: "-owner !- -owner !" + AccountHostRBACCLIOwnerQueryTemplate},
		{Path: "network fcp adapter show", Access: "readonly", Query: "-node !gcnv-*"},
		{Path: "vserver peer modify-local-name", Access: "all", Query: "-new-name " + template},
		{Path: "vserver peer transition", Access: "all", Query: "-local-vserver " + template},
		{Path: "network interface show", Access: "readonly", Query: "-role data|cluster_mgmt|intercluster"},
		{Path: "volume create", Access: "all", Query: "-vserver " + template},
	}
}

func sampleAccountHostRBACCLIExpectedRESTPrivileges(svmPattern string) []ExpertModeRBACPrivilege {
	return []ExpertModeRBACPrivilege{
		{Path: "DEFAULT", Access: "all"},
		{Path: "debug", Access: "none"},
		{Path: "job expunge", Access: "all", Query: "-vserver " + svmPattern},
		{Path: "event notification", Access: "all", Query: "-access-control-role " + testTargetRoleName},
		{Path: "event notification destination", Access: "all", Query: "-access-control-role " + testTargetRoleName},
		{Path: "event filter update-access-control-role", Access: "none"},
		{Path: "job schedule show-jobs", Access: "all", Query: "-owner !- -owner !" + svmPattern},
		{Path: "network fcp adapter show", Access: "readonly", Query: "-node !gcnv-*"},
		{Path: "vserver peer modify-local-name", Access: "all", Query: "-new-name " + svmPattern},
		{Path: "vserver peer transition", Access: "all", Query: "-local-vserver " + svmPattern},
		{Path: "network interface show", Access: "readonly", Query: "-role data|cluster_mgmt|intercluster"},
		{Path: "volume create", Access: "all", Query: "-vserver " + svmPattern},
	}
}
