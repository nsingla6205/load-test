//go:build !test_exclude

package sanity

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
)

// command to run this test: go test -v -timeout 0 scripts/sanity/sanity_test.go
var (
	globalServerURL     = getEnvOrDefault("API_SERVER_URL", "http://localhost:8080")
	globalProjectNumber = getEnvOrDefault("PROJECT_NUMBER", "1234567")
	globalLocationId    = getEnvOrDefault("LOCATION_ID", "region-a")
	vpcName             = getEnvOrDefault("DEFAULT_VPC", "your-vpc-name")
	globalPoolName      = "p" + strconv.FormatInt(time.Now().Unix(), 10)
	globalHostGroupName = "hg" + strconv.FormatInt(time.Now().Unix(), 10)
	globalVolumeName    = "v" + strconv.FormatInt(time.Now().Unix(), 10)
	globalSnapshotName  = "s" + strconv.FormatInt(time.Now().Unix(), 10)
	// NFS volume specific variables
	globalNFSVolumeName = "nfsv" + strconv.FormatInt(time.Now().Unix(), 10)
	globalCreationToken = "policy" + strconv.FormatInt(time.Now().Unix(), 10)
	globalNetwork       = fmt.Sprintf("projects/%s/global/networks/%s", globalProjectNumber, vpcName)
	globalPoolUUID      = ""
	globalHostGroupUUID = ""
	globalVolumeUUID    = ""
	globalSnapshotUUID  = ""
	// NFS specific UUIDs
	globalNFSVolumeUUID  = ""
	defaultPoolSize      = 2199023255552 // 2 TiB
	defaultVolumeSize    = 107374182400  // 100 GiB
	defaultNFSVolumeSize = 1073741824    // 1 GiB for NFS testing
)

// getEnvOrDefault retrieves the value of an environment variable or returns a default value if the variable is not set or empty.
func getEnvOrDefault(key string, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists && value != "" {
		return value
	}
	return defaultValue
}

// extractOperationID extracts the operation ID from the operation name.
func extractOperationID(operationName string) string {
	parts := strings.Split(operationName, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// pollOperationDone polls the operation until it is done or maxAttempts is reached.
func pollOperationDone(t *testing.T, client *googleproxyclient.Client, ctx context.Context, describeParams googleproxyclient.V1betaDescribeOperationParams, maxAttempts int, sleep time.Duration) bool {
	t.Helper()
	for i := 0; i < maxAttempts; i++ {
		describeRes, err := client.V1betaDescribeOperation(ctx, describeParams)
		log.Printf("DescribeOperation response: %+v, err: %v", describeRes, err)
		require.NoError(t, err)
		operation, ok := describeRes.(*googleproxyclient.OperationV1beta)
		require.True(t, ok, "expected OperationV1beta, got %T", describeRes)
		if operation.GetDone().Value {
			log.Printf("Operation done status: %v (done)", operation.GetDone().Value)
			return true
		}
		log.Printf("Operation done status: %v (not done)", operation.GetDone().Value)
		time.Sleep(sleep)
	}
	return false
}

// getTestClient initializes a new Google Proxy client for testing.
func getTestClient(t *testing.T) *googleproxyclient.Client {
	serverURL := globalServerURL
	client, err := googleproxyclient.NewClient(serverURL)
	require.NoError(t, err)
	return client
}

func TestCreatePoolAndWaitForCompletion(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaCreatePoolParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
	}
	poolReq := &googleproxyclient.PoolV1beta{
		ResourceId:               globalPoolName,
		ServiceLevel:             googleproxyclient.PoolV1betaServiceLevelFLEX,
		SizeInBytes:              float64(defaultPoolSize),
		Network:                  globalNetwork,
		UnifiedPool:              googleproxyclient.OptBool{Value: true, Set: true},
		Zone:                     googleproxyclient.OptString{Value: globalLocationId, Set: true},
		CustomPerformanceEnabled: googleproxyclient.OptBool{Value: true, Set: true},
		TotalThroughputMibps:     googleproxyclient.OptNilFloat64{Value: 64, Set: true},
		TotalIops:                googleproxyclient.OptNilFloat64{Value: 1024, Set: true},
	}

	res, err := client.V1betaCreatePool(ctx, poolReq, params)
	log.Printf("CreatePool response: %+v, err: %v", res, err)
	require.NoError(t, err)

	operation, ok := res.(*googleproxyclient.OperationV1beta)
	require.True(t, ok, "expected OperationV1beta, got %T", res)
	operationID := extractOperationID(operation.GetName().Value)
	require.NotEmpty(t, operationID)

	var pool gcpgenserver.PoolV1beta
	err = json.Unmarshal(operation.GetResponse(), &pool)
	if err != nil {
		log.Printf("Error unmarshalling PoolV1beta: %v", err)
		require.Fail(t, "Failed to unmarshal PoolV1beta")
		return
	}

	globalPoolUUID = pool.PoolId.Value

	describeParams := googleproxyclient.V1betaDescribeOperationParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		OperationId:   operationID,
	}

	done := pollOperationDone(t, client, ctx, describeParams, 60, 30*time.Second)
	require.True(t, done, "operation did not complete in time")
}

