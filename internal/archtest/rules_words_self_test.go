package archtest

import (
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
)

func TestClientWordsFlagsAPlantedWord(t *testing.T) {
	src := `package p
const verb = "edit"
func f(m *Model, s string, args map[string]string) {
	m.status = "cancelled"
	fact("to", s)
	head := "sends " + s + " · edited" + ", stalled"
	if s == "open" {}
	switch s {
	case "Enter":
	}
	_ = args["send"]
	_ = map[string]int{"draft": 1}
	_ = m.w().T("Sweep now", nil)
	_ = sel("setTitle:")
	_ = "draft:" + s
	_ = "q"
	_ = fmt.Sprintf("answer %s: %d. %s?", s, 1, s)
	_ = fmt.Sprintf("%d. %s", 1, s)
	_ = "VISUAL"
	_ = "task " + s //prose:internal wraps an id
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	saved := clientTokens
	clientTokens = map[string]string{"VISUAL": "an environment variable's name"}
	defer func() { clientTokens = saved }()
	got, tokens := clientWords(fset, "internal/client/tui/p.go", f)
	flagged(t, "go", got, 6, "texts.Catalog")
	for _, want := range []string{`"cancelled"`, `"to"`, `"sends "`, `" · edited"`, `", stalled"`, `"answer %s: %d. %s?"`} {
		if !slices.ContainsFunc(got, func(g string) bool { return strings.Contains(g, want) }) {
			t.Errorf("%s is not flagged: %q", want, got)
		}
	}
	if !slices.Equal(tokens, []string{"VISUAL"}) {
		t.Errorf("tokens %q, want the one clientTokens names", tokens)
	}
}
