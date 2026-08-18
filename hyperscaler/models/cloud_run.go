package hyperscaler

type CloudRunServiceConfig struct {
	ProjectID     string
	LocationID    string
	ServiceName   string
	Image         string
	Description   string
	Labels        map[string]string
	Annotations   map[string]string
	Ingress       string // "INGRESS_TRAFFIC_ALL", "INGRESS_TRAFFIC_INTERNAL_ONLY", "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"
	EnvVars       map[string]string
	VolumeMounts  []VolumeMount
	Volumes       []Volume
	Resources     *ResourceConfig
	StartupProbe  *ProbeConfig
	ContainerPort int64
}

// ProbeConfig represents a probe configuration (startup, liveness, or readiness)
type ProbeConfig struct {
	InitialDelaySeconds int64            // Initial delay before the first probe (default: 0)
	PeriodSeconds       int64            // Period between probes (default: 10)
	TimeoutSeconds      int64            // Timeout for each probe (default: 1)
	FailureThreshold    int64            // Number of failures before marking as failed (default: 3)
	SuccessThreshold    int64            // Number of successes before marking as successful (default: 1)
	HTTPGet             *HTTPGetAction   // HTTP probe configuration
	TCPSocket           *TCPSocketAction // TCP probe configuration
	GRPC                *GRPCAction      // gRPC probe configuration
}

// HTTPGetAction represents an HTTP GET probe action
type HTTPGetAction struct {
	Path   string // Path to probe (e.g., "/health")
	Port   int64  // Port to probe
	Scheme string // "HTTP" or "HTTPS" (default: "HTTP")
}

// TCPSocketAction represents a TCP socket probe action
type TCPSocketAction struct {
	Port int64 // Port to probe
}

// GRPCAction represents a gRPC probe action
type GRPCAction struct {
	Port    int64  // Port to probe
	Service string // Service name for gRPC health check
}

// VolumeMount represents a volume mount configuration
type VolumeMount struct {
	Name      string
	MountPath string
}

// Volume represents a volume configuration
type Volume struct {
	Name       string
	VolumeType string // "secret", "configmap", etc.
	Source     VolumeSource
}

// VolumeSource represents the source of a volume
type VolumeSource struct {
	SecretName string
	Items      []SecretItem
}

// SecretItem represents an item in a secret volume
type SecretItem struct {
	Path    string
	Version string
}

// ResourceConfig represents resource configuration for containers
type ResourceConfig struct {
	CPULimit    string
	MemoryLimit string
}

// CloudRunOperationResponse represents the response from a Cloud Run operation
type CloudRunOperationResponse struct {
	OperationName string
	Status        string
}
