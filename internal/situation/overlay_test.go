package situation

import (
	"fmt"
	"testing"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/readmodel/overlay"
)

// TestTheHostOverlaysDecideAsDeriveDid compares the source-free overlays
// with Derive on a pull request whose source situation is plain: the run
// overlay, then the mute.
func TestTheHostOverlaysDecideAsDeriveDid(t *testing.T) {
	var turns []*overlay.Turn
	turns = append(turns, nil)
	for _, role := range []string{builtin.RoleReview, builtin.RoleAddress, "pr.nudge", ""} {
		for _, st := range []string{"asked", "failed", "unrecorded", "started", "ended", "sent"} {
			for _, sum := range []string{"", "which branch?"} {
				turns = append(turns, &overlay.Turn{Intent: "review", Role: role, State: st, Summary: sum, At: h(2)},
					&overlay.Turn{Intent: "review", Role: role, State: st, Summary: sum, At: h(2), AnsweredAt: h(3)})
			}
		}
	}
	mutes := []dinv.Override{{}, {MutedUntil: h(20)}, {MutedUntil: h(5)},
		{MuteUntilStateChange: true, MutedKind: string(KindLoopWaiting), MutedWaitingOn: "me"},
		{MuteUntilStateChange: true, MutedKind: string(KindNoReviewer), MutedWaitingOn: "author"},
		{MuteUntilStateChange: true, MutedKind: string(KindLoopWorking), MutedWaitingOn: "ana"}}
	p := pr("ana")
	src := Derive(Input{PR: p, Viewer: "me", Now: h(10), Stalled: th})
	for _, tf := range turns {
		for _, waiting := range []bool{false, true} {
			for _, o := range mutes {
				name := fmt.Sprintf("%+v waiting %t %+v", tf, waiting, o)
				want := Derive(Input{PR: p, Override: o, Viewer: "me", Now: h(10), Stalled: th, Turn: tf, SessionWaiting: waiting})
				got := overlay.Situation{Kind: string(src.Kind), WaitingOn: src.WaitingOn, Since: src.Since, Reason: src.Reason, Stalled: src.Stalled}
				if tf != nil {
					if s, ok := tf.Situation(waiting); ok {
						got = s
					}
				}
				m := overlay.Mute{Until: o.MutedUntil, UntilStateChange: o.MuteUntilStateChange, Kind: o.MutedKind, WaitingOn: o.MutedWaitingOn}
				if m.Holds(got, h(10)) {
					got = m.Muted()
				}
				if string(want.Kind) != got.Kind || want.WaitingOn != got.WaitingOn || !want.Since.Equal(got.Since) || want.Reason != got.Reason || want.Stalled != got.Stalled {
					t.Errorf("%s: Derive %+v, overlays %+v", name, want, got)
				}
			}
		}
	}
}
