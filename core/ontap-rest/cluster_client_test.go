package ontap_rest

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/cluster"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	securitypriv "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/client/operations"
	privmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestClusterPeerList(t *testing.T) {
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		response, err := client.ClusterPeersList()
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, response)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		links := models.ClusterPeerResponseInlineLinks{
			Self: nil,
		}
		name := "name"
		transport := &mockTransport{response: &cluster.ClusterPeerCollectionGetOK{
			Payload: &models.ClusterPeerResponse{
				Links: &links,
				ClusterPeerResponseInlineRecords: []*models.ClusterPeer{
					{
						Authentication: &models.ClusterPeerInlineAuthentication{State: &name},
						Name:           nil,
						Remote:         &models.ClusterPeerInlineRemote{Name: &name},
						Status:         &models.ClusterPeerInlineStatus{State: &name},
						UUID:           &name,
					},
				},
				NumRecords: nillable.ToPointer(int64(1)),
			},
		}}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		response, err := client.ClusterPeersList()
		assert.NoError(tt, err)
		assert.NotEmpty(tt, response)
	})
}

func TestClusterPeerDelete(t *testing.T) {
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		err := client.ClusterPeerDelete("someUUID")
		assert.EqualError(tt, err, transport.err.Error())
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		transport := &mockTransport{response: &cluster.ClusterPeerDeleteOK{}}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		err := client.ClusterPeerDelete("someUUID")
		assert.NoError(tt, err)
	})
}

func TestClusterPeerCreate(t *testing.T) {
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := securitypriv.New(transport, nil)
		client := &clusterClient{apiPriv: &clust}
		response, err := client.ClusterPeerCreate(ClusterPeerCreateParams{})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, response)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		links := privmodels.ClusterPeerSetupResponseInlineLinks{
			Self: nil,
		}
		passphrase := "test"
		ipAddresses := []string{"1.2.3.4"}
		transport := &mockTransport{response: &securitypriv.ClusterPeerCreateCreated{
			Payload: &privmodels.ClusterPeerSetupResponse{
				NumRecords: nillable.ToPointer(int64(1)),
				ClusterPeerResponseInlineRecords: []*privmodels.ClusterPeerSetupRecord{
					{
						Links: &links,
						Authentication: &privmodels.ClusterPeerSetupResponseInlineAuthentication{
							ExpiryTime: nil,
							Passphrase: &passphrase,
						},
						IPAddress: nil,
						Name:      nil,
					},
				},
			},
		}}
		clust := securitypriv.New(transport, nil)
		client := &clusterClient{apiPriv: &clust}
		response, err := client.ClusterPeerCreate(ClusterPeerCreateParams{
			Name:               "cluster",
			IPAddresses:        ipAddresses,
			GeneratePassphrase: true,
		})
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
	})
}

func TestClusterPeerAccept(t *testing.T) {
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := securitypriv.New(transport, nil)
		client := &clusterClient{apiPriv: &clust}
		response, err := client.ClusterPeerAccept(ClusterPeerCreateParams{})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, response)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		links := privmodels.ClusterPeerSetupResponseInlineLinks{
			Self: nil,
		}
		passphrase := "test"
		ipAddresses := []string{"1.2.3.4"}
		transport := &mockTransport{response: &securitypriv.ClusterPeerCreateCreated{
			Payload: &privmodels.ClusterPeerSetupResponse{
				NumRecords: nillable.ToPointer(int64(1)),
				ClusterPeerResponseInlineRecords: []*privmodels.ClusterPeerSetupRecord{
					{
						Links: &links,
						Authentication: &privmodels.ClusterPeerSetupResponseInlineAuthentication{
							ExpiryTime: nil,
							Passphrase: &passphrase,
						},
						IPAddress: nil,
						Name:      nil,
					},
				},
			},
		}}
		clust := securitypriv.New(transport, nil)
		client := &clusterClient{apiPriv: &clust}
		response, err := client.ClusterPeerAccept(ClusterPeerCreateParams{
			Name:               "cluster",
			IPAddresses:        ipAddresses,
			GeneratePassphrase: false,
		})
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
	})
}

