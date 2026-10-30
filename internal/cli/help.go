package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/termfmt"
)

const tagline = "A work loop for the reviews and pull requests that reach you."

// Help wraps to the terminal within these columns.
const (
	helpMinWidth = 40
	helpMaxWidth = 100
)

// helpFlags are the arguments that ask a command for its help.
var helpFlags = []string{"-h", "--help", "-help"}

// helpGroups is the main help's groups with the registry's intents, each
// first in the group its manifest names, else in the user's own group.
func (e *Env) helpGroups() []helpGroup {
	groups := helpGroups(e.configuredManagerDisplay())
	l := e.loadedIntents()
	joined := map[int][]helpTopic{}
	var mine []helpTopic
	for _, d := range l.Registry.UserFacing() {
		tp := intentTopic(d)
		if gi := slices.IndexFunc(groups, func(g helpGroup) bool { return d.Help.Group != "" && strings.EqualFold(g.Title, d.Help.Group) }); gi >= 0 {
			joined[gi] = append(joined[gi], tp)
			continue
		}
		mine = append(mine, tp)
	}
	for gi, topics := range joined {
		groups[gi].Topics = append(topics, groups[gi].Topics...)
	}
	for _, broken := range slices.Sorted(maps.Keys(l.Broken)) {
		name := string(broken)
		mine = append(mine, helpTopic{
			Names:   []string{name},
			Summary: "This intent of your profile has problems; run '" + e.command() + " " + name + "' to list them",
			Usage:   []string{name},
			About:   "This intent of your profile does not load. Run '" + e.command() + " " + name + "' or '" + e.command() + " profile check' to list its problems.",
			Docs:    "guides/custom-intent",
		})
	}
	if len(mine) > 0 {
		at := slices.IndexFunc(groups, func(g helpGroup) bool { return g.Title == "PULL REQUESTS" }) + 1
		groups = slices.Insert(groups, at, helpGroup{groupYourIntents, mine})
	}
	return groups
}

// intentTopic is an intent's topic, from its manifest.
func intentTopic(d dintent.Def) helpTopic {
	name := string(d.Name)
	tp := helpTopic{Names: []string{name}, Summary: d.Help.Summary, Docs: d.Help.Docs}
	if tp.Summary == "" {
		tp.Summary = d.Summary
	}
	if tp.Docs == "" && !d.Bundled {
		tp.Docs = "guides/custom-intent"
	}
	for _, ex := range d.Help.Examples {
		tp.Examples = append(tp.Examples, name+" "+ex)
	}
	tp.Usage = []string{name + " " + intentTarget(d) + intentFlags(d, surfaceCLI) + " [guidance...]"}
	about := strings.TrimSpace(d.Summary)
	if about != "" && !strings.HasSuffix(about, ".") {
		about += "."
	}
	if d.Help.Target != "" {
		about += " The target is " + d.Help.Target + "; words after it are guidance for the turn."
	} else {
		about += " Words after the target are guidance for the turn."
	}
	if d.Intake {
		about += " It runs as an intake this command follows; Ctrl-C stops following, not the intake."
	}
	tp.About = about
	tp.Flags = nil
	for _, in := range d.Inputs {
		if in.From != "user" || in.Hidden || in.Flag == "" || !slices.Contains(in.Surfaces, surfaceCLI) {
			continue
		}
		flag := in.Flag
		if in.Type == "text" {
			flag += " <" + in.Name + ">"
		}
		tp.Flags = append(tp.Flags, helpFlag{flag, sentence(in.Help)})
	}
	if d.Intake {
		tp.Flags = append(tp.Flags,
			helpFlag{flagNoWait, "Print the intake's number instead of following it"},
			helpFlag{flagNoBrief, "Start the round without its brief"})
	}
	if d.Target.Missing == dintent.TargetCapture {
		tp.Flags = append(tp.Flags, helpFlag{flagBriefFile + " <path>", "Read the new task's brief from a file, instead of the words after the flags"})
	}
	if d.WorkspaceRepo != nil {
		tp.Flags = append(tp.Flags,
			helpFlag{flagManager + " <name>", "The workspace manager that creates the worktree, instead of the configured ones"},
			helpFlag{flagParent + " <worktree>", "The worktree the new one goes under, where its manager keeps parents; the one the command runs in by default"})
	}
	return tp
}

