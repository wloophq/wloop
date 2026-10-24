package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/backup"
	"github.com/wloophq/wloop/internal/config"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/termfmt"
	"github.com/wloophq/wloop/internal/turn"
)

const profileUsage = `usage:
  profile show <owner/repo|pr-url> [--kind <intent>] [--show] [--color auto|always|never]
  profile where <intent> [conditions]
  profile add <intent>[,<intent>...] [conditions] [--section <heading>] [--title <text>] [--override] [--paths <glob>]... [--apply] [-- <text>]
  profile check [<dir>] [--color auto|always|never]
  profile schema [--workflow]
  profile try <pr-url>
conditions: --org, --repo, --user, --team, --association, --label, --base, --round, --kind, --size, --risk, --tag <key>=<value> (repeatable)`

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func runProfile(ctx context.Context, e *Env, args []string) error {
	if len(args) == 0 {
		return usageError{usage: profileUsage}
	}
	switch args[0] {
	case "try":
		return runTry(ctx, e, args[1:])
	case "show":
		return runProfileShow(ctx, e, args[1:])
	case "where":
		return runProfileWhere(e, args[1:])
	case "add":
		return runProfileAdd(e, args[1:])
	case "check":
		return runProfileCheck(e, args[1:])
	case "schema":
		switch {
		case len(args) == 1:
			_, err := e.Out.Write(intentload.Schema)
			return err
		case len(args) == 2 && args[1] == "--workflow":
			_, err := e.Out.Write(intentload.WorkflowSchema)
			return err
		}
		return usageError{usage: profileUsage}
	}
	return usageError{usage: profileUsage, why: "unknown profile command " + args[0]}
}

type conditions struct {
	dims map[profile.Dim]*string
	tags listFlag
}

func addConditions(fs *flag.FlagSet) *conditions {
	c := &conditions{dims: map[profile.Dim]*string{}}
	names := map[profile.Dim]string{profile.DimOrganization: "org"}
	for _, d := range profile.Dims {
		name := names[d]
		if name == "" {
			name = string(d)
		}
		c.dims[d] = fs.String(name, "", string(d)+" condition")
	}
	fs.Var(&c.tags, "tag", "tag condition <key>=<value>, repeatable")
	return c
}

// path builds the block path the flags name; notes says which tag was
// written in another spelling.
func (c *conditions) path() (p profile.BlockPath, notes []string, err error) {
	if *c.dims[profile.DimRepo] != "" && *c.dims[profile.DimOrganization] == "" {
		return p, nil, errors.New("--repo needs --org: a repository block sits under its organization")
	}
	for _, d := range []profile.Dim{profile.DimOrganization, profile.DimRepo, profile.DimUser, profile.DimTeam} {
		if v := *c.dims[d]; strings.Contains(v, "/") {
			return p, nil, fmt.Errorf("%s %s: the value has no /; name a repository with --org <owner> --repo <repo>, a team as <org>-<slug>", d, v)
		}
	}
	if v := *c.dims[profile.DimAssociation]; v != "" {
		if folded := strings.ToLower(strings.ReplaceAll(v, "_", "-")); folded != v {
			notes = append(notes, "association "+v+" written as "+folded)
			*c.dims[profile.DimAssociation] = folded
		}
	}
	for _, d := range profile.Dims {
		if v := *c.dims[d]; v != "" {
			p.Conds = append(p.Conds, profile.Cond{Dim: d, Value: v})
		}
	}
	for _, kv := range c.tags {
		t, err := profile.NormalizeTag(kv)
		if err != nil {
			return p, nil, err
		}
		if written := strings.Replace(t, ":", "=", 1); written != kv {
			notes = append(notes, "tag "+kv+" written as "+written)
		}
		p.Tags = append(p.Tags, t)
	}
	return p, notes, nil
}

func (c *conditions) facts() profile.Facts {
	v := func(d profile.Dim) string { return *c.dims[d] }
	f := profile.Facts{Owner: v(profile.DimOrganization), Repo: v(profile.DimRepo), Author: v(profile.DimUser), Association: v(profile.DimAssociation),
		Base: v(profile.DimBase), Round: v(profile.DimRound), Kind: v(profile.DimKind), Size: v(profile.DimSize), Risk: v(profile.DimRisk)}
	if t := v(profile.DimTeam); t != "" {
		f.Teams = []string{t}
	}
	if l := v(profile.DimLabel); l != "" {
		f.Labels = []string{l}
	}
	for _, kv := range c.tags {
		if t, err := profile.NormalizeTag(kv); err == nil {
			f.Tags = append(f.Tags, t)
		}
	}
	f.HasBrief = f.Kind != "" || f.Size != "" || f.Risk != "" || len(f.Tags) > 0
	return f
}

