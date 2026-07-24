package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/jx"
	"github.com/google/uuid"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// volumeResponse represents the volume data in the Operation response
type volumeResponse struct {
	VolumeId           string                `json:"volumeId,omitempty"`
	ResourceId         string                `json:"resourceId,omitempty"`
	Created            time.Time             `json:"created,omitempty"`
	CreationToken      string                `json:"creationToken,omitempty"`
	PoolId             string                `json:"poolId,omitempty"`
	PoolResourceId     string                `json:"poolResourceId,omitempty"`
	Network            string                `json:"network,omitempty"`
	ServiceLevel       string                `json:"serviceLevel,omitempty"`
	UsedBytes          float64               `json:"usedBytes"`
	QuotaInBytes       float64               `json:"quotaInBytes,omitempty"`
	SnapReserve        int64                 `json:"snapReserve"`
	SnapshotDirectory  bool                  `json:"snapshotDirectory"`
	VolumeState        string                `json:"volumeState,omitempty"`
	VolumeStateDetails string                `json:"volumeStateDetails,omitempty"`
	IsDataProtection   bool                  `json:"isDataProtection"`
	SnapshotPolicy     *volumeSnapshotPolicy `json:"snapshotPolicy,omitempty"`
	StorageClass       string                `json:"storageClass,omitempty"`
	ExportPolicy       *volumeExportPolicy   `json:"exportPolicy,omitempty"`
	Protocols          []string              `json:"protocols,omitempty"`
	Labels             map[string]string     `json:"labels,omitempty"`
	KerberosEnabled    bool                  `json:"kerberosEnabled"`
	LdapEnabled        bool                  `json:"ldapEnabled"`
	EncryptionType     string                `json:"encryptionType,omitempty"`
	Description        string                `json:"description,omitempty"`
	Zone               string                `json:"zone,omitempty"`
	LargeCapacity      bool                  `json:"largeCapacity"`
	CloneDetails       *volumeCloneDetails   `json:"cloneDetails,omitempty"`
	RestrictedActions  []string              `json:"restrictedActions,omitempty"`
}

type volumeSnapshotPolicy struct {
	Enabled         bool                  `json:"enabled"`
	HourlySchedule  volumeHourlySchedule  `json:"hourlySchedule"`
	DailySchedule   volumeDailySchedule   `json:"dailySchedule"`
	WeeklySchedule  volumeWeeklySchedule  `json:"weeklySchedule"`
	MonthlySchedule volumeMonthlySchedule `json:"monthlySchedule"`
}

type volumeHourlySchedule struct {
	SnapshotsToKeep float64 `json:"snapshotsToKeep,omitempty"`
	Minute          float64 `json:"minute,omitempty"`
}

type volumeDailySchedule struct {
	SnapshotsToKeep float64 `json:"snapshotsToKeep,omitempty"`
	Hour            float64 `json:"hour,omitempty"`
	Minute          float64 `json:"minute,omitempty"`
}

type volumeWeeklySchedule struct {
	SnapshotsToKeep float64 `json:"snapshotsToKeep,omitempty"`
	Day             string  `json:"day,omitempty"`
	Hour            float64 `json:"hour,omitempty"`
	Minute          float64 `json:"minute,omitempty"`
}

type volumeMonthlySchedule struct {
	SnapshotsToKeep float64 `json:"snapshotsToKeep,omitempty"`
	DaysOfMonth     string  `json:"daysOfMonth,omitempty"`
	Hour            float64 `json:"hour,omitempty"`
	Minute          float64 `json:"minute,omitempty"`
}

type volumeExportPolicy struct {
	Rules []volumeExportRule `json:"rules,omitempty"`
}

type volumeExportRule struct {
	AllowedClients      string `json:"allowedClients,omitempty"`
	HasRootAccess       string `json:"hasRootAccess,omitempty"`
	AccessType          string `json:"accessType,omitempty"`
	Nfsv3               bool   `json:"nfsv3"`
	Nfsv4               bool   `json:"nfsv4"`
	Kerberos5ReadOnly   bool   `json:"kerberos5ReadOnly"`
	Kerberos5ReadWrite  bool   `json:"kerberos5ReadWrite"`
	Kerberos5iReadOnly  bool   `json:"kerberos5iReadOnly"`
	Kerberos5iReadWrite bool   `json:"kerberos5iReadWrite"`
	Kerberos5pReadOnly  bool   `json:"kerberos5pReadOnly"`
	Kerberos5pReadWrite bool   `json:"kerberos5pReadWrite"`
	AllSquash           *bool  `json:"allSquash,omitempty"`
	AnonUid             *int64 `json:"anonUid,omitempty"`
}

