package helper

import (
	"testing"

	"github.com/stretchr/testify/assert"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
)

func TestStringToActiveDirectoryState(t *testing.T) {
	tests := []struct {
		state    string
		expected gcpgenserver.ActiveDirectoryV1betaActiveDirectoryState
	}{
		{"CREATING", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateCREATING},
		{"UPDATING", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateUPDATING},
		{"READY", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateREADY},
		{"IN_USE", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateINUSE},
		{"DELETING", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateDELETING},
		{"ERROR", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR},
		{"DIAGNOSING", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateDIAGNOSING},
		{"SOMETHING_ELSE", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateSTATEUNSPECIFIED},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			assert.Equal(t, tt.expected, stringToActiveDirectoryState(tt.state))
		})
	}
}
