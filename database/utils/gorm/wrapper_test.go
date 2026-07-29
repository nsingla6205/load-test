package gorm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type TestModel struct {
	ID        uint `gorm:"primaryKey"`
	Name      string
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func setupTestDB(t *testing.T) *Wrapper {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	w := New(db)
	assert.NoError(t, w.AutoMigrate(&TestModel{}))
	return w
}

func TestWrapperBasicOps(t *testing.T) {
	w := setupTestDB(t)

	// Test Create
	model := &TestModel{Name: "foo"}
	w2 := w.Create(model)
	assert.NoError(t, w2.Error())
	assert.NotZero(t, model.ID)

	// Test First
	var found TestModel
	w3 := w.First(&found, "id = ?", model.ID)
	assert.NoError(t, w3.Error())
	assert.Equal(t, model.Name, found.Name)

	// Test Save
	found.Name = "bar"
	w4 := w.Save(&found)
	assert.NoError(t, w4.Error())

	// Test Where
	w5 := w.Where("name = ?", "bar").First(&found)
	assert.NoError(t, w5.Error())
	assert.Equal(t, "bar", found.Name)

	// Test Delete (soft delete)
	w6 := w.Delete(&found)
	assert.NoError(t, w6.Error())

	// Ensure record is deleted (soft delete)
	var deleted TestModel
	w7 := w.First(&deleted, "id = ?", found.ID)
	assert.Error(t, w7.Error())

	// Ensure record still exists with Unscoped (soft delete)
	w8 := w.Unscoped().First(&deleted, "id = ?", found.ID)
	assert.NoError(t, w8.Error())
	assert.True(t, deleted.DeletedAt.Valid)

	// Now hard delete
	w9 := w.Unscoped().Delete(&deleted)
	assert.NoError(t, w9.Error())
	w10 := w.Unscoped().First(&deleted, "id = ?", found.ID)
	assert.Error(t, w10.Error())
}

func TestWrapperTransaction(t *testing.T) {
	w := setupTestDB(t)
	wTx := w.Begin()
	model := &TestModel{Name: "tx"}
	wTx.Create(model)
	assert.NoError(t, wTx.Commit())

	var found TestModel
	w.First(&found, "id = ?", model.ID)
	assert.Equal(t, "tx", found.Name)

	wTx2 := w.Begin()
	model2 := &TestModel{Name: "rollback"}
	wTx2.Create(model2)
	assert.NoError(t, wTx2.Rollback())
	// After rollback, model2 should not exist
	var notFound TestModel
	w2 := w.Where("name = ?", "rollback").First(&notFound)
	assert.Error(t, w2.Error())
}

func TestWrapperSetRawExecScan(t *testing.T) {
	w := setupTestDB(t)
	w.Set("gorm:query_option", "FOR UPDATE")
	w.Exec("INSERT INTO test_models (name) VALUES (?)", "rawexec")
	var found TestModel
	w.Raw("SELECT * FROM test_models WHERE name = ?", "rawexec").Scan(&found)
	assert.Equal(t, "rawexec", found.Name)
}

func TestWrapperWithContextUnscopedApplyFilter(t *testing.T) {
	w := setupTestDB(t)
	w.Create(&TestModel{Name: "a"})
	w.Create(&TestModel{Name: "b"})
	ctx := context.Background()
	w2 := w.WithContext(ctx).Where("name = ?", "a")
	var found TestModel
	w2.First(&found)
	assert.Equal(t, "a", found.Name)

	w3 := w.Unscoped().Where("name = ?", "b")
	var foundB TestModel
	w3.First(&foundB)
	assert.Equal(t, "b", foundB.Name)

	filters := [][]interface{}{{"name = ?", "a"}}
	w4 := w.ApplyFilter(filters)
	var foundA TestModel
	w4.First(&foundA)
	assert.Equal(t, "a", foundA.Name)
}

func TestWrapperDBError(t *testing.T) {
	w := setupTestDB(t)
	w2 := w.Where("name = ?", "notfound").First(&TestModel{})
	assert.Error(t, w2.Error())
}

func TestWrapperSetAndDB(t *testing.T) {
	w := setupTestDB(t)
	w2 := w.Set("gorm:query_option", "FOR UPDATE")
	assert.NotNil(t, w2)
	// DB() should return *sql.DB and no error
	sqldb, err := w.DB()
	assert.NoError(t, err)
	assert.NotNil(t, sqldb)
}

func TestWrapperApplyFilterMultipleConditions(t *testing.T) {
	w := setupTestDB(t)
	w.Create(&TestModel{Name: "x"})
	w.Create(&TestModel{Name: "y"})
	w.Create(&TestModel{Name: "z"})
	filters := [][]interface{}{
		{"name = ?", "x"},
		{"id > ?", 0},
	}
	var found TestModel
	w2 := w.ApplyFilter(filters).First(&found)
	assert.NoError(t, w2.Error())
	assert.Equal(t, "x", found.Name)
}

func TestWrapperApplyFilterParameterizedSlice(t *testing.T) {
	w := setupTestDB(t)
	w.Create(&TestModel{Name: "safe"})
	w.Create(&TestModel{Name: "other"})

	untrustedValue := "missing') OR 1=1 --"
	filters := [][]interface{}{
		{"name IN ?", []string{"safe", untrustedValue}},
	}

	var found []TestModel
	err := w.ApplyFilter(filters).GORM().Find(&found).Error

	assert.NoError(t, err)
	assert.Len(t, found, 1)
	assert.Equal(t, "safe", found[0].Name)
}