type volumeCloneDetails struct {
	ParentVolumeId       string  `json:"parentVolumeId,omitempty"`
	ParentSnapshotId     string  `json:"parentSnapshotId,omitempty"`
	SharedBytes          float64 `json:"sharedBytes"`
	State                string  `json:"state,omitempty"`
	StateDetails         string  `json:"stateDetails,omitempty"`
	SplitCompletePercent *int64  `json:"splitCompletePercent,omitempty"`
}

// V1UpdateVolumeDatabaseValue partially patches raw volume DB columns (state, stateDetails,
// deletedAt, volumeAttributes) for the volume identified by the volumeId path parameter. All
// request body fields are optional; only fields explicitly present are written, the rest of the
// row is left unchanged.
func (h Handler) V1UpdateVolumeDatabaseValue(ctx context.Context, req *oasgenserver.UpdateVolumeDatabaseValueRequestV1, params oasgenserver.V1UpdateVolumeDatabaseValueParams) (oasgenserver.V1UpdateVolumeDatabaseValueRes, error) {
	if req == nil {
		return &oasgenserver.V1UpdateVolumeDatabaseValueBadRequest{
			Code:    400,
			Message: "request body is required",
		}, nil
	}

	patch, err := buildUpdateVolumeDatabaseValuePatch(req)
	if err != nil {
		return &oasgenserver.V1UpdateVolumeDatabaseValueBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}

	volume, err := h.Orchestrator.UpdateVolumeDatabaseValue(ctx, params.VolumeId, patch)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &oasgenserver.V1UpdateVolumeDatabaseValueNotFound{
				Code:    404,
				Message: err.Error(),
			}, nil
		}
		if errors.IsBadRequestErr(err) || errors.IsUserInputValidationErr(err) {
			return &oasgenserver.V1UpdateVolumeDatabaseValueBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		if errors.IsConflictErr(err) {
			return &oasgenserver.V1UpdateVolumeDatabaseValueConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}

		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			if hasCode, httpCode := customErr.GetHttpCode(); hasCode {
				switch httpCode {
				case 400:
					return &oasgenserver.V1UpdateVolumeDatabaseValueBadRequest{
						Code:    400,
						Message: customErr.GetMessage(),
					}, nil
				case 404:
					return &oasgenserver.V1UpdateVolumeDatabaseValueNotFound{
						Code:    404,
						Message: customErr.GetMessage(),
					}, nil
				case 409:
					return &oasgenserver.V1UpdateVolumeDatabaseValueConflict{
						Code:    409,
						Message: customErr.GetMessage(),
					}, nil
				}
			}
		}

		return &oasgenserver.V1UpdateVolumeDatabaseValueInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	resp := &oasgenserver.UpdateVolumeDatabaseValueResponseV1{
		VolumeId:         oasgenserver.NewOptString(volume.UUID),
		State:            oasgenserver.NewOptString(volume.LifeCycleState),
		StateDetails:     oasgenserver.NewOptString(volume.LifeCycleStateDetails),
		IsDataProtection: oasgenserver.NewOptBool(volume.IsDataProtection),
	}
	if volume.DeletedAt != nil {
		var deletedAt oasgenserver.OptNilDateTime
		deletedAt.SetTo(*volume.DeletedAt)
		resp.DeletedAt = deletedAt
	}
	return resp, nil
}

// buildUpdateVolumeDatabaseValuePatch translates the optional PATCH request fields into a
// commonparams.UpdateVolumeDatabaseValueParams, leaving unset fields nil so downstream code only
// updates what was explicitly provided.
func buildUpdateVolumeDatabaseValuePatch(req *oasgenserver.UpdateVolumeDatabaseValueRequestV1) (*commonparams.UpdateVolumeDatabaseValueParams, error) {
	patch := &commonparams.UpdateVolumeDatabaseValueParams{}

	if req.State.IsSet() {
		v := req.State.Value
		patch.State = &v
	}
	if req.StateDetails.IsSet() {
		v := req.StateDetails.Value
		patch.StateDetails = &v
	}
	if req.DeletedAt.IsSet() {
		patch.DeletedAtSet = true
		if !req.DeletedAt.Null {
			v := req.DeletedAt.Value
			patch.DeletedAt = &v
		}
	}

	if req.VolumeAttributes.IsSet() {
		attrsPatch, err := buildVolumeAttributesPatch(req.VolumeAttributes.Value)
		if err != nil {
			return nil, err
		}
		patch.VolumeAttributes = attrsPatch
	}

	return patch, nil
}

