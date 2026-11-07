package daemon

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// configSets records the config edits a server asks for.
type configSets struct {
	paths [][]string
	vals  []any
	err   error
}

func (c *configSets) set(path []string, v any) error {
	c.paths, c.vals = append(c.paths, path), append(c.vals, v)
	return c.err
}

func disablingServer(t *testing.T) (*Server, *configSets, string) {
	t.Helper()
	sets := &configSets{}
	s := newServerWith(t, func(d *Deps) { d.ConfigSet = sets.set })
	wfs := s.loaded().Workflows
	if len(wfs) == 0 {
		t.Fatal("no workflow loaded")
	}
	return s, sets, wfs[0].Name
}

func TestDisablingAWorkflowStopsItsDrafts(t *testing.T) {
	t.Parallel()
	s, sets, name := disablingServer(t)
	rec := post(t, s, api.WorkflowDisablePath(name), "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got api.WorkflowSwitch
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got != (api.WorkflowSwitch{Workflow: name, Enabled: false}) {
		t.Fatalf("%s %v", rec.Body, err)
	}
	if len(sets.paths) != 1 || !slices.Equal(sets.paths[0], []string{"workflows", name, "enabled"}) || sets.vals[0] != false {
		t.Fatalf("config sets %v %v", sets.paths, sets.vals)
	}
}

func TestAFailedConfigEditIsAnError(t *testing.T) {
	t.Parallel()
	s, sets, name := disablingServer(t)
	sets.err = errors.New("config.toml does not load")
	if rec := post(t, s, api.WorkflowDisablePath(name), ""); rec.Code != 500 || !strings.Contains(rec.Body.String(), "does not load") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestADaemonThatEditsNoConfigSaysSoInTheProfilesWords(t *testing.T) {
	t.Parallel()
	s := newServerWith(t, func(*Deps) {})
	rec := post(t, s, api.WorkflowDisablePath(s.loaded().Workflows[0].Name), "")
	want := s.loaded().Notices[builtin.NoticeConfigUnavailable]
	if rec.Code != 503 || want == "" || !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("%d %s, want the notice %q", rec.Code, rec.Body, want)
	}
}

func TestDisablingAnUnknownWorkflowIsNotFound(t *testing.T) {
	t.Parallel()
	s, sets, _ := disablingServer(t)
	if rec := post(t, s, api.WorkflowDisablePath("no-such-workflow"), ""); rec.Code != 404 || len(sets.paths) != 0 {
		t.Fatalf("%d %s, sets %v", rec.Code, rec.Body, sets.paths)
	}
}

func TestAnAgentCannotDisableAWorkflow(t *testing.T) {
	t.Parallel()
	s, sets, name := disablingServer(t)
	req := apiRequest("POST", api.WorkflowDisablePath(name), nil)
	req.Header.Set(api.SessionHeader, "term_agent1")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 403 || len(sets.paths) != 0 {
		t.Fatalf("%d %s, sets %v", rec.Code, rec.Body, sets.paths)
	}
}
