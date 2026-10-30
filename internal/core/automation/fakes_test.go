package automation_test

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

func TestFakeUnits(t *testing.T) {
	automationtest.RunUnits(t, unit.Session, func(*testing.T) automation.Units {
		us := automationtest.NewUnits()
		for _, k := range []string{"t2", "t1"} {
			us.Put(unit.Unit{Provider: unit.Session, Key: k}, workflow.Facts{"session.fingerprint": k, "session.repo": "o/r",
				"session.idle_for": time.Hour, "session.managed": true})
		}
		us.Put(unit.Unit{Provider: unit.Workspace, Key: "w"}, workflow.Facts{"workspace.fingerprint": "w", "workspace.repo": ""})
		return us
	})
}

func TestFakeRevisioner(t *testing.T) {
	automationtest.RunRevisioner(t, unit.Session, func(*testing.T) automationtest.RevisionerBackend {
		us := automationtest.NewUnits()
		for _, k := range []string{"t2", "t1"} {
			us.Put(unit.Unit{Provider: unit.Session, Key: k}, workflow.Facts{"session.fingerprint": k, "session.repo": "o/r"})
		}
		return us
	})
}

func TestFakeEvents(t *testing.T) {
	automationtest.RunEvents(t, func(*testing.T) automationtest.EventsBackend { return automationtest.NewEvents() })
}

func TestFakeSteps(t *testing.T) {
	automationtest.RunSteps(t, func(*testing.T) automation.Steps {
		return automationtest.NewSteps(map[string]workflow.Reach{"session.close": workflow.ReachLocal})
	})
}

func TestFakeLedger(t *testing.T) {
	automationtest.RunLedger(t, func(*testing.T) automationtest.LedgerBackend { return automationtest.NewLedger() })
}
