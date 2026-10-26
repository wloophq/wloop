package config

import (
	"errors"
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
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path, tools())
	return err
}

func TestOldConfigKeysAreUnknown(t *testing.T) {
	t.Parallel()
	for raw, key := range map[string]string{
		`{"workflows": {"x": {"mode": "observe"}}}`:                                        "workflows.x.mode",
		`{"workflows": {"x": {"approval": {"default": "wait"}}}}`:                          "workflows.x.approval.default",
		`{"workflows": {"x": {"draft_approval": {"mode": "wait"}}}}`:                       "workflows.x.draft_approval",
		`{"project_settings": {"review": "true"}}`:                                         "project_settings",
		`{"proposals": {"renudge_after": "1d"}}`:                                           "proposals",
		`{"proposals": {"github-nudge": {"approval": {"mode": "wait"}}}}`:                  "proposals",
		`{"proposals": {"ask-owner": {"approval": {"mode": "wait"}}}}`:                     "proposals",
		`{"rounds": {"per_pr_per_day": 3}}`:                                                "rounds",
		`{"turns": {"automated_per_day": 3}}`:                                              "turns.automated_per_day",
		`{"classify": {"daily_cap": 3}}`:                                                   "classify.daily_cap",
		`{"brief": {"max_diff_bytes": 3000}}`:                                              "brief.max_diff_bytes",
		`{"slack": {"extraction": {"max_call_usd": 1}}}`:                                   "slack.extraction.max_call_usd",
		`{"slack": {"extraction": {"max_day_usd": 1}}}`:                                    "slack.extraction.max_day_usd",
		`{"slack": {"extraction": {"max_day_calls": 1}}}`:                                  "slack.extraction.max_day_calls",
		`{"plugins": {"github": {"enabled": false}}}`:                                      "plugins",
		`{"automated_reviewers": ["bot"]}`:                                                 "automated_reviewers",
		`{"owners": ["acme"]}`:                                                             "owners",
		`{"trusted_authors": ["bot"]}`:                                                     "trusted_authors",
		`{"review_prefix": "R "}`:                                                          "review_prefix",
		`{"followups_without_workspace": true}`:                                            "followups_without_workspace",
		`{"address_without_workspace": true}`:                                              "address_without_workspace",
		`{"address_reviews": true}`:                                                        "address_reviews",
		`{"address_skip_reviewers": ["bot"]}`:                                              "address_skip_reviewers",
		`{"address_checks": true}`:                                                         "address_checks",
		`{"ignore_checks": ["lint"]}`:                                                      "ignore_checks",
		`{"address_conflicts": true}`:                                                      "address_conflicts",
		`{"stalled": {"review": "1d"}}`:                                                    "stalled",
		`{"scope": {"repos": ["acme/api"]}}`:                                               "scope",
		`{"projects": [{"match": "acme/*", "trusted_authors": ["bot"]}]}`:                  "projects[0].trusted_authors",
		`{"projects": [{"match": "acme/*", "followups_without_workspace": true}]}`:         "projects[0].followups_without_workspace",
		`{"projects": [{"match": "acme/*", "address_without_workspace": true}]}`:           "projects[0].address_without_workspace",
		`{"github": {"drafts": {"approval": {"mode": "wait", "rule": []}}}}`:               "github.drafts.approval.rule",
		`{"workflows": {"x": {"drafts": {"approval": {"mode": "wait", "default": "x"}}}}}`: "workflows.x.drafts.approval.default",
	} {
		var unknown *UnknownKeysError
		if err := loadErr(t, raw); !errors.As(err, &unknown) || !slices.Equal(unknown.Keys, []string{key}) {
			t.Errorf("%s: %v, want the unknown key %s", raw, err, key)
		}
	}
}

func TestRecordIsNoDraftApprovalMode(t *testing.T) {
	t.Parallel()
	for raw, path := range map[string]string{
		`{"workflows": {"x": {"drafts": {"approval": {"mode": "record"}}}}}`: "workflows.x.drafts.approval",
		`{"github": {"drafts": {"approval": {"mode": "record"}}}}`:           "github.drafts.approval",
		`{"slack": {"drafts": {"approval": {"mode": "record"}}}}`:            "slack.drafts.approval",
	} {
		err := loadErr(t, raw)
		if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), `"record"`) {
			t.Errorf("%s: %v", raw, err)
		}
		if err != nil && strings.Contains(err.Error(), "did you mean") {
			t.Errorf("%s: a guess turns a draft that never sends into one that does: %v", raw, err)
		}
	}
	c := loadJSON(t, `{"workflows": {"x": {"drafts": {"approval": {"mode": "dry_run"}}}}}`)
	if a := c.Workflows["x"].Drafts.Approval; a.Mode != string(draft.DryRun) {
		t.Errorf("drafts.approval %+v", a)
	}
}

