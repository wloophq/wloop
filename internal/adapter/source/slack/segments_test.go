package slack

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	segTeam = "T000AAA"
	segDM   = "D000AAA"
)

type segClock struct{ now time.Time }

func (c segClock) Now() time.Time { return c.now }

func openSegments(t *testing.T) (Segments, *change.Log) {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "segments.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return Segments{Store: db.Segments(), Clock: segClock{time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}}, log
}

func apply(t *testing.T, s Segments, spans ...dslack.Span) []unit.Unit {
	t.Helper()
	us, err := s.Apply(context.Background(), segTeam, segDM, spans)
	if err != nil {
		t.Fatal(err)
	}
	return us
}

func segID(t *testing.T, u unit.Unit) string {
	t.Helper()
	k, err := dslack.ParseUnit(u)
	if err != nil || k.Segment == "" {
		t.Fatalf("%s is not a segment unit: %v", u, err)
	}
	return k.Segment
}

func wantRange(t *testing.T, s Segments, u unit.Unit, from, to string) {
	t.Helper()
	conv, gotFrom, gotTo, err := s.Range(context.Background(), u)
	if err != nil || conv != segDM || gotFrom != from || gotTo != to {
		t.Errorf("range of %s: %s [%s, %s] %v, want %s [%s, %s]", u, conv, gotFrom, gotTo, err, segDM, from, to)
	}
}

func wantUnits(t *testing.T, s Segments, ts string, want ...unit.Unit) {
	t.Helper()
	got, err := s.UnitsFor(context.Background(), segDM, ts)
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("units for %s: %v %v, want %v", ts, got, err, want)
	}
}

func TestApplyMintsStableIDs(t *testing.T) {
	s, _ := openSegments(t)
	us := apply(t, s, dslack.Span{FromTS: "1792511726.457859", ToTS: "1792511800.000100"}, dslack.Span{FromTS: "1792511900.000200"})
	if len(us) != 2 || segID(t, us[0]) != "1792511726457859" || segID(t, us[1]) != "1792511900000200" {
		t.Fatalf("minted %v", us)
	}
	for _, u := range us {
		if err := dslack.Codec().Valid(u.Key); err != nil || u.Provider != dslack.Provider {
			t.Errorf("%s: %v", u, err)
		}
	}
	again := apply(t, s, dslack.Span{ID: segID(t, us[0]), FromTS: "1792511726.457859", ToTS: "1792511800.000100"},
		dslack.Span{ID: segID(t, us[1]), FromTS: "1792511900.000200"})
	if !slices.Equal(again, us) {
		t.Errorf("a re-apply returned %v, want %v", again, us)
	}
}

func TestResegmentationMovesRanges(t *testing.T) {
	s, _ := openSegments(t)
	us := apply(t, s, dslack.Span{FromTS: "1000000000.000100", ToTS: "1000000000.000500"}, dslack.Span{FromTS: "1000000000.000600"})
	moved := apply(t, s, dslack.Span{ID: segID(t, us[0]), FromTS: "1000000000.000100", ToTS: "1000000000.000300"},
		dslack.Span{ID: segID(t, us[1]), FromTS: "1000000000.000400"})
	if !slices.Equal(moved, us) {
		t.Fatalf("ids changed: %v, want %v", moved, us)
	}
	wantRange(t, s, us[0], "1000000000.000100", "1000000000.000300")
	wantRange(t, s, us[1], "1000000000.000400", "")
	wantUnits(t, s, "1000000000.000450", us[1])
}

func TestMergeKeepsOneID(t *testing.T) {
	s, _ := openSegments(t)
	us := apply(t, s, dslack.Span{FromTS: "1000000000.000100", ToTS: "1000000000.000500"}, dslack.Span{FromTS: "1000000000.000600"})
	merged := apply(t, s, dslack.Span{ID: segID(t, us[0]), FromTS: "1000000000.000100"})
	if !slices.Equal(merged, us[:1]) {
		t.Fatalf("merge returned %v", merged)
	}
	wantRange(t, s, us[0], "1000000000.000100", "")
	wantRange(t, s, us[1], "1000000000.000600", "")
	wantUnits(t, s, "1000000000.000700", us[0])
	if _, err := s.Apply(context.Background(), segTeam, segDM, []dslack.Span{{ID: segID(t, us[0]), FromTS: "1000000000.000100", ToTS: "1000000000.000500"},
		{ID: segID(t, us[1]), FromTS: "1000000000.000600"}}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("a closed id was reused: %v", err)
	}
	split := apply(t, s, dslack.Span{ID: segID(t, us[0]), FromTS: "1000000000.000100", ToTS: "1000000000.000500"},
		dslack.Span{FromTS: "1000000000.000600"})
	if split[1] == us[1] || !strings.HasPrefix(segID(t, split[1]), segID(t, us[1])) {
		t.Errorf("the split minted %s again, or an unrelated id: %v", us[1], split)
	}
}

