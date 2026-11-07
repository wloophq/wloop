package builtin

// SurfaceStrings is strings.toml, the client strings.
const SurfaceStrings Surface = "strings"

// LocalizedFiles are the whole-file texts a person reads, by file name under workflows/<name>/.
var LocalizedFiles = []string{"text.md.tmpl"}

// Localized says whether the key at path of a file of surface s holds a text a person reads; path holds
// concrete segments, an array index written "[3]".
func Localized(s Surface, path []string) bool {
	panic("not implemented: D2")
}

// SurfaceOf is the surface of profile file rel; false when rel is no file of the language.
func SurfaceOf(rel string) (Surface, bool) {
	panic("not implemented: D2")
}
