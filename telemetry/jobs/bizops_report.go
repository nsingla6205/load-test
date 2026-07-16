package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
)

type BizOpsReport struct {
	BizOpsParams *utils.BizOpsReportParams `json:"BizOpsParams"`
}

func NewBizOpsReport(params *utils.BizOpsReportParams) *BizOpsReport {
	return &BizOpsReport{
		BizOpsParams: params,
	}
}

func (br BizOpsReport) Perform(ctx context.Context, p interface{}, attempt int32) error {
	procssor, ok := p.(common.VCPProcessor)
	if !ok {
		return fmt.Errorf("invalid processor type: %T", p)
	}
	err := procssor.ProcessBizOps(ctx, br.BizOpsParams)
	if err != nil {
		return err
	}
	return nil
}

func (br BizOpsReport) Load(data string) (utils.Job, error) {
	var wrapper struct {
		Params utils.BizOpsReportParams `json:"BizOpsParams"`
	}
	err := json.Unmarshal([]byte(data), &wrapper)
	if err != nil {
		return nil, err
	}
	return BizOpsReport{
		BizOpsParams: &wrapper.Params,
	}, nil
}