// sentence capitalizes s's first letter.
func sentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func allTopics(groups []helpGroup) []helpTopic {
	var out []helpTopic
	for _, g := range groups {
		out = append(out, g.Topics...)
	}
	return out
}

// helpTarget is what a help request names: a topic, or the parent of
// several, such as profile.
type helpTarget struct {
	topic    *helpTopic
	parent   string
	children []helpTopic
}

// resolveHelp finds the topic of the longest prefix of words, else the
// topics under words[0].
func resolveHelp(topics []helpTopic, words []string) (helpTarget, bool) {
	for n := len(words); n > 0; n-- {
		key := strings.Join(words[:n], " ")
		for i := range topics {
			if slices.Contains(topics[i].Names, key) {
				return helpTarget{topic: &topics[i]}, true
			}
		}
	}
	if len(words) == 0 {
		return helpTarget{}, false
	}
	var children []helpTopic
	for _, tp := range topics {
		if strings.HasPrefix(tp.Names[0], words[0]+" ") {
			children = append(children, tp)
		}
	}
	if len(children) == 0 {
		return helpTarget{}, false
	}
	return helpTarget{parent: words[0], children: children}, true
}

// helpWords are the leading words of args, the command and its
// subcommands, before the first flag.
func helpWords(args []string) []string {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			break
		}
		out = append(out, a)
	}
	return out
}

// asksHelp tells whether args, a command and its arguments, ask for its
// help. An intent's guidance and what follows "--" are text, never a flag.
func (e *Env) asksHelp(args []string) bool {
	rest := args[1:]
	if d, ok := e.loadedIntents().Registry.Get(dintent.Name(args[0])); ok && d.UserFacing {
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			rest = rest[1:]
		}
		for _, a := range rest {
			if slices.Contains(helpFlags, a) {
				return true
			}
			if a == "--" || !strings.HasPrefix(a, "-") {
				return false
			}
		}
		return false
	}
	for _, a := range rest {
		if a == "--" {
			return false
		}
		if slices.Contains(helpFlags, a) {
			return true
		}
	}
	return false
}

// devCommand tells the developer tools, whose own usage 'help dev' lists.
func devCommand(args []string) bool {
	return args[0] == "eval-instructions" || args[0] == "slack" && len(args) > 1 && args[1] == "eval"
}

// colorOf reads --color from args; an unknown value is auto.
func colorOf(args []string) termfmt.Mode {
	var m termfmt.Mode
	for i, a := range args {
		switch {
		case a == "--color" && i+1 < len(args):
			_ = m.Set(args[i+1])
		case strings.HasPrefix(a, "--color="):
			_ = m.Set(strings.TrimPrefix(a, "--color="))
		}
	}
	return m
}

// withoutColor drops --color and its value from args.
func withoutColor(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--color":
			i++
		case strings.HasPrefix(args[i], "--color="):
		default:
			out = append(out, args[i])
		}
	}
	return out
}

// runHelp prints the help args name: the main help, help dev, or a
// command's.
func runHelp(e *Env, args []string) error {
	t := e.term(colorOf(args))
	args = withoutColor(args)
	words := slices.DeleteFunc(slices.Clone(args), func(a string) bool { return slices.Contains(helpFlags, a) })
	switch {
	case len(words) == 0:
		_, err := fmt.Fprint(e.Out, renderMainHelp(t, e.helpGroups()))
		return err
	case len(words) == 1 && words[0] == "dev":
		_, err := fmt.Fprint(e.Out, devUsage)
		return err
	}
	target, ok := resolveHelp(allTopics(e.helpGroups()), words)
	if !ok {
		return e.unknownCommand(words[0])
	}
	_, err := fmt.Fprint(e.Out, renderHelpTarget(t, target))
	return err
}

// commandHelp prints the help of the command args run, when they ask for it
// and it has one.
func (e *Env) commandHelp(args []string) (bool, error) {
	if !e.asksHelp(args) || devCommand(args) {
		return false, nil
	}
	target, ok := resolveHelp(allTopics(e.helpGroups()), helpWords(args))
	if !ok {
		return false, nil
	}
	_, err := fmt.Fprint(e.Out, renderHelpTarget(e.term(colorOf(args)), target))
	return true, err
}

