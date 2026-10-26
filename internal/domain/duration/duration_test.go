package duration

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
	}{
		{"30s", 30 * time.Second},
		{"5m", 5 * time.Minute},
		{"2h", 2 * time.Hour},
		{"1d", 24 * time.Hour},
		{"90m", 90 * time.Minute},
		{"7d", 7 * 24 * time.Hour},
		{"1h30m", 90 * time.Minute},
	} {
		got, err := Parse(c.in)
		if err != nil || got != c.want {
			t.Errorf("Parse(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
}

func TestParseRefuses(t *testing.T) {
	for _, in := range []string{"", "0", "0s", "0d", "-1h", "-1d", "d", "1.5d", "1d2h", "1w", "abc", " 1d"} {
		if got, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %v, want an error", in, got)
		}
	}
}

func TestFormat(t *testing.T) {
	for _, c := range []struct {
		in   time.Duration
		want string
	}{
		{10 * time.Minute, "10m"},
		{30 * time.Second, "30s"},
		{2 * time.Hour, "2h"},
		{24 * time.Hour, "1d"},
		{72 * time.Hour, "3d"},
		{90 * time.Minute, "1h30m"},
		{25 * time.Hour, "25h"},
		{1500 * time.Millisecond, "1.5s"},
	} {
		if got := Format(c.in); got != c.want {
			t.Errorf("Format(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatParsesBack(t *testing.T) {
	for _, d := range []time.Duration{time.Second, 10 * time.Minute, 90 * time.Minute, 24 * time.Hour, 49 * time.Hour} {
		got, err := Parse(Format(d))
		if err != nil || got != d {
			t.Errorf("Parse(Format(%v)) = %v, %v", d, got, err)
		}
	}
}
