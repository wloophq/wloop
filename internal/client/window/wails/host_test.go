//go:build darwin && purego

package wails

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/window"
)

var _ window.Host = (*host)(nil)

// A link that launched the app arrives before the controller listens: it is
// kept and handed over once a handler is bound, in order.
func TestALinkBeforeListenIsHandedOverOnListen(t *testing.T) {
	links := []string{brand.URLScheme() + "://open/#task=t-1", brand.URLScheme() + "://open/#task=t-2", brand.URLScheme() + "://open/#task=t-3"}
	h := &host{}
	h.openURL(links[0])
	h.openURL(links[1])
	var got []string
	if err := h.Listen(window.Handlers{OnOpenURL: func(l string) { got = append(got, l) }}); err != nil {
		t.Fatal(err)
	}
	h.openURL(links[2])
	if !slices.Equal(got, links) {
		t.Fatalf("handed over %q, want %q", got, links)
	}
}

// The panel opens in the middle of the focused screen's visible area, which
// on a second screen starts away from the origin.
func TestThePanelCentersInTheFocusedScreen(t *testing.T) {
	right := rect{Origin: point{X: 1512, Y: -200}, Size: size{Width: 2560, Height: 1415}}
	if got, want := centeredIn(right, size{Width: 680, Height: 420}), (point{X: 1512 + 940, Y: -200 + 497.5}); got != want {
		t.Fatalf("origin %+v, want %+v", got, want)
	}
}
