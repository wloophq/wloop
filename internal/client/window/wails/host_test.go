//go:build darwin && purego

package wails

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/client/window"
)

var _ window.Host = (*host)(nil)

// A link that launched the app arrives before the controller listens: it is
// kept and handed over once a handler is bound, in order.
func TestALinkBeforeListenIsHandedOverOnListen(t *testing.T) {
	h := &host{}
	h.openURL("wloop://open/#task=t-1")
	h.openURL("wloop://open/#task=t-2")
	var got []string
	if err := h.Listen(window.Handlers{OnOpenURL: func(l string) { got = append(got, l) }}); err != nil {
		t.Fatal(err)
	}
	h.openURL("wloop://open/#task=t-3")
	if want := []string{"wloop://open/#task=t-1", "wloop://open/#task=t-2", "wloop://open/#task=t-3"}; !slices.Equal(got, want) {
		t.Fatalf("handed over %q, want %q", got, want)
	}
}
