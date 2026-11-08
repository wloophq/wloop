package profile

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func pk(path ...string) string { return pathKey(path) }

func TestSpliceKeepsLinesAndOtherBytes(t *testing.T) {
	src := "# c\na = \"x\"\nb = \"\"\"\nline1\nline2\"\"\"\nc = 3\n[t]\nd = { label = 'y', id = \"k\", m = '''\nm1\nm2''' }\nl = ['''\nl1''', \"l2\"]\n[[arr]]\nlabel = \"z\"\n[[arr]]\nlabel = \"w\"\n[[arr.sub]]\nlabel = \"s\"\n"
	got, err := Splice([]byte(src), map[string]string{
		pk("a"): "á \"q\" \\ \t\x01", pk("b"): "B", pk("t", "d", "label"): "Y", pk("t", "d", "m"): "M\nN", pk("t", "l", "[0]"): "L0",
		pk("arr", "[1]", "label"): "W", pk("arr", "[1]", "sub", "[0]", "label"): "S", pk("absent"): "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		A, B string
		C    int
		T    struct {
			D struct{ Label, ID, M string }
			L []string
		}
		Arr []struct {
			Label string
			Sub   []struct{ Label string }
		}
	}
	if err := toml.Unmarshal(got, &doc); err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	if doc.A != "á \"q\" \\ \t\x01" || doc.B != "B" || doc.C != 3 || doc.T.D.Label != "Y" || doc.T.D.ID != "k" || doc.T.D.M != "M\nN" ||
		doc.T.L[0] != "L0" || doc.T.L[1] != "l2" || doc.Arr[0].Label != "z" || doc.Arr[1].Label != "W" || doc.Arr[1].Sub[0].Label != "S" {
		t.Fatalf("%+v\n%s", doc, got)
	}
	if strings.Count(string(got), "\n") != strings.Count(src, "\n") {
		t.Fatalf("lines moved:\n%s", got)
	}
	gl, sl := strings.Split(string(got), "\n"), strings.Split(src, "\n")
	for i, line := range sl {
		if strings.HasPrefix(line, "c = 3") || strings.HasPrefix(line, "# c") || strings.HasPrefix(line, "[[arr.sub]]") {
			if gl[i] != line {
				t.Errorf("line %d: %q, want %q", i+1, gl[i], line)
			}
		}
	}
}

func TestSpliceRefusesABrokenDocument(t *testing.T) {
	if _, err := Splice([]byte("a = "), nil); err == nil {
		t.Fatal("a broken document spliced")
	}
}
