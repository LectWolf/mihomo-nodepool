#!/bin/sh
set -eu

if [ "$(id -u)" = "0" ]; then
  mkdir -p "${DATA_DIR:-/app/data}"
  chown -R nodepool:nodepool "${DATA_DIR:-/app/data}"
  exec su-exec nodepool:nodepool "$@"
fi

exec "$@"
