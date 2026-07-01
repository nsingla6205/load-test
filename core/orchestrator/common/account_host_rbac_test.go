package common

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLooksLikeONTAPRBACCLIFile(t *testing.T) {
	assert.True(t, LooksLikeONTAPRBACCLIFile("security login role create -role gcnvadmin -cmddirname \"DEFAULT\" -access all\n"))
	assert.False(t, LooksLikeONTAPRBACCLIFile("DEFAULT|none,volume|all|-vserver <svm_name>"))
}

func TestParseAccountHostRBACPrivileges_InvalidRuleFormat(t *testing.T) {
	_, err := ParseAccountHostRBACPrivilegesFromConfig("DEFAULT", "test RBAC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected <path>|<access>[|<query>]")
}

func TestParseAccountHostRBACPrivileges_MissingPath(t *testing.T) {
	_, err := ParseAccountHostRBACPrivilegesFromConfig("|all", "test RBAC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".path must not be empty")
}

func TestParseAccountHostRBACPrivileges_SingleFormat(t *testing.T) {
	privileges, err := ParseAccountHostRBACPrivilegesFromConfig(
		`DEFAULT|none,volume create|all|-vserver <svm_name>,vserver show|all|-vserver <svm_name>`,
		"test RBAC",
	)
	require.NoError(t, err)
	require.Len(t, privileges, 3)
	assert.Equal(t, ExpertModeRBACPrivilege{Path: "DEFAULT", Access: "none"}, privileges[0])
	assert.Equal(t, ExpertModeRBACPrivilege{
		Path:   "volume create",
		Access: "all",
		Query:  "-vserver <svm_name>",
	}, privileges[1])
	assert.Equal(t, ExpertModeRBACPrivilege{
		Path:   "vserver show",
		Access: "all",
		Query:  "-vserver <svm_name>",
	}, privileges[2])
}

func TestParseAccountHostRBACPrivileges_EmptyConfig(t *testing.T) {
	_, err := ParseAccountHostRBACPrivilegesFromConfig("", "test RBAC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestParseAccountHostRBACPrivilegesFromFile_Success(t *testing.T) {
	origReadFile := readAccountHostRBACFile
	readAccountHostRBACFile = func(path string) ([]byte, error) {
		require.Equal(t, "/tmp/rbac-privileges.txt", path)
		return []byte("DEFAULT|none\nvolume create|all|-vserver <svm_name>\n"), nil
	}
	t.Cleanup(func() { readAccountHostRBACFile = origReadFile })

	privileges, err := ParseAccountHostRBACPrivilegesFromFile("/tmp/rbac-privileges.txt")
	require.NoError(t, err)
	require.Len(t, privileges, 2)
	assert.Equal(t, "DEFAULT", privileges[0].Path)
	assert.Equal(t, "volume create", privileges[1].Path)
}

func TestParseAccountHostRBACPrivilegesFromFile_EmptyContent(t *testing.T) {
	origReadFile := readAccountHostRBACFile
	readAccountHostRBACFile = func(_ string) ([]byte, error) {
		return []byte("   \n"), nil
	}
	t.Cleanup(func() { readAccountHostRBACFile = origReadFile })

	_, err := ParseAccountHostRBACPrivilegesFromFile("/tmp/rbac-privileges.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestParseAccountHostRBACPrivilegesFromFile_ReadFailure(t *testing.T) {
	origReadFile := readAccountHostRBACFile
	readAccountHostRBACFile = func(_ string) ([]byte, error) {
		return nil, errors.New("not found")
	}
	t.Cleanup(func() { readAccountHostRBACFile = origReadFile })

	_, err := ParseAccountHostRBACPrivilegesFromFile("/tmp/rbac-privileges.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read account host RBAC file")
}
