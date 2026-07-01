package vsa

import (
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// CreateRoleParams represents parameters for creating a role
type CreateRoleParams struct {
	Name       string
	Privileges []*RolePrivilege
}

// RolePrivilege represents a role privilege
type RolePrivilege struct {
	Path   string
	Access string
	Query  string
}

// Role represents a role response
type Role struct {
	Name       string
	Privileges []*RolePrivilege
	OwnerID    string
}

// GetRoleParams represents parameters for getting a role
type GetRoleParams struct {
	Name      string
	OwnerUUID *string
}

// ModifyRolePrivilegeParams represents parameters for modifying a role privilege
type ModifyRolePrivilegeParams struct {
	OwnerID string
	Name    string
	Path    string
	Access  string
	Query   string
}

// DeleteRolePrivilegeParams represents parameters for deleting a role privilege
type DeleteRolePrivilegeParams struct {
	OwnerID string
	Name    string
	Path    string
}

// CreateRolePrivilegeParams represents parameters for creating a role privilege
type CreateRolePrivilegeParams struct {
	OwnerID string
	Name    string
	Path    string
	Access  string
	Query   string
}

// GetRoleCollectionParams represents parameters for getting a collection of roles
type GetRoleCollectionParams struct {
	Name *string
}

// DeleteRoleParams represents parameters for deleting a role
type DeleteRoleParams struct {
	Name      string
	OwnerUUID *string
}

// CreateRole creates a new role in ONTAP
func (rc *OntapRestProvider) CreateRole(params CreateRoleParams) (string, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return "", err
	}

	// Convert privileges to ONTAP format
	privileges := make([]*ontapRest.RolePrivilege, 0)
	for _, privilege := range params.Privileges {
		privileges = append(privileges, &ontapRest.RolePrivilege{
			Path:   privilege.Path,
			Access: privilege.Access,
			Query:  privilege.Query,
		})
	}

	// Create role parameters
	roleParams := &ontapRest.RoleCreateParams{
		Name:       params.Name,
		Privileges: privileges,
	}

	// Create the role
	location, err := client.Security().RoleCreate(roleParams)
	if err != nil {
		return "", err
	}

	return location, nil
}

// GetRole retrieves a role from ONTAP
func (rc *OntapRestProvider) GetRole(params GetRoleParams) (*Role, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	// Create get role parameters
	getParams := &ontapRest.RoleGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"privileges", "owner"}},
		Name:       params.Name,
		OwnerUUID:  params.OwnerUUID,
	}

	// Get the role
	ontapRole, err := client.Security().RoleGet(getParams)
	if err != nil {
		return nil, err
	}

	rolePrivileges := make([]*RolePrivilege, 0)
	for _, privilege := range ontapRole.RoleInlinePrivileges {
		query := nillable.GetString(privilege.Query, "")
		rolePrivileges = append(rolePrivileges, &RolePrivilege{
			Access: string(*privilege.Access),
			Path:   *privilege.Path,
			Query:  query,
		})
	}
	returnedRole := &Role{
		Name:       *ontapRole.Name,
		Privileges: rolePrivileges,
	}
	if ontapRole.Owner != nil {
		returnedRole.OwnerID = *ontapRole.Owner.UUID
	}

	return returnedRole, nil
}

// ModifyRolePrivilege modifies a role privilege in ONTAP
func (rc *OntapRestProvider) ModifyRolePrivilege(params ModifyRolePrivilegeParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	// Create modify privilege parameters
	modifyParams := &ontapRest.RolePrivilegeModifyParams{
		OwnerID: params.OwnerID,
		Name:    params.Name,
		Path:    params.Path,
		Access:  params.Access,
		Query:   params.Query,
	}

	// Modify the role privilege
	err = client.Security().RolePrivilegeModify(modifyParams)
	if err != nil {
		return err
	}

	return nil
}

// DeleteRolePrivilege deletes a role privilege from ONTAP
func (rc *OntapRestProvider) DeleteRolePrivilege(params DeleteRolePrivilegeParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	// Create delete privilege parameters
	deleteParams := &ontapRest.RolePrivilegeDeleteParams{
		OwnerID: params.OwnerID,
		Name:    params.Name,
		Path:    params.Path,
	}

	// Delete the role privilege
	err = client.Security().RolePrivilegeDelete(deleteParams)
	if err != nil {
		return err
	}

	return nil
}

// CreateRolePrivilege creates a new role privilege in ONTAP
func (rc *OntapRestProvider) CreateRolePrivilege(params CreateRolePrivilegeParams) (string, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return "", err
	}

	// Create privilege parameters
	createParams := &ontapRest.RolePrivilegeCreateParams{
		OwnerID: params.OwnerID,
		Name:    params.Name,
		Path:    params.Path,
		Access:  params.Access,
		Query:   params.Query,
	}

	// Create the role privilege
	location, err := client.Security().RolePrivilegeCreate(createParams)
	if err != nil {
		return "", err
	}

	return location, nil
}

// GetRoleCollection retrieves a collection of roles from ONTAP
func (rc *OntapRestProvider) GetRoleCollection(params GetRoleCollectionParams) ([]*Role, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	// Create role collection get parameters
	otParams := &ontapRest.RoleCollectionGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"name", "owner", "privileges"}},
		Name:       params.Name,
	}

	// Query for roles
	response, err := client.Security().RoleCollectionGet(otParams)
	if err != nil {
		return nil, err
	}

	// Convert ONTAP roles to VSA roles
	var roles []*Role
	if response.Payload != nil && response.Payload.RoleResponseInlineRecords != nil {
		for _, record := range response.Payload.RoleResponseInlineRecords {
			if record == nil || record.Name == nil {
				continue
			}

			role := &Role{
				Name: *record.Name,
			}

			// Set OwnerID if owner exists
			if record.Owner != nil && record.Owner.UUID != nil {
				role.OwnerID = *record.Owner.UUID
			}

			// Convert privileges if available
			if record.RoleInlinePrivileges != nil {
				rolePrivileges := make([]*RolePrivilege, 0)
				for _, priv := range record.RoleInlinePrivileges {
					if priv == nil || priv.Path == nil || priv.Access == nil {
						continue
					}
					rolePrivileges = append(rolePrivileges, &RolePrivilege{
						Path:   *priv.Path,
						Access: string(*priv.Access),
						Query:  nillable.GetString(priv.Query, ""),
					})
				}
				role.Privileges = rolePrivileges
			}

			roles = append(roles, role)
		}
	}

	return roles, nil
}

// DeleteRole deletes a role from ONTAP
func (rc *OntapRestProvider) DeleteRole(params DeleteRoleParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	// Create delete role parameters
	deleteParams := &ontapRest.RoleDeleteParams{
		Name:      params.Name,
		OwnerUUID: params.OwnerUUID,
	}

	// Delete the role
	err = client.Security().RoleDelete(deleteParams)
	if err != nil {
		return err
	}

	return nil
}
