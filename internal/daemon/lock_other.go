//go:build !unix

package daemon

func LockFile(string, error) (func(), error) { return func() {}, nil }
