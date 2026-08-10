package vsa

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
	ontaprestmodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type action int

const (
	add action = iota
	rem
	mod
	tmpScheduleName    = "vsa-tmp"
	tmpSnapMirrorLabel = "vsa-tmp-sml"
)

func (a action) String() string {
	switch a {
	case add:
		return "add"
	case rem:
		return "rem"
	default:
		return "mod"
	}
}

// CreateSnapshot creates a snapshot by calling the ONTAP REST Client
func (rc *OntapRestProvider) CreateSnapshot(params CreateSnapshotParams) (*SnapshotProviderResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrONTAPClientCreationError, err)
	}
	snapshot, job, err := client.Storage().SnapshotCreate(&ontapRest.SnapshotCreateParams{
		VolumeUUID: params.VolumeUUID,
		Name:       params.Name,
		Comment:    nillable.ToPointer(params.Comment),
	})
	if err != nil {
		if errors.IsConflictErr(err) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrCreateSnapshotConflict, err)
		}
		if errors.IsBadRequestErr(err) || strings.Contains(err.Error(), "Snapshots can only be created on read/write") ||
			strings.Contains(err.Error(), "snapshot creation operation not allowed") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSnapshotNotAllowedForVolume, errors.New("snapshot creation operation not allowed for this volume"))
		}
		if strings.Contains(err.Error(), "No space left on device") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSnapshotInsufficientSpace, err)
		}
		if strings.Contains(err.Error(), "Cannot exceed maximum number of snapshots") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSnapshotMaximumLimitExceeded, err)
		}
		if !errors.IsNotFoundErr(err) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.NewNotFoundErr("Volume", nil))
	}

	var uuid string
	if snapshot != nil && snapshot.UUID != nil {
		uuid = *snapshot.UUID
	} else if job != nil {
		uuid = job.ResourceUUID
	}

	// Always poll when ONTAP returns a job. A 202 payload may include a UUID
	// before the snapshot is queryable.
	if err = waitForJobIfNeeded(rc, job); err != nil {
		if strings.Contains(err.Error(), "Snapshots can only be created on read/write") ||
			strings.Contains(err.Error(), "snapshot creation operation not allowed") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSnapshotNotAllowedForVolume, errors.New("snapshot creation operation not allowed for this volume"))
		}
		if strings.Contains(err.Error(), "No space left on device") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSnapshotInsufficientSpace, err)
		}
		if strings.Contains(err.Error(), "Cannot exceed maximum number of snapshots") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSnapshotMaximumLimitExceeded, err)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}

	snapshot, err = client.Storage().SnapshotGet(&ontapRest.SnapshotGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{
			"reclaimable_space",
			"size",
			"logical_size",
		}},
		UUID:       uuid,
		VolumeUUID: params.VolumeUUID,
	})
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	// Validate the Snapshot response to avoid nil pointer dereferences
	if snapshot == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, errors.NewBadRequestErr("invalid Snapshot create response from API: snapshot is nil"))
	}
	if snapshot.Name == nil || snapshot.UUID == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, errors.NewBadRequestErr("invalid Snapshot create response from API: missing required fields"))
	}

	// Return the created SVM
	return &SnapshotProviderResponse{
		ProviderResponse: ProviderResponse{
			Name:         *snapshot.Name,
			ExternalUUID: *snapshot.UUID,
		},
		SizeInBytes:        int64(0),
		LogicalSizeInBytes: *snapshot.LogicalSize,
	}, nil
}

