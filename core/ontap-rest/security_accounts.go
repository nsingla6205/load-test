package ontap_rest

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/security"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// AccountCreateParams describes parameters for creating an ONTAP security account.
type AccountCreateParams struct {
	Name      string
	Password  string
	RoleName  string
	OwnerUUID string

	// AuthenticationMethods, when non-empty, replaces the default password-based
	// application bindings (http/ontapi/ssh) with the given methods for the
	// http and ontapi applications. Used for certificate-auth accounts, e.g.
	// ["certificate"]. When set, Password may be empty.
	AuthenticationMethods []string

	// SslCaCertificate is optional. Prefer installing the issuer as a
	// client_ca via the certificates API; some ONTAP builds reject this
	// field on account create with "Unexpected argument".
	SslCaCertificate string
}

// AccountModifyParams describes parameters for modifying an ONTAP security account.
type AccountModifyParams struct {
	Name      string
	OwnerUUID string
	// Applications replaces authentication bindings for the listed applications.
	Applications []SecurityAccountApplication
}

// AccountGetParams describes parameters for looking up an ONTAP security account.
type AccountGetParams struct {
	Name string
}

// AccountDeleteParams describes parameters for deleting an ONTAP security account.
type AccountDeleteParams struct {
	Name      string
	OwnerUUID string
}

// SecurityAccount describes an ONTAP security account response record.
type SecurityAccount struct {
	Name  string                `json:"name,omitempty"`
	Role  *SecurityAccountRole  `json:"role,omitempty"`
	Owner *SecurityAccountOwner `json:"owner,omitempty"`
}

// SecurityAccountRole describes the role assigned to a security account.
type SecurityAccountRole struct {
	Name string `json:"name,omitempty"`
}

// SecurityAccountOwner describes the owner scope for a security account.
type SecurityAccountOwner struct {
	UUID string `json:"uuid,omitempty"`
}

// SecurityAccountApplication describes an application authentication binding.
type SecurityAccountApplication struct {
	Application           string   `json:"application"`
	AuthenticationMethods []string `json:"authentication_methods"`
}

func (sc *securityClient) AccountCreate(params *AccountCreateParams) error {
	if params == nil {
		return fmt.Errorf("account create params are required")
	}
	if strings.TrimSpace(params.Name) == "" {
		return fmt.Errorf("account name is required")
	}
	// A cert-auth account carries no password; require one only when no explicit
	// authentication methods (e.g. certificate) are supplied.
	if strings.TrimSpace(params.Password) == "" && len(params.AuthenticationMethods) == 0 {
		return fmt.Errorf("account password or authentication methods are required")
	}
	if strings.TrimSpace(params.RoleName) == "" {
		return fmt.Errorf("account role name is required")
	}
	if strings.TrimSpace(params.OwnerUUID) == "" {
		return fmt.Errorf("account owner uuid is required")
	}

	_, err := (*sc.api).AccountCreate(accountCreateParamsToONTAP(params), nil)
	return err
}

func (sc *securityClient) AccountModify(params *AccountModifyParams) error {
	if params == nil {
		return fmt.Errorf("account modify params are required")
	}
	if strings.TrimSpace(params.Name) == "" {
		return fmt.Errorf("account name is required")
	}
	if strings.TrimSpace(params.OwnerUUID) == "" {
		return fmt.Errorf("account owner uuid is required")
	}
	if len(params.Applications) == 0 {
		return fmt.Errorf("account applications are required")
	}

	_, err := (*sc.api).AccountModify(accountModifyParamsToONTAP(params), nil)
	return err
}

func (sc *securityClient) AccountGet(params *AccountGetParams) (*SecurityAccount, error) {
	if params == nil || strings.TrimSpace(params.Name) == "" {
		return nil, fmt.Errorf("account name is required")
	}

	response, err := (*sc.api).AccountCollectionGet(accountCollectionGetParamsToONTAP(params), nil)
	if err != nil {
		var defaultErr *security.AccountCollectionGetDefault
		if errors.As(err, &defaultErr) && defaultErr.Code() == 404 {
			return nil, utilserrors.NewNotFoundErr("SecurityAccount", &params.Name)
		}
		return nil, err
	}

	if response == nil || response.Payload == nil || len(response.Payload.AccountResponseInlineRecords) == 0 {
		return nil, utilserrors.NewNotFoundErr("SecurityAccount", &params.Name)
	}

	return securityAccountFromONTAP(response.Payload.AccountResponseInlineRecords[0]), nil
}

func (sc *securityClient) AccountDelete(params *AccountDeleteParams) error {
	if params == nil {
		return fmt.Errorf("account delete params are required")
	}
	if strings.TrimSpace(params.Name) == "" {
		return fmt.Errorf("account name is required")
	}
	if strings.TrimSpace(params.OwnerUUID) == "" {
		return fmt.Errorf("account owner uuid is required")
	}

	_, err := (*sc.api).AccountDelete(accountDeleteParamsToONTAP(params), nil)
	if err != nil {
		var defaultErr *security.AccountDeleteDefault
		if errors.As(err, &defaultErr) && defaultErr.Code() == 404 {
			return utilserrors.NewNotFoundErr("SecurityAccount", &params.Name)
		}
		return err
	}

	return nil
}

func securityAccountFromONTAP(account *models.Account) *SecurityAccount {
	if account == nil {
		return nil
	}

	resp := &SecurityAccount{}
	if account.Name != nil {
		resp.Name = *account.Name
	}
	if account.Role != nil && account.Role.Name != nil {
		resp.Role = &SecurityAccountRole{Name: *account.Role.Name}
	}
	if account.Owner != nil && account.Owner.UUID != nil {
		resp.Owner = &SecurityAccountOwner{UUID: *account.Owner.UUID}
	}
	return resp
}