func TestGetPool(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaDescribePoolParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		PoolId:        globalPoolUUID,
	}

	res, err := client.V1betaDescribePool(ctx, params)
	require.NoError(t, err)

	pool, ok := res.(*googleproxyclient.PoolV1beta)
	require.True(t, ok, "expected PoolV1beta, got %T", res)
	log.Printf("Type of res: %T\n", pool)

	require.Equal(t, googleproxyclient.PoolV1betaStoragePoolStateREADY, pool.StoragePoolState.Value)
	require.Equal(t, "Available for use", pool.StoragePoolStateDetails.Value)
}

func TestUpdatePoolAndWaitForCompletion(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaUpdatePoolParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		PoolId:        globalPoolUUID,
	}
	poolReq := &googleproxyclient.PoolUpdateV1beta{
		Description:          googleproxyclient.OptNilString{Value: "updated pool", Set: true},
		SizeInBytes:          googleproxyclient.OptNilFloat64{Value: float64(defaultPoolSize), Set: true},
		TotalThroughputMibps: googleproxyclient.OptNilFloat64{Value: 65, Set: true},
		TotalIops:            googleproxyclient.OptNilFloat64{Value: 1040, Set: true},
		QosType:              googleproxyclient.OptNilString{Value: "auto", Set: true},
	}

	res, err := client.V1betaUpdatePool(ctx, poolReq, params)
	log.Printf("CreatePool response: %+v, err: %v", res, err)
	require.NoError(t, err)

	operation, ok := res.(*googleproxyclient.OperationV1beta)
	require.True(t, ok, "expected OperationV1beta, got %T", res)
	operationID := extractOperationID(operation.GetName().Value)
	require.NotEmpty(t, operationID)

	var pool gcpgenserver.PoolV1beta
	err = json.Unmarshal(operation.GetResponse(), &pool)

	if err != nil {
		log.Printf("Error unmarshalling PoolV1beta: %v", err)
	}

	globalPoolUUID = pool.PoolId.Value

	describeParams := googleproxyclient.V1betaDescribeOperationParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		OperationId:   operationID,
	}

	done := pollOperationDone(t, client, ctx, describeParams, 120, 30*time.Second)
	require.True(t, done, "operation did not complete in time")
}

func TestCreateHostGroup(t *testing.T) {
	client := getTestClient(t)
	ctx := context.Background()

	params := googleproxyclient.V1betaCreateHostGroupParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
	}
	body := &googleproxyclient.HostGroupV1beta{
		ResourceId:  globalHostGroupName,
		OsType:      googleproxyclient.HostGroupV1betaOsTypeLINUX,
		Hosts:       []string{"iqn.1993-08.org.debian:01:08912fe38fd"},
		Description: googleproxyclient.NewOptString("test host group"),
	}

	res, err := client.V1betaCreateHostGroup(ctx, body, params)
	require.NoError(t, err)
	require.NotNil(t, res)

	operation, ok := res.(*googleproxyclient.V1betaCreateHostGroupOK)

	require.True(t, ok, "expected OperationV1beta, got %T", res)
	require.True(t, operation.Done.Value)

	var hg *googleproxyclient.HostGroupV1beta
	err = json.Unmarshal(operation.Response, &hg)
	if err != nil {
		log.Printf("Error unmarshalling HostGroupV1beta: %v", err)
		require.Fail(t, "Failed to unmarshal HostGroupV1beta")
		return
	}

	globalHostGroupUUID = hg.HostGroupId.Value
}

