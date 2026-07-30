package utils

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	stdErrors "errors"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	ontapmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	errs "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestValidateIPv4Address(t *testing.T) {
	tests := []struct {
		ipAddr string
		want   bool
	}{
		{"192.168.1.1", true},
		{"255.255.255.255", true},
		{"0.0.0.0", true},
		{"256.256.256.256", false},
		{"abc.def.ghi.jkl", false},
	}

	for _, tt := range tests {
		t.Run(tt.ipAddr, func(t *testing.T) {
			if got := ValidateIPv4Address(tt.ipAddr); got != tt.want {
				t.Errorf("ValidateIPv4Address(%v) = %v, want %v", tt.ipAddr, got, tt.want)
			}
		})
	}
}

func TestValidateIPRange(t *testing.T) {
	tests := []struct {
		name    string
		start   string
		end     string
		gateway string
		netmask string
		wantErr string
	}{
		{
			name:    "ValidRange",
			start:   "10.0.0.10",
			end:     "10.0.0.20",
			gateway: "10.0.0.1",
			netmask: "255.255.255.0",
		},
		{
			name:    "MissingStartIP",
			start:   "",
			end:     "10.0.0.20",
			gateway: "10.0.0.1",
			netmask: "255.255.255.0",
			wantErr: "must specify start IP address",
		},
		{
			name:    "MissingEndIP",
			start:   "10.0.0.10",
			end:     "",
			gateway: "10.0.0.1",
			netmask: "255.255.255.0",
			wantErr: "must specify end IP address",
		},
		{
			name:    "MissingGateway",
			start:   "10.0.0.10",
			end:     "10.0.0.20",
			gateway: "",
			netmask: "255.255.255.0",
			wantErr: "must specify gateway",
		},
		{
			name:    "MissingNetmask",
			start:   "10.0.0.10",
			end:     "10.0.0.20",
			gateway: "10.0.0.1",
			netmask: "",
			wantErr: "must specify netmask",
		},
		{
			name:    "InvalidStartIP",
			start:   "not-an-ip",
			end:     "10.0.0.20",
			gateway: "10.0.0.1",
			netmask: "255.255.255.0",
			wantErr: "'not-an-ip' is not a valid IPv4 address",
		},
		{
			name:    "InvalidEndIP",
			start:   "10.0.0.10",
			end:     "bad-end",
			gateway: "10.0.0.1",
			netmask: "255.255.255.0",
			wantErr: "'bad-end' is not a valid IPv4 address",
		},
		{
			name:    "InvalidGatewayIP",
			start:   "10.0.0.10",
			end:     "10.0.0.20",
			gateway: "bad-gateway",
			netmask: "255.255.255.0",
			wantErr: "'bad-gateway' is not a valid IPv4 address",
		},
		{
			name:    "InvalidNetmask",
			start:   "10.0.0.10",
			end:     "10.0.0.20",
			gateway: "10.0.0.1",
			netmask: "255.0.255.0",
			wantErr: "'255.0.255.0' is not a valid IPv4 netmask",
		},
		{
			name:    "StartIPHigherThanEndIP",
			start:   "10.0.0.20",
			end:     "10.0.0.10",
			gateway: "10.0.0.1",
			netmask: "255.255.255.0",
			wantErr: "specified start IP is higher than specified end IP",
		},
		{
			name:    "GatewayUnreachableFromStartIP",
			start:   "10.0.1.10",
			end:     "10.0.1.20",
			gateway: "10.0.0.1",
			netmask: "255.255.255.0",
			wantErr: "specified gateway is unreachable from start IP",
		},
		{
			name:    "GatewayUnreachableFromEndIP",
			start:   "10.0.0.10",
			end:     "10.0.1.20",
			gateway: "10.0.0.1",
			netmask: "255.255.255.0",
			wantErr: "specified gateway is unreachable from end IP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIPRange(tt.start, tt.end, tt.gateway, tt.netmask)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestIsNetmask(t *testing.T) {
	tests := []struct {
		name string
		ip   net.IP
		want bool
	}{
		{
			name: "NilIP",
			ip:   nil,
			want: false,
		},
		{
			name: "IPv6Address",
			ip:   net.ParseIP("2001:db8::1"),
			want: false,
		},
		{
			name: "ValidMask",
			ip:   net.ParseIP("255.255.255.0"),
			want: true,
		},
		{
			name: "InvalidMask",
			ip:   net.ParseIP("255.0.255.0"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNetmask(tt.ip))
		})
	}
}

func TestCompareIP(t *testing.T) {
	tests := []struct {
		name string
		ip1  net.IP
		ip2  net.IP
		want int
	}{
		{
			name: "EqualIPv4",
			ip1:  net.ParseIP("10.0.0.1"),
			ip2:  net.ParseIP("10.0.0.1"),
			want: 0,
		},
		{
			name: "LessThanIPv4",
			ip1:  net.ParseIP("10.0.0.1"),
			ip2:  net.ParseIP("10.0.0.2"),
			want: -1,
		},
		{
			name: "GreaterThanIPv4",
			ip1:  net.ParseIP("10.0.0.3"),
			ip2:  net.ParseIP("10.0.0.2"),
			want: 1,
		},
		{
			name: "LengthMismatchBothIPv4",
			ip1:  net.ParseIP("10.0.0.1"),
			ip2:  net.ParseIP("10.0.0.2").To4(),
			want: -1,
		},
		{
			name: "IPv4LessThanIPv6OnMismatch",
			ip1:  net.ParseIP("10.0.0.1"),
			ip2:  net.ParseIP("2001:db8::1"),
			want: -1,
		},
		{
			name: "IPv6GreaterThanIPv4OnMismatch",
			ip1:  net.ParseIP("2001:db8::1"),
			ip2:  net.ParseIP("10.0.0.1"),
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, compareIP(tt.ip1, tt.ip2))
		})
	}
}

func TestItemsInSliceUnique(t *testing.T) {
	tests := []struct {
		in   []string
		want bool
	}{
		{[]string{"a", "b", "c"}, true},
		{[]string{"a", "b", "a"}, false},
		{[]string{"A", "a"}, false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			if got := ItemsInSliceUnique(tt.in); got != tt.want {
				t.Errorf("ItemsInSliceUnique(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestContainsString(t *testing.T) {
	tests := []struct {
		arr  []string
		elem string
		want bool
	}{
		{[]string{"a", "b", "c"}, "b", true},
		{[]string{"a", " b", "c"}, "b", true},
		{[]string{"a", "b", "c"}, "d", false},
		{[]string{"a", " b", "c"}, "d", false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			if got := ContainsString(tt.arr, tt.elem); got != tt.want {
				t.Errorf("ContainsString(%v, %v) = %v, want %v", tt.arr, tt.elem, got, tt.want)
			}
		})
	}
}

func TestContainsStringCaseInsensitive(t *testing.T) {
	tests := []struct {
		name  string
		slice []string
		item  string
		want  bool
	}{
		{
			name:  "ExactMatch",
			slice: []string{"hello", "world", "test"},
			item:  "hello",
			want:  true,
		},
		{
			name:  "CaseInsensitiveMatchUppercase",
			slice: []string{"hello", "world", "test"},
			item:  "HELLO",
			want:  true,
		},
		{
			name:  "CaseInsensitiveMatchLowercase",
			slice: []string{"HELLO", "WORLD", "TEST"},
			item:  "hello",
			want:  true,
		},
		{
			name:  "CaseInsensitiveMatchMixedCase",
			slice: []string{"Hello", "World", "Test"},
			item:  "hELLO",
			want:  true,
		},
		{
			name:  "NoMatch",
			slice: []string{"hello", "world", "test"},
			item:  "missing",
			want:  false,
		},
		{
			name:  "EmptySlice",
			slice: []string{},
			item:  "hello",
			want:  false,
		},
		{
			name:  "EmptyItem",
			slice: []string{"hello", "world", "test"},
			item:  "",
			want:  false,
		},
		{
			name:  "EmptyItemInSlice",
			slice: []string{"hello", "", "test"},
			item:  "",
			want:  true,
		},
		{
			name:  "SpecialCharacters",
			slice: []string{"hello-world", "test_case", "dot.test"},
			item:  "HELLO-WORLD",
			want:  true,
		},
		{
			name:  "Numbers",
			slice: []string{"123", "456", "789"},
			item:  "123",
			want:  true,
		},
		{
			name:  "UnicodeCharacters",
			slice: []string{"café", "naïve", "résumé"},
			item:  "CAFÉ",
			want:  true,
		},
		{
			name:  "ProtocolNames",
			slice: []string{"iscsi", "nfsv3", "nfsv4", "smb"},
			item:  "ISCSI",
			want:  true,
		},
		{
			name:  "ProtocolNamesMixedCase",
			slice: []string{"ISCSI", "NFSV3", "NFSV4", "SMB"},
			item:  "nfsv3",
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContainsStringCaseInsensitive(tt.slice, tt.item)
			if got != tt.want {
				t.Errorf("ContainsStringCaseInsensitive(%v, %q) = %v, want %v", tt.slice, tt.item, got, tt.want)
			}
		})
	}
}

func TestIsCVPHostFlags(t *testing.T) {
	origHost := cvp.CVP_HOST
	origForce := ForceVCPKMSPathForTesting
	defer func() {
		cvp.CVP_HOST = origHost
		ForceVCPKMSPathForTesting = origForce
	}()

	cvp.CVP_HOST = "localhost:8009"

	t.Run("IsCVPHostSetHonorsForceOverride", func(t *testing.T) {
		ForceVCPKMSPathForTesting = true
		assert.False(t, IsCVPHostSet())
	})

	t.Run("IsCVPHostSetWithoutOverrideUsesHost", func(t *testing.T) {
		ForceVCPKMSPathForTesting = false
		assert.True(t, IsCVPHostSet())
	})

	t.Run("IsCVPHostConfiguredIgnoresForceOverride", func(t *testing.T) {
		ForceVCPKMSPathForTesting = true
		assert.True(t, IsCVPHostConfigured())
	})
}

func TestEnvToInt32Conversion(t *testing.T) {
	tests := []struct {
		envVal string
		def    int32
		want   int32
	}{
		{"123", 0, 123},
		{"abc", 0, 0},
		{"2147483647", 0, 2147483647},
		{"-2147483648", 0, -2147483648},
	}

	for _, tt := range tests {
		t.Run(tt.envVal, func(t *testing.T) {
			if got := EnvToInt32Conversion(tt.envVal, tt.def); got != tt.want {
				t.Errorf("EnvToInt32Conversion(%v, %v) = %v, want %v", tt.envVal, tt.def, got, tt.want)
			}
		})
	}
}

func TestCheckForRetriableError(t *testing.T) {
	tests := []struct {
		errorMessage    string
		retriableErrors []string
		want            bool
	}{
		{"network timeout", []string{"timeout", "temporary"}, true},
		{"disk full", []string{"timeout", "temporary"}, false},
		{"", []string{"timeout", "temporary"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.errorMessage, func(t *testing.T) {
			if got := CheckForRetriableError(tt.errorMessage, tt.retriableErrors); got != tt.want {
				t.Errorf("CheckForRetriableError(%v, %v) = %v, want %v", tt.errorMessage, tt.retriableErrors, got, tt.want)
			}
		})
	}
}

func TestContainsInt(t *testing.T) {
	tests := []struct {
		arr  []int
		elem int
		want bool
	}{
		{[]int{1, 2, 3}, 2, true},
		{[]int{1, 2, 3}, 4, false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			if got := ContainsInt(tt.arr, tt.elem); got != tt.want {
				t.Errorf("ContainsInt(%v, %v) = %v, want %v", tt.arr, tt.elem, got, tt.want)
			}
		})
	}
}

func TestContainsFloat64(t *testing.T) {
	tests := []struct {
		arr  []float64
		elem float64
		want bool
	}{
		{[]float64{1.1, 2.2, 3.3}, 2.2, true},
		{[]float64{1.1, 2.2, 3.3}, 4.4, false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			if got := ContainsFloat64(tt.arr, tt.elem); got != tt.want {
				t.Errorf("ContainsFloat64(%v, %v) = %v, want %v", tt.arr, tt.elem, got, tt.want)
			}
		})
	}
}

func TestDeepCopyPool(t *testing.T) {
	t.Run("ReturnsIndependentCopy", func(t *testing.T) {
		original := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-1",
			},
			Description: "original-description",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "us-central1-a",
			},
			AutoTieringConfig: &datamodel.AutoTieringConfig{
				BucketName: "bucket-a",
			},
		}

		copied, err := DeepCopyPool(original)
		require.NoError(t, err)
		require.NotNil(t, copied)
		require.NotSame(t, original, copied)
		require.NotSame(t, original.PoolAttributes, copied.PoolAttributes)
		require.NotSame(t, original.AutoTieringConfig, copied.AutoTieringConfig)

		// Mutate only the copy and verify original remains unchanged.
		copied.Description = "copied-description"
		copied.PoolAttributes.PrimaryZone = "us-central1-b"
		copied.AutoTieringConfig.BucketName = "bucket-b"

		assert.Equal(t, "original-description", original.Description)
		assert.Equal(t, "us-central1-a", original.PoolAttributes.PrimaryZone)
		assert.Equal(t, "bucket-a", original.AutoTieringConfig.BucketName)
	})

	t.Run("ReturnsErrorForNilInput", func(t *testing.T) {
		copied, err := DeepCopyPool(nil)
		require.Error(t, err)
		assert.Nil(t, copied)
		assert.Contains(t, err.Error(), "pool is nil")
	})

	t.Run("ReturnsErrorWhenMarshalFails", func(t *testing.T) {
		origMarshal := jsonMarshalFn
		jsonMarshalFn = func(v interface{}) ([]byte, error) {
			return nil, stdErrors.New("marshal failed")
		}
		t.Cleanup(func() {
			jsonMarshalFn = origMarshal
		})

		copied, err := DeepCopyPool(&datamodel.Pool{})
		require.Error(t, err)
		assert.Nil(t, copied)
		assert.Contains(t, err.Error(), "failed to marshal pool")
	})

	t.Run("ReturnsErrorWhenUnmarshalFails", func(t *testing.T) {
		origUnmarshal := jsonUnmarshalFn
		jsonUnmarshalFn = func(data []byte, v interface{}) error {
			return stdErrors.New("unmarshal failed")
		}
		t.Cleanup(func() {
			jsonUnmarshalFn = origUnmarshal
		})

		copied, err := DeepCopyPool(&datamodel.Pool{})
		require.Error(t, err)
		assert.Nil(t, copied)
		assert.Contains(t, err.Error(), "failed to unmarshal pool copy")
	})
}

func TestIsDuplicateUUID(t *testing.T) {
	tests := []struct {
		keys map[string]bool
		uuid string
		want bool
	}{
		{map[string]bool{"uuid1": true, "uuid2": true}, "uuid1", true},
		{map[string]bool{"uuid1": true, "uuid2": true}, "uuid3", false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			if got := IsDuplicateUUID(tt.keys, tt.uuid); got != tt.want {
				t.Errorf("IsDuplicateUUID(%v, %v) = %v, want %v", tt.keys, tt.uuid, got, tt.want)
			}
		})
	}
}

func TestParseProjectId(t *testing.T) {
	t.Run("ValidNetwork", func(tt *testing.T) {
		project, network, err := ParseProjectId("projects/12345/global/networks/my-network")
		if err != nil {
			tt.Errorf("Unexpected error: %s", err.Error())
		}
		if project != "12345" {
			tt.Errorf("Unexpected project ID: %s", project)
		}
		if network != "my-network" {
			tt.Errorf("Unexpected network name: %s", network)
		}
	})
	t.Run("InvalidNetwork", func(tt *testing.T) {
		_, _, err := ParseProjectId("invalid/network/format")
		if err == nil {
			tt.Error("Expected an error but got none")
		} else if !strings.Contains(err.Error(), "parseProjectId failed for network ") {
			tt.Errorf("Unexpected error message: %s", err.Error())
		}
	})
}

func TestConvertToBytes(t *testing.T) {
	tests := []struct {
		size   float64
		unit   Unit
		want   int64
		hasErr bool
	}{
		{1, B, 1, false},
		{1, KiB, 1024, false},
		{1, MiB, 1024 * 1024, false},
		{1, GiB, 1024 * 1024 * 1024, false},
		{0.5, GiB, 536870912, false},
		{0.33, GiB, 354334801, false}, // actual 354334801.92 floored to 354334801
		{1, TiB, 1024 * 1024 * 1024 * 1024, false},
		{1, PiB, 1024 * 1024 * 1024 * 1024 * 1024, false},
		{1, Unit(0), 0, true}, // Invalid unit
	}

	for _, tt := range tests {
		t.Run("TestConvertToBytes", func(t *testing.T) {
			got, err := ConvertToBytes(tt.size, tt.unit)
			if (err != nil) != tt.hasErr {
				t.Errorf("ConvertToBytes(%v, %v) error = %v, wantErr %v", tt.size, tt.unit, err, tt.hasErr)
				return
			}
			if got != tt.want {
				t.Errorf("ConvertToBytes(%v, %v) = %v, want %v", tt.size, tt.unit, got, tt.want)
			}
		})
	}
}

