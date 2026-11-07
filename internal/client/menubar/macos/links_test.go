//go:build darwin

package macos

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/client/menubar"
)

var _ menubar.LinkSource = linkTray{}

// A link that launched the app arrives before the menu bar takes links: it
// is kept and handed over, in order, once it does.
func TestALinkBeforeTheMenuBarTakesLinksIsHandedOver(t *testing.T) {
	l := &links{}
	l.receive("a")
	l.receive("b")
	got := make(chan string, 3)
	l.take(func(link string) { got <- link })
	l.receive("c")
	var all []string
	for range 3 {
		all = append(all, <-got)
	}
	if want := []string{"a", "b", "c"}; !slices.Equal(all, want) {
		t.Fatalf("handed over %q, want %q", all, want)
	}
}
