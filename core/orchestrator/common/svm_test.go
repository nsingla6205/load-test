package common

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func TestDeriveVCMSvmName(t *testing.T) {
	got, err := DeriveVCMSvmName("gcnv-e29621e4df3c677")
	require.NoError(t, err)
	assert.Equal(t, "gcnv-e29621e4df3c677-svm-01", got)

	_, err = DeriveVCMSvmName("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deployment ID is required")
}

func TestUpdatePoolNasLifIPs_WritesVLMConfigAndLifRows(t *testing.T) {
	const previous = "10.33.182.10/32"
	config := `{
		"deployment": {"deployment_id": "keep-me"},
		"svm": {
			"svm1": {
				"svm_name": "svm1",
				"svm_lifs": {
					"ilbnas": [{"lif_name": "svm1-ilbnas", "ip": "` + previous + `", "home_node": "node-1"}],
					"san": [{"lif_name": "svm1-san", "ip": "10.1.1.8"}]
				}
			}
		}
	}`
	store := &fakeNasLifStore{
		pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-1"},
			AccountID: 3,
			VLMConfig: config,
		},
		nodes: []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 11}}},
		lifs: []*datamodel.Lif{
			{
				BaseModel:  datamodel.BaseModel{UUID: "lif-nas"},
				Name:       "svm1-ilbnas",
				IPAddress:  "10.33.182.10",
				LifDetails: &datamodel.LifDetails{ProtocolType: "nas"},
			},
			{
				BaseModel:  datamodel.BaseModel{UUID: "lif-nas-2"},
				Name:       "svm1-ilbnas",
				IPAddress:  "10.33.182.10",
				LifDetails: &datamodel.LifDetails{ProtocolType: ""},
			},
			{
				BaseModel:  datamodel.BaseModel{UUID: "lif-san"},
				Name:       "svm1-san",
				IPAddress:  "10.1.1.8",
				LifDetails: &datamodel.LifDetails{ProtocolType: "san"},
			},
		},
	}

	results, err := UpdatePoolNasLifIPs(context.Background(), store, "pool-1", []NasLifIPUpdate{{
		LifName:   " svm1-ilbnas ",
		IPAddress: " 10.33.182.243 ",
	}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "svm1-ilbnas", results[0].LifName)
	assert.Equal(t, previous, results[0].PreviousIPAddress)
	assert.Equal(t, "10.33.182.243", results[0].IPAddress)
	assert.Equal(t, []string{NasLifLocationVLMConfig, NasLifLocationLifs}, results[0].UpdatedLocations)

	var written map[string]any
	require.NoError(t, json.Unmarshal([]byte(store.appliedVLM), &written))
	assert.Equal(t, "keep-me", written["deployment"].(map[string]any)["deployment_id"])
	ilb := written["svm"].(map[string]any)["svm1"].(map[string]any)["svm_lifs"].(map[string]any)["ilbnas"].([]any)
	assert.Equal(t, "10.33.182.243/32", ilb[0].(map[string]any)["ip"])
	san := written["svm"].(map[string]any)["svm1"].(map[string]any)["svm_lifs"].(map[string]any)["san"].([]any)
	assert.Equal(t, "10.1.1.8", san[0].(map[string]any)["ip"])

	require.Len(t, store.appliedLifs, 2)
	assert.Equal(t, "10.33.182.243", store.appliedLifs[0].IPAddress)
	assert.Equal(t, "10.33.182.243", store.appliedLifs[1].IPAddress)
	assert.ElementsMatch(t, []string{"lif-nas", "lif-nas-2"}, []string{store.appliedLifs[0].UUID, store.appliedLifs[1].UUID})
}

func TestUpdatePoolNasLifIPs_NasTypeWithoutLifRow(t *testing.T) {
	store := &fakeNasLifStore{
		pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-1"},
			VLMConfig: `{"svm":{"svm1":{"svm_lifs":{"nas":[{"lif_name":"svm1-nas","ip":"10.0.0.5"}]}}}}`,
		},
		nodes: []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}}},
	}

	results, err := UpdatePoolNasLifIPs(context.Background(), store, "pool-1", []NasLifIPUpdate{{
		LifName:   "svm1-nas",
		IPAddress: "10.0.0.9",
	}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, []string{NasLifLocationVLMConfig}, results[0].UpdatedLocations)
	assert.Empty(t, store.appliedLifs)
	assert.Contains(t, store.appliedVLM, `"ip":"10.0.0.9"`)
}