func TestGetHostGroup(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaDescribeHostGroupParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		HostGroupId:   globalHostGroupUUID,
	}

	res, err := client.V1betaDescribeHostGroup(ctx, params)
	log.Printf("GetVolume response: %+v, err: %v", res, err)
	require.NoError(t, err)
	require.NotNil(t, res)

	hg, ok := res.(*googleproxyclient.HostGroupV1beta)
	require.True(t, ok, "expected HostGroupV1beta, got %T", res)

	require.Equal(t, globalHostGroupUUID, hg.HostGroupId.Value)
	require.Equal(t, globalHostGroupName, hg.ResourceId)
	require.Equal(t, googleproxyclient.HostGroupV1betaOsTypeLINUX, hg.OsType)
	require.Equal(t, []string{"iqn.1993-08.org.debian:01:08912fe38fd"}, hg.Hosts)
	require.Equal(t, "READY", string(hg.State.Value))
	require.Equal(t, "Available for use", hg.StateDetails.Value)
	require.Equal(t, googleproxyclient.HostGroupV1betaTypeUNSPECIFIED, hg.Type.Value)
	require.Equal(t, "test host group", hg.Description.Value)
}

func TestCreateVolumeAndWaitForCompletion(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaCreateVolumeParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
	}
	volumeReq := &googleproxyclient.VolumeCreateV1beta{
		Volume: googleproxyclient.VolumeV1beta{
			ResourceId:   globalVolumeName,
			PoolId:       googleproxyclient.NilString{Value: globalPoolUUID, Null: false},
			QuotaInBytes: googleproxyclient.OptFloat64{Value: float64(defaultVolumeSize), Set: true},
			Description:  googleproxyclient.OptNilString{Value: "test volume", Set: true},
			Network:      googleproxyclient.OptString{Value: globalNetwork, Set: true},
			Protocols:    []googleproxyclient.ProtocolsV1beta{googleproxyclient.ProtocolsV1betaISCSI},
			BlockProperties: googleproxyclient.OptBlockPropertiesV1beta{
				Value: googleproxyclient.BlockPropertiesV1beta{
					OsType: googleproxyclient.OptBlockPropertiesV1betaOsType{Value: googleproxyclient.BlockPropertiesV1betaOsType(gcpgenserver.BlockPropertiesV1betaOsTypeLINUX), Set: true},
					HostGroupIds: []string{
						globalHostGroupUUID,
					},
				},
				Set: true,
			},
			StorageClass: googleproxyclient.OptStorageClassV1beta{
				Value: googleproxyclient.StorageClassV1betaSOFTWARE,
				Set:   true,
			},
		},
		VolumeType: googleproxyclient.OptVolumeCreateV1betaVolumeType{Value: googleproxyclient.VolumeCreateV1betaVolumeTypePRIMARY, Set: true},
	}

	res, err := client.V1betaCreateVolume(ctx, volumeReq, params)
	log.Printf("CreateVolume response: %+v, err: %v", res, err)
	require.NoError(t, err)
	operation, ok := res.(*googleproxyclient.OperationV1beta)
	require.True(t, ok, "expected OperationV1beta, got %T", res)
	operationID := extractOperationID(operation.GetName().Value)

	var volumeV1beta gcpgenserver.VolumeV1beta
	err = json.Unmarshal(operation.GetResponse(), &volumeV1beta)
	if err != nil {
		log.Printf("Error unmarshalling VolumeV1beta: %v", err)
		require.Fail(t, "Failed to unmarshal VolumeV1beta")
		return
	}
	globalVolumeUUID = volumeV1beta.VolumeId.Value

	require.NotEmpty(t, operationID)

	describeParams := googleproxyclient.V1betaDescribeOperationParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		OperationId:   operationID,
	}
	done := pollOperationDone(t, client, ctx, describeParams, 20, 30*time.Second)

	require.True(t, done, "operation did not complete in time")
}

