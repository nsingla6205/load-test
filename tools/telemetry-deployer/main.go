package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	cloudscheduler "google.golang.org/api/cloudscheduler/v1"
	"google.golang.org/api/option"
	cloudrun "google.golang.org/api/run/v2"
)

type DeploymentConfig struct {
	ServiceName         string
	Image               string
	ProjectID           string
	Region              string
	SchedulerRegion     string // Region for Cloud Scheduler (defaults to Region if not specified)
	MinInstances        int64
	MaxInstances        int64
	TelemetryCPU        string // CPU configuration for telemetry container (e.g., "2", "1000m")
	TelemetryMemory     string // Memory configuration for telemetry container (e.g., "2Gi", "512Mi")
	SqlProxyCPU         string // CPU configuration for SQL proxy container (e.g., "1", "500m")
	SqlProxyMemory      string // Memory configuration for SQL proxy container (e.g., "512Mi", "256Mi")
	EnvVars             map[string]string
	EnableScheduler     bool
	SchedulerCron       string
	ServiceURL          string
	ServiceAccountName  string
	Network             string
	Subnetwork          string
	VPCConnector        string
	CloudSQLInstances   []string // List of Cloud SQL instance connection names
	CloudSQLImage       string
	OTELImage           string
	MonitoringProjectID string // GCP project ID used by the OTEL collector's Google Cloud exporter for monitoring data
	CustomPrefix        string // Custom prefix for resource names used by the OTEL collector's Google Cloud exporter
	OTELCPU             string // CPU configuration for otel container (e.g., "1", "500m")
	OTELMemory          string // Memory configuration for otel container (e.g., "512Mi", "256Mi")
	OTELConfigSecret    string // Secret Manager secret name for OTEL config (if empty, uses env var approach)
}

