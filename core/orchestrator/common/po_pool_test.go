package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

func poRequestParams() *CreatePoolParams {
	return &CreatePoolParams{
		ClusterName:  "test-cluster",
		ServiceLevel: ServiceLevelExtreme,
		Mode:         ONTAPMode,
		IsRegionalHA: true,
	}
}

func TestIsPORequest(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*CreatePoolParams)
		nilParams bool
		want      bool
	}{
		{name: "extreme tier ONTAP mode", want: true},
		{name: "premium tier", mutate: func(p *CreatePoolParams) { p.ServiceLevel = ServiceLevelPremium }, want: true},
		{name: "nil params", nilParams: true, want: false},
		{name: "missing clusterName still PO request", mutate: func(p *CreatePoolParams) { p.ClusterName = "" }, want: true},
		{name: "unsupported service level", mutate: func(p *CreatePoolParams) { p.ServiceLevel = "FLEX" }, want: false},
		{name: "non-ONTAP mode", mutate: func(p *CreatePoolParams) { p.Mode = DEFAULTMode }, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var params *CreatePoolParams
			if !tt.nilParams {
				params = poRequestParams()
				if tt.mutate != nil {
					tt.mutate(params)
				}
			}

			assert.Equal(t, tt.want, IsPORequest(params))
		})
	}
}

func TestIsPOModelPool(t *testing.T) {
	tests := []struct {
		name string
		pool *models.Pool
		want bool
	}{
		{name: "nil pool", pool: nil, want: false},
		{
			name: "ONTAP extreme",
			pool: &models.Pool{APIAccessMode: ONTAPMode, ServiceLevel: ServiceLevelExtreme},
			want: true,
		},
		{
			name: "ONTAP premium",
			pool: &models.Pool{APIAccessMode: ONTAPMode, ServiceLevel: ServiceLevelPremium},
			want: true,
		},
		{
			name: "default extreme not PO",
			pool: &models.Pool{APIAccessMode: DEFAULTMode, ServiceLevel: ServiceLevelExtreme},
			want: false,
		},
		{
			name: "ONTAP flex not PO",
			pool: &models.Pool{APIAccessMode: ONTAPMode, ServiceLevel: "FLEX"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsPOModelPool(tt.pool))
		})
	}
}

func TestIsPOPool(t *testing.T) {
	tests := []struct {
		name string
		pool *datamodel.Pool
		want bool
	}{
		{name: "nil pool", pool: nil, want: false},
		{name: "no service level", pool: &datamodel.Pool{}, want: false},
		{name: "extreme tier on pool", pool: &datamodel.Pool{ServiceLevel: ServiceLevelExtreme}, want: true},
		{
			name: "premium tier in attributes",
			pool: &datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ServiceLevel: ServiceLevelPremium},
			},
			want: true,
		},
		{name: "flex tier", pool: &datamodel.Pool{ServiceLevel: "FLEX"}, want: false},
		{name: "host uuid alone not PO", pool: &datamodel.Pool{HostUUID: "host-1"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsPOPool(tt.pool))
		})
	}
}

