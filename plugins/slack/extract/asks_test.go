package extract

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/host"
)

func TestASlackBriefHasTheAsksShape(t *testing.T) {
	in := loadInput(t)
	var a map[string]any
	if err := json.Unmarshal(answer(t, "answer.json"), &a); err != nil {
		t.Fatal(err)
	}
	a["brief"].(map[string]any)["asks"] = []any{
		map[string]any{"ref": "n1", "ask": "Can you sign off the widget launch?", "asker": "Bob", "ts": "1700007200.000400",
			"deadline": "before the Friday release", "missing": "the QA result", "said": "",
			"quotes": []any{map[string]any{"ts": "1700007200.000400", "text": "can someone sign off the launch"}}},
		map[string]any{"ref": "i1", "ask": "Will you review the release notes draft?", "asker": "Ann", "ts": "1700000000.000100",
			"deadline": "Friday", "missing": "", "said": "on it", "quotes": []any{}},
		map[string]any{"ref": "i9", "ask": "An ask about no item", "asker": "Cy", "ts": "", "deadline": "", "missing": "", "said": "", "quotes": []any{}},
	}
	raw, _ := json.Marshal(a)
	r, _, err := Extract(context.Background(), &scripted{answers: [][]byte{raw}}, host.Ask{Tier: host.TierFast}, in)
	if err != nil {
		t.Fatal(err)
	}
	var b storedBrief
	if err := json.Unmarshal([]byte(r.Brief), &b); err != nil {
		t.Fatal(err)
	}
	if len(b.Asks) != 2 {
		t.Fatalf("asks %+v, want the two about an item", b.Asks)
	}
	launch, notes := b.Asks[0], b.Asks[1]
	if !strings.HasPrefix(launch.Item, "e1700007200000400") || launch.Ask != "Can you sign off the widget launch?" || launch.Asker != "Bob" ||
		launch.Deadline != "before the Friday release" || launch.Missing != "the QA result" ||
		len(launch.Quotes) != 1 || launch.Quotes[0] != (storedQuote{Ref: "1700007200.000400", Text: "can someone sign off the launch"}) {
		t.Fatalf("the new item's ask %+v", launch)
	}
	ops, _, err := Ops(in, r, "T0001", parseTarget)
	if err != nil {
		t.Fatal(err)
	}
	created := ""
	for _, o := range ops {
		if o.Op == "create" {
			created = o.Item
		}
	}
	if launch.Item != created {
		t.Fatalf("the ask names %s, the op creates %s", launch.Item, created)
	}
	if notes.Item != "i1" || notes.Said != "on it" || !strings.Contains(notes.Permalink, "p1700000000000100") || notes.Quotes != nil {
		t.Fatalf("the ledger item's ask %+v", notes)
	}
	if strings.Contains(r.Brief, `"ts"`) {
		t.Fatalf("the record names a message by its permalink and a quote by its ref: %s", r.Brief)
	}
}
