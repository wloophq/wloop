package intent

import (
	"slices"
	"testing"
)

func TestAnInputIsNamedByItsFlagElseItsField(t *testing.T) {
	if got := (Input{Name: "take_over", Flag: "--take-over"}).Label(); got != "--take-over" {
		t.Errorf("flag: %q", got)
	}
	if got := (Input{Name: "who"}).Label(); got != "inputs.who" {
		t.Errorf("field: %q", got)
	}
}

func TestNormalizeTakesAValueAsItsInputsType(t *testing.T) {
	who := Input{Name: "who", Type: "list"}
	if got, err := Normalize(who, []any{"@ana", "@bo"}); err != nil || !slices.Equal(got.([]string), []string{"@ana", "@bo"}) {
		t.Errorf("a list from JSON: %#v %v", got, err)
	}
	if _, err := Normalize(who, []any{"@ana", 2.0}); err == nil || err.Error() != "inputs.who takes a list of texts" {
		t.Errorf("a list holding a number: %v", err)
	}
	for _, c := range []struct {
		in   Input
		v    any
		want string
	}{
		{Input{Name: "take_over", Flag: "--take-over", Type: "bool"}, "yes", "--take-over takes true or false"},
		{Input{Name: "topic", Type: "text"}, true, "inputs.topic takes a text"},
		{Input{Name: "x", Type: "date"}, "", "inputs.x takes a known type"},
	} {
		if _, err := Normalize(c.in, c.v); err == nil || err.Error() != c.want {
			t.Errorf("%s %v: %v, want %q", c.in.Name, c.v, err, c.want)
		}
	}
}

func TestNormalizeAllKeepsOnlyTheDeclaredInputs(t *testing.T) {
	d := Def{Inputs: []Input{{Name: "who", Type: "list"}, {Name: "fast", Type: "bool"}}}
	got, err := d.NormalizeAll(map[string]any{"who": []any{"@ana"}, "waits_on_maintainers": true})
	if err != nil || len(got) != 1 || !slices.Equal(got["who"].([]string), []string{"@ana"}) {
		t.Errorf("stored inputs: %#v %v", got, err)
	}
	if _, err := d.NormalizeAll(map[string]any{"fast": "yes"}); err == nil {
		t.Error("a wrong type was taken")
	}
}

func TestZeroTellsAnUnsetValue(t *testing.T) {
	for _, v := range []any{nil, false, "", []string{}, []any{}, map[string]any{}} {
		if !Zero(v) {
			t.Errorf("%#v is not zero", v)
		}
	}
	for _, v := range []any{true, "x", []string{"a"}} {
		if Zero(v) {
			t.Errorf("%#v is zero", v)
		}
	}
}
