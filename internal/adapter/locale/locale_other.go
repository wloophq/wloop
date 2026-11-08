//go:build !darwin

package locale

// Preferred are the system's preferred languages, most preferred first, as the environment names them.
func Preferred() []string { return environment() }
