package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeriveAccountHostBaseHash_Deterministic(t *testing.T) {
	uuid := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	assert.Equal(t, DeriveAccountHostBaseHash(uuid), DeriveAccountHostBaseHash(uuid))
	assert.Len(t, DeriveAccountHostBaseHash(uuid), accountHostBaseHashLen)
}

func TestBuildAccountHostUsername_DefaultTemplate(t *testing.T) {
	assert.Equal(t, "gcnv_u_a1b2c3d4", BuildAccountHostUsername("a1b2c3d4"))
}

func TestBuildAccountHostRoleName_DefaultTemplate(t *testing.T) {
	assert.Equal(t, "gcnv_r_a1b2c3d4", BuildAccountHostRoleName("a1b2c3d4"))
}

func TestBuildAccountHostRoleAndUser_ShareBaseHash(t *testing.T) {
	baseHash := "a1b2c3d4"
	roleName := BuildAccountHostRoleName(baseHash)
	userName := BuildAccountHostUsername(baseHash)
	assert.Equal(t, baseHash, roleName[len("gcnv_r_"):])
	assert.Equal(t, baseHash, userName[len("gcnv_u_"):])
}

func TestBuildAccountHostSvmPattern(t *testing.T) {
	accountUUID := "414c2518-7ed2-569e-10c6-1df45d4db528"
	assert.Equal(t, "svm_414c25187ed2569e10c61df45d4db528_*", BuildAccountHostSvmPattern(accountUUID))
}
