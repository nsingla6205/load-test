package api

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// V1ListSVMs returns SVMs for EXTREME or PREMIUM service-level hardware cluster pools.
func (h Handler) V1ListSVMs(ctx context.Context, _ oasgenserver.V1ListSVMsParams) (oasgenserver.V1ListSVMsRes, error) {
	list, err := h.Orchestrator.ListSvmsForHardwareClusters(ctx)
	if err != nil {
		return &oasgenserver.V1ListSVMsInternalServerError{
			Message: fmt.Sprintf("Error listing SVMs: %v", err),
			Code:    500,
		}, nil
	}

	result := make(oasgenserver.V1ListSVMsOKApplicationJSON, 0, len(list))
	for _, svm := range list {
		result = append(result, svmToV1(svm))
	}
	return &result, nil
}

func svmToV1(svm *datamodel.SvmView) oasgenserver.SVMV1 {
	v := oasgenserver.SVMV1{
		UUID:           oasgenserver.NewOptString(svm.UUID),
		CreatedAt:      oasgenserver.NewOptDateTime(svm.CreatedAt),
		UpdatedAt:      oasgenserver.NewOptDateTime(svm.UpdatedAt),
		Name:           oasgenserver.NewOptString(svm.Name),
		AccountUUID:    oasgenserver.NewOptString(svm.AccountUUID),
		AccountName:    oasgenserver.NewOptString(svm.AccountName),
		ClusterUUID:    oasgenserver.NewOptString(svm.ClusterUUID),
		VendorSubnetID: oasgenserver.NewOptString(svm.VendorSubnetID),
		IprangeUUID:    oasgenserver.NewOptString(svm.IPRangeUUID),
	}
	if svm.DeletedAt != nil && svm.DeletedAt.Valid {
		v.DeletedAt = oasgenserver.NewOptNilDateTime(svm.DeletedAt.Time)
	}
	if svm.SvmDetails != nil && svm.SvmDetails.ExternalUUID != "" {
		v.ExternalUUID = oasgenserver.NewOptString(svm.SvmDetails.ExternalUUID)
	}
	return v
}