func TestUpdatePoolNasLifIPs_SkipsSanProtocolRow(t *testing.T) {
	store := &fakeNasLifStore{
		pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 7, UUID: "pool-1"},
			VLMConfig: `{"svm":{"svm1":{"svm_lifs":{"ilbnas":[{"lif_name":"shared","ip":"10.0.0.5"}]}}}}`,
		},
		nodes: []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}}},
		lifs: []*datamodel.Lif{{
			BaseModel:  datamodel.BaseModel{UUID: "san-row"},
			Name:       "shared",
			LifDetails: &datamodel.LifDetails{ProtocolType: "san"},
		}},
	}

	results, err := UpdatePoolNasLifIPs(context.Background(), store, "pool-1", []NasLifIPUpdate{{
		LifName:   "shared",
		IPAddress: "10.0.0.9",
	}})
	require.NoError(t, err)
	assert.Equal(t, []string{NasLifLocationVLMConfig}, results[0].UpdatedLocations)
	assert.Empty(t, store.appliedLifs)
}

func TestUpdatePoolNasLifIPs_ValidationAndNotFound(t *testing.T) {
	validConfig := `{"svm":{"svm1":{"svm_lifs":{"ilbnas":[{"lif_name":"svm1-ilbnas","ip":"10.0.0.5"}]}}}}`
	tests := []struct {
		name       string
		store      *fakeNasLifStore
		updates    []NasLifIPUpdate
		trackingID int
	}{
		{
			name:       "pool not found",
			store:      &fakeNasLifStore{getPoolErr: customerrors.NewNotFoundErr("Pool", nil)},
			updates:    []NasLifIPUpdate{{LifName: "svm1-ilbnas", IPAddress: "10.0.0.9"}},
			trackingID: vsaerrors.ErrPoolNotFound,
		},
		{
			name:       "unknown lif",
			store:      &fakeNasLifStore{pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}, VLMConfig: validConfig}},
			updates:    []NasLifIPUpdate{{LifName: "missing", IPAddress: "10.0.0.9"}},
			trackingID: vsaerrors.ErrInputValidationError,
		},
		{
			name:       "invalid ip",
			store:      &fakeNasLifStore{pool: &datamodel.Pool{VLMConfig: validConfig}},
			updates:    []NasLifIPUpdate{{LifName: "svm1-ilbnas", IPAddress: "not-an-ip"}},
			trackingID: vsaerrors.ErrInputValidationError,
		},
		{
			name:  "duplicate lif",
			store: &fakeNasLifStore{pool: &datamodel.Pool{VLMConfig: validConfig}},
			updates: []NasLifIPUpdate{
				{LifName: "svm1-ilbnas", IPAddress: "10.0.0.9"},
				{LifName: "svm1-ilbnas", IPAddress: "10.0.0.10"},
			},
			trackingID: vsaerrors.ErrInputValidationError,
		},
		{
			name:       "empty config",
			store:      &fakeNasLifStore{pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}}},
			updates:    []NasLifIPUpdate{{LifName: "svm1-ilbnas", IPAddress: "10.0.0.9"}},
			trackingID: vsaerrors.ErrInputValidationError,
		},
		{
			name:       "no updates",
			store:      &fakeNasLifStore{},
			trackingID: vsaerrors.ErrInputValidationError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := UpdatePoolNasLifIPs(context.Background(), tt.store, "pool-1", tt.updates)
			require.Error(t, err)
			var customErr *vsaerrors.CustomError
			require.ErrorAs(t, err, &customErr)
			assert.True(t, customErr.IsError(tt.trackingID))
			assert.Empty(t, tt.store.appliedVLM)
		})
	}
}

