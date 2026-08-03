package utils

import (
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// alwaysErrReader makes crypto/rand.Int fail (Read errors), exercising secureIntn retries and fallback.
type alwaysErrReader struct{}

func (alwaysErrReader) Read(_ []byte) (int, error) {
	return 0, errors.New("mock: secure random read failed")
}

func restoreSecureIntnDeps(t *testing.T) {
	t.Helper()
	prevReader := secureRandomSource
	prevNow := secureIntnNow
	t.Cleanup(func() {
		secureRandomSource = prevReader
		secureIntnNow = prevNow
	})
}

func TestGenerateRandomAlphanumeric(t *testing.T) {
	if r := GenerateRandomAlphanumeric(0); len(r) != 0 {
		t.Fail()
	}
	if r := GenerateRandomAlphanumeric(1); len(r) == 0 {
		t.Fail()
	}
	if r := GenerateRandomAlphanumeric(2); len(r) == 0 {
		t.Fail()
	}
	if r := GenerateRandomAlphanumeric(10); len(r) == 0 {
		t.Fail()
	}
}

func TestGenerateRandomHex(t *testing.T) {
	if r := GenerateRandomHex(0); len(r) != 0 {
		t.Fail()
	}
	if r := GenerateRandomHex(1); len(r) == 0 {
		t.Fail()
	}
	if r := GenerateRandomHex(2); len(r) == 0 {
		t.Fail()
	}
	if r := GenerateRandomHex(10); len(r) == 0 {
		t.Fail()
	}
}

func TestGenerateRandomInRange(t *testing.T) {
	if r := GenerateRandomInRange(1); r < 0 {
		t.Fail()
	}
	if r := GenerateRandomInRange(2); r < 0 {
		t.Fail()
	}
	if r := GenerateRandomInRange(10); r < 0 {
		t.Fail()
	}
	if r := GenerateRandomInRange(10000); r < 0 {
		t.Fail()
	}
}

const (
	uuidLength = 36
	uuidFormat = "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"
)

func TestRandomUUID(t *testing.T) {
	re := regexp.MustCompile(uuidFormat)
	if re == nil {
		t.Error("Regular expression for UUID format failed to compile")
	}

	uuid := RandomUUID()
	if len(uuid) != uuidLength {
		t.Errorf("Length %v of random UUID does not match expected length of %v", len(uuid), uuidLength)
	}
	if !re.MatchString(uuid) {
		t.Error("Random UUID does not match expected UUID format")
	}

	uuids := make(map[string]bool)
	uuids[uuid] = true
	// Keep uniqueness coverage without spending seconds on a million allocations.
	numUniquenessChecks := 10000
	for i := 1; i < numUniquenessChecks; i++ {
		uuids[RandomUUID()] = true
	}
	if len(uuids) != numUniquenessChecks {
		t.Errorf("Insufficient uniqueness for RandomUUID() - %v unique UUIDs out of %v calls", len(uuids), numUniquenessChecks)
	}
}

func TestIsValidUUID(t *testing.T) {
	t.Run("WithInvalidUUIDString", func(tt *testing.T) {
		testString := "sOmE-rANdom-string123"
		if IsValidUUID(testString) {
			tt.Errorf("IsValidUUID giving wrong response: true for invalid uuid string %s", testString)
		}
	})
	t.Run("WithValidUUIDString", func(tt *testing.T) {
		testString := "54bD70b1-Bc09-45A0-9A38-1f929EC352c3"
		if !IsValidUUID(testString) {
			tt.Errorf("IsValidUUID giving wrong response: false for valid uuid string %s", testString)
		}
	})
}

func TestGetOperationUUID(t *testing.T) {
	t.Run("TestGetOperationUUIDReturnsInputWhenNoSlashesPresent", func(tt *testing.T) {
		operationID := "no-slash-uuid"
		if got := GetOperationUUID(operationID); got != operationID {
			t.Errorf("expected %s, got %s", operationID, got)
		}
	})
	t.Run("TestGetOperationUUIDReturnsEmptyStringForEmptyInput", func(tt *testing.T) {
		if got := GetOperationUUID(""); got != "" {
			t.Errorf("expected empty string, got %s", got)
		}
	})
	t.Run("TestGetOperationUUIDReturnsLastSegmentForNonStandardOperationID", func(tt *testing.T) {
		operationID := "foo/bar/baz/last-segment"
		expected := "last-segment"
		if got := GetOperationUUID(operationID); got != expected {
			t.Errorf("expected %s, got %s", expected, got)
		}
	})

	t.Run("TestGetOperationUUIDReturnsUUIDFromValidOperationID", func(tt *testing.T) {
		operationID := "projects/123456789/locations/us-central1/operations/12345678-1234-5678-9012-123456789012"
		expectedUUID := "12345678-1234-5678-9012-123456789012"
		if got := GetOperationUUID(operationID); got != expectedUUID {
			t.Errorf("expected %s, got %s", expectedUUID, got)
		}
	})
}

func TestGenerateDeterministicDeploymentName(t *testing.T) {
	accountID := int64(12345)
	poolID := "test-pool"
	region := "us-central1"
	name := GenerateDeterministicDeploymentName(accountID, poolID, region)

	// Check length
	if len(name) != 20 {
		t.Errorf("Expected length 20, got %d", len(name))
	}

	// Check prefix
	if name[:5] != "gcnv-" {
		t.Errorf("Expected prefix 'gcnv-', got %s", name[:5])
	}

	// Check hex part
	hexPart := name[5:]
	if len(hexPart) != 15 {
		t.Errorf("Expected hex part length 15, got %d", len(hexPart))
	}
	hexRegex := regexp.MustCompile("^[0-9a-f]{15}$")
	if !hexRegex.MatchString(hexPart) {
		t.Errorf("Hex part contains invalid characters: %s", hexPart)
	}

	// Check determinism
	name2 := GenerateDeterministicDeploymentName(accountID, poolID, region)
	assert.Equal(t, name, name2, "Expected deterministic output to be the same for the same inputs")

	// Check uniqueness for different inputs
	name3 := GenerateDeterministicDeploymentName(accountID+1, poolID, region)
	assert.NotEqual(t, name, name3, "Expected different output for different accountID")

	name4 := GenerateDeterministicDeploymentName(accountID, poolID+"-diff", region)
	assert.NotEqual(t, name, name4, "Expected different output for different poolID")

	name5 := GenerateDeterministicDeploymentName(accountID, poolID, region+"-diff")
	assert.NotEqual(t, name, name5, "Expected different output for different region")
}

func TestGenerateDeterministicDeploymentNameForIntegrationTests(t *testing.T) {
	accountID := int64(12345)
	poolID := "test-pool"
	region := "us-central1"

	originalEnv := strconv.FormatBool(env.GetBool("INTEGRATION_TEST", false))
	// Restore the original value after the test
	err := os.Setenv("INTEGRATION_TEST", "true")
	if err != nil {
		return
	}
	defer func() {
		err := os.Setenv("INTEGRATION_TEST", originalEnv)
		if err != nil {
			return
		}
	}()

	name := GenerateDeterministicDeploymentName(accountID, poolID, region)
	assert.Equal(t, name, "integration-test", "Expected deterministic output to be 'integration-test'")
}

// Unit test for GenerateUniqueUsername
func TestGenerateUniqueUsername(t *testing.T) {
	input := "testuser"
	username1 := GenerateUniqueUsername(input)

	// Sleep to ensure different timestamp for the next call
	// (consecutive calls can have the same nanosecond timestamp on fast machines)
	time.Sleep(time.Millisecond)

	username2 := GenerateUniqueUsername(input)

	// Should be 8 characters
	if len(username1) != 8 {
		t.Errorf("Expected length 8, got %d", len(username1))
	}

	// Should be different for different timestamps
	if username1 == username2 {
		t.Errorf("Expected different usernames for different timestamps, got %s and %s", username1, username2)
	}

	// Should only contain hex characters
	matched, err := regexp.MatchString("^[a-f0-9]{8}$", username1)
	if err != nil || !matched {
		t.Errorf("Username contains invalid characters: %s", username1)
	}
}

func TestSecureIntn(t *testing.T) {
	t.Run("WhenNIsPositive", func(tt *testing.T) {
		// Test with various positive values
		testCases := []int{1, 10, 100, 1000}
		for _, n := range testCases {
			result, err := secureIntn(n)
			if err != nil {
				tt.Errorf("secureIntn(%d) returned unexpected error: %v", n, err)
			}
			if result < 0 || result >= n {
				tt.Errorf("secureIntn(%d) returned value %d out of range [0, %d)", n, result, n)
			}
		}
	})

	t.Run("WhenNIsZero", func(tt *testing.T) {
		result, err := secureIntn(0)
		if err == nil {
			tt.Error("secureIntn(0) should return an error")
		}
		if result != 0 {
			tt.Errorf("secureIntn(0) should return 0, got %d", result)
		}
	})

	t.Run("WhenNIsNegative", func(tt *testing.T) {
		result, err := secureIntn(-1)
		if err == nil {
			tt.Error("secureIntn(-1) should return an error")
		}
		if result != 0 {
			tt.Errorf("secureIntn(-1) should return 0, got %d", result)
		}
	})

	t.Run("WhenRandReaderSucceeds", func(tt *testing.T) {
		// This test verifies that secureIntn works correctly with the real rand.Reader
		// and returns values in the correct range
		for i := 0; i < 100; i++ {
			result, err := secureIntn(100)
			if err != nil {
				tt.Errorf("secureIntn(100) returned unexpected error on iteration %d: %v", i, err)
			}
			if result < 0 || result >= 100 {
				tt.Errorf("secureIntn(100) returned value %d out of range [0, 100) on iteration %d", result, i)
			}
		}
	})

	t.Run("MultipleCallsReturnDifferentValues", func(tt *testing.T) {
		// Verify that multiple calls return different values (randomness)
		results := make(map[int]bool)
		n := 1000
		uniqueCount := 0

		for i := 0; i < 100; i++ {
			result, err := secureIntn(n)
			if err != nil {
				tt.Fatalf("secureIntn(%d) returned error: %v", n, err)
			}
			if !results[result] {
				results[result] = true
				uniqueCount++
			}
		}

		// With 100 calls and range of 1000, we should get many unique values
		// This is a probabilistic test, but should pass with high probability
		if uniqueCount < 50 {
			tt.Errorf("Expected at least 50 unique values from 100 calls, got %d", uniqueCount)
		}
	})

	t.Run("WhenRandReaderAlwaysFailsUsesTimeFallback", func(tt *testing.T) {
		restoreSecureIntnDeps(tt)
		secureRandomSource = alwaysErrReader{}
		fixed := time.Unix(1000, 123456789)
		secureIntnNow = func() time.Time { return fixed }

		n := 17
		result, err := secureIntn(n)
		if err != nil {
			tt.Fatalf("expected nil error after fallback, got %v", err)
		}
		if result < 0 || result >= n {
			tt.Fatalf("fallback out of range: got %d want [0,%d)", result, n)
		}
		u := fixed.UnixNano()
		expected := int(u % int64(n))
		if result != expected {
			tt.Fatalf("fallback mismatch: got %d want %d (UnixNano %% n)", result, expected)
		}
	})

	t.Run("WhenRandReaderAlwaysFailsFallbackUsesAbsForNegativeUnixNano", func(tt *testing.T) {
		restoreSecureIntnDeps(tt)
		secureRandomSource = alwaysErrReader{}
		// Before 1970 => negative UnixNano
		secureIntnNow = func() time.Time {
			return time.Date(1969, 12, 31, 23, 59, 59, 0, time.UTC)
		}

		n := 7
		result, err := secureIntn(n)
		if err != nil {
			tt.Fatalf("expected nil error after fallback, got %v", err)
		}
		if result < 0 || result >= n {
			tt.Fatalf("fallback out of range: got %d want [0,%d)", result, n)
		}
		u := secureIntnNow().UnixNano()
		if u >= 0 {
			tt.Fatalf("test setup: expected negative UnixNano, got %d", u)
		}
		expected := int((-u) % int64(n))
		if result != expected {
			tt.Fatalf("fallback mismatch: got %d want %d ((-UnixNano) %% n)", result, expected)
		}
	})

	t.Run("WhenRandReaderFailsThenSucceedsReturnsCryptoValue", func(tt *testing.T) {
		restoreSecureIntnDeps(tt)
		// Reader that fails a few times then provides bytes rand.Int needs.
		secureRandomSource = &flakyThenOKReader{failReads: 2}

		n := 100
		result, err := secureIntn(n)
		if err != nil {
			tt.Fatalf("expected success after reader recovers, got %v", err)
		}
		if result < 0 || result >= n {
			tt.Fatalf("result %d out of [0,%d)", result, n)
		}
	})
}

// flakyThenOKReader fails the first N Read calls, then fills p with zeros so rand.Int succeeds.
type flakyThenOKReader struct {
	failReads int
}

func (f *flakyThenOKReader) Read(p []byte) (int, error) {
	if f.failReads > 0 {
		f.failReads--
		return 0, io.EOF
	}
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
