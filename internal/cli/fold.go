package cli

import (
	"slices"

	intentload "github.com/wloophq/wloop/internal/intent"
)

// verbsHandledBy are the profile's verbs whose handler is handler, as it loads now, sorted.
func verbsHandledBy(loaded func() *intentload.Loaded, handler string) func() []string {
	return func() []string {
		var out []string
		for name, v := range loaded().Tasks.Verbs {
			if v.Handler == handler {
				out = append(out, name)
			}
		}
		slices.Sort(out)
		return out
	}
}
