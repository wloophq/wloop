package ghsource

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/github/ghsourcetest"
	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/domain/card"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestCardFromRow(t *testing.T) {
	s := ghsourcetest.Situation{PR: ghsourcetest.Open(1, ghsourcetest.Viewer)}
	s.PR.ChecksState, s.PR.FailingChecks = "FAILURE", []dinv.FailingCheck{{Name: "lint"}}
	r := ghsourcetest.Derive(s)
	c, err := cardOf(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if c.Unit != r.Unit || c.Label != r.Label || c.Label == "" || c.Title != "Change 1" || c.State != "open" || c.Outcome != "" {
		t.Errorf("card %+v", c)
	}
	if !slices.Contains(c.Signals, card.Signal{Key: "checks", Value: "failing: lint", Tone: card.Tone("bad")}) {
		t.Errorf("signals %+v", c.Signals)
	}
	if c.Why != r.Summary.Reason || c.Why == "" {
		t.Errorf("why %q, reason %q", c.Why, r.Summary.Reason)
	}
	if want := []items.Person{{Key: "github:" + ghsourcetest.Viewer, Name: ghsourcetest.Viewer}}; !slices.EqualFunc(c.WaitsOn, want, samePerson) {
		t.Errorf("waits on %+v", c.WaitsOn)
	}
	if !c.Since.Equal(r.Summary.Since) || c.Since.IsZero() || c.Link != r.Link || c.Link == "" {
		t.Errorf("since %v link %q", c.Since, c.Link)
	}
	if c.TurnsLink != "#unit=github/"+r.Unit.Key || !c.StartedByMe || !c.Cares || c.ClosurePosted {
		t.Errorf("card %+v", c)
	}
}

func samePerson(a, b items.Person) bool { return a.Key == b.Key && a.Name == b.Name }

func TestCardLastHuman(t *testing.T) {
	at := ghsourcetest.Now.Add(-time.Hour)
	s := ghsourcetest.Situation{PR: ghsourcetest.Open(1, ghsourcetest.Viewer)}
	s.Events = []dinv.Event{ghsourcetest.Event(1, dinv.KindComment, ghsourcetest.Reviewer, at, "")}
	c, err := cardOf(context.Background(), ghsourcetest.Derive(s))
	if err != nil {
		t.Fatal(err)
	}
	h := c.LastHuman
	if h == nil || h.Who.Key != "github:"+ghsourcetest.Reviewer || h.Who.Name != ghsourcetest.Reviewer || h.What != dinv.KindComment || !h.At.Equal(at) || h.Link == "" {
		t.Fatalf("last human %+v", h)
	}
	s.Events = []dinv.Event{ghsourcetest.Event(1, dinv.KindComment, ghsourcetest.Viewer, at, "")}
	if c, _ := cardOf(context.Background(), ghsourcetest.Derive(s)); c.LastHuman != nil {
		t.Errorf("last human %+v", c.LastHuman)
	}
}

func TestCardDone(t *testing.T) {
	for _, tc := range []struct {
		state, want string
		outcome     items.Resolution
	}{{"MERGED", "merged", items.Done}, {"CLOSED", "closed", items.Moot}} {
		s := ghsourcetest.Situation{PR: ghsourcetest.Open(1, ghsourcetest.Other)}
		s.PR.State, s.PR.ChecksState = tc.state, "FAILURE"
		r := ghsourcetest.Derive(s)
		c, err := cardOf(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if c.State != tc.want || c.Outcome != tc.outcome || !c.ClosurePosted {
			t.Errorf("%s: %+v", tc.state, c)
		}
		if slices.ContainsFunc(c.Signals, func(s card.Signal) bool { return s.Tone == "bad" }) {
			t.Errorf("%s: signals %+v", tc.state, c.Signals)
		}
		if got := itemsOf(r); len(got) != 0 {
			t.Errorf("%s: items %+v", tc.state, got)
		}
	}
}

func TestCardRefusesAnotherProvider(t *testing.T) {
	r := ghsourcetest.Derive(ghsourcetest.Situation{PR: ghsourcetest.Open(1, ghsourcetest.Other)})
	r.Unit = unit.Unit{Provider: "slack", Key: "T000/C000/1.2"}
	if _, err := cardOf(context.Background(), r); err == nil {
		t.Fatal("no error")
	}
	if got := itemsOf(r); got != nil {
		t.Errorf("items %+v", got)
	}
}

func TestIdentity(t *testing.T) {
	ctx := context.Background()
	f := storetest.Empty(t)
	id := IdentityOf(f.Inventory)
	if _, err := id(ctx); err == nil {
		t.Fatal("no error without a viewer")
	}
	if err := f.Inventory.SetViewer(ctx, unit.GitHub, "octocat"); err != nil {
		t.Fatal(err)
	}
	p, err := id(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.Key != "github:octocat" || p.Name != "octocat" {
		t.Errorf("%+v", p)
	}
}

// TestTheCardsAreTheRowsCardsOfBefore holds the cards of every situation,
// author, scope and mute, each the plugin's card under the host's overlays,
// to the cards testdata/cards.golden.json holds: those the host derived
// from the whole row before the plugin reported its cards.
func TestTheCardsAreTheRowsCardsOfBefore(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "cards.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want []struct {
		Name string    `json:"name"`
		Card card.Card `json:"card"`
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	cases := ghsourcetest.Cases()
	if len(cases) != len(want) {
		t.Fatalf("%d cases, the golden holds %d", len(cases), len(want))
	}
	for i, c := range cases {
		got, err := CardOf(c.Row, c.Report)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(want[i].Card)
		if want[i].Name != c.Name || !bytes.Equal(a, b) {
			t.Errorf("%s:\n got %s\nwant %s", c.Name, a, b)
		}
	}
}
