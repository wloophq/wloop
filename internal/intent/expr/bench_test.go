package expr

import "testing"

type benchFacts struct{}

func (benchFacts) Get(field string) any {
	switch field {
	case "pr.mine":
		return true
	case "pr.state":
		return "open"
	case "pr.labels":
		return []string{"x", "bug", "y"}
	case "pr.reviews":
		return 3
	case "pr.adopted":
		return false
	}
	return nil
}

func BenchmarkEval(b *testing.B) {
	p, err := Compile(`pr.state == "open" && "bug" in pr.labels && pr.reviews >= 2 && !pr.adopted`, testEnv)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		p.Eval(benchFacts{})
	}
}
