package slack

import (
	"testing"

	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

func TestTheManifestDeclaresTheJobsAndTools(t *testing.T) {
	t.Parallel()
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}
	jobs := map[string]bool{}
	for _, j := range s.Jobs {
		jobs[j.Name] = true
	}
	if !jobs[JobIngest] || !jobs[JobExtract] {
		t.Errorf("jobs %+v, want %s and %s", s.Jobs, JobIngest, JobExtract)
	}
	tools := map[string]bool{}
	for _, tl := range s.Tools {
		tools[tl.Name] = true
	}
	for _, name := range []string{ToolSend, ToolRead, ToolSearch, ToolDraft, ToolReact, ToolEdit} {
		if !tools[name] {
			t.Errorf("the manifest has no tool %s", name)
		}
	}
	if len(s.ConfigReads) != 2 || len(s.Notify) != 2 {
		t.Errorf("config_reads %v notify %v", s.ConfigReads, s.Notify)
	}
}
