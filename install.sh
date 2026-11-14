#!/usr/bin/env bash
# Install or upgrade Rough Country Media on a Linux machine, straight from GitHub:
#
#   curl -fsSL https://raw.githubusercontent.com/ulfr-wolf/RoughCountryMedia/main/install.sh | sudo bash
#
# It downloads the latest release for this machine, checks its SHA-256
# checksum, and runs the guided installer (`rcm install`). Options after
# `bash -s --` are passed through, e.g. `| sudo bash -s -- --reconfigure`.
set -euo pipefail

repo="ulfr-wolf/RoughCountryMedia"

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "This installer is for Linux. On a Mac, use ./deploy.sh to install onto the Mini." >&2
  exit 1
fi
if [[ "$(id -u)" -ne 0 ]]; then
  echo "Please run with sudo:  curl -fsSL https://raw.githubusercontent.com/$repo/main/install.sh | sudo bash" >&2
  exit 1
fi
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "Sorry, there's no RCM build for $(uname -m) yet." >&2; exit 1 ;;
esac
for tool in curl sha256sum; do
  command -v "$tool" >/dev/null || { echo "Missing $tool; install it first (sudo apt install $tool)." >&2; exit 1; }
done

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
base="https://github.com/$repo/releases/latest/download"
echo "→ downloading the latest Rough Country Media for $arch"
curl -fsSL --retry 3 -o "$tmp/rcm" "$base/rcm-linux-$arch" || {
  echo "Download failed. Is there a release at https://github.com/$repo/releases ?" >&2
  exit 1
}
curl -fsSL --retry 3 -o "$tmp/rcm.sha256" "$base/rcm-linux-$arch.sha256"

echo "→ verifying the download"
expected="$(cut -d' ' -f1 <"$tmp/rcm.sha256")"
actual="$(sha256sum "$tmp/rcm" | cut -d' ' -f1)"
if [[ -z "$expected" || "$expected" != "$actual" ]]; then
  echo "Checksum mismatch: the download is corrupt or was tampered with. Nothing was installed." >&2
  exit 1
fi
chmod +x "$tmp/rcm"

# When piped from curl, stdin is this script, so read answers from the terminal instead.
if [[ -r /dev/tty ]]; then
  "$tmp/rcm" install "$@" </dev/tty
else
  "$tmp/rcm" install --yes "$@"
fi
