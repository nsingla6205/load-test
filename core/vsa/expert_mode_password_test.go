package vsa

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

func TestResetLoginPassword(t *testing.T) {
	const (
		vserver     = "cluster1"
		username    = "ociadmin"
		newPassword = "n3wP@ssw0rd!"
	)

	t.Run("WhenClientBuildFails_ThenReturnRestAPIError", func(tt *testing.T) {
		orig := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("cannot build client")
		}
		tt.Cleanup(func() { getOntapClientFunc = orig })

		err := (&OntapRestProvider{}).ResetLoginPassword(context.Background(), vserver, username, newPassword)
		require.Error(tt, err)
		var vcpErr *vsaerrors.CustomError
		require.ErrorAs(tt, err, &vcpErr)
		assert.Equal(tt, vsaerrors.ErrOntapRestAPIError, vcpErr.TrackingID)
	})

	t.Run("WhenSecurityCallFails_ThenReturnExpertModePasswordError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		mockClient.On("Security").Return(mockSecurity)
		ontapErr := errors.New("new password reuses an old password")
		mockSecurity.On("ResetLoginPassword", mock.Anything, vserver, username, newPassword).
			Return(ontapErr).Once()

		orig := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		tt.Cleanup(func() { getOntapClientFunc = orig })

		err := (&OntapRestProvider{}).ResetLoginPassword(context.Background(), vserver, username, newPassword)
		require.Error(tt, err)
		var vcpErr *vsaerrors.CustomError
		require.ErrorAs(tt, err, &vcpErr)
		assert.Equal(tt, vsaerrors.ErrOntapExpertModePasswordError, vcpErr.TrackingID)
		assert.ErrorIs(tt, err, ontapErr)
		mockSecurity.AssertExpectations(tt)
	})

	t.Run("WhenSuccessful_ThenReturnNoError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("ResetLoginPassword", mock.Anything, vserver, username, newPassword).
			Return(nil).Once()

		orig := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		tt.Cleanup(func() { getOntapClientFunc = orig })

		err := (&OntapRestProvider{}).ResetLoginPassword(context.Background(), vserver, username, newPassword)
		require.NoError(tt, err)
		mockSecurity.AssertExpectations(tt)
	})
}

func TestExpireLoginPassword(t *testing.T) {
	const (
		vserver  = "cluster1"
		username = "ociadmin"
	)

	t.Run("WhenClientBuildFails_ThenReturnRestAPIError", func(tt *testing.T) {
		orig := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("cannot build client")
		}
		tt.Cleanup(func() { getOntapClientFunc = orig })

		err := (&OntapRestProvider{}).ExpireLoginPassword(context.Background(), vserver, username)
		require.Error(tt, err)
		var vcpErr *vsaerrors.CustomError
		require.ErrorAs(tt, err, &vcpErr)
		assert.Equal(tt, vsaerrors.ErrOntapRestAPIError, vcpErr.TrackingID)
	})

	t.Run("WhenSecurityCallFails_ThenReturnExpertModePasswordError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		mockClient.On("Security").Return(mockSecurity)
		ontapErr := errors.New("expire-password failed")
		mockSecurity.On("ExpireLoginPassword", mock.Anything, vserver, username).
			Return(ontapErr).Once()

		orig := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		tt.Cleanup(func() { getOntapClientFunc = orig })

		err := (&OntapRestProvider{}).ExpireLoginPassword(context.Background(), vserver, username)
		require.Error(tt, err)
		var vcpErr *vsaerrors.CustomError
		require.ErrorAs(tt, err, &vcpErr)
		assert.Equal(tt, vsaerrors.ErrOntapExpertModePasswordError, vcpErr.TrackingID)
		assert.ErrorIs(tt, err, ontapErr)
		mockSecurity.AssertExpectations(tt)
	})

	t.Run("WhenSuccessful_ThenReturnNoError", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("ExpireLoginPassword", mock.Anything, vserver, username).
			Return(nil).Once()

		orig := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		tt.Cleanup(func() { getOntapClientFunc = orig })

		err := (&OntapRestProvider{}).ExpireLoginPassword(context.Background(), vserver, username)
		require.NoError(tt, err)
		mockSecurity.AssertExpectations(tt)
	})
}
