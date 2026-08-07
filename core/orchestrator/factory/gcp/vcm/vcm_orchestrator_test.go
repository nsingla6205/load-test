package vcm

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// TestNotImplementedOrchestrator verifies unsupported VCM operations return NotImplementedYetErr.
func TestNotImplementedOrchestrator(t *testing.T) {
	orchestrator := reflect.ValueOf(notImplementedOrchestrator{})
	orchestratorType := orchestrator.Type()
	require.Positive(t, orchestratorType.NumMethod())

	for i := 0; i < orchestratorType.NumMethod(); i++ {
		method := orchestratorType.Method(i)
		t.Run(method.Name, func(t *testing.T) {
			boundMethod := orchestrator.MethodByName(method.Name)
			args := make([]reflect.Value, boundMethod.Type().NumIn())
			for argIndex := range args {
				args[argIndex] = reflect.Zero(boundMethod.Type().In(argIndex))
			}

			results := boundMethod.Call(args)
			require.NotEmpty(t, results)

			for _, result := range results[:len(results)-1] {
				assert.True(t, result.IsZero(), "expected zero-value result")
			}

			err, ok := results[len(results)-1].Interface().(error)
			require.True(t, ok, "last return value must be an error")
			require.Error(t, err)
			assert.True(t, customerrors.IsNotImplementedYetErr(err))
		})
	}
}