func TestClusterPeerGet(t *testing.T) {
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		response, err := client.ClusterPeerGet("test")
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, response)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		name := "name"
		transport := &mockTransport{response: &cluster.ClusterPeerGetOK{
			Payload: &models.ClusterPeer{
				Authentication: &models.ClusterPeerInlineAuthentication{State: &name},
				Name:           nil,
				Remote:         &models.ClusterPeerInlineRemote{Name: &name},
				Status:         &models.ClusterPeerInlineStatus{State: &name},
				UUID:           &name,
			},
		}}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		response, err := client.ClusterPeerGet("test")
		assert.NoError(tt, err)
		assert.NotEmpty(tt, response)
	})
}

func TestScheduleCreate(t *testing.T) {
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		mcs := cluster.NewMockClientService(tt)
		client := &clusterClient{api: mcs}
		expectedError := errors.New("something went wrong")
		params := &ScheduleCreateParams{
			Name:        "policy-1",
			Months:      []int{3},
			DaysOfMonth: []int{5, 6},
			DaysOfWeek:  []int{2},
			Hours:       []int{3, 6},
			Minutes:     []int{2},
		}
		expectedSchedule := &models.Schedule{
			Name: &params.Name,
			Cron: &models.ScheduleInlineCron{
				Months:   []*int64{nillable.GetInt64Ptr(3)},
				Days:     []*int64{nillable.GetInt64Ptr(5), nillable.GetInt64Ptr(6)},
				Weekdays: []*int64{nillable.GetInt64Ptr(2)},
				Hours:    []*int64{nillable.GetInt64Ptr(3), nillable.GetInt64Ptr(6)},
				Minutes:  []*int64{nillable.GetInt64Ptr(2)},
			},
		}

		go func() {
			defer mcs.MockClientServiceDone()

			err := client.ScheduleCreate(params)
			assert.Equal(tt, expectedError, err)
		}()

		mcs.AssertScheduleCreate(cluster.NewScheduleCreateParams().WithInfo(expectedSchedule), nil, nil, nil, expectedError)
		mcs.AssertMockClientServiceDone()
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		mcs := cluster.NewMockClientService(tt)
		client := &clusterClient{api: mcs}

		params := &ScheduleCreateParams{
			Name:        "policy-1",
			Months:      []int{3},
			DaysOfMonth: []int{5, 6},
			DaysOfWeek:  []int{2},
			Hours:       []int{3, 6},
			Minutes:     []int{2},
		}
		expectedSchedule := &models.Schedule{
			Name: &params.Name,
			Cron: &models.ScheduleInlineCron{
				Months:   []*int64{nillable.GetInt64Ptr(3)},
				Days:     []*int64{nillable.GetInt64Ptr(5), nillable.GetInt64Ptr(6)},
				Weekdays: []*int64{nillable.GetInt64Ptr(2)},
				Hours:    []*int64{nillable.GetInt64Ptr(3), nillable.GetInt64Ptr(6)},
				Minutes:  []*int64{nillable.GetInt64Ptr(2)},
			},
		}

		go func() {
			defer mcs.MockClientServiceDone()

			err := client.ScheduleCreate(params)
			assert.Nil(tt, err)
		}()

		mcs.AssertScheduleCreate(cluster.NewScheduleCreateParams().WithInfo(expectedSchedule), nil, nil, &cluster.ScheduleCreateCreated{}, nil)
		mcs.AssertMockClientServiceDone()
	})
}