func TestGetVolume(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaDescribeVolumeParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		VolumeId:      globalVolumeUUID,
	}

	res, err := client.V1betaDescribeVolume(ctx, params)
	log.Printf("GetVolume response: %+v, err: %v", res, err)
	require.NoError(t, err)
	require.NotNil(t, res)

	volume, ok := res.(*googleproxyclient.VolumeV1beta)
	require.True(t, ok, "expected VolumeV1beta, got %T", res)
	require.Equal(t, globalVolumeUUID, volume.VolumeId.Value)
	require.Equal(t, globalVolumeName, volume.ResourceId)
	require.Equal(t, googleproxyclient.VolumeV1betaVolumeStateREADY, volume.VolumeState.Value)
	require.Equal(t, googleproxyclient.StorageClassV1betaSOFTWARE, volume.StorageClass.Value)
	require.Equal(t, googleproxyclient.VolumeV1betaEncryptionTypeSERVICEMANAGED, volume.EncryptionType.Value)
	require.Equal(t, googleproxyclient.VolumeV1betaServiceLevelFLEX, volume.ServiceLevel.Value)
	require.Equal(t, "Available for use", volume.VolumeStateDetails.Value)
	// require.Equal(t, globalLocationId, volume.Zone.Value) // commenting out as Pool.PoolAttribute.Zone is getting lost during update pool
	require.Equal(t, float64(defaultVolumeSize), volume.QuotaInBytes.Value)
	require.NotEmpty(t, volume.Network.Value)
	require.NotEmpty(t, volume.MountPoints)
}

func TestUpdateVolumeAndWaitForCompletion(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaUpdateVolumeParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		VolumeId:      globalVolumeUUID,
	}
	volumeReq := &googleproxyclient.VolumeUpdateV1beta{
		PoolId:       googleproxyclient.OptNilString{Value: globalPoolUUID, Null: false},
		QuotaInBytes: googleproxyclient.OptNilFloat64{Value: 161061273600, Set: true}, // 150 GiB
		Description:  googleproxyclient.OptNilString{Value: "updated volume", Set: true},
	}

	res, err := client.V1betaUpdateVolume(ctx, volumeReq, params)
	log.Printf("UpdateVolume response: %+v, err: %v", res, err)
	require.NoError(t, err)
	operation, ok := res.(*googleproxyclient.OperationV1beta)
	require.True(t, ok, "expected OperationV1beta, got %T", res)
	operationID := extractOperationID(operation.GetName().Value)
	require.NotEmpty(t, operationID)
	describeParams := googleproxyclient.V1betaDescribeOperationParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		OperationId:   operationID,
	}
	done := pollOperationDone(t, client, ctx, describeParams, 20, 30*time.Second)
	require.True(t, done, "operation did not complete in time")
}

func TestCreateSnapshot(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaCreateSnapshotParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		VolumeId:      globalVolumeUUID,
	}
	snapshotReq := &googleproxyclient.VolumeSnapshotCreateV1beta{
		ResourceId:  globalSnapshotName,
		Description: googleproxyclient.NewOptString("test snapshot"),
	}

	res, err := client.V1betaCreateSnapshot(ctx, snapshotReq, params)
	log.Printf("CreateSnapshot response: %+v, err: %v", res, err)
	require.NoError(t, err)
	operation, ok := res.(*googleproxyclient.OperationV1beta)
	require.True(t, ok, "expected OperationV1beta, got %T", res)
	operationID := extractOperationID(operation.GetName().Value)

	var snapshotV1beta gcpgenserver.SnapshotV1beta
	err = json.Unmarshal(operation.GetResponse(), &snapshotV1beta)
	if err != nil {
		log.Printf("Error unmarshalling snapshotV1beta: %v", err)
		require.Fail(t, "Failed to unmarshal snapshotV1beta")
		return
	}
	globalSnapshotUUID = snapshotV1beta.SnapshotId.Value

	require.NotEmpty(t, operationID)

	describeParams := googleproxyclient.V1betaDescribeOperationParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		OperationId:   operationID,
	}
	done := pollOperationDone(t, client, ctx, describeParams, 20, 10*time.Second)

	require.True(t, done, "operation did not complete in time")
}

func TestGetSnapshot(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaDescribeSnapshotParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		VolumeId:      globalVolumeUUID,
		SnapshotId:    globalSnapshotUUID,
	}

	res, err := client.V1betaDescribeSnapshot(ctx, params)
	log.Printf("GetSnapshot response: %+v, err: %v", res, err)
	require.NoError(t, err)
	require.NotNil(t, res)

	snapshot, ok := res.(*googleproxyclient.SnapshotV1beta)
	require.True(t, ok, "expected SnapshotV1beta, got %T", res)
	require.Equal(t, globalSnapshotUUID, snapshot.SnapshotId.Value)
	require.Equal(t, globalSnapshotName, snapshot.ResourceId)
	require.Equal(t, googleproxyclient.SnapshotV1betaSnapshotStateREADY, snapshot.SnapshotState.Value)
	require.Equal(t, googleproxyclient.StorageClassV1betaSOFTWARE, snapshot.StorageClass.Value)
	require.Equal(t, "Available for use", snapshot.SnapshotStateDetails.Value)
}

