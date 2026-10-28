package cli

import (
	"bytes"
	"strings"
	"testing"

	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestWorkflowsFactsOfASubjectListItsSlotWithTheTriggerAndConfig(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("workflows", "facts", "session"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := te.out.String()
	for _, f := range builtin.Facts {
		listed := strings.Contains(out, f.Name+" ")
		switch f.Slot {
		case "session", "trigger", "config":
			if !listed {
				t.Errorf("lacks %s:\n%s", f.Name, out)
			}
		case "pr", "workspace":
			if listed {
				t.Errorf("lists %s, a fact of another subject", f.Name)
			}
		}
	}
}

func TestWorkflowsFactsWithoutASubjectListEverySlot(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("workflows", "facts"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	for _, want := range []string{"pr.state ", "session.idle_for ", "workspace.unpushed ", "subject github"} {
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("lacks %q", want)
		}
	}
}

func TestWorkflowsFactsRefuseAnUnknownSubject(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("workflows", "facts", "gmail"); code == 0 {
		t.Fatal("exit 0")
	}
	if !strings.Contains(te.err.String(), "github, session, slack, task, workspace") {
		t.Fatal(te.err.String())
	}
}

func TestWorkflowsStepsListTheVocabularyWithArguments(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("workflows", "steps"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := te.out.String()
	for _, d := range builtin.Steps {
		if !strings.Contains(out, d.Name+" ") {
			t.Errorf("lacks %s", d.Name)
		}
		for _, a := range builtin.StepArgs(d.Name) {
			if !strings.Contains(out, a) {
				t.Errorf("lacks %s's argument %s", d.Name, a)
			}
		}
	}
}

func TestProfileSchemaPrintsTheWorkflowSchemaOnRequest(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("profile", "schema", "--workflow"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !bytes.Equal(te.out.Bytes(), intentload.WorkflowSchema) {
		t.Fatal(te.out.String())
	}
}

func TestProfileSchemaPrintsTheActionsListsAndConfigSchemas(t *testing.T) {
	t.Parallel()
	for flag, want := range map[string][]byte{"--actions": intentload.ActionsSchema, "--tasks": intentload.TasksSchema, "--views": intentload.ViewsSchema,
		"--gates": intentload.GatesSchema, "--keys": intentload.KeysSchema, "--config": intentload.ConfigSchema} {
		te := newTestEnv(t, &fake.Manager{})
		if code := te.run("profile", "schema", flag); code != 0 || !bytes.Equal(te.out.Bytes(), want) {
			t.Errorf("%s: exit %d: %s", flag, code, te.err)
		}
	}
}

func TestProfileSchemaGtdFlagIsRefused(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("profile", "schema", "--gtd"); code == 0 || te.out.Len() > 0 {
		t.Fatalf("exit %d: %s", code, te.out)
	}
	for _, flag := range []string{"--gtd is no longer a schema", "--tasks", "--views", "--gates"} {
		if !strings.Contains(te.err.String(), flag) {
			t.Errorf("the refusal names no %s: %s", flag, te.err)
		}
	}
}
