#!/usr/bin/env bash
set -euo pipefail

CMD="${1:-pre-start}"
if [[ "${CMD}" != "pre-start" ]]; then
  echo "usage: $0 pre-start [ota-dir]" >&2
  exit 2
fi

BASE_DIR="${BASE_DIR:-/opt/hvac-edge-gateway}"
OTA_DIR="${2:-${OTA_DIR:-}}"
if [[ -z "${OTA_DIR}" ]]; then
  if [[ -f "${BASE_DIR}/ota/ota-state.json" ]]; then
    OTA_DIR="${BASE_DIR}/ota"
  elif [[ -f "${BASE_DIR}/state/ota/ota-state.json" ]]; then
    OTA_DIR="${BASE_DIR}/state/ota"
  else
    OTA_DIR="${BASE_DIR}/ota"
  fi
fi

STATE_FILE="${OTA_DIR}/ota-state.json"
if [[ ! -f "${STATE_FILE}" ]]; then
  exit 0
fi

NOW_SEC="$(date +%s 2>/dev/null || echo 0)"
if [[ ! "${NOW_SEC}" =~ ^[0-9]+$ || "${NOW_SEC}" -le 0 ]]; then
  NOW_MS=1
else
  NOW_MS=$((NOW_SEC * 1000))
fi

PREV_LINK_SLOT=""
if [[ -L "${OTA_DIR}/previous" ]]; then
  link_target="$(readlink "${OTA_DIR}/previous" 2>/dev/null || true)"
  link_base="${link_target##*/}"
  if [[ "${link_base}" == "slot-a" || "${link_base}" == "slot-b" ]]; then
    PREV_LINK_SLOT="${link_base}"
  fi
fi

TMP_STATE="${STATE_FILE}.tmp.$$"
cleanup() {
  rm -f "${TMP_STATE}" "${OTA_DIR}/current.tmp"
}
trap cleanup EXIT

