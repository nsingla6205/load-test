package database

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"time"

	datamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	"gorm.io/gorm"
)

var (
	reportColumns = []string{
		"COMPONENT",
		"CUSTOMER_ID",
		"IS_ACTIVE",
		"CHARGEABILITY",
		"NUM_POOLS",
		"NUM_VOLUMES",
		"REPORT_START",
		"REPORT_END",
		"SERVICE_LEVEL",
		"CRR_FREQUENCY",
		"RESOURCE_TYPE",
		"VOLUME_TYPE",
		"REGION",
		"SOURCE_REGION",
		"BACKUP_REGION",
		"CRR_SOURCE_CONTINENT",
		"CRR_DEST_CONTINENT",
		"TOTAL_BYTES_TRANSFERRED_CRR_GIB",
		"TOTAL_POOL_ALLOCATED_GIBH",
		"TOTAL_AVG_GIB_USED",
		"TOTAL_BACKUP_GIBH",
		"TOTAL_BACKUP_MANAGEMENT_USAGE_GIBH",
		"TOTAL_CROSS_REGION_BACKUP_TRANSFERRED_GIB",
		"TOTAL_RESTORE_TRANSFERRED_BYTES_GIB",
		"TOTAL_POOL_COOL_TIER_GIBH",
		"TOTAL_POOL_STANDARD_TIER_GIBH",
		"TOTAL_POOL_COOL_TIER_READ_SIZE_GIBH",
		"TOTAL_POOL_COOL_TIER_WRITE_SIZE_GIBH",
		"POOL_TOTAL_THROUGHPUT_MIBPS",
		"POOL_TOTAL_BILLABLE_IOPS",
		"ACTUAL_SUBMITTED_QUANTITY_GIB",
	}
)

func (r *DataStoreRepository) AggregateUsageForBizOps(
	ctx context.Context,
	bizopsAggrParams *datamodel.BizOpsAggregateParams,
) error {
	var err error
	db := r.db.GORM().WithContext(ctx)
	// Below we are starting a new session and transaction to ensure temp tables are scoped to this transaction only and database agnostic
	// and do not interfere with any other operations that might be happening concurrently.
	// As per GORM docs, NewDB: true creates a new session without any of the previous session's settings.
	// https://gorm.io/docs/session.html#New-Session
	session := db.Session(&gorm.Session{NewDB: true})
	tx := session.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()

	// Create Users Info
	err = ingestAccountInfo(tx, bizopsAggrParams.AccountsInfo)
	if err != nil {
		return err
	}

	// Create AggregatedUsageConstrained
	err = tx.Exec(aggregatedUsageConstrained,
		bizopsAggrParams.AggrStart.UTC(), bizopsAggrParams.AggrEnd.UTC(),
		bizopsAggrParams.Region).Error
	if err != nil {
		return err
	}

	// Create UsageStatistics
	err = tx.Exec(usageStatistics).Error
	if err != nil {
		return err
	}

	// Process and Create Google Continents
	err = ingestGoogleContinents(tx, bizopsAggrParams.ContinentMap, bizopsAggrParams.Region)
	if err != nil {
		return err
	}

	// PoolUsageCalculated
	err = tx.Exec(poolUsageCalculated).Error
	if err != nil {
		return err
	}

	// Final Report
	rows, err := tx.Raw(
		finalReport, bizopsAggrParams.AggrStart.Format(time.RFC3339),
		bizopsAggrParams.AggrEnd.Format(time.RFC3339), bizopsAggrParams.Region,
	).Rows()
	if err != nil {
		return err
	}

	defer func() {
		_ = rows.Close()
	}()

	err = convertRowsToCSV(rows, bizopsAggrParams.Writer)
	if err != nil {
		return err
	}
	return nil
}

func ingestAccountInfo(tx *gorm.DB, accountInfo []*datamodel.AccountInfo) error {
	var err error
	query := fmt.Sprintf(createTempAccountTable, accountTableName)
	err = tx.Exec(query).Error
	if err != nil {
		return err
	}
	// TODO: Batch size need to passed as param which needs to be fetched from env
	err = tx.Table(accountTableName).CreateInBatches(accountInfo, 1000).Error
	if err != nil {
		return err
	}
	return nil
}

func ingestGoogleContinents(tx *gorm.DB, continentMap map[string]string, region string) error {
	var err error
	err = tx.Exec(createGoogleContinents).Error
	if err != nil {
		return err
	}
	for key, val := range continentMap {
		if err := tx.Exec(insertGoogleContinent, key, val).Error; err != nil {
			return err
		}
	}
	err = tx.Exec(googleContinents, region).Error
	if err != nil {
		return err
	}
	return nil
}

func convertRowsToCSV(rows *sql.Rows, writer io.Writer) error {
	// Get Columns
	columns, err := rows.Columns()
	if err != nil {
		return err
	}

	colLen := len(columns)

	// Prepare values and pointers to values
	values := make([]interface{}, colLen)
	valuePtrs := make([]interface{}, colLen)
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	csvWriter := csv.NewWriter(writer)
	defer func() {
		csvWriter.Flush()
	}()

	// Write header
	err = csvWriter.Write(reportColumns)
	if err != nil {
		return err
	}

	// Process each row
	for rows.Next() {
		// Scan the row into the slice of interface{}
		if err := rows.Scan(valuePtrs...); err != nil {
			return err
		}

		rowInfo, err := convertInterfacesToStrings(values)
		if err != nil {
			return err
		}
		err = csvWriter.Write(rowInfo)
		if err != nil {
			return err
		}
	}

	// Check for errors from iterating over rows
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func convertInterfacesToStrings(rowInfo []interface{}) ([]string, error) {
	var result []string
	for _, row := range rowInfo {
		var value string
		switch v := row.(type) {
		case nil:
			value = ""
		case []byte:
			value = string(v)
		case string:
			value = v
		case int, int8, int16, int32, int64:
			value = fmt.Sprintf("%d", v)
		case uint, uint8, uint16, uint32, uint64:
			value = fmt.Sprintf("%d", v)
		case float32, float64:
			value = fmt.Sprintf("%f", v)
		case bool:
			value = fmt.Sprintf("%t", v)
		default:
			return nil, fmt.Errorf("unsupported data type: %T", v)
		}
		result = append(result, value)
	}
	return result, nil
}