// buildVolumeAttributesPatch translates the optional volumeAttributes sub-object into a
// commonparams.UpdateVolumeAttributesPatch.
//
// For now only isDataProtection is supported, since it's the only VolumeAttributes field with
// real update business logic wired up (used by the Atom SO -> VSA migration flow to flip a
// hidden DP/2NDR volume to RW). The rest of VolumeAttributes will be added to the request schema
// once their update semantics are implemented.
func buildVolumeAttributesPatch(reqAttrs oasgenserver.UpdateVolumeDatabaseValueVolumeAttributesV1) (*commonparams.UpdateVolumeAttributesPatch, error) {
	patch := &commonparams.UpdateVolumeAttributesPatch{}

	if reqAttrs.IsDataProtection.IsSet() {
		v := reqAttrs.IsDataProtection.Value
		patch.IsDataProtection = &v
	}

	return patch, nil
}

// V1SplitStartVolume implements the split start volume endpoint
func (h Handler) V1SplitStartVolume(ctx context.Context, params oasgenserver.V1SplitStartVolumeParams) (oasgenserver.V1SplitStartVolumeRes, error) {
	logger := util.GetLogger(ctx)

	region, zone, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &oasgenserver.V1SplitStartVolumeBadRequest{
			Code:    float64(parsingErr.Code),
			Message: parsingErr.Message,
		}, nil
	}

	splitParams := &commonparams.SplitStartVolumeParams{
		AccountName: params.ProjectNumber,
		Region:      region,
		VolumeID:    params.VolumeId,
	}

	volume, _, err := h.Orchestrator.SplitStartVolume(ctx, splitParams)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &oasgenserver.V1SplitStartVolumeNotFound{
				Code:    404,
				Message: err.Error(),
			}, nil
		} else if errors.IsUserInputValidationErr(err) || errors.IsBadRequestErr(err) {
			return &oasgenserver.V1SplitStartVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		} else if errors.IsConflictErr(err) {
			return &oasgenserver.V1SplitStartVolumeConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}

		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			if hasCode, httpCode := customErr.GetHttpCode(); hasCode {
				message := customErr.GetMessage()
				if httpCode == 400 {
					return &oasgenserver.V1SplitStartVolumeBadRequest{
						Code:    400,
						Message: message,
					}, nil
				} else if httpCode == 409 {
					return &oasgenserver.V1SplitStartVolumeConflict{
						Code:    409,
						Message: message,
					}, nil
				}
			}
		}

		logger.Errorf("Failed to start volume split: %v", err)
		return &oasgenserver.V1SplitStartVolumeInternalServerError{Code: 500, Message: err.Error()}, nil
	}

	volResp := convertModelToVolumeResponse(volume)
	if zone != "" {
		volResp.Zone = zone
	} else {
		volResp.Zone = region
	}

	resp, err := encodeVolumeResponse(volResp)
	if err != nil {
		logger.Errorf("Failed to encode volume response: %v", err)
		return &oasgenserver.V1SplitStartVolumeInternalServerError{Code: 500, Message: err.Error()}, err
	}

	operationUUID := uuid.UUID{}.String()
	operationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + params.LocationId + "/operations/" + operationUUID
	if volume.LifeCycleState == datamodel.LifeCycleStateCreating {
		return &oasgenserver.OperationV1{
			Name:     oasgenserver.NewOptString(operationID),
			Response: resp,
			Done:     oasgenserver.NewOptBool(false),
		}, nil
	}
	return &oasgenserver.OperationV1{
		Name:     oasgenserver.NewOptString(operationID),
		Response: resp,
		Done:     oasgenserver.NewOptBool(true),
	}, nil
}

