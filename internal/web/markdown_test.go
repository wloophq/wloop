package web

import (
	"slices"
	"strings"
	"testing"
)

// markdownPrelude is markdown.js over a tree h, with the cockpit's own safeURL and i18n.js.
func markdownPrelude(t *testing.T) string {
	return treeJS + jsFunction(t, appJS(t), "safeURL") + staticJS(t, "i18n.js") + staticJS(t, "markdown.js") + `
function shape(n) {
  if (!n || typeof n !== "object") return null;
  return { tag: n.tag, text: text(n), attrs: n.attrs, children: n.children.map(shape).filter(Boolean) };
}
`
}

type mdNode struct {
	Tag      string            `json:"tag"`
	Text     string            `json:"text"`
	Attrs    map[string]string `json:"attrs"`
	Children []mdNode          `json:"children"`
}

func (n mdNode) all(pred func(mdNode) bool) []mdNode {
	var out []mdNode
	if pred(n) {
		out = append(out, n)
	}
	for _, c := range n.Children {
		out = append(out, c.all(pred)...)
	}
	return out
}

func (n mdNode) tags(tag string) []mdNode {
	return n.all(func(x mdNode) bool { return x.Tag == tag })
}

func renderMD(t *testing.T, text string, opts string) mdNode {
	t.Helper()
	var got mdNode
	nodeRun(t, markdownPrelude(t), nil, `process.stdout.write(JSON.stringify(shape(renderMarkdown(input`+opts+`))));`, text, &got)
	return got
}

func TestMarkdownRendersTheAgentsSubset(t *testing.T) {
	got := renderMD(t, "Use `retry()` **now**.\n\n- one\n- two\n\n```\nx := 1\n```", "")
	if got.Tag != "div" || got.Attrs["class"] != "md" {
		t.Fatalf("root %s.%s", got.Tag, got.Attrs["class"])
	}
	var blocks []string
	for _, c := range got.Children {
		blocks = append(blocks, c.Tag)
	}
	if !slices.Equal(blocks, []string{"p", "ul", "pre"}) {
		t.Fatalf("blocks %v", blocks)
	}
	p := got.Children[0]
	if code := p.tags("code"); len(code) != 1 || code[0].Text != "retry()" {
		t.Errorf("code spans %+v", code)
	}
	if strong := p.tags("strong"); len(strong) != 1 || strong[0].Text != "now" {
		t.Errorf("strong %+v", strong)
	}
	if p.Text != "Use retry() now." {
		t.Errorf("paragraph %q", p.Text)
	}
	var items []string
	for _, li := range got.Children[1].tags("li") {
		items = append(items, li.Text)
	}
	if !slices.Equal(items, []string{"one", "two"}) {
		t.Errorf("items %v", items)
	}
	if code := got.Children[2].tags("code"); len(code) != 1 || code[0].Text != "x := 1" {
		t.Errorf("code block %+v", code)
	}
}

func TestMarkdownRendersHeadingsQuotesEmphasisAndOrderedLists(t *testing.T) {
	got := renderMD(t, "## Plan\n\n> a *quoted* line\n> goes on\n\n1. first\n2. second_one with snake_case", "")
	var blocks []string
	for _, c := range got.Children {
		blocks = append(blocks, c.Tag)
	}
	if !slices.Equal(blocks, []string{"h4", "blockquote", "ol"}) {
		t.Fatalf("blocks %v", blocks)
	}
	if em := got.Children[1].tags("em"); len(em) != 1 || em[0].Text != "quoted" {
		t.Errorf("em %+v", em)
	}
	if li := got.Children[2].tags("li"); len(li) != 2 || li[1].Text != "second_one with snake_case" {
		t.Errorf("ordered items %+v", li)
	}
}

