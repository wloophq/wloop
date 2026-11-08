package slack

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/config"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/host/hosttest"
)

func TestALandingAsksForTheExtractionWhenItsQuietTimeEnds(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.now = tsAt(1)
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: chan1, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads:       []dslack.Thread{{Conversation: chan1, TS: ts(1), Messages: []dslack.Message{{TS: ts(1), User: other, Text: "review please"}}}},
	})
	f.p.wake.flush(context.Background())
	want := []hosttest.JobWake{{Job: JobExtract, At: tsAt(1).Add(90 * time.Second)}}
	if got := f.host.Wakes(); !slices.Equal(got, want) {
		t.Fatalf("wakes %+v, want the extraction at the landing plus its quiet time", got)
	}
	f.now = tsAt(1).Add(30 * time.Second)
	f.seed(t, dslack.Facts{
		Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), Messages: []dslack.Message{{TS: ts(1), User: other, Text: "review please"},
			{TS: ts(2), ThreadTS: ts(1), User: other, Text: "and soon"}}}},
	})
	f.p.wake.flush(context.Background())
	if got := f.host.Wakes(); !slices.Equal(got, want) {
		t.Fatalf("wakes %+v: a landing inside a wake the host holds asked again", got)
	}
}

func TestAWakeTheHostCannotTakeYetIsKeptForTheNextLanding(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.host.RefuseWakes(fmt.Errorf("%w: the host runs no job extract:slack", src.ErrNotFound))
	f.now = tsAt(1)
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: chan1, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads:       []dslack.Thread{{Conversation: chan1, TS: ts(1), Messages: []dslack.Message{{TS: ts(1), User: other, Text: "review please"}}}},
	})
	f.p.wake.flush(context.Background())
	if strings.Contains(f.logs.String(), "wake") {
		t.Fatalf("a wake before the daemon serves logged: %s", f.logs)
	}
	f.host.RefuseWakes(nil)
	f.now = tsAt(1).Add(30 * time.Second)
	f.seed(t, dslack.Facts{
		Threads: []dslack.Thread{{Conversation: chan1, TS: ts(1), Messages: []dslack.Message{{TS: ts(1), User: other, Text: "review please"},
			{TS: ts(2), ThreadTS: ts(1), User: other, Text: "and soon"}}}},
	})
	f.p.wake.flush(context.Background())
	want := []hosttest.JobWake{{Job: JobExtract, At: tsAt(1).Add(90 * time.Second)}}
	if got := f.host.Wakes(); !slices.Equal(got, want) {
		t.Fatalf("wakes %+v, want the first landing's, kept while the host could not take it", got)
	}
}

func TestALandingReadsTheQuietTimeInForce(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.p.all.Slack.Extraction.Quiet = config.Days(3 * time.Minute)
	f.now = tsAt(1)
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: chan1, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads:       []dslack.Thread{{Conversation: chan1, TS: ts(1), Messages: []dslack.Message{{TS: ts(1), User: other, Text: "review please"}}}},
	})
	f.p.wake.flush(context.Background())
	if got := f.host.Wakes(); len(got) != 1 || !got[0].At.Equal(tsAt(1).Add(3*time.Minute)) {
		t.Fatalf("wakes %+v, want the landing plus the quiet time the extraction reads", got)
	}
}

func TestAStoreWriteWithNoChangeAsksNoWake(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.knownUser(t, other, false)
	f.p.wake.flush(context.Background())
	if got := f.host.Wakes(); len(got) != 0 {
		t.Fatalf("wakes %+v after a write the extraction does not read", got)
	}
}

func TestABriefWantAsksForTheExtractionNow(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.now = tsAt(1)
	f.seed(t, dslack.Facts{
		Conversations: []dslack.Conversation{{ID: chan1, Name: "general", Kind: dslack.KindChannel, IsMember: true}},
		Threads:       []dslack.Thread{{Conversation: chan1, TS: ts(1), Messages: []dslack.Message{{TS: ts(1), User: other, Text: "review please"}}}},
	})
	ctx := context.Background()
	f.now = tsAt(1).Add(10 * time.Second)
	if _, _, err := f.p.job.Run(ctx); err != nil {
		t.Fatal(err)
	}
	f.p.wake.flush(ctx)
	if want, err := (&Plugin{p: f.p}).BriefWant(ctx, dslack.ThreadUnit(team, chan1, ts(1)).Wire()); err != nil || !want {
		t.Fatalf("want %v %v", want, err)
	}
	f.p.wake.flush(ctx)
	got := f.host.Wakes()
	if len(got) != 2 || got[1] != (hosttest.JobWake{Job: JobExtract, At: f.now}) {
		t.Fatalf("wakes %+v, want the extraction now after the want", got)
	}
}
