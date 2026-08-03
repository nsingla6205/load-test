package vsa

import (
	"testing"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func TestRetryOnErrors(t *testing.T) {
	oldRetrySleepInterval := retrySleepInterval
	defer func() {
		retrySleepInterval = oldRetrySleepInterval
	}()
	retrySleepInterval = 1 * time.Millisecond
	t.Run("WhenRetryingMaxNumberOfTimes", func(tt *testing.T) {
		errs := make([]error, maxRetryCount)
		for i := range errs {
			errs[i] = errors.New("very busy")
		}
		testRetryOnErrors(tt, errs, errors.New("very busy"))
	})
	t.Run("WhenRetryingBeforeSuccessful", func(tt *testing.T) {
		errs := []error{errors.New("Ruleset is in use by a volume")}
		testRetryOnErrors(tt, errs, nil)
	})
	t.Run("WhenRetryingBeforeReturningError", func(tt *testing.T) {
		errs := []error{errors.New("Ruleset is in use by a volume")}
		testRetryOnErrors(tt, errs, errors.New("now for something completely different"))
	})
	t.Run("WhenRetryingTwiceBeforeSuccessful", func(tt *testing.T) {
		errs := []error{errors.New("Ruleset is in use by a volume"), errors.New("Ruleset is in use by a volume")}
		testRetryOnErrors(tt, errs, nil)
	})
	t.Run("WhenRetryingTwiceBeforeError", func(tt *testing.T) {
		errs := []error{errors.New("Ruleset is in use by a volume"), errors.New("Ruleset is in use by a volume")}
		testRetryOnErrors(tt, errs, errors.New("now for something completely different"))
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		err := _retryOnErrors(func() error { return nil }, []string{"busy", "is in use"})
		if err != nil {
			tt.Fail()
		}
	})
}

func testRetryOnErrors(t *testing.T, retryErrs []error, expected error) {
	errs := append(retryErrs, expected)
	op := func() (err error) {
		if len(errs) > 0 {
			err = errs[0]
			errs = errs[1:]
		}
		return
	}

	before := time.Now()
	err := _retryOnErrors(op, []string{"busy", "is in use"})
	after := time.Now()
	if err != expected {
		t.Fail()
	}
	if after.UnixNano()-before.UnixNano() < int64(len(retryErrs))*int64(retrySleepInterval) {
		t.Error("Wait period before retrying was too short")
	}
}

func TestShouldRetry(t *testing.T) {
	err := errors.New("error: entity is in use")
	t.Run("WhenShouldNotRetry", func(tt *testing.T) {
		if shouldRetry(err, []string{"not found", "doesn't exist"}) {
			tt.Fail()
		}
	})
	t.Run("WhenShouldRetry", func(tt *testing.T) {
		if !shouldRetry(err, []string{"busy", "is in use"}) {
			tt.Fail()
		}
	})
}