// V1SplitStopVolume implements the synchronous split stop volume endpoint.
//
// Unlike splitStart, this endpoint does NOT spawn a Temporal workflow. The stop
// is issued directly to ONTAP and the VCP database is updated in the same
// request, so the response always carries `done: true` with the post-stop
// volume state embedded under `response`.
func (h Handler) V1SplitStopVolume(ctx context.Context, params oasgenserver.V1SplitStopVolumeParams) (oasgenserver.V1SplitStopVolumeRes, error) {
	logger := util.GetLogger(ctx)

	region, zone, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &oasgenserver.V1SplitStopVolumeBadRequest{
			Code:    float64(parsingErr.Code),
			Message: parsingErr.Message,
		}, nil
	}

	stopParams := &commonparams.SplitStopVolumeParams{
		AccountName: params.ProjectNumber,
		Region:      region,
		VolumeID:    params.VolumeId,
	}

	volume, err := h.Orchestrator.SplitStopVolume(ctx, stopParams)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &oasgenserver.V1SplitStopVolumeNotFound{
				Code:    404,
				Message: err.Error(),
			}, nil
		} else if errors.IsUserInputValidationErr(err) || errors.IsBadRequestErr(err) {
			return &oasgenserver.V1SplitStopVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		} else if errors.IsConflictErr(err) {
			return &oasgenserver.V1SplitStopVolumeConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}

		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			if hasCode, httpCode := customErr.GetHttpCode(); hasCode {
				message := customErr.GetMessage()
				if httpCode == 400 {
					return &oasgenserver.V1SplitStopVolumeBadRequest{
						Code:    400,
						Message: message,
					}, nil
				} else if httpCode == 409 {
					return &oasgenserver.V1SplitStopVolumeConflict{
						Code:    409,
						Message: message,
					}, nil
				}
			}
		}

		logger.Errorf("Failed to stop volume split: %v", err)
		return &oasgenserver.V1SplitStopVolumeInternalServerError{Code: 500, Message: err.Error()}, nil
	}

	volResp := convertModelToVolumeResponse(volume)
	if zone != "" {
		volResp.Zone = zone
	} else {
		volResp.Zone = region
	}

	resp, err := encodeVolumeResponse(volResp)
	if err != nil {
		// Return the typed 500 response with a nil error so the generated server
		// wrapper encodes our structured JSON body. A non-nil error here would
		// short-circuit ogen into its generic NewError path and drop the typed
		// response we just constructed.
		logger.Errorf("Failed to encode volume response: %v", err)
		return &oasgenserver.V1SplitStopVolumeInternalServerError{Code: 500, Message: err.Error()}, nil
	}

	operationUUID := uuid.UUID{}.String()
	operationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + params.LocationId + "/operations/" + operationUUID
	// Stop is synchronous: the work is already done by the time we get here, so
	// done=true is always set. This mirrors the splitStart response shape so
	// callers can use the same Operation_v1 envelope handling for both flows.
	return &oasgenserver.OperationV1{
		Name:     oasgenserver.NewOptString(operationID),
		Response: resp,
		Done:     oasgenserver.NewOptBool(true),
	}, nil
}

// convertModelToVolumeResponse converts core model volume to volume response format
func convertModelToVolumeResponse(volume *coremodels.Volume) *volumeResponse {
	if volume == nil {
		return nil
	}
	resp := &volumeResponse{
		VolumeId:           volume.UUID,
		ResourceId:         volume.DisplayName,
		Created:            volume.CreatedAt,
		CreationToken:      volume.CreationToken,
		PoolId:             volume.PoolID,
		PoolResourceId:     volume.PoolName,
		Network:            volume.VendorSubnetID,
		ServiceLevel:       string(oasgenserver.PoolV1ServiceLevelFLEX),
		UsedBytes:          float64(volume.UsedBytes),
		QuotaInBytes:       float64(volume.QuotaInBytes),
		SnapReserve:        volume.SnapReserve,
		SnapshotDirectory:  volume.SnapshotDirectory,
		VolumeState:        volume.LifeCycleState,
		VolumeStateDetails: volume.LifeCycleStateDetails,
		IsDataProtection:   volume.IsDataProtection,
		StorageClass:       string(oasgenserver.StorageClassV1SOFTWARE),
		Protocols:          volume.ProtocolTypes,
		Labels:             volume.Labels,
		KerberosEnabled:    volume.KerberosEnabled,
		LdapEnabled:        volume.LdapEnabled,
		EncryptionType:     volume.EncryptionType,
		Description:        volume.Description,
		LargeCapacity:      volume.LargeCapacity,
		RestrictedActions:  volume.RestrictedActions,
	}

	if volume.SnapshotPolicy != nil {
		resp.SnapshotPolicy = convertModelToVolumeSnapshotPolicy(volume.SnapshotPolicy)
	}

	if volume.FileProperties != nil && volume.FileProperties.ExportPolicy != nil {
		resp.ExportPolicy = convertModelToVolumeExportPolicy(volume.FileProperties.ExportPolicy)
	}

	if volume.CloneParentInfo != nil {
		splitComplete := volume.CloneParentInfo.SplitCompletePercent != nil &&
			*volume.CloneParentInfo.SplitCompletePercent == 100
		if !splitComplete {
			resp.CloneDetails = convertModelToVolumeCloneDetails(volume.CloneParentInfo, volume.CloneSharedBytes)
		}
	}

	return resp
}

