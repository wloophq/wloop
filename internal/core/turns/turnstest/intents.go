package turnstest

import (
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Intents is the registry of the bundled intents, with the record rules
// of their manifests, for tests that build a turns.Service.
func Intents() *dintent.Registry {
	return dintent.NewRegistry([]dintent.Def{
		{Name: dintent.Review, Role: "pr.review", UserFacing: true, Board: "review", Answerable: true, Reconcile: "my_review", Session: dintent.Session{Title: "review", NoSourceWrites: true},
			Record: dintent.RecordRules{
				States: []string{"done", "settled", "asked", "failed"}, Missing: "missing-record", Artifacts: []string{"review-draft"}, Outcome: []string{"verdict", "url"},
				OutcomeDoc: "`outcome`: on `done` and on `asked`, the `verdict` your review draft\n  posts, `blocking` and `non_blocking`; leave `url` out: it is filled\n  when the draft is posted.",
				Require: []dintent.Require{
					{State: "done", Fields: []string{"verdict"}},
					{State: "asked", Artifact: "review-draft", Fields: []string{"verdict"}},
				},
			}},
		{Name: dintent.Address, Role: "pr.address", UserFacing: true, Board: "author", Answerable: true, Session: dintent.Session{Title: "triage", NoSourceWrites: true},
			Record: dintent.RecordRules{States: []string{"done", "asked", "failed"}, Missing: "missing-record", Artifacts: []string{"triage"}, HistoryArtifact: "triage"}},
		{Name: dintent.Nudge, Role: "pr.nudge", UserFacing: true, Session: dintent.Session{Title: "nudge", CloseWhenDone: true, NoSourceWrites: true},
			Record: dintent.RecordRules{
				States: []string{"done", "failed"}, Outcome: []string{"url"}, OutcomeDoc: "`outcome` (done): leave `url` out; it is filled when your comment draft is posted.",
			}},
		{Name: dintent.Probe, Role: "probe", Answerable: true, Record: dintent.RecordRules{States: []string{"done", "asked", "failed"}, Missing: "missing-record"}},
		{Name: dintent.Act, Role: "headless", UserFacing: true, Answerable: true, Provider: dintent.AnyProvider, Runners: []string{"headless", "session"}, Verb: "delegate",
			Session: dintent.Session{Title: "act", CloseWhenDone: true}, Record: dintent.RecordRules{States: []string{"done", "asked", "failed"}, Missing: "missing-record"}},
		{Name: "start", UserFacing: true, Answerable: true, Provider: "task", Session: dintent.Session{Title: "start"},
			Record: dintent.RecordRules{States: []string{"done", "asked", "failed"}, Outcome: []string{"url"},
				OutcomeDoc: "`outcome` (done): `url`, the pull request you opened or the message you posted.", AttachURL: true,
				Requires: "report", Missing: "missing-record"}},
		{Name: "chat", UserFacing: true, Answerable: true, Provider: "task", Runners: []string{"session"}, Session: dintent.Session{Title: "chat"},
			Record: dintent.RecordRules{States: []string{"done", "asked", "failed"}, Outcome: []string{"url"},
				OutcomeDoc: "`outcome` (done): `url`, when the turn produced a link the user should open.", Missing: "missing-record"}},
		{Name: "attach", Role: "attach", Provider: "task", Runners: []string{"session"}, Session: dintent.Session{Title: "attach"}, Record: dintent.RecordRules{None: true}},
		{Name: "brief", Builds: "brief", Prompt: "prompts/brief.md", Provider: "github", Providers: []unit.ProviderID{"github", "slack"}},
	})
}
