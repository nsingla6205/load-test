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
		wantThroughput int // Throughput (YAML key)
		wantVPUName    string
		wantVPU        int
		wantIOPS       int64
	}{
		{
			// Canonical example: 9.00 TB @ 4800 MB/s → bucket=4800, vpu_30 floor 7.58 TB.
			name: "9TB_4800MBps_PicksOCPU48VPU30",
			req: oci.CustomerRequest{
				DesiredCapacityTB:     9.00,
				DesiredThroughputMBps: 4800,
			},
			wantOCPUs:      48,
			wantMemoryGBs:  576,
			wantThroughput: 4800,
			wantVPUName:    "vpu_30",
			wantVPU:        30,
			wantIOPS:       260000,
		},
		{
			// vpu_30 floor exactly 7.58 TB at bucket=4800.
			name: "7_58TB_4800MBps_PicksOCPU48VPU30",
			req: oci.CustomerRequest{
				DesiredCapacityTB:     7.58,
				DesiredThroughputMBps: 4800,
			},
			wantOCPUs:      48,
			wantMemoryGBs:  576,
			wantThroughput: 4800,
			wantVPUName:    "vpu_30",
			wantVPU:        30,
			wantIOPS:       260000,
		},
		{
			// Below vpu_30 floor (7.58 TB) at bucket=4800 → infeasible.
			name: "5TB_4800MBps_BelowVPU30Floor",
			req: oci.CustomerRequest{
				DesiredCapacityTB:     5.00,
				DesiredThroughputMBps: 4800,
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
		{
			// 2400 MB/s → bucket=2400 → 24 OCPU, vpu_30 floor 3.79 TB fits in 4 TB.
			name: "4TB_2400MBps_PicksOCPU24VPU30",
			req: oci.CustomerRequest{
				DesiredCapacityTB:     4.00,
				DesiredThroughputMBps: 2400,
			},
			wantOCPUs:      24,
			wantMemoryGBs:  288,
			wantThroughput: 2400,
			wantVPUName:    "vpu_30",
			wantVPU:        30,
			wantIOPS:       260000,
		},
		{
			// 2000 MB/s is not a multiple of 800 → rejected.
			name: "5TB_2000MBps_NotAMultipleOf800_Rejected",
			req: oci.CustomerRequest{
				DesiredCapacityTB:     5.00,
				DesiredThroughputMBps: 2000,
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
		{
			// IOPS is currently ignored — selection is capacity-driven only.
			name: "9TB_4800MBps_IOPS800K_IgnoredPicksVPU30",
			req: oci.CustomerRequest{
				DesiredCapacityTB:     9.00,
				DesiredThroughputMBps: 4800,
				DesiredIOPS:           ptrInt64(800000),
			},
			wantOCPUs:      48,
			wantMemoryGBs:  576,
			wantThroughput: 4800,
			wantVPUName:    "vpu_30",
			wantVPU:        30,
			wantIOPS:       260000,
		},
		{
			// Even an impossible-to-satisfy IOPS target doesn't affect
			// the outcome today: selection ignores IOPS entirely.
			name: "9TB_4800MBps_IOPS1M_IgnoredStillPicksVPU30",
			req: oci.CustomerRequest{
				DesiredCapacityTB:     9.00,
				DesiredThroughputMBps: 4800,
				DesiredIOPS:           ptrInt64(1_000_000),
			},
			wantOCPUs:      48,
			wantMemoryGBs:  576,
			wantThroughput: 4800,
			wantVPUName:    "vpu_30",
			wantVPU:        30,
			wantIOPS:       260000,
		},
		{
			// Below vpu_30 floor (1.26 TB) at bucket=800 → infeasible regardless
			// of the IOPS value passed.
			name: "1TB_800MBps_IOPS130K_BelowVPU30Floor",
			req: oci.CustomerRequest{
				DesiredCapacityTB:     1.00,
				DesiredThroughputMBps: 800,
				DesiredIOPS:           ptrInt64(130_000),
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
		{
			// vpu_30 floor at bucket=800 is 1.26 TB — 1 TB still infeasible.
			name: "1TB_800MBps_IOPS140K_BelowVPU30Floor",
			req: oci.CustomerRequest{
				DesiredCapacityTB:     1.00,
				DesiredThroughputMBps: 800,
				DesiredIOPS:           ptrInt64(140_000),
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
		{
			// 5600 MB/s is a multiple of 800 but no matching catalogue tier exists → infeasible.
			name: "5600MBps_NoBucket",
			req: oci.CustomerRequest{
				DesiredCapacityTB:     10.00,
				DesiredThroughputMBps: 5600,
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
		{
			// Below vpu_30's floor at bucket=800 (1.26 TB) → infeasible.
			name: "0_5TB_800MBps_BelowAllFloors",
			req: oci.CustomerRequest{
				DesiredCapacityTB:     0.5,
				DesiredThroughputMBps: 800,
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
		{
			name: "ZeroThroughput_Rejected",
			req: oci.CustomerRequest{
				DesiredCapacityTB:     5,
				DesiredThroughputMBps: 0,
			},
			wantErr:     true,
			wantErrType: &oci.NoFeasibleSelectionError{},
		},
		{
			name: "ZeroCapacity_Rejected",
			req: oci.CustomerRequest{
				DesiredCapacityTB:     0,
				DesiredThroughputMBps: 2400,
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
			assert.Equal(t, tc.wantThroughput, dec.Throughput, "Throughput mismatch")
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
				ThroughputWithHeadroom: 1000,
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
		"vpu_50", oci.VPUCell{MinCapacityTB: 1, MaxIOPS: 1},
	))
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

func TestNewSelector_NoInstances(t *testing.T) {
	_, err := oci.NewSelector(&oci.Config{
		Throughput: map[int]oci.ThroughputTier{
			800: {Instances: nil},
		},
	})
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

func TestNewSelector_NonPositiveOCPUs(t *testing.T) {
	_, err := oci.NewSelector(cfgWith(
		800, expectedShape, 0,
		"vpu_50", oci.VPUCell{MinCapacityTB: 1, MaxIOPS: 1},
	))
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

func TestNewSelector_EmptyInstanceShape(t *testing.T) {
	_, err := oci.NewSelector(cfgWith(
		800, "", 8,
		"vpu_50", oci.VPUCell{MinCapacityTB: 1, MaxIOPS: 1},
	))
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

func TestNewSelector_NoVPUs(t *testing.T) {
	_, err := oci.NewSelector(&oci.Config{
		Throughput: map[int]oci.ThroughputTier{
			800: {
				ThroughputWithHeadroom: 1000,
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
			800: {
				ThroughputWithHeadroom: 1000,
				Instances: map[string]oci.Instance{
					expectedShape: {
						OCPUs:     8,
						MemoryGBs: 0,
						VPU: map[string]oci.VPUCell{
							"vpu_50": {MinCapacityTB: 1, MaxIOPS: 1},
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
		800, expectedShape, 8,
		"vpuu_50", oci.VPUCell{MinCapacityTB: 1, MaxIOPS: 1},
	))
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

func TestNewSelector_NonPositiveCell(t *testing.T) {
	_, err := oci.NewSelector(cfgWith(
		800, expectedShape, 8,
		"vpu_50", oci.VPUCell{MinCapacityTB: 0, MaxIOPS: 1},
	))
	require.Error(t, err)
	var invalidErr *oci.InvalidConfigError
	assert.True(t, errors.As(err, &invalidErr))
}

// TestDecide_OneCallEntryPoint verifies the package-level convenience
// wrapper returns the same Decision as NewSelector(cfg).Select(req) +
// ResolveIOPSFromDecision for the canonical 9 TB / 4800 MB/s case.
func TestDecide_OneCallEntryPoint(t *testing.T) {
	cfg, err := oci.LoadConfig(configPath(t))
	require.NoError(t, err)

	req := oci.CustomerRequest{
		DesiredCapacityTB:     9.00,
		DesiredThroughputMBps: 4800,
	}

	viaSelector, err := func() (*oci.Decision, error) {
		sel, err := oci.NewSelector(cfg)
		if err != nil {
			return nil, err
		}
		d, err := sel.Select(req)
		if err != nil {
			return nil, err
		}
		// Decide also resolves IOPS; mirror that here so the comparison is valid.
		resolvedIOPS, err := oci.ResolveIOPSFromDecision(d, req.DesiredIOPS, req.CurrentThroughputMBps, req.CurrentIOPS)
		if err != nil {
			return nil, err
		}
		d.ResolvedIOPS = resolvedIOPS
		return d, nil
	}()
	require.NoError(t, err)

	viaDecide, err := oci.Decide(cfg, req)
	require.NoError(t, err)

	assert.Equal(t, viaSelector, viaDecide,
		"Decide must return the same Decision as NewSelector + Select + ResolveIOPSFromDecision")
}

func TestDecide_NilConfigError(t *testing.T) {
	_, err := oci.Decide(nil, oci.CustomerRequest{
		DesiredCapacityTB:     1,
		DesiredThroughputMBps: 800,
	})
	require.Error(t, err)
}

func TestDecide_SelectError(t *testing.T) {
	cfg, err := oci.LoadConfig(configPath(t))
	require.NoError(t, err)

	_, err = oci.Decide(cfg, oci.CustomerRequest{
		DesiredCapacityTB:     9,
		DesiredThroughputMBps: 1000, // not a multiple of 800
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple of")
}

func TestDecide_ResolveIOPSError(t *testing.T) {
	cfg, err := oci.LoadConfig(configPath(t))
	require.NoError(t, err)

	tooHigh := int64(999_999_999)
	_, err = oci.Decide(cfg, oci.CustomerRequest{
		DesiredCapacityTB:     9,
		DesiredThroughputMBps: 800,
		DesiredIOPS:           &tooHigh,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the maximum")
}

// TestSelect_MultipleInstancesTiebreakOnOCPUs verifies the
// lowest-OCPUs tiebreak when multiple shapes coexist at one throughput
// tier — the documented forward-compatibility path. Each instance now
// carries its own VPU table.
func TestSelect_MultipleInstancesTiebreakOnOCPUs(t *testing.T) {
	sharedVPU := map[string]oci.VPUCell{
		"vpu_50": {MinCapacityTB: 4.0, MaxIOPS: 480000},
	}
	cfg := &oci.Config{
		Throughput: map[int]oci.ThroughputTier{
			2400: {
				ThroughputWithHeadroom: 3000,
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
		DesiredCapacityTB:     5,
		DesiredThroughputMBps: 2400,
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
		800, expectedShape, 8,
		"vpu_50", oci.VPUCell{MinCapacityTB: 1, MaxIOPS: 0},
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
		"vpu_50": {MinCapacityTB: 1.0, MaxIOPS: 160000},
	}
	cfg := &oci.Config{
		Throughput: map[int]oci.ThroughputTier{
			800: {
				ThroughputWithHeadroom: 1000,
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
		DesiredCapacityTB:     2.0,
		DesiredThroughputMBps: 800,
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
		800, expectedShape, 8,
		// 21 nines: well beyond math.MaxInt64.
		"vpu_999999999999999999999",
		oci.VPUCell{MinCapacityTB: 1, MaxIOPS: 1},
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
		800, expectedShape, 8,
		"vpu_0",
		oci.VPUCell{MinCapacityTB: 1, MaxIOPS: 1},
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
			2400: {
				ThroughputWithHeadroom: 3000,
				Instances: map[string]oci.Instance{
					// Lower-OCPU shape only offers vpu_40 with a high
					// floor (10 TB). A 5-TB request can't land here.
					"VM.Small": {
						OCPUs:     8,
						MemoryGBs: 96,
						VPU: map[string]oci.VPUCell{
							"vpu_40": {MinCapacityTB: 10.0, MaxIOPS: 480000},
						},
					},
					// Higher-OCPU shape offers vpu_90 with a lower
					// floor (3 TB).
					"VM.Large": {
						OCPUs:     16,
						MemoryGBs: 192,
						VPU: map[string]oci.VPUCell{
							"vpu_90": {MinCapacityTB: 3.0, MaxIOPS: 392400},
						},
					},
				},
			},
		},
	}
	sel, err := oci.NewSelector(cfg)
	require.NoError(t, err)

	dec, err := sel.Select(oci.CustomerRequest{
		DesiredCapacityTB:     5,
		DesiredThroughputMBps: 2400,
	})
	require.NoError(t, err)
	assert.Equal(t, "VM.Large", dec.VMShape)
	assert.Equal(t, 16, dec.OCPUs)
	assert.Equal(t, 90, dec.VPU)
}

// ---------------------------------------------------------------------------
// ResolveIOPSFromDecision — IOPS resolution rules
// ---------------------------------------------------------------------------

// decisionForTier builds a minimal Decision for IOPS resolution tests.
func decisionForTier(throughputMBps int, defaultIOPS, maxIOPS int64) *oci.Decision {
	return &oci.Decision{
		Throughput:  throughputMBps,
		DefaultIOPS: defaultIOPS,
		IOPS:        maxIOPS,
	}
}

// TestResolveIOPS_ExplicitWithinRange verifies that an explicit IOPS within
// [DefaultIOPS, MaxIOPS] is accepted and returned verbatim.
func TestResolveIOPS_ExplicitWithinRange(t *testing.T) {
	d := decisionForTier(800, 12500, 128000)
	got, err := oci.ResolveIOPSFromDecision(d, ptrInt64(50000), 1600, 50000)
	require.NoError(t, err)
	assert.Equal(t, int64(50000), got)
}

// TestResolveIOPS_ExplicitAboveMax verifies that an explicit IOPS exceeding
// the new tier's MaxIOPS is rejected with an error.
func TestResolveIOPS_ExplicitAboveMax(t *testing.T) {
	d := decisionForTier(800, 12500, 128000)
	_, err := oci.ResolveIOPSFromDecision(d, ptrInt64(200000), 1600, 50000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the maximum")
}

// TestResolveIOPS_ExplicitBelowDefault verifies that an explicit IOPS below
// the tier's DefaultIOPS is rejected.
func TestResolveIOPS_ExplicitBelowDefault(t *testing.T) {
	d := decisionForTier(800, 12500, 128000)
	_, err := oci.ResolveIOPSFromDecision(d, ptrInt64(5000), 0, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "below the minimum")
}

// TestResolveIOPS_DowngradeNoIOPS_PreservesCurrentIOPS is the primary new
// behaviour: on a throughput downgrade with no explicit IOPS, the customer's
// current IOPS is preserved as long as it fits within the new tier's MaxIOPS.
//
// Scenario: 1600 MB/s → 800 MB/s, currentIOPS = 50K, newTier.MaxIOPS = 128K.
// Expected: 50K (preserved, not jumped to 128K).
func TestResolveIOPS_DowngradeNoIOPS_PreservesCurrentIOPS(t *testing.T) {
	d := decisionForTier(800, 12500, 128000)
	got, err := oci.ResolveIOPSFromDecision(d, nil, 1600, 50000)
	require.NoError(t, err)
	assert.Equal(t, int64(50000), got,
		"downgrade with no explicit IOPS must preserve the customer's current IOPS")
}

// TestResolveIOPS_DowngradeNoIOPS_CapsAtMaxWhenCurrentExceedsNewMax verifies
// that if the current IOPS exceeds the new tier's MaxIOPS, the result is
// capped at MaxIOPS rather than returning an error.
//
// Scenario: 2400 MB/s (max 260K) → 800 MB/s (max 128K), currentIOPS = 200K.
// Expected: 128K (capped).
func TestResolveIOPS_DowngradeNoIOPS_CapsAtMaxWhenCurrentExceedsNewMax(t *testing.T) {
	d := decisionForTier(800, 12500, 128000)
	got, err := oci.ResolveIOPSFromDecision(d, nil, 2400, 200000)
	require.NoError(t, err)
	assert.Equal(t, int64(128000), got,
		"downgrade where currentIOPS > newMax must cap at newMax")
}

// TestResolveIOPS_DowngradeNoIOPS_ZeroCurrentFallsBackToDefault covers the
// case where currentIOPS is unknown (0) on a downgrade. No IOPS to preserve,
// so the safe fallback is DefaultIOPS (not MaxIOPS).
func TestResolveIOPS_DowngradeNoIOPS_ZeroCurrentFallsBackToDefault(t *testing.T) {
	d := decisionForTier(800, 12500, 128000)
	got, err := oci.ResolveIOPSFromDecision(d, nil, 1600, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(12500), got,
		"downgrade with unknown current IOPS must fall back to DefaultIOPS")
}

// TestResolveIOPS_NoDowngrade_UsesDefault verifies that when there is no
// throughput downgrade and no explicit IOPS, the tier's DefaultIOPS is used.
func TestResolveIOPS_NoDowngrade_UsesDefault(t *testing.T) {
	d := decisionForTier(1600, 25000, 256000)
	got, err := oci.ResolveIOPSFromDecision(d, nil, 800, 12500)
	require.NoError(t, err)
	assert.Equal(t, int64(25000), got,
		"upgrade/no-change with no explicit IOPS must use the tier's DefaultIOPS")
}

// TestResolveIOPS_CreatePath_UsesDefault verifies the create path
// (currentThroughputMBps = 0): no downgrade detection, falls to DefaultIOPS.
func TestResolveIOPS_CreatePath_UsesDefault(t *testing.T) {
	d := decisionForTier(800, 12500, 128000)
	got, err := oci.ResolveIOPSFromDecision(d, nil, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(12500), got,
		"create path (currentThroughput=0) must use DefaultIOPS")
}

// TestResolveIOPS_SameTier_PreservesCurrentIOPS verifies H1:
// a capacity-only update (same throughput tier, no explicit IOPS) preserves
// the pool's current IOPS instead of resetting to DefaultIOPS.
//
// Scenario: stays on 1600 MB/s tier, currentIOPS = 50K, DefaultIOPS = 25K.
// Expected: 50K (preserved, not reset to 25K).
func TestResolveIOPS_SameTier_PreservesCurrentIOPS(t *testing.T) {
	d := decisionForTier(1600, 25000, 256000)
	got, err := oci.ResolveIOPSFromDecision(d, nil, 1600, 50000)
	require.NoError(t, err)
	assert.Equal(t, int64(50000), got,
		"same-tier update with no explicit IOPS must preserve the current IOPS")
}

// TestResolveIOPS_DowngradeNoIOPS_CurrentBelowDefault_ClampsToDefault covers M1:
// if the current IOPS somehow falls below the new tier's DefaultIOPS on a
// downgrade, the result is floored at DefaultIOPS rather than sending a
// below-floor value to VLM.
//
// Scenario: 1600→800 downgrade, currentIOPS = 5K, DefaultIOPS = 12.5K.
// Expected: 12500 (floored to DefaultIOPS).
func TestResolveIOPS_DowngradeNoIOPS_CurrentBelowDefault_ClampsToDefault(t *testing.T) {
	d := decisionForTier(800, 12500, 128000)
	got, err := oci.ResolveIOPSFromDecision(d, nil, 1600, 5000)
	require.NoError(t, err)
	assert.Equal(t, int64(12500), got,
		"current IOPS below new tier's DefaultIOPS must be floored to DefaultIOPS")
}

func TestResolveIOPS_NilDecision(t *testing.T) {
	_, err := oci.ResolveIOPSFromDecision(nil, nil, 0, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decision is nil")
}

func TestNewSelector_RejectsZeroThroughputWithHeadroom(t *testing.T) {
	cfg := &oci.Config{
		Throughput: map[int]oci.ThroughputTier{
			800: {
				ThroughputWithHeadroom: 0,
				Instances: map[string]oci.Instance{
					"VM.DenseIO.E5.Flex": {
						OCPUs:     8,
						MemoryGBs: 96,
						VPU: map[string]oci.VPUCell{
							"vpu_30": {DefaultIOPS: 12500, MaxIOPS: 128000, MinCapacityTB: 1.26},
						},
					},
				},
			},
		},
	}
	_, err := oci.NewSelector(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "throughput_with_headroom must be > 0")
}
