package cli

import (
	"fmt"
	"sync"

	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/profile"
)

// wireProse wires the partials and prompts the profile layers resolve.
func (e *Env) wireProse(w *serveWiring) {
	if svc, ok := w.Deps.Headless.(*headless.Service); ok {
		svc.Wrapper = e.headlessWrapper(w.Log)
	}
}

// headlessWrapper reads the headless wrapper through the profile's layers
// at each call; a user's wrapper that does not parse is logged once and the
// bundled one is used.
func (e *Env) headlessWrapper(logf func(string, ...any)) func() (headless.Wrapper, error) {
	var mu sync.Mutex
	var logged string
	return func() (headless.Wrapper, error) {
		layers := e.intentState().profileLayers(e)
		w, err := parseWrapper(layers)
		if err == nil {
			return w, nil
		}
		mu.Lock()
		if err.Error() != logged {
			logged = err.Error()
			if logf != nil {
				logf("headless: %v; the bundled wrapper is used", err)
			}
		}
		mu.Unlock()
		return parseWrapper([]profile.Layer{{Name: "bundled", Source: "embedded"}})
	}
}

func parseWrapper(layers []profile.Layer) (headless.Wrapper, error) {
	r, err := profile.Resolve(layers, headless.WrapperFile)
	if err != nil {
		return headless.Wrapper{}, err
	}
	w, err := headless.ParseWrapper(string(r.Body))
	if err != nil {
		return headless.Wrapper{}, fmt.Errorf("%s: %w", r.Path, err)
	}
	return w, nil
}
