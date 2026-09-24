package vsa

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestGetAllInitiators(t *testing.T) {
	iqn1 := "iqn.1998-01.com.vmware:host1"
	iqn2 := "iqn.1998-01.com.vmware:host2"

	assert.Empty(t, GetAllInitiators(nil))
	assert.Empty(t, GetAllInitiators([]*models.IgroupInlineInitiatorsInlineArrayItem{}))
	assert.Equal(t, []string{iqn1, iqn2}, GetAllInitiators([]*models.IgroupInlineInitiatorsInlineArrayItem{
		{Name: &iqn1},
		nil,
		{Name: nil},
		{Name: &iqn2},
	}))
}

func TestIgroupMatchesCreateRequest(t *testing.T) {
	iqn1 := "iqn.1998-01.com.vmware:host1"
	iqn2 := "iqn.1998-01.com.vmware:host2"
	igroup := &ontaprest.Igroup{
		Igroup: models.Igroup{
			OsType: nillable.ToPointer("linux"),
			IgroupInlineInitiators: []*models.IgroupInlineInitiatorsInlineArrayItem{
				{Name: nillable.ToPointer(iqn1)},
				{Name: nillable.ToPointer(iqn2)},
			},
		},
	}

	assert.True(t, IgroupMatchesCreateRequest(igroup, "LINUX", []string{iqn2, iqn1}))
	assert.False(t, IgroupMatchesCreateRequest(igroup, "WINDOWS", []string{iqn1, iqn2}))
	assert.False(t, IgroupMatchesCreateRequest(igroup, "LINUX", []string{iqn1}))
	assert.False(t, IgroupMatchesCreateRequest(nil, "LINUX", []string{iqn1}))

	esxiIgroup := &ontaprest.Igroup{
		Igroup: models.Igroup{
			OsType: nillable.ToPointer("vmware"),
			IgroupInlineInitiators: []*models.IgroupInlineInitiatorsInlineArrayItem{
				{Name: nillable.ToPointer(iqn1)},
			},
		},
	}
	assert.True(t, IgroupMatchesCreateRequest(esxiIgroup, "ESXI", []string{iqn1}))
}

