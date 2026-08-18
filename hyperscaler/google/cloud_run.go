package google

import (
	"context"
	"fmt"

	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"google.golang.org/api/idtoken"
	cloudrun "google.golang.org/api/run/v2"
)

var (
	idtokenNewTokenSource = idtoken.NewTokenSource
)

// CloudRunServiceConfig represents the configuration for a Cloud Run service

func (gcpService *GcpServices) CreateCloudRunService(ctx context.Context, config *models.CloudRunServiceConfig) (*models.CloudRunOperationResponse, error) {
	gcpService.Logger.Infof("Creating Cloud Run service %s in project %s at location %s",
		config.ServiceName, config.ProjectID, config.LocationID)

	// Convert environment variables
	envVars := make([]*cloudrun.GoogleCloudRunV2EnvVar, 0, len(config.EnvVars))
	for key, value := range config.EnvVars {
		envVars = append(envVars, &cloudrun.GoogleCloudRunV2EnvVar{
			Name:  key,
			Value: value,
		})
	}

	// Convert volume mounts
	volumeMounts := make([]*cloudrun.GoogleCloudRunV2VolumeMount, 0, len(config.VolumeMounts))
	for _, vm := range config.VolumeMounts {
		volumeMounts = append(volumeMounts, &cloudrun.GoogleCloudRunV2VolumeMount{
			Name:      vm.Name,
			MountPath: vm.MountPath,
		})
	}

	containerPort := config.ContainerPort
	if containerPort == 0 {
		containerPort = 80
	}
	if containerPort < 1 || containerPort > 65535 {
		return nil, fmt.Errorf("container port must be between 1 and 65535: %d", containerPort)
	}

	// Define the container
	container := &cloudrun.GoogleCloudRunV2Container{
		Image:        config.Image,
		Env:          envVars,
		VolumeMounts: volumeMounts,
		Ports: []*cloudrun.GoogleCloudRunV2ContainerPort{
			{
				ContainerPort: containerPort,
			},
		},
	}

	// Add resource limits if specified
	if config.Resources != nil {
		container.Resources = &cloudrun.GoogleCloudRunV2ResourceRequirements{
			Limits: map[string]string{},
		}
		if config.Resources.CPULimit != "" {
			container.Resources.Limits["cpu"] = config.Resources.CPULimit
		}
		if config.Resources.MemoryLimit != "" {
			container.Resources.Limits["memory"] = config.Resources.MemoryLimit
		}
	}

	// Add startup probe if specified
	if config.StartupProbe != nil {
		probe := &cloudrun.GoogleCloudRunV2Probe{
			InitialDelaySeconds: config.StartupProbe.InitialDelaySeconds,
			PeriodSeconds:       config.StartupProbe.PeriodSeconds,
			TimeoutSeconds:      config.StartupProbe.TimeoutSeconds,
			FailureThreshold:    config.StartupProbe.FailureThreshold,
		}
		if config.StartupProbe.TCPSocket != nil {
			probe.TcpSocket = &cloudrun.GoogleCloudRunV2TCPSocketAction{
				Port: int64(config.StartupProbe.TCPSocket.Port),
			}
		}
		if config.StartupProbe.HTTPGet != nil {
			probe.HttpGet = &cloudrun.GoogleCloudRunV2HTTPGetAction{
				Path: config.StartupProbe.HTTPGet.Path,
				Port: int64(config.StartupProbe.HTTPGet.Port),
			}
		}
		if config.StartupProbe.GRPC != nil {
			probe.Grpc = &cloudrun.GoogleCloudRunV2GRPCAction{
				Port:    int64(config.StartupProbe.GRPC.Port),
				Service: config.StartupProbe.GRPC.Service,
			}
		}
		container.StartupProbe = probe
	}

	// Convert volumes
	volumes := make([]*cloudrun.GoogleCloudRunV2Volume, 0, len(config.Volumes))
	for _, vol := range config.Volumes {
		volume := &cloudrun.GoogleCloudRunV2Volume{
			Name: vol.Name,
		}

		switch vol.VolumeType {
		case "secret":
			items := make([]*cloudrun.GoogleCloudRunV2VersionToPath, 0, len(vol.Source.Items))
			for _, item := range vol.Source.Items {
				items = append(items, &cloudrun.GoogleCloudRunV2VersionToPath{
					Path:    item.Path,
					Version: item.Version,
				})
			}

			volume.Secret = &cloudrun.GoogleCloudRunV2SecretVolumeSource{
				Secret: vol.Source.SecretName,
				Items:  items,
			}
		}
		volumes = append(volumes, volume)
	}

	// Set default values if not provided
	description := config.Description
	if description == "" {
		description = fmt.Sprintf("Cloud Run service %s", config.ServiceName)
	}

	labels := config.Labels
	if labels == nil {
		labels = map[string]string{
			"managed-by": "vsa-control-plane",
		}
	}

	annotations := config.Annotations
	if annotations == nil {
		annotations = map[string]string{}
	}

	// Create the Cloud Run service request
	service := &cloudrun.GoogleCloudRunV2Service{
		Description: description,
		Labels:      labels,
		Annotations: annotations,
		Template: &cloudrun.GoogleCloudRunV2RevisionTemplate{
			Containers: []*cloudrun.GoogleCloudRunV2Container{container},
			Volumes:    volumes,
		},
	}

	// Set ingress configuration if specified
	if config.Ingress != "" {
		service.Ingress = config.Ingress
	}

	// Create the service using REST API
	parent := fmt.Sprintf("projects/%s/locations/%s", config.ProjectID, config.LocationID)
	operation, err := gcpService.AdminGCPService.cloudRunService.Projects.Locations.Services.Create(parent, service).ServiceId(config.ServiceName).Do()
	if err != nil {
		gcpService.Logger.Errorf("Failed to create Cloud Run service: %v", err)
		return nil, err
	}

	operationName := operation.Name
	gcpService.Logger.Debugf("Cloud Run service creation operation started: %s", operationName)

	return &models.CloudRunOperationResponse{
		OperationName: operationName,
		Status:        "RUNNING",
	}, nil
}