GUARD_ACTION="$(
  awk -v prev_link_slot="${PREV_LINK_SLOT}" -v now_ms="${NOW_MS}" -v tmp_out="${TMP_STATE}" '
  function is_space(ch) {
    return ch == " " || ch == "\t" || ch == "\r" || ch == "\n"
  }
  function json_escape(s,    out, idx, c) {
    out = ""
    for (idx = 1; idx <= length(s); idx++) {
      c = substr(s, idx, 1)
      if (c == "\\") out = out "\\\\"
      else if (c == "\"") out = out "\\\""
      else if (c == "\n") out = out "\\n"
      else if (c == "\r") out = out "\\r"
      else if (c == "\t") out = out "\\t"
      else out = out c
    }
    return out
  }
  {
    src = src $0 "\n"
  }
  END {
    n = length(src)
    depth = 0
    in_pending = 0
    top_key = ""
    ps_key = ""
    root_open = 0
    ba_start = 0
    ba_end = 0
    i = 1
    while (i <= n) {
      ch = substr(src, i, 1)
      if (is_space(ch)) {
        i++
        continue
      }
      if (ch == "\"") {
        i++
        sval = ""
        while (i <= n) {
          c = substr(src, i, 1)
          if (c == "\\") {
            i++
            if (i <= n) {
              esc = substr(src, i, 1)
              if (esc == "n") sval = sval "\n"
              else if (esc == "r") sval = sval "\r"
              else if (esc == "t") sval = sval "\t"
              else sval = sval esc
            }
            i++
            continue
          }
          if (c == "\"") {
            i++
            break
          }
          sval = sval c
          i++
        }
        j = i
        while (j <= n && is_space(substr(src, j, 1))) j++
        if (j <= n && substr(src, j, 1) == ":") {
          if (depth == 1) {
            top_key = sval
          } else if (depth == 2 && in_pending) {
            ps_key = sval
          }
          i = j + 1
          continue
        } else {
          if (depth == 1 && top_key != "") {
            top[top_key] = sval
            top_key = ""
          } else if (depth == 2 && in_pending && ps_key != "") {
            ps[ps_key] = sval
            ps_key = ""
          }
          continue
        }
      }
      if (ch == "{") {
        if (depth == 0 && root_open == 0) root_open = i
        if (depth == 1 && top_key == "pendingStage") {
          in_pending = 1
          top_key = ""
        } else if (depth == 1) {
          top_key = ""
        } else if (depth == 2 && in_pending) {
          ps_key = ""
        }
        depth++
        i++
        continue
      }
      if (ch == "}") {
        if (depth == 2 && in_pending) in_pending = 0
        if (depth == 1) top_key = ""
        if (depth == 2) ps_key = ""
        depth--
        i++
        continue
      }
      if (ch == "[") {
        if (depth == 1) top_key = ""
        if (depth == 2 && in_pending) ps_key = ""
        depth++
        i++
        continue
      }
      if (ch == "]") {
        depth--
        i++
        continue
      }
      if (ch == "," || ch == ":") {
        if (ch == "," && depth == 1) top_key = ""
        if (ch == "," && depth == 2) ps_key = ""
        i++
        continue
      }
      tstart = i
      while (i <= n) {
        tc = substr(src, i, 1)
        if (is_space(tc) || tc == "," || tc == "}" || tc == "]") break
        i++
      }
      tval = substr(src, tstart, i - tstart)
      if (depth == 1 && top_key != "") {
        top[top_key] = tval
        if (top_key == "bootAttempts") {
          ba_start = tstart
          ba_end = i - 1
        }
        top_key = ""
      } else if (depth == 2 && in_pending && ps_key != "") {
        ps[ps_key] = tval
        ps_key = ""
      }
    }

    if (top["status"] != "HEALTH_CHECKING") {
      print "NOOP"
      exit 0
    }

    attempts = 0
    if (top["bootAttempts"] ~ /^[0-9]+$/) {
      attempts = top["bootAttempts"] + 0
    }

    if (attempts < 2) {
      next_attempts = attempts + 1
      if (ba_start > 0) {
        out = substr(src, 1, ba_start - 1) next_attempts substr(src, ba_end + 1)
      } else if (root_open > 0) {
        out = substr(src, 1, root_open) "\n  \"bootAttempts\": " next_attempts "," substr(src, root_open + 1)
      } else {
        print "NOOP"
        exit 0
      }
      printf "%s", out > tmp_out
      close(tmp_out)
      print "BUMP:" next_attempts
      exit 0
    }

    prev_slot = top["previousSlot"]
    if (prev_slot == "") prev_slot = prev_link_slot
    if (prev_slot != "slot-a" && prev_slot != "slot-b") {
      print "MISSING_PREV"
      exit 0
    }

    cur_ver = top["previousVersion"]
    if (cur_ver == "") cur_ver = top["currentVersion"]
    if (cur_ver == "") cur_ver = "0.1.0"
    reason = "boot guard rollback after 2 failed boot attempts"

    json = "{\n"
    json = json "  \"currentVersion\": \"" json_escape(cur_ver) "\",\n"
    if (top["previousVersion"] != "") {
      json = json "  \"previousVersion\": \"" json_escape(top["previousVersion"]) "\",\n"
    }
    json = json "  \"activeSlot\": \"" json_escape(prev_slot) "\",\n"
    if (top["previousSlot"] != "") {
      json = json "  \"previousSlot\": \"" json_escape(top["previousSlot"]) "\",\n"
    }
    json = json "  \"status\": \"ROLLED_BACK\",\n"
    json = json "  \"errorReason\": \"" json_escape(reason) "\""
    if (ps["campaignId"] != "" && ps["taskId"] != "" && ps["packageId"] != "") {
      target_ver = ps["version"]
      if (target_ver == "") target_ver = cur_ver
      json = json ",\n  \"pendingRollbackReport\": {\n"
      json = json "    \"campaignId\": \"" json_escape(ps["campaignId"]) "\",\n"
      json = json "    \"taskId\": \"" json_escape(ps["taskId"]) "\",\n"
      json = json "    \"packageId\": \"" json_escape(ps["packageId"]) "\",\n"
      json = json "    \"stage\": \"ROLLED_BACK\",\n"
      json = json "    \"progressPct\": 100,\n"
      json = json "    \"currentVersion\": \"" json_escape(cur_ver) "\",\n"
      json = json "    \"targetVersion\": \"" json_escape(target_ver) "\",\n"
      json = json "    \"errorCode\": \"BOOT_GUARD_ROLLBACK\",\n"
      json = json "    \"errorMessage\": \"" json_escape(reason) "\",\n"
      json = json "    \"occurredAt\": " now_ms "\n"
      json = json "  }"
    }
    json = json "\n}\n"

    printf "%s", json > tmp_out
    close(tmp_out)
    print "ROLLBACK:" prev_slot
  }' "${STATE_FILE}"
)"

case "${GUARD_ACTION}" in
  NOOP)
    exit 0
    ;;
  BUMP:*)
    chmod 600 "${TMP_STATE}"
    mv -f "${TMP_STATE}" "${STATE_FILE}"
    echo "ota-boot-guard: boot attempt ${GUARD_ACTION#BUMP:} for pending OTA update" >&2
    exit 0
    ;;
  ROLLBACK:*)
    PREV_SLOT="${GUARD_ACTION#ROLLBACK:}"
    if [[ ! -e "${OTA_DIR}/slots/${PREV_SLOT}" ]]; then
      echo "ota-boot-guard: previous slot ${OTA_DIR}/slots/${PREV_SLOT} missing; skipping rollback" >&2
      exit 0
    fi
    ln -sfn "slots/${PREV_SLOT}" "${OTA_DIR}/current.tmp"
    mv -f "${OTA_DIR}/current.tmp" "${OTA_DIR}/current"
    chmod 600 "${TMP_STATE}"
    mv -f "${TMP_STATE}" "${STATE_FILE}"
    echo "ota-boot-guard: rolled back ota/current to slots/${PREV_SLOT} after repeated failed boots" >&2
    exit 0
    ;;
  MISSING_PREV)
    echo "ota-boot-guard: previous OTA slot unknown; skipping rollback" >&2
    exit 0
    ;;
  *)
    echo "ota-boot-guard: unexpected state action ${GUARD_ACTION}" >&2
    exit 0
    ;;
esac
