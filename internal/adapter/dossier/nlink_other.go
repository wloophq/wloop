//go:build !unix

package dossier

import "io/fs"

func links(fs.FileInfo) uint64 { return 1 }
