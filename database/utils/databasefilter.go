package utils

import (
	"fmt"
)

type FilterCondition struct {
	Field string
	Op    string
	Value interface{}
}

func NewFilterCondition(field string, op string, value interface{}) *FilterCondition {
	return &FilterCondition{
		Field: field,
		Op:    op,
		Value: value,
	}
}

func (fc *FilterCondition) WithConditions(field string, op string, value interface{}) *FilterCondition {
	fc.Field = field
	fc.Op = op
	fc.Value = value
	return fc
}

type Filter struct {
	IncludeDeleted bool
	Conditions     []*FilterCondition
}

func CreateFilterWithConditions(filterConditions ...*FilterCondition) *Filter {
	return &Filter{
		Conditions: filterConditions,
	}
}

func (f *Filter) Apply() [][]interface{} {
	return f.ToGORMQuery()
}

func (f *Filter) SetConditions(filterConditions []*FilterCondition) {
	f.Conditions = filterConditions
}

func (f *Filter) SetIncludeDeleted(includeDeleted bool) {
	f.IncludeDeleted = includeDeleted
}

func (f *Filter) ShouldIncludeDeleted() bool {
	return f.IncludeDeleted
}

func (f *Filter) ToGORMQuery() [][]interface{} {
	var query [][]interface{}

	if f.Conditions == nil {
		return query
	}

	for _, condition := range f.Conditions {
		if condition.Value == nil {
			continue
		}

		query = append(query, []interface{}{
			fmt.Sprintf("%s %s ?", condition.Field, condition.Op),
			condition.Value,
		})
	}
	return query
}
