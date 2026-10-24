#!/usr/bin/env bash
# Runs a daemon that cannot reach the user's Orca, GitHub, agents, Keychain
# items or daemon.
#
# Usage:
#   isolated-daemon.sh setup <dir> <binary> <dm-channel-id> [<index>] [--slack] [--agent]
#   isolated-daemon.sh env <dir>
#   isolated-daemon.sh run <dir> <command> [<arg>...]
#   isolated-daemon.sh snapshot <dir> <source-store>
#   isolated-daemon.sh pause <dir>
#   isolated-daemon.sh start <dir> [<serve arg>...]
#   isolated-daemon.sh stop <dir>
#   isolated-daemon.sh clean <dir>
#
# setup creates <dir> (it must not exist) with state/, config/, bin/, gh/ and
# home/ (the user home the binary sees; HOME stays real for the Keychain),
# copies <binary> into bin/ beside the orca, gh, codex, launchctl and claude
# stubs, and writes the config: brief, every workflow of
# profiles/default/workflows/ and the github and slack plugins off, ports 47910+<index> (HTTP) and
# 47810+<index> (MCP), the e2e Keychain service and <dm-channel-id> as the
# only channel posts may reach. --slack turns the slack plugin on; every start
# or pause of that directory then takes <dir>/../slack-socket.lock and refuses
# while another daemon holds it. --agent leaves claude unstubbed. The state and
# config directory name is ISOLATED_STATE_NAME, else the binary's basename, and
# setup refuses when the binary does not read the config it wrote. setup also
# writes the MCP key the daemon requires (state/<name>/mcp.key, 0600); env and
# run expose it as ISOLATED_MCP_KEY for a recipe's X-Loop-Key header.
#
# snapshot copies a store into <dir>'s state, never writing the source, at
# 0600, and marks <dir> as holding a copy: start and serve then require
# --no-jobs. The copy must carry a stored GitHub viewer.
#
# pause serves --no-jobs once, stores the pause through the CLI and stops.
# start serves in the background, logging to <dir>/daemon.log. stop signals
# only the pid start recorded, and only while that pid is still the process
# start launched. clean stops and deletes state/. run refuses any argument
# naming the user's real state or config directory, and the binary's
# install, uninstall and app subcommands.
set -euo pipefail

usage() {
	sed -n '5,13p' "$0" | sed 's/^# \{0,1\}//' >&2
	exit 2
}

die() {
	echo "isolated-daemon: $*" >&2
	exit 1
}

repo=$(cd "$(dirname "$0")/.." && pwd)

load() {
	[[ -d $1 ]] || die "$1 is not a directory this script set up"
	dir=$(cd "$1" && pwd)
	[[ -f $dir/isolated.env ]] || die "$1 is not a directory this script set up"
	slack=0 agent=0
	# shellcheck source=/dev/null
	source "$dir/isolated.env"
}

