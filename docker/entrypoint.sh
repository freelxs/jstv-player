#!/bin/sh
set -eu
dir="${JSTV_DATA_DIR:-/data}"
mkdir -p "$dir"
if [ "$(id -u)" = "0" ]; then
  chown -R node:node "$dir"
  exec gosu node /usr/local/bin/jstv-player --web
fi
exec /usr/local/bin/jstv-player --web
