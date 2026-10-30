package extract

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	slack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/host"
	"github.com/wloophq/wloop/plugins/slack/store"
	"github.com/wloophq/wloop/plugins/slack/unit"
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
	r.asker.reply = func(n int, _ host.Ask) ([]byte, error) {
		if n == 0 {
			return answerJSON(t, nil, []obj{{"ref": "new", "from_ts": ts(1), "to_ts": ts(2), "topic": "the deck", "brief": deckBrief},
				{"ref": "new", "from_ts": ts(3), "to_ts": "", "topic": "the export", "brief": outage}}, nil, nil), nil
		}
		return answerJSON(t, nil, []obj{{"ref": "s1", "from_ts": ts(1), "to_ts": ts(2), "topic": "the deck", "brief": kept},
			{"ref": "s2", "from_ts": ts(3), "to_ts": "", "topic": "the export", "brief": kept}}, nil, obj{"brief": kept}), nil
	}
	var put, briefed []unit.Unit
	r.job.PutBrief = func(_ context.Context, u unit.Unit, _ string, _ time.Time) error {
		if err := slack.ValidKey(u.Key); err != nil {
			return err
		}
		put = append(put, u)
		return nil
	}
	r.job.Briefed = func(u unit.Unit) { briefed = append(briefed, u) }
	r.run(jT0.Add(2 * time.Minute))
	deck, export := segUnit(jTeam, jDM, strings.ReplaceAll(ts(1), ".", "")), segUnit(jTeam, jDM, strings.ReplaceAll(ts(3), ".", ""))
	slices.SortFunc(briefed, unit.Compare)
	if segments := []unit.Unit{deck, export}; !slices.Equal(put, segments) || !slices.Equal(briefed, segments) {
		t.Fatalf("the host got the briefs of %v and heard of %v, want the segments %v", put, briefed, segments)
	}
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
	for name, mod := range map[string]func(obj){
		"an extra outside its enum": func(b obj) { b["extra"] = obj{"approach": "magic"} },
		"a brief without its extra": func(b obj) { delete(b, "extra") },
	} {
		bad := briefObj("g")
		mod(bad)
		seg := obj{"ref": "new", "from_ts": in.Messages[0].TS, "to_ts": "", "topic": "t", "brief": bad}
		r, err := decode(answerJSON(t, []obj{jItem("n1", in.Messages[len(in.Messages)-1].TS)}, []obj{seg}, nil, obj{"brief": bad}), in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(r.Items) != 1 || r.ExtrasDropped != 2 {
			t.Errorf("%s: %d items, %d extras dropped", name, len(r.Items), r.ExtrasDropped)
		}
		for _, stored := range []string{r.Brief, r.Segments[0].Brief} {
			var got storedBrief
			if err := json.Unmarshal([]byte(stored), &got); err != nil || got.Goal != "g" || got.Extra != nil || got.ExtraVersions != nil {
				t.Errorf("%s: stored %s %v", name, stored, err)
			}
		}
	}
	b["goal"] = 3
	if _, err := decode(answerJSON(t, nil, nil, nil, obj{"brief": b}), in); err == nil {
		t.Fatal("a brief outside the core schema decoded")
	}
	if _, err := decode(answerJSON(t, nil, nil, nil, obj{"brief": briefObj("g")}), loadInput(t)); err != nil {
		t.Fatalf("without extras: %v", err)
	}
}

var approachExtra = &Extra{Schema: []byte(`{"type":"object","additionalProperties":false,"required":["approach"],"properties":{"approach":{"type":"string","enum":["patch","rewrite"]}}}`),
	Versions: map[string]string{"approach": "f1-abc"}}

