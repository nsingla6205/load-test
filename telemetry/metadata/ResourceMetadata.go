package metadata

import "time"

type ResourceMetadata struct {
	ResourceUUID        *string
	ResourceName        *string
	ResourceDisplayName *string
	ResourceType        ResourceType
	SizeInBytes         *int64
	RegionName          *string
	Tags                map[string]string
	AutoTierEnabled     *bool
	AccountName         *string
	DeploymentName      *string
	Throughput          *float64
	ResourceID          *int64
	ServiceLevel        *string
	APIAccessMode       *string
	DeletedAt           *time.Time
	BackupRegionName    *string
	SourceRegionName    *string
	PoolName            *string
	CreatedAt           *time.Time
	TransferType        *string
}

func (m *ResourceMetadata) SetResourceUUID(uuid string) {
	m.ResourceUUID = &uuid
}

func (m *ResourceMetadata) SetResourceName(name string) {
	m.ResourceName = &name
}

func (m *ResourceMetadata) SetResourceDisplayName(name string) {
	m.ResourceDisplayName = &name
}

func (m *ResourceMetadata) SetResourceType(resourceType ResourceType) {
	m.ResourceType = resourceType
}

func (m *ResourceMetadata) SetSizeInBytes(size int64) {
	m.SizeInBytes = &size
}

func (m *ResourceMetadata) SetRegionName(region string) {
	m.RegionName = &region
}

func (m *ResourceMetadata) SetAccountName(accountName string) {
	m.AccountName = &accountName
}

func (m *ResourceMetadata) SetDeploymentName(deploymentName string) {
	m.DeploymentName = &deploymentName
}

func (m *ResourceMetadata) SetThroughput(throughput float64) {
	m.Throughput = &throughput
}

func (m *ResourceMetadata) SetResourceID(resourceID int64) {
	m.ResourceID = &resourceID
}

func (m *ResourceMetadata) SetServiceLevel(serviceLevel string) {
	m.ServiceLevel = &serviceLevel
}

func (m *ResourceMetadata) SetAPIAccessMode(apiAccessMode string) {
	m.APIAccessMode = &apiAccessMode
}

func (m *ResourceMetadata) SetDeletedAt(deletedAt time.Time) {
	m.DeletedAt = &deletedAt
}

func (m *ResourceMetadata) SetBackupRegionName(region string) {
	m.BackupRegionName = &region
}

func (m *ResourceMetadata) SetSourceRegionName(region string) {
	m.SourceRegionName = &region
}

func (m *ResourceMetadata) SetPoolName(name string) {
	m.PoolName = &name
}

func (m *ResourceMetadata) SetTransferType(transferType string) {
	m.TransferType = &transferType
}
