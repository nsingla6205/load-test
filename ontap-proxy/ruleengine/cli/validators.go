package cli

import (
	"context"
	"fmt"

	coreapi "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/core-api"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	core "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/coreapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/middleware"
	proxyutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	validateVolumeCreate            = _validateVolumeCreate
	validateVolumeDelete            = _validateVolumeDelete
	validateVolumeUpdate            = _validateVolumeUpdate
	validateVolumeRename            = _validateVolumeRename
	validateVolumeCloneCreate       = _validateVolumeCloneCreate
	validateVolumeCloneSplitStart   = _validateVolumeCloneSplitStart
	validateFlexCacheCreate         = _validateFlexCacheCreate
	validateFlexCacheDelete         = _validateFlexCacheDelete
	validateAggregateShow           = _validateAggregateShow
	submitExpertModeVolumeOperation = core.SubmitExpertModeVolumeOperation
	submitExpertModeVolumeRename    = core.SubmitExpertModeVolumeRename
	submitExpertModeFlexCloneSplit  = core.SubmitExpertModeFlexCloneSplit
)

func _validateAggregateShow(ctx context.Context, _ *CLICommand) (bool, string) {
	cacheKey := cache.GetAuthDataKeyFromContext(ctx)
	if cacheKey == "" {
		return false, "cache key not found in context"
	}

	authData, exists := cache.GetFromAuthDataCache(cacheKey)
	if !exists || authData == nil {
		return false, fmt.Sprintf("auth data not found in cache for key: %s", cacheKey)
	}

	pool := &datamodel.Pool{ServiceLevel: authData.ServiceLevel}
	if !pool.IsDedicatedCapacity() {
		return false, "Aggregate listing not allowed"
	}

	return true, ""
}

func _validateVolumeCreate(ctx context.Context, cmd *CLICommand) (bool, string) {
	return validateVolumeCreationByStyle(ctx, cmd, coreapi.ExpertModeVolumeV1StyleFlexvol)
}

func _validateFlexCacheCreate(ctx context.Context, cmd *CLICommand) (bool, string) {
	return validateVolumeCreationByStyle(ctx, cmd, coreapi.ExpertModeVolumeV1StyleFlexcache)
}

// validateVolumeCreationByStyle validates volume/flexcache create via the core API.
// Called after credential setup; uses auth from context and builds ExpertModeVolumeV1 Create request.
func validateVolumeCreationByStyle(ctx context.Context, cmd *CLICommand, style coreapi.ExpertModeVolumeV1Style) (bool, string) {
	logger := util.GetLogger(ctx)

	cacheKey := cache.GetAuthDataKeyFromContext(ctx)
	if cacheKey == "" {
		return false, "cache key not found in context"
	}
	authData, exists := cache.GetFromAuthDataCache(cacheKey)
	if !exists || authData == nil {
		return false, fmt.Sprintf("auth data not found in cache for key: %s", cacheKey)
	}

	volumeName := cmd.GetArgument("-volume")
	vserverName := cmd.GetArgument("-vserver")
	sizeStr := cmd.GetArgument("-size")

	sizeInBytes := proxyutils.ParseSizeString(sizeStr)
	if sizeInBytes == 0 {
		return false, fmt.Sprintf("%q is an invalid value for argument \"-size\"", sizeStr)
	}

	expertVolumeRequest := &coreapi.ExpertModeVolumeV1{
		ProjectNumber: authData.AccountName,
		PoolUUID:      authData.PoolID,
		Action:        coreapi.ExpertModeVolumeV1ActionCreate,
		VolumeName:    volumeName,
		SizeInBytes:   coreapi.NewOptFloat64(sizeInBytes),
		Style:         style,
		SvmName:       coreapi.NewOptString(vserverName),
	}

	if err := submitExpertModeVolumeOperation(ctx, expertVolumeRequest, "", logger); err != nil {
		return false, err.Error()
	}

	return true, ""
}

func _validateVolumeDelete(ctx context.Context, cmd *CLICommand) (bool, string) {
	return validateVolumeDeletionByStyle(ctx, cmd, coreapi.ExpertModeVolumeV1StyleFlexvol)
}

func _validateFlexCacheDelete(ctx context.Context, cmd *CLICommand) (bool, string) {
	return validateVolumeDeletionByStyle(ctx, cmd, coreapi.ExpertModeVolumeV1StyleFlexcache)
}

