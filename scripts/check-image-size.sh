#!/bin/sh
set -eu

image="${1:-homedex:local}"
target_bytes="${HOMEDEX_IMAGE_TARGET_BYTES:-31457280}"
limit_bytes="${HOMEDEX_IMAGE_LIMIT_BYTES:-41943040}"
size="$(docker image inspect "$image" --format '{{.Size}}')"

echo "image size: $size bytes (target: <$target_bytes; hard limit: <$limit_bytes)"
if [ "$size" -ge "$limit_bytes" ]; then
  echo "error: $image exceeds the 40 MiB release limit" >&2
  exit 1
fi
if [ "$size" -ge "$target_bytes" ]; then
  echo "warning: $image is within the release limit but above the 30 MiB target" >&2
fi
