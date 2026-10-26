package expr

import (
	"errors"
	"strings"
	"testing"
)

var testEnv = Env{
	"pr.mine":    Bool,
	"pr.draft":   Bool,
	"pr.adopted": Bool,
	"pr.state":   String,
	"pr.author":  String,
	"pr.labels":  List,
	"pr.reviews": Int,
}

type mapFacts map[string]any

func (m mapFacts) Get(field string) any { return m[field] }

func compile(t *testing.T, src string) *Program {
	t.Helper()
	p, err := Compile(src, testEnv)
	if err != nil {
		t.Fatalf("Compile(%q): %v", src, err)
	}
	return p
}

func compileErr(t *testing.T, src string) *Error {
	t.Helper()
	_, err := Compile(src, testEnv)
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("Compile(%q) = %v, want *Error", src, err)
	}
	return e
}

func TestGrammar(t *testing.T) {
	facts := mapFacts{
		"pr.mine": true, "pr.draft": false, "pr.adopted": false,
		"pr.state": "open", "pr.author": "ana", "pr.labels": []string{"bug", "x"}, "pr.reviews": 2,
	}
	cases := []struct {
		src  string
		want bool
	}{
		{"true", true},
		{"false", false},
		{"pr.mine", true},
		{"!pr.mine", false},
		{"!!pr.mine", true},
		{"pr.mine && pr.draft", false},
		{"pr.mine || pr.draft", true},
		{"pr.draft || pr.mine && pr.draft", false},
		{"(pr.draft || pr.mine) && pr.draft", false},
		{"!pr.draft && !pr.adopted", true},
		{`pr.state == "open"`, true},
		{`pr.state != "open"`, false},
		{`pr.mine == true`, true},
		{`pr.draft != pr.mine`, true},
		{`"bug" in pr.labels`, true},
		{`"nope" in pr.labels`, false},
		{`!("nope" in pr.labels)`, true},
		{`"x" in ["a","x"]`, true},
		{`"x" in ["a", "b"]`, false},
		{`"x" in []`, false},
		{`pr.author in ["ana", "bob"]`, true},
		{"pr.reviews == 2", true},
		{"pr.reviews != 2", false},
		{"pr.reviews < 3", true},
		{"pr.reviews <= 2", true},
		{"pr.reviews > 2", false},
		{"pr.reviews >= 2", true},
		{"pr.reviews > 0 && pr.mine", true},
		{`pr.author == "a\"b"`, false},
		{"  pr.mine\t", true},
	}
	for _, c := range cases {
		if got := compile(t, c.src).Eval(facts); got != c.want {
			t.Errorf("%q = %v, want %v", c.src, got, c.want)
		}
	}
}

func TestEval(t *testing.T) {
	p := compile(t, `pr.mine && "bug" in pr.labels && pr.reviews >= 2 && pr.state == "open"`)
	hit := mapFacts{"pr.mine": true, "pr.labels": []string{"bug"}, "pr.reviews": 3, "pr.state": "open"}
	if !p.Eval(hit) {
		t.Error("want true")
	}
	if p.Eval(mapFacts{"pr.mine": true, "pr.labels": []string{"bug"}, "pr.reviews": 1, "pr.state": "open"}) {
		t.Error("reviews 1 must not match")
	}
	if p.Eval(mapFacts{}) {
		t.Error("zero facts must not match")
	}
	if !compile(t, `!pr.mine && pr.author == ""`).Eval(mapFacts{}) {
		t.Error("a missing fact reads as its zero value")
	}
}

func TestEmptyPredicateIsAnError(t *testing.T) {
	for _, src := range []string{"", "   ", "\t\n"} {
		e := compileErr(t, src)
		if e.Col != 1 || e.Msg != "empty predicate" {
			t.Errorf("%q: %+v", src, e)
		}
	}
}

func TestUnknownFieldSuggests(t *testing.T) {
	e := compileErr(t, "pr.mien")
	if e.Col != 1 || e.Msg != "unknown field pr.mien; did you mean \"pr.mine\"?" || e.Snippet != "pr.mien\n^" {
		t.Errorf("%+v", e)
	}
	e = compileErr(t, "pr.mine && zzz.qqq")
	if e.Col != 12 || e.Msg != "unknown field zzz.qqq" {
		t.Errorf("%+v", e)
	}
}