xdg_base() {
	if [[ $1 == /* ]]; then echo "$1"; else echo "$HOME/$2"; fi
}

# real_paths lists the user's own state and config directories for name,
# literally and resolved.
real_paths() {
	local p
	for p in "$(xdg_base "${XDG_STATE_HOME:-}" .local/state)/$1" "$(xdg_base "${XDG_CONFIG_HOME:-}" .config)/$1"; do
		echo "$p"
		if [[ -d $p ]]; then (cd "$p" && pwd -P); fi
	done
}

resolved() {
	local d
	d=$(dirname -- "$1")
	if [[ -d $d ]]; then echo "$(cd "$d" && pwd -P)/$(basename -- "$1")"; else echo "$1"; fi
}

refuse_real() {
	local what=$1 arg real reals=()
	shift
	while IFS= read -r real; do reals+=("$real"); done < <(real_paths "$name")
	for arg in "$@"; do
		for real in "${reals[@]}"; do
			if [[ $arg == *"$real"* || $(resolved "$arg") == "$real"* ]]; then
				die "$what names the user's real $real; isolated commands never touch it"
			fi
		done
	done
}

isolation() {
	unsets=(-u CLAUDECODE -u CODEX_HOME -u GITHUB_TOKEN -u GH_ENTERPRISE_TOKEN -u GITHUB_ENTERPRISE_TOKEN -u GH_HOST)
	local v
	for v in $(compgen -e); do
		case $v in
		ORCA_* | CLAUDE_*) unsets+=(-u "$v") ;;
		ANTHROPIC_*) ((agent)) || unsets+=(-u "$v") ;;
		esac
	done
	vars=(XDG_STATE_HOME="$dir/state" XDG_CONFIG_HOME="$dir/config"
		ORCA_USER_DATA_PATH="$dir/no-orca" PATH="$dir/bin:$PATH"
		GH_TOKEN=isolated-invalid GH_CONFIG_DIR="$dir/gh"
		GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 LOOP_USER_HOME="$dir/home"
		ISOLATED_MCP_KEY="$(cat "$dir/state/$name/mcp.key" 2>/dev/null || true)")
}

with_env() {
	local vars unsets
	isolation
	env "${unsets[@]}" "${vars[@]}" "$@"
}

started_at() {
	ps -o lstart= -p "$1" 2>/dev/null | sed 's/^ *//; s/ *$//' || true
}

# running says whether the pid recorded in <dir>/daemon.pid is still the
# process start launched.
running() {
	[[ -f $dir/daemon.pid ]] || return 1
	local pid at
	{ read -r pid; read -r at; } <"$dir/daemon.pid"
	[[ -n $pid && -n $at && $(started_at "$pid") == "$at" ]]
}

lock_path() {
	echo "$(dirname "$dir")/slack-socket.lock"
}

take_lock() {
	local lock owner
	lock=$(lock_path)
	for _ in 1 2; do
		if (set -o noclobber; printf '%s\n' "$dir" >"$lock") 2>/dev/null; then
			return 0
		fi
		owner=$(cat "$lock" 2>/dev/null || true)
		if [[ -n $owner && -f $owner/isolated.env ]] && (dir=$owner; running); then
			die "the Slack app socket is held by the daemon in $owner; stop it first"
		fi
		rm -f "$lock"
	done
	die "could not take $lock"
}

release_lock() {
	local lock
	lock=$(lock_path)
	if [[ $(cat "$lock" 2>/dev/null || true) == "$dir" ]]; then
		rm -f "$lock"
	fi
}

# needs_no_jobs refuses a serve without --no-jobs over a copied store.
needs_no_jobs() {
	[[ -f $dir/copied ]] || return 0
	local a
	for a in "$@"; do
		[[ $a == --no-jobs ]] && return 0
	done
	die "$dir holds a copy of a store; serve it only with --no-jobs"
}

json_list() {
	local first=1 item
	printf '['
	for item in "$@"; do
		((first)) || printf ', '
		first=0
		printf '"%s"' "$item"
	done
	printf ']'
}

cmd_setup() {
	local pos=() a
	slack=0 agent=0
	for a in "$@"; do
		case $a in
		--slack) slack=1 ;;
		--agent) agent=1 ;;
		--*) usage ;;
		*) pos+=("$a") ;;
		esac
	done
	[[ ${#pos[@]} -ge 3 && ${#pos[@]} -le 4 ]] || usage
	local target=${pos[0]} binary=${pos[1]} dm=${pos[2]} index=${pos[3]:-0}
	[[ ! -e $target ]] || die "$target exists; every acceptance starts from a fresh directory"
	[[ -x $binary && -f $binary ]] || die "$binary is not an executable file"
	[[ $dm =~ ^[CDG][A-Z0-9]+$ ]] || die "$dm is not a Slack channel id"
	[[ $index =~ ^[0-9]$ ]] || die "index $index is not 0-9"
	name=${ISOLATED_STATE_NAME:-$(basename "$binary")}
	[[ $name =~ ^[A-Za-z0-9._-]+$ ]] || die "state name $name has characters a directory name should not"
	refuse_real "the directory" "$target"

	mkdir -p "$target"
	dir=$(cd "$target" && pwd)
	local socket="$dir/state/$name/daemon.sock"
	if ((${#socket} >= 104)); then
		rmdir "$dir"
		die "the daemon's socket $socket would be ${#socket} bytes, over a unix socket's 103; use a shorter directory"
	fi
	mkdir -m 700 "$dir/state" "$dir/config" "$dir/bin" "$dir/gh" "$dir/home" "$dir/config/$name"
	local bin
	bin="$dir/bin/$(basename "$binary")"
	cp "$binary" "$bin"
	local stubs=(orca gh codex launchctl) stub
	((agent)) || stubs+=(claude)
	for stub in "${stubs[@]}"; do
		printf '#!/bin/sh\necho refused >&2\nexit 1\n' >"$dir/bin/$stub"
		chmod 755 "$dir/bin/$stub"
	done

	local workflows=() manifest
	for manifest in "$repo"/profiles/default/workflows/*/workflow.toml; do
		[[ -f $manifest ]] && workflows+=("$(basename "$(dirname "$manifest")")")
	done
	[[ ${#workflows[@]} -gt 0 ]] || die "no workflow under $repo/profiles/default/workflows"
	local wf="" w
	for w in "${workflows[@]}"; do
		wf+="${wf:+,}"$'\n'"    \"$w\": {\"enabled\": false}"
	done
	local slack_on=false
	((slack)) && slack_on=true

	cat >"$dir/config/$name/config.json" <<-EOF
		{
		  "http": {"port": $((47910 + index))},
		  "mcp": {"port": $((47810 + index))},
		  "rounds": {"per_pr_per_day": 1},
		  "brief": {"enabled": false},
		  "workflows": {$wf
		  },
		  "plugins": {"github": {"enabled": false}, "slack": {"enabled": $slack_on}},
		  "slack": {
		    "keychain_service": "wloop-e2e-slack",
		    "post_allowlist": $(json_list "$dm"),
		    "request_log": true
		  }
		}
	EOF
	chmod 600 "$dir/config/$name/config.json"
	mkdir -m 700 "$dir/state/$name"
	(umask 077 && od -An -tx1 -N24 /dev/urandom | tr -d ' \n' >"$dir/state/$name/mcp.key")
	printf 'binary=%q\nname=%q\nslack=%d\nagent=%d\n' "$bin" "$name" "$slack" "$agent" >"$dir/isolated.env"

	local got
	got=$(with_env "$bin" config get http.port 2>/dev/null || true)
	if [[ $got != "$((47910 + index))" ]]; then
		rm -rf "$dir"
		die "$(basename "$binary") does not read $dir/config/$name/config.json; set ISOLATED_STATE_NAME to its brand.StateDir()"
	fi
	echo "$dir"
}

cmd_env() {
	[[ $# -eq 1 ]] || usage
	load "$1"
	local vars unsets v i
	isolation
	for ((i = 1; i < ${#unsets[@]}; i += 2)); do
		printf 'unset %s\n' "${unsets[i]}"
	done
	for v in "${vars[@]}"; do
		printf 'export %s=%q\n' "${v%%=*}" "${v#*=}"
	done
}

cmd_run() {
	[[ $# -ge 2 ]] || usage
	load "$1"
	shift
	refuse_real "run" "$@"
	if [[ $# -ge 2 && ($1 == "$binary" || $1 == "$(basename "$binary")") ]]; then
		case $2 in
		install | uninstall | app) die "isolated daemons never install: $2 would reach the user's LaunchAgent" ;;
		esac
	fi
	local a
	for a in "$@"; do
		if [[ $a == serve ]]; then
			needs_no_jobs "$@"
			break
		fi
	done
	with_env "$@"
}

cmd_snapshot() {
	[[ $# -eq 2 ]] || usage
	load "$1"
	local src=$2
	[[ -f $src ]] || die "no store at $src"
	command -v sqlite3 >/dev/null || die "snapshot needs sqlite3"
	! running || die "stop the daemon of $dir first"
	dest="$dir/state/$name/store.db"
	[[ ! -e $dest ]] || die "$dest exists; snapshot into a fresh directory"
	mkdir -m 700 -p "$(dirname "$dest")"
	# A read-only open creates the -shm a stopped store lacks; copy that one.
	if [[ ! -e $src-shm ]] || ! sqlite3 "file:$src?mode=ro" "VACUUM INTO '$dest'" 2>/dev/null; then
		rm -f "$dest"
		local t="$dir/tmp"
		rm -rf "$t"
		mkdir -m 700 "$t"
		cp "$src" "$t/s.db"
		if [[ -e $src-wal ]]; then cp "$src-wal" "$t/s.db-wal"; fi
		sqlite3 "$t/s.db" "VACUUM INTO '$dest'"
		rm -rf "$t"
	fi
	chmod 600 "$dest"
	[[ $(sqlite3 "$dest" 'PRAGMA integrity_check') == ok ]] || drop_copy "the snapshot fails its integrity check"
	sqlite3 "$dest" "INSERT OR IGNORE INTO meta (key, value) SELECT 'viewer:github', value FROM meta WHERE key = 'viewer'" ||
		drop_copy "the snapshot has no meta table"
	[[ $(sqlite3 "$dest" "SELECT count(*) FROM meta WHERE key = 'viewer:github' AND value != ''") != 0 ]] ||
		drop_copy "the copy has no stored GitHub viewer; its rows cannot render"
	touch "$dir/copied"
	echo "copied $(sqlite3 "$dest" "SELECT count(*) FROM sqlite_master WHERE type = 'table'") tables"
}

drop_copy() {
	rm -f "$dest"
	die "$1"
}

launch() {
	local vars unsets
	isolation
	(exec env "${unsets[@]}" "${vars[@]}" nohup "$binary" serve "$@" </dev/null >>"$dir/daemon.log" 2>&1) &
	local pid=$!
	disown "$pid"
	local at=""
	for _ in $(seq 50); do
		at=$(started_at "$pid")
		[[ -n $at ]] && break
		sleep 0.1
	done
	[[ -n $at ]] || die "the daemon exited at once; see $dir/daemon.log"
	printf '%s\n%s\n' "$pid" "$at" >"$dir/daemon.pid"
}

# launch_locked launches with the Slack socket lock when the directory has
# the slack plugin on.
launch_locked() {
	if running; then
		die "a daemon from $dir is already running"
	fi
	if ((slack)); then take_lock; fi
	if ! (launch "$@"); then
		if ((slack)); then release_lock; fi
		exit 1
	fi
}

stop_ours() {
	if ! running; then
		rm -f "$dir/daemon.pid"
		release_lock
		echo "no daemon of $dir is running"
		return 0
	fi
	local pid
	pid=$(head -n 1 "$dir/daemon.pid")
	kill -TERM "$pid"
	for _ in $(seq 150); do
		running || break
		sleep 0.1
	done
	if running; then
		kill -KILL "$pid"
	fi
	rm -f "$dir/daemon.pid"
	release_lock
	echo "stopped $pid"
}

cmd_pause() {
	[[ $# -eq 1 ]] || usage
	load "$1"
	launch_locked --no-jobs
	local ok=0
	for _ in $(seq 120); do
		if with_env "$binary" pause >>"$dir/daemon.log" 2>&1; then
			ok=1
			break
		fi
		running || break
		sleep 0.5
	done
	stop_ours >/dev/null
	((ok)) || die "the pause did not reach the daemon; see $dir/daemon.log"
	echo "paused"
}

cmd_start() {
	[[ $# -ge 1 ]] || usage
	load "$1"
	shift
	needs_no_jobs "$@"
	launch_locked "$@"
	echo "started $(head -n 1 "$dir/daemon.pid")"
}

cmd_stop() {
	[[ $# -eq 1 ]] || usage
	load "$1"
	stop_ours
}

cmd_clean() {
	[[ $# -eq 1 ]] || usage
	load "$1"
	stop_ours >/dev/null
	rm -rf "$dir/state"
	rm -f "$dir/copied"
	echo "removed $dir/state"
}

[[ $# -ge 1 ]] || usage
sub=$1
shift
case $sub in
setup) cmd_setup "$@" ;;
env) cmd_env "$@" ;;
run) cmd_run "$@" ;;
snapshot) cmd_snapshot "$@" ;;
pause) cmd_pause "$@" ;;
start) cmd_start "$@" ;;
stop) cmd_stop "$@" ;;
clean) cmd_clean "$@" ;;
*) usage ;;
esac
