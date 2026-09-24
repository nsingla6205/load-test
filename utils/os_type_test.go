package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapONTAPOSTypeToAPIEnum(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "ontap vmware maps to ESXI", input: "vmware", want: APIOSTypeESXI},
		{name: "uppercase VMWARE maps to ESXI", input: "VMWARE", want: APIOSTypeESXI},
		{name: "esxi stays ESXI", input: "ESXI", want: APIOSTypeESXI},
		{name: "linux", input: "linux", want: APIOSTypeLinux},
		{name: "windows", input: "WINDOWS", want: APIOSTypeWindows},
		{name: "empty stays empty", input: "", want: ""},
		{name: "unknown uppercased", input: "solaris", want: "SOLARIS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, MapONTAPOSTypeToAPIEnum(tt.input))
		})
	}
}
