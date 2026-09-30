package database

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupFailingTestDataStoreRepository(t *testing.T, failurePoint string) *DataStoreRepository {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)

	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: db,
	}), &gorm.Config{})
	assert.NoError(t, err)

	if failurePoint == "begin" {
		mock.ExpectBegin().WillReturnError(fmt.Errorf("failed to begin transaction"))
		return &DataStoreRepository{
			db: gormwrapper.New(gormDB),
		}
	}

	mock.ExpectBegin()

	// Define common mock expectations
	setupAccountInfo := func() {
		mock.ExpectExec("(?i)CREATE TEMP TABLE").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("(?i)INSERT INTO").WillReturnResult(sqlmock.NewResult(1, 1))
	}

	setupAggregatedUsage := func() {
		mock.ExpectExec("(?i)create temp table aggregated_usage_constrained").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	setupUsageStatistics := func() {
		mock.ExpectExec("(?i)create temp table usage_statistics").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	setupGCPContinents := func() {
		mock.ExpectExec("(?i)create temp table gcp_continents").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("(?i)INSERT INTO gcp_continents").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("(?i)create temp table google_continents").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	setupPoolUsage := func() {
		mock.ExpectExec("(?i)create  temp table pool_usage_calculated").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	switch failurePoint {
	case "account_info":
		mock.ExpectExec("(?i)CREATE TEMP TABLE").WillReturnError(fmt.Errorf("failed to ingest account info"))
	case "aggregated_usage":
		setupAccountInfo()
		mock.ExpectExec("(?i)create temp table aggregated_usage_constrained").WillReturnError(fmt.Errorf("failed to create aggregated usage"))
	case "usage_statistics":
		setupAccountInfo()
		setupAggregatedUsage()
		mock.ExpectExec("(?i)create temp table usage_statistics").WillReturnError(fmt.Errorf("failed to create usage statistics"))
	case "google_continents":
		setupAccountInfo()
		setupAggregatedUsage()
		setupUsageStatistics()
		mock.ExpectExec("(?i)create temp table gcp_continents").WillReturnError(fmt.Errorf("failed to create google continents"))
	case "pool_usage":
		setupAccountInfo()
		setupAggregatedUsage()
		setupUsageStatistics()
		setupGCPContinents()
		mock.ExpectExec("(?i)create temp table pool_usage_calculated").WillReturnError(fmt.Errorf("failed to create pool usage"))
	case "final_report":
		setupAccountInfo()
		setupAggregatedUsage()
		setupUsageStatistics()
		setupGCPContinents()
		setupPoolUsage()
		mock.ExpectQuery(".*").WillReturnError(fmt.Errorf("failed to query final report"))
	case "success":
		setupAccountInfo()
		setupAggregatedUsage()
		setupUsageStatistics()
		setupGCPContinents()
		setupPoolUsage()
		// Mock the final query with actual data
		rows := sqlmock.NewRows(reportColumns).
			AddRow("TEST_COMPONENT", "TEST_CUSTOMER", true, "COMMERCIAL", 1, 2,
				"2024-01-01", "2024-01-31", "Standard", "Daily",
				"Volume", "Block", "us-east1", "us-east1", "us-west1",
				"NA", "NA", 100.0, 200.0, 300.0, 400.0, 500.0,
				600.0, 700.0, 800.0, 900.0, 1000.0, 1100.0,
				1200.0, 1300.0, 1400.0)

		mock.ExpectQuery(".*").WillReturnRows(rows)
	}

	mock.ExpectCommit()

	return &DataStoreRepository{
		db: gormwrapper.New(gormDB),
	}
}

func TestDataStoreRepository_AggregateUsageForBizOps(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	bizopsAggrParams := &datamodel.BizOpsAggregateParams{
		AccountsInfo: []*datamodel.AccountInfo{
			{AccountID: "123", UserName: "TEST_CUSTOMER", IsActive: true},
		},
		ContinentMap: map[string]string{
			"us": "northamerica",
		},
		Region:    "us-east1",
		AggrStart: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		AggrEnd:   time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
		Writer:    &buf,
	}

	// As part of success test we are also verifying CSV output
	// as the function is writing to the provided writer
	// In other tests we are only verifying error handling
	// by mocking DB calls to simulate failures at different points
	// and not checking CSV output
	t.Run("Success", func(t *testing.T) {
		mockRepo := setupFailingTestDataStoreRepository(t, "success")
		err := mockRepo.AggregateUsageForBizOps(ctx, bizopsAggrParams)
		assert.NoError(t, err)
		// Check if writer has data
		assert.Greater(t, buf.Len(), 0, "Writer should contain data")
		// Optionally check CSV header
		csvData := buf.String()
		assert.Contains(t, csvData, "COMPONENT")
		assert.Contains(t, csvData, "CUSTOMER_ID")
		assert.Contains(t, csvData, "CHARGEABILITY")
		assert.Contains(t, csvData, "TEST_CUSTOMER")
	})
	// For below tests we are mocking DB calls to simulate failures at different points
	t.Run("Failure - Database Begin Error", func(t *testing.T) {
		mockRepo := setupFailingTestDataStoreRepository(t, "begin")
		err := mockRepo.AggregateUsageForBizOps(ctx, bizopsAggrParams)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to begin transaction")
	})
	t.Run("Failure - Ingest Account Info Error", func(t *testing.T) {
		mockRepo := setupFailingTestDataStoreRepository(t, "account_info")
		err := mockRepo.AggregateUsageForBizOps(ctx, bizopsAggrParams)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to ingest account info")
	})
	t.Run("Failure - Ingest Aggregated Usage Error", func(t *testing.T) {
		mockRepo := setupFailingTestDataStoreRepository(t, "aggregated_usage")
		err := mockRepo.AggregateUsageForBizOps(ctx, bizopsAggrParams)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create aggregated usage")
	})
	t.Run("Failure - Ingest Usage Statistics Error", func(t *testing.T) {
		mockRepo := setupFailingTestDataStoreRepository(t, "usage_statistics")
		err := mockRepo.AggregateUsageForBizOps(ctx, bizopsAggrParams)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create usage statistics")
	})
	t.Run("Failure - Ingest Continent Error", func(t *testing.T) {
		mockRepo := setupFailingTestDataStoreRepository(t, "google_continents")
		err := mockRepo.AggregateUsageForBizOps(ctx, bizopsAggrParams)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create google continents")
	})
	t.Run("Failure - Ingest Pool Usage Statistics Error", func(t *testing.T) {
		mockRepo := setupFailingTestDataStoreRepository(t, "pool_usage")
		err := mockRepo.AggregateUsageForBizOps(ctx, bizopsAggrParams)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create pool usage")
	})
	t.Run("Failure - Final Report", func(t *testing.T) {
		mockRepo := setupFailingTestDataStoreRepository(t, "final_report")
		err := mockRepo.AggregateUsageForBizOps(ctx, bizopsAggrParams)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to query final report")
	})
}

func TestDataStoreRepository_AggregateUsageForBizOps_ExpertModeServiceLevelSKUs(t *testing.T) {
	extremePool := "when puc.resource_type = 'VOLUME_POOL_REGIONAL_HA' and puc.service_level = 'EXTREME' then 'netapp.googleapis.com/pool/Allocation/Extreme'"
	premiumPool := "when puc.resource_type = 'VOLUME_POOL_REGIONAL_HA' and puc.service_level = 'PREMIUM' then 'netapp.googleapis.com/pool/Allocation/Premium'"
	flexRegionalPool := "when puc.resource_type = 'VOLUME_POOL_REGIONAL_HA' then 'netapp.googleapis.com/pool/Allocation/Flex/Regional/Unified'"

	for _, clause := range []string{extremePool, premiumPool} {
		assert.Contains(t, finalReport, clause)
		assert.Less(t, strings.Index(finalReport, clause), strings.Index(finalReport, flexRegionalPool))
	}
	assert.NotContains(t, finalReport, "netapp.googleapis.com/volume/Allocation/Extreme")
	assert.NotContains(t, finalReport, "netapp.googleapis.com/volume/Allocation/Premium")
}

func TestDryRun_BizOpsSKUForSingleAndMultiTenant(t *testing.T) {
	tests := []struct {
		name         string
		tenancy      string
		resourceType string
		serviceLevel string
		wantSKU      string
	}{
		{name: "single tenant zonal volume", tenancy: "ST", resourceType: "VOLUME", serviceLevel: "unified", wantSKU: "netapp.googleapis.com/volume/Allocation/Flex/Zonal/Unified"},
		{name: "single tenant zonal pool", tenancy: "ST", resourceType: "VOLUME_POOL", serviceLevel: "unified", wantSKU: "netapp.googleapis.com/pool/Allocation/Flex/Zonal/Unified"},
		{name: "single tenant regional volume", tenancy: "ST", resourceType: "VOLUME_REGIONAL_HA", serviceLevel: "unified", wantSKU: "netapp.googleapis.com/volume/Allocation/Flex/Regional/Unified"},
		{name: "single tenant regional pool", tenancy: "ST", resourceType: "VOLUME_POOL_REGIONAL_HA", serviceLevel: "unified", wantSKU: "netapp.googleapis.com/pool/Allocation/Flex/Regional/Unified"},
		{name: "multi tenant extreme volume stays flex regional", tenancy: "MT", resourceType: "VOLUME_REGIONAL_HA", serviceLevel: "EXTREME", wantSKU: "netapp.googleapis.com/volume/Allocation/Flex/Regional/Unified"},
		{name: "multi tenant premium volume stays flex regional", tenancy: "MT", resourceType: "VOLUME_REGIONAL_HA", serviceLevel: "PREMIUM", wantSKU: "netapp.googleapis.com/volume/Allocation/Flex/Regional/Unified"},
		{name: "multi tenant extreme pool", tenancy: "MT", resourceType: "VOLUME_POOL_REGIONAL_HA", serviceLevel: "EXTREME", wantSKU: "netapp.googleapis.com/pool/Allocation/Extreme"},
		{name: "multi tenant premium pool", tenancy: "MT", resourceType: "VOLUME_POOL_REGIONAL_HA", serviceLevel: "PREMIUM", wantSKU: "netapp.googleapis.com/pool/Allocation/Premium"},
		{name: "zonal pool extreme stays flex zonal", tenancy: "MT", resourceType: "VOLUME_POOL", serviceLevel: "EXTREME", wantSKU: "netapp.googleapis.com/pool/Allocation/Flex/Zonal/Unified"},
		{name: "historical extreme volume stays flex zonal", tenancy: "MT", resourceType: "VOLUME", serviceLevel: "EXTREME", wantSKU: "netapp.googleapis.com/volume/Allocation/Flex/Zonal/Unified"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reportedServiceLevelSKU(tt.resourceType, tt.serviceLevel)
			assert.Equal(t, tt.wantSKU, got)
			t.Logf("%s %s service_level=%s -> %s", tt.tenancy, tt.resourceType, tt.serviceLevel, got)
		})
	}
}

// reportedServiceLevelSKU applies the finalReport service_level CASE in order.
func reportedServiceLevelSKU(resourceType, serviceLevel string) string {
	end := strings.Index(finalReport, "end as service_level")
	if end < 0 {
		return ""
	}
	block := finalReport[:end]
	start := strings.LastIndex(block, "case")
	if start < 0 {
		return ""
	}
	for _, line := range strings.Split(block[start:end], "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "when "):
			if serviceLevelWhenMatches(line, resourceType, serviceLevel) {
				return thenLiteral(line)
			}
		case strings.HasPrefix(line, "else "):
			return thenLiteral(line)
		}
	}
	return ""
}

func serviceLevelWhenMatches(line, resourceType, serviceLevel string) bool {
	if strings.Contains(line, "puc.service_level = '") {
		if serviceLevel != quotedAfter(line, "puc.service_level = '") {
			return false
		}
	}
	if strings.Contains(line, "puc.resource_type in (") {
		return strings.Contains(quotedList(line, "puc.resource_type in (", ")"), "'"+resourceType+"'")
	}
	if strings.Contains(line, "puc.resource_type = '") {
		return quotedAfter(line, "puc.resource_type = '") == resourceType
	}
	return false
}

func thenLiteral(line string) string {
	_, after, ok := strings.Cut(line, " then ")
	if !ok {
		return ""
	}
	after = strings.TrimSpace(strings.TrimSuffix(after, ","))
	return strings.Trim(after, "'")
}

func quotedAfter(line, marker string) string {
	rest := line[strings.Index(line, marker)+len(marker):]
	return rest[:strings.Index(rest, "'")]
}

func quotedList(line, startMarker, endMarker string) string {
	rest := line[strings.Index(line, startMarker)+len(startMarker):]
	return rest[:strings.Index(rest, endMarker)]
}

func TestDataStoreRepository_AggregateUsageForBizOps_ReplicationTypeLabels(t *testing.T) {
	intraZoneClause := "when puc.resource_type = 'VOLUME_REPLICATION_RELATIONSHIP' and puc.replication_type = 'INTRA_ZONE_REPLICATION' then 'Intra Zone Replication'"
	interZoneClause := "when puc.resource_type = 'VOLUME_REPLICATION_RELATIONSHIP' and puc.replication_type = 'INTER_ZONE_REPLICATION' then 'Inter Zone Replication'"
	genericReplicationClause := "when puc.resource_type in ('VOLUME_REPLICATION_RELATIONSHIP', 'SDS_VOLUME_REPLICATION_RELATIONSHIP') then 'Cross Region Replication'"

	assert.Contains(t, finalReport, intraZoneClause)
	assert.Contains(t, finalReport, interZoneClause)
	assert.Contains(t, finalReport, genericReplicationClause)
	assert.Less(t, strings.Index(finalReport, intraZoneClause), strings.Index(finalReport, genericReplicationClause))
	assert.Less(t, strings.Index(finalReport, interZoneClause), strings.Index(finalReport, genericReplicationClause))
}

func TestConvertRowsToCSV(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer func() {
		_ = db.Close()
	}()

	// Create mock rows with expected columns
	rows := sqlmock.NewRows(reportColumns).
		AddRow("COMPONENT1", "CUST123", true, "COMMERCIAL", 5, 10, "2024-01-01", "2024-01-31",
			"Standard", "Daily", "Volume", "Block", "us-east1", "us-east1",
			"us-west1", "NA", "NA", 100.5, 200.5, 300.5, 400.5, 500.5,
			600.5, 700.5, 800.5, 900.5, 1000.5, 1100.5, 1200.5, 1300.5, 1400.5)

	mock.ExpectQuery(".*").WillReturnRows(rows)

	// Execute query to get rows
	sqlRows, err := db.Query("SELECT * FROM test")
	assert.NoError(t, err)

	// Test CSV conversion
	var buf bytes.Buffer
	err = convertRowsToCSV(sqlRows, &buf)
	assert.NoError(t, err)

	// Verify CSV output
	csvData := buf.String()
	assert.Contains(t, csvData, "COMPONENT")
	assert.Contains(t, csvData, "COMPONENT1")
	assert.Contains(t, csvData, "CUST123")
}

func TestConvertRowsToCSV_Errors(t *testing.T) {
	t.Run("Column Error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		assert.NoError(t, err)
		defer func() {
			_ = db.Close()
		}()

		// Mock rows that will return error on Columns() call
		mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows(nil))

		sqlRows, err := db.Query("SELECT * FROM test")
		assert.NoError(t, err)

		// Force column error by closing rows before calling convertRowsToCSV
		err = sqlRows.Close()
		assert.NoError(t, err)

		var buf bytes.Buffer
		err = convertRowsToCSV(sqlRows, &buf)
		assert.Error(t, err)
	})

	t.Run("Rows Error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		assert.NoError(t, err)
		defer func() {
			_ = db.Close()
		}()

		// Create rows that will have an error
		rows := sqlmock.NewRows(reportColumns).
			AddRow("COMPONENT1", "CUST123", true, "FREE_TRIAL", 5, 10, "2024-01-01", "2024-01-31",
				"Standard", "Daily", "Volume", "Block", "us-east1", "us-east1",
				"us-west1", "NA", "NA", 100.5, 200.5, 300.5, 400.5, 500.5,
				600.5, 700.5, 800.5, 900.5, 1000.5, 1100.5, 1200.5, 1300.5, 1400.5).
			RowError(0, fmt.Errorf("row iteration error"))

		mock.ExpectQuery(".*").WillReturnRows(rows)

		sqlRows, err := db.Query("SELECT * FROM test")
		assert.NoError(t, err)

		var buf bytes.Buffer
		err = convertRowsToCSV(sqlRows, &buf)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "row iteration error")
	})
}

func TestConvertInterfacesToStrings(t *testing.T) {
	t.Run("Success - All Types", func(t *testing.T) {
		input := []interface{}{
			nil,
			[]byte("bytes"),
			"string",
			int(1),
			int64(2),
			float64(3.14),
			true,
			uint(4),
			uint64(5),
		}

		result, err := convertInterfacesToStrings(input)
		assert.NoError(t, err)
		assert.Equal(t, []string{"", "bytes", "string", "1", "2", "3.140000", "true", "4", "5"}, result)
	})

	t.Run("Error - Unsupported Type", func(t *testing.T) {
		input := []interface{}{
			"valid",
			struct{}{}, // unsupported type
		}

		result, err := convertInterfacesToStrings(input)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "unsupported data type")
	})
}
