package catalog

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

func TestAReviewRequestIsReadyForAChat(t *testing.T) {
	l := bundled(t)
	got, notice, err := Copy(l, CopyReviewRequest, CopyFacts{
		Row: api.Row{Unit: r7, Label: "acme/widgets#4877", Link: "https://github.com/acme/widgets/pull/4877", Title: "perf(queue): refill <each> `free` slot"},
		Brief: &api.Brief{Goal: "Give each free slot the next message.", Context: "The loop asked for the whole batch.",
			RiskWhy: "Affects every pod & the dispatch job."},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantText := "review, pls? `perf(queue): refill <each> 'free' slot`\nhttps://github.com/acme/widgets/pull/4877\n\n*Goal:*\nGive each free slot the next message.\n\n*Context:*\nThe loop asked for the whole batch.\n\n*Risk:*\nAffects every pod & the dispatch job."
	if got.Text != wantText {
		t.Errorf("text:\n%s\nwant:\n%s", got.Text, wantText)
	}
	wantHTML := `review, pls? <code>perf(queue): refill &lt;each&gt; ` + "`free`" + ` slot</code><br><a href="https://github.com/acme/widgets/pull/4877">acme/widgets#4877</a><br><br><b>Goal:</b><br>Give each free slot the next message.<br><br><b>Context:</b><br>The loop asked for the whole batch.<br><br><b>Risk:</b><br>Affects every pod &amp; the dispatch job.`
	if got.HTML != wantHTML {
		t.Errorf("html:\n%s\nwant:\n%s", got.HTML, wantHTML)
	}
	if notice != "Copied the review request for acme/widgets#4877; paste it in a chat." {
		t.Errorf("notice %q", notice)
	}
}

func TestAReviewRequestWithoutABriefOrAWebLink(t *testing.T) {
	got, _, err := Copy(bundled(t), CopyReviewRequest, CopyFacts{Row: api.Row{Unit: r7, Label: "acme/api#7", Link: "javascript:alert(1)", Title: "fix: x"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "review, pls? `fix: x`\nacme/api#7" || got.HTML != "review, pls? <code>fix: x</code><br>acme/api#7" || strings.Contains(got.HTML, "javascript") {
		t.Errorf("without a brief or a web link: %q / %q", got.Text, got.HTML)
	}
}

func TestOnlyAnActionWithACopyTableCopies(t *testing.T) {
	l := bundled(t)
	if got, _, err := Copy(l, "pause", CopyFacts{}); got != nil || err != nil {
		t.Errorf("pause copies %v, %v", got, err)
	}
	for _, s := range Specs(l) {
		if s.Copies != (s.ID == CopyReviewRequest) {
			t.Errorf("%s copies: %v", s.ID, s.Copies)
		}
	}
}

func TestAnOpenPullRequestOffersItsReviewRequestOnItsTasks(t *testing.T) {
	l := bundled(t)
	f := Facts{Intents: l, PR: r7, Owner: "acme", Repo: "api", Row: api.Row{Unit: r7}, Open: true, NudgeWho: []string{}}
	if st := stateOf(t, f, CopyReviewRequest); !st.Enabled {
		t.Errorf("open: %+v", st)
	}
	f.Open = false
	if slices.ContainsFunc(States(f), func(st api.ActionState) bool { return st.ID == CopyReviewRequest }) {
		t.Error("a closed pull request offers a review request")
	}
	i := slices.IndexFunc(Specs(l), func(s api.ActionSpec) bool { return s.ID == CopyReviewRequest })
	if s := Specs(l)[i]; !slices.Contains(s.Surfaces, SurfaceTask) || !slices.Contains(s.Surfaces, SurfaceDetail) || s.Plugin != "github" {
		t.Errorf("spec %+v", s)
	}
}
