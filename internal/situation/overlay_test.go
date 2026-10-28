package situation

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/readmodel/overlay"
)

// TestTheHostOverlaysDecideAsDeriveDid composes the host's overlays over
// each source situation as a reported row does (the run overlay but on a
// done unit, the mute, then the pending round) and compares them with
// testdata/overlays.golden, which Derive and prrow's pending round wrote
// before the overlays left them. The run overlay now wins over blocked,
// held and to_address, which Derive put before the turn: those rows must
// differ from the golden.
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
	cases, diverged := 0, 0
	for _, s := range gridSources() {
		src := overlayOf(Derive(Input{PR: s.pr, Events: s.evs, Viewer: "me", MyTeams: []string{"Acme/search"}, Ignore: []string{"sonar"},
			Now: s.now, Stalled: th}))
		for _, gt := range gridTurns() {
			for _, waiting := range []bool{false, true} {
				for _, o := range gridMutes() {
					for _, p := range gridPendings() {
						cases++
						sit, ran := src, false
						if gt != nil && sit.Kind != overlay.KindDone {
							tf := overlay.Turn{Intent: "review", Role: gt.role, State: gt.state, Summary: gt.summary, At: h(2)}
							if gt.answered {
								tf.AnsweredAt = h(3)
							}
							if r, ok := tf.Situation(waiting); ok {
								sit, ran = r, true
							}
						}
						if m := (overlay.Mute{Until: o.MutedUntil, UntilStateChange: o.MuteUntilStateChange, Kind: o.MutedKind,
							WaitingOn: o.MutedWaitingOn}); m.Holds(sit, s.now) {
							sit = m.Muted()
						}
						if p != nil {
							sit = overlay.Pending{Intent: p.Intent, Reason: p.Reason}.Over(sit)
						}
						key := gridCase(s, gt, waiting, o, p)
						want, ok := golden[key]
						if !ok {
							t.Fatalf("the golden has no %s", key)
						}
						got := gridResult(sit.Kind, sit.WaitingOn, sit.Since, sit.Reason, sit.Stalled)
						switch reorders := ran && (src.Kind == string(KindBlocked) || src.Kind == string(KindHeldByCap) || src.Kind == string(KindToAddress)); {
						case reorders && !overlaid(sit.Kind):
							t.Errorf("%s: the run overlay no longer wins: %s", key, got)
						case reorders:
							if got != want {
								diverged++
							}
						case got != want:
							t.Errorf("%s:\n got %s\nwant %s", key, got, want)
						}
					}
				}
			}
		}
	}
	if cases != len(golden) || diverged == 0 {
		t.Errorf("%d cases for %d golden rows, %d reordered", cases, len(golden), diverged)
	}
}

func overlaid(kind string) bool {
	switch kind {
	case overlay.KindLoopWorking, overlay.KindLoopWaiting, overlay.KindLoopStopped, overlay.KindMuted, overlay.KindRoundPending:
		return true
	}
	return false
}
