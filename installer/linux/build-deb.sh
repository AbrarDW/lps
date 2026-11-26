#!/bin/sh
# Build the LPS .deb from an already built lpsd binary.
#
#   installer/linux/build-deb.sh <lpsd binary> <amd64|arm64> <version> <out dir>
#
# <version> is an LPS version (1.2.3, v1.2.3-beta.1) or git describe output
# (v1.2.3-4-gabc1234-dirty); anything else (dev, a bare commit) becomes 0.0.0.
# Writes <out dir>/lps_<debian version>_<arch>.deb and a .sha256 next to it.
set -eu

[ $# -eq 4 ] || { echo "usage: $0 <lpsd binary> <amd64|arm64> <version> <out dir>" >&2; exit 2; }
bin=$1 arch=$2 version=$3 out=$4
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)

case $arch in amd64 | arm64) ;; *) echo "unsupported architecture: $arch" >&2; exit 2 ;; esac
[ -f "$bin" ] || { echo "no lpsd binary at $bin" >&2; exit 1; }

# Debian version: 1.2.3-beta.1 -> 1.2.3~beta.1 (sorts before 1.2.3), a build
# after a tag 1.2.3-4-gabc -> 1.2.3+4.gabc (sorts after it).
v=${version#v}
dirty=
case $v in *-dirty) v=${v%-dirty} dirty=.dirty ;; esac
debver=$(printf '%s\n' "$v" | sed -E \
	-e 's/^([0-9]+\.[0-9]+\.[0-9]+)-([0-9]+)-g([0-9a-f]+)$/\1+\2.g\3/' \
	-e 's/^([0-9]+\.[0-9]+\.[0-9]+)-([0-9A-Za-z.]+)$/\1~\2/')
case $debver in
[0-9]*.[0-9]*.[0-9]*) ;;
*) debver="0.0.0+$(printf '%s' "$v" | tr -c 'A-Za-z0-9.' '.')" ;;
esac
if [ -n "$dirty" ]; then
	case $debver in *+*) debver=$debver$dirty ;; *) debver=$debver+dirty ;; esac
fi

stage=$(mktemp -d)
chmod 755 "$stage"
trap 'rm -rf "$stage"' EXIT
install -Dm755 "$bin" "$stage/usr/bin/lpsd"
install -Dm644 "$here/lps.service" "$stage/usr/lib/systemd/user/lps.service"
install -Dm644 "$root/LICENSE" "$stage/usr/share/doc/lps/copyright"
install -d "$stage/DEBIAN"
install -m755 "$here/debian/postinst" "$here/debian/prerm" "$stage/DEBIAN/"
size=$(du -sk "$stage/usr" | cut -f1)
cat >"$stage/DEBIAN/control" <<CONTROL
Package: lps
Version: $debver
Architecture: $arch
Maintainer: AbrarDW <AbrarDW@users.noreply.github.com>
Installed-Size: $size
Section: admin
Priority: optional
Homepage: https://github.com/AbrarDW/lps
Recommends: docker-ce | docker.io | docker-desktop
Suggests: tailscale, cloudflared
Description: Local Personal Server: your PC as a VPS host
 LPS runs Linux VPS (Ubuntu containers you SSH into) and web app
 deployments on your own hardware, managed from a web dashboard at
 http://127.0.0.1:7070. It runs as a systemd user service for each user
 who enables it, and needs Docker.
CONTROL

mkdir -p "$out"
deb="$out/lps_${debver}_${arch}.deb"
dpkg-deb --root-owner-group -Zxz --build "$stage" "$deb" >/dev/null
(cd "$out" && sha256sum "$(basename "$deb")" >"$(basename "$deb").sha256")
echo "$deb"
