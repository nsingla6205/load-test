package opsinventory

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPageToken(t *testing.T) {
	key := listFilterKey{resourceType: "volume", projectNumber: "1", parents: []string{"pool:a", "svm:b"}, fieldFilters: map[string]string{"state": "READY"}}
	token := encodePageToken(42, key.hash())

	t.Run("WhenTokenMatchesTheFilters", func(t *testing.T) {
		id, err := decodePageToken(token, key.hash())
		require.NoError(t, err)
		assert.Equal(t, int64(42), id)
	})

	t.Run("WhenFiltersChange", func(t *testing.T) {
		for name, change := range map[string]func(k *listFilterKey){
			"type":      func(k *listFilterKey) { k.resourceType = "pool" },
			"project":   func(k *listFilterKey) { k.projectNumber = "2" },
			"parent":    func(k *listFilterKey) { k.parents = []string{"pool:a"} },
			"time":      func(k *listFilterKey) { k.timeFilters = []string{"createdAt:[x,)"} },
			"filter":    func(k *listFilterKey) { k.fieldFilters = map[string]string{"state": "ERROR"} },
			"deleted":   func(k *listFilterKey) { k.includeDeleted = true },
			"key=value": func(k *listFilterKey) { k.fieldFilters = map[string]string{"state=READY": ""} },
		} {
			changed := key
			change(&changed)
			_, err := decodePageToken(token, changed.hash())
			assert.ErrorIs(t, err, errPageTokenMismatch, name)
		}
	})

	t.Run("WhenTokenIsMalformed", func(t *testing.T) {
		for _, bad := range []string{"%%%", base64.RawURLEncoding.EncodeToString([]byte("not json")), encodePageToken(0, key.hash()), encodePageToken(-5, key.hash())} {
			_, err := decodePageToken(bad, key.hash())
			assert.ErrorIs(t, err, errBadPageToken, bad)
		}
	})
}

func TestListFilterKeyHash(t *testing.T) {
	t.Run("WhenParentAndTimeOrderDiffers", func(t *testing.T) {
		a := listFilterKey{parents: []string{"pool:a", "svm:b"}, timeFilters: []string{"x", "y"}}
		b := listFilterKey{parents: []string{"svm:b", "pool:a"}, timeFilters: []string{"y", "x"}}
		assert.Equal(t, a.hash(), b.hash())
	})

	t.Run("WhenFieldsCouldBeConcatenatedTheSame", func(t *testing.T) {
		a := listFilterKey{parents: []string{"a,b"}}
		b := listFilterKey{parents: []string{"a", "b"}}
		assert.NotEqual(t, a.hash(), b.hash())
	})

	t.Run("WhenKeyIsUnchanged", func(t *testing.T) {
		k := listFilterKey{resourceType: "job", fieldFilters: map[string]string{"type": "CREATE_POOL", "state": "NEW"}}
		assert.Equal(t, k.hash(), k.hash())
		assert.Len(t, k.hash(), 16)
	})
}

func TestFormatTimeFilter(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+1800)
	from := time.Date(2026, 10, 1, 5, 30, 0, 0, ist)
	to := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)

	t.Run("WhenBothBoundsAreSet", func(t *testing.T) {
		assert.Equal(t, "createdAt:[2026-10-01T00:00:00Z,2026-10-02T00:00:00Z)", formatTimeFilter("createdAt", &from, &to))
	})

	t.Run("WhenOneBoundIsOpen", func(t *testing.T) {
		assert.Equal(t, "updatedAt:[,2026-10-02T00:00:00Z)", formatTimeFilter("updatedAt", nil, &to))
		assert.Equal(t, "updatedAt:[2026-10-01T00:00:00Z,)", formatTimeFilter("updatedAt", &from, nil))
	})
}
