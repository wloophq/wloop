#!/usr/bin/env bash
# Prints the packages CI tests in shard $1. The rest shard takes every package
# the named shards leave out, so a new package is never skipped.
set -euo pipefail

named="daemon store cli sources"

patterns() {
	case "$1" in
	daemon) echo ./internal/daemon ;;
	store) echo ./internal/adapter/store/... ;;
	cli) echo ./internal/cli ;;
	sources) echo ./internal/adapter/source/slack ./internal/adapter/source/github ./internal/readmodel ./internal/brief ;;
	*) echo "unknown shard $1" >&2; return 1 ;;
	esac
}

if [ "${1:?usage: $0 <shard>|rest}" != rest ]; then
	pkgs=$(patterns "$1")
	# shellcheck disable=SC2086
	go list $pkgs
	exit
fi
taken=$(for s in $named; do patterns "$s"; done)
# shellcheck disable=SC2086
go list ./... | grep -vxF -f <(go list $taken)
