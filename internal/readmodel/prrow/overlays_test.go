package prrow

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
	"github.com/wloophq/wloop/internal/readmodel/prrow/ghreporttest"
	"github.com/wloophq/wloop/internal/situation"
)

// TestTheHostOverlaysDecideAsDeriveDid composes the host's overlays over
// each situation the GitHub plugin reports, derived by the plugin's own
// code, as a GitHub row does (the run overlay but on a done unit, the mute,
// then the pending round) and compares them with testdata/overlays.golden,
// which the host's Derive and prrow's pending round wrote before the
// plugin derived the situation and the overlays left Derive. The run
// overlay now wins over blocked, held and to_address, which Derive put
// before the turn: those rows must differ from the golden.
func TestTheHostOverlaysDecideAsDeriveDid(t *testing.T) {
	golden := map[string]string{}
	f, err := os.Open(filepath.Join("testdata", "overlays.golden"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " => ")
		golden[k] = v
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	cases, diverged, woken := 0, 0, 0
	for _, s := range gridSources() {
		rep, ok, err := ReportOf(ghreporttest.Snapshot(ghreporttest.Env{Viewer: "me", Teams: []string{"Acme/search"}, Ignore: []string{"sonar"},
			Stalled: &gridStalled, Now: s.now}, ghreporttest.Input{PR: s.pr, Events: s.evs}))
		if err != nil || !ok {
			t.Fatalf("%s: report %v %v", s.name, ok, err)
		}
		src := rep.source(s.now)
		for _, gt := range gridTurns() {
			for _, waiting := range []bool{false, true} {
				for _, o := range gridMutes() {
					for _, p := range gridPendings() {
						cases++
						var tf *TurnFact
						if gt != nil {
							tf = &TurnFact{Facts: overlay.Turn{Intent: "review", Role: gt.role, State: gt.state, Summary: gt.summary, At: h(2)}, Waiting: waiting}
							if gt.answered {
								tf.Facts.AnsweredAt = h(3)
							}
						}
						sit, _ := SituationOf(rep, tf, o.mute, nil, s.now)
						_, ranOK := tf.situation(src)
						ran := ranOK
						if p != nil {
							sit = overlay.Pending{Intent: p.Intent, Reason: p.Reason}.Over(sit)
						}
						key := gridCase(s, gt, waiting, o, p)
						want, ok := golden[key]
						if !ok {
							t.Fatalf("the golden has no %s", key)
						}
						got := gridResult(sit.Kind, sit.WaitingOn, sit.Since, sit.Reason, sit.Stalled)
						switch reorders := ran && (src.Kind == string(situation.KindBlocked) || src.Kind == string(situation.KindHeldByCap) || src.Kind == string(situation.KindToAddress)); {
						case reorders && !overlaid(sit.Kind):
							t.Errorf("%s: the run overlay no longer wins: %s", key, got)
						case reorders:
							if got != want {
								diverged++
							}
						case o.mute.UntilStateChange && sit.Kind == overlay.KindMuted && !strings.HasPrefix(want, overlay.KindMuted+" "):
							// The wake of a mute until the state changes decides when it lapses, not the row.
							woken++
						case got != want:
							t.Errorf("%s:\n got %s\nwant %s", key, got, want)
						}
					}
				}
			}
		}
	}
	if cases != len(golden) || diverged == 0 || woken == 0 {
		t.Errorf("%d cases for %d golden rows, %d reordered, %d left to the wake", cases, len(golden), diverged, woken)
	}
}

// gridStalled are the stalled thresholds the golden's sources stall by.
var gridStalled = config.Stalled{Review: config.Days(48 * time.Hour), Reply: config.Days(24 * time.Hour), Merge: config.Days(24 * time.Hour)}

// situation is the run overlay of f over src; false when f is nil, src is
// done or f leaves the situation to the source.
func (f *TurnFact) situation(src overlay.Situation) (overlay.Situation, bool) {
	if f == nil || src.Kind == overlay.KindDone {
		return overlay.Situation{}, false
	}
	return f.Facts.Situation(f.Waiting)
}

func overlaid(kind string) bool {
	switch kind {
	case overlay.KindLoopWorking, overlay.KindLoopWaiting, overlay.KindLoopStopped, overlay.KindMuted, overlay.KindRoundPending:
		return true
	}
	return false
}
