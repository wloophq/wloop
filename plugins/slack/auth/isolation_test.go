package auth

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTestsAvoidRealPortAndKeychain(t *testing.T) {
	forbidden := []string{"5368" + "2", "/usr/bin/" + "security"}
	var scanned int
	for _, root := range []string{"..", filepath.Join("..", "..", "..", "internal", "cli")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			scanned++
			for _, f := range forbidden {
				if strings.Contains(string(b), f) {
					t.Errorf("%s names %s", path, f)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 5 {
		t.Fatalf("scanned only %d test files", scanned)
	}
}