func parseIntents(s string, intents *dintent.Registry) ([]profile.Kind, error) {
	var out []profile.Kind
	kinds := profile.Kinds(intents)
	for _, part := range strings.Split(s, ",") {
		k := profile.Kind(strings.TrimSpace(part))
		if !slices.Contains(kinds, k) {
			names := make([]string, len(kinds))
			for i, k := range kinds {
				names[i] = string(k)
			}
			return nil, fmt.Errorf("unknown intent %q; use %s", part, strings.Join(names, ", "))
		}
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out, nil
}

func (e *Env) profileDir() (config.Config, string, error) {
	cfg, err := e.loadConfig()
	if err != nil {
		return cfg, "", err
	}
	if cfg.Profile == "" {
		return cfg, "", fmt.Errorf("no review profile is configured; set profile in %s to a directory", e.ConfigPath)
	}
	if fi, err := os.Stat(cfg.Profile); err != nil || !fi.IsDir() {
		return cfg, "", profile.MissingError{Layer: "user", Dir: cfg.Profile}
	}
	return cfg, cfg.Profile, nil
}

func runProfileWhere(e *Env, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usageError{usage: profileUsage}
	}
	kinds, err := parseIntents(args[0], e.loadedIntents().Registry)
	if err != nil || len(kinds) != 1 {
		return usageError{usage: profileUsage, why: "where takes one intent"}
	}
	fs := flag.NewFlagSet("profile where", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	conds := addConditions(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return usageError{usage: profileUsage}
	}
	cfg, dir, err := e.profileDir()
	if err != nil {
		return err
	}
	p, _, err := conds.path()
	if err != nil {
		return err
	}
	p.Intent = kinds[0]
	loaded := e.loadedIntents()
	intents := loaded.Registry
	rel, err := profile.PathOf(p, intents)
	if err != nil {
		return err
	}
	tree, err := profile.Load(profile.Chain(cfg), intents, loaded.BrokenNames())
	if err != nil {
		return err
	}
	file := filepath.Join(dir, filepath.FromSlash(rel))
	_, statErr := os.Stat(file)
	exists := statErr == nil
	if !exists {
		tree.Blocks = append(tree.Blocks, profile.Block{Layer: profile.Chain(cfg)[1], File: file, Path: p})
	}
	g := tree.Compose(kinds[0], conds.facts())
	state := "exists"
	if !exists {
		state = "new"
	}
	fmt.Fprintf(e.Out, "layer:   %s\nblock:   %s (%s)\n", dir, rel, state)
	for _, part := range g.Parts {
		if part.File == file {
			fmt.Fprintf(e.Out, "heading: %s %s\n", strings.Repeat("#", part.Level), part.Label)
		}
	}
	fmt.Fprintln(e.Out, "joins, in a pull request with exactly these facts:")
	for _, part := range g.Parts {
		if part.File != file {
			fmt.Fprintf(e.Out, "  %s %s   (%s)\n", part.Layer.Name, part.Path, part.Label)
		}
	}
	return nil
}

func inGitWorkTree(dir string) bool {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return true
		}
		if filepath.Dir(d) == d {
			return false
		}
	}
}

// applyChanges writes changes with a backup of each file outside git, and
// says how to review them.
func (e *Env) applyChanges(dir string, changes []profile.Change) error {
	var keep func(string, []byte) (string, error)
	var kept []string
	if !inGitWorkTree(dir) {
		stamp := e.Now().Format("20060102-150405")
		keep = func(path string, raw []byte) (string, error) {
			name, err := backup.File(path, raw, stamp)
			kept = append(kept, name)
			return name, err
		}
	}
	l := e.loadedIntents()
	if err := profile.Apply(dir, changes, keep, l.Registry, l.BrokenNames()); err != nil {
		return err
	}
	for _, c := range changes {
		fmt.Fprintf(e.Out, "wrote %s\n", c.File)
	}
	if keep == nil {
		fmt.Fprintf(e.Out, "review with: git -C %s status && git -C %s diff (new blocks show as untracked)\n", dir, dir)
	}
	for _, k := range kept {
		fmt.Fprintf(e.Out, "backup: %s\n", k)
	}
	return nil
}

func readText(e *Env, args []string) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	if e.In == nil {
		return "", errors.New("no text: pass it after -- or on standard input")
	}
	raw, err := io.ReadAll(e.In)
	return string(raw), err
}

