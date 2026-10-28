package config

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
)

func loadErr(t *testing.T, raw string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path, tools())
	return err
}

func TestOldConfigKeysAreUnknown(t *testing.T) {
	t.Parallel()
	for raw, key := range map[string]string{
		`workflows = {x = {mode = "observe"}}`:                                     "workflows.x.mode",
		`workflows = {x = {approval = {default = "wait"}}}`:                        "workflows.x.approval.default",
		`workflows = {x = {draft_approval = {mode = "wait"}}}`:                     "workflows.x.draft_approval",
		`project_settings = {review = "true"}`:                                     "project_settings",
		`proposals = {renudge_after = "1d"}`:                                       "proposals",
		`proposals = {github-nudge = {approval = {mode = "wait"}}}`:                "proposals",
		`proposals = {ask-owner = {approval = {mode = "wait"}}}`:                   "proposals",
		`rounds = {per_pr_per_day = 3}`:                                            "rounds",
		`turns = {automated_per_day = 3}`:                                          "turns.automated_per_day",
		`classify = {daily_cap = 3}`:                                               "classify.daily_cap",
		`brief = {max_diff_bytes = 3000}`:                                          "brief.max_diff_bytes",
		`plugins.slack = {extraction = {max_call_usd = 1}}`:                        "plugins.slack.extraction.max_call_usd",
		`plugins.slack = {extraction = {max_day_usd = 1}}`:                         "plugins.slack.extraction.max_day_usd",
		`plugins.slack = {extraction = {max_day_calls = 1}}`:                       "plugins.slack.extraction.max_day_calls",
		`automated_reviewers = ["bot"]`:                                            "automated_reviewers",
		`owners = ["acme"]`:                                                        "owners",
		`trusted_authors = ["bot"]`:                                                "trusted_authors",
		`review_prefix = "R "`:                                                     "review_prefix",
		`followups_without_workspace = true`:                                       "followups_without_workspace",
		`address_without_workspace = true`:                                         "address_without_workspace",
		`address_reviews = true`:                                                   "address_reviews",
		`address_skip_reviewers = ["bot"]`:                                         "address_skip_reviewers",
		`address_checks = true`:                                                    "address_checks",
		`ignore_checks = ["lint"]`:                                                 "ignore_checks",
		`address_conflicts = true`:                                                 "address_conflicts",
		`stalled = {review = "1d"}`:                                                "stalled",
		`scope = {repos = ["acme/api"]}`:                                           "scope",
		`projects = [{match = "acme/*", trusted_authors = ["bot"]}]`:               "projects[0].trusted_authors",
		`projects = [{match = "acme/*", followups_without_workspace = true}]`:      "projects[0].followups_without_workspace",
		`projects = [{match = "acme/*", address_without_workspace = true}]`:        "projects[0].address_without_workspace",
		`plugins.github = {gates = {approval = {mode = "wait", rule = []}}}`:       "plugins.github.gates.approval.rule",
		`workflows = {x = {drafts = {approval = {mode = "wait", default = "x"}}}}`: "workflows.x.drafts.approval.default",
	} {
		var unknown *UnknownKeysError
		if err := loadErr(t, raw); !errors.As(err, &unknown) || !slices.Equal(unknown.Keys, []string{key}) {
			t.Errorf("%s: %v, want the unknown key %s", raw, err, key)
		}
	}
}

func TestTheOldPluginSectionsNameTheirNewKeys(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]map[string]string{
		`github = {owners = ["acme"]}`:                                  {"github": "plugins.github"},
		`slack = {client_id = "1.2"}`:                                   {"slack": "plugins.slack"},
		`github = {drafts = {approval = {mode = "wait"}}}`:              {"github.drafts.approval": "plugins.github.gates.approval"},
		`github = {owners = [], drafts = {approval = {mode = "wait"}}}`: {"github.drafts.approval": "plugins.github.gates.approval"},
		`slack = {drafts = {approval = {mode = "send"}}}`:               {"slack.drafts.approval": "plugins.slack.gates.approval"},
	} {
		var unknown *UnknownKeysError
		err := loadErr(t, raw)
		if !errors.As(err, &unknown) || !maps.Equal(unknown.Near, want) {
			t.Errorf("%s: %v, want %v", raw, err, want)
		}
	}
}

func TestRecordIsNoDraftApprovalMode(t *testing.T) {
	t.Parallel()
	for raw, path := range map[string]string{
		`workflows = {x = {drafts = {approval = {mode = "record"}}}}`: "workflows.x.drafts.approval",
		`plugins.github = {gates = {approval = {mode = "record"}}}`:   "plugins.github.gates.approval",
		`plugins.slack = {gates = {approval = {mode = "record"}}}`:    "plugins.slack.gates.approval",
	} {
		err := loadErr(t, raw)
		if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), `"record"`) {
			t.Errorf("%s: %v", raw, err)
		}
		if err != nil && strings.Contains(err.Error(), "did you mean") {
			t.Errorf("%s: a guess turns a draft that never sends into one that does: %v", raw, err)
		}
	}
	c := loadRaw(t, `workflows = {x = {drafts = {approval = {mode = "dry_run"}}}}`)
	if a := c.Workflows["x"].Drafts.Approval; a.Mode != string(draft.DryRun) {
		t.Errorf("drafts.approval %+v", a)
	}
}

