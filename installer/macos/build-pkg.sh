#!/bin/sh
# Build the LPS macOS installer (run on macOS; needs pkgbuild and productbuild).
#
#   installer/macos/build-pkg.sh <lpsd binary> <version> <out dir>
#
# <lpsd binary> should be universal (lipo -create of the arm64 and amd64
# builds). <version> is an LPS version or git describe output. Writes
# <out dir>/LPS-<version>-macOS.pkg and a .sha256 next to it.
#
# Set LPS_MAC_INSTALLER_IDENTITY to a "Developer ID Installer: ..." identity
# in the keychain to sign the package.
set -eu

[ $# -eq 3 ] || { echo "usage: $0 <lpsd binary> <version> <out dir>" >&2; exit 2; }
bin=$1 version=${2#v} out=$3
here=$(cd "$(dirname "$0")" && pwd)
[ -f "$bin" ] || { echo "no lpsd binary at $bin" >&2; exit 1; }

# Installer versions are numbers only: 1.2.3-beta.1 and 1.2.3-4-gabc -> 1.2.3.
pkgver=$(printf '%s' "$version" | sed -nE 's/^([0-9]+\.[0-9]+\.[0-9]+).*/\1/p')
[ -n "$pkgver" ] || pkgver=0.0.0
name=$(printf '%s' "$version" | tr -c 'A-Za-z0-9.+-' '-')

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/root/usr/local/bin" "$work/scripts" "$out"
install -m 755 "$bin" "$work/root/usr/local/bin/lpsd"
install -m 755 "$here/lps-uninstall" "$work/root/usr/local/bin/lps-uninstall"
install -m 755 "$here/scripts/preinstall" "$here/scripts/postinstall" "$work/scripts/"
sed "s/@VERSION@/$pkgver/" "$here/distribution.xml" >"$work/distribution.xml"

pkgbuild --quiet --root "$work/root" --scripts "$work/scripts" \
	--identifier com.abrardw.lps --version "$pkgver" --install-location / \
	"$work/lps.pkg"

pkg="$out/LPS-$name-macOS.pkg"
set -- --quiet --distribution "$work/distribution.xml" --resources "$here/resources" --package-path "$work"
if [ -n "${LPS_MAC_INSTALLER_IDENTITY:-}" ]; then
	set -- "$@" --sign "$LPS_MAC_INSTALLER_IDENTITY"
fi
productbuild "$@" "$pkg"
(cd "$out" && shasum -a 256 "$(basename "$pkg")" >"$(basename "$pkg").sha256")
echo "$pkg"