func (gcpService *GcpServices) CheckOperationStatus(ctx context.Context, operationName string) (bool, error) {
	gcpService.Logger.Infof("Checking status of operation: %s", operationName)

	// Get the operation status using REST API
	operation, err := gcpService.AdminGCPService.cloudRunService.Projects.Locations.Operations.Get(operationName).Do()
	if err != nil {
		gcpService.Logger.Errorf("Failed to get operation status: %v", err)
		return false, err
	}

	if operation.Done {
		if operation.Error != nil {
			gcpService.Logger.Errorf("Operation completed with error: %v", operation.Error)
			return true, fmt.Errorf("operation completed with error: code=%v, message=%s", operation.Error.Code, operation.Error.Message)
		}
		gcpService.Logger.Debugf("Operation completed successfully.")
		return true, nil
	}

	gcpService.Logger.Debugf("Operation is still in progress.")
	return false, nil
}

func (gcpService *GcpServices) GetCloudRunServiceURL(ctx context.Context, projectID, locationID, serviceName string) (string, error) {
	gcpService.Logger.Infof("Getting Cloud Run service URL for %s in project %s at location %s",
		serviceName, projectID, locationID)

	// Construct the service name
	serviceFullName := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, locationID, serviceName)

	// Get the service using REST API
	service, err := gcpService.AdminGCPService.cloudRunService.Projects.Locations.Services.Get(serviceFullName).Do()
	if err != nil {
		gcpService.Logger.Errorf("Failed to get Cloud Run service: %v", err)
		return "", err
	}

	if len(service.Urls) == 0 {
		return "", fmt.Errorf("service URLs not available for service %s", serviceName)
	}
	serviceURL := service.Urls[0]
	if serviceURL == "" {
		return "", fmt.Errorf("service URL not available for service %s", serviceName)
	}

	gcpService.Logger.Debugf("Cloud Run service URL: %s", serviceURL)
	return serviceURL, nil
}

func (gcpService *GcpServices) DeleteCloudRunService(ctx context.Context, projectID, locationID, serviceName string) (*models.CloudRunOperationResponse, error) {
	gcpService.Logger.Infof("Deleting Cloud Run service %s in project %s at location %s", serviceName, projectID, locationID)

	// Construct the service name
	serviceFullName := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, locationID, serviceName)

	// Delete the Cloud Run service using REST API
	operation, err := gcpService.AdminGCPService.cloudRunService.Projects.Locations.Services.Delete(serviceFullName).Do()
	if err != nil {
		gcpService.Logger.Errorf("Failed to delete Cloud Run service: %v", err)
		return nil, err
	}

	operationName := operation.Name
	gcpService.Logger.Debugf("Cloud Run service deletion operation started: %s", operationName)

	return &models.CloudRunOperationResponse{
		OperationName: operationName,
		Status:        "RUNNING",
	}, nil
}

// GetIdentityToken gets a Google Cloud identity token for the specified audience
func (gcpService *GcpServices) GetIdentityToken(ctx context.Context, audience string) (string, error) {
	// Create a token source for the specified audience
	tokenSource, err := idtokenNewTokenSource(ctx, audience)
	if err != nil {
		gcpService.Logger.Errorf("Failed to create token source: %v", err)
		return "", fmt.Errorf("failed to create token source: %w", err)
	}

	// Get the token
	token, err := tokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("failed to get identity token: %w", err)
	}
	return token.AccessToken, nil
}
