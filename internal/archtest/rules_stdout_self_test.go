package archtest

import "testing"

const writesStdout = `package x
import (
	"fmt"
	out "os"
	"syscall"

	"golang.org/x/sys/unix"
)
func f() {
	fmt.Println("a")
	fmt.Printf("%d", 1)
	fmt.Fprintln(out.Stdout, "b")
	_ = syscall.Stdout
	fmt.Fprintln(out.Stderr, "c")
	println("d")
	_ = unix.Stdout
	_ = out.NewFile(1, "stdout")
	_ = out.NewFile(3, "fd3")
}
`

const shadowsFmt = `package x
import "github.com/acme/fmt"
func f() { fmt.Println("a") }
`

func TestRStdoutFlagsWritesToStdout(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, writesStdout)
	flagged(t, "writes", ruleStdout(fset, "pkg/plugin/x.go", f), 6, "(R-stdout)")
	fset, f = parse(t, shadowsFmt)
	flagged(t, "another fmt", ruleStdout(fset, "pkg/plugin/x.go", f), 0, "")
}