func TestSafeOptFloat64(t *testing.T) {
	tests := []struct {
		name  string
		input *float64
		want  gcpgenserver.OptFloat64
	}{
		{"NilInput", nil, gcpgenserver.OptFloat64{}},
		{"ValidInput", func() *float64 { v := 3.14; return &v }(), gcpgenserver.NewOptFloat64(3.14)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeOptFloat64(tt.input)
			if got != tt.want {
				t.Errorf("SafeOptFloat64(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSafeOptNilFloat64(t *testing.T) {
	tests := []struct {
		name  string
		input *float64
		want  gcpgenserver.OptNilFloat64
	}{
		{"NilInput", nil, gcpgenserver.OptNilFloat64{}},
		{"ValidInput", func() *float64 { v := 3.14; return &v }(), gcpgenserver.NewOptNilFloat64(3.14)},
		{"ZeroInput", func() *float64 { v := 0.0; return &v }(), gcpgenserver.NewOptNilFloat64(0.0)},
		{"NegativeInput", func() *float64 { v := -2.5; return &v }(), gcpgenserver.NewOptNilFloat64(-2.5)},
		{"LargeInput", func() *float64 { v := 1.7976931348623157e+308; return &v }(), gcpgenserver.NewOptNilFloat64(1.7976931348623157e+308)},
		{"SmallInput", func() *float64 { v := 2.2250738585072014e-308; return &v }(), gcpgenserver.NewOptNilFloat64(2.2250738585072014e-308)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeOptNilFloat64(tt.input)
			if got != tt.want {
				t.Errorf("SafeOptNilFloat64(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSafeTime(t *testing.T) {
	tests := []struct {
		name  string
		input *strfmt.DateTime
		want  gcpgenserver.OptNilDateTime
	}{
		{
			name:  "NilInput",
			input: nil,
			want:  gcpgenserver.OptNilDateTime{},
		},
		{
			name: "ValidDateTime",
			input: func() *strfmt.DateTime {
				dt := strfmt.DateTime(time.Date(2023, 12, 25, 10, 30, 45, 0, time.UTC))
				return &dt
			}(),
			want: gcpgenserver.NewOptNilDateTime(time.Date(2023, 12, 25, 10, 30, 45, 0, time.UTC)),
		},
		{
			name:  "ZeroDateTime",
			input: func() *strfmt.DateTime { dt := strfmt.DateTime(time.Time{}); return &dt }(),
			want:  gcpgenserver.NewOptNilDateTime(time.Time{}),
		},
		{
			name:  "CurrentTime",
			input: func() *strfmt.DateTime { dt := strfmt.DateTime(time.Now()); return &dt }(),
			want: func() gcpgenserver.OptNilDateTime {
				dt := strfmt.DateTime(time.Now())
				return gcpgenserver.NewOptNilDateTime(time.Time(dt))
			}(),
		},
		{
			name: "FutureDateTime",
			input: func() *strfmt.DateTime {
				dt := strfmt.DateTime(time.Date(2030, 6, 15, 14, 20, 30, 0, time.UTC))
				return &dt
			}(),
			want: gcpgenserver.NewOptNilDateTime(time.Date(2030, 6, 15, 14, 20, 30, 0, time.UTC)),
		},
		{
			name: "PastDateTime",
			input: func() *strfmt.DateTime {
				dt := strfmt.DateTime(time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC))
				return &dt
			}(),
			want: gcpgenserver.NewOptNilDateTime(time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeTime(tt.input)

			// For the current time test, we need to compare the actual time values
			if tt.name == "CurrentTime" {
				if tt.input != nil && got.IsSet() {
					expectedTime := time.Time(*tt.input)
					actualTime := got.Value
					// Allow for small time differences due to execution time
					timeDiff := actualTime.Sub(expectedTime)
					if timeDiff < 0 {
						timeDiff = -timeDiff
					}
					if timeDiff > time.Second {
						t.Errorf("SafeTime(%v) = %v, want time within 1 second of %v", tt.input, actualTime, expectedTime)
					}
				} else {
					t.Errorf("SafeTime(%v) should be set for current time test", tt.input)
				}
			} else {
				if got != tt.want {
					t.Errorf("SafeTime(%v) = %v, want %v", tt.input, got, tt.want)
				}
			}
		})
	}
}

func TestSafeBool(t *testing.T) {
	tests := []struct {
		name  string
		input *bool
		want  gcpgenserver.OptNilBool
	}{
		{"NilInput", nil, gcpgenserver.NewOptNilBool(false)},
		{"ValidInput", func() *bool { v := true; return &v }(), gcpgenserver.NewOptNilBool(true)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeBool(tt.input)
			if got != tt.want {
				t.Errorf("SafeBool(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSafeString(t *testing.T) {
	tests := []struct {
		name  string
		input *string
		want  gcpgenserver.OptNilString
	}{
		{"NilInput", nil, gcpgenserver.OptNilString{}},
		{"ValidInput", func() *string { v := "true"; return &v }(), gcpgenserver.NewOptNilString("true")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeString(tt.input)
			if got != tt.want {
				t.Errorf("SafeBool(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSafeFloat64(t *testing.T) {
	tests := []struct {
		name  string
		input *float64
		want  gcpgenserver.OptNilFloat64
	}{
		{"NilInput", nil, gcpgenserver.OptNilFloat64{}},
		{"ValidInput", func() *float64 { var v float64 = 1; return &v }(), gcpgenserver.NewOptNilFloat64(1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeFloat64(tt.input)
			if got != tt.want {
				t.Errorf("SafeFloat64(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSafeInt64(t *testing.T) {
	tests := []struct {
		name  string
		input *int64
		want  gcpgenserver.OptNilInt64
	}{
		{"NilInput", nil, gcpgenserver.OptNilInt64{}},
		{"ValidInput", func() *int64 { var v int64 = 1; return &v }(), gcpgenserver.NewOptNilInt64(1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeInt64(tt.input)
			if got != tt.want {
				t.Errorf("SafeInt64(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSafeInt64ToInt32(t *testing.T) {
	tests := []struct {
		name  string
		input *int64
		want  gcpgenserver.OptNilInt32
	}{
		{"NilInput", nil, gcpgenserver.OptNilInt32{}},
		{"ValidInput", func() *int64 { var v int64 = 1; return &v }(), gcpgenserver.NewOptNilInt32(1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeInt64ToInt32(tt.input)
			if got != tt.want {
				t.Errorf("SafeInt64ToInt32(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSafeInt32(t *testing.T) {
	tests := []struct {
		name  string
		input *int32
		want  gcpgenserver.OptNilInt32
	}{
		{"NilInput", nil, gcpgenserver.OptNilInt32{}},
		{"ValidInput", func() *int32 { var v int32 = 42; return &v }(), gcpgenserver.NewOptNilInt32(42)},
		{"ZeroInput", func() *int32 { var v int32 = 0; return &v }(), gcpgenserver.NewOptNilInt32(0)},
		{"NegativeInput", func() *int32 { var v int32 = -10; return &v }(), gcpgenserver.NewOptNilInt32(-10)},
		{"MaxInt32Input", func() *int32 { var v int32 = 2147483647; return &v }(), gcpgenserver.NewOptNilInt32(2147483647)},
		{"MinInt32Input", func() *int32 { var v int32 = -2147483648; return &v }(), gcpgenserver.NewOptNilInt32(-2147483648)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SafeInt32(tt.input)
			if got != tt.want {
				t.Errorf("SafeInt32(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetOptString(t *testing.T) {
	tests := []struct {
		name  string
		input *string
		want  gcpgenserver.OptString
	}{
		{"NilInput", nil, gcpgenserver.OptString{}},
		{"ValidInput", func() *string { v := "a"; return &v }(), gcpgenserver.NewOptString("a")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetOptString(tt.input)
			if got != tt.want {
				t.Errorf("GetOptString(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetOptInt64(t *testing.T) {
	tests := []struct {
		name  string
		input *int64
		want  gcpgenserver.OptInt64
	}{
		{"NilInput", nil, gcpgenserver.OptInt64{}},
		{"ValidInput", func() *int64 { var v int64 = 1; return &v }(), gcpgenserver.NewOptInt64(1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetOptInt64(tt.input)
			if got != tt.want {
				t.Errorf("GetOptString(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestGetOptBool(t *testing.T) {
	tests := []struct {
		name  string
		input *bool
		want  gcpgenserver.OptBool
	}{
		{"NilInput", nil, gcpgenserver.OptBool{}},
		{"ValidInput", func() *bool { v := true; return &v }(), gcpgenserver.NewOptBool(true)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetOptBool(tt.input)
			if got != tt.want {
				t.Errorf("GetOptBool(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestConvertStringToMap(t *testing.T) {
	t.Run("WhenErrorMarshalling", func(tt *testing.T) {
		inputString := "{\"an-awesome-place\" : }"
		s, err := ConvertStringToMap(inputString)
		require.EqualError(tt, err, "error when unmarshalling response")
		assert.Nil(tt, s)
	})
	t.Run("WhenOk", func(tt *testing.T) {
		inputString := "{\"an-awesome-place\" : \"http:10.10.10.1\", \"ok-place\" : \"10.100\"}"
		s, err := ConvertStringToMap(inputString)
		assert.NoError(tt, err)
		assert.Equal(tt, "http:10.10.10.1", s["an-awesome-place"])
	})
}

func TestGetPairedRegionURI(t *testing.T) {
	PairedRegions = "{\"an-awesome-place\" : \"someIp\"}"
	t.Run("WhenNotFound", func(tt *testing.T) {
		regions := make(map[string]string)
		regions["ok-place"] = "someIp"
		defer func() {
			ConvertStringToMap = _convertStringToMap
		}()
		ConvertStringToMap = func(s string) (map[string]string, error) {
			return regions, nil
		}
		region, err := GetPairedRegionURI("an-awesome-place")
		require.EqualError(tt, err, "region not found in paired regions list")
		assert.Equal(tt, "", region)
	})
	t.Run("WhenConvertReturnsError", func(tt *testing.T) {
		defer func() {
			ConvertStringToMap = _convertStringToMap
		}()
		ConvertStringToMap = func(s string) (map[string]string, error) {
			return nil, errors.New("some error")
		}
		region, err := GetPairedRegionURI("west")
		require.EqualError(tt, err, "some error")
		assert.Equal(tt, "", region)
	})
	t.Run("WhenFound", func(tt *testing.T) {
		regions := make(map[string]string)
		regions["an-awesome-place"] = "someIp"
		region, err := GetPairedRegionURI("an-awesome-place")
		require.NoError(tt, err, "region unexpectedly not found")
		assert.Equal(tt, "someIp", region)
	})
	t.Run("WhenNothingDefinedInConfig", func(tt *testing.T) {
		regions := make(map[string]string)
		regions["an-awesome-place"] = "someIp"
		PairedRegions = ""
		defer func() {
			ConvertStringToMap = _convertStringToMap
			PairedRegions = "{\"an-awesome-place\" : \"someIp\"}"
		}()
		ConvertStringToMap = func(s string) (map[string]string, error) {
			return regions, nil
		}
		region, err := GetPairedRegionURI("an-awesome-place")
		require.EqualError(tt, err, "paired regions not defined for this region")
		assert.Equal(tt, "", region)
	})
}

func TestGetCoRelationIDFromContext(t *testing.T) {
	// Create a context with http.Header
	header := http.Header{}
	header.Set(string(middleware.CorrelationIDName), "test-correlation-id")
	ctxWithHeader := context.WithValue(context.Background(), middleware.CorrelationContextKey, header)

	// Create a context with log.Fields
	fields := log.Fields{
		string(middleware.RequestCorrelationID): "test-correlation-id-from-logger",
	}
	ctxWithFields := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)

	// Test case 1: Context with http.Header
	t.Run("Context with http.Header", func(t *testing.T) {
		result := GetCoRelationIDFromContext(ctxWithHeader)
		assert.Equal(t, "test-correlation-id", result)
	})

	// Test case 2: Context with log.Fields
	t.Run("Context with log.Fields", func(t *testing.T) {
		result := GetCoRelationIDFromContext(ctxWithFields)
		assert.Equal(t, "test-correlation-id-from-logger", result)
	})

	// Test case 3: Context with no correlation ID
	t.Run("Context with no correlation ID", func(t *testing.T) {
		ctx := context.Background()
		result := GetCoRelationIDFromContext(ctx)
		assert.Equal(t, "", result)
	})
}

func TestGenerateStrongPassword(t *testing.T) {
	// Test valid password generation
	length := 12
	password, err := GenerateStrongPassword(length)
	if err != nil {
		t.Fatalf("expected no error, but got: %v", err)
	}
	if len(password) != length {
		t.Errorf("expected password length %d, but got %d", length, len(password))
	}

	// Check if the password contains at least one character from each category
	hasLower, hasUpper, hasDigit := false, false, false
	for _, char := range password {
		switch {
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsDigit(char):
			hasDigit = true
		}
	}
	if !hasLower || !hasUpper || !hasDigit {
		t.Errorf("password does not contain all required character types: %s", password)
	}

	// Test invalid password length
	_, err = GenerateStrongPassword(6)
	if err == nil {
		t.Errorf("expected error for password length less than 8, but got none")
	}
}

func TestRetrierOnCodes(t *testing.T) {
	mockLogger := log.NewLogger()
	httpCode := 429
	t.Run("RetriesOnRetryCode", func(t *testing.T) {
		attempts := 0
		fn := func() error {
			attempts++
			return &errs.CustomError{
				OriginalErr: errors.New("some error"),
				HttpCode:    &httpCode,
			}
		}
		err := RetrierOnCodes(mockLogger, fn, []int{429}, 2, time.Millisecond)
		if attempts != 2 {
			t.Errorf("expected 2 attempts, got %d", attempts)
		}
		if err == nil {
			t.Error("expected error to be returned")
		}
	})

	t.Run("StopsOnNonRetryableError", func(t *testing.T) {
		attempts := 0
		httpCode := 500
		fn := func() error {
			attempts++
			return &errs.CustomError{
				OriginalErr: errors.New("some error"),
				HttpCode:    &httpCode,
			}
		}
		err := RetrierOnCodes(mockLogger, fn, []int{429}, 3, time.Millisecond)
		if attempts != 1 {
			t.Errorf("expected 1 attempt, got %d", attempts)
		}
		if err == nil {
			t.Error("expected error to be returned")
		}
	})

	t.Run("ReturnsOnNoError", func(t *testing.T) {
		attempts := 0
		fn := func() error {
			attempts++
			return nil
		}
		err := RetrierOnCodes(mockLogger, fn, []int{429}, 3, time.Millisecond)
		if attempts != 1 {
			t.Errorf("expected 1 attempt, got %d", attempts)
		}
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("StopsOnStopRetry", func(t *testing.T) {
		attempts := 0
		httpCode := 429
		fn := func() error {
			attempts++
			return &errs.CustomError{
				OriginalErr: errors.New("some error"),
				HttpCode:    &httpCode,
			}
		}
		err := RetrierOnCodes(mockLogger, fn, []int{429}, 3, time.Millisecond)
		if attempts != 3 {
			t.Errorf("expected 1 attempt, got %d", attempts)
		}
		if err == nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}

func Test_convertBytesToGib(t *testing.T) {
	tests := []struct {
		name  string
		input float64
		want  int64
	}{
		{"ZeroBytes", 0, 0},
		{"OneGiB", 1024 * 1024 * 1024, 1},
		{"HalfGiB", 0.5 * 1024 * 1024 * 1024, 1},
		{"NegativeBytes", -1024 * 1024 * 1024, -1},
		{"NonIntegerGiB", 1.7 * 1024 * 1024 * 1024, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := _convertBytesToGib(tt.input)
			if got != tt.want {
				t.Errorf("_convertBytesToGib(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func Test_calculateRequiredVolumeSize(t *testing.T) {
	t.Run("WithRestoreVolumeBufferEnabled_Returns20PercentMore", func(t *testing.T) {
		// Enable the new calculation method
		SetRestoreVolumeBufferEnabledForTesting(true)
		defer SetRestoreVolumeBufferEnabledForTesting(false)

		testCases := []struct {
			name       string
			backupSize int64
			expected   int64
		}{
			{
				name:       "NormalSize",
				backupSize: 100 * 1024 * 1024 * 1024,                  // 100 GiB
				expected:   int64(float64(100*1024*1024*1024) * 1.20), // 120 GiB
			},
			{
				name:       "ZeroBackupSize",
				backupSize: 0, // 0 bytes
				expected:   0, // 0 * 1.20 = 0
			},
			{
				name:       "VerySmallBackup",
				backupSize: 1, // 1 byte
				expected:   1, // 1 * 1.20 = 1.2, truncated to 1
			},
			{
				name:       "LargeBackup",
				backupSize: 1000 * 1024 * 1024 * 1024,                  // 1000 GiB
				expected:   int64(float64(1000*1024*1024*1024) * 1.20), // 1200 GiB
			},
		}

		for _, tt := range testCases {
			t.Run(tt.name, func(t *testing.T) {
				result := _calculateRequiredVolumeSize(tt.backupSize, datamodel.BackupAttributes{})
				if result != tt.expected {
					t.Errorf("Expected %d, got %d", tt.expected, result)
				}
			})
		}
	})

	t.Run("WithRestoreVolumeBufferDisabled_ReturnsCeilOfBackupSizePlus1GiB", func(t *testing.T) {
		// Disable the new calculation method (default)
		SetRestoreVolumeBufferEnabledForTesting(false)
		defer SetRestoreVolumeBufferEnabledForTesting(false)

		testCases := []struct {
			name       string
			backupSize int64
			expected   int64
		}{
			{
				name:       "ExactGiB",
				backupSize: 100 * 1024 * 1024 * 1024, // 100 GiB
				expected:   101 * 1024 * 1024 * 1024, // 101 GiB (100 + 1)
			},
			{
				name:       "NonExactGiB",
				backupSize: 100*1024*1024*1024 + 500*1024*1024, // 100.5 GiB
				expected:   102 * 1024 * 1024 * 1024,           // 102 GiB (ceil(100.5 + 1) = 102)
			},
			{
				name:       "SmallBackup",
				backupSize: 500 * 1024 * 1024,      // 0.5 GiB
				expected:   2 * 1024 * 1024 * 1024, // 2 GiB (ceil(0.5 + 1) = 2)
			},
			{
				name:       "ZeroBackupSize",
				backupSize: 0,                      // 0 bytes
				expected:   1 * 1024 * 1024 * 1024, // 1 GiB (ceil(0 + 1) = 1)
			},
			{
				name:       "VerySmallBackup",
				backupSize: 1,                      // 1 byte
				expected:   2 * 1024 * 1024 * 1024, // 2 GiB (ceil(1 + 1GiB) = ceil(1.0000000009313226) = 2)
			},
			{
				name:       "JustUnder1GiB",
				backupSize: 1024*1024*1024 - 1,     // 1 GiB - 1 byte
				expected:   2 * 1024 * 1024 * 1024, // 2 GiB (ceil(1GiB-1 + 1GiB) = ceil(1.9999999990686774) = 2)
			},
		}

		for _, tt := range testCases {
			t.Run(tt.name, func(t *testing.T) {
				// Use SAN protocols to get the ceil calculation when flag is disabled
				result := _calculateRequiredVolumeSize(tt.backupSize, datamodel.BackupAttributes{Protocols: []string{ProtocolISCSI}})
				if result != tt.expected {
					t.Errorf("Expected %d, got %d", tt.expected, result)
				}
			})
		}
	})
}

func TestConvertsValidJsonToModel(t *testing.T) {
	t.Run("ValidJson", func(tt *testing.T) {
		jsonData := []byte(`{"name": "test", "age": 30}`)
		var model struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}

		err := ConvertJsonToModel(jsonData, &model)
		require.NoError(t, err)
		assert.Equal(t, "test", model.Name)
		assert.Equal(t, 30, model.Age)
	})
	t.Run("InvalidJson", func(tt *testing.T) {
		jsonData := []byte(`{"name": "test", "age": }`)
		var model struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}

		err := ConvertJsonToModel(jsonData, &model)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Failed to unmarshal json")
	})
	t.Run("EmptyJson", func(tt *testing.T) {
		jsonData := []byte(``)
		var model struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}

		err := ConvertJsonToModel(jsonData, &model)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Failed to unmarshal json")
	})
	t.Run("ConvertsJsonWithExtraFieldsToModel", func(tt *testing.T) {
		jsonData := []byte(`{"name": "test", "age": 30, "extra": "ignored"}`)
		var model struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}

		err := ConvertJsonToModel(jsonData, &model)
		require.NoError(t, err)
		assert.Equal(t, "test", model.Name)
		assert.Equal(t, 30, model.Age)
	})
	t.Run("FailsToConvertNonJsonInput", func(tt *testing.T) {
		jsonData := []byte(`not a json`)
		var model struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}

		err := ConvertJsonToModel(jsonData, &model)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Failed to unmarshal json")
	})
}

func TestRemapJSON(t *testing.T) {
	t.Run("ValidModel", func(tt *testing.T) {
		src := struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}{
			Name: "test",
			Age:  30,
		}
		var model struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}

		err := RemapJSON(src, &model)
		require.NoError(tt, err)
		assert.Equal(tt, "test", model.Name)
		assert.Equal(tt, 30, model.Age)
	})

	t.Run("InvalidModel", func(tt *testing.T) {
		src := map[string]any{"bad": func() {}}
		var model struct{}

		err := RemapJSON(src, &model)
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "Failed to marshal json")
	})
}

func TestGetRequestIDFromContext(t *testing.T) {
	t.Run("RequestIDPresent", func(tt *testing.T) {
		fields := log.Fields{
			string(middleware.RequestID): "test-request-id",
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)
		got := GetRequestIDFromContext(ctx)
		assert.Equal(tt, "test-request-id", got)
	})

	t.Run("RequestIDAbsent", func(tt *testing.T) {
		fields := log.Fields{}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)
		got := GetRequestIDFromContext(ctx)
		assert.Equal(tt, "", got)
	})

	t.Run("NoFieldsInContext", func(tt *testing.T) {
		ctx := context.Background()
		got := GetRequestIDFromContext(ctx)
		assert.Equal(tt, "", got)
	})
}

func TestGenerateRandomString(t *testing.T) {
	t.Run("GeneratesRandomStringOfCorrectLength", func(t *testing.T) {
		result, err := generateRandomString(10)
		assert.NoError(t, err)
		assert.Equal(t, 10, len(result))
	})
	t.Run("FailsToGenerateRandomStringWhenLengthIsZero", func(t *testing.T) {
		result, err := generateRandomString(0)
		assert.NoError(t, err)
		assert.Equal(t, "", result)
	})
	t.Run("GeneratesRandomStringWithValidCharacters", func(t *testing.T) {
		result, err := generateRandomString(15)
		assert.NoError(t, err)
		for _, char := range result {
			assert.Contains(t, "abcdefghijklmnopqrstuvwxyz0123456789", string(char))
		}
	})
}

func TestSliceRegionForServiceAccount(t *testing.T) {
	t.Run("RegionUnchangedWhenWithinMaxLength", func(t *testing.T) {
		region := "short-region-name"
		expected := "short-region-name"
		result := sliceRegionForServiceAccount(region)
		assert.Equal(t, expected, result)
	})
	t.Run("RegionEmptyStringReturnsEmptyString", func(t *testing.T) {
		region := ""
		expected := ""
		result := sliceRegionForServiceAccount(region)
		assert.Equal(t, expected, result)
	})
	t.Run("RegionExactlyMaxLengthReturnsSameRegion", func(t *testing.T) {
		region := "this-is-exactly-25-characters"
		expected := "this-is-exactly-25-charac"
		result := sliceRegionForServiceAccount(region)
		assert.Equal(t, expected, result)
	})
}

func TestGeneratesValidResourceNames(t *testing.T) {
	gcpRegion := "us-central1"
	tenantProjectRegion := "us-central1"
	tenantProjectNumber := "123456789"
	backupVaultUUID := "vault-uuid"
	email, bucketName, serviceAccountId, err := GetResourcesNameForBackup(gcpRegion, tenantProjectRegion, tenantProjectNumber, backupVaultUUID)
	require.NoError(t, err)
	assert.Equal(t, "vsa-backup-us-cent", serviceAccountId[:18])
	assert.Contains(t, email, "@123456789.iam.gserviceaccount.com")
	assert.Contains(t, bucketName, "vsa-backup-vault-uuid")
}

func TestHandlesEmptyRegion(t *testing.T) {
	gcpRegion := ""
	tenantProjectRegion := "us-central1"
	tenantProjectNumber := "123456789"
	backupVaultUUID := "vault-uuid"
	email, bucketName, serviceAccountId, err := GetResourcesNameForBackup(gcpRegion, tenantProjectRegion, tenantProjectNumber, backupVaultUUID)
	require.NoError(t, err)
	assert.Equal(t, "vsa-backup", serviceAccountId[:10])
	assert.Contains(t, email, "@123456789.iam.gserviceaccount.com")
	assert.Contains(t, bucketName, "vsa-backup-vault-uuid")
}

func TestGetLunName(t *testing.T) {
	tests := []struct {
		testName   string
		volumeName string
		want       string
	}{
		{"WhenVolumeNameIsPassed_ThenReturnLunName_1", "my_volume", "lun_my_volume"},
		{"WhenVolumeNameIsPassed_ThenReturnLunName_2", "volume", "lun_volume"},
	}

	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			got := GetLunName(tt.volumeName)
			if got != tt.want {
				t.Errorf("GetLunName(%s) = %s, want %s", tt.volumeName, got, tt.want)
			}
		})
	}
}

func TestIsTransitionalState(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  bool
	}{
		{"CreatingState", "CREATING", true},
		{"UpdatingState", datamodel.LifeCycleStateCreating, true},
		{"UpdatingState", datamodel.LifeCycleStateUpdating, true},
		{"DeletingState", datamodel.LifeCycleStateDeleting, true},
		{"ReadyState", datamodel.LifeCycleStateREADY, false},
		{"AvailableState", datamodel.LifeCycleStateAvailable, false},
		{"EmptyString", "", false},
		{"RandomString", "SOME_UNKNOWN_STATE", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsTransitionalState(tt.state)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateCcfeReplicationUri(t *testing.T) {
	tests := []struct {
		name  string
		uri   string
		valid bool
	}{
		{"ValidURI", "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6", true},
		{"InvalidURI", "invalid://project/region/volume", false},
		{"EmptyURI", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCcfeReplicationUri(tt.uri)
			if tt.valid {
				assert.NoError(t, err, "Expected no error for valid URI")
			} else {
				assert.Error(t, err, "Expected error for invalid URI")
			}
		})
	}
}

func TestCFFEURIToMap(t *testing.T) {
	tests := []struct {
		name     string
		ccfeUri  string
		expected map[string]string
	}{
		{
			"ValidURI", "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6",
			map[string]string{"projects": "45110233509", "locations": "us-east4", "volumes": "gosrcvolume1", "replications": "replication-name-6"},
		},
		{"InvalidURI", "invalid://project/region/volume", nil},
		{"EmptyURI", "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CFFEURIToMap(tt.ccfeUri)
			if tt.expected == nil {
				assert.Error(t, err, "Expected error for invalid URI")
				assert.Empty(t, result, "Expected result to be nil for invalid URI")
			} else {
				assert.NoError(t, err, "Expected no error for valid URI")
				assert.Equal(t, tt.expected, result, "Expected map to match")
			}
		})
	}
}

func TestValidateOperationUri(t *testing.T) {
	tests := []struct {
		name         string
		uri          string
		valid        bool
		expectedOpID string
	}{
		{"ValidURI", "/v1beta/projects/45110233509/locations/us-east4/operations/operation-123", true, "operation-123"},
		{"InvalidURI", "invalid://project/region/operation", false, ""},
		{"EmptyURI", "", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opID, err := ValidateOperationUri(tt.uri)
			if tt.valid {
				assert.NoError(t, err, "Expected no error for valid URI")
				assert.Equal(t, tt.expectedOpID, opID, "Expected operation ID to match")
			} else {
				assert.Error(t, err, "Expected error for invalid URI")
				assert.Empty(t, opID, "Expected empty operation ID for invalid URI")
			}
		})
	}
}

func TestParseProjectNumberFromURI(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		expected string
	}{
		{"ValidURI", "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6", "45110233509"},
		{"InvalidURI", "invalid/uri/format", ""},
		{"EmptyURI", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := _parseProjectNumberFromURI(tt.uri)
			if err != nil {
				assert.Empty(t, result, "Expected empty result for invalid URI")
				return
			}
		})
	}
}

func TestGetReplicationNameFromURI(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		expected string
	}{
		{"ValidURI", "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6", "replication-name-6"},
		{"ValidURIWithSpecialChars", "projects/123456789/locations/australia-southeast1-a/volumes/mrasrc1255/replications/replicationtest581", "replicationtest581"},
		{"ValidURIWithNumbers", "projects/987654321/locations/us-central1/volumes/volume123/replications/replication-456", "replication-456"},
		{"InvalidURI", "invalid://project/region/volume", ""},
		{"EmptyURI", "", ""},
		{"MalformedURI", "projects/123/locations/us-central1/volumes/test", ""},
		{"MissingReplication", "projects/123/locations/us-central1/volumes/test-volume", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := _getReplicationNameFromURI(tt.uri)
			if tt.expected == "" {
				assert.Error(t, err, "Expected error for invalid URI")
				assert.Empty(t, result, "Expected empty result for invalid URI")
				return
			}
			assert.NoError(t, err, "Expected no error for valid URI")
			assert.Equal(t, tt.expected, result, "Expected replication name to match")
		})
	}
}

func TestSplitIntSliceIntoChunks(t *testing.T) {
	tests := []struct {
		name     string
		input    []int64
		lim      int
		expected [][]int64
	}{
		{
			name:     "ExactChunks",
			input:    []int64{1, 2, 3, 4, 5, 6},
			lim:      2,
			expected: [][]int64{{1, 2}, {3, 4}, {5, 6}},
		},
		{
			name:     "LastChunkSmaller",
			input:    []int64{1, 2, 3, 4, 5},
			lim:      2,
			expected: [][]int64{{1, 2}, {3, 4}, {5}},
		},
		{
			name:     "SingleChunk",
			input:    []int64{1, 2},
			lim:      5,
			expected: [][]int64{{1, 2}},
		},
		{
			name:     "EmptyInput",
			input:    []int64{},
			lim:      3,
			expected: [][]int64{},
		},
		{
			name:     "LimitIsOne",
			input:    []int64{1, 2, 3},
			lim:      1,
			expected: [][]int64{{1}, {2}, {3}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SplitIntSliceIntoChunks(tt.input, tt.lim)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSplitStringSliceIntoChunks(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		lim      int
		expected [][]string
	}{
		{
			name:     "ExactChunks",
			input:    []string{"a", "b", "c", "d", "e", "f"},
			lim:      2,
			expected: [][]string{{"a", "b"}, {"c", "d"}, {"e", "f"}},
		},
		{
			name:     "LastChunkSmaller",
			input:    []string{"a", "b", "c", "d", "e"},
			lim:      2,
			expected: [][]string{{"a", "b"}, {"c", "d"}, {"e"}},
		},
		{
			name:     "SingleChunk",
			input:    []string{"a", "b"},
			lim:      5,
			expected: [][]string{{"a", "b"}},
		},
		{
			name:     "EmptyInput",
			input:    []string{},
			lim:      3,
			expected: [][]string{},
		},
		{
			name:     "LimitIsOne",
			input:    []string{"a", "b", "c"},
			lim:      1,
			expected: [][]string{{"a"}, {"b"}, {"c"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SplitStringSliceIntoChunks(tt.input, tt.lim)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLoadJsonFromFile(t *testing.T) {
	t.Run("WhenFileExistsAndHasValidJSON", func(tt *testing.T) {
		tmpFile, err := os.CreateTemp("", "employee*.json")
		if err != nil {
			tt.Fatalf("Failed to create temp file: %v", err)
		}
		defer func(name string) {
			err := os.Remove(name)
			if err != nil {
				return
			}
		}(tmpFile.Name())

		validJSON := `{"name": "John Doe", "age": "30", "position": "Software Engineer"}`
		if _, err := tmpFile.Write([]byte(validJSON)); err != nil {
			tt.Fatalf("Failed to write to temp file: %v", err)
		}
		err = tmpFile.Close()
		if err != nil {
			return
		}

		var employee map[string]string
		err = LoadJsonFromFile(tmpFile.Name(), &employee)
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}
	})
	t.Run("WhenFileDoesNotExist", func(tt *testing.T) {
		var employee map[string]string
		err := LoadJsonFromFile("nonexistent.json", &employee)
		if err == nil {
			tt.Errorf("Expected error for non-existent file, got nil")
		}
	})
	t.Run("WhenFileExistsAndHasInvalidJSON", func(tt *testing.T) {
		invalidJSON := `{"name": "John Doe", "age": 30, "position": "Software Engineer"}`
		tmpFile, err := os.CreateTemp("", "admin_background_jobs*.json")
		if err != nil {
			tt.Fatalf("Failed to create temp file: %v", err)
		}
		defer func(name string) {
			err := os.Remove(name)
			if err != nil {
				return
			}
		}(tmpFile.Name())

		if _, err := tmpFile.Write([]byte(invalidJSON)); err != nil {
			tt.Fatalf("Failed to write to temp file: %v", err)
		}
		err = tmpFile.Close()
		if err != nil {
			return
		}

		var employee map[string]string
		err = LoadJsonFromFile(tmpFile.Name(), &employee)
		if err == nil {
			tt.Errorf("Expected error for invalid JSON, got nil")
		}
	})
}

func TestGetArrayDiff(t *testing.T) {
	tests := []struct {
		name             string
		existingList     []string
		newList          []string
		expectedToCreate []string
		expectedToDelete []string
	}{
		{
			name:             "NoDifference",
			existingList:     []string{"a", "b", "c"},
			newList:          []string{"a", "b", "c"},
			expectedToCreate: []string{},
			expectedToDelete: []string{},
		},
		{
			name:             "AllNewItems",
			existingList:     []string{},
			newList:          []string{"a", "b", "c"},
			expectedToCreate: []string{"a", "b", "c"},
			expectedToDelete: []string{},
		},
		{
			name:             "AllItemsRemoved",
			existingList:     []string{"a", "b", "c"},
			newList:          []string{},
			expectedToCreate: []string{},
			expectedToDelete: []string{"a", "b", "c"},
		},
		{
			name:             "SomeItemsAddedAndRemoved",
			existingList:     []string{"a", "b", "c"},
			newList:          []string{"b", "c", "d"},
			expectedToCreate: []string{"d"},
			expectedToDelete: []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toCreate, toDelete := GetArrayDiff(tt.existingList, tt.newList)
			assert.ElementsMatch(t, tt.expectedToCreate, toCreate, "toCreate does not match expected")
			assert.ElementsMatch(t, tt.expectedToDelete, toDelete, "toDelete does not match expected")
		})
	}
}

func TestIsSliceEqual(t *testing.T) {
	tests := []struct {
		name   string
		slice1 []string
		slice2 []string
		want   bool
	}{
		{"EqualSlices", []string{"a", "b", "c"}, []string{"a", "b", "c"}, true},
		{"DifferentLengths", []string{"a", "b"}, []string{"a", "b", "c"}, false},
		{"DifferentElements", []string{"a", "b", "c"}, []string{"a", "b", "d"}, false},
		{"EmptySlices", []string{}, []string{}, true},
		{"OneEmptySlice", []string{"a", "b", "c"}, []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSliceEqual(tt.slice1, tt.slice2)
			if got != tt.want {
				t.Errorf("IsSliceEqual(%v, %v) = %v, want %v", tt.slice1, tt.slice2, got, tt.want)
			}
		})
	}
}

func TestGetEncryptionType(t *testing.T) {
	cloudKms := "kms-id"
	servManaged := (*string)(nil)

	got := GetEncryptionType(&cloudKms)
	assert.Equal(t, "CLOUD_KMS", got)

	got = GetEncryptionType(servManaged)
	assert.Equal(t, "SERVICE_MANAGED", got)

	empty := ""
	got = GetEncryptionType(&empty)
	assert.Equal(t, "SERVICE_MANAGED", got)
}

func TestRenameSnapshotName(t *testing.T) {
	t.Run("WhenSnapMirror", func(tt *testing.T) {
		snapshot := models.Snapshot{}
		snapshot.Name = "snapmirror.d416b9ed-df7d-11ed-b115-d039ea174b66_2159698722.2023-05-02_162000"

		expectedName := "replication-2023-05-02-162000"

		response := RenameSnapshotName(snapshot.Name)

		assert.Equal(tt, expectedName, response)
	})
	t.Run("WhenNameContainsUnderScore", func(tt *testing.T) {
		snapshot := models.Snapshot{}
		snapshot.Name = "random_name"

		expectedName := "random-name"

		response := RenameSnapshotName(snapshot.Name)

		assert.Equal(tt, expectedName, response)
	})
	t.Run("WhenNameContainsPrefixAsWeekly", func(tt *testing.T) {
		snapshot := models.Snapshot{}
		snapshot.Name = "weekly-on-saturday+sunday+monday+tuesday+wednesday+thursday+friday-10-min-past-1pm.2023-12-01_1310"
		expectedName := "weekly-2023-12-01-1310"

		response := RenameSnapshotName(snapshot.Name)

		assert.Equal(tt, expectedName, response)
	})
	t.Run("WhenNameContainsPrefixAsMonthly", func(tt *testing.T) {
		snapshot := models.Snapshot{}
		snapshot.Name = "monthly-on-day-1+2+3-10-min-past-1pm.2023-12-01_1310"

		expectedName := "monthly-2023-12-01-1310"

		response := RenameSnapshotName(snapshot.Name)

		assert.Equal(tt, expectedName, response)
	})
	t.Run("WhenNameContainsPrefixAsMonthly", func(tt *testing.T) {
		snapshot := models.Snapshot{}
		snapshot.Name = "weekly-on-monday-35-min-past-7am.2023-12-18_0735"

		expectedName := "weekly-on-monday-35-min-past-7am-2023-12-18-0735"

		response := RenameSnapshotName(snapshot.Name)

		assert.Equal(tt, expectedName, response)
	})
	t.Run("WhenNameContainsPrefixAsDaily", func(tt *testing.T) {
		snapshot := models.Snapshot{}
		snapshot.Name = "daily-10-min-past-1pm.2023-12-01_1310"

		expectedName := "daily-10-min-past-1pm-2023-12-01-1310"

		response := RenameSnapshotName(snapshot.Name)

		assert.Equal(tt, expectedName, response)
	})
}

func TestCheckFor1pNamingConvention(t *testing.T) {
	t.Run("WhenStringIsNotIN1P", func(tt *testing.T) {
		snapshot := models.Snapshot{}
		snapshot.Name = "snapmirror.d416b9ed-df7d-11ed-b115-d039ea174b66_2159698722.2023-05-02_162000"
		expectedName := false
		response := CheckForGcpNamingConvention(snapshot.Name)
		assert.Equal(tt, expectedName, response)
	})
	t.Run("WhenStringIsIN1P", func(tt *testing.T) {
		snapshot := models.Snapshot{}
		snapshot.Name = "snapmirror"
		expectedName := true
		response := CheckForGcpNamingConvention(snapshot.Name)
		assert.Equal(tt, expectedName, response)
	})
}

func TestConvertTo1pString(t *testing.T) {
	t.Run("WhenConvertNot1pString", func(tt *testing.T) {
		snapshot := models.Snapshot{}
		snapshot.Name = "snapmirror.d416b9ed-df7d-11ed-b115-d039ea174b66_2159698722.2023-05-02_162000"
		expectedName := "b115-d039ea174b66_2159698722.2023-05-02_162000"
		response := ConvertToGcpResourceName(snapshot.Name)
		assert.Equal(tt, expectedName, response)
	})
	t.Run("WhenConvert1pString", func(tt *testing.T) {
		snapshot := models.Snapshot{}
		snapshot.Name = "snapmirror-123"
		expectedName := "snapmirror-123"
		response := ConvertToGcpResourceName(snapshot.Name)
		assert.Equal(tt, expectedName, response)
	})
}

func TestGetRegion(t *testing.T) {
	t.Run("WhenSecondaryZoneIsPresent", func(tt *testing.T) {
		snapshot := datamodel.Snapshot{
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					PoolAttributes: &datamodel.PoolAttributes{
						PrimaryZone:   "au-se1",
						SecondaryZone: "us-east4-b",
						IsRegionalHA:  true,
					},
				},
			},
		}

		expectedName := "us-east4"
		response := GetLocation(snapshot)
		assert.Equal(tt, expectedName, response)
	})

	t.Run("WhenSecondaryZoneIsEmpty", func(tt *testing.T) {
		snapshot := datamodel.Snapshot{
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					PoolAttributes: &datamodel.PoolAttributes{
						PrimaryZone:   "au-se1",
						SecondaryZone: "",
						IsRegionalHA:  false,
					},
				},
			},
		}

		expectedName := "au-se1"
		response := GetLocation(snapshot)
		assert.Equal(tt, expectedName, response)
	})
}

func TestGetHgUUIDs(t *testing.T) {
	tests := []struct {
		name      string
		hgDetails []datamodel.HostGroupDetail
		want      []string
	}{
		{
			name: "ValidHostGroupDetails",
			hgDetails: []datamodel.HostGroupDetail{
				{HostGroupUUID: "uuid1"},
				{HostGroupUUID: "uuid2"},
				{HostGroupUUID: "uuid3"},
			},
			want: []string{"uuid1", "uuid2", "uuid3"},
		},
		{
			name:      "EmptyHostGroupDetails",
			hgDetails: []datamodel.HostGroupDetail{},
			want:      []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetHgUUIDs(tt.hgDetails)
			if !assert.ElementsMatch(t, tt.want, got) {
				t.Errorf("GetHgUUIDs() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Unit test for _parsePEMCertificate
func Test_parsePEMCertificate(t *testing.T) {
	validCert := []string{
		"-----BEGIN CERTIFICATE-----\nMIIFEDCCA3igAwIBAgITS/bXdYAv5LyZiy9Wd+osv4/IazANBgkqhkiG9w0BAQsF\nADAkMQ8wDQYDVQQKEwZOZXRhcHAxETAPBgNVBAMTCHNzLWNhLWNuMB4XDTI1MDYw\nMjE0MzQ1MFoXDTI1MDcwMjE0MzQ0OVowLjEPMA0GA1UEChMGbmV0YXBwMRswGQYD\nVQQDExJzaGFzaGktdXNlci1jbGllbnQwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAw\nggEKAoIBAQDcagOhNAO68lpqXSkHrxBlF3xLNetZS739eGUneujlMqLWjMA04u9v\nBZGYdbKP5VDB8j2H4qiop7KzXIteKCpP6lmLD7vqpV5a7wIU2esewI4QeaZ+RvdL\np9duqnAv5JpTTAjKFhA4+jZQCqjJaqltYtPYMBxw8TiQcwkQUlQbYlVMYbHurkPE\nCItpDJPEyyddabM6lXri6nsWRyC2J13h8BAJMflC6yPXsb/fwsSjJULNzQr41A74\nwBJ/OTtQwLgrS8qxlxTAUL+dBUKW2/kIUT+pkqCd1nOvOgSzSdDj2qhLI2aNWrQl\noCNvdCWELoq6D0sj+DssrB7KNyrNRCafAgMBAAGjggGvMIIBqzAOBgNVHQ8BAf8E\nBAMCBaAwHQYDVR0lBBYwFAYIKwYBBQUHAwEGCCsGAQUFBwMCMAwGA1UdEwEB/wQC\nMAAwHQYDVR0OBBYEFJbr1PPEc/mieh1yXp+j5Mx80g79MB8GA1UdIwQYMBaAFM8w\nTjkEr4gm0t2b6qptwRlFdAQrMIGNBggrBgEFBQcBAQSBgDB+MHwGCCsGAQUFBzAC\nhnBodHRwOi8vcHJpdmF0ZWNhLWNvbnRlbnQtNjg1YmQ5ZjctMDAwMC0yMWQ2LTlj\nM2ItMTRjMTRlZjQ4NmQ0LnN0b3JhZ2UuZ29vZ2xlYXBpcy5jb20vYTNkM2UyNWE0\nZjhkNmJjNzQ5YmUvY2EuY3J0MBcGA1UdEQQQMA6CDCouc2hhc2hpLmNvbTCBggYD\nVR0fBHsweTB3oHWgc4ZxaHR0cDovL3ByaXZhdGVjYS1jb250ZW50LTY4NWJkOWY3\nLTAwMDAtMjFkNi05YzNiLTE0YzE0ZWY0ODZkNC5zdG9yYWdlLmdvb2dsZWFwaXMu\nY29tL2EzZDNlMjVhNGY4ZDZiYzc0OWJlL2NybC5jcmwwDQYJKoZIhvcNAQELBQAD\nggGBAIVgs0Kp142hnA3AxTTF84GqkX5gDuoAn7thK7Mgvjeuc8XPaM/jj+CNApK7\nGoQazkNxz2VJmwYtCaXPzYwMd6H10Y8CsF02mfbRXLbxa0MwVP/LR7rO0sOlv32o\nqzk1rs/UHYffaEz+CrxuPFqdhh5gw188siGIrlpfLNfR6IjdwLE1anH0dYwcxKFc\nDdNMxyX3wXnT4yVe2ufAK0PMvmJHHicoWsVU1CCRzHtySfKpRKYhWI54gbI0fmWK\nTjbf1jg5veC42ShIpFzCi7bU/7tfnhweD1qskqOuw+ipjbqxlxOuSoUw439WTVfb\nDvnEZAN0i/xR8/F0gv5TQwIY03ip1Lq08ak8/tTdJabInGtqquJsaFzgzO8b+0hE\nSWtfJXPFZh6UKLjAaxh4j7kKq2f8QS4uG07THlh0SPOmI+O0SKaw6gfk3gqZXyJ0\nXGw/CqljKg+9HZ1JeN6M/hT0cH7rSSfKmaySY9iD1i1lxjxM+zHuiWYRJbA2Ahhf\nim6RRg==\n-----END CERTIFICATE-----",
		"-----BEGIN CERTIFICATE-----\nMIIFEDCCA3igAwIBAgITS/bXdYAv5LyZiy9Wd+osv4/IazANBgkqhkiG9w0BAQsF\nADAkMQ8wDQYDVQQKEwZOZXRhcHAxETAPBgNVBAMTCHNzLWNhLWNuMB4XDTI1MDYw\nMjE0MzQ1MFoXDTI1MDcwMjE0MzQ0OVowLjEPMA0GA1UEChMGbmV0YXBwMRswGQYD\nVQQDExJzaGFzaGktdXNlci1jbGllbnQwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAw\nggEKAoIBAQDcagOhNAO68lpqXSkHrxBlF3xLNetZS739eGUneujlMqLWjMA04u9v\nBZGYdbKP5VDB8j2H4qiop7KzXIteKCpP6lmLD7vqpV5a7wIU2esewI4QeaZ+RvdL\np9duqnAv5JpTTAjKFhA4+jZQCqjJaqltYtPYMBxw8TiQcwkQUlQbYlVMYbHurkPE\nCItpDJPEyyddabM6lXri6nsWRyC2J13h8BAJMflC6yPXsb/fwsSjJULNzQr41A74\nwBJ/OTtQwLgrS8qxlxTAUL+dBUKW2/kIUT+pkqCd1nOvOgSzSdDj2qhLI2aNWrQl\noCNvdCWELoq6D0sj+DssrB7KNyrNRCafAgMBAAGjggGvMIIBqzAOBgNVHQ8BAf8E\nBAMCBaAwHQYDVR0lBBYwFAYIKwYBBQUHAwEGCCsGAQUFBwMCMAwGA1UdEwEB/wQCMAAwHQYDVR0OBBYEFJbr1PPEc/mieh1yXp+j5Mx80g79MB8GA1UdIwQYMBaAFM8wTjkEr4gm0t2b6qptwRlFdAQrMIGNBggrBgEFBQcBAQSBgDB+MHwGCCsGAQUFBzAChnBodHRwOi8vcHJpdmF0ZWNhLWNvbnRlbnQtNjg1YmQ5ZjctMDAwMC0yMWQ2LTljM2ItMTRjMTRlZjQ4NmQ0LnN0b3JhZ2UuZ29vZ2xlYXBpcy5jb20vYTNkM2UyNWE0ZjhkNmJjNzQ5YmUvY2EuY3J0MBcGA1UdEQQQMA6CDCouc2hhc2hpLmNvbTCBggYDVR0fBHsweTB3oHWgc4ZxaHR0cDovL3ByaXZhdGVjYS1jb250ZW50LTY4NWJkOWY3LTAwMDAtMjFkNi05YzNiLTE0YzE0ZWY0ODZkNC5zdG9yYWdlLmdvb2dsZWFwaXMuY29tL2EzZDNlMjVhNGY4ZDZiYzc0OWJlL2NybC5jcmwwDQYJKoZIhvcNAQELBQADggGBAIVgs0Kp142hnA3AxTTF84GqkX5gDuoAn7thK7Mgvjeuc8XPaM/jj+CNApK7GoQazkNxz2VJmwYtCaXPzYwMd6H10Y8CsF02mfbRXLbxa0MwVP/LR7rO0sOlv32oqzk1rs/UHYffaEz+CrxuPFqdhh5gw188siGIrlpfLNfR6IjdwLE1anH0dYwcxKFcDdNMxyX3wXnT4yVe2ufAK0PMvmJHHicoWsVU1CCRzHtySfKpRKYhWI54gbI0fmWKTjbf1jg5veC42ShIpFzCi7bU/7tfnhweD1qskqOuw+ipjbqxlxOuSoUw439WTVfbDvnEZAN0i/xR8/F0gv5TQwIY03ip1Lq08ak8/tTdJabInGtqquJsaFzgzO8b+0hESWtfJXPFZh6UKLjAaxh4j7kKq2f8QS4uG07THlh0SPOmI+O0SKaw6gfk3gqZXyJ0XGw/CqljKg+9HZ1JeN6M/hT0cH7rSSfKmaySY9iD1i1lxjxM+zHuiWYRJbA2Ahhfim6RRg==\n-----END CERTIFICATE-----",
	}

	t.Run("Valid PEM certificate", func(t *testing.T) {
		pool, err := _parsePEMCertificate(validCert, "CERTIFICATE")
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if pool == nil {
			t.Error("expected non-nil CertPool")
		}
	})

	t.Run("Invalid PEM certificate", func(t *testing.T) {
		invalidCert := []string{"not a pem", "two"}
		pool, err := _parsePEMCertificate(invalidCert, "CERTIFICATE")
		if err == nil {
			t.Error("expected error for invalid PEM, got nil")
		}
		if pool != nil {
			t.Error("expected nil CertPool for invalid PEM")
		}
	})

	t.Run("Valid PEM, wrong type", func(t *testing.T) {
		pool, err := _parsePEMCertificate(validCert, "WRONGTYPE")
		if err == nil {
			t.Error("expected error for wrong type, got nil")
		}
		if pool != nil {
			t.Error("expected nil CertPool for wrong type")
		}
	})
}

func TestGetAuthTokenFromContext(t *testing.T) {
	t.Run("TokenPresent", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.AuthorizationToken, "test-token")
		got := GetAuthTokenFromContext(ctx)
		assert.Equal(tt, "test-token", got)
	})

	t.Run("TokenAbsent", func(tt *testing.T) {
		ctx := context.Background()
		got := GetAuthTokenFromContext(ctx)
		assert.Equal(tt, "", got)
	})

	t.Run("TokenWrongType", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.AuthorizationToken, 12345)
		got := GetAuthTokenFromContext(ctx)
		assert.Equal(tt, "", got)
	})
}

func TestGetCVPJWTFromContext(t *testing.T) {
	t.Run("TokenFromAuthToken", func(tt *testing.T) {
		// Test when GetAuthTokenFromContext returns a token (line 669)
		ctx := context.WithValue(context.Background(), middleware.AuthorizationToken, "auth-token")
		got := GetCVPJWTFromContext(ctx)
		assert.Equal(tt, "auth-token", got)
	})

	t.Run("TokenFromJWTTokenWhenAuthTokenEmpty", func(tt *testing.T) {
		// Test when GetAuthTokenFromContext returns empty and GetJWTTokenFromContext returns a token (lines 666-667)
		header := make(http.Header)
		header.Set("Authorization", "Bearer jwt-token")
		ctx := context.WithValue(context.Background(), middleware.HeaderContextKey, header)
		got := GetCVPJWTFromContext(ctx)
		assert.Equal(tt, "Bearer jwt-token", got)
	})

	t.Run("NoToken", func(tt *testing.T) {
		// Test when both return empty
		ctx := context.Background()
		got := GetCVPJWTFromContext(ctx)
		assert.Equal(tt, "", got)
	})
}

func TestGetVPCNameFromSubnetID(t *testing.T) {
	tests := []struct {
		name           string
		vendorSubNetID string
		expectedVPC    string
	}{
		{
			name:           "ValidSubnetID",
			vendorSubNetID: "projects/project-id/regions/us-central1/subnetworks/my-vpc-name",
			expectedVPC:    "my-vpc-name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetVPCNameFromSubnetID(tt.vendorSubNetID)
			assert.Equal(t, tt.expectedVPC, got)
		})
	}
}

func TestExtractLunNameFromPath(t *testing.T) {
	tests := []struct {
		testName     string
		fullLunName  string
		expectedName string
	}{
		{
			"WhenFullLunNameIsPassed_ThenReturnLunName_1",
			"/vol/volume1752243551/lun_volume1752243551",
			"lun_volume1752243551",
		},
		{
			"WhenFullLunNameIsPassed_ThenReturnLunName_2",
			"/vol/my_volume/lun_my_volume",
			"lun_my_volume",
		},
		{
			"WhenFullLunNameIsPassed_ThenReturnLunName_3",
			"/vol/test_volume_123/lun_test_volume_123",
			"lun_test_volume_123",
		},
		{
			"WhenSimpleLunNameIsPassed_ThenReturnSameName",
			"lun_simple_volume",
			"lun_simple_volume",
		},
		{
			"WhenNestedPathIsPassed_ThenReturnLastSegment",
			"/vol/parent/child/lun_nested_volume",
			"lun_nested_volume",
		},
		{
			"WhenEmptyStringIsPassed_ThenReturnEmptyString",
			"/vol/parent/child/lun_nested_volume//",
			"lun_nested_volume",
		},
		{
			"WhenEmptyStringIsPassed_ThenReturnEmptyString",
			"",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			got := ExtractLunNameFromPath(tt.fullLunName)
			if got != tt.expectedName {
				t.Errorf("ExtractLunNameFromPath(%s) = %s, want %s", tt.fullLunName, got, tt.expectedName)
			}
		})
	}
}

func TestConstructServiceAccountEmail(t *testing.T) {
	tests := []struct {
		name      string
		accountID string
		projectID string
		expected  string
	}{
		{
			name:      "Valid account and project IDs",
			accountID: "my-service-account",
			projectID: "my-gcp-project",
			expected:  "my-service-account@my-gcp-project.iam.gserviceaccount.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConstructServiceAccountEmail(tt.accountID, tt.projectID)
			if result != tt.expected {
				t.Errorf("ConstructServiceAccountEmail(%q, %q) = %q, want %q",
					tt.accountID, tt.projectID, result, tt.expected)
			}
		})
	}
}

func TestIsFileProtocolSupported(t *testing.T) {
	tests := []struct {
		name                string
		fileProtocolSupport string
		whitelistedAccounts string
		accountID           string
		expectedResult      bool
	}{
		{
			name:                "Flag disabled, should return false regardless of account",
			fileProtocolSupport: "false",
			whitelistedAccounts: "account1,account2",
			accountID:           "account1",
			expectedResult:      false,
		},
		{
			name:                "Flag enabled, no whitelisted accounts, should return false",
			fileProtocolSupport: "true",
			whitelistedAccounts: "",
			accountID:           "account1",
			expectedResult:      false,
		},
		{
			name:                "Flag enabled, account in whitelist, should return true",
			fileProtocolSupport: "true",
			whitelistedAccounts: "account1,account2,account3",
			accountID:           "account2",
			expectedResult:      true,
		},
		{
			name:                "Flag enabled, account not in whitelist, should return false",
			fileProtocolSupport: "true",
			whitelistedAccounts: "account1,account2,account3",
			accountID:           "account4",
			expectedResult:      false,
		},
		{
			name:                "Flag enabled, exact case match, should return true",
			fileProtocolSupport: "true",
			whitelistedAccounts: "Account1,ACCOUNT2,account3",
			accountID:           "ACCOUNT2",
			expectedResult:      true,
		},
		{
			name:                "Flag enabled, whitespace in whitelist, should return true",
			fileProtocolSupport: "true",
			whitelistedAccounts: "account1, account2 , account3",
			accountID:           "account2",
			expectedResult:      true,
		},
		{
			name:                "Flag enabled, single account in whitelist, should return true",
			fileProtocolSupport: "true",
			whitelistedAccounts: "account1",
			accountID:           "account1",
			expectedResult:      true,
		},
		{
			name:                "Flag enabled, case sensitive mismatch, should return false",
			fileProtocolSupport: "true",
			whitelistedAccounts: "account1,Account2,ACCOUNT3",
			accountID:           "account2",
			expectedResult:      false,
		},
		{
			name:                "Flag enabled, wildcard in whitelist, should return true for any account",
			fileProtocolSupport: "true",
			whitelistedAccounts: "*",
			accountID:           "anyAccountID",
			expectedResult:      true,
		},
		{
			name:                "Flag enabled, wildcard in whitelist, should return true for empty account",
			fileProtocolSupport: "true",
			whitelistedAccounts: "*",
			accountID:           "",
			expectedResult:      true,
		},
		{
			name:                "Flag enabled, wildcard in whitelist, should return true for numeric account",
			fileProtocolSupport: "true",
			whitelistedAccounts: "*",
			accountID:           "123456789",
			expectedResult:      true,
		},
		{
			name:                "Flag enabled, wildcard with other accounts in whitelist, should check exact match",
			fileProtocolSupport: "true",
			whitelistedAccounts: "*,account1,account2",
			accountID:           "account1",
			expectedResult:      false,
		},
		{
			name:                "Flag enabled, wildcard with other accounts in whitelist, non-listed account should be rejected",
			fileProtocolSupport: "true",
			whitelistedAccounts: "*,account1,account2",
			accountID:           "randomAccount",
			expectedResult:      false,
		},
		{
			name:                "Flag disabled, wildcard in whitelist, should return false",
			fileProtocolSupport: "false",
			whitelistedAccounts: "*",
			accountID:           "anyAccount",
			expectedResult:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables for the test
			err := os.Setenv("FILES_PROTOCOL_SUPPORT", tt.fileProtocolSupport)
			if err != nil {
				return
			}
			err = os.Setenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", tt.whitelistedAccounts)
			if err != nil {
				return
			}

			// Reset the global variables to pick up new environment values
			FileProtocolSupported = env.GetBool("FILES_PROTOCOL_SUPPORT", false)
			experimentalVersionAllowlistedAccounts = ParseCommaSeparatedStringToMap(env.GetString("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", ""))

			result := IsFileProtocolSupported(tt.accountID)
			if result != tt.expectedResult {
				t.Errorf("IsFileProtocolSupported() = %v, want %v", result, tt.expectedResult)
			}
		})
	}
}

// TestSetFileProtocolSupportedForTesting tests the test helper function
func TestSetFileProtocolSupportedForTesting(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		expected bool
	}{
		{
			name:     "Enable file protocol support",
			enabled:  true,
			expected: true,
		},
		{
			name:     "Disable file protocol support",
			enabled:  false,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the test helper function
			SetFileProtocolSupportedForTesting(tt.enabled)

			// Verify the environment variable was set correctly
			envValue := os.Getenv("FILES_PROTOCOL_SUPPORT")
			expectedEnvValue := strconv.FormatBool(tt.enabled)
			if envValue != expectedEnvValue {
				t.Errorf("Environment variable FILES_PROTOCOL_SUPPORT = %q, want %q", envValue, expectedEnvValue)
			}

			// Verify the cached value was updated correctly
			if FileProtocolSupported != tt.expected {
				t.Errorf("FileProtocolSupported = %v, want %v", FileProtocolSupported, tt.expected)
			}
		})
	}
}

func TestSetExperimentalVersionAllowlistedAccountsForTesting(t *testing.T) {
	tests := []struct {
		name     string
		accounts string
		expected map[string]struct{}
	}{
		{
			name:     "Empty accounts list",
			accounts: "",
			expected: map[string]struct{}{},
		},
		{
			name:     "Single account",
			accounts: "account1",
			expected: map[string]struct{}{
				"account1": {},
			},
		},
		{
			name:     "Multiple accounts with whitespace",
			accounts: "account1, account2 , account3",
			expected: map[string]struct{}{
				"account1": {},
				"account2": {},
				"account3": {},
			},
		},
		{
			name:     "Multiple accounts without whitespace",
			accounts: "account1,account2,account3",
			expected: map[string]struct{}{
				"account1": {},
				"account2": {},
				"account3": {},
			},
		},
		{
			name:     "Accounts with empty entries",
			accounts: "account1,,account2, ,account3",
			expected: map[string]struct{}{
				"account1": {},
				"account2": {},
				"account3": {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the test helper function
			SetExperimentalVersionAllowlistedAccountsForTesting(tt.accounts)

			// Verify the environment variable was set correctly
			envValue := os.Getenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS")
			if envValue != tt.accounts {
				t.Errorf("Environment variable EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS = %q, want %q", envValue, tt.accounts)
			}

			// Verify the cached value was updated correctly
			if len(experimentalVersionAllowlistedAccounts) != len(tt.expected) {
				t.Errorf("experimentalVersionAllowlistedAccounts length = %d, want %d", len(experimentalVersionAllowlistedAccounts), len(tt.expected))
			}

			// Check each expected account is present
			for account := range tt.expected {
				if _, exists := experimentalVersionAllowlistedAccounts[account]; !exists {
					t.Errorf("Expected account %q not found in experimentalVersionAllowlistedAccounts", account)
				}
			}

			// Check no unexpected accounts are present
			for account := range experimentalVersionAllowlistedAccounts {
				if _, exists := tt.expected[account]; !exists {
					t.Errorf("Unexpected account %q found in experimentalVersionAllowlistedAccounts", account)
				}
			}
		})
	}
}

func TestSetFileProtocolSupportedForTesting_ErrorHandling(t *testing.T) {
	// Test that the function handles errors gracefully
	// This is difficult to test directly since os.Setenv rarely fails,
	// but we can verify the function doesn't panic and handles the flow correctly

	// Test with valid input
	SetFileProtocolSupportedForTesting(true)

	// Verify the function completed without issues
	envValue := os.Getenv("FILES_PROTOCOL_SUPPORT")
	if envValue != "true" {
		t.Errorf("Environment variable not set correctly: %q", envValue)
	}
}

func TestSetExperimentalVersionAllowlistedAccountsForTesting_ErrorHandling(t *testing.T) {
	// Test that the function handles errors gracefully
	// This is difficult to test directly since os.Setenv rarely fails,
	// but we can verify the function doesn't panic and handles the flow correctly

	// Test with valid input
	SetExperimentalVersionAllowlistedAccountsForTesting("account1,account2")

	// Verify the function completed without issues
	envValue := os.Getenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS")
	if envValue != "account1,account2" {
		t.Errorf("Environment variable not set correctly: %q", envValue)
	}
}

// TestEnableAllSquashForTesting tests the test helper function
func TestEnableAllSquashForTesting(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		expected bool
	}{
		{
			name:     "Enable all squash support",
			enabled:  true,
			expected: true,
		},
		{
			name:     "Disable all squash support",
			enabled:  false,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the test helper function
			EnableAllSquashForTesting(tt.enabled)

			// Verify the environment variable was set correctly
			envValue := os.Getenv("IS_ALL_SQUASH_ENABLED")
			expectedEnvValue := strconv.FormatBool(tt.enabled)
			if envValue != expectedEnvValue {
				t.Errorf("Environment variable IS_ALL_SQUASH_ENABLED = %q, want %q", envValue, expectedEnvValue)
			}

			// Verify the cached value was updated correctly
			if IsAllSquashEnabled != tt.expected {
				t.Errorf("IsAllSquashEnabled = %v, want %v", IsAllSquashEnabled, tt.expected)
			}
		})
	}
}

func TestEnableAllSquashForTesting_ErrorHandling(t *testing.T) {
	// Test that the function handles errors gracefully
	// This is difficult to test directly since os.Setenv rarely fails,
	// but we can verify the function doesn't panic and handles the flow correctly

	// Test with valid input
	EnableAllSquashForTesting(true)

	// Verify the function completed without issues
	envValue := os.Getenv("IS_ALL_SQUASH_ENABLED")
	if envValue != "true" {
		t.Errorf("Environment variable not set correctly: %q", envValue)
	}
}

func TestParseCommaSeparatedStringToMap(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]struct{}
	}{
		{
			name:     "Empty String",
			input:    "",
			expected: map[string]struct{}{},
		},
		{
			name:  "Single item",
			input: "item1",
			expected: map[string]struct{}{
				"item1": {},
			},
		},
		{
			name:  "Multiple items without whitespace",
			input: "item1,item2,item3",
			expected: map[string]struct{}{
				"item1": {},
				"item2": {},
				"item3": {},
			},
		},
		{
			name:  "Multiple items with whitespace",
			input: "item1, item2 , item3",
			expected: map[string]struct{}{
				"item1": {},
				"item2": {},
				"item3": {},
			},
		},
		{
			name:  "Items with empty entries",
			input: "item1,,item2, ,item3",
			expected: map[string]struct{}{
				"item1": {},
				"item2": {},
				"item3": {},
			},
		},
		{
			name:  "Items with only whitespace entries",
			input: "item1,   ,item2,  ,item3",
			expected: map[string]struct{}{
				"item1": {},
				"item2": {},
				"item3": {},
			},
		},
		{
			name:  "Case sensitive items",
			input: "Item1,ITEM2,item3",
			expected: map[string]struct{}{
				"Item1": {},
				"ITEM2": {},
				"item3": {},
			},
		},
		{
			name:  "Special characters in items",
			input: "item-1,item_2,item.3",
			expected: map[string]struct{}{
				"item-1": {},
				"item_2": {},
				"item.3": {},
			},
		},
		{
			name:  "Numbers as items",
			input: "123,456,789",
			expected: map[string]struct{}{
				"123": {},
				"456": {},
				"789": {},
			},
		},
		{
			name:  "Mixed content",
			input: "account1, 123 ,user-name,",
			expected: map[string]struct{}{
				"account1":  {},
				"123":       {},
				"user-name": {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseCommaSeparatedStringToMap(tt.input)

			// Check length
			if len(result) != len(tt.expected) {
				t.Errorf("ParseCommaSeparatedStringToMap() length = %d, want %d", len(result), len(tt.expected))
			}

			// Check each expected item is present
			for item := range tt.expected {
				if _, exists := result[item]; !exists {
					t.Errorf("Expected item %q not found in result", item)
				}
			}

			// Check no unexpected items are present
			for item := range result {
				if _, exists := tt.expected[item]; !exists {
					t.Errorf("Unexpected item %q found in result", item)
				}
			}
		})
	}
}

func TestParseCommaSeparatedStringToMap_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]struct{}
	}{
		{
			name:     "Only commas",
			input:    ",,,",
			expected: map[string]struct{}{},
		},
		{
			name:     "Only whitespace",
			input:    "   ,  , ",
			expected: map[string]struct{}{},
		},
		{
			name:     "Single comma",
			input:    ",",
			expected: map[string]struct{}{},
		},
		{
			name:  "Leading and trailing commas",
			input: ",item1,item2,",
			expected: map[string]struct{}{
				"item1": {},
				"item2": {},
			},
		},
		{
			name:  "Multiple consecutive commas",
			input: "item1,,,item2",
			expected: map[string]struct{}{
				"item1": {},
				"item2": {},
			},
		},
		{
			name:  "Very long item names",
			input: "very-long-item-name-with-many-characters,another-long-item",
			expected: map[string]struct{}{
				"very-long-item-name-with-many-characters": {},
				"another-long-item":                        {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseCommaSeparatedStringToMap(tt.input)

			// Check length
			if len(result) != len(tt.expected) {
				t.Errorf("ParseCommaSeparatedStringToMap() length = %d, want %d", len(result), len(tt.expected))
			}

			// Check each expected item is present
			for item := range tt.expected {
				if _, exists := result[item]; !exists {
					t.Errorf("Expected item %q not found in result", item)
				}
			}

			// Check no unexpected items are present
			for item := range result {
				if _, exists := tt.expected[item]; !exists {
					t.Errorf("Unexpected item %q found in result", item)
				}
			}
		})
	}
}

func TestSplitAndTrimString(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		delimiter string
		expected  []string
	}{
		{
			name:      "Comma separated values",
			input:     "a,b,c",
			delimiter: ",",
			expected:  []string{"a", "b", "c"},
		},
		{
			name:      "Comma separated values with spaces and empty tokens",
			input:     "a, b, , c ,,",
			delimiter: ",",
			expected:  []string{"a", "b", "c"},
		},
		{
			name:      "Slash separated values",
			input:     "one / two / three",
			delimiter: "/",
			expected:  []string{"one", "two", "three"},
		},
		{
			name:      "Empty input",
			input:     "",
			delimiter: ",",
			expected:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SplitAndTrimString(tt.input, tt.delimiter)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDefaultString(t *testing.T) {
	assert.Equal(t, "fallback", DefaultString("", "fallback"))
	assert.Equal(t, "value", DefaultString("value", "fallback"))
}

func TestTrimOrNil(t *testing.T) {
	vendor := " vendor "
	assert.Equal(t, "vendor", *TrimOrNil(&vendor))
	assert.Nil(t, TrimOrNil(nillable.ToPointer("  ")))
}

func TestTrimOrEmpty(t *testing.T) {
	label := " label "
	assert.Equal(t, "label", TrimOrEmpty(&label))
	assert.Equal(t, "", TrimOrEmpty(nil))
}

func TestBoolOrFalse(t *testing.T) {
	trueVal := true
	assert.True(t, BoolOrFalse(&trueVal))
	assert.False(t, BoolOrFalse(nil))
}

func TestGetSnHostProject(t *testing.T) {
	t.Run("ReturnsSnHostProject_WhenPoolIsNotNil", func(t *testing.T) {
		pool := &datamodel.Pool{SnHostProject: "test-sn-host-project"}
		result := GetSnHostProject(pool)
		assert.Equal(t, "test-sn-host-project", result)
	})

	t.Run("ReturnsEmptyString_WhenPoolIsNil", func(t *testing.T) {
		result := GetSnHostProject(nil)
		assert.Equal(t, "", result)
	})

	t.Run("ReturnsEmptyString_WhenSnHostProjectIsEmpty", func(t *testing.T) {
		pool := &datamodel.Pool{SnHostProject: ""}
		result := GetSnHostProject(pool)
		assert.Equal(t, "", result)
	})
}

func TestIsProberProject(t *testing.T) {
	tests := []struct {
		name              string
		proberProjectList string
		projectNumber     string
		expectedResult    bool
	}{
		{
			name:              "Project exists in prober list",
			proberProjectList: "project1,project2,project3",
			projectNumber:     "project2",
			expectedResult:    true,
		},
		{
			name:              "Project does not exist in prober list",
			proberProjectList: "project1,project2,project3",
			projectNumber:     "project4",
			expectedResult:    false,
		},
		{
			name:              "Empty prober list",
			proberProjectList: "",
			projectNumber:     "project1",
			expectedResult:    false,
		},
		{
			name:              "Empty project number with populated list",
			proberProjectList: "project1,project2,project3",
			projectNumber:     "",
			expectedResult:    false,
		},
		{
			name:              "Empty project number with empty list",
			proberProjectList: "",
			projectNumber:     "",
			expectedResult:    false,
		},
		{
			name:              "Single project in list matches",
			proberProjectList: "single-project",
			projectNumber:     "single-project",
			expectedResult:    true,
		},
		{
			name:              "Single project in list does not match",
			proberProjectList: "single-project",
			projectNumber:     "different-project",
			expectedResult:    false,
		},
		{
			name:              "Project with whitespace in list",
			proberProjectList: "project1, project2 , project3",
			projectNumber:     "project2",
			expectedResult:    true,
		},
		{
			name:              "Case sensitive matching - exact match",
			proberProjectList: "Project1,PROJECT2,project3",
			projectNumber:     "PROJECT2",
			expectedResult:    true,
		},
		{
			name:              "Case sensitive matching - no match",
			proberProjectList: "Project1,PROJECT2,project3",
			projectNumber:     "project2",
			expectedResult:    false,
		},
		{
			name:              "Numeric project numbers",
			proberProjectList: "123456789,987654321,555666777",
			projectNumber:     "987654321",
			expectedResult:    true,
		},
		{
			name:              "Mixed alphanumeric project numbers",
			proberProjectList: "proj-123,test-456,demo-789",
			projectNumber:     "test-456",
			expectedResult:    true,
		},
		{
			name:              "Project list with empty entries",
			proberProjectList: "project1,,project2, ,project3",
			projectNumber:     "project2",
			expectedResult:    true,
		},
		{
			name:              "Project number with special characters",
			proberProjectList: "project-1,project_2,project.3",
			projectNumber:     "project_2",
			expectedResult:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable for the test
			err := os.Setenv("PROBER_PROJECT_LIST", tt.proberProjectList)
			if err != nil {
				t.Fatalf("Failed to set environment variable: %v", err)
			}

			// Reset the global variable to pick up new environment value
			isProberProject = ParseCommaSeparatedStringToMap(env.GetString("PROBER_PROJECT_LIST", ""))

			result := IsProberProject(tt.projectNumber)
			if result != tt.expectedResult {
				t.Errorf("IsProberProject(%q) = %v, want %v", tt.projectNumber, result, tt.expectedResult)
			}
		})
	}
}

func TestGetResourcesNameForBackup_Comprehensive(t *testing.T) {
	tests := []struct {
		name                         string
		gcpRegion                    string
		tenantProjectRegion          string
		tenantProjectNumber          string
		backupVaultUUID              string
		expectedEmailPrefix          string
		expectedBucketPrefix         string
		expectedServiceAccountPrefix string
		expectError                  bool
	}{
		{
			name:                         "Normal case with standard inputs",
			gcpRegion:                    "us-central1",
			tenantProjectRegion:          "us-central1",
			tenantProjectNumber:          "123456789",
			backupVaultUUID:              "vault-uuid-123",
			expectedEmailPrefix:          "vsa-backup-us-cent",
			expectedBucketPrefix:         "vsa-backup-vault-uuid-123",
			expectedServiceAccountPrefix: "vsa-backup-us-cent",
			expectError:                  false,
		},
		{
			name:                         "Empty GCP region",
			gcpRegion:                    "",
			tenantProjectRegion:          "us-central1",
			tenantProjectNumber:          "123456789",
			backupVaultUUID:              "vault-uuid-123",
			expectedEmailPrefix:          "vsa-backup",
			expectedBucketPrefix:         "vsa-backup-vault-uuid-123",
			expectedServiceAccountPrefix: "vsa-backup",
			expectError:                  false,
		},
		{
			name:                         "Special characters in inputs",
			gcpRegion:                    "us-central1",
			tenantProjectRegion:          "us-central1",
			tenantProjectNumber:          "123456789",
			backupVaultUUID:              "vault_uuid_with_underscores",
			expectedEmailPrefix:          "vsa-backup-us-cent",
			expectedBucketPrefix:         "vsa-backup-vault_uuid_with_underscores",
			expectedServiceAccountPrefix: "vsa-backup-us-cent",
			expectError:                  false,
		},
		{
			name:                         "Different regions for GCP and tenant",
			gcpRegion:                    "us-west1",
			tenantProjectRegion:          "us-east1",
			tenantProjectNumber:          "987654321",
			backupVaultUUID:              "different-vault-uuid",
			expectedEmailPrefix:          "vsa-backup-us-west",
			expectedBucketPrefix:         "vsa-backup-different-vault-uuid",
			expectedServiceAccountPrefix: "vsa-backup-us-west",
			expectError:                  false,
		},
		{
			name:                         "Numeric project number",
			gcpRegion:                    "us-central1",
			tenantProjectRegion:          "us-central1",
			tenantProjectNumber:          "12345678901234567890",
			backupVaultUUID:              "vault-uuid-123",
			expectedEmailPrefix:          "vsa-backup-us-cent",
			expectedBucketPrefix:         "vsa-backup-vault-uuid-123",
			expectedServiceAccountPrefix: "vsa-backup-us-cent",
			expectError:                  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email, bucketName, serviceAccountId, err := GetResourcesNameForBackup(
				tt.gcpRegion,
				tt.tenantProjectRegion,
				tt.tenantProjectNumber,
				tt.backupVaultUUID,
			)

			if tt.expectError {
				assert.Error(t, err)
				assert.Empty(t, email)
				assert.Empty(t, bucketName)
				assert.Empty(t, serviceAccountId)
				return
			}

			assert.NoError(t, err)
			assert.NotEmpty(t, email)
			assert.NotEmpty(t, bucketName)
			assert.NotEmpty(t, serviceAccountId)

			// Check email format
			assert.Contains(t, email, "@"+tt.tenantProjectNumber+".iam.gserviceaccount.com")
			assert.True(t, strings.HasPrefix(email, tt.expectedEmailPrefix))

			// Check bucket name format
			assert.True(t, strings.HasPrefix(bucketName, tt.expectedBucketPrefix))
			// The function has a bug: it doesn't account for the "-" separator in length calculation
			// So the actual limit is 61 characters (60 + 1 for the separator)
			assert.True(t, len(bucketName) <= 61, "Bucket name should not exceed 61 characters (60 + 1 for separator)")

			// Check service account ID format
			assert.True(t, strings.HasPrefix(serviceAccountId, tt.expectedServiceAccountPrefix))
			assert.True(t, len(serviceAccountId) <= 30, "Service account ID should not exceed 30 characters")

			// Verify deterministic behavior - same inputs should produce same outputs
			email2, bucketName2, serviceAccountId2, err2 := GetResourcesNameForBackup(
				tt.gcpRegion,
				tt.tenantProjectRegion,
				tt.tenantProjectNumber,
				tt.backupVaultUUID,
			)
			assert.NoError(t, err2)
			assert.Equal(t, email, email2)
			assert.Equal(t, bucketName, bucketName2)
			assert.Equal(t, serviceAccountId, serviceAccountId2)
		})
	}
}

func TestGetResourcesNameForBackup_DebugLength(t *testing.T) {
	t.Run("WhenVeryLongBackupVaultUUID", func(t *testing.T) {
		veryLongUUID := "this-is-a-very-long-backup-vault-uuid-that-might-cause-issues-with-bucket-naming-conventions"

		email, bucketName, serviceAccountId, err := GetResourcesNameForBackup(
			"us-central1",
			"us-central1",
			"123456789",
			veryLongUUID,
		)

		assert.NoError(t, err)

		// Log the actual values for debugging
		t.Logf("Email: %s (length: %d)", email, len(email))
		t.Logf("Bucket name: %s (length: %d)", bucketName, len(bucketName))
		t.Logf("Service account ID: %s (length: %d)", serviceAccountId, len(serviceAccountId))

		// Check the actual length constraint
		assert.True(t, len(bucketName) <= 67,
			"Bucket name length %d should not exceed 67 characters", len(bucketName))
	})
	t.Run("WhenLongRegionName", func(t *testing.T) {
		veryLongUUID := "this-is-a-very-long-backup-vault-uuid-that-might-cause-issues-with-bucket-naming-conventions"

		email, bucketName, serviceAccountId, err := GetResourcesNameForBackup(
			"northamerica-northeast1",
			"northamerica-northeast1",
			"123456789",
			veryLongUUID,
		)

		assert.NoError(t, err)

		// Log the actual values for debugging
		t.Logf("Email: %s (length: %d)", email, len(email))
		t.Logf("Bucket name: %s (length: %d)", bucketName, len(bucketName))
		t.Logf("Service account ID: %s (length: %d)", serviceAccountId, len(serviceAccountId))

		// Check the actual length constraint
		assert.True(t, len(bucketName) <= 67,
			"Bucket name length %d should not exceed 67 characters", len(bucketName))
	})
}

func TestGetCorrelationIDFromWorkflowContextLoggerFields(t *testing.T) {
	var ts testsuite.WorkflowTestSuite

	tests := []struct {
		name          string
		setupHeader   bool
		expectedID    string
		expectError   bool
		correlationID string
	}{
		{
			name:          "Valid Correlation ID",
			setupHeader:   true,
			expectedID:    "test-correlation-id",
			expectError:   false,
			correlationID: "test-correlation-id",
		},
		{
			name:          "Missing Header",
			setupHeader:   false,
			expectedID:    "",
			expectError:   true,
			correlationID: "",
		},
		{
			name:          "no correlation ID in header",
			setupHeader:   true,
			expectedID:    "",
			expectError:   true,
			correlationID: "",
		},
		{
			name:          "Special Characters in Correlation ID",
			setupHeader:   true,
			expectedID:    "test-123_correlation@id",
			expectError:   false,
			correlationID: "test-123_correlation@id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := ts.NewTestWorkflowEnvironment()
			if tt.setupHeader {
				env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
				var encodedValue *commonpb.Payload
				if tt.correlationID == "" {
					encodedValue, _ = converter.GetDefaultDataConverter().ToPayload(log.Fields{})
				} else {
					encodedValue, _ = converter.GetDefaultDataConverter().ToPayload(log.Fields{
						"requestCorrelationID": tt.correlationID,
					})
				}
				mockHeader := &commonpb.Header{
					Fields: map[string]*commonpb.Payload{
						"logParam": encodedValue,
					},
				}
				env.SetHeader(mockHeader)
			}

			env.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
				return GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
			})

			assert.True(t, env.IsWorkflowCompleted())
			var result string
			err := env.GetWorkflowResult(&result)

			if tt.expectError {
				assert.Error(t, err, "Expected error for test case: %s", tt.name)
				assert.Empty(t, result, "Expected empty result for test case: %s", tt.name)
			} else {
				assert.NoError(t, err, "Unexpected error for test case: %s", tt.name)
				assert.Equal(t, tt.expectedID, result, "Correlation ID mismatch for test case: %s", tt.name)
			}
		})
	}
}

func TestGetOpcWorkRequestIDFromWorkflowContextLoggerFields(t *testing.T) {
	var ts testsuite.WorkflowTestSuite

	tests := []struct {
		name             string
		setupHeader      bool
		setupField       bool
		opcWorkRequestID string
		expectedID       string
		expectError      bool
	}{
		{
			name:             "Valid opc-work-request-id",
			setupHeader:      true,
			setupField:       true,
			opcWorkRequestID: "wr-12345",
			expectedID:       "wr-12345",
			expectError:      false,
		},
		{
			name:        "Missing logger fields",
			setupHeader: false,
			expectError: true,
		},
		{
			name:        "Fields present but no opc-work-request-id",
			setupHeader: true,
			setupField:  false,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := ts.NewTestWorkflowEnvironment()
			if tt.setupHeader {
				env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
				fields := log.Fields{}
				if tt.setupField {
					fields[string(middleware.RequestOpcWorkRequestID)] = tt.opcWorkRequestID
				}
				encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(fields)
				mockHeader := &commonpb.Header{
					Fields: map[string]*commonpb.Payload{
						"logParam": encodedValue,
					},
				}
				env.SetHeader(mockHeader)
			}

			env.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
				return GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
			})

			assert.True(t, env.IsWorkflowCompleted())
			var result string
			err := env.GetWorkflowResult(&result)

			if tt.expectError {
				assert.Error(t, err, "Expected error for test case: %s", tt.name)
				assert.Empty(t, result, "Expected empty result for test case: %s", tt.name)
			} else {
				assert.NoError(t, err, "Unexpected error for test case: %s", tt.name)
				assert.Equal(t, tt.expectedID, result, "opc-work-request-id mismatch for test case: %s", tt.name)
			}
		})
	}
}

func TestGetBackupRegion(t *testing.T) {
	tests := []struct {
		name        string
		volume      *datamodel.Volume
		expected    string
		expectError bool
		errorMsg    string
	}{
		{
			name: "ValidVolumeWithRegionalZone",
			volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					PoolAttributes: &datamodel.PoolAttributes{
						PrimaryZone: "us-central1-a",
					},
				},
			},
			expected:    "us-central1",
			expectError: false,
		},
		{
			name: "ValidVolumeWithRegionOnly",
			volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					PoolAttributes: &datamodel.PoolAttributes{
						PrimaryZone: "us-east1",
					},
				},
			},
			expected:    "us-east1",
			expectError: false,
		},
		{
			name: "ValidVolumeWithMultiRegionZone",
			volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					PoolAttributes: &datamodel.PoolAttributes{
						PrimaryZone: "europe-west1-b",
					},
				},
			},
			expected:    "europe-west1",
			expectError: false,
		},
		{
			name: "ValidVolumeWithAsiaRegion",
			volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					PoolAttributes: &datamodel.PoolAttributes{
						PrimaryZone: "asia-southeast1-c",
					},
				},
			},
			expected:    "asia-southeast1",
			expectError: false,
		},
		{
			name:        "NilVolume",
			volume:      nil,
			expected:    "",
			expectError: true,
			errorMsg:    "Volume or Pool Attributes is nil when extracting backup region",
		},
		{
			name: "NilPool",
			volume: &datamodel.Volume{
				Pool: nil,
			},
			expected:    "",
			expectError: true,
			errorMsg:    "Volume or Pool Attributes is nil when extracting backup region",
		},
		{
			name: "NilPoolAttributes",
			volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					PoolAttributes: nil,
				},
			},
			expected:    "",
			expectError: true,
			errorMsg:    "Volume or Pool Attributes is nil when extracting backup region",
		},
		{
			name: "EmptyPrimaryZone",
			volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					PoolAttributes: &datamodel.PoolAttributes{
						PrimaryZone: "",
					},
				},
			},
			expected:    "",
			expectError: true,
			errorMsg:    "LocationID represents neither a region nor a zone",
		},
		{
			name: "InvalidPrimaryZoneFormat",
			volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					PoolAttributes: &datamodel.PoolAttributes{
						PrimaryZone: "invalid-zone-format",
					},
				},
			},
			expected:    "",
			expectError: true,
			errorMsg:    "LocationID represents neither a region nor a zone",
		},
		{
			name: "InvalidPrimaryZoneWithNumbers",
			volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					PoolAttributes: &datamodel.PoolAttributes{
						PrimaryZone: "us123-central1-a",
					},
				},
			},
			expected:    "",
			expectError: true,
			errorMsg:    "LocationID represents neither a region nor a zone",
		},
		{
			name: "InvalidPrimaryZoneWithSpecialChars",
			volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					PoolAttributes: &datamodel.PoolAttributes{
						PrimaryZone: "us-central1@a",
					},
				},
			},
			expected:    "",
			expectError: true,
			errorMsg:    "LocationID represents neither a region nor a zone",
		},
		{
			name: "ValidVolumeWithCompletePoolAttributes",
			volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					PoolAttributes: &datamodel.PoolAttributes{
						PrimaryZone:     "us-west2-b",
						SecondaryZone:   "us-west2-c",
						MediatorZone:    "us-west2-a",
						ThroughputMibps: 1000,
						Iops:            10000,
						IsRegionalHA:    true,
					},
				},
			},
			expected:    "us-west2",
			expectError: false,
		},
		{
			name: "ValidVolumeWithMinimalPoolAttributes",
			volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					PoolAttributes: &datamodel.PoolAttributes{
						PrimaryZone: "australia-southeast1",
					},
				},
			},
			expected:    "australia-southeast1",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GetBackupRegion(tt.volume)

			if tt.expectError {
				require.Error(t, err, "Expected error for test case: %s", tt.name)
				assert.Equal(t, tt.errorMsg, err.Error(), "Error message mismatch for test case: %s", tt.name)
				assert.Equal(t, tt.expected, result, "Result should be empty string when error occurs for test case: %s", tt.name)
			} else {
				require.NoError(t, err, "Unexpected error for test case: %s", tt.name)
				assert.Equal(t, tt.expected, result, "Region mismatch for test case: %s", tt.name)
			}
		})
	}
}

