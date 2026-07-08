package oci

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/dns"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// ──────────────────────────────────────────────────────────────────────────────
//
// OCI DNS Service docs:
//   - GetRRSet:        https://docs.oracle.com/en-us/iaas/api/#/en/dns/20180115/RRSet/GetRRSet
//   - UpdateRRSet:     https://docs.oracle.com/en-us/iaas/api/#/en/dns/20180115/RRSet/UpdateRRSet
//   - DeleteRRSet:     https://docs.oracle.com/en-us/iaas/api/#/en/dns/20180115/RRSet/DeleteRRSet
//   - Go SDK reference: https://docs.oracle.com/en-us/iaas/tools/go/latest/dns/index.html
//
// Why this file exists:
//
// GCP's hyperscaler/google/cloud_dns.go uses three operations on the
// resourceRecordSets collection (Create / List+filter / Delete) to manage a
// single A record per node. OCI's DNS service exposes the same concept but
// addresses it at the RRSet level (a "resource record set" is all records of a
// given Domain + Type), so we use:
//
//	GCP                            | OCI equivalent
//	-------------------------------|-----------------------------------------
//	CreateResourceRecordSet (POST) | UpdateRRSet  — replaces RRSet with the
//	                               | one record we care about (idempotent;
//	                               | naturally upserts).
//	GetResourceRecordSet  (LIST)   | GetRRSet     — returns the items[] of
//	                               | the RRSet, or 404 if absent.
//	DeleteResourceRecordSet (DEL)  | DeleteRRSet  — removes the whole RRSet
//	                               | for a (Domain, Rtype) pair.
//
// All three calls take the zone OCID directly (env.OCIVsaDnsZoneOCID) and
// therefore do NOT require a ViewId, even for PRIVATE zones — see
// dns.UpdateRRSetRequest.ViewId: "Required when accessing a private zone by
// name."
//
// Returned shape:
//
// All three methods return *hyperscalermodels.CustomCloudDNSRecord — the same
// shape the GCP wrappers return — so the cross-hyperscaler helpers in
// ontap_provider.go and the activities layer can stay symmetric.
//
// ──────────────────────────────────────────────────────────────────────────────

const (
	// dnsRecordTypeA is the only RR type VCP ever writes via this layer.
	// VSA management LIFs are IPv4; we never mint CNAME/AAAA/TXT/etc.
	dnsRecordTypeA = "A"
)

// CreateOrUpdateDnsRecord upserts a single A record (recordName → ip) in the
// zone identified by zoneOCID. Mirrors GCP's CreateResourceRecordSet, with one
// behavioural difference forced by OCI semantics: OCI's UpdateRRSet REPLACES
// the entire RRSet for (domain, A), which means re-running the activity with
// the same args is naturally idempotent — re-runs do not append duplicates.
//
// Arguments:
//   - zoneOCID:   OCID of the DNS zone (env.OCIVsaDnsZoneOCID). Must be set.
//   - recordName: fully-qualified domain name to register (e.g.
//     "dns-1.deployment-foo.vsa.netapp.internal." — trailing dot
//     optional; OCI accepts both forms).
//   - ip:         IPv4 address (the management LIF) the record should point at.
//
// TTL is sourced from env.CloudDNSCacheTTL (shared with GCP). Scope is taken
// from env.OCIVsaDnsScope (defaults to "PRIVATE").
func (sdkWrapper *ociSDKWrapper) CreateOrUpdateDnsRecord(zoneOCID, recordName, ip string) (*hyperscalermodels.CustomCloudDNSRecord, error) {
	ociService := sdkWrapper.svc
	if err := validateDnsArgs(zoneOCID, recordName); err != nil {
		return nil, err
	}
	if ip == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("CreateOrUpdateDnsRecord: ip is required"))
	}

	logger := ociService.GetLogger()
	logger.Debugf("Calling UpdateRRSet — zoneOCID: %s, domain: %s, ip: %s",
		zoneOCID, recordName, ip)

	ttl := int(env.CloudDNSCacheTTL)
	req := dns.UpdateRRSetRequest{
		ZoneNameOrId: common.String(zoneOCID),
		Domain:       common.String(recordName),
		Rtype:        common.String(dnsRecordTypeA),
		Scope:        dns.UpdateRRSetScopeEnum(env.OCIVsaDnsScope),
		UpdateRrSetDetails: dns.UpdateRrSetDetails{
			Items: []dns.RecordDetails{
				{
					Domain: common.String(recordName),
					Rdata:  common.String(ip),
					Rtype:  common.String(dnsRecordTypeA),
					Ttl:    common.Int(ttl),
				},
			},
		},
	}

	resp, err := ociService.AdminOCIService.dnsClient.UpdateRRSet(ociService.Ctx, req)
	if err != nil {
		logger.Errorf("Failed to update DNS RRSet for domain %s in zone %s: %v",
			recordName, zoneOCID, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, err)
	}

	// UpdateRRSet returns a RecordCollection (not an RrSet) — same shape
	// ([]Record), just a different wrapper type. Convert from the items
	// slice directly so we don't depend on the wrapper.
	logger.Debugf("UpdateRRSet succeeded for domain %s in zone %s", recordName, zoneOCID)
	return convertOCIRecordsToCustomRecord(resp.Items, zoneOCID, recordName, ip, ttl), nil
}

