package datamodel

import (
	"io"
	"time"
)

type HydratedMetrics struct {
	ID              int64      `gorm:"primaryKey;autoIncrement" json:"id"`
	MetricTimestamp time.Time  `gorm:"column:metric_timestamp;not null;index" json:"metric_timestamp"`
	MeasuredType    string     `gorm:"column:measured_type;not null;index" json:"measured_type"`
	ResourceType    string     `gorm:"column:resource_type;not null;index" json:"resource_type"`
	Quantity        float64    `gorm:"column:quantity;not null" json:"quantity"`
	ResourceName    string     `gorm:"column:resource_name;size:255;index" json:"resource_name"`
	ConsumerID      string     `gorm:"column:consumer_id;size:255;index" json:"consumer_id"`
	Location        string     `gorm:"column:location;size:255;index" json:"location"`
	Metadata        []byte     `gorm:"column:metadata;type:jsonb" json:"metadata"`
	DeploymentName  string     `gorm:"column:deployment_name;size:255;index" json:"deployment_name"`
	DeletedAt       *time.Time `gorm:"-" json:"deleted_at"`
}

type AggregatedUsage struct {
	ID                     int64         `gorm:"primaryKey;autoIncrement" json:"id"`
	ResourceUUID           string        `gorm:"column:resource_uuid;size:255;not null;index" json:"resource_uuid"`
	AccountID              string        `gorm:"column:account_id;size:255;not null;index" json:"account_id"`
	VendorCustomerID       *string       `gorm:"column:vendor_customer_id;size:255;not null;index" json:"vendor_customer_id"`
	AggregationEnd         time.Time     `gorm:"column:aggregation_end;not null;index" json:"aggregation_end"`
	AggregationStart       time.Time     `gorm:"column:aggregation_start;not null;index" json:"aggregation_start"`
	MeasuredType           string        `gorm:"column:measured_type;not null;index" json:"measured_type"`
	Quantity               float64       `gorm:"column:quantity;not null" json:"quantity"`
	ResourceName           *string       `gorm:"column:resource_name;size:255;index" json:"resource_name"`
	ResourceType           string        `gorm:"column:resource_type;not null;index" json:"resource_type"`
	AggregationType        string        `gorm:"column:aggregation_type;size:100;not null" json:"aggregation_type"`
	LastCounterValue       *float64      `gorm:"column:last_counter_value" json:"last_counter_value"`
	LastTransferType       *string       `gorm:"column:last_transfer_type;size:32" json:"last_transfer_type"`
	RegionName             *string       `gorm:"column:region_name;size:255;index" json:"region_name"`
	Zone                   *string       `gorm:"column:zone;size:255" json:"zone"`
	SourceRegion           *string       `gorm:"column:source_region;size:255" json:"source_region"`
	DestinationRegion      *string       `gorm:"column:destination_region;size:255" json:"destination_region"`
	BillingLabels          *string       `gorm:"column:billing_labels;type:jsonb" json:"billing_labels"`
	ReplicationDstVolumeID *string       `gorm:"column:replication_dst_volume_id;size:255" json:"replication_dst_volume_id"`
	DoubleEncryption       *bool         `gorm:"column:double_encryption;default:false" json:"double_encryption"`
	State                  TrackingState `gorm:"column:state;not null;default:0" json:"state"`
	ErrorCount             int32         `gorm:"column:error_count;default:0" json:"error_count"`
	ErrorMessage           *string       `gorm:"column:error_message;type:text" json:"error_message"`
	Submission             *string       `gorm:"column:submission;type:jsonb" json:"submission"`
	IsBillable             bool          `gorm:"column:is_billable;default:false" json:"is_billable"`
	BillingMode            BillingMode   `gorm:"column:billing_mode;size:32;index" json:"billing_mode"`
	CreatedAt              time.Time     `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt              time.Time     `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
	VolumeStyle            string        `gorm:"column:volume_style;size:255" json:"volume_style"`
	ReplicationType        string        `gorm:"column:replication_type;size:255" json:"replication_type"`
	ServiceLevel           string        `gorm:"column:service_level;size:255" json:"service_level"`
	IsUnified              bool          `gorm:"column:is_unified;default:true" json:"is_unified"`
}

type Job struct {
	ID       int64  `gorm:"primaryKey;autoIncrement"`
	TypeName string `gorm:"type:text"`
	Status   string `gorm:"type:text"`
	Queue    string `gorm:"type:text"`
	Data     string `gorm:"type:text"`
	Error    string `gorm:"type:text"`
	Attempt  int32  `gorm:"default:0"`

	// LeaseExpiresAt marks how long the worker that claimed this job holds it.
	// While a job runs outside any DB transaction, the sweeper reclaims jobs
	// whose lease has expired (crash/stuck detection). Nil when not in progress.
	LeaseExpiresAt *time.Time `gorm:"column:lease_expires_at;index"`
	// WorkerID identifies the worker that currently owns the job. It guards
	// heartbeat and finalize writes so a stale worker cannot clobber a job that
	// the sweeper already reclaimed and another worker re-ran.
	WorkerID *string `gorm:"column:worker_id;type:text"`

	CreatedAt   time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
	ScheduledAt time.Time
}

type TrackingState int32
type BillingMode string

const (
	Unsubmitted TrackingState = iota
	Submitted
	Error
	Ignored
	Invalid
)

const (
	BillingModeFreeTrial  BillingMode = "FREE_TRIAL"
	BillingModeCommercial BillingMode = "COMMERCIAL"
)

// Below structs are not created in DB
type AccountInfo struct {
	AccountID string `json:"account_id"`
	UserName  string `json:"user_name"`
	IsActive  bool   `json:"is_active"`
}
type BizOpsAggregateParams struct {
	AccountsInfo []*AccountInfo
	ContinentMap map[string]string
	Region       string
	AggrStart    time.Time
	AggrEnd      time.Time
	Writer       io.Writer
}

// RestoreTimestamp is a table to track the last processed timestamp for restore operations
type RestoreTimestamp struct {
	ID              int64     `gorm:"primaryKey;autoIncrement"`
	LastProcessedAt time.Time `gorm:"column:last_processed_at;not null"`
	UpdatedAt       time.Time `gorm:"column:updated_at;autoUpdateTime"`
}