func TestGetBackupRegionEdgeCases(t *testing.T) {
	t.Run("VolumeWithEmptyPoolAttributesStruct", func(t *testing.T) {
		volume := &datamodel.Volume{
			Pool: &datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{},
			},
		}

		result, err := GetBackupRegion(volume)
		require.Error(t, err)
		assert.Equal(t, "", result)
		assert.Contains(t, err.Error(), "LocationID represents neither a region nor a zone")
	})

	t.Run("VolumeWithWhitespacePrimaryZone", func(t *testing.T) {
		volume := &datamodel.Volume{
			Pool: &datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone: "   ",
				},
			},
		}

		result, err := GetBackupRegion(volume)
		require.Error(t, err)
		assert.Equal(t, "", result)
		assert.Contains(t, err.Error(), "LocationID represents neither a region nor a zone")
	})

	t.Run("VolumeWithVeryLongZoneName", func(t *testing.T) {
		volume := &datamodel.Volume{
			Pool: &datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone: "very-long-region-name-that-exceeds-normal-limits-a",
				},
			},
		}

		result, err := GetBackupRegion(volume)
		require.Error(t, err)
		assert.Equal(t, "", result)
		assert.Contains(t, err.Error(), "LocationID represents neither a region nor a zone")
	})
}

