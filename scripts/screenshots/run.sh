#!/bin/sh
# Retakes the screenshots in docs/images from a real, running LPS.
#
#   scripts/screenshots/run.sh            (or: make screenshots)
#
# It builds lpsd, starts it with a throwaway data folder and a fake Tailscale
# CLI, creates a demo (VPS dev-box, app portfolio, app notes-api inside the
# VPS), saves the screenshots and deletes the demo again.
#
# Needs: Docker running, Go, Node.js with Playwright and Chromium
# (require("playwright") must work), Python 3, and internet access for the
# Ubuntu, nginx and Node.js downloads. Run it where no other LPS is running:
# it uses the usual ports (7070, 2201, 8100…).
#
# Environment:
#   LPS_SHOTS_PROJECTS  where the demo projects are written; their path shows
#                       in the screenshots (default /home/you/projects)
#   LPS_SHOTS_KEEP=1    keep the demo and lpsd running afterwards
#
# docs/images/setup-wizard.png isn't retaken: it shows the Windows checks
# (WSL2), which only appear on Windows.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
out="$repo/docs/images"
projects=${LPS_SHOTS_PROJECTS:-/home/you/projects}
addr=127.0.0.1:7070

if ! docker info >/dev/null 2>&1; then
	echo "Docker isn't running." >&2
	exit 1
fi
if curl -s -o /dev/null "http://$addr/api/health"; then
	echo "Something already answers on $addr: stop your LPS first." >&2
	exit 1
fi
mkdir -p "$projects" || {
	echo "Can't create $projects: set LPS_SHOTS_PROJECTS to a folder you can write." >&2
	exit 1
}

work=$(mktemp -d)
mkdir -p "$work/bin" "$work/data"
cp "$here/fake-tailscale" "$work/bin/tailscale"
chmod +x "$work/bin/tailscale"

echo "[screenshots] building lpsd"
(cd "$repo" && make all >/dev/null)

echo "[screenshots] starting lpsd (log: $work/lpsd.log)"
PATH="$work/bin:$PATH" \
	LPS_FAKE_TS_STATUS="$repo/internal/tunnel/testdata/status_ready.json" \
	LPS_FAKE_TS_DIR="$work" \
	"$repo/bin/lpsd" serve -addr "$addr" -data "$work/data" -no-update-check >"$work/lpsd.log" 2>&1 &
lpsd=$!
stop_lpsd() {
	if [ "${LPS_SHOTS_KEEP:-}" = 1 ]; then
		echo "[screenshots] lpsd keeps running (pid $lpsd) at http://$addr, user admin, password demo-password-123"
	else
		kill "$lpsd" 2>/dev/null || true
	fi
}
trap stop_lpsd EXIT

i=0
until curl -s -o /dev/null "http://$addr/api/health"; do
	i=$((i + 1))
	if [ $i -gt 50 ]; then
		echo "lpsd didn't start; its log:" >&2
		cat "$work/lpsd.log" >&2
		exit 1
	fi
	sleep 0.2
done

node "$here/shoot.mjs" "http://$addr" "$out" "$projects"
