package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

func TestEffectivePOEfficiencyFactor(t *testing.T) {
	assert.Equal(t, datamodel.DefaultHostEfficiencyFactor, EffectivePOEfficiencyFactor(nil))
	assert.Equal(t, datamodel.DefaultHostEfficiencyFactor, EffectivePOEfficiencyFactor(&datamodel.Host{}))
	assert.Equal(t, 1.8, EffectivePOEfficiencyFactor(&datamodel.Host{EfficiencyFactor: 1.8}))
	assert.Equal(t, datamodel.DefaultHostEfficiencyFactor, EffectivePOEfficiencyFactor(&datamodel.Host{EfficiencyFactor: -1}))
}

func TestValidatePOPoolShrink(t *testing.T) {
	assert.NoError(t, ValidatePOPoolShrink(1000, 500))
	assert.NoError(t, ValidatePOPoolShrink(1000, 1000))
	assert.ErrorContains(t, ValidatePOPoolShrink(500, 501), POValidationPoolShrinkMsg)
}