func TestIsEligibleONTAPModePool(t *testing.T) {
	original := enablePOPremiumPools
	t.Cleanup(func() { enablePOPremiumPools = original })

	tests := []struct {
		name               string
		pool               *datamodel.Pool
		enablePremiumPools bool
		want               bool
	}{
		{name: "nil pool", pool: nil, want: false},
		{name: "empty pool", pool: &datamodel.Pool{}, want: false},
		{
			name: "ONTAP extreme",
			pool: &datamodel.Pool{APIAccessMode: ONTAPMode, ServiceLevel: ServiceLevelExtreme},
			want: true,
		},
		{
			name: "ONTAP extreme via attributes",
			pool: &datamodel.Pool{
				APIAccessMode:  ONTAPMode,
				PoolAttributes: &datamodel.PoolAttributes{ServiceLevel: ServiceLevelExtreme},
			},
			want: true,
		},
		{
			name:               "ONTAP premium not eligible when flag disabled",
			pool:               &datamodel.Pool{APIAccessMode: ONTAPMode, ServiceLevel: ServiceLevelPremium},
			enablePremiumPools: false,
			want:               false,
		},
		{
			name:               "ONTAP premium eligible when flag enabled",
			pool:               &datamodel.Pool{APIAccessMode: ONTAPMode, ServiceLevel: ServiceLevelPremium},
			enablePremiumPools: true,
			want:               true,
		},
		{
			name: "DEFAULT extreme not eligible",
			pool: &datamodel.Pool{APIAccessMode: DEFAULTMode, ServiceLevel: ServiceLevelExtreme},
			want: false,
		},
		{
			name: "ONTAP flex not eligible",
			pool: &datamodel.Pool{APIAccessMode: ONTAPMode, ServiceLevel: "FLEX"},
			want: false,
		},
		{
			name: "attributes override pool service level",
			pool: &datamodel.Pool{
				APIAccessMode:  ONTAPMode,
				ServiceLevel:   ServiceLevelPremium,
				PoolAttributes: &datamodel.PoolAttributes{ServiceLevel: ServiceLevelExtreme},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enablePOPremiumPools = tt.enablePremiumPools
			assert.Equal(t, tt.want, IsEligibleONTAPModePool(tt.pool))
		})
	}
}

func TestDerivePOStorageClass(t *testing.T) {
	assert.Equal(t, StorageClassHardware, DerivePOStorageClass(ServiceLevelExtreme))
	assert.Equal(t, StorageClassHardware, DerivePOStorageClass(ServiceLevelPremium))
	assert.Equal(t, StorageClassSoftware, DerivePOStorageClass("FLEX"))
}

