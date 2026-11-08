package extract

import (
	"errors"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/slack/host"
)

func TestARunSaysWhenTheNewestChangeGoesQuiet(t *testing.T) {
	r := newRig(t)
	if r.run(jT0); !r.job.Next().IsZero() {
		t.Fatalf("a run with nothing pending asks for a run at %v", r.job.Next())
	}
	r.facts.post(jChan, ts(1), "", jOther, "first", jT0)
	r.facts.post(jChan, ts(2), ts(1), jOther, "second", jT0.Add(30*time.Second))
	r.facts.post(jChan, ts(3), ts(1), jOther, "third", jT0.Add(60*time.Second))
	r.run(jT0.Add(61 * time.Second))
	want := jT0.Add(60*time.Second + 90*time.Second)
	if got := r.job.Next(); !got.Equal(want) {
		t.Fatalf("next %v, want the newest change plus the quiet time, %v", got, want)
	}
	r.run(want)
	if n := r.asker.calls(); n != 1 {
		t.Fatalf("the burst asked %d calls, want one", n)
	}
	if got := r.job.Next(); !got.IsZero() {
		t.Fatalf("next %v after the burst was read, want none", got)
	}
}

func TestACappedRunWaitsForTheCapToFree(t *testing.T) {
	r := newRig(t)
	r.cfg.Slack.Extraction.PerDay, r.cfg.Slack.Extraction.PerDayUSD = 2, 100
	postUnits(r, 4)
	r.run(jT0.Add(2 * time.Minute))
	l := jT0.Local()
	midnight := time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, time.Local)
	if got := r.job.Next(); !got.Equal(midnight) {
		t.Fatalf("next %v with units waiting on a spent cap, want when it frees, %v", got, midnight)
	}
}

func TestAnOpenBreakerWaitsForItsTrial(t *testing.T) {
	r := newRig(t)
	r.asker.reply = func(int, host.Ask) ([]byte, error) { return nil, errors.New("agent down") }
	postUnits(r, 6)
	r.run(jT0.Add(2 * time.Minute))
	if got, want := r.job.Next(), jT0.Add(2*time.Minute+BreakerCool); !got.Equal(want) {
		t.Fatalf("next %v with the breaker open, want its trial at %v", got, want)
	}
}

func TestAFailedTargetWaitsForItsRetry(t *testing.T) {
	r := newRig(t)
	r.asker.reply = func(int, host.Ask) ([]byte, error) { return nil, errors.New("agent down") }
	postUnits(r, 1)
	r.run(jT0.Add(2 * time.Minute))
	if got, want := r.job.Next(), jT0.Add(2*time.Minute+90*time.Second); !got.Equal(want) {
		t.Fatalf("next %v after a failed call, want its retry at %v", got, want)
	}
}
