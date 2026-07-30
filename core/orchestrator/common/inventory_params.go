package common

// GetProjectInventoryParams are the inputs for the GetProjectInventory orchestrator method.
type GetProjectInventoryParams struct {
	// ProjectNumber is the GCP project number, used as the account name in VCP.
	ProjectNumber string
	// ShowVolumes, when true, includes the SVM → volume tree in the response.
	ShowVolumes bool
}

// InventoryVolume is a lightweight volume descriptor in the inventory tree.
type InventoryVolume struct {
	Name string
	UUID string
}

// InventorySVM is an SVM with its optional volume list.
type InventorySVM struct {
	Name    string
	Volumes []InventoryVolume
}

// InventoryAggregate is an aggregate with capacity and volume metadata.
type InventoryAggregate struct {
	Name        string
	UUID        string
	State       string
	VolumeCount int64
	// All sizes in bytes.
	Size          int64
	UsedSize      int64
	AvailableSize int64
}

// InventoryCluster is a cluster with its aggregates and optional SVM tree.
type InventoryCluster struct {
	Name       string
	Aggregates []InventoryAggregate
	SVMs       []InventorySVM
	// All sizes in bytes.
	Size          int64
	UsedSize      int64
	AvailableSize int64
}

// ProjectInventoryResult is the return type of GetProjectInventory.
type ProjectInventoryResult struct {
	Clusters []InventoryCluster
}
