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
