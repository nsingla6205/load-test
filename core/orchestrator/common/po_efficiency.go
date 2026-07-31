package common

import (
	"errors"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

// POValidationHostCapacityMsg is returned when a PO pool would exceed host capacity.
const POValidationHostCapacityMsg = "requested pool size exceeds available host capacity"

// EffectivePOEfficiencyFactor returns the storage-efficiency multiplier for a host row.
func EffectivePOEfficiencyFactor(host *datamodel.Host) float64 {
	if host == nil || host.EfficiencyFactor <= 0 {
		return datamodel.DefaultHostEfficiencyFactor
	}
	return host.EfficiencyFactor
}

// ValidatePOHostCapacity rejects create when used + requested exceeds host capacity × efficiency factor.
func ValidatePOHostCapacity(host *datamodel.Host, usedBytes int64, requestedBytes uint64) error {
	if host == nil {
		return errors.New("host is required")
	}
	if host.Capacity <= 0 {
		return errors.New("host capacity is not configured")
	}

	factor := EffectivePOEfficiencyFactor(host)
	effectiveCapacity := int64(float64(host.Capacity) * factor)
	requested := int64(requestedBytes)
	if usedBytes+requested > effectiveCapacity {
		return fmt.Errorf("%s: requested %d bytes, already allocated %d bytes, effective capacity %d bytes",
			POValidationHostCapacityMsg, requested, usedBytes, effectiveCapacity)
	}
	return nil
}

// ValidatePOHostCapacityForUpdate validates host capacity when a PO pool size changes, excluding the current pool size from allocated bytes.
func ValidatePOHostCapacityForUpdate(host *datamodel.Host, allocatedBytes int64, currentPoolBytes int64, newSize uint64) error {
	adjustedUsed := allocatedBytes - currentPoolBytes
	if adjustedUsed < 0 {
		adjustedUsed = 0
	}
	return ValidatePOHostCapacity(host, adjustedUsed, newSize)
}

const POValidationPoolShrinkMsg = "requested pool size is less than effective expert-mode volume usage"

// ValidatePOPoolShrink rejects THICK PO pool shrink when effective expert-mode usage exceeds the requested size.
func ValidatePOPoolShrink(newSize uint64, effectiveUsedBytes int64) error {
	if effectiveUsedBytes > int64(newSize) {
		return fmt.Errorf("%s: requested %d bytes, effective usage %d bytes",
			POValidationPoolShrinkMsg, newSize, effectiveUsedBytes)
	}
	return nil
}
