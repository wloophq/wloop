package sqlite

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	linkThread  = unit.Unit{Provider: "slack", Key: "C000AAA/1.1"}
	linkThread2 = unit.Unit{Provider: "slack", Key: "C000AAA/2.2"}
	linkPull    = unit.Unit{Provider: unit.GitHub, Key: "acme/api#527"}
	linkPull2   = unit.Unit{Provider: unit.GitHub, Key: "acme/api#528"}
)

func init() {
	reads = append(reads, "DB.Links", "DB.LinkTargets", "linkStore.From", "linkStore.To")
	writes = append(writes,
		writeCase{
			method: "linkStore.ReplaceFacts",
			call: func(ctx context.Context, e *noteEnv) error {
				return e.db.Links().ReplaceFacts(ctx, []unit.Unit{linkThread}, []link.Link{sampleLink(linkThread, linkPull, link.Facts, noteAt)}, noteAt)
			},
			topics: port.TopicLinks, units: []unit.Unit{linkPull, linkThread},
		},
		writeCase{
			method: "linkStore.Put",
			call: func(ctx context.Context, e *noteEnv) error {
				return e.db.Links().Put(ctx, sampleLink(linkThread, linkPull, link.Extraction, noteAt))
			},
			topics: port.TopicLinks, units: []unit.Unit{linkPull, linkThread},
		},
	)
	hotPlans = append(hotPlans,
		hotPlan{name: "links from a unit", query: linksFromSQL, args: 2, want: []string{"SEARCH links USING PRIMARY KEY (provider=? AND unit_key=?)"},
			tempSort: "USE TEMP B-TREE FOR ORDER BY"},
		hotPlan{name: "links to units", query: linksToSQL, args: 1, want: []string{"SEARCH links USING INDEX links_to (to_provider=? AND to_key=?)"},
			tempSort: "USE TEMP B-TREE FOR ORDER BY"},
		hotPlan{name: "link targets", query: linkTargetsSQL, args: 0, want: []string{"links USING COVERING INDEX links_to"}},
	)
}

func sampleLink(from, to unit.Unit, origin link.Origin, at time.Time) link.Link {
	return link.Link{From: from, To: to, Kind: link.Review, Item: "i1",
		Evidence: unit.Anchor{Unit: from, Ref: "C000AAA/1.1", At: at}, Origin: origin, At: at}
}

func sameLinks(t *testing.T, got, want []link.Link) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("links = %+v\nwant %+v", got, want)
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.From != w.From || g.To != w.To || g.Kind != w.Kind || g.Item != w.Item || g.Origin != w.Origin ||
			!g.At.Equal(w.At) || g.Evidence.Ref != w.Evidence.Ref || g.Evidence.Unit != w.Evidence.Unit {
			t.Fatalf("link %d = %+v\nwant %+v", i, g, w)
		}
	}
}

func TestLinksPutUpserts(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	first := sampleLink(linkThread, linkPull, link.Extraction, noteAt)
	if err := db.Links().Put(ctx, first); err != nil {
		t.Fatal(err)
	}
	newer := first
	newer.Evidence.Ref, newer.At = "C000AAA/1.9", noteAt.Add(time.Hour)
	if err := db.Links().Put(ctx, newer); err != nil {
		t.Fatal(err)
	}
	got, err := db.Links().From(ctx, linkThread)
	if err != nil {
		t.Fatal(err)
	}
	want := newer
	want.At = noteAt
	sameLinks(t, got, []link.Link{want})
}

func TestLinksPutRefusesFactsOrigin(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	if err := db.Links().Put(context.Background(), sampleLink(linkThread, linkPull, link.Facts, noteAt)); err == nil {
		t.Fatal("Put of a facts link succeeded")
	}
}