// DeleteSnapshot deletes a snapshot by calling the ONTAP REST Client
func (rc *OntapRestProvider) DeleteSnapshot(snapshotUUID string, volumeUUID string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	sc := client.Storage()
	snapshot, err := sc.SnapshotGet(&ontapRest.SnapshotGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"name", "owners"}},
		UUID:       snapshotUUID,
		VolumeUUID: volumeUUID,
	})
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}

		var volume *ontapRest.Volume
		volume, err = sc.VolumeGet(&ontapRest.VolumeGetParams{
			BaseParams: ontapRest.BaseParams{Fields: []string{"state"}},
			UUID:       volumeUUID,
		})
		if err != nil {
			if errors.IsNotFoundErr(err) {
				return nil
			}
			return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}

		if *volume.State != "online" {
			return vsaerrors.NewVCPError(vsaerrors.ErrVolumeNotOnlineForSnapshotDelete, errors.NewConflictErr("Cannot delete snapshot because volume is not online"))
		}
		rc.Logger.With(log.Fields{
			"snapshotUUID":       snapshotUUID,
			"volumeExternalUUID": volumeUUID,
		}).Warn("Missing snapshot from online volume")
		return nil
	}

	if snapshot.UUID == nil {
		rc.Logger.With(log.Fields{
			"snapshotUUID":       snapshotUUID,
			"volumeExternalUUID": volumeUUID,
		}).Warn("Snapshot not found, it may have already been deleted")
		return nil
	}

	if len(snapshot.Owners) != 0 {
		return vsaerrors.NewVCPError(vsaerrors.ErrDeleteSnapshot, errors.New("Cannot delete a snapshot that is being actively used in a Volume Replication relationship or a file clone split triggered by Snapshot RestoreFiles operation or used as a reference snapshot for a backup"))
	}

	_, accepted, err := client.Storage().SnapshotDelete(&ontapRest.SnapshotDeleteParams{
		UUID:       *snapshot.UUID,
		VolumeUUID: volumeUUID,
	})
	if err != nil {
		rc.Logger.With(log.Fields{
			"snapshotUUID":       snapshotUUID,
			"volumeExternalUUID": volumeUUID,
			"error":              err,
		}).Error("Failed to delete snapshot")
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}

	if accepted != nil {
		if err = client.Poll(accepted.JobUUID); err != nil {
			if !errors.IsNotFoundErr(err) && !strings.Contains(err.Error(), "entry doesn't exist") {
				return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
			}
		}
	}

	return nil
}

// GetSnapshot retrieves a snapshot by calling the ONTAP REST Client
func (rc *OntapRestProvider) GetSnapshot(snapshotUUID string, volumeUUID string) (*SnapshotProviderResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	sc := client.Storage()
	snapshot, err := sc.SnapshotGet(&ontapRest.SnapshotGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"uuid", "version_uuid", "name", "size", "create_time", "snapmirror_label", "provenance_volume", "volume", "svm", "logical_size"}},
		UUID:       snapshotUUID,
		VolumeUUID: volumeUUID,
	})
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}

		// Check if volume exists when snapshot is not found
		var volume *ontapRest.Volume
		volume, err = sc.VolumeGet(&ontapRest.VolumeGetParams{
			BaseParams: ontapRest.BaseParams{Fields: []string{"state"}},
			UUID:       volumeUUID,
		})
		if err != nil {
			if errors.IsNotFoundErr(err) {
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.NewNotFoundErr("Volume", nil))
			}
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}

		if *volume.State != "online" {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.NewNotFoundErr("Snapshot on offline volume", nil))
		}
		rc.Logger.With(log.Fields{
			"snapshotUUID":       snapshotUUID,
			"volumeExternalUUID": volumeUUID,
		}).Warn("Missing snapshot from online volume")
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.NewNotFoundErr("Snapshot", nil))
	}

	// Validate the Snapshot response to avoid nil pointer dereferences
	if snapshot == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapInconsistentResourceError, errors.NewBadRequestErr("invalid Snapshot get response from API: snapshot is nil"))
	}
	if snapshot.Name == nil || snapshot.UUID == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapInconsistentResourceError, errors.NewBadRequestErr("invalid Snapshot get response from API: missing required fields"))
	}

	// Return the retrieved snapshot
	return &SnapshotProviderResponse{
		ProviderResponse: ProviderResponse{
			Name:         *snapshot.Name,
			ExternalUUID: *snapshot.UUID,
		},
		SizeInBytes:        *snapshot.Size,
		LogicalSizeInBytes: *snapshot.LogicalSize,
	}, nil
}