// unknownCommand says name is no command, with the closest one.
func (e *Env) unknownCommand(name string) error {
	msg := "unknown command " + quoteName(name)
	if near := closest(name, e.commandNames()); near != "" {
		msg += "; did you mean " + quoteName(near) + "?"
	}
	return usageError{why: msg, usage: "Run '" + e.command() + " --help' for the commands."}
}

func quoteName(s string) string { return "'" + termfmt.Clean(s) + "'" }

// commandNames are the words that start a command.
func (e *Env) commandNames() []string {
	set := map[string]bool{}
	for name := range commands() {
		if !strings.HasPrefix(name, "-") && name != "eval-instructions" {
			set[name] = true
		}
	}
	for _, d := range e.loadedIntents().Registry.UserFacing() {
		set[string(d.Name)] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// closest is the candidate nearest name: one it starts, or one at an edit
// distance a typo makes; "" for none.
func closest(name string, candidates []string) string {
	best, bestD := "", 0
	for _, c := range candidates {
		d := editDistance(name, c)
		if len(name) >= 2 && strings.HasPrefix(c, name) {
			d = 1
		}
		if d <= max(2, len(name)/3) && (best == "" || d < bestD) {
			best, bestD = c, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// helpPrinter lays help out for a terminal t within width.
type helpPrinter struct {
	t     termfmt.Term
	width int
	b     strings.Builder
}

func newHelpPrinter(t termfmt.Term) *helpPrinter {
	return &helpPrinter{t: t, width: min(max(t.Width, helpMinWidth), helpMaxWidth)}
}

func (p *helpPrinter) line(s string) { p.b.WriteString(s + "\n") }

func (p *helpPrinter) heading(s string) { p.line(p.t.Bold(s)) }

// text styles the placeholders of plain text.
func (p *helpPrinter) text(s string) string { return dimPlaceholders(p.t, s) }

// para wraps s within the width, each line indent spaces in.
func (p *helpPrinter) para(indent int, s string) {
	pad := strings.Repeat(" ", indent)
	for _, l := range termfmt.Wrap(s, p.width-indent, p.width-indent) {
		p.line(pad + p.text(l))
	}
}

// rows lays out label and text pairs two spaces in, the text in a column
// after the widest label, wrapped under itself; a label wider than cap puts
// its text on the next line.
func (p *helpPrinter) rows(labels, texts []string, style func(string) string, cap int) {
	col := 0
	for _, l := range labels {
		if w := termfmt.Cells(l); w <= cap {
			col = max(col, w)
		}
	}
	col += 4
	for i, l := range labels {
		lines := termfmt.Wrap(texts[i], p.width-col, p.width-col)
		if len(lines) == 0 {
			lines = []string{""}
		}
		w := termfmt.Cells(l)
		head := "  " + style(l)
		if w+4 > col {
			p.line(head)
		} else {
			p.line(strings.TrimRight(head+strings.Repeat(" ", col-2-w)+p.text(lines[0]), " "))
			lines = lines[1:]
		}
		for _, rest := range lines {
			p.line(strings.Repeat(" ", col) + p.text(rest))
		}
	}
}

// command styles a command line: the command and its subcommand words cyan,
// the placeholders dim.
func (p *helpPrinter) command(words, rest string) string {
	out := p.t.Cyan(strings.TrimSpace(brand.Command + " " + words))
	if rest != "" {
		out += " " + p.text(rest)
	}
	return out
}

func (p *helpPrinter) docs(page string) {
	if page == "" {
		return
	}
	url := brand.DocsURL() + page + "/"
	p.line("")
	p.heading("DOCS")
	p.line("  " + p.t.Link(url, url))
}

func (p *helpPrinter) footer(hint string) {
	p.line("")
	p.line(hint)
	url := brand.DocsURL()
	p.line("Docs: " + p.t.Link(url, url))
}

// label is a topic's name in the main help: pairs share it, as pause/resume
// or slack follow/unfollow.
func (tp helpTopic) label() string {
	out := tp.Names[0]
	for _, n := range tp.Names[1:] {
		out += "/" + n[strings.LastIndex(n, " ")+1:]
	}
	return out
}

func renderMainHelp(t termfmt.Term, groups []helpGroup) string {
	p := newHelpPrinter(t)
	p.line(brand.Name + " " + Version)
	p.line(tagline)
	p.line("")
	p.heading("USAGE")
	p.line("  " + p.command("", "<command> [arguments]"))
	var labels []string
	for _, g := range groups {
		for _, tp := range g.Topics {
			labels = append(labels, tp.label())
		}
	}
	col := 0
	for _, l := range labels {
		col = max(col, termfmt.Cells(l))
	}
	for _, g := range groups {
		p.line("")
		p.heading(g.Title)
		var ls, ts []string
		for _, tp := range g.Topics {
			ls, ts = append(ls, padRight(tp.label(), col)), append(ts, tp.Summary)
		}
		p.rows(ls, ts, func(s string) string {
			trimmed := strings.TrimRight(s, " ")
			return t.Cyan(trimmed) + s[len(trimmed):]
		}, col)
	}
	p.footer("Run '" + brand.Command + " <command> --help' for its flags and examples.")
	return p.b.String()
}

func padRight(s string, w int) string { return s + strings.Repeat(" ", max(w-termfmt.Cells(s), 0)) }

func renderHelpTarget(t termfmt.Term, target helpTarget) string {
	if target.topic != nil {
		return renderTopicHelp(t, *target.topic)
	}
	return renderParentHelp(t, target.parent, target.children)
}

func renderTopicHelp(t termfmt.Term, tp helpTopic) string {
	p := newHelpPrinter(t)
	p.heading("USAGE")
	for _, u := range tp.Usage {
		words, rest := splitUsage(tp.Names, u)
		p.line("  " + p.command(words, rest))
	}
	if tp.About != "" {
		p.line("")
		p.para(0, tp.About)
	}
	if len(tp.Flags) > 0 {
		p.line("")
		p.heading("FLAGS")
		var ls, ts []string
		for _, f := range tp.Flags {
			ls, ts = append(ls, f.Name), append(ts, f.Text)
		}
		p.rows(ls, ts, func(s string) string { return dimPlaceholders(t, s) }, 28)
	}
	if len(tp.Examples) > 0 {
		p.line("")
		p.heading("EXAMPLES")
		for _, ex := range tp.Examples {
			p.line("  " + brand.Command + " " + ex)
		}
	}
	p.docs(tp.Docs)
	return p.b.String()
}

// splitUsage cuts a usage line into the name of the topic it starts with
// and the rest.
func splitUsage(names []string, usage string) (string, string) {
	for _, n := range names {
		if usage == n || strings.HasPrefix(usage, n+" ") {
			return n, strings.TrimSpace(strings.TrimPrefix(usage, n))
		}
	}
	first, rest, _ := strings.Cut(usage, " ")
	return first, rest
}

func renderParentHelp(t termfmt.Term, parent string, children []helpTopic) string {
	p := newHelpPrinter(t)
	p.heading("USAGE")
	p.line("  " + p.command(parent, "<command> [arguments]"))
	p.line("")
	p.heading("COMMANDS")
	var ls, ts []string
	for _, c := range children {
		ls, ts = append(ls, strings.TrimPrefix(c.label(), parent+" ")), append(ts, c.Summary)
	}
	p.rows(ls, ts, t.Cyan, 24)
	p.footer("Run '" + brand.Command + " " + parent + " <command> --help' for its flags and examples.")
	return p.b.String()
}

// dimPlaceholders dims each outermost <placeholder> of s.
func dimPlaceholders(t termfmt.Term, s string) string {
	if !t.Color || !strings.Contains(s, "<") {
		return s
	}
	var b strings.Builder
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '<':
			if depth == 0 {
				b.WriteString(s[start:i])
				start = i
			}
			depth++
		case '>':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 {
				b.WriteString(t.Dim(s[start : i+1]))
				start = i + 1
			}
		}
	}
	b.WriteString(s[start:])
	return b.String()
}
