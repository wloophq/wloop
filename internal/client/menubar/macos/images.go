package macos

import (
	"embed"
	"os"

	"github.com/wloophq/wloop/internal/client/menubar"
)

//go:embed img/*.png
var images embed.FS

// imageFile writes a fresh copy of the image of a kind under dir and returns
// its path: macOS moves an attachment's file into its own store, so every
// post needs its own copy. "" with no error when the kind has no image.
func imageFile(dir string, img menubar.Image) (string, error) {
	if img == "" {
		return "", nil
	}
	raw, err := images.ReadFile("img/" + string(img) + ".png")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, string(img)+"-*.png")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