// ListSnapmirrorSnapshots gets the snapmirror snapshots with prefix "snapmirror*" for the specified volume
func (rc *OntapRestProvider) ListSnapmirrorSnapshots(volumeUUID string) ([]*SnapshotListResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	var volume *ontapRest.Volume
	volume, err = client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"state"}},
		UUID:       volumeUUID,
	})
	if err != nil {
		return nil, err
	}

	if *volume.State != "online" {
		return nil, errors.NewConflictErr("Cannot delete snapshot because volume is not online")
	}

	fields := []string{"name", "uuid", "create_time", "volume.uuid"}
	snapshotCollection := make([]*SnapshotListResponse, 0)

	otParams := &ontapRest.SnapshotCollectionGetParams{
		BaseParams: ontapRest.BaseParams{Fields: fields},
		VolumeUUID: volumeUUID,
		Name:       swag.String("snapmirror*"),
	}

	err = client.Storage().SnapshotCollectionGet(otParams, func(snapshots []*ontapRest.Snapshot) error {
		for _, snapshot := range snapshots {
			ss := &SnapshotListResponse{
				ProviderResponse: ProviderResponse{
					Name:         nillable.FromPointer(snapshot.Name),
					ExternalUUID: nillable.FromPointer(snapshot.UUID),
				},
				VolumeExternalUUID: nillable.FromPointer(volume.UUID),
			}
			snapshotCollection = append(snapshotCollection, ss)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snapshotCollection, nil
}

func (rc *OntapRestProvider) GetSnapshots(volumeUUID string) ([]*Snapshot, error) {
	var resultSnapshots []*Snapshot
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	// TODO: CVS fetches "afs-used" attribute of the snapshot and uses it to identify if the snapshot is updated or not.
	//  VSA's ONTAP REST API does not support this attribute. We need to check if this attribute is necessary for VSA's snapshot management.
	ucbf := func(snapshots []*ontapRest.Snapshot) error {
		for _, ss := range snapshots {
			snapshot := &Snapshot{
				Snapshot: ontaprestmodel.Snapshot{
					Name:             ss.Name,
					ProvenanceVolume: ss.ProvenanceVolume,
					Volume:           ss.Volume,
					Svm:              ss.Svm,
					SnapmirrorLabel:  ss.SnapmirrorLabel,
				},
				ExternalUUID:           *ss.UUID,
				ExternalVersionUUID:    *ss.VersionUUID,
				SizeInBytes:            *ss.Size,
				LogicalSizeUsedInBytes: *ss.LogicalSize,
				CreationTime:           ss.CreateTime,
			}

			resultSnapshots = append(resultSnapshots, snapshot)
		}
		return nil
	}

	err = client.Storage().SnapshotCollectionGet(&ontapRest.SnapshotCollectionGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"uuid", "version_uuid", "name", "size", "create_time", "snapmirror_label", "provenance_volume", "volume", "svm", "logical_size"},
		},
		VolumeUUID: volumeUUID,
	}, ucbf)

	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	return resultSnapshots, nil
}

