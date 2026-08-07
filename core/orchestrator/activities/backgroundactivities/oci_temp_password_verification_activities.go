package backgroundactivities

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const tempPasswordActiveFilterField = "external_temp_credentials->>'temp_password_active'"

type OciTempPasswordVerificationActivity struct {
	SE database.Storage
}

// ListOciPoolsWithActiveTempPassword returns one paginated batch of pools with an
// active temporary ociadmin password that are also in a reset-eligible state
// (OCI_TEMP_PASSWORD_RESET_POOL_STATES). The state filter skips pools that are being
// deleted or are otherwise not serviceable so we do not attempt a reset against a gone VSA.
func (a *OciTempPasswordVerificationActivity) ListOciPoolsWithActiveTempPassword(ctx context.Context, offset, limit int) (*ListPoolsBatchResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Listing OCI pools with active temporary password (offset=%d, limit=%d)", offset, limit)

	readyStates := env.GetOCITempPasswordResetPoolStates()
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition(tempPasswordActiveFilterField, "=", "true"),
		dbutils.NewFilterCondition("state", "in", readyStates),
	)
	pagination := &dbutils.Pagination{Limit: limit, Offset: offset}

	poolViews, err := a.SE.ListPoolsWithFilterAndPaginationOrderedByUUID(ctx, filter, pagination)
	if err != nil {
		logger.Errorf("Failed to list OCI pools with active temporary password: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	hasMore := len(poolViews) == limit
	var pools []*datamodel.Pool
	for _, poolView := range poolViews {
		pools = append(pools, poolWithTempCredentials(poolView))
	}
	logger.Debugf("Retrieved batch of %d OCI pools with active temporary password (hasMore=%v)", len(pools), hasMore)
	return &ListPoolsBatchResult{Pools: pools, HasMore: hasMore}, nil
}

// poolWithTempCredentials converts a pool view to a pool, re-attaching the
// ExternalTempCredentials that ConvertPoolViewToPool does not copy.
func poolWithTempCredentials(view *datamodel.PoolView) *datamodel.Pool {
	pool := database.ConvertPoolViewToPool(view)
	if pool != nil && view != nil {
		pool.ExternalTempCredentials = view.ExternalTempCredentials
	}
	return pool
}