func TestLinksPutKeepsUserOrigin(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	if err := db.Links().Put(ctx, sampleLink(linkThread, linkPull, link.User, noteAt)); err != nil {
		t.Fatal(err)
	}
	if err := db.Links().Put(ctx, sampleLink(linkThread, linkPull, link.Extraction, noteAt)); err != nil {
		t.Fatal(err)
	}
	got, _ := db.Links().From(ctx, linkThread)
	if len(got) != 1 || got[0].Origin != link.User {
		t.Fatalf("links = %+v", got)
	}
}

func TestLinksTo(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	a := sampleLink(linkThread2, linkPull, link.Extraction, noteAt)
	b := sampleLink(linkThread, linkPull, link.Extraction, noteAt.Add(2*time.Hour))
	c := sampleLink(linkThread, linkPull2, link.User, noteAt.Add(time.Hour))
	c.Kind = link.Merge
	other := sampleLink(linkThread, unit.Unit{Provider: unit.GitHub, Key: "acme/api#1"}, link.Extraction, noteAt)
	for _, l := range []link.Link{a, b, c, other} {
		if err := db.Links().Put(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.Links().To(ctx, []unit.Unit{linkPull, linkPull2})
	if err != nil {
		t.Fatal(err)
	}
	sameLinks(t, got, []link.Link{c, b, a})
	if got, err := db.Links().To(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("To(nil) = %v %v", got, err)
	}
}

func TestReplaceFactsKeepsOtherOriginsAndAt(t *testing.T) {
	t.Parallel()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := Open(filepath.Join(t.TempDir(), "store.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	facts := sampleLink(linkThread, linkPull, link.Facts, noteAt)
	facts.Kind = link.Mention
	extracted := sampleLink(linkThread, linkPull, link.Extraction, noteAt)
	if err := db.Links().Put(ctx, extracted); err != nil {
		t.Fatal(err)
	}
	if err := db.Links().ReplaceFacts(ctx, []unit.Unit{linkThread}, []link.Link{facts}, noteAt); err != nil {
		t.Fatal(err)
	}
	later := facts
	later.At = noteAt.Add(time.Hour)
	gen := log.Gen()
	if err := db.Links().ReplaceFacts(ctx, []unit.Unit{linkThread}, []link.Link{later}, noteAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if c := log.Since(gen, port.TopicLinks); !c.Empty() {
		t.Fatalf("an unchanged replace noted %+v", c)
	}
	got, _ := db.Links().From(ctx, linkThread)
	sameLinks(t, got, []link.Link{facts, extracted})
	if err := db.Links().ReplaceFacts(ctx, []unit.Unit{linkThread}, nil, noteAt.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ = db.Links().From(ctx, linkThread)
	sameLinks(t, got, []link.Link{extracted})
}

func TestReplaceFactsRefusesForeignFrom(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	err := db.Links().ReplaceFacts(context.Background(), []unit.Unit{linkThread}, []link.Link{sampleLink(linkThread2, linkPull, link.Facts, noteAt)}, noteAt)
	if err == nil {
		t.Fatal("a link from a unit outside from was saved")
	}
	if got, _ := db.Links().From(context.Background(), linkThread2); !slices.Equal(got, nil) {
		t.Fatalf("links = %+v", got)
	}
}

func TestLinkTargetsAreResolvableTargets(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	mention := sampleLink(linkThread, unit.Unit{Provider: unit.GitHub, Key: "acme/api#1"}, link.Extraction, noteAt)
	mention.Kind = link.Mention
	unitOnly := sampleLink(linkThread, unit.Unit{Provider: unit.GitHub, Key: "acme/api#2"}, link.Extraction, noteAt)
	unitOnly.Item = ""
	merge := sampleLink(linkThread2, linkPull, link.User, noteAt)
	merge.Kind = link.Merge
	for _, l := range []link.Link{mention, unitOnly, sampleLink(linkThread, linkPull, link.Extraction, noteAt), merge,
		sampleLink(linkThread, linkPull2, link.Extraction, noteAt)} {
		if err := db.Links().Put(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.LinkTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []unit.Unit{linkPull, linkPull2}) {
		t.Fatalf("targets = %v", got)
	}
}