func TestValidatePORequest(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*CreatePoolParams)
		wantErr string
	}{
		{name: "valid PO request", mutate: nil, wantErr: ""},
		{name: "premium PO request rejected when flag disabled", mutate: func(p *CreatePoolParams) { p.ServiceLevel = ServiceLevelPremium }, wantErr: POValidationPremiumDisabledMsg},
		{name: "non-ONTAP mode", mutate: func(p *CreatePoolParams) { p.Mode = DEFAULTMode }, wantErr: POValidationModeMsg},
		{name: "missing clusterName", mutate: func(p *CreatePoolParams) { p.ClusterName = "" }, wantErr: POValidationClusterNameMsg},
		{name: "unsupported service level", mutate: func(p *CreatePoolParams) { p.ServiceLevel = "FLEX" }, wantErr: POValidationServiceLevelMsg},
		{name: "auto-tiering flag", mutate: func(p *CreatePoolParams) { p.AllowAutoTiering = true }, wantErr: POValidationAutoTieringMsg},
		{name: "hot tier auto resize", mutate: func(p *CreatePoolParams) { p.EnableHotTierAutoResize = true }, wantErr: POValidationAutoTieringMsg},
		{
			name: "size without auto-tiering",
			mutate: func(p *CreatePoolParams) {
				p.SizeInBytes = 1099511627776
				p.HotTierSizeInBytes = p.SizeInBytes
			},
			wantErr: "",
		},
		{name: "manual qos", mutate: func(p *CreatePoolParams) { p.QosType = "manual" }, wantErr: POValidationManualQoSMsg},
		{
			name:    "custom performance",
			mutate:  func(p *CreatePoolParams) { p.CustomPerformanceParams = &CustomPerformanceParams{Enabled: true} },
			wantErr: POValidationManualQoSMsg,
		},
		{name: "zonal po request", mutate: func(p *CreatePoolParams) { p.IsRegionalHA = false }, wantErr: POValidationRegionalOnlyMsg},
		{name: "valid without zones", mutate: func(p *CreatePoolParams) {
			p.PrimaryZone = ""
			p.SecondaryZone = ""
		}, wantErr: ""},
		{name: "valid with zones when provided", mutate: func(p *CreatePoolParams) {
			p.PrimaryZone = "us-central1-a"
			p.SecondaryZone = "us-central1-b"
		}, wantErr: ""},
		{name: "active directory config id", mutate: func(p *CreatePoolParams) { p.ActiveDirectoryId = "ad-uuid" }, wantErr: POValidationActiveDirectoryMsg},
		{name: "ldap enabled", mutate: func(p *CreatePoolParams) { p.LdapEnabled = true }, wantErr: POValidationLdapEnabledMsg},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := poRequestParams()
			if tt.mutate != nil {
				tt.mutate(params)
			}

			err := ValidatePORequest(params)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestValidatePOPoolHostUUID(t *testing.T) {
	assert.NoError(t, ValidatePOPoolHostUUID(&datamodel.Pool{HostUUID: "host-1"}))
	assert.EqualError(t, ValidatePOPoolHostUUID(&datamodel.Pool{}), POValidationHostUUIDMsg)
	assert.EqualError(t, ValidatePOPoolHostUUID(nil), POValidationHostUUIDMsg)
}

func TestValidatePOAdLdapRestrictions(t *testing.T) {
	assert.NoError(t, ValidatePOAdLdapRestrictions("", false))
	assert.EqualError(t, ValidatePOAdLdapRestrictions("ad-uuid", false), POValidationActiveDirectoryMsg)
	assert.EqualError(t, ValidatePOAdLdapRestrictions("", true), POValidationLdapEnabledMsg)
}

func TestValidatePORequest_NilParams(t *testing.T) {
	assert.Error(t, ValidatePORequest(nil))
}

func TestValidatePORequest_POPremiumFeatureFlag(t *testing.T) {
	original := enablePOPremiumPools
	defer func() { enablePOPremiumPools = original }()
	originalCmek := enablePOCmek
	defer func() { enablePOCmek = originalCmek }()

	t.Run("RejectsPremiumWhenFlagDisabled", func(t *testing.T) {
		enablePOPremiumPools = false
		params := poRequestParams()
		params.ServiceLevel = ServiceLevelPremium

		err := ValidatePORequest(params)
		assert.EqualError(t, err, POValidationPremiumDisabledMsg)
	})

	t.Run("AllowsPremiumWhenFlagEnabled", func(t *testing.T) {
		enablePOPremiumPools = true
		params := poRequestParams()
		params.ServiceLevel = ServiceLevelPremium

		err := ValidatePORequest(params)
		assert.NoError(t, err)
	})

	t.Run("AllowsExtremeWhenFlagDisabled", func(t *testing.T) {
		enablePOPremiumPools = false
		params := poRequestParams()
		params.ServiceLevel = ServiceLevelExtreme

		err := ValidatePORequest(params)
		assert.NoError(t, err)
	})

	t.Run("AllowsKmsOnPORequestWhenFlagEnabled", func(t *testing.T) {
		enablePOCmek = true
		params := poRequestParams()
		params.KmsConfigId = "kms-config-id"

		err := ValidatePORequest(params)
		assert.NoError(t, err)
	})

	t.Run("RejectsKmsOnPORequestWhenFlagDisabled", func(t *testing.T) {
		enablePOCmek = false
		params := poRequestParams()
		params.KmsConfigId = "kms-config-id"

		err := ValidatePORequest(params)
		assert.EqualError(t, err, POValidationCmekMsg)
	})
}

func TestBuildPODataPathParams(t *testing.T) {
	params := BuildPODataPathParams(
		"acct-uuid",
		"projects/p/global/networks/net",
		"host-uuid-1",
		ServiceLevelExtreme,
		24,
		"pool-uuid",
		42,
	)
	assert.Equal(t, "acct-uuid", params.AccountUUID)
	assert.Equal(t, "projects/p/global/networks/net", params.RemoteAccountID)
	assert.Empty(t, params.ZoneUUID)
	assert.Equal(t, "host-uuid-1", params.MicrozoneUUID)
	assert.Equal(t, PODefaultSubnetMaskSize, params.SubnetMaskSize)
	assert.Equal(t, VlanBandwidth50G, params.VlanBandwidth)
	assert.True(t, params.IsMultiLIF)
	assert.True(t, params.IsFlexGroup)
	assert.Equal(t, "pool-uuid", params.PoolUUID)
	assert.Equal(t, int64(42), params.PoolID)
}
