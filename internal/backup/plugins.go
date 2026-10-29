package backup

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// pluginStoreFiles are the files of a plugin's store in its state
// directory: the database with its write-ahead log, and an export it has
// not imported yet. The shared-memory file is rebuilt from the log.
var pluginStoreFiles = []string{"plugin.db", "plugin.db-wal", "import.db"}

// PluginsOf is the directory beside the store backup at backup that holds
// the plugin stores taken with it.
func PluginsOf(backup string) string { return backup + ".plugins" }

// PluginStores copies the store of each plugin under <stateDir>/plugins
// into PluginsOf(backup). The plugins must not be running: the copy is file
// by file.
func PluginStores(stateDir, backup string) error {
	ids, err := pluginDirs(filepath.Join(stateDir, "plugins"))
	if err != nil {
		return err
	}
	for _, id := range ids {
		for _, name := range pluginStoreFiles {
			err := copyFile(filepath.Join(stateDir, "plugins", id, name), filepath.Join(PluginsOf(backup), id, name))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// RestorePluginStores puts back the plugin stores taken with the store
// backup at backup, removing every store under <stateDir>/plugins that the
// backup did not hold: a plugin's store follows the core store it sits
// beside. The plugins must not be running.
func RestorePluginStores(backup, stateDir string) error {
	root := filepath.Join(stateDir, "plugins")
	ids, err := pluginDirs(root)
	if err != nil {
		return err
	}
	for _, id := range ids {
		for _, name := range append([]string{"plugin.db-shm"}, pluginStoreFiles...) {
			if err := os.Remove(filepath.Join(root, id, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	kept, err := pluginDirs(PluginsOf(backup))
	if err != nil {
		return err
	}
	for _, id := range kept {
		for _, name := range pluginStoreFiles {
			err := copyFile(filepath.Join(PluginsOf(backup), id, name), filepath.Join(root, id, name))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// pluginDirs are the plugin ids with a directory under root; none when root
// is absent.
func pluginDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// copyFile copies src to dst at 0600, through a temporary name so an
// interrupted copy never takes dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err = errors.Join(err, out.Sync(), out.Close()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
