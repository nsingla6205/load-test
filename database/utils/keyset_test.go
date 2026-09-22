package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestKeyset_IsFirstPage(t *testing.T) {
	cursorTimestamp := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		keyset   *Keyset
		expected bool
	}{
		{name: "nil keyset", keyset: nil, expected: true},
		{name: "empty cursor", keyset: &Keyset{Limit: 100}, expected: true},
		{name: "complete cursor", keyset: &Keyset{AfterTimestamp: cursorTimestamp, AfterID: 42}, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.keyset.IsFirstPage())
		})
	}
}

func TestKeyset_Validate(t *testing.T) {
	cursorTimestamp := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		keyset    *Keyset
		shouldErr bool
	}{
		{name: "nil keyset", keyset: nil},
		{name: "empty cursor", keyset: &Keyset{Limit: 100}},
		{name: "complete cursor", keyset: &Keyset{Limit: 100, AfterTimestamp: cursorTimestamp, AfterID: 42}},
		{name: "timestamp only", keyset: &Keyset{Limit: 100, AfterTimestamp: cursorTimestamp}, shouldErr: true},
		{name: "ID only", keyset: &Keyset{Limit: 100, AfterID: 42}, shouldErr: true},
		{name: "zero limit", keyset: &Keyset{}, shouldErr: true},
		{name: "negative limit", keyset: &Keyset{Limit: -1}, shouldErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.shouldErr {
				assert.Error(t, tt.keyset.Validate())
				return
			}
			assert.NoError(t, tt.keyset.Validate())
		})
	}
}

func TestKeyset_PageLimit(t *testing.T) {
	tests := []struct {
		name     string
		keyset   *Keyset
		expected int
	}{
		{name: "nil keyset", keyset: nil, expected: defaultKeysetLimit},
		{name: "zero limit", keyset: &Keyset{}, expected: defaultKeysetLimit},
		{name: "negative limit", keyset: &Keyset{Limit: -1}, expected: defaultKeysetLimit},
		{name: "custom limit", keyset: &Keyset{Limit: 250}, expected: 250},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.keyset.PageLimit())
		})
	}
}