// validateVolumeDeletionByStyle validates volume/flexcache delete via the core API.
// Called after credential setup; uses auth from context and builds ExpertModeVolumeV1 Delete request.
// Core resolves volume by name (no VolumeUUID needed).
func validateVolumeDeletionByStyle(ctx context.Context, cmd *CLICommand, style coreapi.ExpertModeVolumeV1Style) (bool, string) {
	logger := util.GetLogger(ctx)

	cacheKey := cache.GetAuthDataKeyFromContext(ctx)
	if cacheKey == "" {
		return false, "cache key not found in context"
	}
	authData, exists := cache.GetFromAuthDataCache(cacheKey)
	if !exists || authData == nil {
		return false, fmt.Sprintf("auth data not found in cache for key: %s", cacheKey)
	}

	volumeName := cmd.GetArgument("-volume")
	vserverName := cmd.GetArgument("-vserver")

	expertVolumeRequest := &coreapi.ExpertModeVolumeV1{
		ProjectNumber: authData.AccountName,
		PoolUUID:      authData.PoolID,
		Action:        coreapi.ExpertModeVolumeV1ActionDelete,
		VolumeName:    volumeName,
		Style:         style,
		SvmName:       coreapi.NewOptString(vserverName),
	}

	jwtToken := middleware.ExtractJWTFromContext(ctx)
	if err := submitExpertModeVolumeOperation(ctx, expertVolumeRequest, jwtToken, logger); err != nil {
		return false, err.Error()
	}

	return true, ""
}

