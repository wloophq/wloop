package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/termfmt"
)

// repoView writes profile show for a person: a section per intent, each
// block's state as a glyph, its source dimmed, its conditions and what
// else holds of it under them.
type repoView struct {
	doctorView
	sections int
}

func (v *repoView) section(title string, counts []tally) {
	if v.sections > 0 {
		fmt.Fprintln(v.w)
	}
	v.sections++
	v.title(title, counts)
}

func (v *repoView) layers(chain []profile.Layer) {
	for _, l := range chain {
		if l.Dir != "" {
			v.lines(0, hanging(v.width, v.t.Bold("Profile")+"  ", l.Name+" "+v.tilde(l.Dir), v.t.Dim), plain)
			v.sections++
		}
	}
}

func (v *repoView) facts(ref github.Ref, f *api.Facts, err error) {
	if err != nil {
		fmt.Fprintln(v.w, v.t.Yellow("brief facts unavailable: ")+termfmt.Clean(err.Error()))
		v.sections++
		return
	}
	fmt.Fprintln(v.w, v.t.Bold("Facts")+"  "+v.t.Link(ref.URL(), ref.String()))
	for _, l := range termfmt.Wrap(describeFacts(*f), v.width-2, v.width-2) {
		fmt.Fprintln(v.w, "  "+v.t.Dim(l))
	}
	v.sections++
}

// blockState is how one state reads: its glyph, its tally label and style.
type blockState struct {
	glyph, tally string
	style        func(string) string
	quiet        bool
}

func (v *repoView) state(s string) blockState {
	switch s {
	case profile.Applies:
		return blockState{v.t.Green("✓"), "apply", v.t.Dim, false}
	case profile.Elsewhere:
		return blockState{v.t.Dim("·"), "not here", v.t.Dim, true}
	case profile.Overridden:
		return blockState{v.t.Dim("·"), "replaced", v.t.Dim, true}
	case profile.Trimmed:
		return blockState{v.t.Yellow("!"), "dropped", v.t.Yellow, false}
	case profile.Depends:
		return blockState{v.t.Yellow("?"), "depend on the PR", v.t.Yellow, false}
	case profile.NeedsBrief:
		return blockState{v.t.Yellow("?"), "need the brief", v.t.Yellow, false}
	}
	return blockState{v.t.Yellow("?"), s, v.t.Yellow, false}
}

func (v *repoView) blocks(kind string, blocks []profile.Listed, g profile.Guide, bundledChanged bool) {
	tallies := map[string]int{}
	var order []string
	lw := 0
	var rels []int
	for _, l := range blocks {
		if tallies[l.State] == 0 {
			order = append(order, l.State)
		}
		tallies[l.State]++
		lw = max(lw, termfmt.Cells(termfmt.Clean(l.Block.Layer.Name)))
		rels = append(rels, termfmt.Cells(termfmt.Clean(l.Block.Rel())))
	}
	slices.SortStableFunc(order, func(a, b string) int { return stateRank(a) - stateRank(b) })
	var counts []tally
	for _, s := range order {
		st := v.state(s)
		counts = append(counts, tally{fmt.Sprintf("%d %s", tallies[s], st.tally), st.style})
	}
	v.section(kind, counts)
	col := 2 + 2 + lw + 2 + pathColumn(v.width, 4+lw+2, rels...) + 2
	for _, l := range blocks {
		st := v.state(l.State)
		layer, rel := termfmt.Clean(l.Block.Layer.Name), termfmt.Clean(l.Block.Rel())
		conds := plain
		if st.quiet {
			conds = v.t.Dim
		}
		lead := "  " + st.glyph + " " + v.t.Dim(layer) + strings.Repeat(" ", lw-termfmt.Cells(layer)+2)
		above, head := pathHead(v.width, lead, 4+lw+2, rel, v.t.Dim)
		v.lines(0, append(above, columned(v.width, head, col, l.Conditions, conds)...), plain)
		var notes []tally
		if l.State != profile.Applies {
			notes = append(notes, tally{l.State, st.style})
		}
		if l.Block.FM.Override {
			notes = append(notes, tally{"replaces the guidance above it", v.t.Cyan})
			if bundledChanged && l.Block.Path.Root() {
				notes = append(notes, tally{"bundled changed since the last round here", v.t.Yellow})
			}
		}
		pad := strings.Repeat(" ", textColumn(v.width, col))
		for _, n := range notes {
			v.lines(0, hanging(v.width, pad, n.text, n.style), plain)
		}
	}
	for _, a := range slices.Sorted(maps.Keys(g.Delegates)) {
		if len(g.Delegates[a]) > 0 {
			lines := hanging(v.width, "    delegates ", a+": "+strings.Join(g.Delegates[a], ", "), plain)
			lines = append(lines, hanging(v.width, "      ", "from "+v.tilde(g.DelegatesFrom), v.t.Dim)...)
			v.lines(0, lines, plain)
		}
	}
}

// stateRank orders a header's counts: what applies, what may, what does not.
func stateRank(s string) int {
	return slices.Index([]string{profile.Applies, profile.Depends, profile.NeedsBrief, profile.Trimmed, profile.Overridden, profile.Elsewhere}, s)
}

func (v *repoView) broken(name string, err error) {
	v.section(name, []tally{{"does not load", v.t.Red}})
	for _, l := range strings.Split(err.Error(), "\n") {
		fmt.Fprintln(v.w, "  "+termfmt.Clean(l))
	}
}

// show writes the sources dimmed and the instructions as the agent reads
// them, unstyled.
func (v *repoView) show(sources []string, text string) {
	fmt.Fprintf(v.w, "\n%s\n\n%s\n", v.t.Dim("sources: "+v.tildeText(strings.Join(sources, "; "))), text)
}