func TestIGroupCreate_Success(t *testing.T) {
	mockSAN := new(ontaprest.MockSANClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("SAN").Return(mockSAN)

	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}
	iGroupName := "testIGroup"
	params := IgroupCreateParams{
		IgroupName: iGroupName,
		SvmName:    "testSVM",
		OsType:     "linux",
		Initiator:  []string{"iqn.1993-08.org.debian:01:123456789"},
	}

	mockSAN.On("IGroupCreate", mock.Anything).Return(iGroupName, nil)

	resp, err := rc.IgroupCreate(params)

	assert.NoError(t, err)
	assert.Equal(t, iGroupName, resp)

	mockSAN.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestIGroupCreate_Error(t *testing.T) {
	mockSAN := new(ontaprest.MockSANClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("SAN").Return(mockSAN)

	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	iGroupName := "testIGroup"
	params := IgroupCreateParams{
		IgroupName: iGroupName,
		SvmName:    "testSVM",
		OsType:     "linux",
		Initiator:  []string{"iqn.1993-08.org.debian:01:123456789"},
	}

	mockSAN.On("IGroupCreate", mock.Anything).Return("", errors.New("creation error"))

	resp, err := rc.IgroupCreate(params)

	assert.Error(t, err)
	assert.Empty(t, resp)

	mockSAN.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestIgroupList(t *testing.T) {
	t.Run("WhenIgroupsExist_ThenReturnIgroupDetails", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		svmName := "testSVM"
		name1 := "hg-1"
		name2 := "hg-2"
		mockSAN.On("IGroupsGet", mock.Anything).Return([]*ontaprest.Igroup{
			{Igroup: models.Igroup{Name: nillable.ToPointer(name1)}},
			{Igroup: models.Igroup{Name: nillable.ToPointer(name2)}},
		}, nil)

		igroups, err := rc.IgroupList(&svmName)

		assert.NoError(tt, err)
		require.Len(tt, igroups, 2)
		assert.Equal(tt, name1, *igroups[0].Name)
		assert.Equal(tt, name2, *igroups[1].Name)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenOntapReturnsError_ThenWrapAsVCPError", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		svmName := "testSVM"
		mockSAN.On("IGroupsGet", mock.Anything).Return(nil, errors.New("list failed"))

		igroups, err := rc.IgroupList(&svmName)

		assert.Error(tt, err)
		assert.Nil(tt, igroups)
		var customErr *vsaerrors.CustomError
		assert.ErrorAs(tt, err, &customErr)
		assert.True(tt, customErr.IsError(vsaerrors.ErrOntapRestAPIError))

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenGetOntapClientFuncFails", func(tt *testing.T) {
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("getOntapClientFunc error")
		}
		rc := &OntapRestProvider{}

		svmName := "testSVM"
		igroups, err := rc.IgroupList(&svmName)

		assert.Error(tt, err)
		assert.Nil(tt, igroups)
		assert.Equal(tt, "getOntapClientFunc error", err.Error())
	})
}

func TestIgroupGet(t *testing.T) {
	t.Run("WhenIgroupExists_ThenReturnIgroupDetails", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockIgroup := &ontaprest.Igroup{
			Igroup: models.Igroup{
				Name: nillable.ToPointer("testIgroup"),
				UUID: nillable.ToPointer("testUUID"),
			},
		}

		mockSAN.On("IGroupGet", mock.Anything).Return(mockIgroup, nil)

		igroup, err := rc.IgroupGet(nillable.GetStringPtr("testIgroup"), nillable.GetStringPtr("testSVM"))

		assert.NoError(tt, err)
		assert.NotNil(tt, igroup)
		assert.Equal(tt, "testIgroup", *igroup.Name)
		assert.Equal(tt, "testUUID", *igroup.UUID)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenIgroupDoesNotExist_ThenReturnNil", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSAN.On("IGroupGet", mock.Anything).Return(nil, nil)

		igroup, err := rc.IgroupGet(nillable.GetStringPtr("testIgroup"), nillable.GetStringPtr("testSVM"))

		assert.NoError(tt, err)
		assert.Nil(tt, igroup)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenFetchingIgroupFails_ThenReturnError", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSAN.On("IGroupGet", mock.Anything).Return(nil, errors.New("fetch error"))

		igroup, err := rc.IgroupGet(nillable.GetStringPtr("testIgroup"), nillable.GetStringPtr("testSVM"))

		assert.Error(tt, err)
		assert.Nil(tt, igroup)
		assert.Equal(tt, "fetch error", err.Error())

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
}

func TestIgroupExists(t *testing.T) {
	t.Run("WhenIgroupExists_ThenReturnTrue", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockIgroup := &ontaprest.Igroup{
			Igroup: models.Igroup{
				Name: nillable.ToPointer("testIgroup"),
			},
		}

		mockSAN.On("IGroupGet", mock.Anything).Return(mockIgroup, nil)

		exists, igroup, err := rc.IgroupExists("testIgroup", nillable.GetStringPtr("testSVM"))

		assert.NoError(tt, err)
		assert.True(tt, exists)
		assert.Equal(tt, igroup.Name, nillable.GetStringPtr("testIgroup"))

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenIgroupDoesNotExist_ThenReturnFalse", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSAN.On("IGroupGet", mock.Anything).Return(nil, nil)

		exists, igroup, err := rc.IgroupExists("testIgroup", nillable.GetStringPtr("testSVM"))

		assert.NoError(tt, err)
		assert.False(tt, exists)
		assert.Nil(tt, igroup)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenFetchingIgroupFailsWithNotFoundError_ThenReturnFalse", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSAN.On("IGroupGet", mock.Anything).Return(nil, errors.NewNotFoundErr("Igroup", nil))

		exists, igroup, err := rc.IgroupExists("testIgroup", nillable.GetStringPtr("testSVM"))

		assert.NoError(tt, err)
		assert.False(tt, exists)
		assert.Nil(tt, igroup)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenFetchingIgroupFailsWithOtherError_ThenReturnError", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSAN.On("IGroupGet", mock.Anything).Return(nil, errors.New("Igroup not found"))

		exists, igroup, err := rc.IgroupExists("testIgroup", nillable.GetStringPtr("testSVM"))

		assert.Error(tt, err)
		assert.False(tt, exists)
		assert.Equal(tt, "An internal error occurred.", err.Error())
		assert.Nil(tt, igroup)
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.EqualError(tt, customErr.OriginalErr, "Igroup not found")
		}
		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
}

func TestIgroupAddInitiator(t *testing.T) {
	t.Run("WhenIgroupAddInitiatorSuccess", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := IgroupAddInitiator{
			Initiator: []string{"iqn.1993-08.org.debian:01:123456789"},
		}
		mockSAN.On("IGroupAddInitiator", mock.Anything).Return(nil)

		err := rc.IgroupAddInitiator(params)

		assert.NoError(tt, err)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
	t.Run("WhenIgroupAddInitiatorFails", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := IgroupAddInitiator{
			Initiator: []string{"iqn.1993-08.org.debian:01:123456789"},
		}
		mockSAN.On("IGroupAddInitiator", mock.Anything).Return(errors.New("dummy error"))

		err := rc.IgroupAddInitiator(params)

		assert.EqualError(tt, err, "dummy error")

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
	t.Run("WhenIgroupAddInitiatorFailsWithAlready", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := IgroupAddInitiator{
			Initiator: []string{"iqn.1993-08.org.debian:01:123456789"},
		}
		mockSAN.On("IGroupAddInitiator", mock.Anything).Return(errors.New("already contains initiator"))

		err := rc.IgroupAddInitiator(params)

		assert.NoError(tt, err)
		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
}

func TestIgroupDeleteInitiator(t *testing.T) {
	t.Run("WhenIgroupDeleteInitiatorSuccess", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := IgroupDeleteInitiator{
			InitiatorName: "sfd",
			IgroupUUID:    "abcd",
		}
		mockSAN.On("IGroupDeleteInitiator", mock.Anything).Return(nil)

		err := rc.IgroupDeleteInitiator(params)

		assert.NoError(tt, err)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
	t.Run("WhenIgroupDeleteInitiatorFails", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := IgroupDeleteInitiator{
			InitiatorName: "sfd",
			IgroupUUID:    "abcd",
		}
		mockSAN.On("IGroupDeleteInitiator", mock.Anything).Return(errors.New("dummy error"))

		err := rc.IgroupDeleteInitiator(params)

		assert.EqualError(tt, err, "dummy error")

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
	t.Run("WhenIgroupDeleteInitiatorFailsWithAlready", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		params := IgroupDeleteInitiator{
			InitiatorName: "sfd",
			IgroupUUID:    "abcd",
		}
		mockSAN.On("IGroupDeleteInitiator", mock.Anything).Return(errors.New("does not contain initiator"))

		err := rc.IgroupDeleteInitiator(params)

		assert.NoError(tt, err)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
}

func TestIgroupDelete(t *testing.T) {
	t.Run("WhenIgroupDeleteSucceeds", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		uuid := "test-igroup-uuid"
		mockSAN.On("IGroupDelete", mock.Anything).Return(nil)

		err := rc.IgroupDelete(uuid)

		assert.NoError(tt, err)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenIgroupDeleteFails", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		uuid := "test-igroup-uuid"
		expectedError := errors.New("delete failed")
		mockSAN.On("IGroupDelete", mock.Anything).Return(expectedError)

		err := rc.IgroupDelete(uuid)

		assert.Error(tt, err)
		var customErr *vsaerrors.CustomError
		if assert.True(tt, vsaerrors.As(err, &customErr)) {
			assert.Equal(tt, vsaerrors.ErrOntapRestAPIError, customErr.TrackingID)
			assert.EqualError(tt, customErr.OriginalErr, expectedError.Error())
		}

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenIgroupDeleteFailsWithLUNMaps", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		uuid := "test-igroup-uuid"
		expectedError := errors.New("LUN maps for this initiator group exist")
		mockSAN.On("IGroupDelete", mock.Anything).Return(expectedError)

		err := rc.IgroupDelete(uuid)

		assert.Error(tt, err)
		var customErr *vsaerrors.CustomError
		if assert.True(tt, vsaerrors.As(err, &customErr)) {
			assert.Equal(tt, vsaerrors.ErrHostGroupHasLUNMaps, customErr.TrackingID)
			assert.EqualError(tt, customErr.OriginalErr, expectedError.Error())
		}

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenGetOntapClientFuncFails", func(tt *testing.T) {
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("getOntapClientFunc error")
		}
		rc := &OntapRestProvider{}

		uuid := "test-igroup-uuid"

		err := rc.IgroupDelete(uuid)

		assert.Error(tt, err)
		assert.Equal(tt, "getOntapClientFunc error", err.Error())
	})
}

func TestSyncIgroupInitiators(t *testing.T) {
	svmName := "gcnv-test-svm-01"
	igroupName := "hg-1"
	igroupUUID := "igroup-uuid-1"
	iqn1 := "iqn.1998-01.com.vmware:db1"
	iqn2 := "iqn.1998-01.com.vmware:db2"
	desiredHosts := []string{iqn1, iqn2}

	t.Run("WhenIgroupExistsFails", func(t *testing.T) {
		mockProvider := NewMockProvider(t)
		mockProvider.EXPECT().
			IgroupExists(igroupName, &svmName).
			Return(false, nil, errors.New("lookup failed"))

		found, _, err := SyncIgroupInitiators(mockProvider, igroupName, desiredHosts, &svmName)

		assert.Error(t, err)
		assert.False(t, found)
	})

	t.Run("WhenIgroupNotFound", func(t *testing.T) {
		mockProvider := NewMockProvider(t)
		mockProvider.EXPECT().
			IgroupExists(igroupName, &svmName).
			Return(false, nil, nil)

		found, _, err := SyncIgroupInitiators(mockProvider, igroupName, desiredHosts, &svmName)

		assert.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("WhenIgroupUUIDMissing", func(t *testing.T) {
		mockProvider := NewMockProvider(t)
		mockProvider.EXPECT().
			IgroupExists(igroupName, &svmName).
			Return(true, &ontaprest.Igroup{Igroup: models.Igroup{}}, nil)

		found, _, err := SyncIgroupInitiators(mockProvider, igroupName, desiredHosts, &svmName)

		assert.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("WhenAddAndDeleteInitiatorsSucceed", func(t *testing.T) {
		mockProvider := NewMockProvider(t)
		mockProvider.EXPECT().
			IgroupExists(igroupName, &svmName).
			Return(true, &ontaprest.Igroup{
				Igroup: models.Igroup{
					UUID: &igroupUUID,
					IgroupInlineInitiators: []*models.IgroupInlineInitiatorsInlineArrayItem{
						{Name: nillable.ToPointer("iqn.1998-01.com.vmware:old")},
					},
				},
			}, nil)
		mockProvider.EXPECT().
			IgroupDeleteInitiator(IgroupDeleteInitiator{
				InitiatorName: "iqn.1998-01.com.vmware:old",
				IgroupUUID:    igroupUUID,
			}).
			Return(nil)
		mockProvider.EXPECT().
			IgroupAddInitiator(IgroupAddInitiator{
				Initiator:  []string{iqn1, iqn2},
				IgroupUUID: igroupUUID,
			}).
			Return(nil)

		found, _, err := SyncIgroupInitiators(mockProvider, igroupName, desiredHosts, &svmName)

		assert.NoError(t, err)
		assert.True(t, found)
	})

	t.Run("WhenAddInitiatorFails", func(t *testing.T) {
		mockProvider := NewMockProvider(t)
		mockProvider.EXPECT().
			IgroupExists(igroupName, &svmName).
			Return(true, &ontaprest.Igroup{
				Igroup: models.Igroup{
					UUID: &igroupUUID,
					IgroupInlineInitiators: []*models.IgroupInlineInitiatorsInlineArrayItem{
						{Name: &iqn1},
					},
				},
			}, nil)
		mockProvider.EXPECT().
			IgroupAddInitiator(mock.Anything).
			Return(errors.New("add failed"))

		found, _, err := SyncIgroupInitiators(mockProvider, igroupName, desiredHosts, &svmName)

		assert.Error(t, err)
		assert.True(t, found)
	})

	t.Run("WhenDeleteInitiatorFails", func(t *testing.T) {
		mockProvider := NewMockProvider(t)
		mockProvider.EXPECT().
			IgroupExists(igroupName, &svmName).
			Return(true, &ontaprest.Igroup{
				Igroup: models.Igroup{
					UUID: &igroupUUID,
					IgroupInlineInitiators: []*models.IgroupInlineInitiatorsInlineArrayItem{
						{Name: &iqn1},
						{Name: nillable.ToPointer("iqn.1998-01.com.vmware:old")},
					},
				},
			}, nil)
		mockProvider.EXPECT().
			IgroupDeleteInitiator(IgroupDeleteInitiator{
				InitiatorName: "iqn.1998-01.com.vmware:old",
				IgroupUUID:    igroupUUID,
			}).
			Return(errors.New("delete failed"))

		found, _, err := SyncIgroupInitiators(mockProvider, igroupName, desiredHosts, &svmName)

		assert.Error(t, err)
		assert.True(t, found)
	})

	t.Run("WhenReplacingAtHostLimitDeletesBeforeAdd", func(t *testing.T) {
		const maxHosts = 128
		oldIQN := "iqn.1998-01.com.vmware:old"
		newIQN := "iqn.1998-01.com.vmware:new"

		currentHosts := make([]*models.IgroupInlineInitiatorsInlineArrayItem, 0, maxHosts)
		for i := range maxHosts - 1 {
			name := fmt.Sprintf("iqn.1998-01.com.vmware:host-%d", i)
			currentHosts = append(currentHosts, &models.IgroupInlineInitiatorsInlineArrayItem{
				Name: nillable.ToPointer(name),
			})
		}
		currentHosts = append(currentHosts, &models.IgroupInlineInitiatorsInlineArrayItem{
			Name: nillable.ToPointer(oldIQN),
		})

		desiredHostsAtLimit := make([]string, 0, maxHosts)
		for i := range maxHosts - 1 {
			desiredHostsAtLimit = append(desiredHostsAtLimit, fmt.Sprintf("iqn.1998-01.com.vmware:host-%d", i))
		}
		desiredHostsAtLimit = append(desiredHostsAtLimit, newIQN)

		mockProvider := NewMockProvider(t)
		mockProvider.EXPECT().
			IgroupExists(igroupName, &svmName).
			Return(true, &ontaprest.Igroup{
				Igroup: models.Igroup{
					UUID:                   &igroupUUID,
					IgroupInlineInitiators: currentHosts,
				},
			}, nil)
		mockProvider.EXPECT().
			IgroupDeleteInitiator(IgroupDeleteInitiator{
				InitiatorName: oldIQN,
				IgroupUUID:    igroupUUID,
			}).
			Return(nil)
		mockProvider.EXPECT().
			IgroupAddInitiator(IgroupAddInitiator{
				Initiator:  []string{newIQN},
				IgroupUUID: igroupUUID,
			}).
			Return(nil)

		found, _, err := SyncIgroupInitiators(mockProvider, igroupName, desiredHostsAtLimit, &svmName)

		assert.NoError(t, err)
		assert.True(t, found)
	})
}