func convertModelToVolumeSnapshotPolicy(pol *coremodels.SnapshotPolicy) *volumeSnapshotPolicy {
	if pol == nil {
		return nil
	}
	result := &volumeSnapshotPolicy{
		Enabled: pol.IsEnabled,
	}
	for _, sc := range pol.Schedules {
		if sc == nil || sc.Schedule == nil {
			continue
		}
		count := float64(sc.Count)
		var minute float64
		if len(sc.Schedule.Minutes) > 0 {
			minute = float64(sc.Schedule.Minutes[0])
		}
		if len(sc.Schedule.DaysOfMonth) > 0 {
			hour := float64(0)
			if len(sc.Schedule.Hours) > 0 {
				hour = float64(sc.Schedule.Hours[0])
			}
			var days []string
			for _, d := range sc.Schedule.DaysOfMonth {
				days = append(days, strconv.Itoa(d))
			}
			result.MonthlySchedule = volumeMonthlySchedule{
				SnapshotsToKeep: count,
				DaysOfMonth:     strings.Join(days, ","),
				Hour:            hour,
				Minute:          minute,
			}
		} else if len(sc.Schedule.DaysOfWeek) > 0 {
			hour := float64(0)
			if len(sc.Schedule.Hours) > 0 {
				hour = float64(sc.Schedule.Hours[0])
			}
			result.WeeklySchedule = volumeWeeklySchedule{
				SnapshotsToKeep: count,
				Day: strings.Join(func() []string {
					var s []string
					for _, d := range sc.Schedule.DaysOfWeek {
						s = append(s, strconv.Itoa(d))
					}
					return s
				}(), ","),
				Hour:   hour,
				Minute: minute,
			}
		} else if len(sc.Schedule.Hours) > 0 {
			result.DailySchedule = volumeDailySchedule{
				SnapshotsToKeep: count,
				Hour:            float64(sc.Schedule.Hours[0]),
				Minute:          minute,
			}
		} else {
			result.HourlySchedule = volumeHourlySchedule{
				SnapshotsToKeep: count,
				Minute:          minute,
			}
		}
	}
	return result
}

func convertModelToVolumeExportPolicy(ep *coremodels.ExportPolicy) *volumeExportPolicy {
	if ep == nil {
		return nil
	}
	rules := make([]volumeExportRule, 0, len(ep.ExportRules))
	for _, rule := range ep.ExportRules {
		if rule == nil {
			continue
		}
		hasRootAccess := "false"
		if rule.Superuser {
			hasRootAccess = "true"
		}
		r := volumeExportRule{
			AllowedClients:      rule.AllowedClients,
			HasRootAccess:       hasRootAccess,
			AccessType:          rule.AccessType,
			Nfsv3:               rule.NFSv3,
			Nfsv4:               rule.NFSv4,
			Kerberos5ReadOnly:   rule.Kerberos5ReadOnly,
			Kerberos5ReadWrite:  rule.Kerberos5ReadWrite,
			Kerberos5iReadOnly:  rule.Kerberos5iReadOnly,
			Kerberos5iReadWrite: rule.Kerberos5iReadWrite,
			Kerberos5pReadOnly:  rule.Kerberos5pReadOnly,
			Kerberos5pReadWrite: rule.Kerberos5pReadWrite,
			AllSquash:           rule.AllSquash,
			AnonUid:             rule.AnonUid,
		}
		rules = append(rules, r)
	}
	return &volumeExportPolicy{Rules: rules}
}

func convertModelToVolumeCloneDetails(cp *coremodels.CloneParentInfo, cloneSharedBytes uint64) *volumeCloneDetails {
	if cp == nil {
		return nil
	}
	cd := &volumeCloneDetails{
		SharedBytes:          float64(cloneSharedBytes),
		SplitCompletePercent: cp.SplitCompletePercent,
	}
	if cp.ParentVolumeId != nil {
		cd.ParentVolumeId = *cp.ParentVolumeId
	}
	if cp.ParentSnapshotId != nil {
		cd.ParentSnapshotId = *cp.ParentSnapshotId
	}
	if cp.State != nil {
		cd.State = *cp.State
	}
	if cp.StateDetails != nil && *cp.StateDetails != "" {
		cd.StateDetails = *cp.StateDetails
	}
	return cd
}

// encodeVolumeResponse encodes a volumeResponse struct to JSON (jx.Raw)
func encodeVolumeResponse(volResp *volumeResponse) (jx.Raw, error) {
	data, err := json.Marshal(volResp)
	if err != nil {
		return nil, err
	}
	return data, nil
}