func main() {
	// Command line flags
	var (
		serviceName         = flag.String("service", "telemetry-service", "Cloud Run service name")
		image               = flag.String("image", "us-docker.pkg.dev/gcnv-artifact-registry-nonprod/vcp-container-images-us/telemetry:latest", "Container image URL")
		projectID           = flag.String("project", "", "GCP project ID")
		region              = flag.String("region", "", "GCP region")
		schedulerRegion     = flag.String("scheduler-region", "", "GCP region for Cloud Scheduler (defaults to region if not specified)")
		network             = flag.String("network", "", "Network name")
		subnet              = flag.String("subnet", "", "Subnetwork name")
		vpcConnector        = flag.String("vpc-connector", "", "VPC Access connector")
		minInstances        = flag.Int64("min-instances", 0, "Minimum number of instances")
		maxInstances        = flag.Int64("max-instances", 1, "Maximum number of instances (0 for no limit)")
		telemetryCPU        = flag.String("telemetry-cpu", "2", "CPU allocation for telemetry container (e.g., '2', '1000m')")
		telemetryMemory     = flag.String("telemetry-memory", "2Gi", "Memory allocation for telemetry container (e.g., '2Gi', '512Mi')")
		sqlProxyCPU         = flag.String("sql-proxy-cpu", "1", "CPU allocation for SQL proxy container (e.g., '1', '500m')")
		sqlProxyMemory      = flag.String("sql-proxy-memory", "512Mi", "Memory allocation for SQL proxy container (e.g., '512Mi', '256Mi')")
		envVarsFlag         = flag.String("env-vars", "", "Environment variables in format KEY1=VALUE1,KEY2=VALUE2")
		cloudSQLInstances   = flag.String("cloud-sql-instances", "", "Comma-separated list of Cloud SQL instance connection names (project:region:instance)")
		enableScheduler     = flag.Bool("enable-scheduler", true, "Enable Cloud Scheduler to invoke the service")
		serviceAccountName  = flag.String("service-account-name", "", "Cloud Run service account name")
		cloudSQLImage       = flag.String("cloud-sql-image", "", "Cloud SQL image URL")
		otelImage           = flag.String("otel-image", "", "OpenTelemetry container image URL")
		otelCPU             = flag.String("otel-cpu", "1", "CPU allocation for OpenTelemetry container (e.g., '1', '500m')")
		otelMemory          = flag.String("otel-memory", "512Mi", "Memory allocation for OpenTelemetry container (e.g., '512Mi', '256Mi')")
		monitoringProjectID = flag.String("monitoring-project-id", "", "GCP project ID used by the OTEL collector's Google Cloud exporter for monitoring data")
		customPrefix        = flag.String("custom-prefix", "", "Custom prefix for resource names used by the OTEL collector's Google Cloud exporter")
		otelConfigSecret    = flag.String("otel-config-secret", "", "Secret Manager secret name for OTEL config (REQUIRED for distroless images like opentelemetry-collector-contrib; config will be mounted as volume at /conf/relay.yaml)")
	)
	flag.Parse()

	// Parse environment variables
	envVars := parseEnvVars(*envVarsFlag)

	// Parse Cloud SQL instances
	var sqlInstances []string
	if *cloudSQLInstances != "" {
		sqlInstances = strings.Split(*cloudSQLInstances, ",")
		for i, instance := range sqlInstances {
			sqlInstances[i] = strings.TrimSpace(instance)
		}
	}

	// Add default environment variables from the gcloud command
	if len(envVars) == 0 {
		envVars = getDefaultEnvVars()
	} else {
		// Merge with defaults to ensure CLOUD_SQL_IAM_AUTH_ENABLED is always available
		defaults := getDefaultEnvVars()
		for key, value := range defaults {
			if _, exists := envVars[key]; !exists {
				envVars[key] = value
			}
		}
	}

	// Set scheduler region to service region if not specified (backward compatibility)
	schedulerRegionValue := *schedulerRegion
	if schedulerRegionValue == "" {
		schedulerRegionValue = *region
	}

	// Note: If using a distroless OTEL image (like opentelemetry-collector-contrib) without --otel-config-secret,
	// the container will fail because distroless images don't have /bin/sh.
	// To use env var approach without secret, ensure your OTEL image has /bin/sh available.

	config := &DeploymentConfig{
		ServiceName:         *serviceName,
		Image:               *image,
		ProjectID:           *projectID,
		Region:              *region,
		SchedulerRegion:     schedulerRegionValue,
		MinInstances:        *minInstances,
		MaxInstances:        *maxInstances,
		TelemetryCPU:        *telemetryCPU,
		TelemetryMemory:     *telemetryMemory,
		SqlProxyCPU:         *sqlProxyCPU,
		SqlProxyMemory:      *sqlProxyMemory,
		EnvVars:             envVars,
		EnableScheduler:     *enableScheduler,
		SchedulerCron:       "*/5 * * * *",
		ServiceAccountName:  *serviceAccountName,
		Network:             *network,
		Subnetwork:          *subnet,
		VPCConnector:        *vpcConnector,
		CloudSQLInstances:   sqlInstances,
		CloudSQLImage:       *cloudSQLImage,
		OTELImage:           *otelImage,
		OTELCPU:             *otelCPU,
		OTELMemory:          *otelMemory,
		MonitoringProjectID: *monitoringProjectID,
		CustomPrefix:        *customPrefix,
		OTELConfigSecret:    *otelConfigSecret,
	}

	if err := deployCloudRunService(context.Background(), config); err != nil {
		log.Fatalf("Failed to deploy Cloud Run service: %v", err)
	}

	log.Printf("Successfully deployed %s to %s in project %s\n", config.ServiceName, config.Region, config.ProjectID)

	// Deploy Cloud Scheduler if enabled
	if config.EnableScheduler {
		// Get the service URL after deployment

		log.Printf("Deploying 5-minutely Cloud Scheduler (region: %s)\n", config.SchedulerRegion)
		serviceURL, err := getCloudRunServiceURL(context.Background(), config)
		if err != nil {
			log.Panicf("Failed to get Cloud Run service URL: %v", err)
		}
		config.ServiceURL = serviceURL + "/v1/performance"
		config.ServiceName = "performance"
		config.SchedulerCron = "*/5 * * * *" // Every 5 minutes

		if err := deployCloudScheduler(context.Background(), config); err != nil {
			log.Panicf("Failed to deploy Cloud Scheduler: %v", err)
		}

		log.Printf("Successfully deployed Cloud Scheduler for service %s with cron: %s in region: %s\n", config.ServiceName, config.SchedulerCron, config.SchedulerRegion)

		log.Printf("Deploying hourly Cloud Scheduler (region: %s)\n", config.SchedulerRegion)
		config.SchedulerCron = "15 * * * *"
		config.ServiceName = "usage"
		config.ServiceURL = serviceURL + "/v1/usage"

		if err := deployCloudScheduler(context.Background(), config); err != nil {
			log.Panicf("Failed to deploy Cloud Scheduler: %v", err)
		}

		log.Printf("Successfully deployed Cloud Scheduler for service %s with cron: %s in region: %s\n", config.ServiceName, config.SchedulerCron, config.SchedulerRegion)

		log.Printf("Deploying Daily BizOps Cloud Scheduler (region: %s)\n", config.SchedulerRegion)
		config.SchedulerCron = "0 10 * * *"
		config.ServiceName = "bizops"
		config.ServiceURL = serviceURL + "/v1/generateReport"

		if err := deployCloudScheduler(context.Background(), config); err != nil {
			log.Panicf("Failed to deploy Cloud Scheduler: %v", err)
		}

		log.Printf("Successfully deployed Cloud Scheduler for service %s with cron: %s in region: %s\n", config.ServiceName, config.SchedulerCron, config.SchedulerRegion)
	}
}

