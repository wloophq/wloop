package host

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// ErrUntrusted is a plugin binary the host does not run.
var ErrUntrusted = errors.New("untrusted plugin binary")

// TrustPath is the [plugins.<id>] trust that runs a binary by its path.
const TrustPath = "path"

// Checksums are the SHA-256 checksums of the bundled plugins' binaries an
// install recorded, by id.
type Checksums map[string][]byte

// ReadChecksums reads path, one "<hex sha256>  <id>" line per binary as
// sha256sum writes them; a missing file records none.
func ReadChecksums(path string) (Checksums, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Checksums{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := Checksums{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		sum, id, ok := strings.Cut(strings.TrimSpace(sc.Text()), "  ")
		if !ok {
			continue
		}
		b, err := hex.DecodeString(sum)
		if err != nil || len(b) != 32 {
			return nil, fmt.Errorf("%s: %q is no SHA-256 of %s", path, sum, id)
		}
		out[strings.TrimSpace(id)] = b
	}
	return out, sc.Err()
}

// Trusted is how p's binary runs: the checksum it must have, nil to run it
// by its path, and why. trust is p's [plugins.<id>] trust. A bundled
// plugin runs only with the checksum its install recorded; one with no
// record, sitting in the bundled directory, is a development build and runs
// by its path. Any other binary runs only with trust = "path", which only
// the user writes.
func Trusted(p Plugin, trust string, recorded Checksums) (checksum []byte, why string, err error) {
	switch {
	case p.Binary == "":
		return nil, "", fmt.Errorf("plugin %s: %s holds no binary", p.ID(), p.Dir)
	case trust == TrustPath:
		return nil, "trusted by its path", nil
	case p.Bundled && recorded[p.ID()] != nil:
		return recorded[p.ID()], "trusted by its recorded checksum", nil
	case p.Bundled:
		return nil, "dev build, no checksum", nil
	}
	return nil, "", fmt.Errorf("%w: plugin %s runs %s, which no install recorded; set [plugins.%s] trust = %q in the configuration to run it",
		ErrUntrusted, p.ID(), p.Binary, p.ID(), TrustPath)
}