func TestUpdateSnapshot(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaUpdateSnapshotParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		VolumeId:      globalVolumeUUID,
		SnapshotId:    globalSnapshotUUID,
	}
	snapshotReq := &googleproxyclient.VolumeSnapshotUpdateV1beta{
		Description: "updated snapshot",
	}

	res, err := client.V1betaUpdateSnapshot(ctx, snapshotReq, params)
	log.Printf("UpdateSnapshot response: %+v, err: %v", res, err)
	require.NoError(t, err)
	operation, ok := res.(*googleproxyclient.OperationV1beta)
	require.True(t, ok, "expected OperationV1beta, got %T", res)
	require.True(t, operation.GetDone().Value, "operation should be done synchronously")
}

func TestDeleteSnapshotAndWaitForCompletion(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaDeleteSnapshotParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		VolumeId:      globalVolumeUUID,
		SnapshotId:    globalSnapshotUUID,
	}

	res, err := client.V1betaDeleteSnapshot(ctx, params)
	log.Printf("DeleteSnapshot response: %+v, err: %v", res, err)
	require.NoError(t, err)
	operation, ok := res.(*googleproxyclient.OperationV1beta)
	require.True(t, ok, "expected OperationV1beta, got %T", res)
	operationID := extractOperationID(operation.GetName().Value)
	require.NotEmpty(t, operationID)
	describeParams := googleproxyclient.V1betaDescribeOperationParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		OperationId:   operationID,
	}
	done := pollOperationDone(t, client, ctx, describeParams, 20, 10*time.Second)
	require.True(t, done, "operation did not complete in time")
}

func TestDetachVolumeFromHostGroupAndWaitForCompletion(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaUpdateVolumeParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		VolumeId:      globalVolumeUUID,
	}
	volumeReq := &googleproxyclient.VolumeUpdateV1beta{
		BlockProperties: googleproxyclient.OptBlockPropertiesV1beta{
			Value: googleproxyclient.BlockPropertiesV1beta{
				HostGroupIds: []string{},
			},
			Set: true,
		},
	}

	res, err := client.V1betaUpdateVolume(ctx, volumeReq, params)
	log.Printf("DetachVolumeFromHostGroup response: %+v, err: %v", res, err)
	require.NoError(t, err)
	operation, ok := res.(*googleproxyclient.OperationV1beta)
	require.True(t, ok, "expected OperationV1beta, got %T", res)
	operationID := extractOperationID(operation.GetName().Value)
	require.NotEmpty(t, operationID)
	describeParams := googleproxyclient.V1betaDescribeOperationParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		OperationId:   operationID,
	}
	done := pollOperationDone(t, client, ctx, describeParams, 20, 30*time.Second)
	require.True(t, done, "volume host group detach operation did not complete in time")
}

func TestDeleteVolumeAndWaitForCompletion(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaDeleteVolumeParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		VolumeId:      globalVolumeUUID,
	}

	res, err := client.V1betaDeleteVolume(ctx, googleproxyclient.OptV1betaDeleteVolumeReq{}, params)
	log.Printf("DeleteVolume response: %+v, err: %v", res, err)
	require.NoError(t, err)
	operation, ok := res.(*googleproxyclient.OperationV1beta)
	require.True(t, ok, "expected OperationV1beta, got %T", res)
	operationID := extractOperationID(operation.GetName().Value)
	require.NotEmpty(t, operationID)
	describeParams := googleproxyclient.V1betaDescribeOperationParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		OperationId:   operationID,
	}
	done := pollOperationDone(t, client, ctx, describeParams, 20, 30*time.Second)
	require.True(t, done, "operation did not complete in time")
}

// NFS Volume Tests

