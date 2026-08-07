package common

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

// EffectivePOEfficiencyFactor returns the storage-efficiency multiplier for a host row.
func EffectivePOEfficiencyFactor(host *datamodel.Host) float64 {
	if host == nil || host.EfficiencyFactor <= 0 {
		return datamodel.DefaultHostEfficiencyFactor
	}
	return host.EfficiencyFactor
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