// userConfig has the shape of a user's file before the GitHub settings
// moved under github; its values are invented.
const userConfig = `{
	"projects_dir": "~/code",
	"profile": "~/profiles/mine",
	"brief": {"enabled": true},
	"automated_reviewers": ["review-bot[bot]"],
	"followups_without_workspace": true,
	"address_without_workspace": true,
	"projects": [
		{"match": "acme/*", "agent_home": "~/.agent-acme"},
		{"match": "globex/*", "followups_without_workspace": false, "trusted_authors": ["deps-bot[bot]"]}
	],
	"slack": {"client_id": "123.456", "ingest": {"terms": ["deploy"]}}
}`

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
		`"automated_reviewers": ["review-bot[bot]"],
	"followups_without_workspace": true,
	"address_without_workspace": true,`, `"github": {"automated_reviewers": ["review-bot[bot]"], "followups_without_workspace": true, "address_without_workspace": true},`,
		`"followups_without_workspace": false, "trusted_authors": ["deps-bot[bot]"]`, `"github": {"followups_without_workspace": false, "trusted_authors": ["deps-bot[bot]"]}`,
	).Replace(userConfig)
	c := loadJSON(t, edited)
	g := c.For("globex", "api").GitHub
	if !slices.Equal(c.GitHub.AutomatedReviewers, []string{"review-bot[bot]"}) || !c.GitHub.AddressWithoutWorkspace ||
		g.FollowupsWithoutWorkspace || !slices.Equal(g.TrustedAuthors, []string{"deps-bot[bot]"}) {
		t.Errorf("after the edits: %+v, globex %+v", c.GitHub, g)
	}
}

func TestGitHubDraftApprovalIsTheRuleApproval(t *testing.T) {
	t.Parallel()
	c := loadJSON(t, `{"github": {"drafts": {"approval": {"mode": "delay", "window": "1h"}}}}`)
	if got := c.PluginDraftApproval("github"); got != (draft.Approval{Mode: draft.Delay, Window: time.Hour}) {
		t.Errorf("github %+v", got)
	}
	for _, id := range []string{"slack", "wiki"} {
		if got := c.PluginDraftApproval(id); got != (draft.Approval{Mode: draft.Wait}) {
			t.Errorf("%s with none set: %+v", id, got)
		}
	}
	for raw, path := range map[string]string{
		`{"github": {"drafts": {"approval": {"mode": "delay"}}}}`:               "github.drafts.approval",
		`{"slack": {"drafts": {"approval": {"mode": "wait", "window": "1h"}}}}`: "slack.drafts.approval",
	} {
		if err := loadErr(t, raw); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestWorkflowSettingsLoad(t *testing.T) {
	t.Parallel()
	c := loadJSON(t, `{"workflows": {"x": {"observe": true, "approval": {"mode": "delay", "window": "10m",
		"rule": [{"when": "pr.mine", "mode": "wait"}]}}, "y": {"observe": false, "approval": {"rule": []}}}}`)
	x, y := c.Workflows["x"], c.Workflows["y"]
	if x.Observe == nil || !*x.Observe || x.Approval.Mode != "delay" || time.Duration(*x.Approval.Window) != 10*time.Minute ||
		len(x.Approval.Rule) != 1 || x.Approval.Rule[0] != (ApprovalRuleConfig{When: "pr.mine", Mode: "wait"}) {
		t.Errorf("x %+v %+v", x, x.Approval)
	}
	if y.Observe == nil || *y.Observe || y.Approval.Rule == nil || len(y.Approval.Rule) != 0 || y.Approval.Mode != "" {
		t.Errorf("y keeps the manifest's mode and clears its rules: %+v", y.Approval)
	}
	for raw, msg := range map[string]string{
		`{"workflows": {"x": {"approval": {"mode": "apply"}}}}`:                                  "workflows.x.approval.mode",
		`{"workflows": {"x": {"approval": {"mode": "send"}}}}`:                                   "workflows.x.approval.mode",
		`{"workflows": {"x": {"approval": {"rule": [{"when": "pr.mine", "mode": "dry_run"}]}}}}`: "workflows.x.approval.rule[0].mode",
		`{"workflows": {"x": {"approval": {"rule": [{"when": "pr.nope", "mode": "wait"}]}}}}`:    "workflows.x.approval.rule[0].when",
	} {
		if err := loadErr(t, raw); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: %v", raw, err)
		}
	}
}
