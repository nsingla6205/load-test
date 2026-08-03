package security

import "testing"

func TestNewGcpKmsRekeyExternalParams_DefaultQueryValuesAreUnquoted(t *testing.T) {
	params := NewGcpKmsRekeyExternalParams()
	if params.ReturnRecords == nil || *params.ReturnRecords != "false" {
		t.Fatalf("expected ReturnRecords default false, got %#v", params.ReturnRecords)
	}
	if params.ReturnTimeout == nil || *params.ReturnTimeout != "0" {
		t.Fatalf("expected ReturnTimeout default 0, got %#v", params.ReturnTimeout)
	}
}
