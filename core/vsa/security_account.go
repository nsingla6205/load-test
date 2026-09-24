package vsa

import (
	"fmt"
	"strings"

	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
)

// CreateSecurityAccountParams represents parameters for creating an ONTAP security account.
type CreateSecurityAccountParams struct {
	Name        string
	Password    string
	RoleName    string
	ClusterUUID string

	// AuthenticationMethods, when non-empty, creates a certificate-auth (or other
	// explicit-method) account instead of a password account, e.g. ["certificate"].
	AuthenticationMethods []string
	// SslCaCertificate is optional and unused by the OCI read-only service
	// account path. Issuer trust is established via InstallClientCA
	// (type=client_ca); this ONTAP REST surface rejects ssl_ca_certificate on
	// account create.
	SslCaCertificate string
}

// ModifySecurityAccountParams represents parameters for modifying an ONTAP security account.
type ModifySecurityAccountParams struct {
	Name         string
	OwnerUUID    string
	Applications []ontapRest.SecurityAccountApplication
}

// GetSecurityAccountParams represents parameters for getting an ONTAP security account.
type GetSecurityAccountParams struct {
	Name string
}

// DeleteSecurityAccountParams represents parameters for deleting an ONTAP security account.
type DeleteSecurityAccountParams struct {
	Name      string
	OwnerUUID string
}

// SecurityAccount represents an ONTAP security account.
type SecurityAccount struct {
	Name         string
	OwnerUUID    string
	Role         *SecurityAccountRole
	Applications []ontapRest.SecurityAccountApplication
}

// SecurityAccountRole represents the role assigned to a security account.
type SecurityAccountRole struct {
	Name string
}

// GetClusterUUID returns the ONTAP cluster UUID.
func (rc *OntapRestProvider) GetClusterUUID() (string, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return "", err
	}
	return client.Cluster().GetClusterUUID()
}

// GetRoleOwnerUUID returns the ONTAP owner UUID for a security role.
func (rc *OntapRestProvider) GetRoleOwnerUUID(roleName string) (string, error) {
	roleName = strings.TrimSpace(roleName)
	if roleName == "" {
		return "", fmt.Errorf("role name is required")
	}
	roles, err := rc.GetRoleCollection(GetRoleCollectionParams{Name: &roleName})
	if err != nil {
		return "", err
	}
	if len(roles) == 0 {
		return "", fmt.Errorf("role %q not found", roleName)
	}
	if roles[0].OwnerID == "" {
		return "", fmt.Errorf("role %q has no owner uuid", roleName)
	}
	return roles[0].OwnerID, nil
}

// CreateSecurityAccount creates an ONTAP security account.
func (rc *OntapRestProvider) CreateSecurityAccount(params CreateSecurityAccountParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	return client.Security().AccountCreate(&ontapRest.AccountCreateParams{
		Name:                  params.Name,
		Password:              params.Password,
		RoleName:              params.RoleName,
		OwnerUUID:             params.ClusterUUID,
		AuthenticationMethods: params.AuthenticationMethods,
		SslCaCertificate:      params.SslCaCertificate,
	})
}

// ModifySecurityAccount updates an ONTAP security account.
func (rc *OntapRestProvider) ModifySecurityAccount(params ModifySecurityAccountParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	return client.Security().AccountModify(&ontapRest.AccountModifyParams{
		Name:         params.Name,
		OwnerUUID:    params.OwnerUUID,
		Applications: params.Applications,
	})
}

// GetSecurityAccount returns an ONTAP security account by name.
func (rc *OntapRestProvider) GetSecurityAccount(params GetSecurityAccountParams) (*SecurityAccount, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	account, err := client.Security().AccountGet(&ontapRest.AccountGetParams{
		Name:   params.Name,
		Fields: []string{"name", "applications", "owner", "role"},
	})
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, nil
	}
	result := &SecurityAccount{Name: account.Name}
	if account.Owner != nil {
		result.OwnerUUID = account.Owner.UUID
	}
	if account.Role != nil {
		result.Role = &SecurityAccountRole{Name: account.Role.Name}
	}
	result.Applications = append([]ontapRest.SecurityAccountApplication(nil), account.Applications...)
	return result, nil
}

// DeleteSecurityAccount deletes an ONTAP security account.
func (rc *OntapRestProvider) DeleteSecurityAccount(params DeleteSecurityAccountParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	return client.Security().AccountDelete(&ontapRest.AccountDeleteParams{
		Name:      params.Name,
		OwnerUUID: params.OwnerUUID,
	})
}