func TestTypeErrors(t *testing.T) {
	cases := []struct {
		src string
		col int
		msg string
	}{
		{`pr.reviews == "two"`, 12, "cannot compare int == string"},
		{`pr.state`, 1, "predicate must be bool, got string"},
		{`pr.reviews`, 1, "predicate must be bool, got int"},
		{`pr.labels`, 1, "predicate must be bool, got list"},
		{`pr.state > 1`, 10, "cannot compare string > int"},
		{`pr.reviews in pr.labels`, 12, "in needs a value and a list of its type, got int in list"},
		{`pr.state in "open"`, 10, "in needs a value and a list of its type, got string in string"},
		{`pr.mine && pr.reviews`, 12, "expected bool, got int"},
		{`!pr.reviews`, 1, "! needs bool, got int"},
	}
	for _, c := range cases {
		e := compileErr(t, c.src)
		if e.Col != c.col || e.Msg != c.msg {
			t.Errorf("%q: col %d %q, want col %d %q", c.src, e.Col, e.Msg, c.col, c.msg)
		}
	}
}

func TestSyntaxErrors(t *testing.T) {
	cases := []struct {
		src string
		col int
		msg string
	}{
		{`pr.mine && (pr.adopted`, 23, "missing ')'"},
		{`&& x`, 1, `unexpected "&&"`},
		{`pr.mine ||`, 11, "unexpected end of expression"},
		{`pr.mine pr.draft`, 9, `unexpected "pr"`},
		{`"open`, 1, "unterminated string"},
		{`'open`, 1, "unterminated string"},
		{`pr.mine & pr.draft`, 9, `unexpected '&'`},
		{`pr.reviews == 99999999999999999999`, 15, "invalid int literal"},
		{`pr..mine`, 4, `unexpected "."`},
		{`pr.`, 4, "unexpected end of expression"},
		{`pr.state == "a\`, 13, "unterminated string"},
		{`"é" == pr.mine`, 5, "cannot compare string == bool"},
		{"pr.state == \"l1\nl2\"", 13, "unterminated string"},
		{`)`, 1, `unexpected ")"`},
		{`()`, 2, `unexpected ")"`},
	}
	for _, c := range cases {
		e := compileErr(t, c.src)
		if e.Col != c.col || e.Msg != c.msg {
			t.Errorf("%q: col %d %q, want col %d %q", c.src, e.Col, e.Msg, c.col, c.msg)
		}
	}
}

func TestSnippetPointsAtTheColumn(t *testing.T) {
	e := compileErr(t, `pr.mine && (pr.adopted`)
	lines := strings.Split(e.Snippet, "\n")
	if len(lines) != 2 || lines[0] != `pr.mine && (pr.adopted` || lines[1] != strings.Repeat(" ", 22)+"^" {
		t.Errorf("%q", e.Snippet)
	}
}

func TestNestingIsBounded(t *testing.T) {
	src := strings.Repeat("(", 1000) + "pr.mine" + strings.Repeat(")", 1000)
	if e := compileErr(t, src); e.Msg != "expression nested too deeply" || e.Col != 1 {
		t.Errorf("%+v", e)
	}
	compileErr(t, strings.Repeat("!", 100000)+"pr.mine")
	compile(t, strings.Repeat("!", 10)+"pr.mine")
}

func TestCaretFollowsTheLineAndTabs(t *testing.T) {
	e := compileErr(t, "pr.mine &&\n\tzz")
	if e.Snippet != "\tzz\n\t^" {
		t.Errorf("%q", e.Snippet)
	}
}

func TestIntLiteralBoundary(t *testing.T) {
	compile(t, "pr.reviews < 9223372036854775807")
	if e := compileErr(t, "pr.reviews < 9223372036854775808"); e.Msg != "invalid int literal" {
		t.Error(e.Msg)
	}
}

func TestANegationBeforeAComparisonSaysWhereToParenthesize(t *testing.T) {
	cases := []struct{ src, msg string }{
		{`!pr.state == "open"`, `! needs bool, got string; ! applies before ==: write !(pr.state == "open")`},
		{`!pr.state in ["a", "b"] && pr.mine`, `! needs bool, got string; ! applies before in: write !(pr.state in ["a", "b"])`},
		{`pr.mine && !pr.author != pr.state`, `! needs bool, got string; ! applies before !=: write !(pr.author != pr.state)`},
		{`!pr.reviews >= ( 2 )`, `! needs bool, got int; ! applies before >=: write !(pr.reviews >= ( 2 ))`},
		{`!pr.reviews`, `! needs bool, got int`},
	}
	for _, c := range cases {
		if e := compileErr(t, c.src); e.Msg != c.msg {
			t.Errorf("%q: %q, want %q", c.src, e.Msg, c.msg)
		}
	}
	compile(t, `!(pr.state == "open") && !(pr.state in ["a"])`)
}
