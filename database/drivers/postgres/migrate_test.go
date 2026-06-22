package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type checksumSample struct {
	ID   int    `gorm:"primaryKey"`
	Name string
}

type checksumOther struct {
	Value string `gorm:"column:value"`
}

func TestMigrator_calculateChecksum(t *testing.T) {
	m := &Migrator{}

	t.Run("dereferences pointer models to the same checksum as value models", func(t *testing.T) {
		valueSum, err := m.calculateChecksum([]interface{}{checksumSample{}})
		require.NoError(t, err)
		assert.NotEmpty(t, valueSum)

		ptrSum, err := m.calculateChecksum([]interface{}{&checksumSample{}})
		require.NoError(t, err)

		assert.Equal(t, valueSum, ptrSum, "a pointer model must hash identically to its element type")
	})

	t.Run("is deterministic for the same models", func(t *testing.T) {
		first, err := m.calculateChecksum([]interface{}{checksumSample{}, &checksumOther{}})
		require.NoError(t, err)

		second, err := m.calculateChecksum([]interface{}{checksumSample{}, &checksumOther{}})
		require.NoError(t, err)

		assert.Equal(t, first, second)
	})

	t.Run("differs when the set of models changes", func(t *testing.T) {
		single, err := m.calculateChecksum([]interface{}{checksumSample{}})
		require.NoError(t, err)

		combined, err := m.calculateChecksum([]interface{}{checksumSample{}, checksumOther{}})
		require.NoError(t, err)

		assert.NotEqual(t, single, combined)
	})
}
