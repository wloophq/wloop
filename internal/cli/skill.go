package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/skills"
)

func skillPathIn(dir, name string) string {
	return filepath.Join(dir, "skills", name, "SKILL.md")
}

// bundledSkills are the skills install writes; the bundled intents always load.
func bundledSkills() []skills.Skill {
	l, err := turn.Bundled()
	if err != nil {
		panic(err)
	}
	return skills.Bundled(l.Registry.All(), intentTarget)
}

func (e *Env) renderSkill(sk skills.Skill) []byte { return sk.Render(e.command(), Version) }

func (e *Env) printSkill(name string) error {
	for _, sk := range bundledSkills() {
		if sk.Name == name {
			_, err := e.Out.Write(e.renderSkill(sk))
			return err
		}
	}
	return fmt.Errorf("no bundled skill named %s", name)
}

type skillState int

const (
	skillAbsent  skillState = iota
	skillLinked             // a symlink to content the loop does not own
	skillCurrent            // as this version renders it
	skillManaged            // as some install wrote it, unedited since
	skillEdited             // written by an install, edited since
	skillForeign            // not written by an install
)

// installedSkill is a bundled skill as one skills directory holds it.
type installedSkill struct {
	state skillState
	path  string // SKILL.md, or the symlink when linked
	mark  skills.Mark
}

func (e *Env) installedSkill(root string, sk skills.Skill) (installedSkill, error) {
	path := filepath.Join(root, sk.Name, "SKILL.md")
	if link := linkedSkill(root, sk.Name); link != "" {
		return installedSkill{state: skillLinked, path: link}, nil
	}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return installedSkill{state: skillAbsent, path: path}, nil
	case err != nil:
		return installedSkill{path: path}, err
	case bytes.Equal(raw, e.renderSkill(sk)):
		return installedSkill{state: skillCurrent, path: path}, nil
	}
	m, ok := skills.ReadMark(raw)
	switch {
	case !ok:
		return installedSkill{state: skillForeign, path: path}, nil
	case !m.Unedited:
		return installedSkill{state: skillEdited, path: path, mark: m}, nil
	}
	return installedSkill{state: skillManaged, path: path, mark: m}, nil
}

// skillKept says why the loop leaves an edited or a foreign skill, and how to compare it.
func (e *Env) skillKept(name string, in installedSkill) string {
	what := "edited since " + brand.Name + " " + in.mark.Version + " installed it"
	if in.state == skillForeign {
		what = brand.Name + " did not write it"
	}
	return fmt.Sprintf("%s; compare with: %s install --print-skill %s | diff %s -", what, e.command(), name, in.path)
}

// skillsRoot is the skills directory of dir with every symlink on the way
// resolved: a dotfiles repository often holds it, and the loop writes there.
func skillsRoot(dir string) string {
	p := filepath.Join(dir, "skills")
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(r, "skills")
	}
	return p
}

// linkedSkill returns the skill's directory or SKILL.md under root when it
// is a symlink, which points at content the loop does not own.
func linkedSkill(root, name string) string {
	for _, p := range []string{filepath.Join(root, name), filepath.Join(root, name, "SKILL.md")} {
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return p
		}
	}
	return ""
}

// installSkills writes every bundled skill into each dir, once per skills
// directory however many homes share it. It replaces a skill an install of
// any version wrote and nobody edited since, and leaves any other.
func (e *Env) installSkills(dirs []string, dryRun bool) error {
	var errs []error
	seen := map[string]bool{}
	for _, dir := range dirs {
		root := skillsRoot(dir)
		if seen[root] {
			continue
		}
		seen[root] = true
		for _, sk := range bundledSkills() {
			if err := e.installSkill(root, sk, dryRun); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (e *Env) installSkill(root string, sk skills.Skill, dryRun bool) error {
	in, err := e.installedSkill(root, sk)
	if err != nil {
		return err
	}
	done, verb := "added", "add"
	switch in.state {
	case skillCurrent:
		return nil
	case skillLinked:
		fmt.Fprintf(e.Out, "skipped skill %s: %s is a symlink to content "+brand.Name+" does not own; compare with: %s install --print-skill %s | diff %s -\n",
			sk.Name, in.path, e.command(), sk.Name, filepath.Join(root, sk.Name, "SKILL.md"))
		return nil
	case skillEdited, skillForeign:
		fmt.Fprintf(e.Out, "kept skill %s at %s: %s; remove it to let install write it\n", sk.Name, in.path, e.skillKept(sk.Name, in))
		return nil
	case skillManaged:
		done, verb = "changed", "change"
	}
	if dryRun {
		fmt.Fprintf(e.Out, "would %s skill %s in %s\n", verb, sk.Name, in.path)
		return nil
	}
	if err := writeSkill(in.path, e.renderSkill(sk)); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "%s skill %s in %s\n", done, sk.Name, in.path)
	return nil
}

func writeSkill(path string, body []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".SKILL-*.md")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
