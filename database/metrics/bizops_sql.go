package database

const (
	accountTableName = "customer_account"
)

const (
	createTempAccountTable = `create  temp table %s (
account_id VARCHAR(255) NOT NULL,
user_name VARCHAR (255) NOT NULL,
is_active BOOLEAN NOT NULL
);
`
	createGoogleContinents = `create  temp table gcp_continents (
region VARCHAR(255) NOT NULL,
continent VARCHAR (255) NOT NULL
) ;
`
	insertGoogleContinent = `insert into gcp_continents values ($1, $2);`

	aggregatedUsageConstrained = `create temp table aggregated_usage_constrained
       as
 select
 	id,
 	aggregation_start,
 	aggregation_end,
 	resource_uuid,
 	resource_type,
	measured_type,
	state,
 	quantity,
 	account_id,
 	service_level,
 	source_region,
 	coalesce(destination_region, '') as destination_region,
    coalesce(volume_style, '') as volume_style,
	coalesce(replication_type, '') as replication_type,
	billing_mode
 from
 	aggregated_usages
 where
 	id is not null and
 	aggregation_start >= $1::timestamp at time zone 'UTC' and 
 	aggregation_end <= $2::timestamp at time zone 'UTC' and
 	(region_name = $3 or region_name = '') and
 	vendor_customer_id is not null and
 	resource_uuid is not null and
 	measured_type is not null and
 	quantity is not null and
 	resource_type in (
     'VOLUME', 
     'VOLUME_POOL', 
     'VOLUME_REPLICATION_RELATIONSHIP', 
     'VOLUME_REGIONAL_HA',
     'VOLUME_POOL_REGIONAL_HA',
     'BACKUP'
     ) and
 	service_level is not null
 ;
 `

	usageStatistics = `create temp table usage_statistics
 as
 select
 	count(distinct resource_uuid) as resource_count,
 	account_id,
 	service_level,
 	resource_type,
 	destination_region,
	volume_style,
	billing_mode
 from
 	aggregated_usage_constrained
 group by
 	account_id,
 	resource_type,
 	service_level,
 	destination_region,
	volume_style,
	billing_mode
 ;
 `
	googleContinents = `create temp table google_continents
	as
	SELECT
	   account_id,
	   service_level,
	   resource_type,
	   source_region,
	   billing_mode,
	   $1 AS destination_region,
	
	   -- Source Continent
	   CASE
	       WHEN POSITION('-' IN source_region) > 0 THEN
	           CASE
	               WHEN SUBSTRING(source_region FROM 1 FOR POSITION('-' IN source_region) - 1) = 'asia'
	                    AND SUBSTRING(source_region FROM POSITION('-' IN source_region) + 1) = 'southeast2' THEN 'indonesia'
	               ELSE (
	                   SELECT continent
	                   FROM gcp_continents
	                   WHERE region = SUBSTRING(source_region FROM 1 FOR POSITION('-' IN source_region) - 1)
	               )
	           END
	       ELSE ''
	   END AS source_continent,
	
	   -- Destination Continent
	   CASE
	       WHEN POSITION('-' IN $1) > 0 THEN
	           CASE
	               WHEN SUBSTRING($1 FROM 1 FOR POSITION('-' IN $1) - 1) = 'asia'
	                    AND SUBSTRING($1 FROM POSITION('-' IN $1) + 1) = 'southeast2' THEN 'indonesia'
	               ELSE (
	                   SELECT continent
	                   FROM gcp_continents
	                   WHERE region = SUBSTRING($1 FROM 1 FOR POSITION('-' IN $1) - 1)
	               )
	           END
	       ELSE ''
	   END AS destination_continent
	
	FROM
	   aggregated_usage_constrained
	WHERE
	   measured_type = 'XREGION_REPLICATION_TOTAL_TRANSFER_BYTES'
	GROUP BY
	   account_id,
	   service_level,
	   resource_type,
	   source_region,
	   destination_region,
	   billing_mode;
	`

	poolUsageCalculated = `create temp table pool_usage_calculated
 as
 select
 	account_id,
 	destination_region,
 	service_level,
 	source_region,
 	resource_type,
 	volume_style,
	replication_type,
	billing_mode,
 	sum(hourly_allocated_quantity) / 1024 as total_allocated_gibh,
 	case
 		when resource_type in ('VOLUME', 'VOLUME_REGIONAL_HA')  then sum(hourly_logical_quantity) / 1024 / 86400*3600
 		when resource_type in ('VOLUME_POOL', 'VOLUME_POOL_REGIONAL_HA') then sum(hourly_pool_logical_quantity) / 1024 / 86400*3600
 		else '0'
 	end as total_avg_gib_used,
 	case
 		when resource_type in ('VOLUME', 'VOLUME_REGIONAL_HA') then sum(hourly_logical_quantity) / 1024
 		when resource_type in ('VOLUME_POOL', 'VOLUME_POOL_REGIONAL_HA') then sum(hourly_pool_logical_quantity) / 1024
 		else '0'
 	end as total_gibh_used,
 	sum(hourly_transfer_bytes) / 1024 as total_transfer_bytes_crr,
 	sum(hourly_backup_total_gibh_used) / 1024 as backup_total_gibh_used,
 	sum(hourly_backup_enabled_volume_allocated_size) / 1024 as backup_enabled_volume_allocated_size_total_gibh,
 	sum(hourly_restore_transferred_bytes) / 1024 as backup_restore_transferred_bytes_used,
 	sum(pool_throughput_mibps) as total_pool_throughput_mibps,
 	sum(pool_billable_iops) as total_pool_billable_iops,
 	sum(agg.submitted_quantity) as submitted_quantity,
 	sum(hourly_cool_tier_bytes) / 1024 as cool_tier_gibh_used,
 	sum(hourly_standard_tier_bytes) / 1024 as standard_tier_gibh_used,
 	sum(hourly_cool_tier_read_bytes) / 1024 as cool_tier_read_gibh_used,
 	sum(hourly_cool_tier_write_bytes) / 1024 as cool_tier_write_gibh_used,
 	sum(hourly_cross_region_backup_transferred_bytes) / 1024 as cross_region_backup_transferred_bytes
 from (
 	select
 		account_id,
 		destination_region,
 		service_level,
		source_region,
		resource_type,
		volume_style,
		replication_type,
		billing_mode,
		sum(case when(measured_type = 'LOGICAL_SIZE') then quantity else 0 end) as hourly_logical_quantity,
		sum(case when(measured_type in ('ALLOCATED_SIZE' ,'POOL_ALLOCATED_SIZE')) then quantity else 0 end) as hourly_allocated_quantity,
		sum(case when(measured_type = 'TOTAL_LOGICAL_SIZE') then quantity else 0 end) as hourly_pool_logical_quantity,
 		sum(case when(measured_type = 'XREGION_REPLICATION_TOTAL_TRANSFER_BYTES') then quantity else 0 end) as hourly_transfer_bytes,
 		sum(case when(measured_type = 'VOLUME_BACKUP_SIZE') then quantity else 0 end) as hourly_backup_total_gibh_used,
 		sum(case when(measured_type = 'BACKUP_ENABLED_VOLUME_ALLOCATED_SIZE') then quantity else 0 end) as hourly_backup_enabled_volume_allocated_size,
 		sum(case when(measured_type = 'CBS_VOLUME_OPERATION_RESTORE_TRANSFERRED_BYTES') then quantity else 0 end) as hourly_restore_transferred_bytes,
		sum(case when(measured_type = 'POOL_CAPACITY_TIER_LOGICAL_FOOTPRINT') then quantity else 0 end) as hourly_cool_tier_bytes,
		sum(case when(measured_type = 'POOL_HOT_TIER_PROVISIONED_SIZE') then quantity else 0 end) as hourly_standard_tier_bytes,
		sum(case when(measured_type = 'COOL_TIER_DATA_READ_SIZE_RAW' AND resource_type IN ('VOLUME_POOL', 'VOLUME_POOL_REGIONAL_HA')) then quantity else 0 end) as hourly_cool_tier_read_bytes,
		sum(case when(measured_type = 'COOL_TIER_DATA_WRITE_SIZE_RAW' AND resource_type IN ('VOLUME_POOL', 'VOLUME_POOL_REGIONAL_HA')) then quantity else 0 end) as hourly_cool_tier_write_bytes,
 		sum(case when(measured_type = 'CBS_CROSS_REGION_VOLUME_BACKUP_TRANSFER_BYTES' or measured_type = 'CBS_CROSS_REGION_VOLUME_RESTORE_TRANSFER_BYTES') then quantity else 0 end) as hourly_cross_region_backup_transferred_bytes,
 		sum(case when(measured_type = 'POOL_TOTAL_THROUGHPUT_MIBPS') then quantity else 0 end) as pool_throughput_mibps,
    	sum(case when(measured_type = 'POOL_TOTAL_IOPS') then quantity else 0 end) as pool_billable_iops,
 		sum(case when(state = 1) then quantity else 0 end) as submitted_quantity
 	from aggregated_usage_constrained
	group by account_id, aggregation_start, destination_region, service_level, source_region, resource_type, volume_style, replication_type, billing_mode
 	) as agg
 group by
 	account_id,
 	destination_region,
 	service_level,
 	source_region,
 	resource_type,
 	volume_style,
	replication_type,
	billing_mode;
 `

	finalReport = `
 select
 	'NetApp Volumes' as component,
 	aa.user_name as customer_id,
 	case
 		when aa.is_active = 'false' then 'FALSE'
 		else 'TRUE'
 	end as is_active,
 	case
 		when puc.account_id is null then ''
		else puc.billing_mode
 	end as chargeability,
 	case
 		when puc.resource_type in ('VOLUME_POOL', 'VOLUME_POOL_REGIONAL_HA') then us.resource_count
 		else 0
 	end as num_pools,
     case
 		when puc.resource_type in ('VOLUME', 'VOLUME_REGIONAL_HA') then us.resource_count
 		when puc.resource_type in ('BACKUP') then us.resource_count
 		else 0
 	end as num_volums,
     $1 as report_start,
     $2 as report_end,
 	case
 		when puc.resource_type = 'VOLUME_POOL_REGIONAL_HA' and puc.service_level = 'EXTREME' then 'netapp.googleapis.com/pool/Allocation/Extreme'
 		when puc.resource_type = 'VOLUME_POOL_REGIONAL_HA' and puc.service_level = 'PREMIUM' then 'netapp.googleapis.com/pool/Allocation/Premium'
 		when puc.resource_type = 'VOLUME_POOL' then 'netapp.googleapis.com/pool/Allocation/Flex/Zonal/Unified'
 	    
 	    when puc.resource_type = 'VOLUME_POOL_REGIONAL_HA' then 'netapp.googleapis.com/pool/Allocation/Flex/Regional/Unified'
 	    
 	    when puc.resource_type = 'VOLUME' then 'netapp.googleapis.com/volume/Allocation/Flex/Zonal/Unified'
 	    
 	    when puc.resource_type = 'VOLUME_REGIONAL_HA' then 'netapp.googleapis.com/volume/Allocation/Flex/Regional/Unified'
 	    
 	    when puc.resource_type = 'BACKUP' then 'netapp.googleapis.com/volume/Allocation/Flex/Unified'
 	    
 	    when puc.resource_type in ('VOLUME_REPLICATION_RELATIONSHIP') then ''
 		else 'N/A'
 	end as service_level,
     case
 		when puc.resource_type in ('VOLUME_REPLICATION_RELATIONSHIP') then
 			case
 				when puc.service_level = '1' then '10 Minutely'
 				when puc.service_level = '2' then 'Hourly'
 				when puc.service_level = '3' then 'Daily'
 				when puc.service_level = '4' then 'Daily'
 				when puc.service_level = '5' then 'Daily'
 				else ''
 			end
 		else ''
 	end as CRR_Frequency,
 	case
 	    when puc.resource_type = 'VOLUME_REPLICATION_RELATIONSHIP' and puc.replication_type = 'MIGRATION' then 'Onprem Migration'
 	    when puc.resource_type = 'VOLUME_REPLICATION_RELATIONSHIP' and puc.replication_type = 'ONPREM_REPLICATION' then 'Onprem Replication'
 	    when puc.resource_type = 'VOLUME_REPLICATION_RELATIONSHIP' and puc.replication_type = 'INTRA_ZONE_REPLICATION' then 'Intra Zone Replication'
 	    when puc.resource_type = 'VOLUME_REPLICATION_RELATIONSHIP' and puc.replication_type = 'INTER_ZONE_REPLICATION' then 'Inter Zone Replication'
 	    when puc.resource_type in ('VOLUME_REPLICATION_RELATIONSHIP', 'SDS_VOLUME_REPLICATION_RELATIONSHIP') then 'Cross Region Replication'
 	    when puc.resource_type in ('BACKUP') and (puc.destination_region is not NULL and puc.destination_region != '') then 'Backup'
 		when puc.resource_type in ('VOLUME', 'VOLUME_REGIONAL_HA') then 'Volume'
 		when puc.resource_type in ('VOLUME_POOL', 'VOLUME_POOL_REGIONAL_HA') then 'Pool'
 		else puc.resource_type
 	end as Resource_Type,
    case
        when puc.volume_style = 'FLEXVOL' and puc.resource_type = 'VOLUME' then 'RegularVolume'
        when puc.volume_style = 'FLEXGROUP' and puc.resource_type = 'VOLUME' then 'LargeVolume'
        when puc.volume_style = 'FLEXCACHE' and puc.resource_type = 'VOLUME' then 'CacheVolume'
        when puc.resource_type in ('VOLUME', 'VOLUME_REGIONAL_HA') then 'RegularVolume'
        else puc.volume_style
    end as Volume_Type,
     $3 as region,
 	puc.source_region as source_region,
 	puc.destination_region as backup_region,
 	gc.source_continent as crr_source_continent,
 	gc.destination_continent as crr_dest_continent,
 	puc.total_transfer_bytes_crr as total_bytes_transferred_crr_gib,
 	max(case when puc.resource_type in ('VOLUME_POOL', 'VOLUME_POOL_REGIONAL_HA') then puc.total_allocated_gibh else 0 end) as total_pool_allocated_gibh,
 	max(case when puc.resource_type in ('VOLUME', 'VOLUME_REGIONAL_HA') then puc.total_avg_gib_used else 0 end) as total_avg_gib_used,
 	puc.backup_total_gibh_used as total_backup_gibh,
 	puc.backup_enabled_volume_allocated_size_total_gibh as total_backup_management_usage_gibh,
 	puc.cross_region_backup_transferred_bytes as total_cross_region_backup_transferred_gib,
 	puc.backup_restore_transferred_bytes_used as total_restore_transferred_bytes_gib,
 	puc.cool_tier_gibh_used as total_pool_cool_tier_gibh,
 	puc.standard_tier_gibh_used as total_pool_standard_tier_gibh,
 	puc.cool_tier_read_gibh_used as total_pool_cool_tier_read_size_gibh,
 	puc.cool_tier_write_gibh_used as total_pool_cool_tier_write_size_gibh,
 	puc.total_pool_throughput_mibps as pool_total_throughput_mibps,
    puc.total_pool_billable_iops as pool_total_billable_iops,
 	case
         when puc.submitted_quantity is not NULL then puc.submitted_quantity
         else 0
     end as actual_submitted_quantity
 from
 	(select user_name, account_id, is_active from customer_account) aa
 	inner join pool_usage_calculated puc on aa.account_id = puc.account_id
	left join usage_statistics us on aa.account_id = us.account_id and puc.resource_type = us.resource_type and puc.service_level = us.service_level and puc.volume_style = us.volume_style and puc.destination_region =us.destination_region and puc.billing_mode IS NOT DISTINCT FROM us.billing_mode
	left join google_continents gc on aa.account_id = gc.account_id and puc.resource_type = gc.resource_type and puc.service_level = gc.service_level and puc.source_region = gc.source_region and puc.billing_mode IS NOT DISTINCT FROM gc.billing_mode
 group by
 	aa.user_name,
 	aa.is_active,
 	us.resource_count,
 	puc.account_id,
 	puc.destination_region,
 	puc.service_level,
 	puc.resource_type,
 	puc.volume_style,
 	puc.replication_type,
	puc.source_region,
	puc.billing_mode,
 	puc.total_transfer_bytes_crr,
 	puc.total_allocated_gibh,
 	puc.total_avg_gib_used,
 	puc.backup_total_gibh_used,
 	puc.backup_enabled_volume_allocated_size_total_gibh,
 	puc.cross_region_backup_transferred_bytes,
 	puc.backup_restore_transferred_bytes_used,
 	puc.cool_tier_gibh_used,
 	puc.standard_tier_gibh_used,
 	puc.cool_tier_read_gibh_used,
 	puc.cool_tier_write_gibh_used,
 	puc.total_pool_throughput_mibps,
    puc.total_pool_billable_iops,
 	puc.submitted_quantity,
 	gc.source_continent,
 	gc.destination_continent
 ;
 `
)