func deployCloudRunService(ctx context.Context, config *DeploymentConfig) error {
	// Initialize Cloud Run client
	client, err := cloudrun.NewService(ctx, option.WithScopes("https://www.googleapis.com/auth/cloud-platform"))
	if err != nil {
		return fmt.Errorf("failed to create Cloud Run client: %w", err)
	}

	// Convert environment variables to Cloud Run format
	envVars := make([]*cloudrun.GoogleCloudRunV2EnvVar, 0, len(config.EnvVars)+2) // +2 for the secrets

	// Check if IAM authentication is enabled
	iamAuthEnabled := false
	if iamAuthEnabledValue, ok := config.EnvVars["CLOUD_SQL_IAM_AUTH_ENABLED"]; ok {
		iamAuthEnabled = strings.EqualFold(strings.TrimSpace(iamAuthEnabledValue), "true")
	}

	// Define secrets that should be fetched from Secret Manager
	// Skip password secrets when IAM authentication is enabled
	secretKeys := map[string]string{}
	if !iamAuthEnabled {
		secretKeys["DB_PASSWORD"] = "vcp-autopush-tst-au-se1-db-password-key"
		secretKeys["METRICS_DB_PASSWORD"] = "vcp-autopush-tst-au-se1-metrics-db-password-key"
	}

	for key, value := range config.EnvVars {
		// Skip commented out variables (starting with ^#^)
		if strings.HasPrefix(key, "^#^") {
			continue
		}

		// Skip password secrets when IAM authentication is enabled
		if iamAuthEnabled && (key == "DB_PASSWORD" || key == "METRICS_DB_PASSWORD") {
			continue
		}

		// Skip if this variable should be configured as a secret
		if secretName, isExists := secretKeys[key]; isExists {
			// Only add secret reference if value is not empty
			if value == "" {
				// Use the default secret name from secretKeys map
				value = secretName
			}
			// Add as secret reference instead of plain value
			envVars = append(envVars, &cloudrun.GoogleCloudRunV2EnvVar{
				Name: key,
				ValueSource: &cloudrun.GoogleCloudRunV2EnvVarSource{
					SecretKeyRef: &cloudrun.GoogleCloudRunV2SecretKeySelector{
						Secret:  value,
						Version: "latest",
					},
				},
			})
			continue
		}

		envVars = append(envVars, &cloudrun.GoogleCloudRunV2EnvVar{
			Name:  key,
			Value: value,
		})
	}

	// Create container configuration with resources
	container := &cloudrun.GoogleCloudRunV2Container{
		Image: config.Image,
		Env:   envVars,
		Ports: []*cloudrun.GoogleCloudRunV2ContainerPort{
			{
				ContainerPort: 8080,
			},
		},
		Resources: &cloudrun.GoogleCloudRunV2ResourceRequirements{
			// Set cpuIdle to false for instance-based billing
			CpuIdle: false,
			Limits: map[string]string{
				"cpu":    config.TelemetryCPU,
				"memory": config.TelemetryMemory,
			},
		},
	}

	if len(config.CloudSQLInstances) == 0 {
		log.Panicf("Cloud SQL Instances are empty")
	}

	cloudSqlInstance := config.CloudSQLInstances[0]
	// SQL Proxy Container Configuration to connect to Cloud SQL instances
	sqlProxyArgs := []string{
		"--private-ip",
		"--structured-logs",
		"--port=5432",
	}
	// Add IAM authentication flag if IAM is enabled
	if iamAuthEnabled {
		sqlProxyArgs = append(sqlProxyArgs, "--auto-iam-authn")
	}
	sqlProxyArgs = append(sqlProxyArgs, cloudSqlInstance)

	sqlProxyContainer := &cloudrun.GoogleCloudRunV2Container{
		Name:  "vsa-cloud-sql-proxy",
		Image: config.CloudSQLImage,
		Args:  sqlProxyArgs,
		Resources: &cloudrun.GoogleCloudRunV2ResourceRequirements{
			// Set cpuIdle to false for instance-based billing
			CpuIdle: false,
			Limits: map[string]string{
				"cpu":    config.SqlProxyCPU,
				"memory": config.SqlProxyMemory,
			},
		},
	}

	otelContainer, otelVolume := createOtelCollectorContainer(config)

	// Create service template with scaling
	template := &cloudrun.GoogleCloudRunV2RevisionTemplate{
		Containers: []*cloudrun.GoogleCloudRunV2Container{sqlProxyContainer, container, otelContainer},
		Scaling: &cloudrun.GoogleCloudRunV2RevisionScaling{
			MinInstanceCount: config.MinInstances,
			MaxInstanceCount: config.MaxInstances,
		},
		ServiceAccount: config.ServiceAccountName,
	}

	// Add volume for OTEL config if using Secret Manager
	if otelVolume != nil {
		template.Volumes = []*cloudrun.GoogleCloudRunV2Volume{otelVolume}
	}

	// Configure VPC access
	vpcAccess := &cloudrun.GoogleCloudRunV2VpcAccess{}
	if config.VPCConnector != "" {
		vpcAccess.Connector = config.VPCConnector
	} else if config.Subnetwork != "" {
		vpcAccess.NetworkInterfaces = append(vpcAccess.NetworkInterfaces,
			&cloudrun.GoogleCloudRunV2NetworkInterface{
				Network:    config.Network,
				Subnetwork: config.Subnetwork,
			})
	} else {
		log.Printf("No VPC connector or subnet specified in config")
		return fmt.Errorf("either Subnetwork or VPC Connector must be specified")
	}
	template.VpcAccess = vpcAccess

	// Create the service
	service := &cloudrun.GoogleCloudRunV2Service{
		Template: template,
		Ingress:  "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER",
		Labels: map[string]string{
			"managed-by": "telemetry-deployer",
		},
	}

	parent := fmt.Sprintf("projects/%s/locations/%s", config.ProjectID, config.Region)

	// Check if service already exists
	existingService, err := client.Projects.Locations.Services.Get(
		fmt.Sprintf("%s/services/%s", parent, config.ServiceName),
	).Do()

	if err != nil {
		// Service doesn't exist, create it
		log.Printf("Creating new Cloud Run service: %s\n", config.ServiceName)
		operation, err := client.Projects.Locations.Services.Create(parent, service).
			ServiceId(config.ServiceName).Do()
		if err != nil {
			return fmt.Errorf("failed to create Cloud Run service: %w", err)
		}
		log.Printf("Service creation operation: %s\n", operation.Name)

		// Wait for the creation operation to complete
		if err := waitForOperation(ctx, client, operation.Name); err != nil {
			return fmt.Errorf("failed to wait for service creation: %w", err)
		}
		log.Printf("Service creation completed successfully\n")
	} else {
		// Service exists, update it
		log.Printf("Updating existing Cloud Run service: %s\n", config.ServiceName)

		// Update the existing service template
		existingService.Template = template

		operation, err := client.Projects.Locations.Services.Patch(
			fmt.Sprintf("%s/services/%s", parent, config.ServiceName),
			existingService,
		).Do()
		if err != nil {
			return fmt.Errorf("failed to update Cloud Run service: %w", err)
		}
		log.Printf("Service update operation: %s\n", operation.Name)

		// Wait for the update operation to complete
		if err := waitForOperation(ctx, client, operation.Name); err != nil {
			return fmt.Errorf("failed to wait for service update: %w", err)
		}
		log.Printf("Service update completed successfully\n")
	}

	if existingService == nil {
		// Fetch the newly created service details
		existingService, err = client.Projects.Locations.Services.Get(
			fmt.Sprintf("%s/services/%s", parent, config.ServiceName),
		).Do()
		if err != nil {
			log.Printf("Warning: Failed to get service info after deployment: %v\n", err)
		}
	}
	if existingService != nil {
		printCloudRunServiceInfo(existingService)
	}

	// Update traffic to route 100% to latest revision
	log.Printf("Updating traffic to latest revision\n")
	trafficUpdate := &cloudrun.GoogleCloudRunV2Service{
		Traffic: []*cloudrun.GoogleCloudRunV2TrafficTarget{
			{
				Type:    "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST",
				Percent: 100,
			},
		},
	}

	_, err = client.Projects.Locations.Services.Patch(
		fmt.Sprintf("%s/services/%s", parent, config.ServiceName),
		trafficUpdate,
	).UpdateMask("traffic").Do()
	if err != nil {
		return fmt.Errorf("failed to update traffic: %w", err)
	}

	return nil
}

