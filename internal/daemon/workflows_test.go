package daemon

import "testing"

func TestDisablingAWorkflowStopsItsDrafts(t *testing.T) {
	t.Skip("E9 POST /api/workflows/{name}/disable stops its drafts")
}

func TestDisablingAnUnknownWorkflowIsNotFound(t *testing.T) {
	t.Skip("E9 disabling an unknown workflow is 404")
}

func TestAnAgentCannotDisableAWorkflow(t *testing.T) { t.Skip("E9 an agent cannot disable a workflow") }
