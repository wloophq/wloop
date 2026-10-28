package main

import (
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

// group is a subsection of the language page. Its keys are path prefixes,
// matched at a segment boundary; a row goes to the group of its longest
// matching key, listed in the order of the keys.
type group struct {
	title string
	keys  []string
}

// groups are the language page's subsections, by surface, in reading order;
// a surface without groups is one list.
var groups = map[builtin.Surface][]group{
	builtin.SurfaceIntent: {
		{"Identity and help", []string{"label", "summary", "shortcut", "verb", "user_facing", "skill", "help"}},
		{"Binding", []string{"role", "provider", "builds", "prompt", "schema"}},
		{"Where it runs", []string{"workspace", "runners", "session", "target"}},
		{"How a turn runs", []string{"intake", "owner_only", "join", "answerable", "reports"}},
		{"Inputs", []string{"inputs"}},
		{"When a turn is due or refused", []string{"why", "refuse"}},
		{"Its action on a row", []string{"action"}},
		{"What the prompt opens with", []string{"headline", "start_line", "resume"}},
		{"Guidance and brief", []string{"guidance", "brief"}},
		{"Judgment", []string{"profile", "dims", "also_profile"}},
		{"What it records", []string{"record"}},
		{"Gate approval", []string{"gates"}},
		{"Effects and drafts", []string{"effects", "drafts"}},
	},
	builtin.SurfaceWorkflow: {
		{"What it matches", []string{"summary", "provider", "when", "order", "key", "marks"}},
		{"When it is planned", []string{"trigger", "observe", "limits"}},
		{"Steps", []string{"step"}},
		{"Run approval", []string{"approval"}},
		{"Gate approval", []string{"gates"}},
		{"Drafts", []string{"drafts"}},
	},
	builtin.SurfaceConfig: {
		{"Agent and workspaces", []string{"profile", "agent", "agent_home", "agent_models", "projects_dir", "workspace_managers", "intents"}},
		{"Projects", []string{"projects"}},
		{"Placement", []string{"placement"}},
		{"GitHub", []string{"github"}},
		{"GitHub address rounds", []string{"github.address_reviews", "github.address_skip_reviewers", "github.address_checks", "github.ignore_checks", "github.address_conflicts"}},
		{"GitHub scope, stalls and nudges", []string{"github.scope", "github.stalled", "github.nudge"}},
		{"Slack", []string{"slack"}},
		{"Slack ingest", []string{"slack.ingest"}},
		{"Slack extraction", []string{"slack.extraction"}},
		{"Workflow overrides", []string{"workflows"}},
		{"Workflow draft approval", []string{"workflows.*.drafts"}},
		{"Workflow gate approval", []string{"workflows.*.gates"}},
		{"Turns and headless calls", []string{"turns", "headless", "brief", "classify"}},
		{"Drafts and notifications", []string{"drafts", "notifications"}},
		{"Units and tasks", []string{"items", "tasks"}},
		{"Cleanup", []string{"cleanup"}},
		{"Dossiers", []string{"dossiers"}},
		{"Daemon", []string{"sweep", "http", "mcp", "views"}},
	},
}

func under(path, key string) bool {
	return path == key || strings.HasPrefix(path, key+".") || strings.HasPrefix(path, key+"[]")
}

// grouped sorts rows into gs; a row no group takes, or a group key no row
// matches, is an error, so a new key cannot land outside every group.
func grouped(rows []builtin.Key, gs []group) ([][]builtin.Key, error) {
	type at struct{ group, key int }
	place := make([]at, len(rows))
	used := map[string]bool{}
	for i, r := range rows {
		best, found := "", false
		for g, grp := range gs {
			for k, key := range grp.keys {
				if under(r.Path, key) && len(key) > len(best) {
					best, found, place[i] = key, true, at{g, k}
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("%s key %q is in no group of the language page; add it to groups in grammardoc", r.In, r.Path)
		}
		used[best] = true
	}
	out := make([][]builtin.Key, len(gs))
	for g, grp := range gs {
		for k, key := range grp.keys {
			if !used[key] {
				return nil, fmt.Errorf("group %q names %q, which no key matches", grp.title, key)
			}
			for i, r := range rows {
				if place[i] == (at{g, k}) {
					out[g] = append(out[g], r)
				}
			}
		}
	}
	return out, nil
}
