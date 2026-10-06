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
	blockTrueZonalAutoTieringAndBackup = env.GetBool("BLOCK_TRUE_ZONAL_AUTO_TIERING_AND_BACKUP", false) && !env.GetBool("ENABLE_TRUE_ZONAL_GA", false)
)

func validateUUIDList(uuids []string, fieldName string) string {
	for i, u := range uuids {
		if !uuidRegex.MatchString(u) {
			return fmt.Sprintf("%s.%d in body should match '%s'", fieldName, i, uuidPattern)
		}
	}
	return ""
}
