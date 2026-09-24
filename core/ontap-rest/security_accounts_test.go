package ontap_rest

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/security"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestSecurityClient_AccountGet_NotFound(t *testing.T) {
	transport := &mockTransport{err: security.NewAccountCollectionGetDefault(404)}
	securityAPI := security.New(transport, nil)
	client := &securityClient{api: &securityAPI}

	account, err := client.AccountGet(&AccountGetParams{Name: "missing-user"})
	assert.Nil(t, account)
	assert.True(t, utilserrors.IsNotFoundErrForObjectTypeInChain(err, "SecurityAccount"))
}

func TestSecurityClient_AccountGet_Success(t *testing.T) {
	accountName := "svc-user"
	roleName := "gcnvadmin"
	ownerUUID := "owner-uuid"
	application := "http"
	certificateMethod := "certificate"
	transport := &mockTransport{response: &security.AccountCollectionGetOK{
		Payload: &models.AccountResponse{
			AccountResponseInlineRecords: []*models.Account{
				{
					Name:  &accountName,
					Role:  &models.AccountInlineRole{Name: &roleName},
					Owner: &models.AccountInlineOwner{UUID: &ownerUUID},
					AccountInlineApplications: []*models.AccountApplication{
						{
							Application:           &application,
							AuthenticationMethods: []*string{&certificateMethod},
						},
					},
				},
			},
		},
	}}
	securityAPI := security.New(transport, nil)
	client := &securityClient{api: &securityAPI}

	account, err := client.AccountGet(&AccountGetParams{Name: "svc-user"})
	assert.NoError(t, err)
	assert.NotNil(t, account)
	assert.Equal(t, "svc-user", account.Name)
	assert.Equal(t, "gcnvadmin", account.Role.Name)
	assert.Equal(t, "owner-uuid", account.Owner.UUID)
	assert.Equal(t, []SecurityAccountApplication{
		{Application: "http", AuthenticationMethods: []string{"certificate"}},
	}, account.Applications)
}

func TestAccountCollectionGetParamsToONTAP_MapsProofFields(t *testing.T) {
	params := accountCollectionGetParamsToONTAP(&AccountGetParams{
		Name:   "svc-user",
		Fields: []string{"name", "applications", "owner", "role"},
	})

	assert.Equal(t, "svc-user", nillable.FromPointer(params.Name))
	assert.Equal(t, []string{"name", "applications", "owner", "role"}, params.Fields)
}

func TestSecurityClient_AccountCreate_Success(t *testing.T) {
	transport := &mockTransport{response: &security.AccountCreateCreated{}}
	securityAPI := security.New(transport, nil)
	client := &securityClient{api: &securityAPI}

	err := client.AccountCreate(&AccountCreateParams{
		Name:      "svc-user",
		Password:  "secret",
		RoleName:  "gcnvadmin",
		OwnerUUID: "cluster-uuid",
	})
	assert.NoError(t, err)
}

func TestSecurityClient_AccountDelete_Success(t *testing.T) {
	transport := &mockTransport{response: &security.AccountDeleteOK{}}
	securityAPI := security.New(transport, nil)
	client := &securityClient{api: &securityAPI}

	err := client.AccountDelete(&AccountDeleteParams{
		Name:      "svc-user",
		OwnerUUID: "owner-uuid",
	})
	assert.NoError(t, err)
}

func TestSecurityClient_AccountDelete_NotFound(t *testing.T) {
	transport := &mockTransport{err: security.NewAccountDeleteDefault(404)}
	securityAPI := security.New(transport, nil)
	client := &securityClient{api: &securityAPI}

	err := client.AccountDelete(&AccountDeleteParams{
		Name:      "missing-user",
		OwnerUUID: "owner-uuid",
	})
	assert.True(t, utilserrors.IsNotFoundErrForObjectTypeInChain(err, "SecurityAccount"))
}

func TestSecurityClient_AccountCreate_ValidationErrors(t *testing.T) {
	client := &securityClient{}
	assert.EqualError(t, client.AccountCreate(nil), "account create params are required")
	assert.EqualError(t, client.AccountCreate(&AccountCreateParams{}), "account name is required")
	assert.EqualError(t, client.AccountCreate(&AccountCreateParams{Name: "svc-user"}), "account password or authentication methods are required")
	assert.EqualError(t, client.AccountCreate(&AccountCreateParams{Name: "svc-user", Password: "secret"}), "account role name is required")
	assert.EqualError(t, client.AccountCreate(&AccountCreateParams{
		Name: "svc-user", Password: "secret", RoleName: "role",
	}), "account owner uuid is required")
	// Certificate-auth accounts carry no password; authentication methods satisfy the check.
	assert.EqualError(t, client.AccountCreate(&AccountCreateParams{
		Name: "svc-user", AuthenticationMethods: []string{"certificate"},
	}), "account role name is required")
}

func TestSecurityClient_AccountGet_ValidationErrors(t *testing.T) {
	client := &securityClient{}
	account, err := client.AccountGet(nil)
	assert.Nil(t, account)
	assert.EqualError(t, err, "account name is required")

	account, err = client.AccountGet(&AccountGetParams{})
	assert.Nil(t, account)
	assert.EqualError(t, err, "account name is required")
}

func TestSecurityClient_AccountDelete_ValidationErrors(t *testing.T) {
	client := &securityClient{}
	assert.EqualError(t, client.AccountDelete(nil), "account delete params are required")
	assert.EqualError(t, client.AccountDelete(&AccountDeleteParams{}), "account name is required")
	assert.EqualError(t, client.AccountDelete(&AccountDeleteParams{Name: "svc-user"}), "account owner uuid is required")
}

func TestSecurityClient_AccountCreate_ServerError(t *testing.T) {
	transport := &mockTransport{err: errors.New("boom")}
	securityAPI := security.New(transport, nil)
	client := &securityClient{api: &securityAPI}

	err := client.AccountCreate(&AccountCreateParams{
		Name: "svc-user", Password: "secret", RoleName: "gcnvadmin", OwnerUUID: "cluster-uuid",
	})
	assert.EqualError(t, err, "boom")
}
