package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	dturn "github.com/wloophq/wloop/internal/domain/turn"
)

func TestCleanupDefaults(t *testing.T) {
	c, err := LoadFile(filepath.Join(t.TempDir(), "absent.toml"), tools())
	if err != nil {
		t.Fatal(err)
	}
	cl := c.Cleanup
	if time.Duration(cl.Idle) != 2*time.Hour || time.Duration(cl.AskedIdle) != 4*time.Hour ||
		time.Duration(cl.WorkspaceGrace) != 24*time.Hour || time.Duration(cl.ManualIdle) != 24*time.Hour ||
		time.Duration(cl.StaleWorkspace) != 14*24*time.Hour {
		t.Errorf("cleanup = %+v", cl)
	}
	for _, name := range []string{"node_modules", ".DS_Store", dturn.ScratchDir} {
		if !slices.Contains(cl.Disposable, name) {
			t.Errorf("disposable lacks %s: %v", name, cl.Disposable)
		}
	}
	if c.Classify.PerDay != 50 || c.GitHub.Nudge.SilentDays != 30 {
		t.Errorf("classify = %+v, nudge = %+v", c.Classify, c.GitHub.Nudge)
	}

	refused := map[string]string{
		`workflows = {x = {approval = {mode = "never"}}}`: `workflows.x.approval.mode: "never" is not one of "run", "delay" or "wait"`,
		`workflows = {x = {approval = {window = "0s"}}}`:  `workflows.x.approval.window: a duration must be longer than zero`,
	}
	for body, want := range refused {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadFile(path, tools())
		if err == nil || err.Error() != path+": "+want {
			t.Errorf("LoadFile(%s) err = %v, want %s", body, err, want)
		}
	}
}

func TestLoadRefusesAnOnlyWhenThatDoesNotCompile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`workflows = {address = {only_when = "pr.nope == 1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path, tools()); err == nil || !strings.HasPrefix(err.Error(), path+": workflows.address.only_when: ") {
		t.Errorf("err = %v", err)
	}
	if err := os.WriteFile(path, []byte(`workflows = {address = {only_when = "pr.visibility == \"private\""}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := LoadFile(path, tools()); err != nil || c.Workflows["address"].OnlyWhen == "" {
		t.Errorf("config %+v, err %v", c.Workflows, err)
	}
}

type prFacts map[string]any

func (f prFacts) Get(field string) any { return f[field] }

func TestProjectSettingsFollowTheirPredicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`intents = {review = {project_settings = {when = "pr.nope"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path, tools()); err == nil || !strings.HasPrefix(err.Error(), path+": intents.review.project_settings.when: ") {
		t.Errorf("err = %v", err)
	}
	c := Config{Intents: map[string]IntentConfig{"review": {ProjectSettings: &ProjectSettings{When: `pr.visibility == "private" && pr.owner != "acme"`}}, "nudge": {ProjectSettings: &ProjectSettings{When: "false"}}}}
	for _, k := range []struct {
		intent string
		facts  prFacts
		want   bool
	}{
		{"review", prFacts{"pr.visibility": "private", "pr.owner": "me"}, true},
		{"review", prFacts{"pr.visibility": "private", "pr.owner": "acme"}, false},
		{"review", prFacts{"pr.visibility": "public", "pr.owner": "me"}, false},
		{"nudge", prFacts{}, false},
		{"address", prFacts{}, true},
	} {
		if got := c.ProjectSettingsFor(k.intent, k.facts); got != k.want {
			t.Errorf("%s %v: %v, want %v", k.intent, k.facts, got, k.want)
		}
	}
	if c.ProjectSettingsFor("review", nil) || !c.ProjectSettingsFor("address", nil) {
		t.Error("without facts, a predicate must keep the settings off and no predicate on")
	}
}

func TestLoadWorkflowConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `[cleanup]
idle = "3h"
disposable = ["tmp"]

[github.nudge]
silent_days = 10

[classify]
per_day = 5

[workflows.x]
observe = true
enabled = false

[workflows.x.approval]
mode = "delay"
window = "10m"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(path, tools())
	if err != nil {
		t.Fatal(err)
	}
	x := c.Workflows["x"]
	if time.Duration(c.Cleanup.Idle) != 3*time.Hour || time.Duration(c.Cleanup.AskedIdle) != 4*time.Hour ||
		!slices.Equal(c.Cleanup.Disposable, []string{"tmp"}) || c.GitHub.Nudge.SilentDays != 10 || c.Classify.PerDay != 5 {
		t.Errorf("config = %+v %+v %+v", c.Cleanup, c.GitHub.Nudge, c.Classify)
	}
	if x.Observe == nil || !*x.Observe || x.Enabled == nil || *x.Enabled || x.Approval == nil || x.Approval.Mode != "delay" ||
		x.Approval.Window == nil || time.Duration(*x.Approval.Window) != 10*time.Minute {
		t.Errorf("workflows.x = %+v", x)
	}
}