// userConfig has the shape of a user's file before the GitHub settings
// moved under github; its values are invented.
const userConfig = `projects_dir = "~/code"
profile = "~/profiles/mine"
automated_reviewers = ["review-bot[bot]"]
followups_without_workspace = true
address_without_workspace = true

[brief]
enabled = true

[[projects]]
match = "acme/*"
agent_home = "~/.agent-acme"

[[projects]]
match = "globex/*"
followups_without_workspace = false
trusted_authors = ["deps-bot[bot]"]

[plugins.slack]
client_id = "123.456"

[plugins.slack.ingest]
terms = ["deploy"]
`

func TestUserConfigEditsLoad(t *testing.T) {
	t.Parallel()
	var unknown *UnknownKeysError
	err := loadErr(t, userConfig)
	want := []string{"address_without_workspace", "automated_reviewers", "followups_without_workspace",
		"projects[1].followups_without_workspace", "projects[1].trusted_authors"}
	if !errors.As(err, &unknown) || !slices.Equal(unknown.Keys, want) {
		t.Fatalf("before the edits: %v, want the unknown keys %v", err, want)
	}
	edited := strings.NewReplacer(
		`automated_reviewers = ["review-bot[bot]"]
followups_without_workspace = true
address_without_workspace = true`, `plugins.github = {automated_reviewers = ["review-bot[bot]"], followups_without_workspace = true, address_without_workspace = true}`,
		`followups_without_workspace = false
trusted_authors = ["deps-bot[bot]"]`, `github = {followups_without_workspace = false, trusted_authors = ["deps-bot[bot]"]}`,
	).Replace(userConfig)
	c := loadRaw(t, edited)
	g := c.For("globex", "api").GitHub
	if !slices.Equal(c.GitHub.AutomatedReviewers, []string{"review-bot[bot]"}) || !c.GitHub.AddressWithoutWorkspace ||
		g.FollowupsWithoutWorkspace || !slices.Equal(g.TrustedAuthors, []string{"deps-bot[bot]"}) {
		t.Errorf("after the edits: %+v, globex %+v", c.GitHub, g)
	}
}

func TestGitHubDraftApprovalIsTheRuleApproval(t *testing.T) {
	t.Parallel()
	c := loadRaw(t, `plugins.github = {gates = {approval = {mode = "delay", window = "1h"}}}`)
	if got := c.PluginDraftApproval("github"); got != (draft.Approval{Mode: draft.Delay, Window: time.Hour}) {
		t.Errorf("github %+v", got)
	}
	for _, id := range []string{"slack", "wiki"} {
		if got := c.PluginDraftApproval(id); got != (draft.Approval{Mode: draft.Wait}) {
			t.Errorf("%s with none set: %+v", id, got)
		}
	}
	for raw, path := range map[string]string{
		`plugins.github = {gates = {approval = {mode = "delay"}}}`:              "plugins.github.gates.approval",
		`plugins.slack = {gates = {approval = {mode = "wait", window = "1h"}}}`: "plugins.slack.gates.approval",
	} {
		if err := loadErr(t, raw); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestWorkflowSettingsLoad(t *testing.T) {
	t.Parallel()
	c := loadRaw(t, `[workflows.x]
observe = true

[workflows.x.approval]
mode = "delay"
window = "10m"

[[workflows.x.approval.rule]]
when = "pr.mine"
mode = "wait"

[workflows.y]
observe = false

[workflows.y.approval]
rule = []
`)
	x, y := c.Workflows["x"], c.Workflows["y"]
	if x.Observe == nil || !*x.Observe || x.Approval.Mode != "delay" || time.Duration(*x.Approval.Window) != 10*time.Minute ||
		len(x.Approval.Rule) != 1 || x.Approval.Rule[0] != (ApprovalRuleConfig{When: "pr.mine", Mode: "wait"}) {
		t.Errorf("x %+v %+v", x, x.Approval)
	}
	if y.Observe == nil || *y.Observe || y.Approval.Rule == nil || len(y.Approval.Rule) != 0 || y.Approval.Mode != "" {
		t.Errorf("y keeps the manifest's mode and clears its rules: %+v", y.Approval)
	}
	for raw, msg := range map[string]string{
		`workflows = {x = {approval = {mode = "apply"}}}`:                                "workflows.x.approval.mode",
		`workflows = {x = {approval = {mode = "send"}}}`:                                 "workflows.x.approval.mode",
		`workflows = {x = {approval = {rule = [{when = "pr.mine", mode = "dry_run"}]}}}`: "workflows.x.approval.rule[0].mode",
		`workflows = {x = {approval = {rule = [{when = "pr.nope", mode = "wait"}]}}}`:    "workflows.x.approval.rule[0].when",
	} {
		if err := loadErr(t, raw); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: %v", raw, err)
		}
	}
}