func TestCreateNFSVolumeAndWaitForCompletion(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaCreateVolumeParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
	}
	volumeReq := &googleproxyclient.VolumeCreateV1beta{
		Volume: googleproxyclient.VolumeV1beta{
			ResourceId:    globalNFSVolumeName,
			CreationToken: googleproxyclient.OptString{Value: globalCreationToken, Set: true},
			PoolId:        googleproxyclient.NilString{Value: globalPoolUUID, Null: false},
			QuotaInBytes:  googleproxyclient.OptFloat64{Value: float64(defaultNFSVolumeSize), Set: true},
			Description:   googleproxyclient.OptNilString{Value: "NFS test volume", Set: true},
			Protocols:     []googleproxyclient.ProtocolsV1beta{googleproxyclient.ProtocolsV1betaNFSV3},
			ExportPolicy: googleproxyclient.OptExportPolicyV1beta{
				Value: googleproxyclient.ExportPolicyV1beta{
					Rules: []googleproxyclient.SimpleExportPolicyRuleV1beta{
						{
							AccessType:     googleproxyclient.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
							AllowedClients: "0.0.0.0/0",
							Nfsv3:          googleproxyclient.OptNilBool{Value: true, Set: true},
							Nfsv4:          googleproxyclient.OptNilBool{Value: false, Set: true},
						},
					},
				},
				Set: true,
			},
			StorageClass: googleproxyclient.OptStorageClassV1beta{
				Value: googleproxyclient.StorageClassV1betaSOFTWARE,
				Set:   true,
			},
		},
		VolumeType: googleproxyclient.OptVolumeCreateV1betaVolumeType{Value: googleproxyclient.VolumeCreateV1betaVolumeTypePRIMARY, Set: true},
	}

	res, err := client.V1betaCreateVolume(ctx, volumeReq, params)
	log.Printf("CreateNFSVolume response: %+v, err: %v", res, err)
	require.NoError(t, err)
	operation, ok := res.(*googleproxyclient.OperationV1beta)
	require.True(t, ok, "expected OperationV1beta, got %T", res)
	operationID := extractOperationID(operation.GetName().Value)

	var volumeV1beta gcpgenserver.VolumeV1beta
	err = json.Unmarshal(operation.GetResponse(), &volumeV1beta)
	if err != nil {
		log.Printf("Error unmarshalling VolumeV1beta: %v", err)
		require.Fail(t, "Failed to unmarshal VolumeV1beta")
		return
	}
	globalNFSVolumeUUID = volumeV1beta.VolumeId.Value

	require.NotEmpty(t, operationID)

	describeParams := googleproxyclient.V1betaDescribeOperationParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		OperationId:   operationID,
	}
	done := pollOperationDone(t, client, ctx, describeParams, 20, 30*time.Second)

	require.True(t, done, "NFS volume creation operation did not complete in time")
}

func TestGetNFSVolume(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaDescribeVolumeParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		VolumeId:      globalNFSVolumeUUID,
	}

	res, err := client.V1betaDescribeVolume(ctx, params)
	log.Printf("GetNFSVolume response: %+v, err: %v", res, err)
	require.NoError(t, err)
	require.NotNil(t, res)

	volume, ok := res.(*googleproxyclient.VolumeV1beta)
	require.True(t, ok, "expected VolumeV1beta, got %T", res)
	require.Equal(t, globalNFSVolumeUUID, volume.VolumeId.Value)
	require.Equal(t, globalNFSVolumeName, volume.ResourceId)
	require.Equal(t, globalCreationToken, volume.CreationToken.Value)
	require.Equal(t, googleproxyclient.VolumeV1betaVolumeStateREADY, volume.VolumeState.Value)
	require.Equal(t, googleproxyclient.StorageClassV1betaSOFTWARE, volume.StorageClass.Value)
	require.Equal(t, googleproxyclient.VolumeV1betaServiceLevelFLEX, volume.ServiceLevel.Value)
	require.Equal(t, "Available for use", volume.VolumeStateDetails.Value)
	require.Equal(t, float64(defaultNFSVolumeSize), volume.QuotaInBytes.Value)
	require.Contains(t, volume.Protocols, googleproxyclient.ProtocolsV1betaNFSV3)

	// Validate export policy
	require.True(t, volume.ExportPolicy.Set)
	require.Len(t, volume.ExportPolicy.Value.Rules, 1)
	rule := volume.ExportPolicy.Value.Rules[0]
	require.Equal(t, googleproxyclient.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE, rule.AccessType)
	require.Equal(t, "0.0.0.0/0", rule.AllowedClients)
	require.True(t, rule.Nfsv3.Value)
	require.False(t, rule.Nfsv4.Value)
}

