package oci_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs/oci"
)

// configPath points at the production OCI VMRS config. The selector tests
// below assert against the perf-team reference table values baked into the
// YAML; if the YAML changes, expected outputs must be refreshed.
func configPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../../../config/vmrs_oci.yaml")
	require.NoError(t, err)
	return p
}

func ptrInt64(v int64) *int64 { return &v }

const expectedShape = "VM.DenseIO.E5.Flex"

func TestSingleVMSelector_Select(t *testing.T) {
	cfg, err := oci.LoadConfig(configPath(t))
	require.NoError(t, err)
	sel, err := oci.NewSelector(cfg)
	require.NoError(t, err)

	cases := []struct {
		name string
		req  oci.CustomerRequest

		wantErr        bool
		wantErrType    any
		wantOCPUs      int
		wantMemoryGBs  float64
		wantThroughput int
		wantVPUName    string
		wantVPU        int
		wantIOPS       int64
	}{
		{
			// Canonical example: 9.00 TB @ 6 GB/s → bucket=6, vpu_30 floor 7.5 TB.
			name: "9TB_6GBs_PicksOCPU48VPU30",
			req: oci.CustomerRequest{
				DesiredCapacityTB:    9.00,
				DesiredThroughputGBs: 6,
			},
			wantOCPUs:      48,
			wantMemoryGBs:  576,
			wantThroughput: 6,
			wantVPUName:    "vpu_30",
			wantVPU:        30,
			wantIOPS:       690000,
		},
		{
			// vpu_30 floor exactly 7.5 TB at bucket=6.
			name: "7_5TB_6GBs_PicksOCPU48VPU30",
			req: oci.CustomerRequest{
				DesiredCapacityTB:    7.5,
				DesiredThroughputGBs: 6,
			},
			wantOCPUs:      48,
			wantMemoryGBs:  576,
			wantThroughput: 6,
			wantVPUName:    "vpu_30",
			wantVPU:        30,
			wantIOPS:       690000,
		},
		{
			// Below vpu_30 floor (7.5 TB) at bucket=6 → infeasible.
			name: "5TB_6GBs_BelowVPU30Floor",
			req: oci.CustomerRequest{
				DesiredCapacityTB:    5.00,
				DesiredThroughputGBs: 6,
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
		{
			// 3 GB/s → bucket=3 → 24 OCPU, vpu_30 floor 3.75 TB fits in 4 TB.
			name: "4TB_3GBs_PicksOCPU24VPU30",
			req: oci.CustomerRequest{
				DesiredCapacityTB:    4.00,
				DesiredThroughputGBs: 3,
			},
			wantOCPUs:      24,
			wantMemoryGBs:  288,
			wantThroughput: 3,
			wantVPUName:    "vpu_30",
			wantVPU:        30,
			wantIOPS:       345000,
		},
		{
			// 2.5 GB/s ceils to bucket=3 → 24 OCPU, vpu_30 floor 3.75 TB.
			name: "5TB_2_5GBs_BumpsUpToBucket3",
			req: oci.CustomerRequest{
				DesiredCapacityTB:    5.00,
				DesiredThroughputGBs: 2.5,
			},
			wantOCPUs:      24,
			wantMemoryGBs:  288,
			wantThroughput: 3,
			wantVPUName:    "vpu_30",
			wantVPU:        30,
			wantIOPS:       345000,
		},
		{
			// IOPS is currently ignored — selection is capacity-driven only.
			name: "9TB_6GBs_IOPS800K_IgnoredPicksVPU30",
			req: oci.CustomerRequest{
				DesiredCapacityTB:    9.00,
				DesiredThroughputGBs: 6,
				DesiredIOPS:          ptrInt64(800000),
			},
			wantOCPUs:      48,
			wantMemoryGBs:  576,
			wantThroughput: 6,
			wantVPUName:    "vpu_30",
			wantVPU:        30,
			wantIOPS:       690000,
		},
		{
			// Even an impossible-to-satisfy IOPS target doesn't affect
			// the outcome today: selection ignores IOPS entirely.
			name: "9TB_6GBs_IOPS1M_IgnoredStillPicksVPU30",
			req: oci.CustomerRequest{
				DesiredCapacityTB:    9.00,
				DesiredThroughputGBs: 6,
				DesiredIOPS:          ptrInt64(1_000_000),
			},
			wantOCPUs:      48,
			wantMemoryGBs:  576,
			wantThroughput: 6,
			wantVPUName:    "vpu_30",
			wantVPU:        30,
			wantIOPS:       690000,
		},
		{
			// Below vpu_30 floor (1.25 TB) at bucket=1 → infeasible regardless
			// of the IOPS value passed.
			name: "1TB_1GBs_IOPS130K_BelowVPU30Floor",
			req: oci.CustomerRequest{
				DesiredCapacityTB:    1.00,
				DesiredThroughputGBs: 1,
				DesiredIOPS:          ptrInt64(130_000),
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
		{
			// vpu_30 floor at bucket=1 is 1.25 TB — 1 TB still infeasible.
			name: "1TB_1GBs_IOPS140K_BelowVPU30Floor",
			req: oci.CustomerRequest{
				DesiredCapacityTB:    1.00,
				DesiredThroughputGBs: 1,
				DesiredIOPS:          ptrInt64(140_000),
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
		{
			// 7 GB/s → bucket=7 not in catalogue → infeasible.
			name: "7GBs_NoBucket",
			req: oci.CustomerRequest{
				DesiredCapacityTB:    10.00,
				DesiredThroughputGBs: 7,
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
		{
			// Below vpu_30's floor at bucket=1 (1.25 TB) → infeasible.
			name: "0_5TB_1GBs_BelowAllFloors",
			req: oci.CustomerRequest{
				DesiredCapacityTB:    0.5,
				DesiredThroughputGBs: 1,
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
		{
			name: "ZeroThroughput_Rejected",
			req: oci.CustomerRequest{
				DesiredCapacityTB:    5,
				DesiredThroughputGBs: 0,
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
		{
			name: "ZeroCapacity_Rejected",
			req: oci.CustomerRequest{
				DesiredCapacityTB:    0,
				DesiredThroughputGBs: 3,
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := sel.Select(tc.req)

			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrType != nil {
					assert.True(
						t,
						errors.As(err, &tc.wantErrType),
						"err %T not assignable to %T", err, tc.wantErrType,
					)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, dec)
			assert.Equal(t, expectedShape, dec.VMShape, "VMShape mismatch")
			assert.Equal(t, tc.wantOCPUs, dec.OCPUs, "OCPUs mismatch")
			assert.InDelta(t, tc.wantMemoryGBs, dec.MemoryGBs, 0.0001,
				"MemoryGBs mismatch — VMRS catalogue drift would silently change "+
					"what gets passed to LaunchInstance.shapeConfig.memoryInGBs")
			assert.Equal(t, tc.wantThroughput, dec.ThroughputGBs, "ThroughputGBs mismatch")
			assert.Equal(t, tc.wantVPUName, dec.VPUName, "VPUName mismatch")
			assert.Equal(t, tc.wantVPU, dec.VPU, "VPU mismatch")
			assert.Equal(t, tc.wantIOPS, dec.IOPS, "IOPS mismatch")
			assert.InDelta(t, tc.req.DesiredCapacityTB, dec.CapacityTB, 0.0001,
				"CapacityTB should echo the request")
		})
	}
}

// cfgWith builds a single-tier, single-instance Config for the
// constructor-validation tests below. Tests vary one input at a time
// (e.g. bucket=0, ocpus=0, malformed vpu key) to assert that the
// validator catches exactly that failure. MemoryGBs is fixed to a
// valid positive value (96) so it never trips validation; tests
// targeting the memory_gbs check construct their config inline.
func cfgWith(
	bucket int,
	shape string, ocpus int,
	vpuKey string, cell oci.VPUCell,
) *oci.Config {
	return &oci.Config{
		Throughput: map[int]oci.ThroughputTier{
			bucket: {
				Instances: map[string]oci.Instance{
					shape: {
						OCPUs:     ocpus,
						MemoryGBs: 96,
						VPU:       map[string]oci.VPUCell{vpuKey: cell},
					},
				},
			},
		},
	}
}

func TestNewSelector_NilConfig(t *testing.T) {
	_, err := oci.NewSelector(nil)
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

func TestNewSelector_EmptyThroughput(t *testing.T) {
	_, err := oci.NewSelector(&oci.Config{})
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

func TestNewSelector_ZeroBucket(t *testing.T) {
	_, err := oci.NewSelector(cfgWith(
		0, expectedShape, 8,
		"vpu_50", oci.VPUCell{CapacityThroughputTB: 1, IOPS: 1},
	))
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

func TestNewSelector_NoInstances(t *testing.T) {
	_, err := oci.NewSelector(&oci.Config{
		Throughput: map[int]oci.ThroughputTier{1: {Instances: nil}},
	})
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

func TestNewSelector_NonPositiveOCPUs(t *testing.T) {
	_, err := oci.NewSelector(cfgWith(
		1, expectedShape, 0,
		"vpu_50", oci.VPUCell{CapacityThroughputTB: 1, IOPS: 1},
	))
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

func TestNewSelector_EmptyInstanceShape(t *testing.T) {
	_, err := oci.NewSelector(cfgWith(
		1, "", 8,
		"vpu_50", oci.VPUCell{CapacityThroughputTB: 1, IOPS: 1},
	))
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

func TestNewSelector_NoVPUs(t *testing.T) {
	_, err := oci.NewSelector(&oci.Config{
		Throughput: map[int]oci.ThroughputTier{
			1: {
				Instances: map[string]oci.Instance{
					expectedShape: {OCPUs: 8, MemoryGBs: 96, VPU: nil},
				},
			},
		},
	})
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

// TestNewSelector_NonPositiveMemoryGBs is the symmetric guard for the
// new memory_gbs validation: a positive ocpus + valid VPU table must
// still be rejected when memory_gbs is missing / zero, because every
// LaunchInstance call needs both ocpus and memoryInGBs.
func TestNewSelector_NonPositiveMemoryGBs(t *testing.T) {
	_, err := oci.NewSelector(&oci.Config{
		Throughput: map[int]oci.ThroughputTier{
			1: {
				Instances: map[string]oci.Instance{
					expectedShape: {
						OCPUs:     8,
						MemoryGBs: 0,
						VPU: map[string]oci.VPUCell{
							"vpu_50": {CapacityThroughputTB: 1, IOPS: 1},
						},
					},
				},
			},
		},
	})
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

func TestNewSelector_MalformedVPUKey(t *testing.T) {
	_, err := oci.NewSelector(cfgWith(
		1, expectedShape, 8,
		"vpuu_50", oci.VPUCell{CapacityThroughputTB: 1, IOPS: 1},
	))
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

func TestNewSelector_NonPositiveCell(t *testing.T) {
	_, err := oci.NewSelector(cfgWith(
		1, expectedShape, 8,
		"vpu_50", oci.VPUCell{CapacityThroughputTB: 0, IOPS: 1},
	))
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

// TestDecide_OneCallEntryPoint verifies the package-level convenience
// wrapper returns the same Decision as NewSelector(cfg).Select(req) for
// the canonical 9 TB / 6 GB/s case.
func TestDecide_OneCallEntryPoint(t *testing.T) {
	cfg, err := oci.LoadConfig(configPath(t))
	require.NoError(t, err)

	req := oci.CustomerRequest{
		DesiredCapacityTB:    9.00,
		DesiredThroughputGBs: 6,
	}

	viaSelector, err := func() (*oci.Decision, error) {
		sel, err := oci.NewSelector(cfg)
		if err != nil {
			return nil, err
		}
		return sel.Select(req)
	}()
	require.NoError(t, err)

	viaDecide, err := oci.Decide(cfg, req)
	require.NoError(t, err)

	assert.Equal(t, viaSelector, viaDecide,
		"Decide must return the same Decision as NewSelector + Select")
}

func TestDecide_NilConfigError(t *testing.T) {
	_, err := oci.Decide(nil, oci.CustomerRequest{
		DesiredCapacityTB:    1,
		DesiredThroughputGBs: 1,
	})
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

// TestSelect_MultipleInstancesTiebreakOnOCPUs verifies the
// lowest-OCPUs tiebreak when multiple shapes coexist at one throughput
// tier — the documented forward-compatibility path. Each instance now
// carries its own VPU table.
func TestSelect_MultipleInstancesTiebreakOnOCPUs(t *testing.T) {
	sharedVPU := map[string]oci.VPUCell{
		"vpu_50": {CapacityThroughputTB: 4.0, IOPS: 480000},
	}
	cfg := &oci.Config{
		Throughput: map[int]oci.ThroughputTier{
			3: {
				Instances: map[string]oci.Instance{
					"VM.DenseIO.E5.Flex":      {OCPUs: 24, MemoryGBs: 288, VPU: sharedVPU},
					"VM.DenseIO.HighOCPU.Big": {OCPUs: 64, MemoryGBs: 768, VPU: sharedVPU},
				},
			},
		},
	}
	sel, err := oci.NewSelector(cfg)
	require.NoError(t, err)

	dec, err := sel.Select(oci.CustomerRequest{
		DesiredCapacityTB:    5,
		DesiredThroughputGBs: 3,
	})
	require.NoError(t, err)
	assert.Equal(t, "VM.DenseIO.E5.Flex", dec.VMShape)
	assert.Equal(t, 24, dec.OCPUs)
}

// TestNewSingleVMSelector_NilConfig covers the nil-config guard at the
// constructor entry. The public TestNewSelector_NilConfig goes through
// NewSelector(nil) which short-circuits in selector.go BEFORE reaching
// NewSingleVMSelector, leaving the latter's identical nil check
// uncovered. Calling NewSingleVMSelector directly closes that gap.
func TestNewSingleVMSelector_NilConfig(t *testing.T) {
	_, err := oci.NewSingleVMSelector(nil)
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr),
		"NewSingleVMSelector must reject nil config with *InvalidConfigError")
}

// TestNewSelector_ZeroIOPSCell asserts the VPU-cell IOPS validator
// rejects a cell with positive capacity but IOPS=0. The capacity-zero
// case is already covered (TestNewSelector_NonPositiveCell); without
// this, a typo'd YAML row with `iops: 0` would slip through and the
// selector would then advertise a 0-IOPS Decision downstream.
func TestNewSelector_ZeroIOPSCell(t *testing.T) {
	_, err := oci.NewSelector(cfgWith(
		1, expectedShape, 8,
		"vpu_50", oci.VPUCell{CapacityThroughputTB: 1, IOPS: 0},
	))
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	require.True(t, errors.As(err, &invalidErr))
	assert.Contains(t, err.Error(), "iops must be > 0")
}

// TestPrepareInstances_TieBreaksOnShapeNameWhenOCPUsEqual covers the
// stable-sort branch in prepareInstances: when two shapes coexist at
// the same throughput tier with the same OCPU count, iteration order
// must be lexicographic on shape name. Without a deterministic tiebreak
// the selector would non-deterministically pick between identical-OCPU
// shapes (Go map iteration is randomized), making Decision output flaky
// across builds.
func TestPrepareInstances_TieBreaksOnShapeNameWhenOCPUsEqual(t *testing.T) {
	sharedVPU := map[string]oci.VPUCell{
		"vpu_50": {CapacityThroughputTB: 1.0, IOPS: 160000},
	}
	cfg := &oci.Config{
		Throughput: map[int]oci.ThroughputTier{
			1: {
				Instances: map[string]oci.Instance{
					// Lexicographically earlier shape comes first when
					// OCPUs match — both shapes here have OCPUs=8.
					"VM.Z.Last":  {OCPUs: 8, MemoryGBs: 96, VPU: sharedVPU},
					"VM.A.First": {OCPUs: 8, MemoryGBs: 96, VPU: sharedVPU},
				},
			},
		},
	}
	sel, err := oci.NewSelector(cfg)
	require.NoError(t, err)

	// A request that fits both should land on "VM.A.First" because it
	// sorts ahead of "VM.Z.Last" when OCPUs are tied.
	dec, err := sel.Select(oci.CustomerRequest{
		DesiredCapacityTB:    2.0,
		DesiredThroughputGBs: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, "VM.A.First", dec.VMShape,
		"OCPU tie must break alphabetically on shape name (deterministic Decision)")
}

// TestNewSelector_VPUKey_AtoiOverflow exercises parseVPUNumber's
// strconv.Atoi failure branch: the regex (\d+) accepts arbitrarily long
// digit strings, but Atoi caps at int range and returns *NumError with
// ErrRange. Without the explicit error check, that case used to fall
// through to "non-positive VPU number" — a misleading message.
func TestNewSelector_VPUKey_AtoiOverflow(t *testing.T) {
	_, err := oci.NewSelector(cfgWith(
		1, expectedShape, 8,
		// 21 nines: well beyond math.MaxInt64.
		"vpu_999999999999999999999",
		oci.VPUCell{CapacityThroughputTB: 1, IOPS: 1},
	))
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	require.True(t, errors.As(err, &invalidErr))
	assert.Contains(t, err.Error(), "invalid vpu key",
		"overflow must surface as an invalid-key error, not as 'non-positive VPU'")
}

// TestNewSelector_VPUKey_ZeroIsRejected covers parseVPUNumber's
// non-positive branch: regex matches "vpu_0", Atoi parses 0, then the
// n <= 0 guard fires. A VPU band of 0 is meaningless and would also
// silently sort to the head of the ascending-VPU walk, hijacking
// selection.
func TestNewSelector_VPUKey_ZeroIsRejected(t *testing.T) {
	_, err := oci.NewSelector(cfgWith(
		1, expectedShape, 8,
		"vpu_0",
		oci.VPUCell{CapacityThroughputTB: 1, IOPS: 1},
	))
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	require.True(t, errors.As(err, &invalidErr))
	assert.Contains(t, err.Error(), "non-positive VPU number")
}

// TestSelect_FallsBackToHigherOCPUInstance verifies that when the
// lower-OCPU instance's VPU table doesn't have a cell that fits the
// request, the selector falls back to a higher-OCPU instance whose VPU
// table does. Useful when shapes have different per-volume caps.
func TestSelect_FallsBackToHigherOCPUInstance(t *testing.T) {
	cfg := &oci.Config{
		Throughput: map[int]oci.ThroughputTier{
			3: {
				Instances: map[string]oci.Instance{
					// Lower-OCPU shape only offers vpu_40 with a high
					// floor (10 TB). A 5-TB request can't land here.
					"VM.Small": {
						OCPUs:     8,
						MemoryGBs: 96,
						VPU: map[string]oci.VPUCell{
							"vpu_40": {CapacityThroughputTB: 10.0, IOPS: 480000},
						},
					},
					// Higher-OCPU shape offers vpu_90 with a lower
					// floor (3 TB).
					"VM.Large": {
						OCPUs:     16,
						MemoryGBs: 192,
						VPU: map[string]oci.VPUCell{
							"vpu_90": {CapacityThroughputTB: 3.0, IOPS: 392400},
						},
					},
				},
			},
		},
	}
	sel, err := oci.NewSelector(cfg)
	require.NoError(t, err)

	dec, err := sel.Select(oci.CustomerRequest{
		DesiredCapacityTB:    5,
		DesiredThroughputGBs: 3,
	})
	require.NoError(t, err)
	assert.Equal(t, "VM.Large", dec.VMShape)
	assert.Equal(t, 16, dec.OCPUs)
	assert.Equal(t, 90, dec.VPU)
}
