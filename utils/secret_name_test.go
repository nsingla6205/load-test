package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBareSecretName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "qualified resource name", in: "projects/123/secrets/admin-pw", want: "admin-pw"},
		{name: "qualified with version", in: "projects/123/secrets/admin-pw/versions/latest", want: "admin-pw"},
		{name: "qualified with numeric version", in: "projects/my-proj/secrets/admin-pw/versions/4", want: "admin-pw"},
		{name: "leading slash tolerated", in: "/projects/123/secrets/admin-pw", want: "admin-pw"},
		{name: "already bare", in: "admin-pw", want: "admin-pw"},
		{name: "padded bare", in: "  admin-pw  ", want: "admin-pw"},
		{name: "empty", in: "", want: ""},
		{name: "trailing secrets segment left alone", in: "projects/123/secrets", want: "projects/123/secrets"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, BareSecretName(tc.in))
		})
	}
}

func TestParseSecretResourceName(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantProject string
		wantSecret  string
		wantErr     bool
	}{
		{
			name:        "bare secret form",
			in:          "projects/237071556299/secrets/ontap-admin-password",
			wantProject: "237071556299",
			wantSecret:  "ontap-admin-password",
		},
		{
			name:        "versioned form ignores version",
			in:          "projects/proj-1/secrets/sec-1/versions/5",
			wantProject: "proj-1",
			wantSecret:  "sec-1",
		},
		{
			name:        "leading and trailing slashes trimmed",
			in:          "/projects/proj-1/secrets/sec-1/",
			wantProject: "proj-1",
			wantSecret:  "sec-1",
		},
		{name: "empty", in: "", wantErr: true},
		{name: "wrong prefix", in: "foo/proj/secrets/sec", wantErr: true},
		{name: "missing secrets segment", in: "projects/proj/bar/sec", wantErr: true},
		{name: "empty project", in: "projects//secrets/sec", wantErr: true},
		{name: "empty secret", in: "projects/proj/secrets/", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			project, secret, err := ParseSecretResourceName(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantProject, project)
			assert.Equal(t, tc.wantSecret, secret)
		})
	}
}
