package intent

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dunit "github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/profiles"
)

func bundledDefault(t *testing.T) fs.FS {
	t.Helper()
	sub, err := fs.Sub(profiles.Default, "default")
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

// userWorkflowProblems loads the bundled workflows with one user workflow
// beside them, and is every problem the load reports.
func userWorkflowProblems(t *testing.T, name, manifest string) string {
	t.Helper()
	root := t.TempDir()
	writeWorkflow(t, root, name, manifest)
	_, broken, _, problems, _ := loadWorkflows(userLayer(root), bundledDefault(t), nil, nil)
	out := problems.Error()
	for _, ps := range broken {
		out += "\n" + ps.Error()
	}
	return out
}

func taskWorkflow(when, step string) string {
	return "summary = \"Close the task\"\nprovider = \"" + string(dunit.Task) + "\"\nwhen = '" + when + "'\n\n[[step]]\n" + step + "\n"
}

func TestAnEntryFactInAUserWorkflowNamesItsTaskName(t *testing.T) {
	got := userWorkflowProblems(t, "mine", taskWorkflow(`entry.state == "inbox"`, "use = \"task.verb\"\nverb = \"done\""))
	if !strings.Contains(got, `unknown field entry.state; did you mean "task.state"?`) {
		t.Fatal(got)
	}
}

func TestAnEntryVerbStepNamesTaskVerb(t *testing.T) {
	got := userWorkflowProblems(t, "mine", taskWorkflow(`task.state == "inbox"`, "use = \"entry.verb\"\nverb = \"done\""))
	if !strings.Contains(got, `"entry.verb" is not one of`) || !strings.Contains(got, `did you mean "task.verb"?`) {
		t.Fatal(got)
	}
}

func TestAnEntryNudgedEffectNamesTaskNudged(t *testing.T) {
	got := userWorkflowProblems(t, "mine", taskWorkflow(`task.state == "waiting"`,
		"use = \"draft.propose\"\npurpose = \"nudge\"\neffects = [\"entry.nudged\"]"))
	if !strings.Contains(got, `"entry.nudged" is not one of`) || !strings.Contains(got, `did you mean "task.nudged"?`) {
		t.Fatal(got)
	}
}

func TestAnEntryProviderNamesTask(t *testing.T) {
	got := userWorkflowProblems(t, "mine", strings.Replace(taskWorkflow(`task.state == "inbox"`, "use = \"task.verb\"\nverb = \"done\""),
		"provider = \""+string(dunit.Task)+"\"", `provider = "entry"`, 1))
	if !strings.Contains(got, `"entry" is not one of`) || !strings.Contains(got, `did you mean "task"?`) {
		t.Fatal(got)
	}
}

func TestAnEntryResultsEndedOverrideNamesTaskResultsEnded(t *testing.T) {
	b, err := fs.ReadFile(bundledDefault(t), "workflows/task-results-ended/workflow.toml")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeWorkflow(t, root, "entry-results-ended", string(b))
	loaded, _, _, problems, _ := loadWorkflows(userLayer(root), bundledDefault(t), nil, nil)
	if !strings.Contains(problems.Error(), `entry-results-ended was renamed; did you mean "task-results-ended"?`) {
		t.Fatal(problems.Error())
	}
	for _, w := range loaded {
		if w.Name == "entry-results-ended" {
			t.Fatal("the old name loaded as a workflow of its own")
		}
	}
}

func TestSlackLiveEntryNamesSlackLiveTask(t *testing.T) {
	got := userWorkflowProblems(t, "mine", "summary = \"Ask\"\nprovider = \"slack\"\nwhen = 'slack.live_entry'\n\n[[step]]\nuse = \"draft.propose\"\npurpose = \"ask_owner\"\n")
	if !strings.Contains(got, `unknown field slack.live_entry; did you mean "slack.live_task"?`) {
		t.Fatal(got)
	}
}

func TestConfigGtdFactNamesConfigTasks(t *testing.T) {
	got := userWorkflowProblems(t, "mine", taskWorkflow(`config.gtd.stale_waiting_days > 2`, "use = \"task.verb\"\nverb = \"done\""))
	if !strings.Contains(got, `unknown field config.gtd.stale_waiting_days; did you mean "config.tasks.stale_waiting_days"?`) {
		t.Fatal(got)
	}
}

func TestAUserGtdTomlNamesItsNewFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "gtd.toml"), []byte("[views.next]\nlabel = \"Now\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	texts, _, problems := loadLists(userLayer(root), bundledDefault(t), nil)
	got := problems.Error()
	if !strings.Contains(got, filepath.Join(root, "gtd.toml")) || !strings.Contains(got, "tasks.toml") || !strings.Contains(got, "views.toml") {
		t.Fatal(got)
	}
	if texts.Views["next"] == "Now" {
		t.Error("gtd.toml was read")
	}
}

func TestAnEntryNameFactInASessionNameNamesItsTaskName(t *testing.T) {
	dir := t.TempDir()
	writeIntent(t, dir, "begin", strings.Replace(startLike, "{{or .workspace.name .task.title}}", "{{or .workspace.name .entry.title}}", 1), "Begin.")
	_, problems := Load(userLayer(dir), bundledFS(), testBase(), nil)
	if !strings.Contains(problems.Error(), `session.name: `) || !strings.Contains(problems.Error(), `did you mean "task.title"?`) {
		t.Fatal(problems.Error())
	}
}
