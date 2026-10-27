package archtest

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestServeThroughTheHarnessFlagsAPlantedCall(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x_test.go", `package cli
func f(te *testEnv) {
	serveDeps(ctx, te.Env, db, cfg, logf, mu, nil)
	serveUntilConfigLoads(ctx, te.Env, db, logf, mu, nil, ep)
	te.serveDeps(ctx, db, cfg, logf, mu, nil)
}
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	flagged(t, "a test", ruleServeThroughTheHarness(fset, "internal/cli/x_test.go", f), 2, "testEnv.serveDeps")
	flagged(t, "the harness", ruleServeThroughTheHarness(fset, "internal/cli/harness_test.go", f), 0, "")
	flagged(t, "serve itself", ruleServeThroughTheHarness(fset, "internal/cli/serve.go", f), 0, "")
	flagged(t, "another package", ruleServeThroughTheHarness(fset, "internal/daemon/x_test.go", f), 0, "")
}
