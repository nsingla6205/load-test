package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPtrBoolEqual(t *testing.T) {
	trueVal := true
	falseVal := false

	assert.True(t, PtrBoolEqual(nil, nil))
	assert.False(t, PtrBoolEqual(&trueVal, nil))
	assert.False(t, PtrBoolEqual(nil, &falseVal))
	assert.True(t, PtrBoolEqual(&trueVal, &trueVal))
	assert.False(t, PtrBoolEqual(&trueVal, &falseVal))
}

func TestPtrInt64Equal(t *testing.T) {
	one := int64(1)
	two := int64(2)

	assert.True(t, PtrInt64Equal(nil, nil))
	assert.False(t, PtrInt64Equal(&one, nil))
	assert.False(t, PtrInt64Equal(nil, &two))
	assert.True(t, PtrInt64Equal(&one, &one))
	assert.False(t, PtrInt64Equal(&one, &two))
}