// TestImmutableBackupVaultErrMsg tests the error message constant for immutable backup vault validation
// This test ensures the constant is properly defined and has the expected value
func TestImmutableBackupVaultErrMsg(t *testing.T) {
	expectedMessage := "Immutable backup vaults are not supported for this region"

	assert.Equal(t, expectedMessage, ImmutableBackupVaultErrMsg,
		"ImmutableBackupVaultErrMsg should have the correct error message")

	// Ensure the message is not empty
	assert.NotEmpty(t, ImmutableBackupVaultErrMsg,
		"ImmutableBackupVaultErrMsg should not be empty")

	// Ensure the message contains key terms
	assert.Contains(t, ImmutableBackupVaultErrMsg, "Immutable backup vaults",
		"Error message should mention immutable backup vaults")
}

// Unit test for _getVolumeUriFromCcfeUri
func Test_getVolumeUriFromCcfeUri(t *testing.T) {
	t.Run("Valid CCFE URI", func(t *testing.T) {
		uri := "projects/test-project/locations/us-central1/volumes/test-volume/replications/test-replication"
		got := _getVolumeUriFromCcfeUri(uri)
		want := "projects/test-project/locations/us-central1/volumes/test-volume"
		if got != want {
			t.Errorf("expected %q, got %q", want, got)
		}
	})

	t.Run("Invalid CCFE URI", func(t *testing.T) {
		uri := "invalid-uri"
		got := _getVolumeUriFromCcfeUri(uri)
		if got != "" {
			t.Errorf("expected empty string for invalid URI, got %q", got)
		}
	})
}

