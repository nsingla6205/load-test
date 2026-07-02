package metadata

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ConvertsResourceTypeToStringSuccessfully(t *testing.T) {
	rt := Volume
	assert.Equal(t, "VOLUME", rt.String())
}

func TestIsValidResourceTypeValue(t *testing.T) {
	t.Run("known values", func(t *testing.T) {
		assert.True(t, IsValidResourceTypeValue(string(Volume)))
		assert.True(t, IsValidResourceTypeValue(string(VolumePoolRegionalHA)))
		assert.True(t, IsValidResourceTypeValue(string(BackupVault)))
	})

	t.Run("invalid values", func(t *testing.T) {
		assert.False(t, IsValidResourceTypeValue(""))
		assert.False(t, IsValidResourceTypeValue("NOT_A_REAL_TYPE"))
		assert.False(t, IsValidResourceTypeValue(string(MetricsNamePrefixPoolFirstParty)))
	})
}

func TestResourceTypeValueHelpers(t *testing.T) {
	assert.Equal(t, "VOLUME", ResourceTypeValue(Volume))
	assert.Equal(t, Volume, ResourceTypeFromValue("VOLUME"))
	assert.True(t, ResourceTypeEquals("VOLUME", Volume))
}

func TestValidateResourceTypeValue(t *testing.T) {
	t.Run("valid value", func(t *testing.T) {
		err := ValidateResourceTypeValue(string(Volume))
		assert.NoError(t, err)
	})

	t.Run("invalid value", func(t *testing.T) {
		err := ValidateResourceTypeValue("bad-resource-type")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid resource_type")
	})
}

// TestAllResourceTypeConstsArePersisted guards against enum drift for
// ResourceType. The untyped MetricsNamePrefix* string constants in the same
// const block have no ResourceType type and are naturally skipped by the
// type filter below.
func TestAllResourceTypeConstsArePersisted(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "ResourceType.go", nil, 0)
	require.NoError(t, err)

	declared := map[string]ResourceType{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		id, ok := vs.Type.(*ast.Ident)
		if !ok || id.Name != "ResourceType" {
			return true
		}
		for i, name := range vs.Names {
			if i >= len(vs.Values) {
				continue // e.g. `var result ResourceType` with no assigned value
			}
			lit, ok := vs.Values[i].(*ast.BasicLit)
			if !ok {
				continue
			}
			val, uErr := strconv.Unquote(lit.Value)
			require.NoError(t, uErr)
			declared[name.Name] = ResourceType(val)
		}
		return true
	})
	require.NotEmpty(t, declared, "expected to parse ResourceType constants")

	persisted := map[ResourceType]struct{}{}
	for _, rt := range allPersistedResourceTypeValues() {
		persisted[rt] = struct{}{}
	}

	for name, val := range declared {
		_, ok := persisted[val]
		assert.Truef(t, ok, "const %s (%q) missing from allPersistedResourceTypeValues()", name, val)
	}
}
