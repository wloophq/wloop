package window

import "strings"

// Navigation is how a host moves a webview from current to next: load the
// page when anything before the fragment differs (another daemon port or
// token), or only set its hash; an empty hash on the same page keeps the
// place.
func Navigation(current, next string) (load bool, hash string) {
	curPage, _, _ := strings.Cut(current, "#")
	nextPage, nextHash, _ := strings.Cut(next, "#")
	return curPage != nextPage, nextHash
}
