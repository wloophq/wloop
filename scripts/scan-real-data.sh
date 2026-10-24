#!/usr/bin/env bash
# Usage: LOOP_DENY_LIST=<file> scan-real-data.sh <base>
#
# Scans the patches and messages of git log <base>..HEAD for every term of
# the deny list (one term per line, matched case-insensitively as a fixed
# string; blank lines and lines starting with # are skipped). Added lines,
# binary files as text, the paths of added or changed files and commit
# messages count. Prints one "<file>: <count>" line per file with a match,
# "commit <sha> file <n>: <count>" when the path itself matches (n is the
# file's place in the commit) and "commit <sha> message: <count>"; never a term.
# Exit 0 clean, 1 on a match, 2 when the deny list is unset, missing or empty.
set -euo pipefail

base=${1:?usage: scan-real-data.sh <base>}
list=${LOOP_DENY_LIST:-}
if [[ -z $list ]]; then
	echo "LOOP_DENY_LIST is not set" >&2
	exit 2
fi
if [[ ! -r $list ]]; then
	echo "the deny list $list is not readable" >&2
	exit 2
fi
if ! grep -qv -e '^[[:space:]]*$' -e '^#' "$list"; then
	echo "the deny list $list holds no term" >&2
	exit 2
fi

git log -p --text --no-renames --cc --src-prefix=a/ --dst-prefix=b/ --no-color --no-ext-diff \
	--format='commit %H%n%B' "$base..HEAD" |
	LC_ALL=C tr '\000' ' ' |
	LC_ALL=C awk -v list="$list" '
	BEGIN {
		while ((getline t < list) > 0) {
			sub(/\r$/, "", t)
			if (t ~ /^[[:space:]]*$/ || t ~ /^#/) continue
			terms[++n] = tolower(t)
		}
	}
	function count(s,    i, c, l, p, rest) {
		s = tolower(s)
		c = 0
		for (i = 1; i <= n; i++) {
			l = length(terms[i])
			rest = s
			while ((p = index(rest, terms[i])) > 0) {
				c++
				rest = substr(rest, p + l)
			}
		}
		return c
	}
	function hit(c) { if (c) hits[where] += c }
	/^commit [0-9a-f]+$/ { sha = substr($2, 1, 12); where = "commit " sha " message"; inmsg = 1; file = 0; next }
	/^diff --(git|cc|combined) / {
		inmsg = 0; file++; where = "commit " sha " file " file
		parents = ($0 ~ /^diff --git /) ? 1 : 2
		next
	}
	!inmsg && /^\+\+\+ / {
		f = substr($0, 5)
		if (f == "/dev/null") next
		sub(/^b\//, "", f)
		c = count(f)
		if (c) hit(c)
		else where = f
		next
	}
	inmsg { hit(count($0)); next }
	substr($0, 1, parents) ~ /\+/ && substr($0, 1, parents) !~ /-/ { hit(count(substr($0, parents + 1))) }
	END {
		for (w in hits) { print w ": " hits[w]; total += hits[w]; places++ }
		if (places) {
			print total " denied matches in " places " places" > "/dev/stderr"
			exit 1
		}
	}' | sort
