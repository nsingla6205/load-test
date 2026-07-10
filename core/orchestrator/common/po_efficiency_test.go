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

func TestValidatePOHostCapacity(t *testing.T) {
	host := &datamodel.Host{Capacity: 1000, EfficiencyFactor: datamodel.DefaultHostEfficiencyFactor}

	assert.NoError(t, ValidatePOHostCapacity(host, 0, 100))
	assert.NoError(t, ValidatePOHostCapacity(host, 300, 1000))
	assert.ErrorContains(t, ValidatePOHostCapacity(host, 401, 1000), POValidationHostCapacityMsg)
	assert.ErrorContains(t, ValidatePOHostCapacity(nil, 0, 1), "host is required")
	assert.ErrorContains(t, ValidatePOHostCapacity(&datamodel.Host{}, 0, 1), "host capacity is not configured")
}
