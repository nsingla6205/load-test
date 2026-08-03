package temporal

import (
	"testing"
	"time"
)

func TestGetWorkflowGlobalTimeout_InvalidEnv(t *testing.T) {
	original := WorkflowGlobalTimeoutMinutes
	defer func() { WorkflowGlobalTimeoutMinutes = original }()

	WorkflowGlobalTimeoutMinutes = "invalid"
	got := GetWorkflowGlobalTimeout()
	want := 60 * time.Minute // fallback default when parsing fails
	if got != want {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestGetWorkflowGlobalTimeout_DefaultEnv(t *testing.T) {
	original := WorkflowGlobalTimeoutMinutes
	defer func() { WorkflowGlobalTimeoutMinutes = original }()

	WorkflowGlobalTimeoutMinutes = "60"
	got := GetWorkflowGlobalTimeout()
	want := 60 * time.Minute // default value
	if got != want {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestGetCMEKWorkFlowGlobalTimeout_Default(t *testing.T) {
	t.Setenv("CMEK_WORKFLOW_GLOBAL_TIMEOUT_MINUTES", "")
	got := GetCMEKWorkFlowGlobalTimeout()
	if got != 14*time.Minute {
		t.Errorf("expected default 14m, got %v", got)
	}
}

func TestGetCMEKWorkFlowGlobalTimeout_Invalid(t *testing.T) {
	t.Setenv("CMEK_WORKFLOW_GLOBAL_TIMEOUT_MINUTES", "oops")
	got := GetCMEKWorkFlowGlobalTimeout()
	if got != 14*time.Minute {
		t.Errorf("expected fallback 14m, got %v", got)
	}
}

func TestGetExpertModeSyncWorkflowTimeout_InvalidEnv(t *testing.T) {
	original := ExpertModeSyncWorkflowTimeoutMinutes
	defer func() { ExpertModeSyncWorkflowTimeoutMinutes = original }()

	ExpertModeSyncWorkflowTimeoutMinutes = "invalid"
	got := GetExpertModeSyncWorkflowTimeout()
	want := 10 * time.Minute
	if got != want {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestGetExpertModeSyncWorkflowTimeout_DefaultsToCustomEnv(t *testing.T) {
	original := ExpertModeSyncWorkflowTimeoutMinutes
	defer func() { ExpertModeSyncWorkflowTimeoutMinutes = original }()

	ExpertModeSyncWorkflowTimeoutMinutes = "15"
	got := GetExpertModeSyncWorkflowTimeout()
	want := 15 * time.Minute
	if got != want {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestGetCreatePoolWorkflowTimeout_Defaults(t *testing.T) {
	origLV := CreatePoolWorkflowTimeoutMinutesLV
	defer func() {
		CreatePoolWorkflowTimeoutMinutesLV = origLV
	}()

	CreatePoolWorkflowTimeoutMinutesLV = "150"

	lv := GetCreatePoolWorkflowTimeout(true)
	if lv == nil || *lv != 150*time.Minute {
		t.Fatalf("expected 150m, got %v", lv)
	}
}

func TestGetCreatePoolWorkflowTimeout_InvalidEnvFallsBack(t *testing.T) {
	origLV := CreatePoolWorkflowTimeoutMinutesLV
	defer func() {
		CreatePoolWorkflowTimeoutMinutesLV = origLV
	}()

	CreatePoolWorkflowTimeoutMinutesLV = "invalid"

	lv := GetCreatePoolWorkflowTimeout(true)
	if lv == nil || *lv != 150*time.Minute {
		t.Fatalf("expected fallback 150m, got %v", lv)
	}
}

func TestGetCreatePoolWorkflowTimeout_StandardReturnsTimeout(t *testing.T) {
	origStd := CreatePoolWorkflowTimeoutMinutes
	defer func() { CreatePoolWorkflowTimeoutMinutes = origStd }()

	CreatePoolWorkflowTimeoutMinutes = "150"
	got := GetCreatePoolWorkflowTimeout(false)
	if got == nil || *got != 150*time.Minute {
		t.Fatalf("expected 150m, got %v", got)
	}
}

func TestGetCreatePoolWorkflowTimeout_StandardInvalidEnvFallsBack(t *testing.T) {
	origStd := CreatePoolWorkflowTimeoutMinutes
	defer func() { CreatePoolWorkflowTimeoutMinutes = origStd }()

	CreatePoolWorkflowTimeoutMinutes = "invalid"
	got := GetCreatePoolWorkflowTimeout(false)
	if got == nil || *got != 150*time.Minute {
		t.Fatalf("expected fallback 150m, got %v", got)
	}
}

func TestGetCreatePoolWorkflowRunTimeout_StandardUsesStandardTimeout(t *testing.T) {
	origStd := CreatePoolWorkflowTimeoutMinutes
	defer func() { CreatePoolWorkflowTimeoutMinutes = origStd }()

	CreatePoolWorkflowTimeoutMinutes = "75"
	got := GetCreatePoolWorkflowRunTimeout(false)
	if got == nil || *got != 75*time.Minute {
		t.Fatalf("expected 75m, got %v", got)
	}
}

func TestGetCreatePoolWorkflowRunTimeout_LVUsesLVTimeout(t *testing.T) {
	origStd := CreatePoolWorkflowTimeoutMinutes
	origLV := CreatePoolWorkflowTimeoutMinutesLV
	defer func() {
		CreatePoolWorkflowTimeoutMinutes = origStd
		CreatePoolWorkflowTimeoutMinutesLV = origLV
	}()

	CreatePoolWorkflowTimeoutMinutes = "75"
	CreatePoolWorkflowTimeoutMinutesLV = "30"

	got := GetCreatePoolWorkflowRunTimeout(true)
	if got == nil || *got != 30*time.Minute {
		t.Fatalf("expected 30m, got %v", got)
	}
}

func TestGetUpdatePoolWorkflowTimeout_Defaults(t *testing.T) {
	origLV := UpdatePoolWorkflowTimeoutMinutesLV
	defer func() {
		UpdatePoolWorkflowTimeoutMinutesLV = origLV
	}()

	UpdatePoolWorkflowTimeoutMinutesLV = "150"

	lv := GetUpdatePoolWorkflowTimeout(true)
	if lv == nil || *lv != 150*time.Minute {
		t.Fatalf("expected 150m, got %v", lv)
	}
}

func TestGetUpdatePoolWorkflowTimeout_InvalidEnvFallsBack(t *testing.T) {
	origLV := UpdatePoolWorkflowTimeoutMinutesLV
	defer func() {
		UpdatePoolWorkflowTimeoutMinutesLV = origLV
	}()

	UpdatePoolWorkflowTimeoutMinutesLV = "invalid"

	lv := GetUpdatePoolWorkflowTimeout(true)
	if lv == nil || *lv != 150*time.Minute {
		t.Fatalf("expected fallback 150m, got %v", lv)
	}
}

func TestGetUpdatePoolWorkflowTimeout_StandardReturnsTimeout(t *testing.T) {
	origStd := UpdatePoolWorkflowTimeoutMinutes
	defer func() { UpdatePoolWorkflowTimeoutMinutes = origStd }()

	UpdatePoolWorkflowTimeoutMinutes = "150"
	got := GetUpdatePoolWorkflowTimeout(false)
	if got == nil || *got != 150*time.Minute {
		t.Fatalf("expected 150m, got %v", got)
	}
}

func TestGetUpdatePoolWorkflowTimeout_StandardInvalidEnvFallsBack(t *testing.T) {
	origStd := UpdatePoolWorkflowTimeoutMinutes
	defer func() { UpdatePoolWorkflowTimeoutMinutes = origStd }()

	UpdatePoolWorkflowTimeoutMinutes = "invalid"
	got := GetUpdatePoolWorkflowTimeout(false)
	if got == nil || *got != 150*time.Minute {
		t.Fatalf("expected fallback 150m, got %v", got)
	}
}

func TestGetUpdatePoolWorkflowRunTimeout_StandardUsesStandardTimeout(t *testing.T) {
	origStd := UpdatePoolWorkflowTimeoutMinutes
	defer func() { UpdatePoolWorkflowTimeoutMinutes = origStd }()

	UpdatePoolWorkflowTimeoutMinutes = "75"
	got := GetUpdatePoolWorkflowRunTimeout(false)
	if got == nil || *got != 75*time.Minute {
		t.Fatalf("expected 75m, got %v", got)
	}
}

func TestGetUpdatePoolWorkflowRunTimeout_LVUsesLVTimeout(t *testing.T) {
	origStd := UpdatePoolWorkflowTimeoutMinutes
	origLV := UpdatePoolWorkflowTimeoutMinutesLV
	defer func() {
		UpdatePoolWorkflowTimeoutMinutes = origStd
		UpdatePoolWorkflowTimeoutMinutesLV = origLV
	}()

	UpdatePoolWorkflowTimeoutMinutes = "75"
	UpdatePoolWorkflowTimeoutMinutesLV = "30"

	got := GetUpdatePoolWorkflowRunTimeout(true)
	if got == nil || *got != 30*time.Minute {
		t.Fatalf("expected 30m, got %v", got)
	}
}

func TestGetPoolMaintenanceWorkflowRunTimeout(t *testing.T) {
	orig := PoolMaintenanceWorkflowTimeoutMinutes
	defer func() { PoolMaintenanceWorkflowTimeoutMinutes = orig }()

	tests := []struct {
		name     string
		envVal   string
		expected time.Duration
	}{
		{"uses configured value", "120", 120 * time.Minute},
		{"uses default on invalid env", "invalid", 150 * time.Minute},
		{"uses default on empty string", "", 150 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			PoolMaintenanceWorkflowTimeoutMinutes = tt.envVal
			got := GetPoolMaintenanceWorkflowRunTimeout()
			if got == nil || *got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestGetRotateFabricPoolKeysWorkflowRunTimeout_UsesConfiguredValue(t *testing.T) {
	orig := RotateFabricPoolKeysWorkflowTimeoutMinutes
	defer func() { RotateFabricPoolKeysWorkflowTimeoutMinutes = orig }()

	RotateFabricPoolKeysWorkflowTimeoutMinutes = "10"
	got := GetRotateFabricPoolKeysWorkflowRunTimeout()
	if got == nil || *got != 10*time.Minute {
		t.Fatalf("expected 10m, got %v", got)
	}
}

func TestGetRotateFabricPoolKeysWorkflowRunTimeout_InvalidEnvFallsBack(t *testing.T) {
	orig := RotateFabricPoolKeysWorkflowTimeoutMinutes
	defer func() { RotateFabricPoolKeysWorkflowTimeoutMinutes = orig }()

	RotateFabricPoolKeysWorkflowTimeoutMinutes = "invalid"
	got := GetRotateFabricPoolKeysWorkflowRunTimeout()
	if got == nil || *got != 10*time.Minute {
		t.Fatalf("expected fallback 10m, got %v", got)
	}
}

func TestGetCreateBackupWorkflowTimeout_InvalidEnv(t *testing.T) {
	original := CreateBackupWorkflowTimeoutMinutes
	defer func() { CreateBackupWorkflowTimeoutMinutes = original }()

	CreateBackupWorkflowTimeoutMinutes = "invalid"
	got := GetCreateBackupWorkflowTimeout()
	want := 8640 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetCreateBackupWorkflowTimeout_ValidEnv(t *testing.T) {
	original := CreateBackupWorkflowTimeoutMinutes
	defer func() { CreateBackupWorkflowTimeoutMinutes = original }()

	CreateBackupWorkflowTimeoutMinutes = "4320"
	got := GetCreateBackupWorkflowTimeout()
	want := 4320 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetDeleteBackupWorkflowTimeout_InvalidEnv(t *testing.T) {
	original := DeleteBackupWorkflowTimeoutMinutes
	defer func() { DeleteBackupWorkflowTimeoutMinutes = original }()

	DeleteBackupWorkflowTimeoutMinutes = "invalid"
	got := GetDeleteBackupWorkflowTimeout()
	want := 6480 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetDeleteBackupWorkflowTimeout_ValidEnv(t *testing.T) {
	original := DeleteBackupWorkflowTimeoutMinutes
	defer func() { DeleteBackupWorkflowTimeoutMinutes = original }()

	DeleteBackupWorkflowTimeoutMinutes = "3240"
	got := GetDeleteBackupWorkflowTimeout()
	want := 3240 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetSFRWorkflowTimeout_InvalidEnv(t *testing.T) {
	original := SFRWorkflowTimeoutMinutes
	defer func() { SFRWorkflowTimeoutMinutes = original }()

	SFRWorkflowTimeoutMinutes = "invalid"
	got := GetSFRWorkflowTimeout()
	want := 13680 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetSFRWorkflowTimeout_ValidEnv(t *testing.T) {
	original := SFRWorkflowTimeoutMinutes
	defer func() { SFRWorkflowTimeoutMinutes = original }()

	SFRWorkflowTimeoutMinutes = "6840"
	got := GetSFRWorkflowTimeout()
	want := 6840 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetCreateSnapshotWorkflowTimeout_InvalidEnv(t *testing.T) {
	original := CreateSnapshotWorkflowTimeoutMinutes
	defer func() { CreateSnapshotWorkflowTimeoutMinutes = original }()

	CreateSnapshotWorkflowTimeoutMinutes = "invalid"
	got := GetCreateSnapshotWorkflowTimeout()
	want := 50 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetCreateSnapshotWorkflowTimeout_ValidEnv(t *testing.T) {
	original := CreateSnapshotWorkflowTimeoutMinutes
	defer func() { CreateSnapshotWorkflowTimeoutMinutes = original }()

	CreateSnapshotWorkflowTimeoutMinutes = "30"
	got := GetCreateSnapshotWorkflowTimeout()
	want := 30 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetDeleteSnapshotWorkflowTimeout_InvalidEnv(t *testing.T) {
	original := DeleteSnapshotWorkflowTimeoutMinutes
	defer func() { DeleteSnapshotWorkflowTimeoutMinutes = original }()

	DeleteSnapshotWorkflowTimeoutMinutes = "invalid"
	got := GetDeleteSnapshotWorkflowTimeout()
	want := 65 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetDeleteSnapshotWorkflowTimeout_ValidEnv(t *testing.T) {
	original := DeleteSnapshotWorkflowTimeoutMinutes
	defer func() { DeleteSnapshotWorkflowTimeoutMinutes = original }()

	DeleteSnapshotWorkflowTimeoutMinutes = "40"
	got := GetDeleteSnapshotWorkflowTimeout()
	want := 40 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetRevertVolumeWorkflowTimeout_InvalidEnv(t *testing.T) {
	original := RevertVolumeWorkflowTimeoutMinutes
	defer func() { RevertVolumeWorkflowTimeoutMinutes = original }()

	RevertVolumeWorkflowTimeoutMinutes = "invalid"
	got := GetRevertVolumeWorkflowTimeout()
	want := 95 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetRevertVolumeWorkflowTimeout_ValidEnv(t *testing.T) {
	original := RevertVolumeWorkflowTimeoutMinutes
	defer func() { RevertVolumeWorkflowTimeoutMinutes = original }()

	RevertVolumeWorkflowTimeoutMinutes = "60"
	got := GetRevertVolumeWorkflowTimeout()
	want := 60 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetVolumeRefreshWorkflowTimeout_InvalidEnv(t *testing.T) {
	original := VolumeRefreshWorkflowTimeoutMinutes
	defer func() { VolumeRefreshWorkflowTimeoutMinutes = original }()

	VolumeRefreshWorkflowTimeoutMinutes = "invalid"
	got := GetVolumeRefreshWorkflowTimeout()
	want := 20 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetVolumeRefreshWorkflowTimeout_ValidEnv(t *testing.T) {
	original := VolumeRefreshWorkflowTimeoutMinutes
	defer func() { VolumeRefreshWorkflowTimeoutMinutes = original }()

	VolumeRefreshWorkflowTimeoutMinutes = "30"
	got := GetVolumeRefreshWorkflowTimeout()
	want := 30 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetVolumeRefreshWorkflowTimeout_DefaultEnv(t *testing.T) {
	original := VolumeRefreshWorkflowTimeoutMinutes
	defer func() { VolumeRefreshWorkflowTimeoutMinutes = original }()

	VolumeRefreshWorkflowTimeoutMinutes = "20"
	got := GetVolumeRefreshWorkflowTimeout()
	want := 20 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetSplitVolumeWorkflowTimeout_InvalidEnv(t *testing.T) {
	original := SplitVolumeWorkflowTimeoutMinutes
	defer func() { SplitVolumeWorkflowTimeoutMinutes = original }()

	SplitVolumeWorkflowTimeoutMinutes = "invalid"
	got := GetSplitVolumeWorkflowTimeout()
	want := 120 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetSplitVolumeWorkflowTimeout_ValidEnv(t *testing.T) {
	original := SplitVolumeWorkflowTimeoutMinutes
	defer func() { SplitVolumeWorkflowTimeoutMinutes = original }()

	SplitVolumeWorkflowTimeoutMinutes = "120"
	got := GetSplitVolumeWorkflowTimeout()
	want := 120 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetUpdateBackupScheduleWorkflowTimeout_InvalidEnv(t *testing.T) {
	original := UpdateBackupScheduleWorkflowTimeoutMinutes
	defer func() { UpdateBackupScheduleWorkflowTimeoutMinutes = original }()

	UpdateBackupScheduleWorkflowTimeoutMinutes = "invalid"
	got := GetUpdateBackupScheduleWorkflowTimeout()
	want := 180 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetUpdateBackupScheduleWorkflowTimeout_ValidEnv(t *testing.T) {
	original := UpdateBackupScheduleWorkflowTimeoutMinutes
	defer func() { UpdateBackupScheduleWorkflowTimeoutMinutes = original }()

	UpdateBackupScheduleWorkflowTimeoutMinutes = "90"
	got := GetUpdateBackupScheduleWorkflowTimeout()
	want := 90 * time.Minute
	if got == nil {
		t.Fatal("expected non-nil timeout, got nil")
	}
	if *got != want {
		t.Errorf("expected %v, got %v", want, *got)
	}
}

func TestGetSplitVolumeRunContinueAsNewDuration_InvalidEnv(t *testing.T) {
	original := SplitVolumeRunContinueAsNewMinutes
	defer func() { SplitVolumeRunContinueAsNewMinutes = original }()

	SplitVolumeRunContinueAsNewMinutes = "invalid"
	got := GetSplitVolumeRunContinueAsNewDuration()
	want := 60 * time.Minute
	if got != want {
		t.Errorf("expected fallback %v, got %v", want, got)
	}
}

func TestGetSplitVolumeRunContinueAsNewDuration_ValidEnv(t *testing.T) {
	original := SplitVolumeRunContinueAsNewMinutes
	defer func() { SplitVolumeRunContinueAsNewMinutes = original }()

	SplitVolumeRunContinueAsNewMinutes = "45"
	got := GetSplitVolumeRunContinueAsNewDuration()
	want := 45 * time.Minute
	if got != want {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestGetReverseHybridReplicationPollWorkflowTimeout_InvalidEnv(t *testing.T) {
	original := ReverseHybridReplicationPollWorkflowTimeoutMinutes
	defer func() { ReverseHybridReplicationPollWorkflowTimeoutMinutes = original }()

	ReverseHybridReplicationPollWorkflowTimeoutMinutes = "invalid"
	got := GetReverseHybridReplicationPollWorkflowTimeout()
	want := 150 * time.Minute
	if got != want {
		t.Errorf("expected fallback %v, got %v", want, got)
	}
}

func TestGetReverseHybridReplicationPollWorkflowTimeout_ValidEnv(t *testing.T) {
	original := ReverseHybridReplicationPollWorkflowTimeoutMinutes
	defer func() { ReverseHybridReplicationPollWorkflowTimeoutMinutes = original }()

	ReverseHybridReplicationPollWorkflowTimeoutMinutes = "200"
	got := GetReverseHybridReplicationPollWorkflowTimeout()
	want := 200 * time.Minute
	if got != want {
		t.Errorf("expected %v, got %v", want, got)
	}
}

// ---------------------------------------------------------------------------
// OCI workflow timeout getters
// ---------------------------------------------------------------------------

func TestGetDeletePoolWorkflowRunTimeout(t *testing.T) {
	orig := DeletePoolWorkflowTimeoutMinutes
	defer func() { DeletePoolWorkflowTimeoutMinutes = orig }()

	tests := []struct {
		name     string
		envVal   string
		expected time.Duration
	}{
		{"uses configured value", "90", 90 * time.Minute},
		{"uses default on invalid env", "invalid", 60 * time.Minute},
		{"uses default on empty string", "", 60 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			DeletePoolWorkflowTimeoutMinutes = tt.envVal
			got := GetDeletePoolWorkflowRunTimeout()
			if got == nil || *got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestGetCreateSVMWorkflowRunTimeout(t *testing.T) {
	orig := CreateSVMWorkflowTimeoutMinutes
	defer func() { CreateSVMWorkflowTimeoutMinutes = orig }()

	tests := []struct {
		name     string
		envVal   string
		expected time.Duration
	}{
		{"uses configured value", "45", 45 * time.Minute},
		{"uses default on invalid env", "bad", 60 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			CreateSVMWorkflowTimeoutMinutes = tt.envVal
			got := GetCreateSVMWorkflowRunTimeout()
			if got == nil || *got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestGetDeleteSVMWorkflowRunTimeout(t *testing.T) {
	orig := DeleteSVMWorkflowTimeoutMinutes
	defer func() { DeleteSVMWorkflowTimeoutMinutes = orig }()

	tests := []struct {
		name     string
		envVal   string
		expected time.Duration
	}{
		{"uses configured value", "45", 45 * time.Minute},
		{"uses default on invalid env", "bad", 60 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			DeleteSVMWorkflowTimeoutMinutes = tt.envVal
			got := GetDeleteSVMWorkflowRunTimeout()
			if got == nil || *got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestGetUpdateSVMWorkflowRunTimeout(t *testing.T) {
	orig := UpdateSVMWorkflowTimeoutMinutes
	defer func() { UpdateSVMWorkflowTimeoutMinutes = orig }()

	tests := []struct {
		name     string
		envVal   string
		expected time.Duration
	}{
		{"uses configured value", "20", 20 * time.Minute},
		{"uses default on invalid env", "oops", 30 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			UpdateSVMWorkflowTimeoutMinutes = tt.envVal
			got := GetUpdateSVMWorkflowRunTimeout()
			if got == nil || *got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestGetClusterUpgradeWorkflowRunTimeout(t *testing.T) {
	origStd := ClusterUpgradeWorkflowTimeoutMinutes
	origLV := ClusterUpgradeWorkflowTimeoutMinutesLV
	defer func() {
		ClusterUpgradeWorkflowTimeoutMinutes = origStd
		ClusterUpgradeWorkflowTimeoutMinutesLV = origLV
	}()

	tests := []struct {
		name          string
		stdVal        string
		lvVal         string
		largeCapacity bool
		expected      time.Duration
	}{
		{"standard uses configured value", "500", "720", false, 500 * time.Minute},
		{"standard uses default on invalid env", "bad", "720", false, 600 * time.Minute},
		{"large capacity uses LV value", "600", "800", true, 800 * time.Minute},
		{"large capacity uses default on invalid env", "600", "bad", true, 720 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ClusterUpgradeWorkflowTimeoutMinutes = tt.stdVal
			ClusterUpgradeWorkflowTimeoutMinutesLV = tt.lvVal
			got := GetClusterUpgradeWorkflowRunTimeout(tt.largeCapacity)
			if got == nil || *got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestGetRefreshRbacWorkflowRunTimeout(t *testing.T) {
	orig := RefreshRbacWorkflowTimeoutMinutes
	defer func() { RefreshRbacWorkflowTimeoutMinutes = orig }()

	tests := []struct {
		name     string
		envVal   string
		expected time.Duration
	}{
		{"uses configured value", "10", 10 * time.Minute},
		{"uses default on invalid env", "xyz", 15 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RefreshRbacWorkflowTimeoutMinutes = tt.envVal
			got := GetRefreshRbacWorkflowRunTimeout()
			if got == nil || *got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

// Ensures every OCI workflow timeout getter returns a non-nil pointer
// when using the package-level defaults (i.e. the env vars as initialised at import time).
func TestOCIWorkflowTimeoutGetters_NeverReturnNil(t *testing.T) {
	getters := []struct {
		name string
		fn   func() *time.Duration
	}{
		{"DeletePool", GetDeletePoolWorkflowRunTimeout},
		{"PoolMaintenance", GetPoolMaintenanceWorkflowRunTimeout},
		{"CreateSVM", GetCreateSVMWorkflowRunTimeout},
		{"DeleteSVM", GetDeleteSVMWorkflowRunTimeout},
		{"UpdateSVM", GetUpdateSVMWorkflowRunTimeout},
		{"RefreshRbac", GetRefreshRbacWorkflowRunTimeout},
	}
	for _, g := range getters {
		t.Run(g.name, func(t *testing.T) {
			got := g.fn()
			if got == nil {
				t.Fatalf("%s returned nil", g.name)
			}
			if *got <= 0 {
				t.Fatalf("%s returned non-positive duration: %v", g.name, *got)
			}
		})
	}

	for _, lc := range []bool{false, true} {
		label := "Standard"
		if lc {
			label = "LargeCapacity"
		}
		t.Run("ClusterUpgrade_"+label, func(t *testing.T) {
			got := GetClusterUpgradeWorkflowRunTimeout(lc)
			if got == nil {
				t.Fatalf("ClusterUpgrade(%v) returned nil", lc)
			}
			if *got <= 0 {
				t.Fatalf("ClusterUpgrade(%v) returned non-positive duration: %v", lc, *got)
			}
		})
	}
}