func TestListPoolNasLifs(t *testing.T) {
	store := &fakeNasLifStore{
		pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			VLMConfig: `{
				"deployment": {"deployment_id": "keep-me"},
				"svm": {
					"svm2": {
						"svm_lifs": {
							"ilbnas": [{"lif_name": "svm2-ilbnas", "ip": "10.0.2.5/32"}]
						}
					},
					"svm1": {
						"svm_lifs": {
							"nas": [{"lif_name": "svm1-nas", "ip": "10.0.1.6"}],
							"ilbnas": [{"lif_name": "svm1-ilbnas", "ip": "10.0.1.5/32"}],
							"san": [{"lif_name": "svm1-san", "ip": "10.0.1.7"}]
						}
					}
				}
			}`,
		},
	}

	got, err := ListPoolNasLifs(context.Background(), store, "pool-1")
	require.NoError(t, err)
	assert.Equal(t, []PoolNasLif{
		{SVMName: "svm1", LifName: "svm1-ilbnas", LifType: "ilbnas", IPAddress: "10.0.1.5/32"},
		{SVMName: "svm1", LifName: "svm1-nas", LifType: "nas", IPAddress: "10.0.1.6"},
		{SVMName: "svm2", LifName: "svm2-ilbnas", LifType: "ilbnas", IPAddress: "10.0.2.5/32"},
	}, got)
}

func TestListPoolNasLifs_PoolNotFound(t *testing.T) {
	_, err := ListPoolNasLifs(context.Background(), &fakeNasLifStore{getPoolErr: customerrors.NewNotFoundErr("Pool", nil)}, "pool-1")
	require.Error(t, err)

	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.True(t, customErr.IsError(vsaerrors.ErrPoolNotFound))
}

type fakeNasLifStore struct {
	pool        *datamodel.Pool
	nodes       []*datamodel.Node
	lifs        []*datamodel.Lif
	getPoolErr  error
	nodesErr    error
	lifsErr     error
	applyErr    error
	appliedVLM  string
	appliedLifs []NasLifIPWrite
}

func (f *fakeNasLifStore) GetPoolByUUID(context.Context, string) (*datamodel.Pool, error) {
	if f.getPoolErr != nil {
		return nil, f.getPoolErr
	}
	return f.pool, nil
}

func (f *fakeNasLifStore) GetNodesByPoolID(context.Context, int64) ([]*datamodel.Node, error) {
	if f.nodesErr != nil {
		return nil, f.nodesErr
	}
	return f.nodes, nil
}

func (f *fakeNasLifStore) GetLifsForNodesWithProtocol(context.Context, []int64, int64, string) ([]*datamodel.Lif, error) {
	if f.lifsErr != nil {
		return nil, f.lifsErr
	}
	return f.lifs, nil
}

func (f *fakeNasLifStore) ApplyPoolNasLifIPs(_ context.Context, _, vlmConfig string, lifs []NasLifIPWrite) error {
	if f.applyErr != nil {
		return f.applyErr
	}
	f.appliedVLM = vlmConfig
	f.appliedLifs = lifs
	return nil
}

func (f *fakeNasLifStore) ApplyPoolNasLifIPUpdates(ctx context.Context, poolUUID string, updates []NasLifIPUpdate) ([]NasLifIPUpdateResult, error) {
	if f.applyErr != nil {
		return nil, f.applyErr
	}
	pool, err := f.GetPoolByUUID(ctx, poolUUID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrPoolNotFound, err)
		}
		return nil, err
	}
	if pool == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrPoolNotFound, customerrors.NewNotFoundErr("Pool", &poolUUID))
	}
	updatedConfig, applied, err := ApplyNasLifIPsToConfig(pool.VLMConfig, updates)
	if err != nil {
		return nil, err
	}
	f.appliedVLM = updatedConfig
	writes, locations, err := nasLifWrites(ctx, f, pool, updates, mapAppliedNasLifs(applied))
	if err != nil {
		return nil, err
	}
	f.appliedLifs = writes

	results := make([]NasLifIPUpdateResult, 0, len(updates))
	for _, update := range updates {
		change := applied[update.LifName]
		results = append(results, NasLifIPUpdateResult{
			LifName:           update.LifName,
			PreviousIPAddress: change.PreviousIPAddress,
			IPAddress:         update.IPAddress,
			UpdatedLocations:  locations[update.LifName],
		})
	}
	return results, nil
}

func mapAppliedNasLifs(applied map[string]NasLifIPApplied) map[string]appliedNasLif {
	out := make(map[string]appliedNasLif, len(applied))
	for lifName, change := range applied {
		out[lifName] = appliedNasLif{
			previous: change.PreviousIPAddress,
			stored:   change.StoredIPAddress,
		}
	}
	return out
}
