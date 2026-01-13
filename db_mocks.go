package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/datamodel"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func cleanupAllResources(db *gorm.DB, volumes []*datamodel.Volume, replications []*datamodel.VolumeReplication, pools []*datamodel.Pool, svms []*datamodel.Svm, accounts []*datamodel.Account) {
	for i := 0; i < len(volumes); i++ {
		cleanupResources(
			db,
			volumes[i],
			replications[i],
			pools[i%len(pools)],
			svms[i%len(svms)],
			accounts[0],
		)
	}
}

func cleanupResources(db *gorm.DB, volume *datamodel.Volume, replication *datamodel.VolumeReplication, pool *datamodel.Pool, svm *datamodel.Svm, account *datamodel.Account) {
	db.Delete(&replication, replication.ID)
	db.Delete(&volume, volume.ID)
	db.Delete(&svm, svm.ID)
	db.Delete(&pool, pool.ID)
	db.Delete(&account, account.ID)
}

func setupResources(vcp *gorm.DB) ([]*datamodel.Account, []*datamodel.Pool, []*datamodel.Svm, []*datamodel.Volume, []*datamodel.VolumeReplication, error) {
	var accounts []*datamodel.Account
	var pools []*datamodel.Pool
	var svms []*datamodel.Svm
	var volumes []*datamodel.Volume
	var replications []*datamodel.VolumeReplication

	ctx := context.Background()

	account, err := CreateAccount(vcp)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	accounts = append(accounts, account)

	tenantProjects, err := getActiveTenantProjects(ctx, parentFolderID, numTPs)
	if err != nil {
		log.Fatalf("Failed to get active tenant projects: %v", err)
	}

	if len(tenantProjects) == 0 {
		log.Fatal("No active tenant projects found")
	}
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	for t := 0; t < numTPs; t++ {
		for p := 0; p < numPoolsEachTP; p++ {
			pool, err := CreatePool(vcp, account, fmt.Sprintf("pool"), tenantProjects[t])
			if err != nil {
				return nil, nil, nil, nil, nil, err
			}
			pools = append(pools, pool)

			svm, err := CreateSvm(vcp, account, pool, fmt.Sprintf("svm"))
			if err != nil {
				return nil, nil, nil, nil, nil, err
			}
			svms = append(svms, svm)

			for v := 0; v < numVolumesEachPool; v++ {
				volume, err := CreateVolume(vcp, account, pool, svm, fmt.Sprintf("volume"))
				if err != nil {
					return nil, nil, nil, nil, nil, err
				}
				volumes = append(volumes, volume)

				replication, err := CreateReplication(vcp, account, volume, fmt.Sprintf("replication"))
				if err != nil {
					return nil, nil, nil, nil, nil, err
				}
				replications = append(replications, replication)
			}
		}
	}
	fmt.Println("Resources setup completed.")
	return accounts, pools, svms, volumes, replications, nil
}

func sendMetricsLoop(volume *datamodel.Volume, replication *datamodel.VolumeReplication, projectID string) {
	ctx := context.Background()
	client, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create metric client: %v", err)
	}
	defer client.Close()

	for {
		if err := pushCustomMetrics(ctx, client, volume, replication, projectID); err != nil {
			log.Printf("Error sending metrics: %v", err)
		}
		time.Sleep(5 * time.Minute)
	}
}

// Add to main.go
func buildReplication(name string, account *datamodel.Account, volume *datamodel.Volume) *datamodel.VolumeReplication {
	Uuid := uuid.New().String()
	externaluuid := uuid.New().String()
	return &datamodel.VolumeReplication{
		BaseModel:    datamodel.BaseModel{UUID: Uuid},
		Name:         name + "-" + Uuid,
		Description:  "Mocked Replication",
		State:        "Ready",
		StateDetails: "Available for use",
		Uri:          "replication-uri",
		RemoteUri:    "remote-replication-uri",
		ReplicationAttributes: &datamodel.ReplicationDetails{
			EndpointType:          "source",
			ReplicationType:       "async",
			ReplicationSchedule:   "daily",
			SourcePoolUUID:        volume.Pool.UUID,
			SourceVolumeUUID:      volume.UUID,
			SourceLocation:        "location1",
			SourceHostName:        "host1",
			SourceSvmName:         volume.Svm.Name,
			SourceVolumeName:      volume.Name,
			DestinationPoolUUID:   "dest-pool-uuid",
			DestinationVolumeUUID: "dest-vol-uuid",
			DestinationLocation:   "location2",
			DestinationHostName:   "host2",
			DestinationSvmName:    "dest-svm",
			DestinationVolumeName: "dest-vol",
			ExternalUUID:          externaluuid,
		},
		TotalTransferBytes: 10000,
		AccountID:          account.ID,
		Account:            account,
		VolumeID:           volume.ID,
		Volume:             volume,
		Healthy:            true,
	}
}

func CreateReplication(db *gorm.DB, account *datamodel.Account, volume *datamodel.Volume, name string) (*datamodel.VolumeReplication, error) {
	replication := buildReplication(name, account, volume)
	if err := db.Create(replication).Error; err != nil {
		return nil, err
	}
	return replication, nil
}
func buildSvm(name string, account *datamodel.Account, pool *datamodel.Pool) *datamodel.Svm {
	return &datamodel.Svm{
		BaseModel:    datamodel.BaseModel{UUID: uuid.New().String()},
		Name:         name,
		Description:  "Mocked SVM",
		State:        "Ready",
		StateDetails: "Available for use",
		AccountID:    account.ID,
		Account:      account,
		PoolID:       pool.ID,
		Pool:         pool,
	}
}

