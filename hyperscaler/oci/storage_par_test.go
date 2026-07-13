package oci

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// ---------------------------------------------------------------------------
// TestParseObjectStoragePath
// ---------------------------------------------------------------------------

func TestParseObjectStoragePath(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		want      *ObjectStoragePath
		wantErr   bool
		errSubstr string
	}{
		{
			name: "valid path with leading slash",
			path: "/n/controlplane-nb/b/vsaimage/o/image-9-20-1P2.tgz",
			want: &ObjectStoragePath{
				Namespace:  "controlplane-nb",
				BucketName: "vsaimage",
				ObjectName: "image-9-20-1P2.tgz",
			},
		},
		{
			name: "valid path without leading slash",
			path: "n/controlplane-nb/b/vsaimage/o/image-9-20-1P2.tgz",
			want: &ObjectStoragePath{
				Namespace:  "controlplane-nb",
				BucketName: "vsaimage",
				ObjectName: "image-9-20-1P2.tgz",
			},
		},
		{
			name: "object name with slashes",
			path: "/n/ns1/b/bucket1/o/path/to/nested/file.tar.gz",
			want: &ObjectStoragePath{
				Namespace:  "ns1",
				BucketName: "bucket1",
				ObjectName: "path/to/nested/file.tar.gz",
			},
		},
		{
			name: "path with surrounding whitespace",
			path: "  /n/ns1/b/bucket1/o/obj.tgz  ",
			want: &ObjectStoragePath{
				Namespace:  "ns1",
				BucketName: "bucket1",
				ObjectName: "obj.tgz",
			},
		},
		{
			name:      "empty string",
			path:      "",
			wantErr:   true,
			errSubstr: "object storage path is empty",
		},
		{
			name:      "whitespace only",
			path:      "   ",
			wantErr:   true,
			errSubstr: "object storage path is empty",
		},
		{
			name:      "too few parts",
			path:      "/n/ns1/b/bucket1",
			wantErr:   true,
			errSubstr: "invalid Object Storage path",
		},
		{
			name:      "wrong marker at position 0",
			path:      "/x/ns1/b/bucket1/o/obj.tgz",
			wantErr:   true,
			errSubstr: "invalid Object Storage path",
		},
		{
			name:      "wrong marker at position 2",
			path:      "/n/ns1/x/bucket1/o/obj.tgz",
			wantErr:   true,
			errSubstr: "invalid Object Storage path",
		},
		{
			name:      "wrong marker at position 4",
			path:      "/n/ns1/b/bucket1/x/obj.tgz",
			wantErr:   true,
			errSubstr: "invalid Object Storage path",
		},
		{
			name:      "empty namespace",
			path:      "/n//b/bucket1/o/obj.tgz",
			wantErr:   true,
			errSubstr: "namespace, bucket, or object name is empty",
		},
		{
			name:      "empty bucket",
			path:      "/n/ns1/b//o/obj.tgz",
			wantErr:   true,
			errSubstr: "namespace, bucket, or object name is empty",
		},
		{
			name:      "empty object name",
			path:      "/n/ns1/b/bucket1/o/",
			wantErr:   true,
			errSubstr: "namespace, bucket, or object name is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseObjectStoragePath(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// TestObjectStorageHostFromRegion
// ---------------------------------------------------------------------------

func TestObjectStorageHostFromRegion(t *testing.T) {
	tests := []struct {
		name    string
		region  string
		want    string
		wantErr bool
	}{
		{
			name:   "us-ashburn-1",
			region: "us-ashburn-1",
			want:   "https://objectstorage.us-ashburn-1.oraclecloud.com",
		},
		{
			name:   "us-phoenix-1",
			region: "us-phoenix-1",
			want:   "https://objectstorage.us-phoenix-1.oraclecloud.com",
		},
		{
			name:    "empty region",
			region:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := objectStorageHostFromRegion(tt.region)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// TestResolvePARObjectStorageHost
// ---------------------------------------------------------------------------

func TestResolvePARObjectStorageHost(t *testing.T) {
	origAuthType := env.OCIAuthType
	t.Cleanup(func() {
		env.OCIAuthType = origAuthType
	})

	t.Run("derives from config provider region", func(t *testing.T) {
		env.OCIAuthType = "env"
		t.Setenv("OCI_TENANCY", "ocid1.tenancy.oc1..test")
		t.Setenv("OCI_USER", "ocid1.user.oc1..test")
		t.Setenv("OCI_REGION", "us-ashburn-1")
		t.Setenv("OCI_FINGERPRINT", "aa:bb:cc:dd:ee:ff")
		t.Setenv("OCI_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----")

		host, err := resolvePARObjectStorageHost()
		require.NoError(t, err)
		assert.Equal(t, "https://objectstorage.us-ashburn-1.oraclecloud.com", host)
	})

	t.Run("config provider failure", func(t *testing.T) {
		env.OCIAuthType = "some_unknown_type"

		host, err := resolvePARObjectStorageHost()
		assert.Empty(t, host)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "oci config provider")
	})
}

// ---------------------------------------------------------------------------
// TestBuildPARURL
// ---------------------------------------------------------------------------

func TestBuildPARURL(t *testing.T) {
	tests := []struct {
		name      string
		host      string
		accessUri string
		want      string
	}{
		{
			name:      "normal host and uri with leading slash",
			host:      "https://objectstorage.us-ashburn-1.oraclecloud.com",
			accessUri: "/p/abc123/n/ns/b/bkt/o/obj",
			want:      "https://objectstorage.us-ashburn-1.oraclecloud.com/p/abc123/n/ns/b/bkt/o/obj",
		},
		{
			name:      "host with trailing slash",
			host:      "https://objectstorage.us-ashburn-1.oraclecloud.com/",
			accessUri: "/p/abc123/n/ns/b/bkt/o/obj",
			want:      "https://objectstorage.us-ashburn-1.oraclecloud.com/p/abc123/n/ns/b/bkt/o/obj",
		},
		{
			name:      "access uri missing leading slash",
			host:      "https://objectstorage.us-ashburn-1.oraclecloud.com",
			accessUri: "p/abc123/n/ns/b/bkt/o/obj",
			want:      "https://objectstorage.us-ashburn-1.oraclecloud.com/p/abc123/n/ns/b/bkt/o/obj",
		},
		{
			name:      "host with trailing slash and uri without leading slash",
			host:      "https://objectstorage.us-ashburn-1.oraclecloud.com/",
			accessUri: "p/abc123/n/ns/b/bkt/o/obj",
			want:      "https://objectstorage.us-ashburn-1.oraclecloud.com/p/abc123/n/ns/b/bkt/o/obj",
		},
		{
			name:      "empty host",
			host:      "",
			accessUri: "/p/abc123",
			want:      "/p/abc123",
		},
		{
			name:      "empty access uri",
			host:      "https://example.com",
			accessUri: "",
			want:      "https://example.com/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildPARURL(tt.host, tt.accessUri)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// TestGenerateVSAPAR
// ---------------------------------------------------------------------------

func TestGenerateVSAPAR(t *testing.T) {
	ctx := context.Background()

	t.Run("nil AdminOCIService — returns error", func(t *testing.T) {
		svc := &OciServices{Ctx: ctx}

		url, err := svc.GenerateVSAPAR(ctx, "/n/ns/b/bkt/o/obj.tgz")
		assert.Error(t, err)
		assert.Empty(t, url)
		assert.Contains(t, err.Error(), "OCI object storage client not initialized")
	})

	t.Run("invalid vsaImagePath — returns parse error", func(t *testing.T) {
		svc := &OciServices{
			Ctx:             ctx,
			AdminOCIService: &AdminOCIService{},
		}

		url, err := svc.GenerateVSAPAR(ctx, "not-a-valid-path")
		assert.Error(t, err)
		assert.Empty(t, url)
		assert.Contains(t, err.Error(), "parse vsaImagePath")
	})

	t.Run("empty vsaImagePath — returns parse error", func(t *testing.T) {
		svc := &OciServices{
			Ctx:             ctx,
			AdminOCIService: &AdminOCIService{},
		}

		url, err := svc.GenerateVSAPAR(ctx, "")
		assert.Error(t, err)
		assert.Empty(t, url)
		assert.Contains(t, err.Error(), "parse vsaImagePath")
	})
}
