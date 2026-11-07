package cli

import (
	"fmt"
	"sync"

	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/omnibox"
	"github.com/wloophq/wloop/internal/profile"
)

// wireProse wires the partials and prompts the profile layers resolve.
func (e *Env) wireProse(w *serveWiring) {
	if svc, ok := w.Deps.Headless.(*headless.Service); ok {
		svc.Wrapper = e.headlessWrapper(w.Log)
	}
	if a, ok := w.Deps.OmniboxAsk.(*omnibox.Asker); ok && a != nil {
		a.Wrapper = profilePrompt(e, w.Log, "omnibox", omnibox.WrapperFile, omnibox.ParseWrapper)
	}
}

// headlessWrapper reads the headless wrapper through the profile's layers at each call.
func (e *Env) headlessWrapper(logf func(string, ...any)) func() (headless.Wrapper, error) {
	return profilePrompt(e, logf, "headless", headless.WrapperFile, headless.ParseWrapper)
}

// profilePrompt reads the prompt template file through the profile's layers at each call; a user's file
// that does not parse is logged once and the bundled one is used.
func profilePrompt[T any](e *Env, logf func(string, ...any), name, file string, parse func(string) (T, error)) func() (T, error) {
	var mu sync.Mutex
	var logged string
	read := func(layers []profile.Layer) (T, error) {
		var zero T
		r, err := profile.Resolve(layers, file)
		if err != nil {
			return zero, err
		}
		w, err := parse(string(r.Body))
		if err != nil {
			return zero, fmt.Errorf("%s: %w", r.Path, err)
		}
		return w, nil
	}
	return func() (T, error) {
		w, err := read(e.intentState().profileLayers(e))
		if err == nil {
			return w, nil
		}
		mu.Lock()
		if err.Error() != logged {
			logged = err.Error()
			if logf != nil {
				logf("%s: %v; the bundled wrapper is used", name, err)
			}
		}
		mu.Unlock()
		return read([]profile.Layer{{Name: "bundled", Source: "embedded"}})
	}
}
