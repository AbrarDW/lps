#!/bin/sh
# lps-app: runs an LPS app's process inside a VPS. lpsd installs this file and
# calls it as the VPS's SSH user:
#
#   lps-app start  <name>          env: LPS_DIR, LPS_PORT, LPS_START
#   lps-app stop   <name>
#   lps-app status <name>          prints "running" or "stopped"
#   lps-app logs   <name> [lines]
#
# The start command runs in its own session (so stop can end the whole
# process group) with output appended to ~/.lps/logs/<name>.log.
set -eu

cmd=${1:?command}
name=${2:?app name}
base="$HOME/.lps"
pidf="$base/run/$name.pid"
logf="$base/logs/$name.log"
mkdir -p "$base/run" "$base/logs"

# The pid file holds "<pid> <start time>": the start time tells a live app
# from an unrelated process that reused the pid after the VPS restarted.
starttime() { cut -d' ' -f22 "/proc/$1/stat" 2>/dev/null || true; }

alive() {
	[ -f "$pidf" ] || return 1
	read -r pid st <"$pidf" || return 1
	[ -n "$pid" ] && [ "$(starttime "$pid")" = "$st" ]
}

stop() {
	if [ -f "$pidf" ] && read -r pid st <"$pidf" && [ -n "$pid" ] && [ "$(starttime "$pid")" = "$st" ]; then
		kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
		i=0
		while [ "$(starttime "$pid")" = "$st" ] && [ $i -lt 100 ]; do
			sleep 0.1
			i=$((i + 1))
		done
		kill -KILL -- "-$pid" 2>/dev/null || true
	fi
	rm -f "$pidf"
}

case $cmd in
start)
	if alive; then
		echo running
		exit 0
	fi
	cd "${LPS_DIR:?}"
	# Keep the log from growing without bound: rotate at ~5 MB.
	if [ -f "$logf" ] && [ "$(wc -c <"$logf")" -gt 5000000 ]; then
		mv -f "$logf" "$logf.1"
	fi
	printf '\n[lps] %s starting: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "${LPS_START:?}" >>"$logf"
	PORT="${LPS_PORT:?}" HOST=0.0.0.0 setsid bash -lc "$LPS_START" </dev/null >>"$logf" 2>&1 &
	pid=$!
	echo "$pid $(starttime "$pid")" >"$pidf"
	echo started
	;;
stop)
	stop
	echo stopped
	;;
status)
	if alive; then echo running; else echo stopped; fi
	;;
logs)
	tail -n "${3:-300}" "$logf" 2>/dev/null || true
	;;
*)
	echo "unknown command: $cmd" >&2
	exit 2
	;;
esac
