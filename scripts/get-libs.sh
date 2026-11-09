#!/bin/sh
# Fetches the Arduino libraries the firmware needs into firmware/libraries (pinned, not committed).
set -e
dir="$(dirname "$0")/../firmware/libraries"
mkdir -p "$dir"
if [ ! -d "$dir/codec-opus" ]; then
  git clone -q https://github.com/pschatzmann/codec-opus.git "$dir/codec-opus"
fi
git -C "$dir/codec-opus" -c advice.detachedHead=false checkout -q c02de63