// GetDnsRecord retrieves the A RRSet for (zoneOCID, recordName). Mirrors GCP's
// GetResourceRecordSet, including the (nil, nil) return on 404 so the caller
// can distinguish "not found" from real fetch errors and route to
// CreateOrUpdateDnsRecord on a miss.
func (sdkWrapper *ociSDKWrapper) GetDnsRecord(zoneOCID, recordName string) (*hyperscalermodels.CustomCloudDNSRecord, error) {
	ociService := sdkWrapper.svc
	if err := validateDnsArgs(zoneOCID, recordName); err != nil {
		return nil, err
	}

	logger := ociService.GetLogger()
	logger.Debugf("Calling GetRRSet — zoneOCID: %s, domain: %s", zoneOCID, recordName)

	req := dns.GetRRSetRequest{
		ZoneNameOrId: common.String(zoneOCID),
		Domain:       common.String(recordName),
		Rtype:        common.String(dnsRecordTypeA),
		Scope:        dns.GetRRSetScopeEnum(env.OCIVsaDnsScope),
	}

	resp, err := ociService.AdminOCIService.dnsClient.GetRRSet(ociService.Ctx, req)
	if err != nil {
		// Match the GCP semantics: 404 → (nil, nil). Lets _getOrCreateOCIDNSRecord
		// route a miss to the create path without leaking the OCI service error.
		if isOCIDnsNotFound(err) {
			logger.Debugf("RRSet not found for domain %s in zone %s (will create)",
				recordName, zoneOCID)
			return nil, nil
		}
		logger.Errorf("Failed to get DNS RRSet for domain %s in zone %s: %v",
			recordName, zoneOCID, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceFetchError, err)
	}

	if len(resp.Items) == 0 {
		// OCI sometimes returns an empty RRSet body (instead of 404) when the
		// last record of a set was deleted. Treat it the same as not-found so
		// the caller flips to the create path.
		logger.Debugf("RRSet exists but is empty for domain %s in zone %s (will create)",
			recordName, zoneOCID)
		return nil, nil
	}

	rec := resp.Items[0]
	out := &hyperscalermodels.CustomCloudDNSRecord{
		RecordName:  strOrDefault(rec.Domain, recordName),
		Type:        strOrDefault(rec.Rtype, dnsRecordTypeA),
		TTL:         int64(intOrDefault(rec.Ttl, int(env.CloudDNSCacheTTL))),
		Data:        strOrDefault(rec.Rdata, ""),
		ManagedZone: zoneOCID,
	}
	return out, nil
}

