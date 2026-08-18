package workflows

import "testing"

func TestNormalizeADCRestPort(t *testing.T) {
	testCases := []struct {
		name string
		port int
		want int
	}{
		{name: "minimum valid port", port: 1, want: 1},
		{name: "maximum valid port", port: maxTCPPort, want: maxTCPPort},
		{name: "zero", port: 0, want: defaultADCRestPort},
		{name: "negative", port: -1, want: defaultADCRestPort},
		{name: "above maximum", port: maxTCPPort + 1, want: defaultADCRestPort},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := normalizeADCRestPort(testCase.port); got != testCase.want {
				t.Errorf("normalizeADCRestPort(%d) = %d, want %d", testCase.port, got, testCase.want)
			}
		})
	}
}
