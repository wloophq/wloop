package turnstest

import (
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Intents is the registry of the bundled intents, with the record rules
// of their manifests, for tests that build a turns.Service.
func Intents() *dintent.Registry {
	return dintent.NewRegistry([]dintent.Def{
		{Name: dintent.Review, Role: "round.review", UserFacing: true, Board: "review", Answerable: true, Reconcile: "my_review", Session: dintent.Session{Title: "review"},
			Record: dintent.RecordRules{
				States: []string{"done", "settled", "asked", "failed"}, Artifacts: []string{"review-draft"}, Outcome: []string{"verdict", "url"},
				OutcomeDoc: "`outcome`: on `done`, the published `verdict`, the review's `url`,\n  `blocking` and `non_blocking`; on `asked` with a held draft, the `verdict`\n  the draft would publish and its counts.",
				Require: []dintent.Require{
					{State: "done", Fields: []string{"verdict", "url"}},
					{State: "asked", Artifact: "review-draft", Fields: []string{"verdict"}},
				},
			}},
		{Name: dintent.Address, Role: "round.address", UserFacing: true, Board: "author", Answerable: true, Session: dintent.Session{Title: "triage"},
			Record: dintent.RecordRules{States: []string{"done", "asked", "failed"}, Artifacts: []string{"triage"}, HistoryArtifact: "triage"}},
		{Name: dintent.Nudge, Role: "nudge", UserFacing: true, Session: dintent.Session{Title: "nudge", CloseWhenDone: true},
			Record: dintent.RecordRules{
				States: []string{"done", "failed"}, Outcome: []string{"url"}, OutcomeDoc: "`outcome` (done): `url`, the comment's URL.",
				Require: []dintent.Require{{State: "done", Fields: []string{"url"}}},
			}},
		{Name: dintent.Probe, Role: "probe", Answerable: true, Record: dintent.RecordRules{States: []string{"done", "asked", "failed"}}},
		{Name: dintent.Act, Role: "headless.default", UserFacing: true, Answerable: true, Provider: dintent.AnyProvider, Runners: []string{"headless", "session"}, Verb: "delegate",
			Session: dintent.Session{Title: "act", CloseWhenDone: true}, Record: dintent.RecordRules{States: []string{"done", "asked", "failed"}}},
		{Name: "brief", Builds: "brief", Prompt: "prompts/brief.md", Provider: "github", Providers: []unit.ProviderID{"github", "slack"}},
	})
}
