#!/usr/bin/env bash
# Install or upgrade Rough Country Media on the Mac Mini from this Mac, in one command:
#
#   ./deploy.sh you@mini.local              guided install (or upgrade, keeping settings)
#   ./deploy.sh you@mini.local --reconfigure change folders or port
#
# It builds the Linux version, copies it to the Mini over SSH and runs the
# installer there. You'll be asked for your password on the Mini (for sudo).
set -euo pipefail

if [[ $# -lt 1 || "$1" == -* ]]; then
  echo "usage: ./deploy.sh USER@HOST [installer options]"
  echo "  e.g. ./deploy.sh wolf@mini.local"
  exit 2
fi
host="$1"
shift

cd "$(dirname "$0")"

echo "→ building for the Mini"
case "${RCM_ARCH:-amd64}" in
  amd64) make -s linux ;;
  arm64) make -s linux-arm64 ;;
  *) echo "RCM_ARCH must be amd64 or arm64" >&2; exit 2 ;;
esac
bin="dist/rcm-linux-${RCM_ARCH:-amd64}"

echo "→ checking the connection to $host"
if ! ssh -o ConnectTimeout=10 "$host" true; then
  echo
  echo "Couldn't connect to $host over SSH. Check that:"
  echo "  • the Mini is on and on the same network"
  echo "  • OpenSSH server is installed on it:  sudo apt install openssh-server"
  echo "  • the name is right (try its IP address, e.g. you@192.168.1.50)"
  exit 1
fi

echo "→ copying RCM to $host"
remote_dir="$(ssh "$host" mktemp -d /tmp/rcm-deploy.XXXXXX)"
trap 'ssh "$host" rm -rf "$remote_dir" >/dev/null 2>&1 || true' EXIT
scp -q "$bin" "$host:$remote_dir/rcm"

echo "→ starting the installer on $host"
echo
# -t gives the installer a terminal so it can ask questions and sudo can ask for the password.
quoted_args=""
for a in "$@"; do quoted_args+=" $(printf '%q' "$a")"; done
ssh -t "$host" "chmod +x '$remote_dir/rcm' && sudo '$remote_dir/rcm' install$quoted_args"