func CreateSvm(db *gorm.DB, account *datamodel.Account, pool *datamodel.Pool, name string) (*datamodel.Svm, error) {
	svm := buildSvm(name, account, pool)
	if err := db.Create(svm).Error; err != nil {
		return nil, err
	}
	return svm, nil
}

func CreateAccount(db *gorm.DB) (*datamodel.Account, error) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{
			UUID: uuid.New().String(),
		},
		Name:        ConsumerProjectID,
		Description: "Account Mocked",
		State:       "ENABLED",
		Tags:        "tag1,tag2",
		AccountMetadata: &datamodel.AccountMetadata{
			VolumeRefreshWorkflowLastCompletionAt: time.Now(),
		},
	}
	if err := db.Create(account).Error; err != nil {
		return nil, err
	}
	return account, nil
}

// Add these functions to main.go

func buildVolume(name string, account *datamodel.Account, pool *datamodel.Pool, svm *datamodel.Svm) *datamodel.Volume {
	Uuid := uuid.New().String()
	isRegionalHA := false
	if pool.PoolAttributes != nil {
		isRegionalHA = pool.PoolAttributes.IsRegionalHA
	}
	return &datamodel.Volume{
		BaseModel:          datamodel.BaseModel{UUID: Uuid},
		Name:               name + "-" + Uuid,
		Description:        "Mocked Volume",
		State:              "Ready",
		StateDetails:       "Available for use",
		Health:             "Healthy",
		MountPath:          "/mnt/" + name,
		SizeInBytes:        1073741824,
		Throughput:         pool.PoolAttributes.ThroughputMibps,
		AccountID:          account.ID,
		PoolID:             pool.ID,
		SvmID:              svm.ID,
		Svm:                svm,
		Account:            account,
		Pool:               pool,
		UsedBytes:          0,
		AutoTieringEnabled: true,
		VolumeAttributes: &datamodel.VolumeAttributes{
			AccountName:    account.Name,
			DeploymentName: pool.DeploymentName,
			IsRegionalHA:   isRegionalHA,
		},
	}
}

func CreateVolume(db *gorm.DB, account *datamodel.Account, pool *datamodel.Pool, svm *datamodel.Svm, name string) (*datamodel.Volume, error) {
	volume := buildVolume(name, account, pool, svm)
	if err := db.Create(volume).Error; err != nil {
		return nil, err
	}
	return volume, nil
}
func buildpool(name string, account *datamodel.Account, tenantProjects string) *datamodel.Pool {
	Uuid := uuid.New().String()
	pool := &datamodel.Pool{
		BaseModel:        datamodel.BaseModel{UUID: Uuid},
		Name:             name + "-" + Uuid,
		Description:      "Mocked Pool",
		State:            "Ready",
		StateDetails:     "Available for use",
		VendorID:         account.Name,
		ServiceLevel:     "flex",
		SizeInBytes:      1099511627776,
		UsedBytes:        1073741824,
		Network:          "10.0.0.0/24",
		AllowAutoTiering: true,
		AccountID:        account.ID,
		Account:          account,
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 1000,
			Iops:            5000,
			PrimaryZone:     "zone1",
			SecondaryZone:   "zone2",
			MediatorZone:    "zone3",
			Labels:          &datamodel.JSONB{"env": "test"},
			IsRegionalHA:    false,
			AccountName:     account.Name,
		},
		ClusterDetails: datamodel.ClusterDetails{
			ExternalName:          "cluster1",
			OntapVersion:          "9.10.1",
			RegionalTenantProject: "z257c6412e15fa257-tp",
			SnHostProject:         "hostproj1",
			Network:               "10.0.1.0/24",
			SubnetNames:           []string{"subnet1", "subnet2"},
			InterclusterLifIPs:    []string{"10.0.1.10"},
			ReservedIPsInSubnet:   &[]datamodel.SubnetToIPs{{SubnetName: "subnet1", IPsReserved: 2}},
		},
		QosType: "high",
		AutoTieringConfig: &datamodel.AutoTieringConfig{
			HotTierSizeInBytes:       100000000,
			EnableHotTierAutoResize:  true,
			BucketName:               "bucket1",
			HotTierConsumption:       50000000,
			ColdTierConsumption:      50000000,
			TieringFullnessThreshold: 80,
		},
		ServiceAccountId: "service-account-1",
		KmsConfigID:      sql.NullInt64{Int64: 2, Valid: false},
		DeploymentName:   "deployment-" + Uuid,
		PoolCredentials: &datamodel.PoolCredentials{
			SecretID:      "secret1",
			CertificateID: "cert1",
			Password:      "password",
			AuthType:      1,
		},
		SnHostProject: "sn-host-proj",
		VLMConfig:     "vlm-config-string",
		LargeCapacity: true,
		SatisfyZI:     true,
		SatisfyZS:     false,
		AssetMetadata: &datamodel.AssetMetadata{
			ChildAssets: []datamodel.ChildAsset{
				{AssetNames: []string{"asset1"}, AssetType: "type1"},
			},
		},
		BuildInfo: &datamodel.PoolBuildInfo{
			VSABuildImage:      "vsa-image",
			MediatorBuildImage: "mediator-image",
			OntapVersion:       "9.10.1",
			BuildTimestamp:     time.Now(),
		},
		ActiveDirectoryID: sql.NullInt64{Int64: 3, Valid: false},
	}
	return pool
}

func CreatePool(db *gorm.DB, account *datamodel.Account, name string, tenantProjects string) (*datamodel.Pool, error) {
	pool := buildpool(name, account, tenantProjects)
	if err := db.Create(pool).Error; err != nil {
		return nil, err
	}
	return pool, nil
}

func openGormConn(connStr string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(connStr), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	return db, nil
}