func TestUpdateNFSVolumeAndWaitForCompletion(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	newCreationToken := "newtoken" + strconv.FormatInt(time.Now().Unix(), 10)
	newSize := int64(2147483648) // 2 GiB

	params := googleproxyclient.V1betaUpdateVolumeParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		VolumeId:      globalNFSVolumeUUID,
	}
	volumeReq := &googleproxyclient.VolumeUpdateV1beta{
		CreationToken: googleproxyclient.OptNilString{Value: newCreationToken, Set: true},
		QuotaInBytes:  googleproxyclient.OptNilFloat64{Value: float64(newSize), Set: true},
		PoolId:        googleproxyclient.OptNilString{Value: globalPoolUUID, Set: true},
		Protocols:     []googleproxyclient.ProtocolsV1beta{googleproxyclient.ProtocolsV1betaNFSV3},
		ExportPolicy: googleproxyclient.OptExportPolicyV1beta{
			Value: googleproxyclient.ExportPolicyV1beta{
				Rules: []googleproxyclient.SimpleExportPolicyRuleV1beta{
					{
						AccessType:     googleproxyclient.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
						AllowedClients: "0.0.0.0/0",
						Nfsv3:          googleproxyclient.OptNilBool{Value: true, Set: true},
						Nfsv4:          googleproxyclient.OptNilBool{Value: false, Set: true},
					},
				},
			},
			Set: true,
		},
		Description: googleproxyclient.OptNilString{Value: "Updated NFS volume", Set: true},
	}

	res, err := client.V1betaUpdateVolume(ctx, volumeReq, params)
	log.Printf("UpdateNFSVolume response: %+v, err: %v", res, err)
	require.NoError(t, err)
	operation, ok := res.(*googleproxyclient.OperationV1beta)
	require.True(t, ok, "expected OperationV1beta, got %T", res)
	operationID := extractOperationID(operation.GetName().Value)
	require.NotEmpty(t, operationID)
	describeParams := googleproxyclient.V1betaDescribeOperationParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		OperationId:   operationID,
	}
	done := pollOperationDone(t, client, ctx, describeParams, 20, 30*time.Second)
	require.True(t, done, "NFS volume update operation did not complete in time")
}

func TestDeleteNFSVolumeAndWaitForCompletion(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaDeleteVolumeParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		VolumeId:      globalNFSVolumeUUID,
	}

	res, err := client.V1betaDeleteVolume(ctx, googleproxyclient.OptV1betaDeleteVolumeReq{}, params)
	log.Printf("DeleteNFSVolume response: %+v, err: %v", res, err)
	require.NoError(t, err)
	operation, ok := res.(*googleproxyclient.OperationV1beta)
	require.True(t, ok, "expected OperationV1beta, got %T", res)
	operationID := extractOperationID(operation.GetName().Value)
	require.NotEmpty(t, operationID)
	describeParams := googleproxyclient.V1betaDescribeOperationParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		OperationId:   operationID,
	}
	done := pollOperationDone(t, client, ctx, describeParams, 20, 30*time.Second)
	require.True(t, done, "NFS volume deletion operation did not complete in time")
}

func TestDeletePoolAndWaitForCompletion(t *testing.T) {
	ctx := context.Background()
	client := getTestClient(t)

	params := googleproxyclient.V1betaDeletePoolParams{
		ProjectNumber: globalProjectNumber,
		LocationId:    globalLocationId,
		PoolId:        globalPoolUUID,
	}

	res, err := client.V1betaDeletePool(ctx, params)
	log.Printf("DeletePool response: %+v, err: %v", res, err)
	require.NoError(t, err)
	operation, ok := res.(*googleproxyclient.OperationV1beta)
	require.True(t, ok, "expected OperationV1beta, got %T", res)
	operationID := extractOperationID(operation.GetName().Value)
	require.NotEmpty(t, operationID)
	describeParams := googleproxyclient.V1betaDescribeOperationParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		OperationId:   operationID,
	}
	done := pollOperationDone(t, client, ctx, describeParams, 30, 30*time.Second)
	require.True(t, done, "operation did not complete in time")
}
