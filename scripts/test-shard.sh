#!/usr/bin/env bash
# Usage: test-shard.sh <shard>|rest|check
#
# Prints the packages CI tests in a shard. The rest shard takes every package
# the named shards leave out, so a new package is never skipped. check fails
# unless the test-shard and brand matrices of ci.yml and cache.yml list exactly
# these shards and every package falls in exactly one of them.
set -euo pipefail

named="daemon store cli sources"

patterns() {
	case "$1" in
	daemon) echo ./internal/daemon ;;
	store) echo ./internal/adapter/store/... ;;
	cli) echo ./internal/cli ;;
	sources) echo ./plugins/... ./internal/adapter/source/... ./internal/readmodel ./internal/brief ;;
	*) echo "unknown shard $1" >&2; return 1 ;;
	esac
}

rest() {
	local taken
	taken=$(for s in $named; do patterns "$s"; done)
	# shellcheck disable=SC2086
	taken=$(go list $taken)
	go list ./... | grep -vxF "$taken"
}

check() {
	local want workflow got all sharded
	want="shard: [${named// /, }, rest]"
	for workflow in .github/workflows/ci.yml .github/workflows/cache.yml; do
		got=$(grep -o 'shard: \[.*\]' "$workflow")
		if [ "$got" != "$want"$'\n'"$want" ]; then
			echo "$workflow has \"$got\", want \"$want\" in its test-shard and brand jobs" >&2
			return 1
		fi
	done
	all=$(go list ./... | sort)
	sharded=$(for s in $named; do "$0" "$s"; done; rest)
	sharded=$(sort <<<"$sharded")
	if [ "$sharded" != "$all" ]; then
		echo "the shards do not cover each package exactly once:" >&2
		diff <(echo "$all") <(echo "$sharded") >&2 || true
		return 1
	fi
}

case "${1:?usage: $0 <shard>|rest|check}" in
rest) rest ;;
check) check ;;
*)
	pkgs=$(patterns "$1")
	# shellcheck disable=SC2086
	go list $pkgs
	;;
esac