func TestMarkdownNeverBuildsHTMLFromText(t *testing.T) {
	const raw = "<img src=x onerror=alert(1)> <b>hi</b>"
	got := renderMD(t, raw, "")
	if len(got.Children) != 1 || got.Children[0].Tag != "p" || got.Children[0].Text != raw {
		t.Fatalf("got %+v", got)
	}
	if n := got.all(func(x mdNode) bool { return x.Tag == "img" || x.Tag == "b" }); len(n) != 0 {
		t.Errorf("built %+v", n)
	}
	src := staticJS(t, "markdown.js")
	for _, sink := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "DOMParser"} {
		if strings.Contains(src, sink) {
			t.Errorf("markdown.js uses %s", sink)
		}
	}
}

func TestMarkdownLinksOnlyHTTPS(t *testing.T) {
	got := renderMD(t, "[a](https://example.test/x) [b](javascript:alert(1)) [c](http://x.test) see https://example.test/y.", "")
	links := got.tags("a")
	if len(links) != 2 {
		t.Fatalf("links %+v", links)
	}
	a := links[0]
	if a.Text != "a" || a.Attrs["href"] != "https://example.test/x" || a.Attrs["target"] != "_blank" || a.Attrs["rel"] != "noopener noreferrer" {
		t.Errorf("link %+v", a)
	}
	if links[1].Attrs["href"] != "https://example.test/y" || links[1].Text != "https://example.test/y" {
		t.Errorf("bare link %+v", links[1])
	}
	if text := got.Children[0].Text; text != "a b c see https://example.test/y." {
		t.Errorf("text %q", text)
	}
}

func TestMarkdownFoldsLongProse(t *testing.T) {
	var paras []string
	for i := range 12 {
		paras = append(paras, "para "+string(rune('a'+i)))
	}
	var got struct {
		Before, After, Other mdNode
	}
	nodeRun(t, markdownPrelude(t), nil, `
const opts = { fold: 3, key: "gate:7" };
const before = renderMarkdown(input, opts);
const button = all(before, (x) => x.tag === "button")[0];
button.attrs.onclick();
const after = renderMarkdown(input, opts);
const other = renderMarkdown(input, { fold: 3, key: "gate:8" });
process.stdout.write(JSON.stringify({ before: shape(before), after: shape(after), other: shape(other) }));`, strings.Join(paras, "\n\n"), &got)
	if p := got.Before.tags("p"); len(p) != 3 {
		t.Errorf("folded shows %d paragraphs, want 3", len(p))
	}
	if b := got.Before.tags("button"); len(b) != 1 || b[0].Text != "Read all" {
		t.Errorf("fold button %+v", b)
	}
	if p := got.After.tags("p"); len(p) != 12 || len(got.After.tags("button")) != 0 {
		t.Errorf("opened shows %d paragraphs and %d buttons", len(p), len(got.After.tags("button")))
	}
	if p := got.Other.tags("p"); len(p) != 3 {
		t.Errorf("another key shows %d paragraphs, want it folded", len(p))
	}
}

func TestMarkdownFoldsAHugeAnswerCheaply(t *testing.T) {
	lines := make([]string, 5000)
	for i := range lines {
		lines[i] = "line with `code` and **bold**"
	}
	got := renderMD(t, strings.Join(lines, "\n\n"), ", { fold: 4 }")
	if p := got.tags("p"); len(p) != 4 {
		t.Errorf("a 5,000-block answer shows %d paragraphs folded, want 4", len(p))
	}
}

func TestMarkdownKeepsUnclosedMarkersAsText(t *testing.T) {
	for _, raw := range []string{"a **b and `c", "2 * 3 * 4", "[label](no close", "_x"} {
		got := renderMD(t, raw, "")
		if len(got.Children) != 1 || got.Children[0].Text != raw {
			t.Errorf("%q rendered %+v", raw, got)
		}
		if n := got.all(func(x mdNode) bool { return x.Tag == "strong" || x.Tag == "em" || x.Tag == "code" || x.Tag == "a" }); len(n) != 0 {
			t.Errorf("%q built %+v", raw, n)
		}
	}
}
