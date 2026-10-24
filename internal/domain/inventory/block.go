package inventory

import "strings"

// ActiveBlock is the newest round_blocked event of evs, oldest first, that
// no later event ended: a round_unblocked, a round sent or held, or a review
// by viewer.
func ActiveBlock(evs []Event, viewer string) (Event, bool) {
	var b Event
	on := false
	for _, e := range evs {
		switch e.Kind {
		case KindRoundBlocked:
			b, on = e, true
		case KindRoundUnblocked, KindRoundSent, KindRoundHeld:
			on = false
		case KindReview:
			if on && viewer != "" && strings.EqualFold(e.Actor, viewer) && e.At.After(b.At) {
				on = false
			}
		}
	}
	return b, on
}