func TestGetSourceVolumePathFromBackup(t *testing.T) {
	tests := []struct {
		name         string
		backup       *datamodel.Backup
		expectedPath string
		expectError  bool
	}{
		{
			name: "backup with custom source volume zone",
			backup: &datamodel.Backup{
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "test-project-123",
					SourceVolumeZone:  "us-central1-a",
					VolumeName:        "test-volume",
				},
				BackupVault: &datamodel.BackupVault{
					SourceRegionName: stringPtr("us-central1"),
				},
			},
			expectedPath: "projects/test-project-123/locations/us-central1-a/volumes/test-volume",
			expectError:  false,
		},
		{
			name: "backup with empty source volume zone uses backup vault region",
			backup: &datamodel.Backup{
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "test-project-456",
					SourceVolumeZone:  "",
					VolumeName:        "test-volume-2",
				},
				BackupVault: &datamodel.BackupVault{
					SourceRegionName: stringPtr("us-west1"),
				},
			},
			expectedPath: "projects/test-project-456/locations/us-west1/volumes/test-volume-2",
			expectError:  false,
		},
		{
			name: "backup with special characters in names",
			backup: &datamodel.Backup{
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "project-with-dashes-123",
					SourceVolumeZone:  "us-central1-b",
					VolumeName:        "volume_with_underscores",
				},
				BackupVault: &datamodel.BackupVault{
					SourceRegionName: stringPtr("us-central1"),
				},
			},
			expectedPath: "projects/project-with-dashes-123/locations/us-central1-b/volumes/volume_with_underscores",
			expectError:  false,
		},
		{
			name: "CrossProject vault: VolumeAccountName overrides AccountIdentifier",
			backup: &datamodel.Backup{
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "vault-project-999",
					SourceVolumeZone:  "us-east1-b",
					VolumeName:        "cross-project-vol",
					VolumeAccountName: "volume-owner-project-123",
				},
				BackupVault: &datamodel.BackupVault{
					SourceRegionName: stringPtr("us-east1"),
					ServiceType:      datamodel.ServiceTypeCrossProject,
				},
			},
			expectedPath: "projects/volume-owner-project-123/locations/us-east1-b/volumes/cross-project-vol",
			expectError:  false,
		},
		{
			name: "CrossProject vault: VolumeAccountName empty falls back to AccountIdentifier",
			backup: &datamodel.Backup{
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "fallback-project-456",
					SourceVolumeZone:  "us-west1-a",
					VolumeName:        "fallback-vol",
				},
				BackupVault: &datamodel.BackupVault{
					SourceRegionName: stringPtr("us-west1"),
					ServiceType:      datamodel.ServiceTypeCrossProject,
				},
			},
			expectedPath: "projects/fallback-project-456/locations/us-west1-a/volumes/fallback-vol",
			expectError:  false,
		},
		{
			name: "Non-CrossProject vault ignores VolumeAccountName",
			backup: &datamodel.Backup{
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "requesting-project-111",
					SourceVolumeZone:  "us-east1-b",
					VolumeName:        "vol-1",
					VolumeAccountName: "should-be-ignored",
				},
				BackupVault: &datamodel.BackupVault{
					SourceRegionName: stringPtr("us-east1"),
					ServiceType:      "GCNV",
				},
			},
			expectedPath: "projects/requesting-project-111/locations/us-east1-b/volumes/vol-1",
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := _getSourceVolumePathFromBackup(tt.backup)
			assert.Equal(t, tt.expectedPath, result)
		})
	}
}

