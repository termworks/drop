#!/bin/sh
# The other machine: same drop, another chip.
#
#   misc/orin.sh deploy    build for arm64, copy it over, restart the daemon there
#   misc/orin.sh serve     restart the daemon there
#   misc/orin.sh log [n]   the last n lines it said
#   misc/orin.sh <args>    run drop there with those arguments
#
# $ORIN is where it lives, and the key is already in place.
set -e

HOST=${ORIN:-172.30.0.248}
FAR=.local/bin/drop
HERE=$(cd "$(dirname "$0")/.." && pwd)

# Anything with a heredoc or a background job goes over as a file and is run there. Quoting a
# script through two shells is how it breaks.
remote() {
  scp -q "$1" "$HOST:.drop-remote.sh"
  ssh "$HOST" 'sh $HOME/.drop-remote.sh'
}

case "$1" in
deploy)
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$HERE/target/drop-arm64" "$HERE/src"
  scp -q "$HERE/target/drop-arm64" "$HOST:$FAR.new"
  ssh "$HOST" "mv -f \$HOME/$FAR.new \$HOME/$FAR && chmod +x \$HOME/$FAR"
  "$0" serve
  ;;
serve)
  script=$(mktemp)
  cat > "$script" <<'SH'
pkill -f "drop serve" >/dev/null 2>&1
sleep 1
setsid $HOME/.local/bin/drop serve </dev/null >$HOME/drop-serve.log 2>&1 &
sleep 6
pgrep -f "drop serve" >/dev/null || { echo "it did not start"; tail -20 $HOME/drop-serve.log; exit 1; }
tail -3 $HOME/drop-serve.log
SH
  remote "$script"
  rm -f "$script"
  ;;
log)
  ssh "$HOST" "tail -${2:-40} \$HOME/drop-serve.log"
  ;;
*)
  ssh "$HOST" "\$HOME/$FAR $*"
  ;;
esac
