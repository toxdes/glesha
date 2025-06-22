#!/bin/bash
set -e

BUILD_DIR=build

for dir in "$BUILD_DIR"/*; do
  [ -d "$dir" ] || continue

  os_arch=$(basename "$dir")
  out_dir="$dir/out"
  bin_name="glesha"

  if [[ "$os_arch" == windows* ]]; then
    bin_name+=".exe"
  fi

  if [ -f "$out_dir/$bin_name" ]; then
    mv "$out_dir/$bin_name" "$dir/glesha"
    rm -rf "$out_dir"
  else
    echo "Binary not found in $out_dir"
  fi
done

DIST_DIR=dist

mkdir -p "$DIST_DIR"

for dir in "$BUILD_DIR"/*; do
  [ -d "$dir" ] || continue
  base=$(basename "$dir")

  if [[ "$base" == windows* || "$base" == darwin* ]]; then
    zip -r "$DIST_DIR/${base}.zip" -j "$dir/"*
  else
    tar czf "$DIST_DIR/${base}.tar.gz" -C "$dir/" .
  fi
done
