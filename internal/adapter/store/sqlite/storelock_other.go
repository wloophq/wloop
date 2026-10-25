//go:build !unix

package sqlite

func LockStore(string) (func(), error) { return func() {}, nil }

func freeBytes(string) int64 { return -1 }
