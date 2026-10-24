package profile

import "strings"

const maxLevel = 6

type fence struct {
	char byte
	size int
}

func fenceOf(line string) (fence, bool) {
	l := strings.TrimLeft(line, " ")
	if len(line)-len(l) > 3 || len(l) < 3 || l[0] != '`' && l[0] != '~' {
		return fence{}, false
	}
	n := 0
	for n < len(l) && l[n] == l[0] {
		n++
	}
	return fence{char: l[0], size: n}, n >= 3
}

func closes(open fence, line string) bool {
	f, ok := fenceOf(line)
	return ok && f.char == open.char && f.size >= open.size && strings.TrimSpace(strings.TrimLeft(line, " ")[f.size:]) == ""
}

func headingLevel(line string) int {
	l := strings.TrimLeft(line, " ")
	if len(line)-len(l) > 3 {
		return 0
	}
	n := 0
	for n < len(l) && l[n] == '#' {
		n++
	}
	if n == 0 || n > maxLevel || n < len(l) && l[n] != ' ' && l[n] != '\t' {
		return 0
	}
	return n
}

func shiftHeadings(text string, top int) string {
	lines := strings.Split(text, "\n")
	var levels []int
	highest := 0
	var open *fence
	for _, l := range lines {
		lv := 0
		switch {
		case open != nil:
			if closes(*open, l) {
				open = nil
			}
		default:
			if f, ok := fenceOf(l); ok {
				open = &f
			} else if lv = headingLevel(l); lv > 0 && (highest == 0 || lv < highest) {
				highest = lv
			}
		}
		levels = append(levels, lv)
	}
	if highest == 0 || highest == top {
		return text
	}
	for i, l := range lines {
		if levels[i] == 0 {
			continue
		}
		to := min(max(levels[i]+top-highest, 1), maxLevel)
		t := strings.TrimLeft(l, " ")
		lines[i] = strings.Repeat("#", to) + t[levels[i]:]
	}
	return strings.Join(lines, "\n")
}
