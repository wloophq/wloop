package cli

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// TestReattackReviewSessionMayWriteTheUsersRules is DELTA RE1-B: a review session's policy must deny an edit of
// the profile, the configuration or the store, and the loop's own config set, or a session that adds
// mode = "send" for its intent releases its own drafts.
func TestReattackReviewSessionMayWriteTheUsersRules(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Profile = t.TempDir()
	settings := claude.Agent{}.Settings(policies[policyRound](te.Env, "review", te.controlSurface(te.cfg)))
	deny := strings.Join(denyRules(t, settings), "\n")
	for name, want := range map[string]string{
		"the profile":       te.cfg.Profile,
		"the configuration": te.ConfigPath,
		"the store":         te.StateDir,
		"config set":        "config set",
	} {
		if !strings.Contains(deny, want) {
			t.Errorf("a review session's policy denies no write of %s (%s)", name, want)
		}
	}
}

// TestASessionsPolicyDeniesEveryWriteOfTheUsersRules renders the round and the probe policies of a session and
// finds a deny rule for each way to change what approves a draft: the profile, the configuration file, the
// store and its backups, and the loop's own commands that write them.
func TestASessionsPolicyDeniesEveryWriteOfTheUsersRules(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Profile = t.TempDir()
	for _, policy := range []string{policyRound, "probe"} {
		deny := denyRules(t, claude.Agent{}.Settings(policies[policy](te.Env, "review", te.controlSurface(te.cfg))))
		for _, want := range []string{
			"Edit(/" + te.cfg.Profile + "/**)",
			"Edit(/" + te.ConfigPath + ")",
			"Edit(/" + te.storePath() + ")",
			"Edit(/" + te.storePath() + "-wal)",
			"Edit(/" + filepath.Join(te.StateDir, "backups") + "/**)",
			"Bash(" + brand.Command + " config set:*)",
			"Bash(" + brand.Command + " config unset:*)",
			"Bash(" + brand.Command + " store:*)",
			"Bash(" + brand.Command + " profile add:*)",
		} {
			if !slices.ContainsFunc(deny, func(r string) bool {
				return r == want || strings.HasPrefix(want, "Bash(") && strings.HasPrefix(r, strings.TrimSuffix(want, ":*)"))
			}) {
				t.Errorf("%s: no deny rule %s in %q", policy, want, deny)
			}
		}
	}
}
