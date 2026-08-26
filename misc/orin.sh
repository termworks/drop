#!/bin/sh
# Cross-architecture testbed: this machine is x86_64, orin is aarch64.
#
#   misc/orin.sh deploy    build for arm64, copy it over, restart the daemon there
#   misc/orin.sh serve     restart the daemon there
#   misc/orin.sh log       tail what it said
#   misc/orin.sh <args>    run drop there with those arguments
set -e

HOST=${ORIN:-172.30.0.248}
FAR=/home/bresilla/.local/bin/drop
HERE=$(cd "$(dirname "$0")/.." && pwd)

case "$1" in
deploy)
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$HERE/target/drop-arm64" "$HERE/src"
  scp -q "$HERE/target/drop-arm64" "$HOST:$FAR.new"
  ssh "$HOST" "mv -f $FAR.new $FAR && chmod +x $FAR"
  "$0" serve
  ;;
serve)
  ssh "$HOST" "sh -c 'pkill -f \"drop serve\" >/dev/null 2>&1; sleep 1; setsid $FAR serve </dev/null >\$HOME/drop-serve.log 2>&1 &'"
  sleep 6
  ssh "$HOST" "tail -3 \$HOME/drop-serve.log"
  ;;
log)
  ssh "$HOST" "tail -${2:-40} \$HOME/drop-serve.log"
  ;;
*)
  ssh "$HOST" "$FAR $*"
  ;;
esac
