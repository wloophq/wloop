package archtest

import (
	"go/token"
	"go/types"
	"testing"
)

func TestGoNamesNoViewFlagsPlantedViews(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	_, _, tp := planted(t, fset, taskPkg, "model.go", "package task\ntype View string\ntype Query struct { View View }\n", nil)
	src := `package x
import "github.com/wloophq/wloop/internal/domain/task"
const Inbox task.View = "inbox"
func f(id string) []task.Query {
	var none task.View = ""
	return []task.Query{{View: "needs-me"}, {View: task.View("outbox")}, {View: task.View(id)}, {View: none}}
}
`
	f, info, _ := planted(t, fset, module+"internal/x", "x.go", src, map[string]*types.Package{taskPkg: tp})
	hits := viewLiteralHits(fset, "internal/x/x.go", info, f)
	flagged(t, "planted views", hits, 3, "is a task.View constant")
}