func runProfileAdd(e *Env, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usageError{usage: profileUsage}
	}
	intents := e.loadedIntents().Registry
	kinds, err := parseIntents(args[0], intents)
	if err != nil {
		return usageError{usage: profileUsage, why: err.Error()}
	}
	fs := flag.NewFlagSet("profile add", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	conds := addConditions(fs)
	section := fs.String("section", "", "the ## heading the rule goes under")
	title := fs.String("title", "", "the block's heading, for a new block")
	override := fs.Bool("override", false, "the new block replaces the guidance above it")
	var paths listFlag
	fs.Var(&paths, "paths", "a glob a changed file must match, repeatable, for a new block")
	apply := fs.Bool("apply", false, "write the change; without it, print the diff")
	if err := fs.Parse(args[1:]); err != nil {
		return usageError{usage: profileUsage}
	}
	if *conds.dims[profile.DimRound] != "" {
		for _, k := range kinds {
			if !slices.Contains(profile.DimsOf(intents, k), string(profile.DimRound)) {
				return fmt.Errorf("--round does not apply to %s: its manifest's dims do not list round", k)
			}
		}
	}
	_, dir, err := e.profileDir()
	if err != nil {
		return err
	}
	p, notes, err := conds.path()
	if err != nil {
		return err
	}
	text, err := readText(e, fs.Args())
	if err != nil {
		return err
	}
	changes, err := profile.Addition{Intents: kinds, Path: p, Section: *section, Title: *title, Override: *override, Paths: paths, Text: text}.Plan(dir, intents)
	if err != nil {
		return err
	}
	for _, n := range notes {
		fmt.Fprintln(e.Out, n)
	}
	if !*apply {
		for _, c := range changes {
			fmt.Fprint(e.Out, profile.Diff(c))
		}
		fmt.Fprintln(e.Out, "dry run; run again with --apply to write it")
		return nil
	}
	return e.applyChanges(dir, changes)
}

func printBlockList(w io.Writer, tree profile.Tree, intents *dintent.Registry) {
	for _, k := range profile.Kinds(intents) {
		label := string(k)
		for _, b := range tree.Blocks {
			if b.Path.Intent != k || b.Layer.Dir == "" {
				continue
			}
			fmt.Fprintf(w, "%-8s %s   %s\n", label, b.Rel(), profile.Conditions(b.Path, b.FM.Paths))
			label = ""
		}
	}
}

func printIntents(w io.Writer, intents *dintent.Registry) {
	fmt.Fprintln(w, "Intents")
	for _, d := range intents.All() {
		source := "bundled"
		if !d.Bundled {
			source = filepath.Join(d.Source.Dir, "intent.toml")
		}
		fmt.Fprintf(w, "  %-16s %s   %s\n", d.Name, source, d.Summary)
	}
	fmt.Fprintln(w)
}

func runProfileCheck(e *Env, args []string) error {
	fs := flag.NewFlagSet("profile check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var color termfmt.Mode
	fs.Var(&color, "color", "lay the report out for a terminal: auto, always or never")
	if err := fs.Parse(args); err != nil {
		return usageError{usage: profileUsage, why: err.Error()}
	}
	var dir string
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return usageError{usage: profileUsage, why: err.Error()}
		}
		if fs.NArg() > 0 {
			return usageError{usage: profileUsage, why: "profile check takes one directory"}
		}
	} else {
		_, d, err := e.profileDir()
		if err != nil {
			return err
		}
		dir = d
	}
	out, errOut := e.term(color), e.termOf(e.Err, color)
	layer := []profile.Layer{{Name: "user", Source: dir, Dir: dir}}
	b, err := turn.Base()
	if err != nil {
		return err
	}
	loaded, problems := intentload.Load(layer, turn.BundledFS(), b, turn.CheckTemplate)
	if out.TTY {
		e.writeIntents(out, dir, loaded.Registry)
	} else {
		printIntents(e.Out, loaded.Registry)
	}
	if len(problems) > 0 {
		if errOut.TTY {
			e.writeProblems(errOut, problems.Sorted())
		} else {
			for _, p := range problems.Sorted() {
				fmt.Fprintln(e.Err, intentload.Problems{p}.Error())
				if p.Snippet != "" {
					fmt.Fprintln(e.Err, p.Snippet)
				}
			}
		}
		return fmt.Errorf("%s: %d problem(s)", dir, len(problems))
	}
	tree, err := profile.Load(layer, loaded.Registry, loaded.BrokenNames())
	if err != nil {
		return err
	}
	if out.TTY {
		e.writeBlocks(out, dir, tree, loaded.Registry)
		return nil
	}
	printBlockList(e.Out, tree, loaded.Registry)
	fmt.Fprintf(e.Out, "%s: %d block(s), no errors\n", dir, len(tree.Blocks))
	return nil
}
