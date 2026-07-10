package api

import (
	"fmt"
	"regexp"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

const uuidPattern = `^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`

const (
	trueZonalBackupConfigNotSupportedErrorMsg      = "Backup configuration is not supported for volumes in true-zonal pools"
	trueZonalEnableAutoTieringErrorMsg             = "AutoTiering cannot be enabled for true-zonal pools"
	trueZonalVolumeAutoTieringNotSupportedErrorMsg = "Auto-tiering is not supported for volumes in true-zonal pools"
	trueZonalBackupsNotSupportedErrorMsg           = "Backups are not supported for volumes in true-zonal pools"
)

var (
	uuidRegex                          = regexp.MustCompile(uuidPattern)
	blockTrueZonalAutoTieringAndBackup = env.GetBool("BLOCK_TRUE_ZONAL_AUTO_TIERING_AND_BACKUP", false)
)

func DeduplicateSlice(elements []string) []string {
	uniqueElements := make(map[string]bool)
	var deduplicatedElements []string

	for _, element := range elements {
		if _, exists := uniqueElements[element]; !exists {
			uniqueElements[element] = true
			deduplicatedElements = append(deduplicatedElements, element)
		}
	}
	return deduplicatedElements
}

func validateUUIDList(uuids []string, fieldName string) string {
	for i, u := range uuids {
		if !uuidRegex.MatchString(u) {
			return fmt.Sprintf("%s.%d in body should match '%s'", fieldName, i, uuidPattern)
		}
	}
	return ""
}
