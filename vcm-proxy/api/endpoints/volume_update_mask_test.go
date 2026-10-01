package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

func TestParseVolumeUpdateMask(t *testing.T) {
	fields, err := parseVolumeUpdateMask("sizeInBytes,shareName")
	require.NoError(t, err)
	assert.True(t, fields["sizeInBytes"])
	assert.True(t, fields["shareName"])
	assert.Len(t, fields, 2)

	fields, err = parseVolumeUpdateMask("sizeInBytes,snapshotPolicy.enabled")
	require.NoError(t, err)
	assert.True(t, fields["sizeInBytes"])
	assert.True(t, fields["snapshotPolicy"])

	_, err = parseVolumeUpdateMask("size_in_bytes")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported field")

	_, err = parseVolumeUpdateMask("uuid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uuid")

	_, err = parseVolumeUpdateMask("unsupportedField")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported field")

	require.True(t, utilErrors.IsUserInputValidationErr(err))
}

func TestApplyVolumeUpdateMask_FiltersUnmaskedFields(t *testing.T) {
	req := &vcmserver.VolumeUpdateV1{
		SizeInBytes: vcmserver.NewOptNilFloat64(214748364800),
		ShareName:   vcmserver.NewOptNilString("my-share"),
	}

	masked, err := applyVolumeUpdateMask(req, "sizeInBytes")
	require.NoError(t, err)
	assert.True(t, masked.SizeInBytes.IsSet())
	assert.False(t, masked.ShareName.IsSet())
}

func TestApplyVolumeUpdateMask_RejectsMissingOrEmptyMask(t *testing.T) {
	for _, mask := range []string{"", "   ", ","} {
		t.Run(mask, func(t *testing.T) {
			_, err := applyVolumeUpdateMask(testVolumeUpdateRequest(), mask)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "updateMask must include at least one field")
			assert.True(t, utilErrors.IsUserInputValidationErr(err))
		})
	}
}

func TestApplyVolumeUpdateMask_MaskWithoutMatchingBodyField(t *testing.T) {
	masked, err := applyVolumeUpdateMask(&vcmserver.VolumeUpdateV1{
		ShareName: vcmserver.NewOptNilString("my-share"),
	}, "sizeInBytes")
	require.NoError(t, err)
	assert.False(t, hasVolumeUpdateRequestFields(masked))
}
