package leakedresources

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/leakedresources/detectors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/leakedresources/model"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
)

// registration pairs a detector with the reporter its leak records flow to.
// Each detector is routed independently, so different detectors can log-only,
// log+metrics, or send to additional reporters (e.g. ServiceNow) as needed.
type registration struct {
	detector model.Detector
	reporter Reporter
}

// Pipeline runs the leaked resources flow: for each registered detector, run
// Detect and report that detector's leak records to the reporter it was
// registered with.
type Pipeline struct {
	registrations []registration
}

// NewPipeline returns an empty pipeline. Call Register to add detectors along
// with the reporters their leak records should flow to.
func NewPipeline() *Pipeline {
	return &Pipeline{}
}

// Register adds a detector paired with the reporters its leak records flow to.
// Detectors are run in registration order. Reporter routing rules:
//   - no reporters (or all nil)  => LogReporter (log-only)
//   - exactly one reporter       => used directly
//   - multiple reporters         => wrapped in a MultiReporter (fan-out)
//
// A nil detector is ignored.
func (p *Pipeline) Register(d model.Detector, reporters ...Reporter) {
	if d == nil {
		return
	}
	p.registrations = append(p.registrations, registration{
		detector: d,
		reporter: combineReporters(reporters...),
	})
}

// combineReporters resolves a variadic reporter list into a single Reporter,
// applying the routing rules documented on Register. Fan-out to multiple
// reporters is delegated to MultiReporter so the loop lives in one place.
func combineReporters(reporters ...Reporter) Reporter {
	nonNil := make([]Reporter, 0, len(reporters))
	for _, r := range reporters {
		if r != nil {
			nonNil = append(nonNil, r)
		}
	}
	switch len(nonNil) {
	case 0:
		return LogReporter{}
	case 1:
		return nonNil[0]
	default:
		return NewMultiReporter(nonNil...)
	}
}

// beginRun resets per-run reporter state at the start of the run, before any
// detector reports. It is anchored to the run (not the detector) so that
// per-detector reporting accumulates within a run instead of each detector
// clobbering the previous one's state (e.g. the metrics gauge snapshot).
// Reporters without per-run state (LogReporter) are skipped. A reporter
// instance shared across registrations (like the default metrics reporter) may
// have BeginRun invoked more than once here; this is why runAware.BeginRun is
// required to be idempotent, and it is always called before any Report.
func (p *Pipeline) beginRun(ctx context.Context) {
	for _, reg := range p.registrations {
		if ra, ok := reg.reporter.(runAware); ok {
			ra.BeginRun(ctx)
		}
	}
}

// Run executes all registered detectors and reports each detector's leak
// records to the reporter it was registered with. It is invoked by the
// cron-triggered locked task in vcp-core/cmd/main.go.
func (p *Pipeline) Run(ctx context.Context, storage database.Storage) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Leaked resources pipeline started (detectors=%d)", len(p.registrations))
	runStatus := "success"
	defer func() {
		recordMonitoringRun(ctx, runStatus)
	}()

	// Reset per-run reporter state before any detector reports so per-detector
	// reporting accumulates within this run (see beginRun).
	p.beginRun(ctx)

	totalLeaks := 0
	failedDetectors := 0
	var firstReportErr error
	for _, reg := range p.registrations {
		name := reg.detector.Name()
		logger.Infof("Leaked resources: checking detector=%s", name)
		records, err := reg.detector.Detect(ctx, storage)
		if err != nil {
			logger.Errorf("Leaked resources detector %s failed: %v", name, err)
			failedDetectors++
			continue
		}
		logger.Infof("Leaked resources: detector=%s completed, leaks_found=%d", name, len(records))
		totalLeaks += len(records)
		if len(records) == 0 {
			continue
		}
		if reportErr := reg.reporter.Report(ctx, records); reportErr != nil {
			logger.Errorf("Leaked resources: reporter for detector=%s failed: %v", name, reportErr)
			if firstReportErr == nil {
				firstReportErr = reportErr
			}
		}
	}

	if totalLeaks == 0 {
		logger.Info("Leaked resources: no leaks found in this run")
	}

	if firstReportErr != nil {
		runStatus = "error"
		logger.Errorf("Leaked resources monitoring failed (total_leaks=%d, failed_detectors=%d): %v", totalLeaks, failedDetectors, firstReportErr)
		return fmt.Errorf("leaked resources reporting failed: %w", firstReportErr)
	}
	if failedDetectors > 0 {
		runStatus = "partial_error"
	}

	logger.Infof("Leaked resources pipeline finished (total_leaks=%d, failed_detectors=%d, status=%s)", totalLeaks, failedDetectors, runStatus)
	return nil
}

// Run executes the default pipeline with registered detectors (pool: live
// CCFE fetch via FetchCCFEPoolsWorkflow; volume, snapshot in follow-up).
// Invoked by the cron in vcp-core/cmd/main.go. The pool detector now triggers two
// Temporal workflows synchronously per tick — one GetRegionZonesWorkflow
// to enumerate zones in LOCAL_REGION and one FetchCCFEPoolsWorkflow per
// VCP account (which fans the per-(project, location) CCFE list calls out
// as parallel activities inside the workflow) — so CCFE data is always
// fresh and every zone in the region is diffed (including zones VCP has
// no rows in yet). There is no longer a clh_resources cache or a separate
// sync schedule. The CCFE client is still used directly by the
// BackupVaultDetector until that one is migrated to the same pattern.
func Run(ctx context.Context, storage database.Storage, temporalClient client.Client) error {
	p := NewPipeline()

	// Default routing: every built-in detector both logs its leaks and exports
	// the leaked-resources gauge. The log and metrics reporters are shared
	// across detectors (the metrics gauge snapshot is reset once per run in
	// Pipeline.Run and then accumulates each detector's records). A new detector
	// that should only be logged can be registered with just logReporter.
	logReporter := LogReporter{}
	metricsReporter := NewMetricsReporter()
	logAndMetrics := []Reporter{logReporter, metricsReporter}

	zoneFetcher := detectors.NewTemporalZoneFetcher(temporalClient)
	keyLister := detectors.NewProjectLocationLister(storage, zoneFetcher)

	ccfePoolFetcher := detectors.NewTemporalCCFEPoolFetcher(temporalClient)
	p.Register(detectors.NewPoolDetector(ccfePoolFetcher, keyLister), logAndMetrics...)
	p.Register(detectors.NewVolumeOrphanDetector(), logAndMetrics...)
	p.Register(detectors.NewSnapshotOrphanDetector(), logAndMetrics...)

	ccfeBackupVaultFetcher := detectors.NewTemporalCCFEBackupVaultFetcher(temporalClient)
	p.Register(detectors.NewBackupVaultDetector(ccfeBackupVaultFetcher, keyLister), logAndMetrics...)

	// Internal reserved IP detector: invokes GCE Compute API via Temporal workflow on worker pod
	p.Register(detectors.NewInternalReservedIPDetector(detectors.DefaultInternalReservedIPMinAge()), logAndMetrics...)

	// VM detector: invokes GCE Compute API via Temporal workflow, diffs VMs vs active pools
	p.Register(detectors.NewVMDetector(), logAndMetrics...)

	// Disk detector: invokes GCE Compute API via Temporal workflow, diffs disks vs active pools
	p.Register(detectors.NewDiskDetector(), logAndMetrics...)

	return p.Run(ctx, storage)
}
