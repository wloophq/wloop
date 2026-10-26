package extract

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTheExtractionWritesTheStructuredBriefPerDMSegment(t *testing.T) {
	r := newRig(t)
	r.facts.post(jDM, ts(1), "", jOther, "can you review the deck", jT0)
	r.facts.post(jDM, ts(2), "", jViewer, "sure", jT0)
	r.facts.post(jDM, ts(3), "", jOther, "the invoice export is down", jT0)
	deckBrief := briefObj("Review the deck")
	deckBrief["kind"], deckBrief["tags"] = "request", obj{"domain": []any{"Sales Deck"}, "audience": []any{"internal"}, "concern": []any{}}
	outage := briefObj("The invoice export is down")
	outage["kind"], outage["risk"], outage["risk_why"] = "incident", "high", "customers cannot export"
	outage["open_questions"], outage["tags"] = []any{"since when?"}, obj{"domain": []any{"billing"}, "audience": []any{"end-user"}, "concern": []any{"reliability"}}
	kept := briefObj("")
	kept["unchanged"] = true
	r.asker.reply = func(n int, _ turns.Ask) ([]byte, error) {
		if n == 0 {
			return answerJSON(t, nil, []obj{{"ref": "new", "from_ts": ts(1), "to_ts": ts(2), "topic": "the deck", "brief": deckBrief},
				{"ref": "new", "from_ts": ts(3), "to_ts": "", "topic": "the export", "brief": outage}}, nil, nil), nil
		}
		return answerJSON(t, nil, []obj{{"ref": "s1", "from_ts": ts(1), "to_ts": ts(2), "topic": "the deck", "brief": kept},
			{"ref": "s2", "from_ts": ts(3), "to_ts": "", "topic": "the export", "brief": kept}}, nil, obj{"brief": kept}), nil
	}
	r.run(jT0.Add(2 * time.Minute))
	deck, export := segUnit(jTeam, jDM, strings.ReplaceAll(ts(1), ".", "")), segUnit(jTeam, jDM, strings.ReplaceAll(ts(3), ".", ""))
	want := map[unit.Unit]storedBrief{
		deck: {Goal: "Review the deck", Kind: "request", Risk: "low", OpenQuestions: []string{}, Tags: []string{"domain:sales-deck", "audience:internal"}},
		export: {Goal: "The invoice export is down", Kind: "incident", Risk: "high", RiskWhy: "customers cannot export",
			OpenQuestions: []string{"since when?"}, Tags: []string{"concern:reliability", "domain:billing", "audience:end-user"}},
	}
	check := func(when string) {
		t.Helper()
		st, err := r.job.Store.States(context.Background(), []unit.Unit{deck, export})
		if err != nil {
			t.Fatal(err)
		}
		for u, w := range want {
			var got storedBrief
			if err := json.Unmarshal([]byte(st[u].Brief), &got); err != nil {
				t.Fatalf("%s: %s: %q: %v", when, u.Key, st[u].Brief, err)
			}
			gj, _ := json.Marshal(got)
			wj, _ := json.Marshal(w)
			if string(gj) != string(wj) {
				t.Errorf("%s: %s's brief\n%s\nwant\n%s", when, u.Key, gj, wj)
			}
		}
	}
	check("the first pass")
	r.facts.post(jDM, ts(4), "", jOther, "still down?", jT0.Add(5*time.Minute))
	r.run(jT0.Add(8 * time.Minute))
	if r.asker.calls() != 2 {
		t.Fatalf("%d calls", r.asker.calls())
	}
	check("an unchanged pass")
}

func TestAnExtractionBriefOutsideTheSchemaIsRefused(t *testing.T) {
	in := loadInput(t)
	for name, mod := range map[string]func(obj){
		"a pull request kind": func(b obj) { b["kind"] = "feature" },
		"a size":              func(b obj) { b["size"] = "s" },
		"four questions":      func(b obj) { b["open_questions"] = []any{"a", "b", "c", "d"} },
		"an unknown tag key":  func(b obj) { b["tags"].(obj)["lang"] = []any{"go"} },
	} {
		b := briefObj("g")
		mod(b)
		raw := answerJSON(t, nil, nil, nil, obj{"brief": b})
		if _, err := decode(raw, in); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}

func TestAWantedThreadIsReadBeforeItIsQuiet(t *testing.T) {
	r := newRig(t)
	u := thread(ts(1))
	var briefed []unit.Unit
	r.job.Briefed = func(b unit.Unit) { briefed = append(briefed, b) }
	if r.job.Want(u) {
		t.Fatal("a want before anything is waiting to be read")
	}
	r.facts.post(jChan, ts(1), "", jOther, "please review", jT0)
	r.run(jT0.Add(time.Second))
	if r.asker.calls() != 0 {
		t.Fatalf("%d calls before quiet_for", r.asker.calls())
	}
	if !r.job.Want(u) {
		t.Fatal("the waiting thread was not wanted")
	}
	if r.job.Want(segUnit(jTeam, jDM, "1700000000000100")) {
		t.Fatal("a DM with nothing to read was wanted")
	}
	r.run(jT0.Add(2 * time.Second))
	if r.asker.calls() != 1 || len(briefed) != 1 || briefed[0] != u {
		t.Fatalf("%d calls, briefed %v", r.asker.calls(), briefed)
	}
	if r.job.Want(u) {
		t.Fatal("a read thread is still wanted")
	}
}

func TestTheBriefIntentsExtrasJoinTheExtraction(t *testing.T) {
	in := loadInput(t)
	in.Extra = &Extra{Schema: []byte(`{"type":"object","additionalProperties":false,"required":["approach"],"properties":{"approach":{"type":"string","enum":["patch","rewrite"]}}}`),
		Versions: map[string]string{"approach": "f1-abc"}}
	asked, _, err := schemaFor(in.Extra)
	if err != nil || !strings.Contains(asked, `"approach"`) {
		t.Fatalf("the asked schema: %v", err)
	}
	b := briefObj("g")
	b["extra"] = obj{"approach": "rewrite"}
	r, err := decode(answerJSON(t, nil, nil, nil, obj{"brief": b}), in)
	if err != nil {
		t.Fatal(err)
	}
	var got storedBrief
	if err := json.Unmarshal([]byte(r.Brief), &got); err != nil || got.Extra["approach"] != "rewrite" || got.ExtraVersions["approach"] != "f1-abc" {
		t.Fatalf("stored %s %v", r.Brief, err)
	}
	b["extra"] = obj{"approach": "magic"}
	if _, err := decode(answerJSON(t, nil, nil, nil, obj{"brief": b}), in); err == nil {
		t.Fatal("an extra outside its enum decoded")
	}
	delete(b, "extra")
	if _, err := decode(answerJSON(t, nil, nil, nil, obj{"brief": b}), in); err == nil {
		t.Fatal("a brief without its extra decoded")
	}
	if _, err := decode(answerJSON(t, nil, nil, nil, obj{"brief": briefObj("g")}), loadInput(t)); err != nil {
		t.Fatalf("without extras: %v", err)
	}
}