func TestUnchangedKeepsOnlyACompleteBrief(t *testing.T) {
	answer := briefObj("Ship the export fix")
	answer["unchanged"], answer["kind"], answer["extra"] = true, "incident", obj{"approach": "patch"}
	for name, c := range map[string]struct {
		prev string
		kept bool
	}{
		"a migrated goal-only brief": {`{"goal":"Ship the export fix"}`, false},
		"a brief without the extra":  {`{"goal":"g","kind":"request"}`, false},
		"an older extra version":     {`{"goal":"g","kind":"request","extra":{"approach":"rewrite"},"extra_versions":{"approach":"f0-old"}}`, false},
		"a complete brief":           {`{"goal":"g","kind":"request","extra":{"approach":"rewrite"},"extra_versions":{"approach":"f1-abc"}}`, true},
	} {
		in := loadInput(t)
		in.Extra, in.Brief = approachExtra, c.prev
		in.Segments, in.SegmentBriefs = []slack.Span{{ID: "1", FromTS: in.Messages[0].TS}}, []string{c.prev}
		seg := obj{"ref": "s1", "from_ts": in.Messages[0].TS, "to_ts": "", "topic": "the export", "brief": answer}
		r, err := decode(answerJSON(t, nil, []obj{seg}, nil, obj{"brief": answer}), in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for which, got := range map[string]string{"conversation": r.Brief, "segment": r.Segments[0].Brief} {
			if c.kept {
				if got != "" {
					t.Errorf("%s: the %s's brief was replaced by %s", name, which, got)
				}
				continue
			}
			var sb storedBrief
			if err := json.Unmarshal([]byte(got), &sb); err != nil || sb.Kind != "incident" || sb.Extra["approach"] != "patch" || sb.ExtraVersions["approach"] != "f1-abc" {
				t.Errorf("%s: the %s's brief %q: %v", name, which, got, err)
			}
		}
	}
}

func TestAMigratedDMSegmentGetsAFullBriefFromAnUnchangedAnswer(t *testing.T) {
	r := newRig(t)
	r.job.Extra = func(context.Context) (Extra, error) { return *approachExtra, nil }
	r.facts.post(jDM, ts(1), "", jOther, "can you review the deck", jT0)
	first := briefObj("Review the deck")
	first["extra"] = obj{"approach": "patch"}
	kept := briefObj("Review the deck")
	kept["unchanged"], kept["kind"], kept["extra"] = true, "question", obj{"approach": "rewrite"}
	r.asker.reply = func(n int, _ host.Ask) ([]byte, error) {
		b := first
		if n > 0 {
			b = kept
		}
		return answerJSON(t, nil, []obj{{"ref": map[bool]string{true: "new", false: "s1"}[n == 0], "from_ts": ts(1), "to_ts": "", "topic": "the deck", "brief": b}},
			nil, obj{"brief": b}), nil
	}
	r.run(jT0.Add(2 * time.Minute))
	if _, err := r.db.Write(context.Background(), func(tx *store.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE extraction_cursors SET brief = '{"goal":"Review the deck"}' WHERE brief != ''`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	r.facts.post(jDM, ts(2), "", jOther, "any news?", jT0.Add(5*time.Minute))
	r.run(jT0.Add(8 * time.Minute))
	if r.asker.calls() != 2 {
		t.Fatalf("%d calls", r.asker.calls())
	}
	deck := segUnit(jTeam, jDM, strings.ReplaceAll(ts(1), ".", ""))
	st, err := r.job.Store.States(context.Background(), []unit.Unit{deck})
	if err != nil {
		t.Fatal(err)
	}
	var got storedBrief
	if err := json.Unmarshal([]byte(st[deck].Brief), &got); err != nil || got.Kind != "question" || got.Extra["approach"] != "rewrite" {
		t.Fatalf("the migrated segment's brief %q: %v", st[deck].Brief, err)
	}
}

func TestADroppedExtraKeepsThePreviousBriefsExtrasAtTheirVersion(t *testing.T) {
	answer := briefObj("Ship the export fix")
	answer["kind"], answer["extra"] = "incident", obj{"approach": "magic"}
	for name, c := range map[string]struct {
		prev            string
		extra, versions string
	}{
		"an extra at its version": {`{"goal":"g","kind":"request","extra":{"approach":"rewrite","legacy":"x"},"extra_versions":{"approach":"f1-abc","legacy":"f0-old"}}`,
			`{"approach":"rewrite"}`, `{"approach":"f1-abc"}`},
		"an extra at an older version": {`{"goal":"g","kind":"request","extra":{"approach":"rewrite"},"extra_versions":{"approach":"f0-old"}}`, `null`, `null`},
		"no previous brief":            {"", `null`, `null`},
	} {
		in := loadInput(t)
		in.Extra, in.Brief = approachExtra, c.prev
		in.Segments, in.SegmentBriefs = []slack.Span{{ID: "1", FromTS: in.Messages[0].TS}}, []string{c.prev}
		seg := obj{"ref": "s1", "from_ts": in.Messages[0].TS, "to_ts": "", "topic": "the export", "brief": answer}
		r, err := decode(answerJSON(t, nil, []obj{seg}, nil, obj{"brief": answer}), in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r.ExtrasDropped != 2 {
			t.Errorf("%s: %d extras dropped", name, r.ExtrasDropped)
		}
		for which, got := range map[string]string{"conversation": r.Brief, "segment": r.Segments[0].Brief} {
			var sb storedBrief
			if err := json.Unmarshal([]byte(got), &sb); err != nil || sb.Kind != "incident" {
				t.Fatalf("%s: the %s's brief %q: %v", name, which, got, err)
			}
			extra, _ := json.Marshal(sb.Extra)
			versions, _ := json.Marshal(sb.ExtraVersions)
			if string(extra) != c.extra || string(versions) != c.versions {
				t.Errorf("%s: the %s's extras %s %s, want %s %s", name, which, extra, versions, c.extra, c.versions)
			}
		}
	}
}
