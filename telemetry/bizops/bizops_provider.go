package bizops

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"

	telemetrydb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/metrics"
	metricModel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	vcpdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/bizops/sink"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/entity"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

const (
	accountEnabled = "ENABLED"
)

var (
	googleContinents = env.GetString("GOOGLE_CONTINENTS", "")
	region           = env.GetString("GOOGLE_REGION", "australia-southeast1")
	paginationLimit  = env.GetInt("BIZOPS_ACCOUNT_PAGINATION_LIMIT", 1000)
)

type BizOpsProvider interface {
	ProcessBizOps(ctx context.Context, logger log.Logger, params *utils.BizOpsReportParams) error
}

type bizOpsProvider struct {
	bizOpsSink sink.BizOpsSink
	metricsDB  telemetrydb.Storage
	vcpDB      vcpdb.Storage
}

func NewBizOpsProvider(metricsDB telemetrydb.Storage, vcpDB vcpdb.Storage, bizOpsSink sink.BizOpsSink) BizOpsProvider {
	return &bizOpsProvider{
		metricsDB:  metricsDB,
		vcpDB:      vcpDB,
		bizOpsSink: bizOpsSink,
	}
}

func (bp *bizOpsProvider) ProcessBizOps(ctx context.Context, logger log.Logger, params *utils.BizOpsReportParams) error {
	logger.Debugf("ProcessBizOps with params: params: %v", params)
	bizOpsSink, err := validateAndGetSink(bp.bizOpsSink, params.SinkType)
	if err != nil {
		logger.Errorf("Failed to validate biz ops sink: %v", err)
		return err
	}
	logger.Debugf("ProcessBizOps with sink: %s", bizOpsSink.Type())
	accountsInfo, err := bp.getAccountsInfo(ctx)
	if err != nil {
		logger.Errorf("Failed to get accounts: %v", err)
		return err
	}
	continentMap := GetContinentMap(googleContinents)
	pr, pw := io.Pipe()
	bizopsAggrParams := &metricModel.BizOpsAggregateParams{
		AccountsInfo: accountsInfo,
		ContinentMap: continentMap,
		Region:       region,
		AggrStart:    params.StartDate,
		AggrEnd:      params.EndDate,
		Writer:       pw,
	}
	logger.Info("Processing BizOps Report")
	sinkParams := &entity.BizopsSinkParams{
		Reader:   pr,
		Region:   region,
		Date:     params.StartDate,
		Timezone: params.TimeZone,
	}

	n := 2
	errchannel := make(chan error, n)

	go func() {
		var err error
		defer func() {
			err = errors.Join(err, pr.Close())
			errchannel <- err
		}()
		err = bizOpsSink.Ingest(ctx, sinkParams)
		if err != nil {
			logger.Errorf("Failed to ingest BizOps Report: %v", err)
		}
	}()
	go func() {
		var err error
		defer func() {
			err = errors.Join(err, pw.Close())
			errchannel <- err
		}()
		err = bp.metricsDB.AggregateUsageForBizOps(ctx, bizopsAggrParams)
		if err != nil {
			logger.Errorf("Failed to aggregate usage for BizOps: %v", err)
		}
	}()

	var errs error
	for i := 0; i != n; {
		select {
		case tmpErr := <-errchannel:
			i++
			errs = errors.Join(errs, tmpErr)
		}
	}
	return errs
}

func prepareAccountInfo(accounts []*vcpdb.AccountTelemetryData) []*metricModel.AccountInfo {
	var accountsInfo []*metricModel.AccountInfo
	for _, account := range accounts {
		var isActive bool
		if account.State == accountEnabled {
			isActive = true
		}
		accountsInfo = append(accountsInfo, &metricModel.AccountInfo{
			AccountID: strconv.FormatInt(account.ID, 10),
			UserName:  account.Name,
			IsActive:  isActive,
		})
	}
	return accountsInfo
}

func GetContinentMap(googleContinents string) map[string]string {
	continentMap := make(map[string]string)
	entries := strings.Split(googleContinents, ",")
	for _, entry := range entries {
		parts := strings.Split(entry, ":")
		if len(parts) > 1 {
			continentMap[parts[0]] = parts[1]
		}
	}
	return continentMap
}

func (bp *bizOpsProvider) getAccountsInfo(ctx context.Context) ([]*metricModel.AccountInfo, error) {
	var accountsInfo []*metricModel.AccountInfo
	totalFetched := 0
	for {
		pagination := &dbutils.Pagination{
			Limit:  paginationLimit,
			Offset: totalFetched,
		}
		// Use optimized query that fetches only id, name, and state fields
		accounts, err := bp.vcpDB.ListAccountsForTelemetry(ctx, pagination)
		if err != nil {
			return accountsInfo, err
		}
		// Break if there are no more accounts to process
		if len(accounts) == 0 {
			break
		}
		totalFetched += len(accounts)
		accountInfo := prepareAccountInfo(accounts)
		accountsInfo = append(accountsInfo, accountInfo...)
	}
	return accountsInfo, nil
}

func validateAndGetSink(bizOpsSink sink.BizOpsSink, sinkType string) (sink.BizOpsSink, error) {
	if bizOpsSink == nil {
		return nil, errors.New("biz ops sink is nil")
	}
	if bizOpsSink.Type() != sinkType {
		if _, ok := bizOpsSinkMapping[sinkType]; !ok {
			return nil, errors.New("invalid biz ops sink type")
		}
		newSink, err := NewSink(bizOpsSinkMapping[sinkType])
		if err != nil {
			return nil, err
		}
		bizOpsSink = newSink
	}
	return bizOpsSink, nil
}
