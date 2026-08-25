package common

import (
	_ "embed"
	"log"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"gopkg.in/yaml.v3"
)

const (
	DefaultShutdownBudget      = 10 * time.Second
	DefaultHTTPShutdownTimeout = 3 * time.Second
)

func loadShutdownDurations() (budget, httpTimeout time.Duration) {
	budget = env.GetDuration("TELEMETRY_SHUTDOWN_BUDGET", DefaultShutdownBudget)
	httpTimeout = env.GetDuration("TELEMETRY_HTTP_SHUTDOWN_TIMEOUT", DefaultHTTPShutdownTimeout)

	if budget <= 0 {
		log.Printf("telemetry: TELEMETRY_SHUTDOWN_BUDGET must be positive, using default %s", DefaultShutdownBudget)
		budget = DefaultShutdownBudget
	}
	if httpTimeout <= 0 {
		log.Printf("telemetry: TELEMETRY_HTTP_SHUTDOWN_TIMEOUT must be positive, using default %s", DefaultHTTPShutdownTimeout)
		httpTimeout = DefaultHTTPShutdownTimeout
	}

	return budget, httpTimeout
}

const (
	Gcp                    string = "gcp"
	Dev                    string = "dev"
	DefaultRegionNumberMap        = `{"africa-south1": "01","asia-east1": "02","asia-east2": "03","asia-northeast1": "04","asia-northeast2": "05","asia-northeast3": "06","asia-south1": "07","asia-south2": "08","asia-southeast1": "09","asia-southeast2": "10","australia-southeast1": "11","australia-southeast2": "12","europe-central2": "13","europe-north1": "14","europe-north2": "15","europe-southwest1": "16","europe-west1": "17","europe-west10": "18","europe-west12": "19","europe-west2": "20","europe-west3": "21","europe-west4": "22","europe-west6": "23","europe-west8": "24","europe-west9": "25","me-central1": "26","me-central2": "27","me-west1": "28","northamerica-northeast1": "29","northamerica-northeast2": "30","northamerica-south1": "31","southamerica-east1": "32","southamerica-west1": "33","us-central1": "34","us-east1": "35","us-east4": "36","us-east5": "37","us-south1": "38","us-west1": "39","us-west2": "40","us-west3": "41","us-west4": "42"}`
)

type TelemetryConfig struct {
	// Server configuration
	OperationBatchSize                           int64
	PusherServiceName                            string
	PusherServiceProject                         string
	PerformanceRootUrl                           string
	UsageRootUrl                                 string
	RegionName                                   string
	EnableVolumeMetrics                          bool
	EnableBackupMetrics                          bool
	EnableBackupBillingMetrics                   bool
	EnableFilesBackupBilling                     bool
	EnableCmekBackupBilling                      bool
	EnableGcbdrBackupBilling                     bool
	EnableGcbdrBmfBilling                        bool
	EnableExpertModeBackupBilling                bool
	EnableCrossRegionBackupBillingMetrics        bool
	EnableSFRCrossRegionRestoreBilling           bool
	EnableReplicationBillingMetrics              bool
	EnableBidirectionalReplicationBillingMetrics bool
	SkipHybridReplicationBaselineBilling         bool
	EnableInRegionReplicationBillingMetrics      bool
	EnableOntapModeReplicationBilling            bool
	EnableAutoTieringBillingMetrics              bool
	EnableONTAPModeAutoTieringBilling            bool
	EnableFilesAutoTieringBilling                bool
	EnableFilesReplicationBillingMetrics         bool
	SFRMetricsEnabled                            bool
	PushBatchSize                                int64
	Environment                                  string
	MaxGoogleBillingPushRetry                    int64
	RetryIntervalSeconds                         int64
	PageSize                                     int32
	NumWorkersPerformance                        int
	NumWorkersUsage                              int
	NumWorkersCollection                         int
	NumWorkersBizOps                             int
	NumWorkersBillingRetry                       int
	GoogleBillingLabelsMaxEntries                int
	PoolVolumeLabelPageSize                      int
	EnableBatchUsageUpdates                      bool // Feature flag for batch usage updates
	ResultUpdateBatchSize                        int
	TargetMinute                                 int
	IntervalBackfillLimitMinutes                 int
	CounterBackfillLimitMinutes                  int
	EnableLargeVolumesBilling                    bool // When true, enables billing for Large Volumes pools (CRR/Backup/AutoTiering)
	EnableTenantQuotaProject                     bool // When true, ListTimeSeries bills Cloud Monitoring quota to the tenant project
	EnableBackupVaultMetrics                     bool
	EnableBackupHistoryFormatter                 bool
	EnableATVolumeBasedPoolBilling               bool
	InjectionWindowMinutes                       int
	EnableCounterFormatter                       bool
	RegionNumberMap                              string
	EnableFreeTrialBilling                       bool
	ShutdownBudget                               time.Duration
	HTTPShutdownTimeout                          time.Duration
}

