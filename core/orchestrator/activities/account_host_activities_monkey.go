//go:build mocks

package activities

import (
	"context"

	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
)

// monkeyMethods declares account-host functions mockery generates mocks for.
// Kept in a mocks-tagged file per MONKEY_MOCKS_README.md; run make generate-monkey-mocks.
type monkeyMethods interface {
	accountHostGetGCPService(ctx context.Context) (hyperscaler.Services, error)
	accountHostGetProviderForHost(ctx context.Context, host *datamodel.Host) (vsa.Provider, error)
	accountHostVerifyHostServiceCredentials(ctx context.Context, host *datamodel.Host, svmPattern, username, password string) error
	accountHostEnsureExpertModeRole(ctx context.Context, provider vsa.Provider, roleName string, privileges []*vsa.RolePrivilege) error
	accountHostEnsureExpertModeAccount(ctx context.Context, provider vsa.ExpertModeAccountProvider, params vsa.CreateExpertModeAccountParams) error
	accountHostDeleteExpertModeAccount(ctx context.Context, provider vsa.ExpertModeAccountProvider, username, ownerUUID string) error
	accountHostDeleteExpertModeRole(ctx context.Context, provider vsa.Provider, roleName, ownerUUID string) error
	accountHostGetRoleOwnerUUID(provider vsa.ExpertModeAccountProvider, roleName string) (string, error)
	accountHostGetBucketFileContent(service hyperscaler.Services, ctx context.Context, bucketName, fileURL string) (*hyperscalermodels.BucketFileDetails, error)
	loadAccountHostRBACPrivileges(ctx context.Context, ontapVersion string) ([]orchcommon.ExpertModeRBACPrivilege, error)
	generateAccountHostServicePassword(ctx context.Context, cred *datamodel.ExpertModeCredential) (string, error)
}