func parseEnvVars(envVarsStr string) map[string]string {
	envVars := make(map[string]string)
	if envVarsStr == "" {
		return envVars
	}

	pairs := strings.Split(envVarsStr, ",")
	for _, pair := range pairs {
		if kv := strings.SplitN(pair, "=", 2); len(kv) == 2 {
			envVars[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return envVars
}

// waitForOperation waits for a Cloud Run operation to complete
func waitForOperation(ctx context.Context, client *cloudrun.Service, operationName string) error {
	log.Printf("Waiting for operation to complete: %s\n", operationName)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Check operation status
			op, err := client.Projects.Locations.Operations.Get(operationName).Do()
			if err != nil {
				return fmt.Errorf("failed to get operation status: %w", err)
			}

			if op.Done {
				if op.Error != nil {
					return fmt.Errorf("operation failed: %s", op.Error.Message)
				}
				log.Printf("Operation completed successfully\n")
				return nil
			}

			// Wait before checking again
			time.Sleep(5 * time.Second)
		}
	}
}

func getDefaultEnvVars() map[string]string {
	getEnvOrDefault := func(key, defaultValue string) string {
		if value := os.Getenv(key); value != "" {
			return value
		}
		return defaultValue
	}

	return map[string]string{
		"ENV":                                              getEnvOrDefault("ENV", "local"),
		"GCP_PROXY_PORT":                                   getEnvOrDefault("GCP_PROXY_PORT", "8090"),
		"DB_TYPE":                                          getEnvOrDefault("DB_TYPE", "postgres"),
		"DB_HOST":                                          getEnvOrDefault("DB_HOST", "127.0.0.1"),
		"DB_PORT":                                          getEnvOrDefault("DB_PORT", "5432"),
		"DB_USER":                                          getEnvOrDefault("DB_USER", "postgres"),
		"DB_PASSWORD":                                      getEnvOrDefault("DB_PASSWORD", ""),
		"DB_NAME":                                          getEnvOrDefault("DB_NAME", "vcp"),
		"DB_SSL_MODE":                                      getEnvOrDefault("DB_SSL_MODE", "disable"),
		"DB_TIMEZONE":                                      getEnvOrDefault("DB_TIMEZONE", "UTC"),
		"DB_MAX_OPEN_CONNS":                                getEnvOrDefault("DB_MAX_OPEN_CONNS", "25"),
		"DB_MAX_IDLE_CONNS":                                getEnvOrDefault("DB_MAX_IDLE_CONNS", "25"),
		"DB_CONN_MAX_LIFETIME":                             getEnvOrDefault("DB_CONN_MAX_LIFETIME", "1h"),
		"DB_ADMIN_USER":                                    getEnvOrDefault("DB_ADMIN_USER", "postgres"),
		"METRICS_DB_TYPE":                                  getEnvOrDefault("METRICS_DB_TYPE", "postgres"),
		"METRICS_DB_HOST":                                  getEnvOrDefault("METRICS_DB_HOST", "127.0.0.1"),
		"METRICS_DB_PORT":                                  getEnvOrDefault("METRICS_DB_PORT", "5432"),
		"METRICS_DB_USER":                                  getEnvOrDefault("METRICS_DB_USER", "metrics"),
		"METRICS_DB_PASSWORD":                              getEnvOrDefault("METRICS_DB_PASSWORD", ""),
		"METRICS_DB_NAME":                                  getEnvOrDefault("METRICS_DB_NAME", "metrics"),
		"METRICS_DB_SSL_MODE":                              getEnvOrDefault("METRICS_DB_SSL_MODE", "disable"),
		"METRICS_DB_TIMEZONE":                              getEnvOrDefault("METRICS_DB_TIMEZONE", "UTC"),
		"METRICS_DB_MAX_OPEN_CONNS":                        getEnvOrDefault("METRICS_DB_MAX_OPEN_CONNS", "25"),
		"METRICS_DB_MAX_IDLE_CONNS":                        getEnvOrDefault("METRICS_DB_MAX_IDLE_CONNS", "25"),
		"INTERVAL_BACKFILL_LIMIT_MINUTES":                  getEnvOrDefault("INTERVAL_BACKFILL_LIMIT_MINUTES", "60"),
		"COUNTER_BACKFILL_LIMIT_MINUTES":                   getEnvOrDefault("COUNTER_BACKFILL_LIMIT_MINUTES", "60"),
		"METRICS_DB_CONN_MAX_LIFETIME":                     getEnvOrDefault("METRICS_DB_CONN_MAX_LIFETIME", "1h"),
		"ROOT_URL":                                         getEnvOrDefault("ROOT_URL", "https://servicecontrol.googleapis.com"),
		"OPERATION_BATCH_SIZE":                             getEnvOrDefault("OPERATION_BATCH_SIZE", "200"),
		"PUSHER_SERVICE_NAME":                              getEnvOrDefault("PUSHER_SERVICE_NAME", "autopush-netapp.sandbox.googleapis.com"),
		"PUSHER_SERVICE_PROJECT":                           getEnvOrDefault("PUSHER_SERVICE_PROJECT", "netapp-au-se1-autopush-sde-tst"),
		"LOCAL_REGION":                                     getEnvOrDefault("LOCAL_REGION", "us-west2"),
		"ENABLE_VOLUME_METRICS":                            getEnvOrDefault("ENABLE_VOLUME_METRICS", "false"),
		"ENABLE_BACKUP_METRICS":                            getEnvOrDefault("ENABLE_BACKUP_METRICS", "false"),
		"ENABLE_BACKUP_VAULT_METRICS":                      getEnvOrDefault("ENABLE_BACKUP_VAULT_METRICS", "false"),
		"ENABLE_BACKUP_BILLING_METRICS":                    getEnvOrDefault("ENABLE_BACKUP_BILLING_METRICS", "false"),
		"ENABLE_SFR_METRICS":                               getEnvOrDefault("ENABLE_SFR_METRICS", "false"),
		"ENABLE_FILES_BACKUP_BILLING":                      getEnvOrDefault("ENABLE_FILES_BACKUP_BILLING", "false"),
		"ENABLE_CMEK_BACKUP_BILLING":                       getEnvOrDefault("ENABLE_CMEK_BACKUP_BILLING", "false"),
		"ENABLE_GCBDR_BACKUP_BILLING":                      getEnvOrDefault("ENABLE_GCBDR_BACKUP_BILLING", "false"),
		"ENABLE_CROSS_PROJECT_BACKUP_USED_METRIC":          getEnvOrDefault("ENABLE_CROSS_PROJECT_BACKUP_USED_METRIC", "false"),
		"ENABLE_CROSS_REGION_BACKUP_BILLING_METRICS":       getEnvOrDefault("ENABLE_CROSS_REGION_BACKUP_BILLING_METRICS", "false"),
		"ENABLE_SFR_CROSS_REGION_RESTORE_BILLING":          getEnvOrDefault("ENABLE_SFR_CROSS_REGION_RESTORE_BILLING", "false"),
		"ENABLE_AUTO_TIERING_BILLING_METRICS":              getEnvOrDefault("ENABLE_AUTO_TIERING_BILLING_METRICS", "false"),
		"ENABLE_ONTAP_MODE_AUTOTIERING_BILLING":            getEnvOrDefault("ENABLE_ONTAP_MODE_AUTOTIERING_BILLING", "false"),
		"ENABLE_FILES_AUTO_TIERING_BILLING":                getEnvOrDefault("ENABLE_FILES_AUTO_TIERING_BILLING", "false"),
		"ENABLE_AT_VOLUME_BASED_POOL_BILLING":              getEnvOrDefault("ENABLE_AT_VOLUME_BASED_POOL_BILLING", "true"),
		"ENABLE_REPLICATION_BILLING_METRICS":               getEnvOrDefault("ENABLE_REPLICATION_BILLING_METRICS", "false"),
		"ENABLE_BIDIRECTIONAL_REPLICATION_BILLING_METRICS": getEnvOrDefault("ENABLE_BIDIRECTIONAL_REPLICATION_BILLING_METRICS", "false"),
		"ENABLE_IN_REGION_REPLICATION_BILLING_METRICS":     getEnvOrDefault("ENABLE_IN_REGION_REPLICATION_BILLING_METRICS", "false"),
		"ENABLE_ONTAP_MODE_REPLICATION_BILLING":            getEnvOrDefault("ENABLE_ONTAP_MODE_REPLICATION_BILLING", "false"),
		"ENABLE_FILES_REPLICATION_BILLING_METRICS":         getEnvOrDefault("ENABLE_FILES_REPLICATION_BILLING_METRICS", "false"),
		"ENABLE_LARGE_VOLUMES_BILLING":                     getEnvOrDefault("ENABLE_LARGE_VOLUMES_BILLING", "false"),
		"ENABLE_TENANT_QUOTA_PROJECT":                      getEnvOrDefault("ENABLE_TENANT_QUOTA_PROJECT", "true"),
		"ENABLE_BATCH_USAGE_UPDATES":                       getEnvOrDefault("ENABLE_BATCH_USAGE_UPDATES", "false"),
		"PUSH_BATCH_SIZE":                                  getEnvOrDefault("PUSH_BATCH_SIZE", "1000"),
		"TELEMETRY_JOB_MAX_RETRY_PERFORMANCE":              getEnvOrDefault("TELEMETRY_JOB_MAX_RETRY_PERFORMANCE", "0"),
		"TELEMETRY_JOB_MAX_RETRY_USAGE":                    getEnvOrDefault("TELEMETRY_JOB_MAX_RETRY_USAGE", "5"),
		"TELEMETRY_JOB_MAX_RETRY_COLLECTION":               getEnvOrDefault("TELEMETRY_JOB_MAX_RETRY_COLLECTION", "5"),
		"TELEMETRY_JOB_MAX_RETRY_BIZOPS":                   getEnvOrDefault("TELEMETRY_JOB_MAX_RETRY_BIZOPS", "5"),
		"TELEMETRY_JOB_MAX_RETRY_BILLING_RETRY":            getEnvOrDefault("TELEMETRY_JOB_MAX_RETRY_BILLING_RETRY", "5"),
		"MAX_GOOGLE_BILLING_PUSH_RETRY":                    getEnvOrDefault("MAX_GOOGLE_BILLING_PUSH_RETRY", "5"),
		"PAGE_SIZE":                                        getEnvOrDefault("PAGE_SIZE", "1000"),
		"GOOGLE_CONTINENTS":                                getEnvOrDefault("GOOGLE_CONTINENTS", ""),
		"USE_NEW_LABEL_FOR_HYBRID_REPLICATION":             getEnvOrDefault("USE_NEW_LABEL_FOR_HYBRID_REPLICATION", "false"),
		"BIZOPS_ACCOUNT_PAGINATION_LIMIT":                  getEnvOrDefault("BIZOPS_ACCOUNT_PAGINATION_LIMIT", "1000"),
		"BIZOPS_REPORT_NAME":                               getEnvOrDefault("BIZOPS_REPORT_NAME", ""),
		"BIZOPS_BUCKET_NAME":                               getEnvOrDefault("BIZOPS_BUCKET_NAME", ""),
		"GOOGLE_REGION":                                    getEnvOrDefault("GOOGLE_REGION", ""),
		"ENVIRONMENT":                                      getEnvOrDefault("ENVIRONMENT", "gcp"),
		"NUM_WORKERS_PERFORMANCE":                          getEnvOrDefault("NUM_WORKERS_PERFORMANCE", "10"),
		"NUM_WORKERS_USAGE":                                getEnvOrDefault("NUM_WORKERS_USAGE", "1"),
		"NUM_WORKERS_BIZOPS":                               getEnvOrDefault("NUM_WORKERS_BIZOPS", "10"),
		"NUM_WORKERS_COLLECTION":                           getEnvOrDefault("NUM_WORKERS_COLLECTION", "25"),
		"TARGET_MINUTE":                                    getEnvOrDefault("TARGET_MINUTE", "15"),
		"PERFORMANCE_ROOT_URL":                             getEnvOrDefault("PERFORMANCE_ROOT_URL", "https://servicecontrol.googleapis.com"),
		"USAGE_ROOT_URL":                                   getEnvOrDefault("USAGE_ROOT_URL", "https://servicecontrol.googleapis.com"),
		"RETRY_INTERVAL_SECONDS":                           getEnvOrDefault("RETRY_INTERVAL_SECONDS", "300"),
		"NUM_WORKERS_BILLING_RETRY":                        getEnvOrDefault("NUM_WORKERS_BILLING_RETRY", "5"),
		// Always include the flag so callers can reliably read it even if --env-vars omits it.
		"CLOUD_SQL_IAM_AUTH_ENABLED":               getEnvOrDefault("CLOUD_SQL_IAM_AUTH_ENABLED", "false"),
		"ENABLE_BACKUP_HISTORY_FORMATTER":          getEnvOrDefault("ENABLE_BACKUP_HISTORY_FORMATTER", "false"),
		"INJECTION_WINDOW_MINUTES":                 getEnvOrDefault("INJECTION_WINDOW_MINUTES", "10"),
		"ENABLE_COUNTER_FORMATTER":                 getEnvOrDefault("ENABLE_COUNTER_FORMATTER", "false"),
		"ENABLE_EXPERT_MODE_BACKUP_BILLING":        getEnvOrDefault("ENABLE_EXPERT_MODE_BACKUP_BILLING", "false"),
		"SKIP_HYBRID_REPLICATION_BASELINE_BILLING": getEnvOrDefault("SKIP_HYBRID_REPLICATION_BASELINE_BILLING", "true"),
		"ENABLE_FREE_TRIAL_BILLING":                getEnvOrDefault("ENABLE_FREE_TRIAL_BILLING", "false"),
		"TELEMETRY_SHUTDOWN_BUDGET":                getEnvOrDefault("TELEMETRY_SHUTDOWN_BUDGET", "10s"),
		"TELEMETRY_HTTP_SHUTDOWN_TIMEOUT":          getEnvOrDefault("TELEMETRY_HTTP_SHUTDOWN_TIMEOUT", "3s"),
	}
}

// getCloudRunServiceURL retrieves the URL of the deployed Cloud Run service
func getCloudRunServiceURL(ctx context.Context, config *DeploymentConfig) (string, error) {
	client, err := cloudrun.NewService(ctx, option.WithScopes("https://www.googleapis.com/auth/cloud-platform"))
	if err != nil {
		return "", fmt.Errorf("failed to create Cloud Run client: %w", err)
	}

	serviceName := fmt.Sprintf("projects/%s/locations/%s/services/%s", config.ProjectID, config.Region, config.ServiceName)
	service, err := client.Projects.Locations.Services.Get(serviceName).Do()
	if err != nil {
		return "", fmt.Errorf("failed to get service: %w", err)
	}

	if service.Uri == "" {
		return "", fmt.Errorf("service URL is empty")
	}

	return service.Uri, nil
}

// printCloudRunServiceInfo prints detailed information about a Cloud Run service
func printCloudRunServiceInfo(service *cloudrun.GoogleCloudRunV2Service) {
	log.Printf("=== Cloud Run Service Details ===\n")
	log.Printf("Service Name: %s\n", service.Name)
	log.Printf("Service URL:  %s\n", service.Uri)

	if service.Template != nil {
		if len(service.Template.Containers) > 0 {
			// Print info for all containers (telemetry is first, SQL proxy is second)
			for i, container := range service.Template.Containers {
				log.Printf("Container %d: %s\n", i+1, container.Name)
				log.Printf("  Image:        %s\n", container.Image)

				if container.Resources != nil {
					log.Printf("  CPU Idle:     %t (instance-based billing)\n", container.Resources.CpuIdle)
					if container.Resources.Limits != nil {
						for k, v := range container.Resources.Limits {
							log.Printf("  Resource %s:  %s\n", k, v)
						}
					}
				}
			}
		}

		if service.Template.Scaling != nil {
			if service.Template.Scaling.MinInstanceCount > 0 {
				log.Printf("Min Instances: %d\n", service.Template.Scaling.MinInstanceCount)
			}
			if service.Template.Scaling.MaxInstanceCount > 0 {
				log.Printf("Max Instances: %d\n", service.Template.Scaling.MaxInstanceCount)
			}
		}

		if service.Template.ServiceAccount != "" {
			log.Printf("Service Account: %s\n", service.Template.ServiceAccount)
		}
	}

	if len(service.Traffic) > 0 {
		log.Printf("Traffic Allocation:\n")
		for _, traffic := range service.Traffic {
			log.Printf("  - %d%% -> %s\n", traffic.Percent, traffic.Type)
		}
	}

	if len(service.Labels) > 0 {
		log.Printf("Labels:\n")
		for k, v := range service.Labels {
			log.Printf("  %s: %s\n", k, v)
		}
	}

	log.Printf("Creation Time: %s\n", service.CreateTime)
	log.Printf("Update Time:   %s\n", service.UpdateTime)
	log.Printf("================================\n")
}

// deployCloudScheduler creates or updates a Cloud Scheduler job to invoke the Cloud Run service
func deployCloudScheduler(ctx context.Context, config *DeploymentConfig) error {
	client, err := cloudscheduler.NewService(ctx, option.WithScopes("https://www.googleapis.com/auth/cloud-platform"))
	if err != nil {
		return fmt.Errorf("failed to create Cloud Scheduler client: %w", err)
	}

	parent := fmt.Sprintf("projects/%s/locations/%s", config.ProjectID, config.SchedulerRegion)
	jobName := fmt.Sprintf("%s/jobs/%s-trigger", parent, config.ServiceName)

	// Create the job configuration
	job := &cloudscheduler.Job{
		Name:        jobName,
		Description: fmt.Sprintf("Scheduled trigger for %s Cloud Run service", config.ServiceName),
		Schedule:    config.SchedulerCron,
		TimeZone:    "Etc/UTC",
		HttpTarget: &cloudscheduler.HttpTarget{
			Uri:        config.ServiceURL,
			HttpMethod: "POST",
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			OidcToken: &cloudscheduler.OidcToken{
				ServiceAccountEmail: config.ServiceAccountName,
				Audience:            config.ServiceURL,
			},
		},
		RetryConfig: &cloudscheduler.RetryConfig{
			RetryCount:         3,
			MaxRetryDuration:   "300s",
			MinBackoffDuration: "5s",
			MaxBackoffDuration: "60s",
			MaxDoublings:       3,
		},
	}

	if config.ServiceName == "bizops" {
		// For Bizops add body to the scheduler job
		job.HttpTarget.Body = base64.StdEncoding.EncodeToString([]byte(`{"sinkType":"gcs","timeZone":"PST"}`))
	}

	// Check if job already exists
	existingJob, err := client.Projects.Locations.Jobs.Get(jobName).Do()
	if err != nil {
		// Job doesn't exist, create it
		log.Printf("Creating new Cloud Scheduler job: %s\n", config.ServiceName+"-trigger")
		_, err := client.Projects.Locations.Jobs.Create(parent, job).Do()
		if err != nil {
			return fmt.Errorf("failed to create Cloud Scheduler job: %w", err)
		}
		log.Printf("Cloud Scheduler job created successfully\n")
	} else {
		// Job exists, update it
		log.Printf("Updating existing Cloud Scheduler job: %s\n", config.ServiceName+"-trigger")

		// Update the existing job
		existingJob.Schedule = job.Schedule
		existingJob.Description = job.Description
		existingJob.HttpTarget = job.HttpTarget
		existingJob.RetryConfig = job.RetryConfig
		existingJob.TimeZone = job.TimeZone

		_, err := client.Projects.Locations.Jobs.Patch(jobName, existingJob).Do()
		if err != nil {
			return fmt.Errorf("failed to update Cloud Scheduler job: %w", err)
		}
		log.Printf("Cloud Scheduler job updated successfully\n")
	}

	return nil
}

// generateOtelConfig generates the OpenTelemetry collector configuration YAML
func generateOtelConfig(monitoringProjectID, customPrefix string) string {
	return `exporters:
  debug:
    verbosity: normal
  googlecloud:
    project: ` + monitoringProjectID + `
    metric:
      prefix: ` + customPrefix + `
      instrumentation_library_labels: false
    sending_queue:
      enabled: true
      queue_size: 40000

extensions:
  health_check:
    endpoint: "0.0.0.0:20001"

processors:
  batch:
    send_batch_size: 200
    send_batch_max_size: 200
    timeout: 200ms
  memory_limiter:
    check_interval: 5s
    limit_percentage: 80
    spike_limit_percentage: 30
  resourcedetection:
    detectors: [gcp]
    timeout: 10s

receivers:
  prometheus:
    config:
      scrape_configs:
        - job_name: "vsa-telemetry-service"
          scrape_interval: 60s
          static_configs:
            - targets: ["localhost:8080"]
          metrics_path: /metrics
service:
  extensions:
    - health_check
  pipelines:
    metrics:
      receivers: [prometheus]
      processors: [batch, resourcedetection]
      exporters: [googlecloud, debug]`
}

// creates an OpenTelemetry collector sidecar container to scrape telemetry metrics
// Returns the container and optionally a volume if using Secret Manager for config
func createOtelCollectorContainer(config *DeploymentConfig) (*cloudrun.GoogleCloudRunV2Container, *cloudrun.GoogleCloudRunV2Volume) {
	// OTEL collector supports YAML config through environment variables
	// Use the --config=env:OTEL_CONFIG syntax
	otelConfig := generateOtelConfig(config.MonitoringProjectID, config.CustomPrefix)

	container := &cloudrun.GoogleCloudRunV2Container{
		Name:    "otel-collector",
		Image:   config.OTELImage, // Use original distroless image
		Command: []string{"/otelcol-contrib"},
		Args: []string{
			"--config=env:OTEL_CONFIG", // OTEL reads config from env var directly
		},
		Env: []*cloudrun.GoogleCloudRunV2EnvVar{
			{
				Name:  "OTEL_CONFIG",
				Value: otelConfig, // Pass config as plain YAML string
			},
		},
		Ports: nil,
		Resources: &cloudrun.GoogleCloudRunV2ResourceRequirements{
			CpuIdle: false,
			Limits: map[string]string{
				"cpu":    config.OTELCPU,
				"memory": config.OTELMemory,
			},
		},
		StartupProbe: &cloudrun.GoogleCloudRunV2Probe{
			InitialDelaySeconds: 0,
			PeriodSeconds:       10,
			TimeoutSeconds:      1,
			FailureThreshold:    3,
			HttpGet: &cloudrun.GoogleCloudRunV2HTTPGetAction{
				Path: "/",
				Port: 20001,
			},
		},
		LivenessProbe: &cloudrun.GoogleCloudRunV2Probe{
			InitialDelaySeconds: 0,
			PeriodSeconds:       10,
			TimeoutSeconds:      1,
			FailureThreshold:    3,
			HttpGet: &cloudrun.GoogleCloudRunV2HTTPGetAction{
				Path: "/",
				Port: 20001,
			},
		},
	}

	return container, nil
}
