// Package backup keeps a copy of a file the loop is about to change.
package backup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// File writes raw to <path>.loop-<stamp>.bak, or the first free name
// after it, and returns the name.
func File(path string, raw []byte, stamp string) (string, error) {
	for i := 1; ; i++ {
		dest := path + ".loop-" + stamp + ".bak"
		if i > 1 {
			dest = fmt.Sprintf("%s.loop-%s-%d.bak", path, stamp, i)
		}
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = f.Write(raw)
		return dest, errors.Join(err, f.Close())
	}
}