// GetSnapshotsViaCLIAPI gets snapshots using the CLI API endpoint with accurate sizes
// This replaces GetSnapshots to get accurate sizes: size (for SizeInBytes) and afs_used (for LogicalSizeUsedInBytes)
// Following the pattern from ontap-proxy/handlers/ontap_client.go GetVolume method
func (rc *OntapRestProvider) GetSnapshotsViaCLIAPI(ctx context.Context, volumeName string, svmName string) ([]*Snapshot, error) {
	logger := rc.Logger
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}

	host := client.Host()
	if host == "" {
		return nil, fmt.Errorf("REST client host is empty")
	}

	baseURL := fmt.Sprintf("https://%s/api/private/cli/snapshot", host)
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	q := u.Query()
	q.Set("volume", volumeName)
	if svmName != "" {
		q.Set("vserver", svmName)
	}
	// Include all fields we need: size (for SizeInBytes), afs_used (for LogicalSizeUsedInBytes), version_uuid (for ExternalVersionUUID), instance_uuid (for ExternalUUID), volume_uuid (for ProvenanceVolume), snapmirror-label (for SnapmirrorLabel)
	q.Set("fields", "vserver,volume,snapshot,size,create-time,afs-used,version-uuid,instance-uuid,snapmirror-label,volume-uuid")
	u.RawQuery = q.Encode()

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	// Create HTTP client with TLS config
	var tlsConfig *tls.Config
	if rc.ClientParams.CertificateBasedAuthEnabled {
		// Load certificates for certificate-based auth
		rootCA, clientCert, err := ontapRest.GetAPICallCertificate(rc.ClientParams)
		if err != nil {
			return nil, fmt.Errorf("failed to load certificates for CLI API: %w", err)
		}
		tlsConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: rc.ClientParams.InsecureSkipVerify,
			RootCAs:            rootCA,
			Certificates:       []tls.Certificate{clientCert},
		}
	} else {
		// For basic auth, just set up TLS without client certificates
		tlsConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: rc.ClientParams.InsecureSkipVerify,
		}
		// Set basic auth header
		req.SetBasicAuth(env.Admin, string(rc.ClientParams.Password))
	}

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	logger.Infof("Fetching snapshots via CLI API for volume %s, URL: %s", volumeName, u.String())

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("CLI API request failed: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ONTAP CLI API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var cliResponse struct {
		Records []struct {
			Vserver         string `json:"vserver"`
			Volume          string `json:"volume"`
			Snapshot        string `json:"snapshot"`
			Size            int64  `json:"size"`
			CreateTime      string `json:"create_time"`
			InstanceUUID    string `json:"instance_uuid"`
			VersionUUID     string `json:"version_uuid"`
			AfsUsed         int64  `json:"afs_used"`
			SnapmirrorLabel string `json:"snapmirror_label"`
			VolumeUUID      string `json:"volume_uuid"`
		} `json:"records"`
		NumRecords int `json:"num_records"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&cliResponse); err != nil {
		return nil, fmt.Errorf("failed to parse CLI API response: %w", err)
	}

	// Convert to vsa.Snapshot format
	var snapshots []*Snapshot
	for _, record := range cliResponse.Records {
		// Parse create_time
		createTime, err := time.Parse("2006-01-02T15:04:05+00:00", record.CreateTime)
		if err != nil {
			createTime, err = time.Parse(time.RFC3339, record.CreateTime)
			if err != nil {
				logger.Warnf("Failed to parse create_time %s: %v", record.CreateTime, err)
				createTime = time.Now()
			}
		}
		createTimeFmt := strfmt.DateTime(createTime)

		snapshotName := record.Snapshot
		volName := record.Volume
		vserverName := record.Vserver

		// Use instance_uuid as ExternalUUID (or version_uuid if instance_uuid is empty)
		externalUUID := record.InstanceUUID
		if externalUUID == "" {
			externalUUID = record.VersionUUID
		}

		// Build ProvenanceVolume using volume_uuid
		var provenanceVolume *ontaprestmodel.SnapshotInlineProvenanceVolume
		if record.VolumeUUID != "" {
			provenanceUUID := record.VolumeUUID
			provenanceVolume = &ontaprestmodel.SnapshotInlineProvenanceVolume{
				UUID: &provenanceUUID,
			}
		}

		// Build Volume reference
		volumeRef := &ontaprestmodel.SnapshotInlineVolume{
			Name: &volName,
		}

		// Build SVM reference
		svmRef := &ontaprestmodel.SnapshotInlineSvm{
			Name: &vserverName,
		}

		// Build SnapmirrorLabel if present
		var snapmirrorLabel *string
		if record.SnapmirrorLabel != "" {
			snapmirrorLabel = &record.SnapmirrorLabel
		}

		snapshot := &Snapshot{
			Snapshot: ontaprestmodel.Snapshot{
				Name:             &snapshotName,
				ProvenanceVolume: provenanceVolume,
				Volume:           volumeRef,
				Svm:              svmRef,
				SnapmirrorLabel:  snapmirrorLabel,
				CreateTime:       &createTimeFmt,
			},
			ExternalUUID:           externalUUID,
			ExternalVersionUUID:    record.VersionUUID,
			SizeInBytes:            record.Size,
			LogicalSizeUsedInBytes: record.AfsUsed,
			CreationTime:           &createTimeFmt,
		}
		snapshots = append(snapshots, snapshot)
	}

	return snapshots, nil
}

func (rc *OntapRestProvider) CreateSnapshotPolicy(sp *SnapshotPolicy) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	if len(sp.Schedules) == 0 {
		return vsaerrors.NewVCPError(vsaerrors.ErrSnapshotPolicyScheduleRequired, errors.New("must have at least one snapshot policy schedule when creating"))
	}
	if len(sp.Schedules) > 4 {
		return vsaerrors.NewVCPError(vsaerrors.ErrSnapshotPolicyScheduleTooMany, errors.New("too many snapshot policy schedules specified"))
	}

	schedules := make([]*ontapRest.SnapshotPolicySchedule, len(sp.Schedules))
	for i, schedule := range sp.Schedules {
		schedules[i] = &ontapRest.SnapshotPolicySchedule{
			Prefix:          schedule.SnapmirrorLabel,
			Count:           schedule.Count,
			SnapmirrorLabel: schedule.SnapmirrorLabel,
			Name:            schedule.Schedule.Name,
			Months:          schedule.Schedule.Months,
			DaysOfMonth:     schedule.Schedule.DaysOfMonth,
			DaysOfWeek:      schedule.Schedule.DaysOfWeek,
			Hours:           schedule.Schedule.Hours,
			Minutes:         schedule.Schedule.Minutes,
		}
	}
	for i := 0; i < len(schedules); i++ {
		schedules[i].Name = generateNameForSchedule(&Schedule{
			Months:      schedules[i].Months,
			DaysOfMonth: schedules[i].DaysOfMonth,
			DaysOfWeek:  schedules[i].DaysOfWeek,
			Hours:       schedules[i].Hours,
			Minutes:     schedules[i].Minutes,
		})
	}

	err = client.Storage().SnapshotPolicyCreate(&ontapRest.SnapshotPolicyCreateParams{
		Name:      &sp.Name,
		Comment:   &sp.Comment,
		Enabled:   &sp.IsEnabled,
		Schedules: schedules,
	})

	if err != nil {
		if strings.Contains(err.Error(), "not found") && strings.HasPrefix(err.Error(), "Schedule") {
			for _, schedule := range schedules {
				err := client.Cluster().ScheduleCreate(&ontapRest.ScheduleCreateParams{
					Name:        schedule.Name,
					Months:      schedule.Months,
					DaysOfMonth: schedule.DaysOfMonth,
					DaysOfWeek:  schedule.DaysOfWeek,
					Hours:       schedule.Hours,
					Minutes:     schedule.Minutes,
				})
				if err != nil {
					if !strings.Contains(err.Error(), "exists") &&
						!strings.Contains(err.Error(), "duplicate entry") {
						return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
					}
				}
			}

			return client.Storage().SnapshotPolicyCreate(&ontapRest.SnapshotPolicyCreateParams{
				Name:      &sp.Name,
				Comment:   &sp.Comment,
				Enabled:   &sp.IsEnabled,
				Schedules: schedules,
			})
		}

		if !strings.Contains(err.Error(), "exists") {
			return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}
	}
	return nil
}

// DeleteSnapshotPolicy deletes a snapshot policy in ONTAP using the provided snapshot policy name.
func (rc *OntapRestProvider) DeleteSnapshotPolicy(snapshotPolicyName string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	err = client.Storage().SnapshotPolicyDelete(&ontapRest.SnapshotPolicyDeleteParams{
		Name: snapshotPolicyName,
	})
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	return nil
}

func generateNameForSchedule(schedule *Schedule) string {
	// generateNameForSchedule returns a name generated for the specified schedule
	if len(schedule.DaysOfMonth) > 0 {
		daysOfMonth := strings.ReplaceAll(strings.Trim(fmt.Sprintf("%v", schedule.DaysOfMonth), "[]"), " ", "+")
		return fmt.Sprintf("monthly-on-day-%s-%s-%s", daysOfMonth, utils.GenerateMinutePartOfScheduleName(schedule.Minutes), utils.GenerateHourPartOfScheduleName(schedule.Hours))
	}
	if len(schedule.DaysOfWeek) > 0 {
		return fmt.Sprintf("weekly-on-%s-%s-%s", utils.GenerateWeekdayPartOfScheduleName(schedule.DaysOfWeek), utils.GenerateMinutePartOfScheduleName(schedule.Minutes), utils.GenerateHourPartOfScheduleName(schedule.Hours))
	}
	if len(schedule.Hours) > 0 {
		return fmt.Sprintf("daily-%s-%s", utils.GenerateMinutePartOfScheduleName(schedule.Minutes), utils.GenerateHourPartOfScheduleName(schedule.Hours))
	}
	return fmt.Sprintf("hourly-%s-hour", utils.GenerateMinutePartOfScheduleName(schedule.Minutes))
}

// UpdateSnapshotPolicy updates volume snapshot policy
func (rc *OntapRestProvider) UpdateSnapshotPolicy(ctx context.Context, params *UpdateSnapshotPolicyParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}

	return updateSnapshotPolicy(ctx, client, params)
}

var generateSnapshotPolicyScheduleUpdateStrategy = _generateSnapshotPolicyScheduleUpdateStrategy

func _generateSnapshotPolicyScheduleUpdateStrategy(updatingSchedules, currentSchedules []*SnapshotPolicySchedule) []*SnapshotPolicyScheduleUpdate {
	// MD: Ontap snapshot policy update rules -
	//   1) Snapshot policy must have at least one schedule
	//   2) Snapshot policy cannot have more than five schedules
	//   3) Two different schedule names cannot reference the same prefix in the same schedule
	var actions []*SnapshotPolicyScheduleUpdate
	var stats struct {
		add     int
		del     int
		mod     int
		withTmp bool
	}
	var remScheds map[string]SnapshotPolicySchedule
	var visited map[string]struct{}
	var tmpSchedule = SnapshotPolicySchedule{
		Schedule: &Schedule{
			Name:    tmpScheduleName,
			Minutes: []int{0},
		},
		Count:           0,
		SnapmirrorLabel: tmpSnapMirrorLabel,
	}
	for _, currentSchedule := range currentSchedules {
		equalFoundFlag := false
		for _, updatingSchedule := range updatingSchedules {
			if equalSnapshotPolicySchedule(*updatingSchedule, *currentSchedule) {
				if visited == nil {
					visited = make(map[string]struct{})
				}
				visited[updatingSchedule.SnapmirrorLabel] = struct{}{}

				// MD: We can avoid a full rem/add cycle if just the count has changed
				if updatingSchedule.Count != currentSchedule.Count {
					stats.mod++
					actions = append(actions, &SnapshotPolicyScheduleUpdate{
						Action:                 mod,
						SnapshotPolicySchedule: *updatingSchedule,
					})
				}
				equalFoundFlag = true
				break
			}
		}
		if equalFoundFlag {
			continue
		}

		if remScheds == nil {
			remScheds = make(map[string]SnapshotPolicySchedule)
		}

		remScheds[currentSchedule.SnapmirrorLabel] = *currentSchedule
	}

	prepended, mightNeedTmpSchedule := false, false
	for _, updatingSchedule := range updatingSchedules {
		if _, ok := visited[updatingSchedule.SnapmirrorLabel]; ok {
			continue
		}

		if re, ok := remScheds[updatingSchedule.SnapmirrorLabel]; ok {
			// MD: Policy update where type is the same but schedule is changed. Rule 1 and 2.
			stats.del++
			actions = append(actions, &SnapshotPolicyScheduleUpdate{
				Action:                 rem,
				SnapshotPolicySchedule: re,
			})

			stats.add++
			actions = append(actions, &SnapshotPolicyScheduleUpdate{
				Action:                 add,
				SnapshotPolicySchedule: *updatingSchedule,
			})

			mightNeedTmpSchedule = true
			delete(remScheds, updatingSchedule.SnapmirrorLabel)
			continue
		}

		stats.add++
		if !prepended {
			// MD: Must assert at least 1 schedule to be specified at all times. Rule 1.
			prepended = true

			actions = append([]*SnapshotPolicyScheduleUpdate{{
				Action:                 add,
				SnapshotPolicySchedule: *updatingSchedule,
			}}, actions...)
		} else {
			actions = append(actions, &SnapshotPolicyScheduleUpdate{
				Action:                 add,
				SnapshotPolicySchedule: *updatingSchedule,
			})
		}
	}

	for _, remSched := range remScheds {
		stats.del++
		actions = append(actions, &SnapshotPolicyScheduleUpdate{
			Action:                 rem,
			SnapshotPolicySchedule: remSched,
		})
	}

	// If we have a single schedule in the current policy and that single schedule is differing from the updating schedule but prefix is same.
	// Since prefix is same, we cannot add the updating schedule without removing the current schedule first (Rule 3).
	// We need to remove the current schedule, but that would leave us with no schedules in the policy. This will lead to ONTAP rejecting the update (Rule 1).
	// So we need to add a temporary schedule to the policy, then remove the current schedule, and finally add the updating schedule.
	// And then remove the temporary schedule.
	if mightNeedTmpSchedule && len(actions) == 2 && len(currentSchedules) == 1 {
		// MD: The schedule name to prefix relationship is a many-to-one kind, except when in the same policy.
		// We must use a temporary schedule to dance with ONTAP. Rule 3

		stats.withTmp = true
		actions = append([]*SnapshotPolicyScheduleUpdate{{
			Action:                 add,
			SnapshotPolicySchedule: tmpSchedule,
		}}, actions...)

		actions = append(actions, &SnapshotPolicyScheduleUpdate{
			Action:                 rem,
			SnapshotPolicySchedule: tmpSchedule,
		})
	}

	for _, a := range actions {
		if a.SnapshotPolicySchedule.Schedule.Name != tmpScheduleName {
			a.SnapshotPolicySchedule.Schedule.Name = generateNameForSchedule(a.SnapshotPolicySchedule.Schedule)
		}
	}

	return actions
}

func equalSnapshotPolicySchedule(sspc1, sspc2 SnapshotPolicySchedule) bool {
	return equalIntArrays(sspc1.Schedule.Minutes, sspc2.Schedule.Minutes) &&
		equalIntArrays(sspc1.Schedule.Hours, sspc2.Schedule.Hours) &&
		equalIntArrays(sspc1.Schedule.DaysOfWeek, sspc2.Schedule.DaysOfWeek) &&
		equalIntArrays(sspc1.Schedule.DaysOfMonth, sspc2.Schedule.DaysOfMonth) &&
		equalIntArrays(sspc1.Schedule.Months, sspc2.Schedule.Months)
}

func equalIntArrays(arr1, arr2 []int) bool {
	if len(arr1) != len(arr2) {
		return false
	}

	sort.Ints(arr1)
	sort.Ints(arr2)
	for i := 0; i < len(arr1); i++ {
		if arr1[i] != arr2[i] {
			return false
		}
	}

	return true
}

var updateSnapshotPolicy = _updateSnapshotPolicy

func _updateSnapshotPolicy(ctx context.Context, api ontapRest.RESTClient, params *UpdateSnapshotPolicyParams) error {
	sp, err := api.Storage().SnapshotPolicyFind(&ontapRest.SnapshotPolicyFindParams{
		Name: params.UpdatingSnapshotPolicy.Name,
		Fields: []string{
			"uuid",
			"name",
			"copies.schedule.uuid",
			"copies.schedule.name",
			"copies.snapmirror_label",
		},
	})
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}

	if params.UpdatingSnapshotPolicy.IsEnabled != params.CurrentSnapshotPolicy.IsEnabled {
		err := api.Storage().SnapshotPolicyModify(&ontapRest.SnapshotPolicyModifyParams{UUID: *sp.UUID, Enabled: &params.UpdatingSnapshotPolicy.IsEnabled})
		if err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}
	}

	tempSchdUUID := ""
	for _, up := range generateSnapshotPolicyScheduleUpdateStrategy(params.UpdatingSnapshotPolicy.Schedules, params.CurrentSnapshotPolicy.Schedules) {
		switch up.Action {
		case add:
			recordUUID, err := addSnapshotPolicySchedule(ctx, api, *sp.UUID, &up.SnapshotPolicySchedule)
			if err != nil {
				return err
			}
			if up.SnapshotPolicySchedule.Schedule.Name == tmpScheduleName {
				tempSchdUUID = recordUUID
			}
		case rem:
			for _, sched := range sp.SnapshotPolicyInlineCopies {
				if *sched.Schedule.Name == up.SnapshotPolicySchedule.Schedule.Name || *sched.SnapmirrorLabel == up.SnapshotPolicySchedule.SnapmirrorLabel {
					err = removeSnapshotPolicySchedule(ctx, api, *sp.UUID, *sched.Schedule.UUID)
					if err != nil {
						return err
					}
					break
				} else if up.SnapshotPolicySchedule.Schedule.Name == tmpScheduleName {
					err = removeSnapshotPolicySchedule(ctx, api, *sp.UUID, tempSchdUUID)
					if err != nil {
						return err
					}
					break
				}
			}
		default:
			for _, sched := range sp.SnapshotPolicyInlineCopies {
				if *sched.Schedule.Name == up.SnapshotPolicySchedule.Schedule.Name || *sched.SnapmirrorLabel == up.SnapshotPolicySchedule.SnapmirrorLabel {
					err = modifySnapshotPolicySchedule(ctx, api, *sp.UUID, *sched.Schedule.UUID, &up.SnapshotPolicySchedule)
					if err != nil {
						return err
					}
					break
				}
			}
		}
	}

	return nil
}

var addSnapshotPolicySchedule = _addSnapshotPolicySchedule

func _addSnapshotPolicySchedule(ctx context.Context, api ontapRest.RESTClient, policyUUID string, schedule *SnapshotPolicySchedule) (string, error) {
	recordUUID, err := api.Storage().SnapshotPolicyScheduleCreate(&ontapRest.SnapshotPolicyScheduleCreateParams{
		SnapshotPolicyUUID: policyUUID,
		ScheduleName:       schedule.Schedule.Name,
		SnapmirrorLabel:    schedule.SnapmirrorLabel,
		Count:              schedule.Count,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			err := api.Cluster().ScheduleCreate(&ontapRest.ScheduleCreateParams{
				Name:        schedule.Schedule.Name,
				Months:      schedule.Schedule.Months,
				DaysOfMonth: schedule.Schedule.DaysOfMonth,
				DaysOfWeek:  schedule.Schedule.DaysOfWeek,
				Hours:       schedule.Schedule.Hours,
				Minutes:     schedule.Schedule.Minutes,
			})
			if err != nil {
				if !strings.Contains(err.Error(), "exists") &&
					!strings.Contains(err.Error(), "duplicate entry") {
					return "", vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
				}
			}

			return api.Storage().SnapshotPolicyScheduleCreate(&ontapRest.SnapshotPolicyScheduleCreateParams{
				SnapshotPolicyUUID: policyUUID,
				ScheduleName:       schedule.Schedule.Name,
				SnapmirrorLabel:    schedule.SnapmirrorLabel,
				Count:              schedule.Count,
			})
		}

		if !strings.Contains(err.Error(), "exists") {
			return "", vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}
	}

	return recordUUID, nil
}

var modifySnapshotPolicySchedule = _modifySnapshotPolicySchedule

func _modifySnapshotPolicySchedule(ctx context.Context, api ontapRest.RESTClient, policyUUID string, scheduleUUID string, schedule *SnapshotPolicySchedule) error {
	logger := util.GetLogger(ctx)
	err := api.Storage().SnapshotPolicyScheduleModify(&ontapRest.SnapshotPolicyScheduleModifyParams{
		ScheduleUUID:       scheduleUUID,
		SnapshotPolicyUUID: policyUUID,
		SnapmirrorLabel:    schedule.SnapmirrorLabel,
		Count:              int(schedule.Count),
	})
	if err != nil {
		if !strings.Contains(err.Error(), "not found") &&
			!strings.Contains(err.Error(), "does not exist") {
			return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}
		logger.Debugf("Snapshot policy schedule %s not found, hence skipping", scheduleUUID)
	}

	return nil
}

var removeSnapshotPolicySchedule = _removeSnapshotPolicySchedule

func _removeSnapshotPolicySchedule(ctx context.Context, api ontapRest.RESTClient, policyUUID string, scheduleUUID string) error {
	logger := util.GetLogger(ctx)
	err := api.Storage().SnapshotPolicyScheduleDelete(&ontapRest.SnapshotPolicyScheduleDeleteParams{ScheduleUUID: scheduleUUID, SnapshotPolicyUUID: policyUUID})
	if err != nil {
		if !strings.Contains(err.Error(), "not found") &&
			!strings.Contains(err.Error(), "does not exist") {
			return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}
		logger.Debugf("Snapshot policy schedule %s not found, hence skipping", scheduleUUID)
	}

	return nil
}
