package macos

import (
	"bytes"
	"image/png"
	"os"
	"testing"

	"github.com/wloophq/wloop/internal/client/menubar"
)

func TestEveryImageKindShipsAPNGAndEachPostGetsItsOwnCopy(t *testing.T) {
	dir := t.TempDir()
	seen := map[string]bool{}
	for _, img := range []menubar.Image{menubar.ImageRound, menubar.ImageHeld, menubar.ImageAsk, menubar.ImageBlocked} {
		for range 2 {
			path, err := imageFile(dir, img)
			if err != nil {
				t.Fatalf("%s: %v", img, err)
			}
			if seen[path] {
				t.Fatalf("%s: %s reused", img, path)
			}
			seen[path] = true
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
				t.Fatalf("%s: %v", img, err)
			}
		}
	}
	if path, err := imageFile(dir, ""); path != "" || err != nil {
		t.Fatalf("no image: %q %v", path, err)
	}
}
