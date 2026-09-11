package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	harvestPollerOrphanCountMetric   = "vcp_harvest_poller_orphan_count"
	harvestPollerMismatchCountMetric = "vcp_harvest_poller_db_pvc_mismatch_count"
	harvestPollerRetryBacklogMetric  = "vcp_harvest_poller_retry_backlog_count"
	harvestPollerLastSuccessMetric   = "vcp_harvest_poller_reconcile_last_success_timestamp_seconds"
)

var (
	harvestPollerOrphanCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: harvestPollerOrphanCountMetric,
			Help: "Current number of Harvest poller orphan YAMLs by reconciliation stage.",
		},
		[]string{"stage"},
	)
	harvestPollerMismatchCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: harvestPollerMismatchCountMetric,
			Help: "Current number of Harvest poller DB vs config (farm PVC or bucket) mismatches by kind and reconciliation stage. Orphans with no active mapping use vcp_harvest_poller_orphan_count.",
		},
		[]string{"kind", "stage"},
	)
	harvestPollerRetryBacklog = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: harvestPollerRetryBacklogMetric,
			Help: "Current number of Harvest poller retries by state.",
		},
		[]string{"state"},
	)
	harvestPollerLastSuccess = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: harvestPollerLastSuccessMetric,
			Help: "Unix timestamp of the start of the latest successfully committed Harvest poller reconciliation.",
		},
		[]string{},
	)
)

// RegisterHarvestMetrics registers the Harvest collectors.
func RegisterHarvestMetrics() error {
	var err error
	if harvestPollerOrphanCount, err = registerHarvestGaugeVec(harvestPollerOrphanCount); err != nil {
		return err
	}
	if harvestPollerMismatchCount, err = registerHarvestGaugeVec(harvestPollerMismatchCount); err != nil {
		return err
	}
	if harvestPollerRetryBacklog, err = registerHarvestGaugeVec(harvestPollerRetryBacklog); err != nil {
		return err
	}
	if harvestPollerLastSuccess, err = registerHarvestGaugeVec(harvestPollerLastSuccess); err != nil {
		return err
	}
	return nil
}

func registerHarvestGaugeVec(collector *prometheus.GaugeVec) (*prometheus.GaugeVec, error) {
	if err := prometheus.Register(collector); err != nil {
		if alreadyRegistered, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := alreadyRegistered.ExistingCollector.(*prometheus.GaugeVec); ok {
				if metricDescriptorsMatch(existing, collector) {
					return existing, nil
				}
				return collector, fmt.Errorf("conflicting Harvest GaugeVec registration")
			}
		}
		return collector, fmt.Errorf("failed to register Harvest GaugeVec: %w", err)
	}
	return collector, nil
}

func metricDescriptorsMatch(left, right prometheus.Collector) bool {
	leftDescriptions := describeMetric(left)
	rightDescriptions := describeMetric(right)
	if len(leftDescriptions) != len(rightDescriptions) {
		return false
	}
	for i := range leftDescriptions {
		if leftDescriptions[i] != rightDescriptions[i] {
			return false
		}
	}
	return true
}

func describeMetric(collector prometheus.Collector) []string {
	descriptions := make([]string, 0, 1)
	ch := make(chan *prometheus.Desc, 10)
	collector.Describe(ch)
	close(ch)
	for description := range ch {
		descriptions = append(descriptions, description.String())
	}
	return descriptions
}