func TestRelabelledSpanKeepsID(t *testing.T) {
	s, log := openSegments(t)
	us := apply(t, s, dslack.Span{FromTS: "1000000000.000100", ToTS: "1000000000.000500"}, dslack.Span{FromTS: "1000000000.000600"})
	from := log.Gen()
	again := apply(t, s, dslack.Span{ID: segID(t, us[0]), FromTS: "1000000000.000100", ToTS: "1000000000.000500"},
		dslack.Span{FromTS: "1000000000.000600"})
	if !slices.Equal(again, us) {
		t.Errorf("a relabelled span returned %v, want %v", again, us)
	}
	if c := log.Since(from, port.TopicSourceEvents); !c.Empty() {
		t.Errorf("a relabelled span noted %+v", c)
	}
	first := apply(t, s, dslack.Span{FromTS: "1000000000.000100", ToTS: "1000000000.000500"}, dslack.Span{FromTS: "1000000000.000600"})
	if !slices.Equal(first, us) {
		t.Errorf("relabelling from the first span returned %v, want %v", first, us)
	}
	wantUnits(t, s, "1000000000.000700", us[1])
}

func TestApplyNotesChangedUnits(t *testing.T) {
	s, log := openSegments(t)
	us := apply(t, s, dslack.Span{FromTS: "1000000000.000100", ToTS: "1000000000.000200"},
		dslack.Span{FromTS: "1000000000.000300", ToTS: "1000000000.000500"}, dslack.Span{FromTS: "1000000000.000600"})
	from := log.Gen()
	apply(t, s, dslack.Span{ID: segID(t, us[0]), FromTS: "1000000000.000100", ToTS: "1000000000.000200"},
		dslack.Span{ID: segID(t, us[1]), FromTS: "1000000000.000300", ToTS: "1000000000.000400"},
		dslack.Span{ID: segID(t, us[2]), FromTS: "1000000000.000450"})
	c := log.Since(from, port.TopicSourceEvents)
	if c.All || !slices.Equal(c.Units, us[1:]) {
		t.Errorf("noted %+v, want %v", c, us[1:])
	}
	from = log.Gen()
	apply(t, s, dslack.Span{ID: segID(t, us[0]), FromTS: "1000000000.000100", ToTS: "1000000000.000200"},
		dslack.Span{ID: segID(t, us[1]), FromTS: "1000000000.000300", ToTS: "1000000000.000400"},
		dslack.Span{ID: segID(t, us[2]), FromTS: "1000000000.000450"})
	if c := log.Since(from, port.TopicSourceEvents); !c.Empty() {
		t.Errorf("an unchanged apply noted %+v", c)
	}
	from = log.Gen()
	apply(t, s, dslack.Span{ID: segID(t, us[0]), FromTS: "1000000000.000100", ToTS: "1000000000.000200"},
		dslack.Span{ID: segID(t, us[1]), FromTS: "1000000000.000300"})
	if c := log.Since(from, port.TopicSourceEvents); !slices.Equal(c.Units, us[1:]) {
		t.Errorf("a merge noted %+v, want the kept and the closed unit %v", c, us[1:])
	}
}

func TestUnitsForOpenSpan(t *testing.T) {
	s, _ := openSegments(t)
	us := apply(t, s, dslack.Span{FromTS: "1000000000.000100", ToTS: "1000000000.000200"}, dslack.Span{FromTS: "1000000000.000300"})
	wantUnits(t, s, "1000000009.000000", us[1])
	wantUnits(t, s, "1000000000.000200", us[0])
	wantUnits(t, s, "1000000000.000250")
	wantRange(t, s, us[1], "1000000000.000300", "")
}

