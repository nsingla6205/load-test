package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewFilterCondition(t *testing.T) {
	fc := NewFilterCondition("", "", nil)
	assert.NotNil(t, fc)
	assert.Equal(t, "", fc.Field)
	assert.Equal(t, "", fc.Op)
	assert.Nil(t, fc.Value)
}

func TestFilterCondition_WithConditions(t *testing.T) {
	fc := &FilterCondition{}
	result := fc.WithConditions("age", ">", 18)
	assert.Equal(t, fc, result)
	assert.Equal(t, "age", fc.Field)
	assert.Equal(t, ">", fc.Op)
	assert.Equal(t, 18, fc.Value)
}

func TestCreateFilterWithConditions(t *testing.T) {
	cond1 := &FilterCondition{Field: "name", Op: "=", Value: "john"}
	cond2 := &FilterCondition{Field: "age", Op: ">", Value: 21}
	filter := CreateFilterWithConditions(cond1, cond2)
	assert.NotNil(t, filter)
	assert.Len(t, filter.Conditions, 2)
	assert.Equal(t, cond1, filter.Conditions[0])
	assert.Equal(t, cond2, filter.Conditions[1])
}

func TestFilter_SetIncludeDeleted(t *testing.T) {
	filter := &Filter{}
	filter.SetIncludeDeleted(true)
	assert.NotNil(t, filter.IncludeDeleted)
	assert.True(t, filter.IncludeDeleted)

	filter.SetIncludeDeleted(false)
	assert.False(t, filter.IncludeDeleted)
	assert.NotNil(t, filter.IncludeDeleted)
}

func TestFilter_ShouldIncludeDeleted(t *testing.T) {
	filter := &Filter{}
	assert.False(t, filter.ShouldIncludeDeleted())

	filter.SetIncludeDeleted(true)
	assert.True(t, filter.ShouldIncludeDeleted())

	filter.SetIncludeDeleted(false)
	assert.False(t, filter.ShouldIncludeDeleted())
}

func TestFilter_SetConditions(t *testing.T) {
	filter := &Filter{}
	conds := []*FilterCondition{
		{Field: "foo", Op: "=", Value: "bar"},
	}
	filter.SetConditions(conds)
	assert.Equal(t, conds, filter.Conditions)
}

func TestFilter_ToGORMQuery(t *testing.T) {
	conds := []*FilterCondition{
		{Field: "name", Op: "=", Value: "alice"},
		{Field: "age", Op: ">", Value: 30},
		{Field: "id", Op: "in", Value: []string{"ab", "bc", "ca"}},
		{}, // Empty condition should be ignored
	}
	filter := &Filter{Conditions: conds}
	query := filter.ToGORMQuery()
	assert.Len(t, query, 3)
	assert.Equal(t, []interface{}{"name = ?", "alice"}, query[0])
	assert.Equal(t, []interface{}{"age > ?", 30}, query[1])
	assert.Equal(t, []interface{}{"id in ?", []string{"ab", "bc", "ca"}}, query[2])
}

func TestFilter_ToGORMQueryKeepsUntrustedValuesParameterized(t *testing.T) {
	untrustedValue := "us-central1-a') OR 1=1 --"
	filter := CreateFilterWithConditions(
		NewFilterCondition("zone", "IN", []string{untrustedValue}),
	)

	query := filter.ToGORMQuery()

	assert.Equal(t, [][]interface{}{
		{"zone IN ?", []string{untrustedValue}},
	}, query)
}

func TestFilter_Apply(t *testing.T) {
	conds := []*FilterCondition{
		{Field: "city", Op: "=", Value: "NY"},
	}
	filter := &Filter{Conditions: conds}
	result := filter.Apply()
	assert.Equal(t, filter.ToGORMQuery(), result)
}