// DeleteDnsRecord removes the A RRSet for (zoneOCID, recordName). Mirrors GCP's
// DeleteResourceRecordSet, treating 404 as success so pool-delete is idempotent
// against partial-create rollbacks (e.g. CreateOrUpdate failed before the record
// was actually written).
func (sdkWrapper *ociSDKWrapper) DeleteDnsRecord(zoneOCID, recordName string) error {
	ociService := sdkWrapper.svc
	if err := validateDnsArgs(zoneOCID, recordName); err != nil {
		return err
	}

	logger := ociService.GetLogger()
	logger.Infof("Calling DeleteRRSet — zoneOCID: %s, domain: %s", zoneOCID, recordName)

	req := dns.DeleteRRSetRequest{
		ZoneNameOrId: common.String(zoneOCID),
		Domain:       common.String(recordName),
		Rtype:        common.String(dnsRecordTypeA),
		Scope:        dns.DeleteRRSetScopeEnum(env.OCIVsaDnsScope),
	}

	if _, err := ociService.AdminOCIService.dnsClient.DeleteRRSet(ociService.Ctx, req); err != nil {
		if isOCIDnsNotFound(err) {
			logger.Infof("RRSet already absent for domain %s in zone %s (delete is a no-op)",
				recordName, zoneOCID)
			return nil
		}
		logger.Errorf("Failed to delete DNS RRSet for domain %s in zone %s: %v",
			recordName, zoneOCID, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceDeprovisionError, err)
	}
	logger.Infof("DeleteRRSet succeeded for domain %s in zone %s", recordName, zoneOCID)
	return nil
}

// validateDnsArgs centralises the shared input checks so each public method
// returns the same shape of validation error.
func validateDnsArgs(zoneOCID, recordName string) error {
	if strings.TrimSpace(zoneOCID) == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("OCI DNS: zoneOCID is required (set env OCI_VSA_DNS_ZONE_OCID)"))
	}
	if strings.TrimSpace(recordName) == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("OCI DNS: recordName is required"))
	}
	return nil
}

// isOCIDnsNotFound returns true when the OCI service error is a 404. The OCI
// DNS service can surface "not found" as either:
//   - HTTP 404 (zone or RRSet truly absent)
//
// We only treat 404 as "not found" here — keeping the contract identical to
// the GCP `Get*` path which only returns nil when the LIST is empty.
func isOCIDnsNotFound(err error) bool {
	if serviceErr, ok := common.IsServiceError(err); ok {
		return serviceErr.GetHTTPStatusCode() == http.StatusNotFound
	}
	return false
}

// convertOCIRecordsToCustomRecord builds a CustomCloudDNSRecord from a
// []dns.Record (the items slice that both GetRRSetResponse.RrSet and
// UpdateRRSetResponse.RecordCollection expose). Individual record fields may
// be nil on partial responses (e.g. async propagation), so we fall back to
// the request-side values we know are correct.
func convertOCIRecordsToCustomRecord(items []dns.Record, zoneOCID, recordName, ip string, ttl int) *hyperscalermodels.CustomCloudDNSRecord {
	rec := hyperscalermodels.CustomCloudDNSRecord{
		RecordName:  recordName,
		Type:        dnsRecordTypeA,
		TTL:         int64(ttl),
		Data:        ip,
		ManagedZone: zoneOCID,
	}
	if len(items) > 0 {
		first := items[0]
		rec.RecordName = strOrDefault(first.Domain, rec.RecordName)
		rec.Type = strOrDefault(first.Rtype, rec.Type)
		rec.TTL = int64(intOrDefault(first.Ttl, ttl))
		rec.Data = strOrDefault(first.Rdata, rec.Data)
	}
	return &rec
}

// strOrDefault returns *p when non-nil, otherwise fallback. Local helper
// scoped to the DNS file — secret_management.go already has its own
// derefString with a different signature, so we deliberately avoid the
// shared name.
func strOrDefault(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

func intOrDefault(p *int, fallback int) int {
	if p == nil {
		return fallback
	}
	return *p
}
