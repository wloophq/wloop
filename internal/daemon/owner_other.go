//go:build !unix

package daemon

import "os"

func ownedByMe(os.FileInfo) bool { return false }