type MetricItem struct {
	Metric       string `yaml:"metric"`
	ResourceType string `yaml:"resourceType"`
	MetricType   string `yaml:"metricType"`
}
type MetricsConfig struct {
	VolumeMetrics []MetricItem `yaml:"metrics"`
}

//go:embed metricList.yaml
var metricListYAML []byte

func LoadConfig() *TelemetryConfig {
	performanceRootURL := env.GetString("PERFORMANCE_ROOT_URL", "https://servicecontrol.googleapis.com")
	usageRootURL := env.GetString("USAGE_ROOT_URL", "https://servicecontrol.googleapis.com")
	operationBatchSize := env.GetInt64("OPERATION_BATCH_SIZE", 200)
	pusherServiceName := env.GetString("PUSHER_SERVICE_NAME", "autopush-netapp.sandbox.googleapis.com")
	pusherServiceProject := env.GetString("PUSHER_SERVICE_PROJECT", "netapp-au-se1-autopush-sde-tst")
	regionName := env.GetString("LOCAL_REGION", "")
	enableVolumeMetrics := env.GetBool("ENABLE_VOLUME_METRICS", false)
	enableBackupMetrics := env.GetBool("ENABLE_BACKUP_METRICS", false)
	enableBackupBillingMetrics := env.GetBool("ENABLE_BACKUP_BILLING_METRICS", false)
	enableFilesBackupBilling := env.GetBool("ENABLE_FILES_BACKUP_BILLING", false)
	enableCmekBackupBilling := env.GetBool("ENABLE_CMEK_BACKUP_BILLING", false)
	enableGcbdrBackupBilling := env.GetBool("ENABLE_GCBDR_BACKUP_BILLING", false)
	enableGcbdrBmfBilling := env.GetBool("ENABLE_GCBDR_BMF_BILLING", false)
	enableExpertModeBackupBilling := env.GetBool("ENABLE_EXPERT_MODE_BACKUP_BILLING", false)
	enableReplicationBillingMetrics := env.GetBool("ENABLE_REPLICATION_BILLING_METRICS", false)
	enableBidirectionalReplicationBillingMetrics := env.GetBool("ENABLE_BIDIRECTIONAL_REPLICATION_BILLING_METRICS", false)
	skipHybridReplicationBaselineBilling := env.GetBool("SKIP_HYBRID_REPLICATION_BASELINE_BILLING", true)
	enableInRegionReplicationBillingMetrics := env.GetBool("ENABLE_IN_REGION_REPLICATION_BILLING_METRICS", false)
	enableOntapModeReplicationBilling := env.GetBool("ENABLE_ONTAP_MODE_REPLICATION_BILLING", false)
	enableAutoTieringBillingMetrics := env.GetBool("ENABLE_AUTO_TIERING_BILLING_METRICS", false)
	enableONTAPModeAutoTieringBilling := env.GetBool("ENABLE_ONTAP_MODE_AUTOTIERING_BILLING", false)
	enableFilesAutoTieringBilling := env.GetBool("ENABLE_FILES_AUTO_TIERING_BILLING", false)
	enableFilesReplicationBillingMetrics := env.GetBool("ENABLE_FILES_REPLICATION_BILLING_METRICS", false)
	sfrMetricsEnabled := env.GetBool("ENABLE_SFR_METRICS", false)
	enableBackupVaultMetrics := env.GetBool("ENABLE_BACKUP_VAULT_METRICS", false)
	enableCrossRegionBackupBillingMetrics := env.GetBool("ENABLE_CROSS_REGION_BACKUP_BILLING_METRICS", false)
	enableBackupHistoryFormatter := env.GetBool("ENABLE_BACKUP_HISTORY_FORMATTER", false)
	enableSFRCrossRegionRestoreBilling := env.GetBool("ENABLE_SFR_CROSS_REGION_RESTORE_BILLING", false)
	pushBatchSize := env.GetInt64("PUSH_BATCH_SIZE", 1000)
	environment := env.GetString("ENVIRONMENT", Dev)
	maxGoogleBillingPushRetry := env.GetInt64("MAX_GOOGLE_BILLING_PUSH_RETRY", 5)
	retryInterval := env.GetInt64("RETRY_INTERVAL_SECONDS", 300)
	pageSize := env.GetInt64("PAGE_SIZE", 1000)
	numWorkersPerformance := env.GetInt("NUM_WORKERS_PERFORMANCE", 10)
	numWorkersUsage := env.GetInt("NUM_WORKERS_USAGE", 1)
	numWorkersCollection := env.GetInt("NUM_WORKERS_COLLECTION", 10)
	numWorkersBizOps := env.GetInt("NUM_WORKERS_BIZOPS", 10)
	numWorkersBillingRetry := env.GetInt("NUM_WORKERS_BILLING_RETRY", 5)
	googleBillingLabelsMaxEntries := env.GetInt("GOOGLE_BILLING_LABELS_MAX_ENTRIES", 64)
	poolVolumeLabelPageSize := env.GetInt("POOL_VOLUME_LABEL_PAGE_SIZE", 5000)
	enableBatchUsageUpdates := env.GetBool("ENABLE_BATCH_USAGE_UPDATES", false)
	resultUpdateBatchSize := env.GetInt("RESULT_UPDATE_BATCH_SIZE", 100)
	intervalBackfillLimitMinutes := env.GetInt("INTERVAL_BACKFILL_LIMIT_MINUTES", 60)
	counterBackfillLimitMinutes := env.GetInt("COUNTER_BACKFILL_LIMIT_MINUTES", 60)
	targetMinute := env.GetInt("TARGET_MINUTE", 15)
	enableLargeVolumesBilling := env.GetBool("ENABLE_LARGE_VOLUMES_BILLING", false)
	enableTenantQuotaProject := env.GetBool("ENABLE_TENANT_QUOTA_PROJECT", true)
	enableATVolumeBasedPoolBilling := env.GetBool("ENABLE_AT_VOLUME_BASED_POOL_BILLING", true)
	injectionWindowMinutes := env.GetInt("INJECTION_WINDOW_MINUTES", 10)
	enableCounterFormatter := env.GetBool("ENABLE_COUNTER_FORMATTER", false)
	regionNumberMap := env.GetString("REGION_NUMBER_MAP", DefaultRegionNumberMap)
	enableFreeTrialBilling := env.GetBool("ENABLE_FREE_TRIAL_BILLING", false)
	shutdownBudget, httpShutdownTimeout := loadShutdownDurations()

	return &TelemetryConfig{
		PerformanceRootUrl:                           performanceRootURL,
		UsageRootUrl:                                 usageRootURL,
		PusherServiceName:                            pusherServiceName,
		PusherServiceProject:                         pusherServiceProject,
		OperationBatchSize:                           operationBatchSize,
		RegionName:                                   regionName,
		EnableVolumeMetrics:                          enableVolumeMetrics,
		PushBatchSize:                                pushBatchSize,
		Environment:                                  environment,
		MaxGoogleBillingPushRetry:                    maxGoogleBillingPushRetry,
		PageSize:                                     int32(pageSize),
		EnableBackupMetrics:                          enableBackupMetrics,
		EnableBackupBillingMetrics:                   enableBackupBillingMetrics,
		EnableFilesBackupBilling:                     enableFilesBackupBilling,
		EnableCmekBackupBilling:                      enableCmekBackupBilling,
		EnableGcbdrBackupBilling:                     enableGcbdrBackupBilling,
		EnableGcbdrBmfBilling:                        enableGcbdrBmfBilling,
		EnableExpertModeBackupBilling:                enableExpertModeBackupBilling,
		EnableCrossRegionBackupBillingMetrics:        enableCrossRegionBackupBillingMetrics,
		EnableSFRCrossRegionRestoreBilling:           enableSFRCrossRegionRestoreBilling,
		EnableReplicationBillingMetrics:              enableReplicationBillingMetrics,
		EnableBidirectionalReplicationBillingMetrics: enableBidirectionalReplicationBillingMetrics,
		SkipHybridReplicationBaselineBilling:         skipHybridReplicationBaselineBilling,
		EnableInRegionReplicationBillingMetrics:      enableInRegionReplicationBillingMetrics,
		EnableOntapModeReplicationBilling:            enableOntapModeReplicationBilling,
		EnableAutoTieringBillingMetrics:              enableAutoTieringBillingMetrics,
		EnableONTAPModeAutoTieringBilling:            enableONTAPModeAutoTieringBilling,
		EnableFilesAutoTieringBilling:                enableFilesAutoTieringBilling,
		EnableFilesReplicationBillingMetrics:         enableFilesReplicationBillingMetrics,
		SFRMetricsEnabled:                            sfrMetricsEnabled,
		EnableBackupVaultMetrics:                     enableBackupVaultMetrics,
		EnableBackupHistoryFormatter:                 enableBackupHistoryFormatter,
		NumWorkersPerformance:                        numWorkersPerformance,
		NumWorkersUsage:                              numWorkersUsage,
		NumWorkersCollection:                         numWorkersCollection,
		NumWorkersBizOps:                             numWorkersBizOps,
		NumWorkersBillingRetry:                       numWorkersBillingRetry,
		GoogleBillingLabelsMaxEntries:                googleBillingLabelsMaxEntries,
		RetryIntervalSeconds:                         retryInterval,
		PoolVolumeLabelPageSize:                      poolVolumeLabelPageSize,
		EnableBatchUsageUpdates:                      enableBatchUsageUpdates,
		ResultUpdateBatchSize:                        resultUpdateBatchSize,
		TargetMinute:                                 targetMinute,
		IntervalBackfillLimitMinutes:                 intervalBackfillLimitMinutes,
		CounterBackfillLimitMinutes:                  counterBackfillLimitMinutes,
		EnableLargeVolumesBilling:                    enableLargeVolumesBilling,
		EnableTenantQuotaProject:                     enableTenantQuotaProject,
		EnableATVolumeBasedPoolBilling:               enableATVolumeBasedPoolBilling,
		InjectionWindowMinutes:                       injectionWindowMinutes,
		EnableCounterFormatter:                       enableCounterFormatter,
		RegionNumberMap:                              regionNumberMap,
		EnableFreeTrialBilling:                       enableFreeTrialBilling,
		ShutdownBudget:                               shutdownBudget,
		HTTPShutdownTimeout:                          httpShutdownTimeout,
	}
}

func LoadMetricsConfigFromBytes() *MetricsConfig {
	var config MetricsConfig
	if err := yaml.Unmarshal(metricListYAML, &config); err != nil {
		log.Fatalf("Failed to unmarshal metrics config: %v", err)
	}

	return &config
}