// _validateVolumeUpdate validates volume modify/size when -size or -new-size is present: validates the size and submits
// an Update operation to the core API. If neither is present, the command is allowed without submission.
// Supports both "volume modify -size" and "volume size -new-size" (-size takes precedence when both exist).
func _validateVolumeUpdate(ctx context.Context, cmd *CLICommand) (bool, string) {
	sizeArg := "-new-size"
	sizeStr := cmd.GetArgument("-size")
	if sizeStr != "" {
		sizeArg = "-size"
	} else {
		sizeStr = cmd.GetArgument("-new-size")
	}
	if sizeStr == "" {
		return true, "" // No size change requested, allow without submitting
	}

	logger := util.GetLogger(ctx)
	cacheKey := cache.GetAuthDataKeyFromContext(ctx)
	if cacheKey == "" {
		return false, "cache key not found in context"
	}
	authData, exists := cache.GetFromAuthDataCache(cacheKey)
	if !exists || authData == nil {
		return false, fmt.Sprintf("auth data not found in cache for key: %s", cacheKey)
	}

	sizeInBytes := proxyutils.ParseSizeString(sizeStr)
	if sizeInBytes <= 0 {
		return false, fmt.Sprintf("%q is an invalid value for argument %q", sizeStr, sizeArg)
	}

	volumeName := cmd.GetArgument("-volume")
	vserverName := cmd.GetArgument("-vserver")

	expertVolumeRequest := &coreapi.ExpertModeVolumeV1{
		ProjectNumber: authData.AccountName,
		PoolUUID:      authData.PoolID,
		Action:        coreapi.ExpertModeVolumeV1ActionUpdate,
		VolumeName:    volumeName,
		SizeInBytes:   coreapi.NewOptFloat64(sizeInBytes),
		Style:         coreapi.ExpertModeVolumeV1StyleFlexvol,
		SvmName:       coreapi.NewOptString(vserverName),
	}

	jwtToken := middleware.ExtractJWTFromContext(ctx)
	if err := submitExpertModeVolumeOperation(ctx, expertVolumeRequest, jwtToken, logger); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// _validateVolumeRename validates volume rename via the core API.
func _validateVolumeRename(ctx context.Context, cmd *CLICommand) (bool, string) {
	logger := util.GetLogger(ctx)

	cacheKey := cache.GetAuthDataKeyFromContext(ctx)
	if cacheKey == "" {
		return false, "cache key not found in context"
	}
	authData, exists := cache.GetFromAuthDataCache(cacheKey)
	if !exists || authData == nil {
		return false, fmt.Sprintf("auth data not found in cache for key: %s", cacheKey)
	}

	vserverName := cmd.GetArgument("-vserver")
	volumeName := cmd.GetArgument("-volume")
	newName := cmd.GetArgument("-newname")

	renameRequest := &coreapi.ExpertModeVolumeRenameV1{
		Name:          newName,
		ProjectNumber: authData.AccountName,
		PoolUUID:      authData.PoolID,
		SvmName:       vserverName,
	}
	params := coreapi.V1ExpertModeVolumeRenameParams{
		Name: volumeName,
	}

	jwtToken := middleware.ExtractJWTFromContext(ctx)
	if err := submitExpertModeVolumeRename(ctx, renameRequest, params, jwtToken, logger); err != nil {
		return false, err.Error()
	}

	return true, ""
}

func _validateVolumeCloneCreate(ctx context.Context, cmd *CLICommand) (bool, string) {
	logger := util.GetLogger(ctx)

	cacheKey := cache.GetAuthDataKeyFromContext(ctx)
	if cacheKey == "" {
		return false, "cache key not found in context"
	}
	authData, exists := cache.GetFromAuthDataCache(cacheKey)
	if !exists || authData == nil {
		return false, fmt.Sprintf("auth data not found in cache for key: %s", cacheKey)
	}

	cloneName := cmd.GetArgument("-flexclone")
	vserverName := cmd.GetArgument("-vserver")

	parentVolume := cmd.GetArgument("-parent-volume")
	if parentVolume == "" {
		parentVolume = cmd.GetArgument("-b")
	}
	if parentVolume == "" {
		return false, "Missing required argument: -parent-volume or -b"
	}

	cloneReq := coreapi.ExpertModeVolumeV1Clone{
		IsFlexclone: coreapi.NewOptBool(true),
		ParentVolume: coreapi.NewOptExpertModeVolumeV1CloneParentVolume(
			coreapi.ExpertModeVolumeV1CloneParentVolume{
				Name: coreapi.NewOptString(parentVolume),
			},
		),
	}
	if parentSnapshot := cmd.GetArgument("-parent-snapshot"); parentSnapshot != "" {
		cloneReq.ParentSnapshot = coreapi.NewOptExpertModeVolumeV1CloneParentSnapshot(
			coreapi.ExpertModeVolumeV1CloneParentSnapshot{
				Name: coreapi.NewOptString(parentSnapshot),
			},
		)
	}

	expertVolumeRequest := &coreapi.ExpertModeVolumeV1{
		ProjectNumber: authData.AccountName,
		PoolUUID:      authData.PoolID,
		Action:        coreapi.ExpertModeVolumeV1ActionCreate,
		VolumeName:    cloneName,
		Style:         coreapi.ExpertModeVolumeV1StyleFlexvol,
		SvmName:       coreapi.NewOptString(vserverName),
		Clone:         coreapi.NewOptExpertModeVolumeV1Clone(cloneReq),
	}

	if sizeArg := cmd.GetArgument("-size"); sizeArg != "" {
		sizeInBytes := proxyutils.ParseSizeString(sizeArg)
		if sizeInBytes <= 0 {
			return false, fmt.Sprintf("%q is an invalid value for argument \"-size\"", sizeArg)
		}
		expertVolumeRequest.SizeInBytes = coreapi.NewOptFloat64(sizeInBytes)
	}

	jwtToken := middleware.ExtractJWTFromContext(ctx)
	if err := submitExpertModeVolumeOperation(ctx, expertVolumeRequest, jwtToken, logger); err != nil {
		return false, err.Error()
	}

	return true, ""
}

func _validateVolumeCloneSplitStart(ctx context.Context, cmd *CLICommand) (bool, string) {
	logger := util.GetLogger(ctx)

	cacheKey := cache.GetAuthDataKeyFromContext(ctx)
	if cacheKey == "" {
		return false, "cache key not found in context"
	}
	authData, exists := cache.GetFromAuthDataCache(cacheKey)
	if !exists || authData == nil {
		return false, fmt.Sprintf("auth data not found in cache for key: %s", cacheKey)
	}

	cloneName := cmd.GetArgument("-flexclone")

	if err := submitExpertModeFlexCloneSplit(ctx, "", cloneName, authData.AccountName, authData.PoolID, "", logger); err != nil {
		return false, err.Error()
	}
	return true, ""
}