func TestScheduleCollectionGet(t *testing.T) {
	t.Run("WhenFilterNil", func(tt *testing.T) {
		funcCalled := false
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		err := client.ScheduleCollectionGet(nil, func(schedules []*Schedule) error {
			funcCalled = true
			return nil
		})
		assert.EqualError(tt, err, "no name filter provided for ScheduleCollectionGet")
		assert.False(tt, funcCalled)
	})
	t.Run("WhenNoNameFilter", func(tt *testing.T) {
		funcCalled := false
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		err := client.ScheduleCollectionGet(&ScheduleCollectionGetParams{}, func(schedules []*Schedule) error {
			funcCalled = true
			return nil
		})
		assert.EqualError(tt, err, "no name filter provided for ScheduleCollectionGet")
		assert.False(tt, funcCalled)
	})
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		funcCalled := false
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		err := client.ScheduleCollectionGet(&ScheduleCollectionGetParams{Name: "a name"}, func(schedules []*Schedule) error {
			funcCalled = true
			return nil
		})
		assert.EqualError(tt, err, transport.err.Error())
		assert.False(tt, funcCalled)
	})
	t.Run("WhenUserCallBackFuncFails", func(tt *testing.T) {
		funcCalled := false
		transport := &mockTransport{response: &cluster.ScheduleCollectionGetOK{
			Payload: &models.ScheduleResponse{NumRecords: nillable.ToPointer(int64(1)), ScheduleResponseInlineRecords: []*models.Schedule{{}}},
		}}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		err := client.ScheduleCollectionGet(&ScheduleCollectionGetParams{Name: "a name"}, func(schedules []*Schedule) error {
			funcCalled = true
			return errors.New("func failed")
		})
		assert.EqualError(tt, err, "func failed")
		assert.True(tt, funcCalled)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		funcCalled := false
		transport := &mockTransport{response: &cluster.ScheduleCollectionGetOK{
			Payload: &models.ScheduleResponse{
				NumRecords: nillable.ToPointer(int64(3)),
				ScheduleResponseInlineRecords: []*models.Schedule{
					{},
					{},
					{},
				}},
		}}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		err := client.ScheduleCollectionGet(&ScheduleCollectionGetParams{Name: "a name"}, func(schedules []*Schedule) error {
			funcCalled = true
			expected := []*Schedule{{}, {}, {}}
			assert.Equal(tt, expected, schedules)
			return nil
		})
		assert.NoError(tt, err)
		assert.True(tt, funcCalled)
	})
}

func TestNodesGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		err := client.NodesGet(&NodesGetParams{}, func(nodes []*Node) error { return nil })
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenSuccessful_ThenReturnNodes", func(tt *testing.T) {
		nodeName := "node1"
		transport := &mockTransport{response: &cluster.NodesGetOK{
			Payload: &models.NodeResponse{
				NodeResponseInlineRecords: []*models.NodeResponseInlineRecordsInlineArrayItem{
					{Name: &nodeName},
				},
				NumRecords: nillable.ToPointer(int64(1)),
			},
		}}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		var nodes []*Node
		err := client.NodesGet(&NodesGetParams{}, func(n []*Node) error {
			nodes = n
			return nil
		})
		assert.NoError(tt, err)
		assert.Len(tt, nodes, 1)
		assert.Equal(tt, nodeName, *nodes[0].Name)
	})
}

func TestGetONTAPVersion(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		version, err := client.GetONTAPVersion()
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, version)
	})

	t.Run("WhenSuccessful_ThenReturnVersion", func(tt *testing.T) {
		version := "9.10.1"
		transport := &mockTransport{response: &cluster.ClusterGetOK{
			Payload: &models.Cluster{
				Version: &models.ClusterInlineVersion{Full: &version},
			},
		}}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		result, err := client.GetONTAPVersion()
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, version, *result)
	})
}

func TestClusterGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}

		result, err := client.ClusterGet(context.Background(), &ClusterGetParams{
			BaseParams: BaseParams{Fields: []string{"uuid", "name", "version"}},
		})

		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, result)
	})

	t.Run("WhenPayloadNil_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{response: &cluster.ClusterGetOK{}}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}

		result, err := client.ClusterGet(context.Background(), &ClusterGetParams{
			BaseParams: BaseParams{Fields: []string{"uuid", "name", "version"}},
		})

		assert.EqualError(tt, err, "empty cluster response")
		assert.Nil(tt, result)
	})

	t.Run("WhenSuccessful_ThenReturnCluster", func(tt *testing.T) {
		name := "cluster-a"
		version := "9.16.1"
		transport := &mockTransport{response: &cluster.ClusterGetOK{
			Payload: &models.Cluster{
				Name:    &name,
				Version: &models.ClusterInlineVersion{Full: &version},
			},
		}}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}

		result, err := client.ClusterGet(context.Background(), &ClusterGetParams{
			BaseParams: BaseParams{Fields: []string{"uuid", "name", "version"}},
		})

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Name)
		assert.Equal(tt, name, *result.Name)
		assert.NotNil(tt, result.Version)
		assert.NotNil(tt, result.Version.Full)
		assert.Equal(tt, version, *result.Version.Full)
	})
}

