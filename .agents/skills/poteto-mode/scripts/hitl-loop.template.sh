#!/usr/bin/env bash
# Human-in-the-loop reproduction loop for bug-fix.md (last resort when no
# headless/Playwright/CLI surface can reach the failing hardware or flow).
# Copy this file into .scratch/, edit the steps below, and run it.
#
# Two helpers:
#   step "<instruction>"          -> show instruction, wait for Enter
#   capture VAR "<question>"      -> show question, read response into VAR
#
# `capture` prints its value back to the terminal, where the agent reads it.
# Keep signing in and secret entry inside `step`, never `capture`, and redact
# any tokens/credentials (`[REDACTED]`) before sharing terminal logs.

set -euo pipefail

step() {
  printf '\n>>> %s\n' "$1"
  read -r -p "    [Enter when done] " _
}

capture() {
  local var="$1" question="$2" answer
  printf '\n>>> %s\n' "$question"
  read -r -p "    > " answer
  printf -v "$var" '%s' "$answer"
}

# --- edit below ---------------------------------------------------------

step "Open the target surface and sign in (do not capture credentials)."

capture ERRORED "Trigger the action under test. Did it throw or fail? (y/n)"

capture ERROR_MSG "Paste the non-secret error message or symptom (or 'none'):"

# --- edit above ---------------------------------------------------------

printf '\n--- Captured ---\n'
printf 'ERRORED=%s\n' "$ERRORED"
printf 'ERROR_MSG=%s\n' "$ERROR_MSG"
