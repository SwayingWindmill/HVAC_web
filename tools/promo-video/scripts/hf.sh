#!/usr/bin/env bash
# Runs the HyperFrames CLI on the film with this repo's ffmpeg and, until the pinned Chrome is
# downloaded, Playwright's headless shell. Usage: scripts/hf.sh <command> [args...]
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
export HYPERFRAMES_NO_TELEMETRY=1 DO_NOT_TRACK=1
export HYPERFRAMES_FFMPEG_PATH="$here/node_modules/ffmpeg-static/ffmpeg"
export HYPERFRAMES_FFPROBE_PATH="$here/node_modules/ffprobe-static/bin/linux/x64/ffprobe"
if [ -z "${HYPERFRAMES_BROWSER_PATH:-}" ]; then
  shell=$(ls -d "$HOME"/.cache/ms-playwright/chromium_headless_shell-*/chrome-headless-shell-linux64/chrome-headless-shell 2>/dev/null | tail -1 || true)
  [ -n "$shell" ] && export HYPERFRAMES_BROWSER_PATH="$shell"
fi
cd "$here/film"
exec "$here/node_modules/.bin/hyperframes" "$@"
