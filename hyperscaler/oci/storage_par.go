package oci

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const (
	defaultVSAPARNamePrefix = "vsa-image-par-"
)

// ObjectStoragePath holds namespace, bucket, and object parsed from an OCI path.
type ObjectStoragePath struct {
	Namespace  string
	BucketName string
	ObjectName string
}

// ParseObjectStoragePath parses "/n/{namespace}/b/{bucket}/o/{objectName}".
// Leading slash is optional; objectName may contain slashes.
func ParseObjectStoragePath(path string) (*ObjectStoragePath, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("object storage path is empty")
	}

	path = strings.TrimPrefix(path, "/")

	// Expected: n/{namespace}/b/{bucket}/o/{objectName...}
	parts := strings.SplitN(path, "/", 6)
	// Minimum: ["n", namespace, "b", bucket, "o", objectName]
	if len(parts) < 6 {
		return nil, fmt.Errorf("invalid Object Storage path: expected /n/{namespace}/b/{bucket}/o/{object}, got %q", path)
	}

	if parts[0] != "n" || parts[2] != "b" || parts[4] != "o" {
		return nil, fmt.Errorf("invalid Object Storage path: expected /n/{namespace}/b/{bucket}/o/{object}, got %q", path)
	}

	namespace := parts[1]
	bucket := parts[3]
	objectName := parts[5]

	if namespace == "" || bucket == "" || objectName == "" {
		return nil, fmt.Errorf("namespace, bucket, or object name is empty in path %q", path)
	}

	return &ObjectStoragePath{
		Namespace:  namespace,
		BucketName: bucket,
		ObjectName: objectName,
	}, nil
}

// GenerateVSAPAR creates a time-bounded PAR URL for the VSA image at vsaImagePath.
// vsaImagePath format: "/n/{namespace}/b/{bucket}/o/{objectName}"
// e.g. "/n/controlplane-nb/b/vsaimage/o/image-9-20-1P2.tgz"
//
// Returns an HTTPS URL that VLM uses as OntapUpgradeImagePath.
// PAR validity is controlled by VSA_SIGNED_URL_DURATION_HOURS (default 12h, max 24h).
func (sdkWrapper *ociSDKWrapper) GenerateVSAPAR(ctx context.Context, vsaImagePath string) (string, error) {
	oci := sdkWrapper.svc
	logger := util.GetLogger(ctx)

	if oci.AdminOCIService == nil {
		return "", fmt.Errorf("OCI object storage client not initialized")
	}

	parsed, err := ParseObjectStoragePath(vsaImagePath)
	if err != nil {
		return "", fmt.Errorf("parse vsaImagePath: %w", err)
	}

	durationHours := env.GetInt("VSA_SIGNED_URL_DURATION_HOURS", 12)
	duration := time.Duration(durationHours) * time.Hour
	if durationHours < 1 || durationHours > 24 {
		logger.Warn("VSA_SIGNED_URL_DURATION_HOURS is out of bounds, using default", "configuredHours", durationHours, "defaultHours", 12)
		duration = 12 * time.Hour
	}

	expiresAt := common.SDKTime{Time: time.Now().Add(duration)}
	parName := fmt.Sprintf("%s%d", defaultVSAPARNamePrefix, time.Now().UnixNano())

	req := objectstorage.CreatePreauthenticatedRequestRequest{
		NamespaceName: common.String(parsed.Namespace),
		BucketName:    common.String(parsed.BucketName),
		CreatePreauthenticatedRequestDetails: objectstorage.CreatePreauthenticatedRequestDetails{
			Name:        common.String(parName),
			ObjectName:  common.String(parsed.ObjectName),
			AccessType:  objectstorage.CreatePreauthenticatedRequestDetailsAccessTypeObjectread,
			TimeExpires: &expiresAt,
		},
	}

	resp, err := oci.AdminOCIService.objectStorageClient.CreatePreauthenticatedRequest(ctx, req)
	if err != nil {
		logger.Error("Failed to create OCI pre-authenticated request",
			"namespace", parsed.Namespace, "bucket", parsed.BucketName, "object", parsed.ObjectName, "error", err)
		return "", fmt.Errorf("create pre-authenticated request: %w", err)
	}
	if resp.RawResponse == nil {
		return "", fmt.Errorf("OCI returned nil response for PAR creation")
	}
	if resp.AccessUri == nil || *resp.AccessUri == "" {
		return "", fmt.Errorf("OCI returned empty AccessUri for PAR")
	}

	parURL := buildPARURL(oci.AdminOCIService.objectStorageClient.Host, *resp.AccessUri)
	logger.Info("Generated OCI VSA pre-authenticated request",
		"namespace", parsed.Namespace, "bucket", parsed.BucketName, "object", parsed.ObjectName,
		"durationHours", duration/time.Hour)
	return parURL, nil
}

// objectStorageHostFromRegion derives the standard OC1 Object Storage endpoint
func objectStorageHostFromRegion(region string) (string, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		return "", fmt.Errorf("region is empty")
	}

	r := common.StringToRegion(region)
	template := "https://objectstorage.{region}.{secondLevelDomain}"
	host := r.EndpointForTemplate("objectstorage", template)
	if host == "" {
		return "", fmt.Errorf("cannot derive object storage host for region %q", region)
	}
	return host, nil
}

// resolvePARObjectStorageHost returns the host used to assemble PAR download URLs
// from the OCI config provider region (e.g. https://objectstorage.us-ashburn-1.oraclecloud.com).
//
// Proxy mode requirement: the region from ociConfigProvider() must match the region
// where the VSA image bucket lives (the region implied by vsaImagePath / deployment).
// There is no separate Object Storage host override.
func resolvePARObjectStorageHost() (string, error) {
	configProvider, err := ociConfigProvider()
	if err != nil {
		return "", fmt.Errorf("oci config provider: %w", err)
	}

	region, err := configProvider.Region()
	if err != nil {
		return "", fmt.Errorf("oci config provider region: %w", err)
	}

	return objectStorageHostFromRegion(region)
}

// isAbsoluteAccessURI reports whether accessUri is already a full http(s) URL.
func isAbsoluteAccessURI(accessUri string) bool {
	return strings.HasPrefix(accessUri, "http://") || strings.HasPrefix(accessUri, "https://")
}

// buildPARURL joins the Object Storage host with the relative AccessUri from OCI.
// When accessUri is already an absolute URL, it is returned unchanged.
func buildPARURL(host, accessUri string) string {
	if isAbsoluteAccessURI(accessUri) {
		return accessUri
	}
	host = strings.TrimRight(host, "/")
	if !strings.HasPrefix(accessUri, "/") {
		accessUri = "/" + accessUri
	}
	return host + accessUri
}
