package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveVCMSvmName(t *testing.T) {
	got, err := DeriveVCMSvmName("gcnv-e29621e4df3c677")
	require.NoError(t, err)
	assert.Equal(t, "gcnv-e29621e4df3c677-svm-01", got)

	_, err = DeriveVCMSvmName("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deployment ID is required")
}
