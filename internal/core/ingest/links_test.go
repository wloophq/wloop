package ingest_test

import (
	"context"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/ingest/ingesttest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var pull = unit.Unit{Provider: unit.GitHub, Key: "acme/api#527"}

func linkedFixture() (*fixture, *ingesttest.Links) {
	f := newFixture(100)
	links := &ingesttest.Links{Log: f.log}
	f.reg.Units = unit.NewRegistry(ingesttest.Codec(), unit.OpaqueCodec{ID: unit.GitHub})
	f.reg.Links = links
	return f, links
}

func threadLink(from, to unit.Unit, kind link.Kind) link.Link {
	return link.Link{From: from, To: to, Kind: kind, Item: "i1", Evidence: unit.Anchor{Unit: from, Ref: "C1/0001"}}
}

func touch(t *testing.T, f *fixture, objects ...string) ingest.Report {
	t.Helper()
	rep, err := ingest.Touch(context.Background(), f.reg, objects, t0)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestIngestStoresFactsLinks(t *testing.T) {
	f, links := linkedFixture()
	f.svc.Post("C1 0001", "0001", "please review acme/api#527")
	f.svc.SetLinks("C1 0001", threadLink(th("C1 0001"), pull, link.Review))
	gen := f.log.Gen()
	rep := touch(t, f, "C1 0001")
	if rep.Links != 1 || rep.LinksDropped != 0 {
		t.Fatalf("report = %+v", rep)
	}
	ctx := context.Background()
	from, _ := links.From(ctx, th("C1 0001"))
	to, _ := links.To(ctx, []unit.Unit{pull})
	if len(from) != 1 || len(to) != 1 || from[0].Origin != link.Facts || to[0].To != pull || !to[0].At.Equal(t0) {
		t.Fatalf("from = %+v, to = %+v", from, to)
	}
	if c := f.log.Since(gen, port.TopicLinks); !c.Has(port.TopicLinks) || !slices.Contains(c.Units, pull) {
		t.Fatalf("links note = %+v", c)
	}
}

func TestIngestDropsLinkToUnknownUnit(t *testing.T) {
	f, links := linkedFixture()
	f.svc.Post("C1 0001", "0001", "two links")
	f.svc.SetLinks("C1 0001",
		threadLink(th("C1 0001"), unit.Unit{Provider: "jira", Key: "ABC-1"}, link.Mention),
		threadLink(th("C1 0001"), unit.Unit{Provider: unit.GitHub, Key: " "}, link.Mention),
		threadLink(th("C1 0001"), pull, link.Mention))
	rep := touch(t, f, "C1 0001")
	if rep.Links != 1 || rep.LinksDropped != 2 {
		t.Fatalf("report = %+v", rep)
	}
	if got, _ := links.From(context.Background(), th("C1 0001")); len(got) != 1 || got[0].To != pull {
		t.Fatalf("links = %+v", got)
	}
}

func TestIngestRefusesLinkFromAnotherUnit(t *testing.T) {
	f, links := linkedFixture()
	f.svc.Post("C1 0001", "0001", "a link")
	f.svc.SetLinks("C1 0001", threadLink(pull, th("C1 0001"), link.Mention))
	if _, err := ingest.Touch(context.Background(), f.reg, []string{"C1 0001"}, t0); err == nil {
		t.Fatal("a link from a unit of another provider was accepted")
	}
	if got, _ := links.To(context.Background(), []unit.Unit{th("C1 0001")}); len(got) != 0 {
		t.Fatalf("links = %+v", got)
	}
}

func TestIngestStillRefusesForeignUnits(t *testing.T) {
	f, links := linkedFixture()
	f.svc.Post("C1 0001", "0001", "first ask")
	f.svc.SetLinks("C1 0001", threadLink(th("C1 0001"), pull, link.Review))
	f.reg.Source = foreign{f.src}
	if _, err := ingest.Run(context.Background(), f.reg, t0); err == nil {
		t.Fatal("a github unit from the thread source was saved")
	}
	if f.log.Gen() != 0 {
		t.Fatal("a refused pass noted the log")
	}
	if got, _ := links.To(context.Background(), []unit.Unit{pull}); len(got) != 0 {
		t.Fatalf("links = %+v", got)
	}
}

func TestReplaceFactsKeepsOtherOrigins(t *testing.T) {
	f, links := linkedFixture()
	ctx := context.Background()
	f.svc.Post("C1 0001", "0001", "please review acme/api#527")
	f.svc.SetLinks("C1 0001", threadLink(th("C1 0001"), pull, link.Review))
	touch(t, f, "C1 0001")
	extracted := threadLink(th("C1 0001"), pull, link.Merge)
	extracted.Origin = link.Extraction
	if err := links.Put(ctx, extracted); err != nil {
		t.Fatal(err)
	}
	f.svc.Post("C1 0001", "0002", "never mind")
	f.svc.SetLinks("C1 0001")
	rep := touch(t, f, "C1 0001")
	if rep.Links != 0 {
		t.Fatalf("report = %+v", rep)
	}
	got, _ := links.From(ctx, th("C1 0001"))
	if len(got) != 1 || got[0].Origin != link.Extraction {
		t.Fatalf("links = %+v", got)
	}
}

func TestIngestWithoutLinkStoreDropsLinks(t *testing.T) {
	f := newFixture(100)
	f.svc.Post("C1 0001", "0001", "please review acme/api#527")
	f.svc.SetLinks("C1 0001", threadLink(th("C1 0001"), pull, link.Review))
	if rep := touch(t, f, "C1 0001"); rep.Links != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if c := f.log.Since(0, port.TopicLinks); !c.Empty() {
		t.Fatalf("noted %+v", c)
	}
}
