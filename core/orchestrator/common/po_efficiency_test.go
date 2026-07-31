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

func TestValidatePOHostCapacityForUpdate(t *testing.T) {
	host := &datamodel.Host{Capacity: 1000, EfficiencyFactor: datamodel.DefaultHostEfficiencyFactor}
	// allocated 800 includes current pool 300; grow to 500 => adjusted 500 + 500 = 1000 effective max 1400 OK
	assert.NoError(t, ValidatePOHostCapacityForUpdate(host, 800, 300, 500))
	// grow to 600 => 500+600=1100 > 1400? 1100 < 1400 OK
	assert.NoError(t, ValidatePOHostCapacityForUpdate(host, 800, 300, 600))
	// grow to 900 => 500+900=1400 OK at boundary
	assert.NoError(t, ValidatePOHostCapacityForUpdate(host, 800, 300, 900))
	// grow to 901 => 1401 > 1400 fail
	assert.ErrorContains(t, ValidatePOHostCapacityForUpdate(host, 800, 300, 901), POValidationHostCapacityMsg)
	// allocated less than current pool size clamps adjusted used to zero
	assert.NoError(t, ValidatePOHostCapacityForUpdate(host, 100, 300, 1000))
}

func TestValidatePOPoolShrink(t *testing.T) {
	assert.NoError(t, ValidatePOPoolShrink(1000, 500))
	assert.NoError(t, ValidatePOPoolShrink(1000, 1000))
	assert.ErrorContains(t, ValidatePOPoolShrink(500, 501), POValidationPoolShrinkMsg)
}
