package ontap_rest

import (
	"context"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/security"
	securitypriv "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/client/operations"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// SecurityClient describes a security client
type SecurityClient interface { // generate:mock
	GcpKmsCreate(params *GcpKmsCreateParams) ([]*GcpKms, error)
	GcpKmsGet(params *GcpKmsGetParams) (*GcpKms, error)
	GcpKmsDelete(params *GcpKmsDeleteParams) error
	SecurityLogForwardingCreate(params *SecurityLogForwardingCreateParams) ([]*SecurityAuditLogForward, error)
	SecurityLogForwardingGet(params *SecurityLogForwardingGetParams) (*SecurityAuditLogForward, error)
	SecurityAuditUpdate(params *SecurityAuditUpdateParams) (*SecurityAudit, error)
	SecurityAuditGet() (*SecurityAudit, error)
	GcpKmsModify(params *GcpKmsModifyParams) (*GcpKms, *JobAccepted, error)
	GcpKmsRekeyExternal(params *GcpKmsRekeyExternalParams) (*JobAccepted, error)
	EnableAutoVolOfflineCronForGCPKMS() error
	RoleCreate(params *RoleCreateParams) (string, error)
	RoleGet(params *RoleGetParams) (*Role, error)
	RoleDelete(params *RoleDeleteParams) error
	RolePrivilegeModify(params *RolePrivilegeModifyParams) error
	RolePrivilegeDelete(params *RolePrivilegeDeleteParams) error
	RolePrivilegeCreate(params *RolePrivilegeCreateParams) (string, error)
	RoleCollectionGet(params *RoleCollectionGetParams) (*RoleCollectionGetResponse, error)
	AccountCreate(params *AccountCreateParams) error
	AccountGet(params *AccountGetParams) (*SecurityAccount, error)
	AccountDelete(params *AccountDeleteParams) error
	ServerRootCACertificateGet(params *ServerRootCAGetParams) (*ServerRootCACertificate, error)
	ServerRootCACertificateInstall(params *ServerRootCAInstallParams) (*ServerRootCACertificate, error)
	ServerRootCACertificateDelete(params *ServerRootCADeleteParams) error
	ServerRootCACertificateCollectionGet(params *ServerRootCAGetCollectionParams) ([]*ServerRootCACertificate, error)
	SecurityCertificateDeleteCollection(params *SecurityCertificateDeleteCollectionParams) error
	// ResetLoginPassword sets a new login password for username on the given
	// vserver (cluster name for cluster-scoped users) via the ONTAP private CLI.
	ResetLoginPassword(ctx context.Context, vserver, username, newPassword string) error
	// ExpireLoginPassword expires username's password on the given vserver so it
	// must be changed on next login, via the ONTAP private CLI.
	ExpireLoginPassword(ctx context.Context, vserver, username string) error
	// HasLoginPasswordResetSuccess reports whether at least one successful
	// `security login password` audit record exists for username since the given time.
	HasLoginPasswordResetSuccess(ctx context.Context, username string, since time.Time) (bool, error)
}

type securityClient struct {
	api     *security.ClientService
	apiPriv *securitypriv.ClientService
}

// GcpKmsCreate invokes pkg/ontap-rest/client/security/Client.GcpKmsCreate
func (sc *securityClient) GcpKmsCreate(params *GcpKmsCreateParams) ([]*GcpKms, error) {
	response, _, err := (*sc.api).GcpKmsCreate(gcpKmsCreateParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}

	if response == nil || response.Payload == nil {
		return nil, errors.New("unexpected response from GcpKmsCreate")
	}

	resp := make([]*GcpKms, nillable.FromPointer(response.Payload.NumRecords))
	for i, gcp := range response.Payload.GcpKmsResponseInlineRecords {
		resp[i] = &GcpKms{GcpKms: *gcp}
	}
	return resp, err
}

// GcpKmsGet invokes pkg/ontap-rest/client/security/Client.GcpKmsGet
func (sc *securityClient) GcpKmsGet(params *GcpKmsGetParams) (*GcpKms, error) {
	response, err := (*sc.api).GcpKmsGet(gcpKmsGetParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}
	resp := &GcpKms{GcpKms: *response.Payload}
	return resp, err
}

func (sc *securityClient) SecurityLogForwardingCreate(params *SecurityLogForwardingCreateParams) ([]*SecurityAuditLogForward, error) {
	response, err := (*sc.api).SecurityLogForwardingCreate(securityLogForwardingCreateParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}

	if response == nil || response.Payload == nil {
		return nil, errors.New("unexpected response from SecurityLogForwardingCreate")
	}

	resp := make([]*SecurityAuditLogForward, nillable.FromPointer(response.Payload.NumRecords))
	for i, hyperscaler := range response.Payload.SecurityAuditLogForwardResponseInlineRecords {
		resp[i] = &SecurityAuditLogForward{SecurityAuditLogForward: *hyperscaler}
	}

	return resp, err
}

func (sc *securityClient) SecurityLogForwardingGet(params *SecurityLogForwardingGetParams) (*SecurityAuditLogForward, error) {
	response, err := (*sc.api).SecurityLogForwardingGet(securityLogForwardingGetParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}

	resp := &SecurityAuditLogForward{SecurityAuditLogForward: *response.Payload}

	return resp, nil
}

func (sc *securityClient) SecurityAuditGet() (*SecurityAudit, error) {
	params := security.SecurityAuditGetParams{}
	response, err := (*sc.api).SecurityAuditGet(&params, nil)
	if err != nil {
		return nil, err
	}

	resp := &SecurityAudit{SecurityAudit: *response.Payload}

	return resp, nil
}

func (sc *securityClient) SecurityAuditUpdate(params *SecurityAuditUpdateParams) (*SecurityAudit, error) {
	response, err := (*sc.api).SecurityAuditModify(securityAuditModifyParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}

	if response == nil || response.Payload == nil {
		return nil, errors.New("unexpected response from SecurityAuditUpdate")
	}

	resp := &SecurityAudit{SecurityAudit: *response.Payload}

	return resp, err
}

// GcpKmsModify invokes pkg/ontap-rest/client/security/Client.GcpKmsModify
func (sc *securityClient) GcpKmsModify(params *GcpKmsModifyParams) (*GcpKms, *JobAccepted, error) {
	responseOK, _, err := (*sc.api).GcpKmsModify(gcpKmsModifyParamsToONTAP(params), nil)
	if err != nil {
		return nil, nil, err
	}
	if responseOK != nil {
		// TODO garpur 2023-03-01 Missing return object in the swagger
		return &GcpKms{}, nil, nil
	}
	return nil, nil, nil
}

// GcpKmsRekeyExternal invokes pkg/ontap-rest/client/security/Client.GcpKmsRekeyExternal
func (sc *securityClient) GcpKmsRekeyExternal(params *GcpKmsRekeyExternalParams) (*JobAccepted, error) {
	responseCreated, responseAccepted, err := (*sc.api).GcpKmsRekeyExternal(gcpKmsRekeyExternalParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}
	if responseCreated != nil && responseCreated.Payload != nil && responseCreated.Payload.Job != nil && responseCreated.Payload.Job.UUID != nil {
		return &JobAccepted{JobUUID: string(*responseCreated.Payload.Job.UUID)}, nil
	}
	if responseAccepted != nil && responseAccepted.Payload != nil && responseAccepted.Payload.Job != nil && responseAccepted.Payload.Job.UUID != nil {
		return &JobAccepted{JobUUID: string(*responseAccepted.Payload.Job.UUID)}, nil
	}
	return nil, errors.New("unexpected response from GcpKmsRekeyExternal")
}

// GcpKmsDelete invokes pkg/ontap-rest/client/security/Client.GcpKmsDelete
func (sc *securityClient) GcpKmsDelete(params *GcpKmsDeleteParams) error {
	response, _, err := (*sc.api).GcpKmsDelete(gcpKmsDeleteParamsToOntap(params), nil)
	if err != nil {
		return err
	}
	if response == nil {
		return errors.New("ontap-rest response for GcpKmsDelete is nil")
	}
	return nil
}

// EnableAutoVolOfflineCronForGCPKMS invokes pkg/ontap-rest/client/security/Client.KeyManagerConfigModify
func (sc *securityClient) EnableAutoVolOfflineCronForGCPKMS() error {
	response, err := (*sc.api).KeyManagerConfigModify(getGCPKeyManagerConfigModifyParamsToOntap(), nil)
	if err != nil {
		return err
	}
	if response == nil {
		return errors.New("ontap-rest response for EnableAutoVolOfflineCronForGCPKMS is nil")
	}
	if !response.IsSuccess() {
		return errors.New("ontap-rest response for EnableAutoVolOfflineCronForGCPKMS is unsuccessful")
	}
	return nil
}

// RoleCreate invokes pkg/ontap-rest/client/security/Client.RoleCreate
func (sc *securityClient) RoleCreate(params *RoleCreateParams) (string, error) {
	response, err := (*sc.api).RoleCreate(roleCreateParamsToONTAP(params), nil)
	if err != nil {
		return "", err
	}
	return response.Location, err
}

// RoleGet invokes pkg/ontap-rest/client/security/Client.RoleGet
func (sc *securityClient) RoleGet(params *RoleGetParams) (*Role, error) {
	response, err := (*sc.api).RoleGet(roleGetParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}

	if response == nil || response.Payload == nil {
		return nil, errors.New("unexpected response from RoleGet")
	}

	resp := &Role{Role: *response.Payload}
	return resp, nil
}

// RoleDelete invokes pkg/ontap-rest/client/security/Client.RoleDelete
func (sc *securityClient) RoleDelete(params *RoleDeleteParams) error {
	_, err := (*sc.api).RoleDelete(roleDeleteParamsToONTAP(params), nil)
	if err != nil {
		return err
	}
	return nil
}

// RolePrivilegeModify invokes pkg/ontap-rest/client/security/Client.RolePrivilegeModify
func (sc *securityClient) RolePrivilegeModify(params *RolePrivilegeModifyParams) error {
	_, err := (*sc.api).RolePrivilegeModify(rolePrivilegeModifyParamsToONTAP(params), nil)
	if err != nil {
		return err
	}
	return nil
}

// RolePrivilegeDelete invokes pkg/ontap-rest/client/security/Client.RolePrivilegeDelete
func (sc *securityClient) RolePrivilegeDelete(params *RolePrivilegeDeleteParams) error {
	_, err := (*sc.api).RolePrivilegeDelete(rolePrivilegeDeleteParamsToONTAP(params), nil)
	if err != nil {
		return err
	}
	return nil
}

// RolePrivilegeCreate invokes pkg/ontap-rest/client/security/Client.RolePrivilegeCreate
func (sc *securityClient) RolePrivilegeCreate(params *RolePrivilegeCreateParams) (string, error) {
	response, err := (*sc.api).RolePrivilegeCreate(rolePrivilegeCreateParamsToONTAP(params), nil)
	if err != nil {
		return "", err
	}
	return response.Location, nil
}

// RoleCollectionGet invokes pkg/ontap-rest/client/security/Client.RoleCollectionGet
func (sc *securityClient) RoleCollectionGet(params *RoleCollectionGetParams) (*RoleCollectionGetResponse, error) {
	response, err := (*sc.api).RoleCollectionGet(roleCollectionGetParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}

	if response == nil || response.Payload == nil {
		return nil, errors.New("unexpected response from RoleCollectionGet")
	}

	resp := &RoleCollectionGetResponse{RoleCollectionGetOK: response}
	return resp, nil
}

// ServerRootCACertificateGet invokes pkg/ontap-rest/client/security/Client.SecurityCertificateCollectionGet
func (sc *securityClient) ServerRootCACertificateGet(params *ServerRootCAGetParams) (*ServerRootCACertificate, error) {
	certs, err := sc.securityCertificateCollectionGet(serverRootCAGetParamsToONTAPCollectionGet(params))
	if err != nil {
		return nil, err
	}
	if len(certs) == 0 {
		return nil, errors.NewNotFoundErr("ServerRootCACertificate", nil)
	}
	return certs[0], nil
}

func (sc *securityClient) securityCertificateCollectionGet(otParams *security.SecurityCertificateCollectionGetParams) ([]*ServerRootCACertificate, error) {
	response, err := (*sc.api).SecurityCertificateCollectionGet(otParams, nil)
	if err != nil {
		return nil, err
	}
	var resp []*ServerRootCACertificate
	if response != nil && response.Payload != nil && len(response.Payload.SecurityCertificateResponseInlineRecords) > 0 {
		resp = make([]*ServerRootCACertificate, len(response.Payload.SecurityCertificateResponseInlineRecords))
		for i, cert := range response.Payload.SecurityCertificateResponseInlineRecords {
			resp[i] = &ServerRootCACertificate{SecurityCertificate: *cert}
		}
	}
	return resp, err
}

// ServerRootCACertificateInstall invokes pkg/ontap-rest/client/security/Client.ServerRootCACertificateInstall
func (sc *securityClient) ServerRootCACertificateInstall(params *ServerRootCAInstallParams) (*ServerRootCACertificate, error) {
	return sc.securityCertificateCreate(serverRootCAInstallParamsToONTAP(params))
}

func (sc *securityClient) securityCertificateCreate(otParams *security.SecurityCertificateCreateParams) (*ServerRootCACertificate, error) {
	response, err := (*sc.api).SecurityCertificateCreate(otParams, nil)
	if err != nil {
		return nil, err
	}

	var cert *ServerRootCACertificate
	if response != nil && response.Payload != nil && len(response.Payload.SecurityCertificateResponseInlineRecords) > 0 {
		cert = &ServerRootCACertificate{SecurityCertificate: *response.Payload.SecurityCertificateResponseInlineRecords[0]}
	}
	return cert, nil
}

// ServerRootCACertificateDelete invokes pkg/ontap-rest/client/security/Client.ServerRootCACertificateDelete
func (sc *securityClient) ServerRootCACertificateDelete(params *ServerRootCADeleteParams) error {
	_, err := (*sc.api).SecurityCertificateDeleteCollection(serverRootCADeleteParamsToONTAPCollectionDelete(params), nil)
	return err
}

// ServerRootCACertificateCollectionGet invokes pkg/ontap-rest/client/security/Client.ServerRootCACertificateCollectionGet
func (sc *securityClient) ServerRootCACertificateCollectionGet(params *ServerRootCAGetCollectionParams) ([]*ServerRootCACertificate, error) {
	return sc.securityCertificateCollectionGet(serverRootCAGetCollectionParamsToONTAP(params))
}

func serverRootCAGetCollectionParamsToONTAP(params *ServerRootCAGetCollectionParams) *security.SecurityCertificateCollectionGetParams {
	otParams := security.NewSecurityCertificateCollectionGetParams()
	if params == nil {
		return otParams
	}
	otParams.SetSvmName(params.SvmName)
	otParams.SetType(params.CertificateType)
	if params.Name != nil {
		otParams.SetName(params.Name)
	}
	return otParams
}

// SecurityCertificateDeleteCollection deletes certificates from ONTAP
func (sc *securityClient) SecurityCertificateDeleteCollection(params *SecurityCertificateDeleteCollectionParams) error {
	_, err := (*sc.api).SecurityCertificateDeleteCollection(securityCertificateDeleteCollectionParamsToONTAP(params), nil)
	return err
}

// ResetLoginPassword invokes POST /api/private/cli/security/login/password via
// the generated private-CLI client, inheriting the shared transport stack
// (retry, auth, logging, TLS/SNI).
func (sc *securityClient) ResetLoginPassword(ctx context.Context, vserver, username, newPassword string) error {
	params := securitypriv.NewSecurityLoginPasswordModifyParams().WithContext(ctx)
	params.SetBody(securitypriv.SecurityLoginPasswordModifyBody{
		Vserver:     &vserver,
		Username:    &username,
		NewPassword: &newPassword,
	})
	_, err := (*sc.apiPriv).SecurityLoginPasswordModify(params)
	return err
}

// ExpireLoginPassword invokes POST /api/private/cli/security/login/expire-password
// via the generated private-CLI client, inheriting the shared transport stack
// (retry, auth, logging, TLS/SNI).
func (sc *securityClient) ExpireLoginPassword(ctx context.Context, vserver, username string) error {
	params := securitypriv.NewSecurityLoginExpirePasswordParams().WithContext(ctx)
	params.SetBody(securitypriv.SecurityLoginExpirePasswordBody{
		Vserver:  &vserver,
		Username: &username,
	})
	_, err := (*sc.apiPriv).SecurityLoginExpirePassword(params)
	return err
}