func TestScheduleCollectionGet2(t *testing.T) {
	t.Run("WhenFilterNil", func(tt *testing.T) {
		funcCalled := false
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		err := client.ScheduleCollectionGet(nil, func(schedules []*Schedule) error {
			funcCalled = true
			return nil
		})
		assert.EqualError(tt, err, "no name filter provided for ScheduleCollectionGet")
		assert.False(tt, funcCalled)
	})

	t.Run("WhenNoNameFilter", func(tt *testing.T) {
		funcCalled := false
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		err := client.ScheduleCollectionGet(&ScheduleCollectionGetParams{}, func(schedules []*Schedule) error {
			funcCalled = true
			return nil
		})
		assert.EqualError(tt, err, "no name filter provided for ScheduleCollectionGet")
		assert.False(tt, funcCalled)
	})

	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		funcCalled := false
		transport := &mockTransport{err: errors.New("something went wrong")}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		err := client.ScheduleCollectionGet(&ScheduleCollectionGetParams{Name: "a name"}, func(schedules []*Schedule) error {
			funcCalled = true
			return nil
		})
		assert.EqualError(tt, err, transport.err.Error())
		assert.False(tt, funcCalled)
	})

	t.Run("WhenUserCallBackFuncFails", func(tt *testing.T) {
		funcCalled := false
		transport := &mockTransport{response: &cluster.ScheduleCollectionGetOK{
			Payload: &models.ScheduleResponse{NumRecords: nillable.ToPointer(int64(1)), ScheduleResponseInlineRecords: []*models.Schedule{{}}},
		}}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		err := client.ScheduleCollectionGet(&ScheduleCollectionGetParams{Name: "a name"}, func(schedules []*Schedule) error {
			funcCalled = true
			return errors.New("func failed")
		})
		assert.EqualError(tt, err, "func failed")
		assert.True(tt, funcCalled)
	})

	t.Run("WhenSuccessful", func(tt *testing.T) {
		funcCalled := false
		transport := &mockTransport{response: &cluster.ScheduleCollectionGetOK{
			Payload: &models.ScheduleResponse{
				NumRecords: nillable.ToPointer(int64(3)),
				ScheduleResponseInlineRecords: []*models.Schedule{
					{},
					{},
					{},
				}},
		}}
		clust := cluster.New(transport, nil)
		client := &clusterClient{api: clust}
		err := client.ScheduleCollectionGet(&ScheduleCollectionGetParams{Name: "a name"}, func(schedules []*Schedule) error {
			funcCalled = true
			expected := []*Schedule{{}, {}, {}}
			assert.Equal(tt, expected, schedules)
			return nil
		})
		assert.NoError(tt, err)
		assert.True(tt, funcCalled)
	})
}
func TestClusterClient_PostClusterLicenseAccessToken(t *testing.T) {
	t.Run("WhenPostClusterAccessTokenSucceeds", func(tt *testing.T) {
		mockAPI := cluster.NewMockClientService(tt)
		client := clusterClient{api: mockAPI}
		ctx := context.Background()
		clientSecret := "secret"
		expectedRes := &cluster.PostClusterAccessTokenOK{}

		go func() {
			defer mockAPI.MockClientServiceDone()
			res, err := client.PostClusterLicenseAccessToken(ctx, clientSecret)
			assert.NoError(tt, err)
			assert.Equal(tt, expectedRes, res)
		}()

		mockAPI.AssertPostClusterAccessToken(cluster.NewPostClusterAccessTokenParams().WithInfo(&models.AccessTokenBody{
			GrantType:    smcGrantType,
			ClientID:     clientId,
			ClientSecret: clientSecret,
		}), nil, nil, expectedRes, nil)
		mockAPI.AssertMockClientServiceDone()
	})

	t.Run("WhenPostClusterAccessTokenFails", func(tt *testing.T) {
		mockAPI := cluster.NewMockClientService(tt)
		client := clusterClient{api: mockAPI}
		ctx := context.Background()
		clientSecret := "secret"
		expectedErr := errors.New("ontap error")

		go func() {
			defer mockAPI.MockClientServiceDone()
			res, err := client.PostClusterLicenseAccessToken(ctx, clientSecret)
			assert.Error(tt, err)
			assert.Nil(tt, res)
		}()

		mockAPI.AssertPostClusterAccessToken(cluster.NewPostClusterAccessTokenParams().WithInfo(&models.AccessTokenBody{
			GrantType:    smcGrantType,
			ClientID:     clientId,
			ClientSecret: clientSecret,
		}), nil, nil, nil, expectedErr)
		mockAPI.AssertMockClientServiceDone()
	})
}