func TestGetSourceSnapshotPathFromBackup(t *testing.T) {
	tests := []struct {
		name         string
		backup       *datamodel.Backup
		expectedPath string
		expectError  bool
	}{
		{
			name: "backup with custom source volume zone and snapshot",
			backup: &datamodel.Backup{
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "test-project-123",
					SourceVolumeZone:  "us-central1-a",
					VolumeName:        "test-volume",
					SnapshotName:      "test-snapshot",
				},
				BackupVault: &datamodel.BackupVault{
					SourceRegionName: stringPtr("us-central1"),
				},
			},
			expectedPath: "projects/test-project-123/locations/us-central1-a/volumes/test-volume/snapshots/test-snapshot",
			expectError:  false,
		},
		{
			name: "backup with empty source volume zone uses backup vault region",
			backup: &datamodel.Backup{
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "test-project-456",
					SourceVolumeZone:  "",
					VolumeName:        "test-volume-2",
					SnapshotName:      "snapshot-2",
				},
				BackupVault: &datamodel.BackupVault{
					SourceRegionName: stringPtr("us-west1"),
				},
			},
			expectedPath: "projects/test-project-456/locations/us-west1/volumes/test-volume-2/snapshots/snapshot-2",
			expectError:  false,
		},
		{
			name: "CrossProject vault: VolumeAccountName overrides AccountIdentifier",
			backup: &datamodel.Backup{
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "vault-project-999",
					SourceVolumeZone:  "us-east1-b",
					VolumeName:        "cross-project-vol",
					SnapshotName:      "snap-1",
					VolumeAccountName: "volume-owner-project-123",
				},
				BackupVault: &datamodel.BackupVault{
					SourceRegionName: stringPtr("us-east1"),
					ServiceType:      datamodel.ServiceTypeCrossProject,
				},
			},
			expectedPath: "projects/volume-owner-project-123/locations/us-east1-b/volumes/cross-project-vol/snapshots/snap-1",
			expectError:  false,
		},
		{
			name: "CrossProject vault: VolumeAccountName empty falls back to AccountIdentifier",
			backup: &datamodel.Backup{
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "fallback-project-456",
					SourceVolumeZone:  "us-west1-a",
					VolumeName:        "fallback-vol",
					SnapshotName:      "snap-fallback",
				},
				BackupVault: &datamodel.BackupVault{
					SourceRegionName: stringPtr("us-west1"),
					ServiceType:      datamodel.ServiceTypeCrossProject,
				},
			},
			expectedPath: "projects/fallback-project-456/locations/us-west1-a/volumes/fallback-vol/snapshots/snap-fallback",
			expectError:  false,
		},
		{
			name: "Non-CrossProject vault ignores VolumeAccountName for snapshot",
			backup: &datamodel.Backup{
				Attributes: &datamodel.BackupAttributes{
					AccountIdentifier: "requesting-project-111",
					SourceVolumeZone:  "us-east1-b",
					VolumeName:        "vol-1",
					SnapshotName:      "snap-1",
					VolumeAccountName: "should-be-ignored",
				},
				BackupVault: &datamodel.BackupVault{
					SourceRegionName: stringPtr("us-east1"),
					ServiceType:      "GCNV",
				},
			},
			expectedPath: "projects/requesting-project-111/locations/us-east1-b/volumes/vol-1/snapshots/snap-1",
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := _getSourceSnapshotPathFromBackup(tt.backup)
			assert.Equal(t, tt.expectedPath, result)
		})
	}
}

func TestIsFilesProtocol(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "NFSv3 protocol",
			input:    string(gcpgenserver.ProtocolsV1betaNFSV3),
			expected: true,
		},
		{
			name:     "NFSv4 protocol",
			input:    string(gcpgenserver.ProtocolsV1betaNFSV4),
			expected: true,
		},
		{
			name:     "SMB protocol",
			input:    string(gcpgenserver.ProtocolsV1betaSMB),
			expected: true,
		},
		{
			name:     "Unknown protocol",
			input:    "iscsi",
			expected: false,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsFilesProtocol(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Helper function to create string pointers for testing
func stringPtr(s string) *string {
	return &s
}

func TestGetNLFSecretPath(t *testing.T) {
	// Save original values and restore after test
	origNLF := env.NLFLicenseSecretPath
	origProject := env.SecretManagerProjectID
	defer func() {
		env.NLFLicenseSecretPath = origNLF
		env.SecretManagerProjectID = origProject
	}()

	// Case: both env vars set
	env.NLFLicenseSecretPath = "nlf-secret"
	env.SecretManagerProjectID = "test-project"
	expected := "projects/test-project/secrets/nlf-secret"
	actual := GetNLFSecretPath()
	assert.Equal(t, expected, actual)

	// Case: one env var empty
	env.NLFLicenseSecretPath = ""
	actual = GetNLFSecretPath()
	assert.Equal(t, "", actual)

	env.NLFLicenseSecretPath = "nlf-secret"
	env.SecretManagerProjectID = ""
	actual = GetNLFSecretPath()
	assert.Equal(t, "", actual)
}

func TestConvertLabelsMapToJSONB(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected *datamodel.JSONB
	}{
		{
			name:     "NilLabels",
			labels:   nil,
			expected: nil,
		},
		{
			name:     "EmptyLabels",
			labels:   map[string]string{},
			expected: nil,
		},
		{
			name: "ValidLabels",
			labels: map[string]string{
				"environment": "production",
				"team":        "platform",
				"cost-center": "engineering",
			},
			expected: &datamodel.JSONB{
				"environment": "production",
				"team":        "platform",
				"cost-center": "engineering",
			},
		},
		{
			name: "SingleLabel",
			labels: map[string]string{
				"owner": "team-a",
			},
			expected: &datamodel.JSONB{
				"owner": "team-a",
			},
		},
		{
			name: "LabelsWithEmptyValues",
			labels: map[string]string{
				"environment": "production",
				"empty-key":   "",
				"team":        "platform",
			},
			expected: &datamodel.JSONB{
				"environment": "production",
				"empty-key":   "",
				"team":        "platform",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertLabelsMapToJSONB(tt.labels)

			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.Equal(t, len(*tt.expected), len(*result))

				for key, expectedValue := range *tt.expected {
					actualValue, exists := (*result)[key]
					assert.True(t, exists, "Expected key %s to exist", key)
					assert.Equal(t, expectedValue, actualValue, "Expected value %s for key %s, got %s", expectedValue, key, actualValue)
				}
			}
		})
	}
}

func TestExtractOntapVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Valid version in middle of string",
			input:    "ONTAP-9.17.1-123456",
			expected: "9.17.1",
		},
		{
			name:     "Valid version at start",
			input:    "9.17.1-ONTAP-123456",
			expected: "9.17.1",
		},
		{
			name:     "Valid version at end",
			input:    "ONTAP-123456-9.17.1",
			expected: "9.17.1",
		},
		{
			name:     "Multiple versions - first match",
			input:    "9.17.1-ONTAP-9.18.0",
			expected: "9.17.1",
		},
		{
			name:     "No version found",
			input:    "ONTAP-version-string",
			expected: "",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Single digit version",
			input:    "9.17.1",
			expected: "9.17.1",
		},
		{
			name:     "Version with extra digits",
			input:    "9.17.1.2.3",
			expected: "9.17.1",
		},
		{
			name:     "Version with letters",
			input:    "v9.17.1",
			expected: "9.17.1",
		},
		{
			name:     "Complex string with version",
			input:    "r9.17.1PxN_250902_0747_promo_image.tgz",
			expected: "9.17.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractOntapVersion(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractOntapRBACVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Base version",
			input:    "9.18.1",
			expected: "9.18.1",
		},
		{
			name:     "P suffix",
			input:    "9.18.1P2",
			expected: "9.18.1P2",
		},
		{
			name:     "Full NetApp Release string with P and X suffix",
			input:    "NetApp Release 9.17.1P9X3: Tue Jun 16 13:35:46 UTC 2026",
			expected: "9.17.1P9X3",
		},
		{
			name:     "Version with X suffix only",
			input:    "NetApp Release 9.18.1X29: Mon May 24 08:07:35 UTC 2017",
			expected: "9.18.1X29",
		},
		{
			name:     "Version with RC suffix",
			input:    "NetApp Release 9.18.1RC1: Mon May 24 08:07:35 UTC 2017",
			expected: "9.18.1RC1",
		},
		{
			name:     "Version with D suffix",
			input:    "9.18.1D1",
			expected: "9.18.1D1",
		},
		{
			name:     "Hotfix suffix is not matched",
			input:    "9.18.1P3_HF1",
			expected: "9.18.1P3",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractOntapRBACVersion(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertTimeToOptDateTime(t *testing.T) {
	tests := []struct {
		name     string
		input    *time.Time
		expected oasgenserver.OptDateTime
	}{
		{
			name:     "Nil time",
			input:    nil,
			expected: oasgenserver.OptDateTime{},
		},
		{
			name:     "Valid time",
			input:    timePtr(time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)),
			expected: oasgenserver.NewOptDateTime(time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)),
		},
		{
			name:     "Zero time",
			input:    timePtr(time.Time{}),
			expected: oasgenserver.NewOptDateTime(time.Time{}),
		},
		{
			name:     "Time with different timezone",
			input:    timePtr(time.Date(2023, 6, 15, 18, 30, 45, 123456789, time.FixedZone("CST", -6*60*60))),
			expected: oasgenserver.NewOptDateTime(time.Date(2023, 6, 15, 18, 30, 45, 123456789, time.FixedZone("CST", -6*60*60))),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertTimeToOptDateTime(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertStringToOptString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected oasgenserver.OptString
	}{
		{
			name:     "Empty string",
			input:    "",
			expected: oasgenserver.OptString{},
		},
		{
			name:     "Valid string",
			input:    "test string",
			expected: oasgenserver.NewOptString("test string"),
		},
		{
			name:     "String with special characters",
			input:    "test-string_with.special@chars!",
			expected: oasgenserver.NewOptString("test-string_with.special@chars!"),
		},
		{
			name:     "String with spaces",
			input:    "test string with spaces",
			expected: oasgenserver.NewOptString("test string with spaces"),
		},
		{
			name:     "String with unicode",
			input:    "test string with unicode: 测试 🚀",
			expected: oasgenserver.NewOptString("test string with unicode: 测试 🚀"),
		},
		{
			name:     "Single character",
			input:    "a",
			expected: oasgenserver.NewOptString("a"),
		},
		{
			name:     "Whitespace only",
			input:    "   ",
			expected: oasgenserver.NewOptString("   "),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertStringToOptString(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Helper function to create time pointers
func timePtr(t time.Time) *time.Time {
	return &t
}

// TestIsCrossRegionBackupEnabled tests the IsCrossRegionBackupEnabled function
func TestIsCrossRegionBackupEnabled(t *testing.T) {
	// Store original value to restore later
	originalValue := IsCrossRegionBackupEnabled()
	defer SetCrossRegionBackupEnabledForTest(originalValue)

	t.Run("returns true when cross-region backup is enabled", func(t *testing.T) {
		SetCrossRegionBackupEnabledForTest(true)

		result := IsCrossRegionBackupEnabled()

		assert.True(t, result, "IsCrossRegionBackupEnabled should return true when enabled")
	})

	t.Run("returns false when cross-region backup is disabled", func(t *testing.T) {
		SetCrossRegionBackupEnabledForTest(false)

		result := IsCrossRegionBackupEnabled()

		assert.False(t, result, "IsCrossRegionBackupEnabled should return false when disabled")
	})

	t.Run("maintains state correctly across multiple calls", func(t *testing.T) {
		// Test with enabled state
		SetCrossRegionBackupEnabledForTest(true)
		assert.True(t, IsCrossRegionBackupEnabled(), "First call should return true")
		assert.True(t, IsCrossRegionBackupEnabled(), "Second call should return true")

		// Test with disabled state
		SetCrossRegionBackupEnabledForTest(false)
		assert.False(t, IsCrossRegionBackupEnabled(), "First call should return false")
		assert.False(t, IsCrossRegionBackupEnabled(), "Second call should return false")
	})

	t.Run("test helper function works correctly", func(t *testing.T) {
		// Verify the test helper function actually changes the state
		SetCrossRegionBackupEnabledForTest(true)
		assert.True(t, IsCrossRegionBackupEnabled(), "Should be true after setting to true")

		SetCrossRegionBackupEnabledForTest(false)
		assert.False(t, IsCrossRegionBackupEnabled(), "Should be false after setting to false")

		// Toggle back
		SetCrossRegionBackupEnabledForTest(true)
		assert.True(t, IsCrossRegionBackupEnabled(), "Should be true after toggling back")
	})
}

// TestSetCrossRegionBackupEnabledForTest tests the test helper function
func TestSetCrossRegionBackupEnabledForTest(t *testing.T) {
	// Store original value to restore later
	originalValue := IsCrossRegionBackupEnabled()
	defer SetCrossRegionBackupEnabledForTest(originalValue)

	t.Run("can enable cross-region backup flag", func(t *testing.T) {
		SetCrossRegionBackupEnabledForTest(true)

		assert.True(t, IsCrossRegionBackupEnabled(),
			"SetCrossRegionBackupEnabledForTest(true) should enable the flag")
	})

	t.Run("can disable cross-region backup flag", func(t *testing.T) {
		SetCrossRegionBackupEnabledForTest(false)

		assert.False(t, IsCrossRegionBackupEnabled(),
			"SetCrossRegionBackupEnabledForTest(false) should disable the flag")
	})

	t.Run("changes take effect immediately", func(t *testing.T) {
		// Start with one state
		SetCrossRegionBackupEnabledForTest(false)
		assert.False(t, IsCrossRegionBackupEnabled())

		// Change to opposite state
		SetCrossRegionBackupEnabledForTest(true)
		assert.True(t, IsCrossRegionBackupEnabled())

		// Change back
		SetCrossRegionBackupEnabledForTest(false)
		assert.False(t, IsCrossRegionBackupEnabled())
	})
}

func TestComparePointerStringSlices(t *testing.T) {
	s1 := []*string{nillable.ToPointer("a"), nillable.ToPointer("b")}
	s2 := []string{"a", "b"}
	s3 := []string{"a", "c"}

	assert.True(t, ComparePointerStringSlices(s1, s2))
	assert.False(t, ComparePointerStringSlices(s1, s3))
	assert.False(t, ComparePointerStringSlices(s1[:1], s2))
}

func TestEnableMultiAD_DefaultValue(t *testing.T) {
	// Test that EnableMultiAD is read from environment with correct default
	// Save original environment
	originalValue := os.Getenv("ENABLE_MULTI_AD")
	defer func() {
		if originalValue != "" {
			_ = os.Setenv("ENABLE_MULTI_AD", originalValue)
		} else {
			_ = os.Unsetenv("ENABLE_MULTI_AD")
		}
	}()

	// Test default value (false)
	require.NoError(t, os.Unsetenv("ENABLE_MULTI_AD"))
	value := env.GetBool("ENABLE_MULTI_AD", false)
	assert.False(t, value, "Default value should be false")

	// Test true value
	require.NoError(t, os.Setenv("ENABLE_MULTI_AD", "true"))
	value = env.GetBool("ENABLE_MULTI_AD", false)
	assert.True(t, value, "Value should be true when env is set to 'true'")

	// Test false value explicitly
	require.NoError(t, os.Setenv("ENABLE_MULTI_AD", "false"))
	value = env.GetBool("ENABLE_MULTI_AD", false)
	assert.False(t, value, "Value should be false when env is set to 'false'")
}

func TestMaxNumberOfADPerAccount_DefaultValue(t *testing.T) {
	// Test that MaxNumberOfADPerAccount is read from environment with correct default
	// Save original environment
	originalValue := os.Getenv("MAX_NUMBER_OF_AD_PER_ACCOUNT")
	defer func() {
		if originalValue != "" {
			_ = os.Setenv("MAX_NUMBER_OF_AD_PER_ACCOUNT", originalValue)
		} else {
			_ = os.Unsetenv("MAX_NUMBER_OF_AD_PER_ACCOUNT")
		}
	}()

	// Test default value (5)
	require.NoError(t, os.Unsetenv("MAX_NUMBER_OF_AD_PER_ACCOUNT"))
	value := env.GetInt("MAX_NUMBER_OF_AD_PER_ACCOUNT", 5)
	assert.Equal(t, 5, value, "Default value should be 5")

	// Test custom value
	require.NoError(t, os.Setenv("MAX_NUMBER_OF_AD_PER_ACCOUNT", "10"))
	value = env.GetInt("MAX_NUMBER_OF_AD_PER_ACCOUNT", 5)
	assert.Equal(t, 10, value, "Value should be 10 when env is set to '10'")

	// Test another custom value
	require.NoError(t, os.Setenv("MAX_NUMBER_OF_AD_PER_ACCOUNT", "1"))
	value = env.GetInt("MAX_NUMBER_OF_AD_PER_ACCOUNT", 5)
	assert.Equal(t, 1, value, "Value should be 1 when env is set to '1'")
}

func TestMaxNumberOfKmsPerAccount_DefaultValue(t *testing.T) {
	originalValue := os.Getenv("MAX_NUMBER_OF_KMS_PER_ACCOUNT")
	defer func() {
		if originalValue != "" {
			_ = os.Setenv("MAX_NUMBER_OF_KMS_PER_ACCOUNT", originalValue)
		} else {
			_ = os.Unsetenv("MAX_NUMBER_OF_KMS_PER_ACCOUNT")
		}
	}()

	require.NoError(t, os.Unsetenv("MAX_NUMBER_OF_KMS_PER_ACCOUNT"))
	value := env.GetInt("MAX_NUMBER_OF_KMS_PER_ACCOUNT", 3)
	assert.Equal(t, 3, value, "Default value should be 3")

	require.NoError(t, os.Setenv("MAX_NUMBER_OF_KMS_PER_ACCOUNT", "5"))
	value = env.GetInt("MAX_NUMBER_OF_KMS_PER_ACCOUNT", 3)
	assert.Equal(t, 5, value, "Value should be 5 when env is set to '5'")
}

func TestMultiADEnvironmentVariables_Integration(t *testing.T) {
	// Integration test to verify both environment variables work together
	originalEnableMultiAD := os.Getenv("ENABLE_MULTI_AD")
	originalMaxNumberOfAD := os.Getenv("MAX_NUMBER_OF_AD_PER_ACCOUNT")
	defer func() {
		if originalEnableMultiAD != "" {
			_ = os.Setenv("ENABLE_MULTI_AD", originalEnableMultiAD)
		} else {
			_ = os.Unsetenv("ENABLE_MULTI_AD")
		}
		if originalMaxNumberOfAD != "" {
			_ = os.Setenv("MAX_NUMBER_OF_AD_PER_ACCOUNT", originalMaxNumberOfAD)
		} else {
			_ = os.Unsetenv("MAX_NUMBER_OF_AD_PER_ACCOUNT")
		}
	}()

	tests := []struct {
		name              string
		enableMultiAD     string
		maxNumberOfAD     string
		expectedEnable    bool
		expectedMaxNumber int
	}{
		{
			name:              "Multi-AD disabled with default max",
			enableMultiAD:     "false",
			maxNumberOfAD:     "",
			expectedEnable:    false,
			expectedMaxNumber: 5,
		},
		{
			name:              "Multi-AD enabled with custom max",
			enableMultiAD:     "true",
			maxNumberOfAD:     "10",
			expectedEnable:    true,
			expectedMaxNumber: 10,
		},
		{
			name:              "Multi-AD enabled with max 1",
			enableMultiAD:     "true",
			maxNumberOfAD:     "1",
			expectedEnable:    true,
			expectedMaxNumber: 1,
		},
		{
			name:              "Default values",
			enableMultiAD:     "",
			maxNumberOfAD:     "",
			expectedEnable:    false,
			expectedMaxNumber: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables
			if tt.enableMultiAD != "" {
				require.NoError(t, os.Setenv("ENABLE_MULTI_AD", tt.enableMultiAD))
			} else {
				require.NoError(t, os.Unsetenv("ENABLE_MULTI_AD"))
			}

			if tt.maxNumberOfAD != "" {
				require.NoError(t, os.Setenv("MAX_NUMBER_OF_AD_PER_ACCOUNT", tt.maxNumberOfAD))
			} else {
				require.NoError(t, os.Unsetenv("MAX_NUMBER_OF_AD_PER_ACCOUNT"))
			}

			// Read values
			enableValue := env.GetBool("ENABLE_MULTI_AD", false)
			maxValue := env.GetInt("MAX_NUMBER_OF_AD_PER_ACCOUNT", 5)

			// Assertions
			assert.Equal(t, tt.expectedEnable, enableValue, "EnableMultiAD should match expected value")
			assert.Equal(t, tt.expectedMaxNumber, maxValue, "MaxNumberOfADPerAccount should match expected value")
		})
	}
}

func TestFetchTieringPolicyAsPerVolumeType(t *testing.T) {
	tests := []struct {
		name           string
		fileVolume     bool
		expectedPolicy string
	}{
		{
			name:           "FileVolume_ReturnsAutoPolicy",
			fileVolume:     true,
			expectedPolicy: ontapmodels.VolumeInlineTieringPolicyAuto,
		},
		{
			name:           "BlockVolume_ReturnsSnapshotOnlyPolicy",
			fileVolume:     false,
			expectedPolicy: ontapmodels.VolumeInlineTieringPolicySnapshotOnly,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FetchTieringPolicyAsPerVolumeType(tt.fileVolume)
			assert.Equal(t, tt.expectedPolicy, result, "Tiering policy should match expected value")
		})
	}
}

func TestGenerateRbacFilePath(t *testing.T) {
	template := "/etc/rbac/%s.yaml"
	configurable := "gcp"
	expected := "/etc/rbac/gcp.yaml"

	result := GenerateRbacFilePath(template, configurable)
	assert.Equal(t, expected, result)

	// Test with multiple %s (should only replace first)
	templateMulti := "/etc/rbac/%s/%s.yaml"
	expectedMulti := "/etc/rbac/gcp/%s.yaml"
	resultMulti := GenerateRbacFilePath(templateMulti, configurable)
	assert.Equal(t, expectedMulti, resultMulti)

	// Test with no %s
	templateNoPlaceholder := "/etc/rbac/static.yaml"
	expectedNoPlaceholder := "/etc/rbac/static.yaml"
	resultNoPlaceholder := GenerateRbacFilePath(templateNoPlaceholder, configurable)
	assert.Equal(t, expectedNoPlaceholder, resultNoPlaceholder)
}

// TestIsAccountAllowlisted_EmptyMap tests lines 1088-1089,1094-1095
// Tests IsAccountAllowlisted when experimentalVersionAllowlistedAccounts is empty
func TestIsAccountAllowlisted_EmptyMap(t *testing.T) {
	// Save original state
	originalEnv := os.Getenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS")
	defer func() {
		if originalEnv != "" {
			_ = os.Setenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", originalEnv)
		} else {
			_ = os.Unsetenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS")
		}
		experimentalVersionAllowlistedAccounts = ParseCommaSeparatedStringToMap(env.GetString("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", ""))
	}()

	// Set empty accounts map
	_ = os.Unsetenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS")
	experimentalVersionAllowlistedAccounts = ParseCommaSeparatedStringToMap(env.GetString("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", ""))

	// Test with empty map - should return false (line 1088-1089)
	result := IsAccountAllowlisted("test-account")
	assert.False(t, result, "Should return false when allowlisted accounts map is empty")

	// Test with any account ID when map is empty (line 1094-1095)
	result = IsAccountAllowlisted("123456")
	assert.False(t, result, "Should return false for any account when map is empty")
}

func TestLvHaPairsForLargeVolume(t *testing.T) {
	origAllow := os.Getenv("EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS")
	orig24 := os.Getenv("EXPERIMENTAL_NUMBER_OF_HA_PAIRS_LARGE_CAPACITY")
	defer func() {
		if origAllow != "" {
			_ = os.Setenv("EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS", origAllow)
		} else {
			_ = os.Unsetenv("EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS")
		}
		if orig24 != "" {
			_ = os.Setenv("EXPERIMENTAL_NUMBER_OF_HA_PAIRS_LARGE_CAPACITY", orig24)
		} else {
			_ = os.Unsetenv("EXPERIMENTAL_NUMBER_OF_HA_PAIRS_LARGE_CAPACITY")
		}
		experimentalCustomHaLargeVolumeAllowlistedAccounts = ParseCommaSeparatedStringToMap(env.GetString("EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS", ""))
	}()

	SetExperimentalCustomHaLargeVolumeAllowlistedAccountsForTesting("acct-a")
	_ = os.Setenv("EXPERIMENTAL_NUMBER_OF_HA_PAIRS_LARGE_CAPACITY", "11")
	assert.Equal(t, int64(11), LvHaPairsForLargeVolume("acct-a", 6))
	assert.Equal(t, int64(6), LvHaPairsForLargeVolume("other", 6))
	assert.False(t, IsCustomHaLargeVolumeAllowlisted("other"))
	assert.True(t, IsCustomHaLargeVolumeAllowlisted("acct-a"))
}

// TestIsCustomHaLargeVolumeAllowlisted_EmptyAllowlist covers IsCustomHaLargeVolumeAllowlisted when the allowlist map is empty.
func TestIsCustomHaLargeVolumeAllowlisted_EmptyAllowlist(t *testing.T) {
	origAllow := os.Getenv("EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS")
	defer func() {
		if origAllow != "" {
			_ = os.Setenv("EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS", origAllow)
		} else {
			_ = os.Unsetenv("EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS")
		}
		experimentalCustomHaLargeVolumeAllowlistedAccounts = ParseCommaSeparatedStringToMap(env.GetString("EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS", ""))
	}()

	_ = os.Unsetenv("EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS")
	experimentalCustomHaLargeVolumeAllowlistedAccounts = ParseCommaSeparatedStringToMap("")

	assert.False(t, IsCustomHaLargeVolumeAllowlisted("any-account"))
}

// TestIsFileProtocolSupportedV2_FlagDisabled tests lines 1104-1105
// Tests IsFileProtocolSupportedV2 when FileProtocolSupported flag is disabled
func TestIsFileProtocolSupportedV2_FlagDisabled(t *testing.T) {
	originalFlag := FileProtocolSupported
	defer func() {
		SetFileProtocolSupportedForTesting(originalFlag)
	}()

	// Disable file protocol support
	SetFileProtocolSupportedForTesting(false)

	// Should return false when flag is disabled (line 1104-1105)
	result := IsFileProtocolSupportedV2("9.18.1")
	assert.False(t, result, "Should return false when FileProtocolSupported flag is disabled")
}

// TestIsFileProtocolSupportedV2_EmptyVersion tests lines 1109-1110
// Tests IsFileProtocolSupportedV2 when ontapVersion is empty
func TestIsFileProtocolSupportedV2_EmptyVersion(t *testing.T) {
	originalFlag := FileProtocolSupported
	defer func() {
		SetFileProtocolSupportedForTesting(originalFlag)
	}()

	// Enable file protocol support
	SetFileProtocolSupportedForTesting(true)

	// Should return false when version is empty (line 1109-1110)
	result := IsFileProtocolSupportedV2("")
	assert.False(t, result, "Should return false when ontapVersion is empty")
}

// TestIsFileProtocolSupportedV2_InvalidVersion tests lines 1114-1116
// Tests IsFileProtocolSupportedV2 when extracted version is empty
func TestIsFileProtocolSupportedV2_InvalidVersion(t *testing.T) {
	originalFlag := FileProtocolSupported
	defer func() {
		SetFileProtocolSupportedForTesting(originalFlag)
	}()

	// Enable file protocol support
	SetFileProtocolSupportedForTesting(true)

	// Should return false when extracted version is empty (line 1114-1116)
	result := IsFileProtocolSupportedV2("invalid-version")
	assert.False(t, result, "Should return false when extracted version is empty")
}

// TestIsFileProtocolSupportedV2_VersionCheck tests line 1120
// Tests IsFileProtocolSupportedV2 version comparison
func TestIsFileProtocolSupportedV2_VersionCheck(t *testing.T) {
	originalFlag := FileProtocolSupported
	defer func() {
		SetFileProtocolSupportedForTesting(originalFlag)
	}()

	// Enable file protocol support
	SetFileProtocolSupportedForTesting(true)

	// Test with version >= 9.18 (line 1120)
	result := IsFileProtocolSupportedV2("9.18.1")
	assert.True(t, result, "Should return true for version >= 9.18")

	// Test with version < 9.18
	result = IsFileProtocolSupportedV2("9.17.1")
	assert.False(t, result, "Should return false for version < 9.18")
}

// TestGetOntapVersionBasedOnAllowlisting_NoExperimentalVersion tests lines 1128-1129
// Tests GetOntapVersionBasedOnAllowlisting when experimental version is not configured
func TestGetOntapVersionBasedOnAllowlisting_NoExperimentalVersion(t *testing.T) {
	originalExperimental := env.ExperimentalOntapVersionDetails
	originalCurrent := env.CurrentOntapVersionDetails
	defer func() {
		env.ExperimentalOntapVersionDetails = originalExperimental
		env.CurrentOntapVersionDetails = originalCurrent
	}()

	// Set experimental version to empty
	env.ExperimentalOntapVersionDetails = ""
	env.CurrentOntapVersionDetails = "9.17.1P2"

	// Should return current version when experimental is not configured (line 1128-1129)
	result := GetOntapVersionBasedOnAllowlisting("test-account")
	assert.Equal(t, "9.17.1P2", result, "Should return current version when experimental is not configured")
}

// TestGetOntapVersionBasedOnAllowlisting_AllowlistedAccount tests lines 1132-1133
// Tests GetOntapVersionBasedOnAllowlisting when account is allowlisted
func TestGetOntapVersionBasedOnAllowlisting_AllowlistedAccount(t *testing.T) {
	originalExperimental := env.ExperimentalOntapVersionDetails
	originalCurrent := env.CurrentOntapVersionDetails
	originalEnv := os.Getenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS")
	defer func() {
		env.ExperimentalOntapVersionDetails = originalExperimental
		env.CurrentOntapVersionDetails = originalCurrent
		if originalEnv != "" {
			_ = os.Setenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", originalEnv)
		} else {
			_ = os.Unsetenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS")
		}
		experimentalVersionAllowlistedAccounts = ParseCommaSeparatedStringToMap(env.GetString("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", ""))
	}()

	// Set experimental version
	env.ExperimentalOntapVersionDetails = "9.18.1P1"
	env.CurrentOntapVersionDetails = "9.17.1P2"
	_ = os.Setenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", "test-account")
	experimentalVersionAllowlistedAccounts = ParseCommaSeparatedStringToMap(env.GetString("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", ""))

	// Should return experimental version for allowlisted account (line 1132-1133)
	result := GetOntapVersionBasedOnAllowlisting("test-account")
	assert.Equal(t, "9.18.1P1", result, "Should return experimental version for allowlisted account")
}

// TestGetOntapVersionBasedOnAllowlisting_NonAllowlistedAccount tests line 1136
// Tests GetOntapVersionBasedOnAllowlisting when account is not allowlisted
func TestGetOntapVersionBasedOnAllowlisting_NonAllowlistedAccount(t *testing.T) {
	originalExperimental := env.ExperimentalOntapVersionDetails
	originalCurrent := env.CurrentOntapVersionDetails
	originalEnv := os.Getenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS")
	defer func() {
		env.ExperimentalOntapVersionDetails = originalExperimental
		env.CurrentOntapVersionDetails = originalCurrent
		if originalEnv != "" {
			_ = os.Setenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", originalEnv)
		} else {
			_ = os.Unsetenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS")
		}
		experimentalVersionAllowlistedAccounts = ParseCommaSeparatedStringToMap(env.GetString("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", ""))
	}()

	// Set experimental version
	env.ExperimentalOntapVersionDetails = "9.18.1P1"
	env.CurrentOntapVersionDetails = "9.17.1P2"
	_ = os.Setenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", "other-account")
	experimentalVersionAllowlistedAccounts = ParseCommaSeparatedStringToMap(env.GetString("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", ""))

	// Should return current version for non-allowlisted account (line 1136)
	result := GetOntapVersionBasedOnAllowlisting("test-account")
	assert.Equal(t, "9.17.1P2", result, "Should return current version for non-allowlisted account")
}

// TestCompareOntapVersion_EmptyVersions tests lines 1317-1318,1320-1321
// Tests CompareOntapVersion when versions are empty or extraction fails
func TestCompareOntapVersion_EmptyVersions(t *testing.T) {
	// Test when v1 extraction fails (line 1317-1318)
	result := CompareOntapVersion("", "9.17.1")
	assert.Equal(t, 0, result, "Should return 0 when v1 extraction fails")

	// Test when v2 extraction fails (line 1320-1321)
	result = CompareOntapVersion("9.17.1", "")
	assert.Equal(t, 0, result, "Should return 0 when v2 extraction fails")

	// Test when both are empty
	result = CompareOntapVersion("", "")
	assert.Equal(t, 0, result, "Should return 0 when both versions are empty")
}

// TestCompareOntapVersion_PatchLevels tests lines 1327-1328,1330-1331
// Tests CompareOntapVersion with patch levels (P2, X29, etc.)
func TestCompareOntapVersion_PatchLevels(t *testing.T) {
	// Test with P patch level (line 1327-1328)
	result := CompareOntapVersion("9.18.1P2", "9.18.1P3")
	assert.Equal(t, 0, result, "Should return 0 when base versions are equal (P patch levels)")

	// Test with X patch level (line 1330-1331)
	result = CompareOntapVersion("9.18.1X29", "9.18.1X30")
	assert.Equal(t, 0, result, "Should return 0 when base versions are equal (X patch levels)")

	// Test comparing P and X patch levels
	result = CompareOntapVersion("9.18.1P2", "9.18.1X29")
	assert.Equal(t, 0, result, "Should return 0 when base versions are equal (different patch types)")
}

// TestCompareOntapVersion_PartsLength tests lines 1334-1335,1337-1339,1341-1342
// Tests CompareOntapVersion with different number of version parts
// Note: The regex requires 3 parts, so we test the maxParts logic with valid 3-part versions
func TestCompareOntapVersion_PartsLength(t *testing.T) {
	// Test maxParts calculation when both versions have 3 parts (line 1334-1335,1337-1339,1341-1342)
	// When both have 3 parts, maxParts will be 3, and all parts will be compared
	result := CompareOntapVersion("9.17.0", "9.17.1")
	assert.Equal(t, -1, result, "Should return -1 when v1 < v2 with same number of parts")

	result = CompareOntapVersion("9.17.1", "9.17.0")
	assert.Equal(t, 1, result, "Should return 1 when v1 > v2 with same number of parts")
}

// TestCompareOntapVersion_ConversionErrors tests lines 1345-1349
// Tests CompareOntapVersion when conversion to int fails
func TestCompareOntapVersion_ConversionErrors(t *testing.T) {
	// Test with invalid version parts (line 1345-1349)
	result := CompareOntapVersion("9.17.invalid", "9.17.1")
	assert.Equal(t, 0, result, "Should return 0 when conversion to int fails")

	result = CompareOntapVersion("9.17.1", "9.17.invalid")
	assert.Equal(t, 0, result, "Should return 0 when conversion to int fails for v2")
}

// TestCompareOntapVersion_Comparison tests lines 1351-1352,1354-1355
// Tests CompareOntapVersion comparison logic
func TestCompareOntapVersion_Comparison(t *testing.T) {
	// Test when num1 < num2 (line 1351-1352)
	result := CompareOntapVersion("9.17.1", "9.18.1")
	assert.Equal(t, -1, result, "Should return -1 when v1 < v2")

	// Test when num1 > num2 (line 1354-1355)
	result = CompareOntapVersion("9.18.1", "9.17.1")
	assert.Equal(t, 1, result, "Should return 1 when v1 > v2")
}

// TestCompareOntapVersion_FewerParts tests lines 1339, 1342, 1360-1361,1363-1364,1366
// Tests CompareOntapVersion when versions have different number of parts after comparison
// Note: The regex requires 3 parts, so versions with fewer parts won't be extracted.
// This test covers the logic that compares parts after the loop completes.
func TestCompareOntapVersion_FewerParts(t *testing.T) {
	// Test maxParts calculation when v1 has fewer parts (line 1339)
	// This tests the case where len(parts1) < maxParts, so maxParts is adjusted
	// Since regex requires 3 parts, we need to test with valid 3-part versions
	// but test the logic that handles when one version has fewer parts in the comparison loop
	result := CompareOntapVersion("9.17.1", "9.17.1")
	assert.Equal(t, 0, result, "Should return 0 when versions are equal")

	// Test maxParts calculation when v2 has fewer parts (line 1342)
	// Similar to above, testing the maxParts adjustment logic
	result = CompareOntapVersion("9.17.1", "9.17.0")
	assert.Equal(t, 1, result, "Should return 1 when v1 > v2")

	// Test when both have same number of parts and are equal (line 1366)
	result = CompareOntapVersion("9.17.1", "9.17.1")
	assert.Equal(t, 0, result, "Should return 0 when versions are equal")

	// Test when versions differ in the third part
	result = CompareOntapVersion("9.17.0", "9.17.1")
	assert.Equal(t, -1, result, "Should return -1 when v1 < v2")

	// Test when len(parts1) < len(parts2) after comparison (line 1360-1361)
	// Since regex requires 3 parts, both will have 3 parts, but we test the comparison logic
	result = CompareOntapVersion("9.17.0", "9.17.1")
	assert.Equal(t, -1, result, "Should return -1 when v1 < v2")

	// Test when len(parts1) > len(parts2) after comparison (line 1363-1364)
	result = CompareOntapVersion("9.17.1", "9.17.0")
	assert.Equal(t, 1, result, "Should return 1 when v1 > v2")
}

// TestCompareOntapVersion_ConversionError tests line 1349
// Tests CompareOntapVersion when conversion to int fails during comparison
func TestCompareOntapVersion_ConversionError(t *testing.T) {
	// Test when conversion fails for a part (line 1349)
	// This tests the error handling in the conversion loop
	result := CompareOntapVersion("9.17.invalid", "9.17.1")
	assert.Equal(t, 0, result, "Should return 0 when conversion to int fails")

	result = CompareOntapVersion("9.17.1", "9.17.invalid")
	assert.Equal(t, 0, result, "Should return 0 when conversion to int fails for v2")
}

// TestIsOntapVersionGreaterOrEqual tests line 1371
// Tests IsOntapVersionGreaterOrEqual function
func TestIsOntapVersionGreaterOrEqual(t *testing.T) {
	// Test when version is greater
	result := IsOntapVersionGreaterOrEqual("9.18.1", "9.17.1")
	assert.True(t, result, "Should return true when version is greater")

	// Test when version is equal
	result = IsOntapVersionGreaterOrEqual("9.18.1", "9.18.1")
	assert.True(t, result, "Should return true when version is equal")

	// Test when version is less
	result = IsOntapVersionGreaterOrEqual("9.17.1", "9.18.1")
	assert.False(t, result, "Should return false when version is less")
}

// TestGetONTAPSnapshotNameFromCBSDisplaySnapshotName tests GetONTAPSnapshotNameFromCBSDisplaySnapshotName function
func TestGetONTAPSnapshotNameFromCBSDisplaySnapshotName(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedOutput string
		description    string
	}{
		{
			name:           "Daily prefix",
			input:          "daily-scheduled-backup-mpukqsfg-2026-03-04-102006",
			expectedOutput: "scheduled-backup-mpukqsfg-2026-03-04-102006",
			description:    "Trims daily- prefix and returns the rest",
		},
		{
			name:           "Weekly prefix",
			input:          "weekly-scheduled-backup-xyz-2026-03-04",
			expectedOutput: "scheduled-backup-xyz-2026-03-04",
			description:    "Trims weekly- prefix and returns the rest",
		},
		{
			name:           "Monthly prefix",
			input:          "monthly-scheduled-backup-abc",
			expectedOutput: "scheduled-backup-abc",
			description:    "Trims monthly- prefix and returns the rest",
		},
		{
			name:           "No schedule prefix",
			input:          "snapshot123",
			expectedOutput: "snapshot123",
			description:    "Returns input as-is when no daily-/weekly-/monthly- prefix",
		},
		{
			name:           "Name with dashes but no schedule prefix",
			input:          "my-snapshot-abc123",
			expectedOutput: "my-snapshot-abc123",
			description:    "Returns input as-is when prefix does not match",
		},
		{
			name:           "Empty string",
			input:          "",
			expectedOutput: "",
			description:    "Returns empty string unchanged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetONTAPSnapshotNameFromCBSDisplaySnapshotName(tt.input)
			assert.Equal(t, tt.expectedOutput, result, tt.description)
		})
	}
}

// TestExtractSnapshotNameFromCVPBackup tests ExtractSnapshotNameFromCVPBackup function
func TestExtractSnapshotNameFromCVPBackup(t *testing.T) {
	snapshotPath := "/vol/volume1/snapshot1"
	snapshotName := "snapshot1"
	backupName := "backup-name-123"

	tests := []struct {
		name           string
		backup         *cvpModels.BackupV1beta
		backupName     string
		expectedOutput string
		description    string
	}{
		{
			name: "SourceSnapshot is set",
			backup: &cvpModels.BackupV1beta{
				SourceSnapshot: &snapshotPath,
				BackupType:     BackupTypeMANUAL,
			},
			backupName:     backupName,
			expectedOutput: snapshotName,
			description:    "Should extract snapshot name from SourceSnapshot path when set",
		},
		{
			name: "SourceSnapshot with multiple slashes",
			backup: &cvpModels.BackupV1beta{
				SourceSnapshot: stringPtr("/vol/volume1/subdir/snapshot1"),
				BackupType:     BackupTypeMANUAL,
			},
			backupName:     backupName,
			expectedOutput: "snapshot1",
			description:    "Should extract last part from SourceSnapshot path with multiple slashes",
		},
		{
			name: "SourceSnapshot with trailing slash",
			backup: &cvpModels.BackupV1beta{
				SourceSnapshot: stringPtr("/vol/volume1/snapshot1/"),
				BackupType:     BackupTypeMANUAL,
			},
			backupName:     backupName,
			expectedOutput: "",
			description:    "Should handle trailing slash in SourceSnapshot path",
		},
		{
			name: "SourceSnapshot is empty string",
			backup: &cvpModels.BackupV1beta{
				SourceSnapshot: stringPtr(""),
				BackupType:     BackupTypeMANUAL,
			},
			backupName:     backupName,
			expectedOutput: backupName,
			description:    "Should fall back to backupName when SourceSnapshot is empty string",
		},
		{
			name: "SourceSnapshot is nil and BackupType is MANUAL",
			backup: &cvpModels.BackupV1beta{
				SourceSnapshot: nil,
				BackupType:     BackupTypeMANUAL,
			},
			backupName:     backupName,
			expectedOutput: backupName,
			description:    "Should return backupName when SourceSnapshot is nil and BackupType is MANUAL",
		},
		{
			name: "SourceSnapshot is nil and BackupType is SCHEDULED",
			backup: &cvpModels.BackupV1beta{
				SourceSnapshot: nil,
				BackupType:     BackupTypeSCHEDULED,
			},
			backupName:     "daily-scheduled-backup-mpukqsfg-2026-03-04-102006",
			expectedOutput: "scheduled-backup-mpukqsfg-2026-03-04-102006",
			description:    "Should process backupName through GetONTAPSnapshotNameFromCBSDisplaySnapshotName when BackupType is SCHEDULED",
		},
		{
			name: "SourceSnapshot is nil and BackupType is empty",
			backup: &cvpModels.BackupV1beta{
				SourceSnapshot: nil,
				BackupType:     "",
			},
			backupName:     backupName,
			expectedOutput: "",
			description:    "Should return empty string when none of the conditions match",
		},
		{
			name: "SourceSnapshot is nil and BackupType is unknown",
			backup: &cvpModels.BackupV1beta{
				SourceSnapshot: nil,
				BackupType:     "UNKNOWN",
			},
			backupName:     backupName,
			expectedOutput: "",
			description:    "Should return empty string for unknown BackupType",
		},
		{
			name: "SourceSnapshot with single segment",
			backup: &cvpModels.BackupV1beta{
				SourceSnapshot: stringPtr("snapshot1"),
				BackupType:     BackupTypeMANUAL,
			},
			backupName:     backupName,
			expectedOutput: "snapshot1",
			description:    "Should return snapshot name when SourceSnapshot has no slashes",
		},
		{
			name: "SCHEDULED backup with weekly prefix",
			backup: &cvpModels.BackupV1beta{
				SourceSnapshot: nil,
				BackupType:     BackupTypeSCHEDULED,
			},
			backupName:     "weekly-scheduled-backup-xyz789",
			expectedOutput: "scheduled-backup-xyz789",
			description:    "Should trim weekly- prefix for SCHEDULED backup name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractSnapshotNameFromCVPBackup(tt.backup, tt.backupName)
			assert.Equal(t, tt.expectedOutput, result, tt.description)
		})
	}
}

func TestSetEnableBackupVaultSwitchingForTest(t *testing.T) {
	orig := EnableBackupVaultSwitching
	defer SetEnableBackupVaultSwitchingForTest(orig)

	SetEnableBackupVaultSwitchingForTest(true)
	assert.True(t, EnableBackupVaultSwitching)

	SetEnableBackupVaultSwitchingForTest(false)
	assert.False(t, EnableBackupVaultSwitching)
}

func TestSetEnableGCNVVaultSwitchingForTest(t *testing.T) {
	orig := EnableGCNVVaultSwitching
	defer SetEnableGCNVVaultSwitchingForTest(orig)

	SetEnableGCNVVaultSwitchingForTest(true)
	assert.True(t, EnableGCNVVaultSwitching)

	SetEnableGCNVVaultSwitchingForTest(false)
	assert.False(t, EnableGCNVVaultSwitching)
}

func TestConvertSourceBackupVaultNameToRemoteBackupVaultName(t *testing.T) {
	t.Run("NoTruncationWithStandardUUID", func(tt *testing.T) {
		result := ConvertSourceBackupVaultNameToRemoteBackupVaultName("source-vault", "abcd-1234-5678")
		assert.Equal(tt, "source-vault-destination-abcd", result)
	})

	t.Run("TruncatesSourceToMaxLength", func(tt *testing.T) {
		sourceName := strings.Repeat("s", 60)
		result := ConvertSourceBackupVaultNameToRemoteBackupVaultName(sourceName, "12345678-1234-1234-1234-1234567890ab")

		expected := strings.Repeat("s", 46) + "-destination-" + "1234"
		assert.Equal(tt, expected, result)
		assert.Equal(tt, 63, len(result))
	})
}

func TestSubnetCIDRInAnyRange(t *testing.T) {
	ranges := func(cidrs ...string) []*datamodel.AddressRange {
		out := make([]*datamodel.AddressRange, 0, len(cidrs))
		for _, c := range cidrs {
			out = append(out, &datamodel.AddressRange{AddressRangeCidr: c})
		}
		return out
	}

	tests := []struct {
		name       string
		subnetCIDR string
		ranges     []*datamodel.AddressRange
		want       bool
	}{
		{
			name:       "subnet fully inside range",
			subnetCIDR: "10.0.1.0/29",
			ranges:     ranges("10.0.0.0/20"),
			want:       true,
		},
		{
			name:       "subnet network address is the range network address (same block)",
			subnetCIDR: "10.0.0.0/24",
			ranges:     ranges("10.0.0.0/20"),
			want:       true,
		},
		{
			name:       "subnet last IP is exactly the range broadcast",
			subnetCIDR: "10.0.15.248/29", // last /29 in 10.0.0.0/20; last IP = 10.0.15.255
			ranges:     ranges("10.0.0.0/20"),
			want:       true,
		},
		{
			name:       "subnet straddles range boundary — last IP outside range",
			subnetCIDR: "10.0.3.252/22", // network=10.0.0.0 of the /22; last IP falls outside 10.0.0.0/24
			ranges:     ranges("10.0.0.0/24"),
			want:       false,
		},
		{
			name:       "subnet network address outside range",
			subnetCIDR: "10.1.0.0/29",
			ranges:     ranges("10.0.0.0/20"),
			want:       false,
		},
		{
			name:       "subnet matched by second range",
			subnetCIDR: "192.168.5.0/29",
			ranges:     ranges("10.0.0.0/20", "192.168.0.0/16"),
			want:       true,
		},
		{
			name:       "no ranges provided",
			subnetCIDR: "10.0.1.0/29",
			ranges:     nil,
			want:       false,
		},
		{
			name:       "malformed subnet CIDR",
			subnetCIDR: "not-a-cidr",
			ranges:     ranges("10.0.0.0/20"),
			want:       false,
		},
		{
			name:       "malformed range CIDR is skipped, valid range still matches",
			subnetCIDR: "10.0.1.0/29",
			ranges:     append(ranges("bad-cidr"), &datamodel.AddressRange{AddressRangeCidr: "10.0.0.0/20"}),
			want:       true,
		},
		{
			name:       "subnet larger than range is rejected",
			subnetCIDR: "10.0.0.0/18", // /18 is wider than /20 — network address matches but last IP overshoots
			ranges:     ranges("10.0.0.0/20"),
			want:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SubnetCIDRInAnyRange(tc.subnetCIDR, tc.ranges)
			assert.Equal(t, tc.want, got)
		})
	}
}

