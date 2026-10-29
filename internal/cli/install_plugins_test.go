package cli

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/plugins/host"
)

func acmeManifest(id string, protocol int) string {
	return fmt.Sprintf("id = %q\nkinds = [\"source\"]\nprotocol = %d\n[source]\nname = \"Acme\"\nslot = %q\ncodec = {keys = [\"{name}\"]}\n", id, protocol, id)
}

// releaseWith writes a release: the binary and, under plugins/<id>/, each
// plugin's manifest, profile layer, binary and the checksum shipped beside
// it, a wrong one for the ids in wrongSum; it returns a link to the binary,
// as Homebrew installs it.
func releaseWith(t *testing.T, ids []string, wrongSum ...string) string {
	t.Helper()
	rel := filepath.Join(t.TempDir(), "release")
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(rel, brand.Name), "#!/bin/sh\n", 0o755)
	for _, id := range ids {
		dir := filepath.Join(rel, "plugins", id)
		bin := "#!/bin/sh\necho " + id + "\n"
		write(filepath.Join(dir, "plugin.toml"), acmeManifest(id, 1), 0o644)
		write(filepath.Join(dir, "profile", "workflows", "w", "workflow.toml"), "# "+id+"\n", 0o644)
		write(filepath.Join(dir, brand.PluginBinary(id)), bin, 0o755)
		sum := sha256.Sum256([]byte(bin))
		if strings.Contains(strings.Join(wrongSum, ","), id) {
			sum = sha256.Sum256([]byte("another build"))
		}
		write(filepath.Join(dir, brand.PluginBinary(id)+".sha256"), fmt.Sprintf("%x  %s\n", sum, brand.PluginBinary(id)), 0o644)
	}
	link := filepath.Join(t.TempDir(), "bin", brand.Name)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(rel, brand.Name), link); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestInstallPlacesTheBundledPluginsWithChecksums(t *testing.T) {
	t.Parallel()
	te := fakeKindEnv(t, fakeKind{}, nil)
	home := filepath.Join(t.TempDir(), "home")
	mkdir(t, home)
	stale := filepath.Join(host.InstalledDir(te.StateDir), "gone")
	mkdir(t, stale)
	te.Executable = releaseWith(t, []string{"acme", "beta"})
	if code := te.run("install", "--home", home); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.out.String(), "placed plugin acme") || !strings.Contains(te.out.String(), "placed plugin beta") {
		t.Errorf("install did not say what it placed:\n%s", te.out)
	}
	sums, err := host.ReadChecksums(host.ChecksumsFile(te.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	found, err := host.Discover(host.Roots(filepath.Join(t.TempDir(), "config"), te.StateDir, "")...)
	if err != nil || len(found) != 2 {
		t.Fatalf("discover %v, %v", found, err)
	}
	for _, p := range found {
		want := sha256.Sum256([]byte("#!/bin/sh\necho " + p.ID() + "\n"))
		sum, why, err := host.Trusted(p, "", sums)
		if err != nil || string(sum) != string(want[:]) || why != "trusted by its recorded checksum" {
			t.Errorf("%s: %x %q %v, want its recorded checksum %x", p.ID(), sum, why, err, want)
		}
		if fi, err := os.Stat(p.Binary); err != nil || fi.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s's placed binary: %v %v", p.ID(), fi, err)
		}
		if _, err := os.Stat(filepath.Join(p.Dir, "profile", "workflows", "w", "workflow.toml")); err != nil {
			t.Errorf("%s's profile layer was not placed: %v", p.ID(), err)
		}
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("install kept a placed plugin the release no longer ships")
	}

	te = fakeKindEnv(t, fakeKind{}, nil)
	te.Executable = releaseWith(t, []string{"acme"}, "acme")
	if code := te.run("install", "--home", home); code == 0 || !strings.Contains(te.err.String(), "does not match the checksum its release shipped") {
		t.Errorf("a binary that is not the release's: exit %d %s", code, te.err)
	}
	if _, err := os.Stat(host.ChecksumsFile(te.StateDir)); err == nil {
		t.Error("install recorded the checksum of a binary that is not the release's")
	}
}

// recordedPlugins are ps with a checksum recorded under stateDir for each,
// as install records them, which a test's launcher does not check.
func recordedPlugins(t *testing.T, stateDir string, ps ...host.Plugin) []host.Plugin {
	t.Helper()
	if stateDir == "" {
		t.Fatal("recording plugin checksums needs a state directory")
	}
	sums := host.Checksums{}
	for _, p := range ps {
		sum := sha256.Sum256([]byte(p.ID()))
		sums[p.ID()] = sum[:]
	}
	if err := writeChecksums(host.ChecksumsFile(stateDir), sums); err != nil {
		t.Fatal(err)
	}
	return ps
}
