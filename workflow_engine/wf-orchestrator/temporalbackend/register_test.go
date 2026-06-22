package temporalbackend

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

func sampleRegisterWorkflow(_ *orchestrator.Context) error { return nil }

type aliasReceiver struct{}

func (aliasReceiver) Workflow(_ *orchestrator.Context) error { return nil }

func TestFuncName_ReturnsRuntimeName(t *testing.T) {
	name := FuncName(sampleRegisterWorkflow)
	assert.True(t, strings.HasSuffix(name, "sampleRegisterWorkflow"), "got %q", name)
}

func TestOrchWorkflowName_StringPassthrough(t *testing.T) {
	assert.Equal(t, "explicit.Name", OrchWorkflowName("explicit.Name"))
}

func TestOrchWorkflowName_PlainFunctionShortName(t *testing.T) {
	assert.Equal(t, "sampleRegisterWorkflow", OrchWorkflowName(sampleRegisterWorkflow))
}

func TestOrchWorkflowName_StripsMethodValueSuffix(t *testing.T) {
	// Method values get a runtime "-fm" suffix which must be trimmed.
	name := OrchWorkflowName(aliasReceiver{}.Workflow)
	assert.False(t, strings.HasSuffix(name, "-fm"))
	assert.Equal(t, "Workflow", name)
}

func TestOrchWorkflowName_NonFunctionReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", OrchWorkflowName(42))
}

func TestRememberAlias_ResolvesViaOrchWorkflowName(t *testing.T) {
	fn := func(_ *orchestrator.Context) error { return nil }
	RememberAlias(fn, "vlm.CustomWorkflowName")
	assert.Equal(t, "vlm.CustomWorkflowName", OrchWorkflowName(fn))
}

func TestRememberAlias_IgnoresNilAndNonFunc(t *testing.T) {
	assert.NotPanics(t, func() {
		RememberAlias(nil, "x")
		RememberAlias(42, "x")
	})
}

func TestWrapAndRegister_PanicsOnNonFunction(t *testing.T) {
	assert.PanicsWithValue(t, "WrapAndRegister: argument must be a function", func() {
		WrapAndRegister(nil, 42)
	})
}

func TestWrapAndRegister_PanicsOnWrongFirstParam(t *testing.T) {
	assert.PanicsWithValue(t, "WrapAndRegister: first parameter must be *orchestrator.Context", func() {
		WrapAndRegister(nil, func(_ context.Context) error { return nil })
	})
}

func TestWrapAndRegister_PanicsOnNoParams(t *testing.T) {
	assert.PanicsWithValue(t, "WrapAndRegister: first parameter must be *orchestrator.Context", func() {
		WrapAndRegister(nil, func() error { return nil })
	})
}

func TestRegisterWorkflowWithName_PanicsOnNonFunction(t *testing.T) {
	r := &TemporalRegistrar{}
	assert.PanicsWithValue(t, "RegisterWorkflowWithName: argument must be a function", func() {
		r.RegisterWorkflowWithName("n", 42)
	})
}

func TestRegisterWorkflowWithName_PanicsOnWrongFirstParam(t *testing.T) {
	r := &TemporalRegistrar{}
	assert.PanicsWithValue(t, "RegisterWorkflowWithName: first parameter must be *orchestrator.Context", func() {
		r.RegisterWorkflowWithName("n", func(_ context.Context) error { return nil })
	})
}

func TestNewRegistrar_SetsHeartbeatAndWorker(t *testing.T) {
	// nil worker is acceptable: NewRegistrar only stores it and installs the
	// heartbeat hook (SetupHeartbeat), which must not panic.
	assert.NotPanics(t, func() {
		reg := NewRegistrar(nil)
		assert.NotNil(t, reg)
	})
}

func TestSetupHeartbeat_InstallsWithoutPanic(t *testing.T) {
	// SetupHeartbeat swaps in activity.RecordHeartbeat as the orchestrator
	// heartbeat hook. Installing it must not panic.
	assert.NotPanics(t, SetupHeartbeat)
}
