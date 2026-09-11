package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
