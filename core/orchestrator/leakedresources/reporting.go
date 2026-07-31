package leakedresources

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/leakedresources/model"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// Reporter receives a detector's leak records from the pipeline and reports them
// (e.g. logs, metrics, GCS). The pipeline calls Report once per detector per run
// with that detector's records.
type Reporter interface {
	Report(ctx context.Context, records []model.LeakRecord) error
}

// runAware is an optional interface for reporters that hold per-run state which
// must be reset at the start of each pipeline run (e.g. MetricsReporter's gauge
// snapshot). The pipeline calls BeginRun at the start of a run, before any
// detector reports, so per-detector reporting accumulates within a run rather
// than each detector clobbering the previous one's state. Reporters without
// per-run state (e.g. LogReporter) simply do not implement it.
//
// BeginRun MUST be idempotent: when a reporter instance is shared across
// multiple detector registrations (as the default metrics reporter is), the
// pipeline may call BeginRun more than once per run. All such calls happen
// before any Report, so resetting state (the only intended action) is safe to
// repeat.
type runAware interface {
	BeginRun(ctx context.Context)
}

// LogReporter writes leak records to the application logger (summary + one line per record).
type LogReporter struct{}

// Report logs a summary and each leak record. internal_reserved_ip leaks use a dedicated prefix
// so they are easy to find in logs (subnet capacity / Compute Address resources).
func (LogReporter) Report(ctx context.Context, records []model.LeakRecord) error {
	logger := util.GetLogger(ctx)
	if len(records) == 0 {
		logger.Info("Leaked resources monitoring: no leaked resources detected")
		return nil
	}
	byType := countLeakRecordsByType(records)
	logger.Warnf("Leaked resources monitoring: found %d leaked resource(s); by type: %s", len(records), formatLeakCountsByType(byType))
	for _, r := range records {
		logSingleLeakRecord(logger, r)
	}
	return nil
}

func countLeakRecordsByType(records []model.LeakRecord) map[model.ResourceType]int {
	m := make(map[model.ResourceType]int)
	for _, r := range records {
		m[r.ResourceType]++
	}
	return m
}

func formatLeakCountsByType(counts map[model.ResourceType]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for rt, n := range counts {
		if n > 0 {
			keys = append(keys, string(rt)+":"+strconv.Itoa(n))
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}

type warnLogger interface {
	Warnf(format string, args ...any)
}

func logSingleLeakRecord(logger warnLogger, r model.LeakRecord) {
	if r.ResourceType == model.ResourceTypeInternalReservedIP {
		extraTail := formatExtraKeyValuesExclude(r.Extra, "ip", "subnet", "pool_uuids")
		msg := "Leaked internal_reserved_ip (subnet capacity): name=%s id=%s ip=%s subnet=%s project=%s region=%s pool_uuids=%s reason=%s"
		args := []any{
			r.ResourceName,
			r.ResourceID,
			extraGet(r.Extra, "ip"),
			extraGet(r.Extra, "subnet"),
			r.ProjectID,
			r.Region,
			extraGet(r.Extra, "pool_uuids"),
			r.Reason,
		}
		if extraTail != "" {
			msg += " %s"
			args = append(args, extraTail)
		}
		logger.Warnf(msg, args...)
		return
	}
	msg := "  Leaked %s: id=%s name=%s project=%s region=%s reason=%s"
	args := []any{r.ResourceType, r.ResourceID, r.ResourceName, r.ProjectID, r.Region, r.Reason}
	if len(r.Extra) > 0 {
		msg += " %s"
		args = append(args, formatExtraKeyValues(r.Extra))
	}
	logger.Warnf(msg, args...)
}

func extraGet(m map[string]string, key string) string {
	if m == nil {
		return ""
	}
	return m[key]
}

// formatExtraKeyValues formats Extra as "k=v" pairs sorted by key (stable log lines).
func formatExtraKeyValues(extra map[string]string) string {
	return formatExtraKeyValuesExclude(extra)
}

// formatExtraKeyValuesExclude omits given keys (e.g. already printed on dedicated internal_reserved_ip line).
func formatExtraKeyValuesExclude(extra map[string]string, exclude ...string) string {
	if len(extra) == 0 {
		return ""
	}
	omit := make(map[string]struct{}, len(exclude))
	for _, k := range exclude {
		omit[k] = struct{}{}
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		if _, skip := omit[k]; skip {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+extra[k])
	}
	return strings.Join(parts, " ")
}
