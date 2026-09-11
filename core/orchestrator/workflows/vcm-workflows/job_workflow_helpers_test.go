package vcm_workflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReturnFinishJobError_PrefersWorkflowError(t *testing.T) {
	workflowErr := errors.New("ontap failure")
	updateErr := errors.New("database unavailable")

	assert.Same(t, workflowErr, returnFinishJobError(workflowErr, updateErr))
}

func TestReturnFinishJobError_ReturnsUpdateErrorWhenWorkflowSucceeded(t *testing.T) {
	updateErr := errors.New("database unavailable")

	assert.Same(t, updateErr, returnFinishJobError(nil, updateErr))
}
