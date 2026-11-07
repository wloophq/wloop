package profile

// Splice is the TOML document src with each string value at a key of values (its path's segments joined by
// "\x00") replaced, every other byte and every line number kept.
func Splice(src []byte, values map[string]string) ([]byte, error) {
	panic("not implemented: D2")
}