func TestUnitsForFollowsTheParentSegment(t *testing.T) {
	s, _ := openSegments(t)
	us := apply(t, s, dslack.Span{FromTS: "1000000000.000100", ToTS: "1000000000.000200"}, dslack.Span{FromTS: "1000000000.000300"})
	reply := dslack.Message{TS: "1000000000.000900", ThreadTS: "1000000000.000150"}
	wantUnits(t, s, reply.ThreadTS, us[0])
}

func TestUnsegmentedIsEmpty(t *testing.T) {
	s, _ := openSegments(t)
	got, err := s.UnitsFor(context.Background(), "D000BBB", "1000000000.000100")
	if err != nil || len(got) != 0 {
		t.Errorf("%v %v", got, err)
	}
}

func TestRangeRefusesForeignUnits(t *testing.T) {
	s, _ := openSegments(t)
	for _, u := range []unit.Unit{
		{Provider: unit.GitHub, Key: "acme/api#7"},
		{Provider: dslack.Provider, Key: "nonsense"},
		{Provider: dslack.Provider, Key: segTeam + "/" + segDM + "/1000000000.000100"},
		{Provider: dslack.Provider, Key: segTeam + "/" + segDM + "/seg/404"},
	} {
		_, _, _, err := s.Range(context.Background(), u)
		if err == nil || !strings.Contains(err.Error(), u.Key) {
			t.Errorf("%s: %v", u, err)
		}
	}
	us := apply(t, s, dslack.Span{FromTS: "1000000000.000100"})
	other := unit.Unit{Provider: dslack.Provider, Key: "T000BBB/" + segDM + "/seg/" + segID(t, us[0])}
	if _, _, _, err := s.Range(context.Background(), other); !errors.Is(err, dslack.ErrUnknownUnit) || !strings.Contains(err.Error(), other.Key) {
		t.Errorf("a segment of another team: %v", err)
	}
	_, _, _, err := s.Range(context.Background(), unit.Unit{Provider: dslack.Provider, Key: segTeam + "/" + segDM + "/seg/404"})
	if !errors.Is(err, dslack.ErrUnknownUnit) {
		t.Errorf("an unknown segment: %v, want ErrUnknownUnit", err)
	}
}

func TestApplyRefusesBadSpans(t *testing.T) {
	s, _ := openSegments(t)
	for name, spans := range map[string][]dslack.Span{
		"none":                  nil,
		"a bad ts":              {{FromTS: "yesterday"}},
		"open not last":         {{FromTS: "1000000000.000100"}, {FromTS: "1000000000.000300"}},
		"overlapping":           {{FromTS: "1000000000.000100", ToTS: "1000000000.000400"}, {FromTS: "1000000000.000300"}},
		"an unknown id":         {{ID: "404", FromTS: "1000000000.000100"}},
		"a malformed id":        {{ID: "a/b", FromTS: "1000000000.000100"}},
		"ends before it starts": {{FromTS: "1000000000.000300", ToTS: "1000000000.000100"}},
		"a ts of another width": {{FromTS: "1000000000.000100", ToTS: "1000000000.000500"}, {FromTS: "999999999.000001"}},
		"a short fraction":      {{FromTS: "1000000000.1"}},
	} {
		if _, err := s.Apply(context.Background(), segTeam, segDM, spans); err == nil {
			t.Errorf("%s: applied", name)
		}
	}
	if _, err := s.Apply(context.Background(), "acme", segDM, []dslack.Span{{FromTS: "1000000000.000100"}}); err == nil {
		t.Error("a malformed team applied")
	}
	us := apply(t, s, dslack.Span{FromTS: "1000000000.000100"})
	dup := []dslack.Span{{ID: segID(t, us[0]), FromTS: "1000000000.000100", ToTS: "1000000000.000200"}, {ID: segID(t, us[0]), FromTS: "1000000000.000300"}}
	if _, err := s.Apply(context.Background(), segTeam, segDM, dup); err == nil {
		t.Error("a duplicate id applied")
	}
	if _, err := s.UnitsFor(context.Background(), segDM, "1.5"); err == nil {
		t.Error("UnitsFor took a ts of another width")
	}
	if _, err := s.Apply(context.Background(), segTeam, segDM, []dslack.Span{{FromTS: "1000000000.000300"}}); err == nil {
		t.Error("an open segment reaching into the applied range was left unnamed")
	}
}
