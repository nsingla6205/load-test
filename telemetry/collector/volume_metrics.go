package collector

import (
	"context"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	datamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/performance"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// TimeSeriesIterator abstracts the Next method for iterating time series.
type TimeSeriesIterator interface {
	Next() (*monitoringpb.TimeSeries, error)
}

type MetricClientWrapper struct {
	Client *monitoring.MetricClient
}

func NewMetricClientWrapper(client *monitoring.MetricClient) *MetricClientWrapper {
	return &MetricClientWrapper{Client: client}
}

func (w *MetricClientWrapper) ListTimeSeries(ctx context.Context, req *monitoringpb.ListTimeSeriesRequest) TimeSeriesIterator {
	it := w.Client.ListTimeSeries(ctx, req)
	return it
}
func (w *MetricClientWrapper) IsNil() bool {
	return w.Client == nil
}

type VolumeMetricsProvider interface {
	GetVolumeMetrics(context.Context, log.Logger, time.Time) error
	CollectProjectMetrics(ctx context.Context, logger log.Logger, projectID string, timestamp time.Time) ([]datamodel.HydratedMetrics, error)
	GetClient() MonitoringClient
	SetJobQueue(q *utils.JobQueue)
	RefreshTimeWindow()
}
type TenantProjectProvider interface {
	GetTenantProjects(ctx context.Context, logger log.Logger) ([]string, error)
}
type MonitoringClient interface {
	ListTimeSeries(ctx context.Context, req *monitoringpb.ListTimeSeriesRequest) TimeSeriesIterator
}
type GoogleTenantProjectProvider struct {
	vcpDatastore database.Storage
}

func NewGoogleTenantProjectProvider(ds database.Storage) *GoogleTenantProjectProvider {
	return &GoogleTenantProjectProvider{vcpDatastore: ds}
}
func (p *GoogleVolumeMetricsProvider) GetClient() MonitoringClient {
	return p.client
}

func (g *GoogleVolumeMetricsProvider) SetJobQueue(q *utils.JobQueue) {
	g.jobQueue = q
}

func (p *GoogleVolumeMetricsProvider) RefreshTimeWindow() {
	p.startTime = time.Now().Add(-5 * time.Minute)
	p.endTime = time.Now()
}

type GoogleVolumeMetricsProvider struct {
	tenantProjectProvider TenantProjectProvider
	client                MonitoringClient
	startTime             time.Time
	endTime               time.Time
	metrics               []common.MetricItem
	jobQueue              *utils.JobQueue
	MetricList            []datamodel.HydratedMetrics
	googleSink            *performance.GoogleSink
}

func NewGoogleVolumeMetricsProvider(tenantProjectProvider TenantProjectProvider, client MonitoringClient, VolumeMetrics []common.MetricItem, googleSink *performance.GoogleSink) *GoogleVolumeMetricsProvider {
	return &GoogleVolumeMetricsProvider{
		tenantProjectProvider: tenantProjectProvider,
		client:                client,
		startTime:             time.Now().Add(-5 * time.Minute),
		endTime:               time.Now(),
		metrics:               VolumeMetrics,
		googleSink:            googleSink,
	}
}

func NewGoogleProvider(tenantProjectProvider TenantProjectProvider, client MonitoringClient, VolumeMetrics []common.MetricItem, googleSink *performance.GoogleSink) VolumeMetricsProvider {
	return NewGoogleVolumeMetricsProvider(tenantProjectProvider, client, VolumeMetrics, googleSink)
}