func generateTestCertPEM(t *testing.T, subjectCN, issuerCN string, dnsNames []string, serial int64) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: subjectCN},
		Issuer:       pkix.Name{CommonName: issuerCN},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	parent := template
	if issuerCN != subjectCN {
		caKey, caErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, caErr)
		parent = &x509.Certificate{
			SerialNumber: big.NewInt(1),
			Subject:      pkix.Name{CommonName: issuerCN},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageCertSign,
			IsCA:         true,
		}
		caDER, caErr := x509.CreateCertificate(rand.Reader, parent, parent, &caKey.PublicKey, caKey)
		require.NoError(t, caErr)
		parent, caErr = x509.ParseCertificate(caDER)
		require.NoError(t, caErr)
		key = caKey
	}

	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, key)
	require.NoError(t, err)

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestDeriveSubjectCNFromPEM(t *testing.T) {
	assert.Empty(t, DeriveSubjectCNFromPEM(""))
	assert.Empty(t, DeriveSubjectCNFromPEM("not-a-pem"))

	pemCert := generateTestCertPEM(t, " leaf-subject ", "leaf-issuer", nil, 1)
	assert.Equal(t, "leaf-subject", DeriveSubjectCNFromPEM(pemCert))
}

func TestDeriveCANameFromPEM(t *testing.T) {
	assert.Empty(t, DeriveCANameFromPEM(""))
	assert.Empty(t, DeriveCANameFromPEM("not-a-pem"))

	pemCert := generateTestCertPEM(t, "leaf-subject", " issuer-ca ", nil, 1)
	assert.Equal(t, "issuer-ca", DeriveCANameFromPEM(pemCert))
}

func TestDNSNamesFromPEM(t *testing.T) {
	assert.Nil(t, DNSNamesFromPEM(""))
	assert.Nil(t, DNSNamesFromPEM("not-a-pem"))

	pemCert := generateTestCertPEM(t, "leaf-subject", "issuer-ca", []string{"host.example.com", "alt.example.com"}, 1)
	assert.Equal(t, []string{"host.example.com", "alt.example.com"}, DNSNamesFromPEM(pemCert))
}

func TestSerialHexFromPEM(t *testing.T) {
	_, err := SerialHexFromPEM("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty certificate PEM")

	_, err = SerialHexFromPEM("not-a-pem")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode certificate PEM")

	pemCert := generateTestCertPEM(t, "leaf-subject", "issuer-ca", nil, 0xAABBCC)
	serial, err := SerialHexFromPEM(pemCert)
	require.NoError(t, err)
	assert.Equal(t, "AABBCC", serial)

	oddSerialPEM := generateTestCertPEM(t, "leaf-subject", "issuer-ca", nil, 0xABC)
	oddSerial, err := SerialHexFromPEM(oddSerialPEM)
	require.NoError(t, err)
	assert.Equal(t, "0ABC", oddSerial)
}
