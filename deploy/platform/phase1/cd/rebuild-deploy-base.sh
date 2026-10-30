#!/usr/bin/env bash
# Rebuild one service's deploy base from the image that is running now, then prove it is the same image.
#
#   rebuild-deploy-base.sh <running image> <kind> <output tag>
#
# apply-on-server.sh runs this for every non-static service when the deploy is started with
# rebuild_base (HVAC_REBUILD_DEPLOY_BASE=1). The output is one fresh layer holding the running image's
# whole filesystem (FROM scratch + COPY --from), with the running image's config written back out
# (ENV, WORKDIR, EXPOSE, STOPSIGNAL, VOLUME, HEALTHCHECK, LABEL, ENTRYPOINT, CMD, USER).
# The migrator is the one exception: it stays on the postgres image (HVAC_MIGRATOR_BASE_IMAGE) and
# only /opt/hvac is copied over, the same shape as deploy/platform/phase1/migrations/Dockerfile.
#
# The new image carries no org.hvac.deploy-base label; org.hvac.flattened-from records the source ID.
# Before it is accepted the config is compared field by field and the filesystems of both images are
# exported and compared file by file (path, type, mode, owner, size, link target, sha256; not mtime).
# Any difference removes the output tag and exits non-zero. Containers made for the export are removed
# with docker rm -v, so their anonymous volumes do not pile up.
set -euo pipefail

image="${1:-}"
kind="${2:-}"
output="${3:-}"
if [[ -z "$image" || -z "$kind" || -z "$output" ]]; then
  echo "error: usage: rebuild-deploy-base.sh <running image> <kind> <output tag>" >&2
  exit 1
fi
if [[ "$kind" == static ]]; then
  echo "error: static images have no deploy base." >&2
  exit 1
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "error: rebuilding a deploy base needs python3 on the host." >&2
  exit 1
fi
migrator_base="${HVAC_MIGRATOR_BASE_IMAGE:-postgres:16.4-bookworm}"

# Prints the Dockerfile for the rebuild from `docker image inspect` of the running image.
dockerfile_py='
import json, subprocess, sys
image, kind, migrator_base = sys.argv[1:4]
def inspect(fmt):
    return subprocess.check_output(["docker", "image", "inspect", "--format", fmt, image]).decode().strip()
cfg = json.loads(inspect("{{json .Config}}")) or {}
source_id = inspect("{{.Id}}")
q = json.dumps
def dur(ns):
    ns = int(ns or 0)
    return f"{ns // 1_000_000_000}s" if ns % 1_000_000_000 == 0 else f"{ns // 1_000_000}ms"
out = [f"FROM {image} AS current"]
if kind == "migrator":
    out += [f"FROM {migrator_base}", "COPY --from=current /opt/hvac /opt/hvac"]
else:
    out += ["FROM scratch", "COPY --from=current / /"]
for entry in cfg.get("Env") or []:
    key, _, value = entry.partition("=")
    out.append(f"ENV {key}={q(value)}")
workdir = cfg.get("WorkingDir")
if workdir:
    out.append("WORKDIR " + workdir)
for port in sorted(cfg.get("ExposedPorts") or {}):
    out.append(f"EXPOSE {port}")
stop_signal = cfg.get("StopSignal")
if stop_signal:
    out.append("STOPSIGNAL " + stop_signal)
for volume in sorted(cfg.get("Volumes") or {}):
    out.append(f"VOLUME {q([volume])}")
hc = cfg.get("Healthcheck") or {}
test = hc.get("Test") or []
if test:
    opts = "".join(f" --{name}={dur(hc[key])}" for name, key in (("interval", "Interval"), ("timeout", "Timeout"), ("start-period", "StartPeriod"), ("start-interval", "StartInterval")) if hc.get(key))
    if hc.get("Retries"):
        opts += " --retries=" + str(hc["Retries"])
    if test[0] == "CMD-SHELL":
        out.append(f"HEALTHCHECK{opts} CMD {test[1]}")
    elif test[0] == "CMD":
        out.append(f"HEALTHCHECK{opts} CMD {q(test[1:])}")
    elif test[0] == "NONE":
        out.append("HEALTHCHECK NONE")
for key, value in sorted((cfg.get("Labels") or {}).items()):
    if key in ("org.hvac.deploy-base", "org.hvac.flattened-from"):
        continue
    out.append(f"LABEL {q(key)}={q(value)}")
out.append("LABEL " + q("org.hvac.flattened-from") + "=" + q(source_id))
entrypoint, cmd, user = cfg.get("Entrypoint"), cfg.get("Cmd"), cfg.get("User")
if entrypoint is not None:
    out.append("ENTRYPOINT " + q(entrypoint))
if cmd is not None:
    out.append("CMD " + q(cmd))
elif entrypoint is not None and kind == "migrator":
    # FROM postgres brings its CMD ["postgres"]; the running migrator has none.
    out.append("CMD []")
if user:
    out.append("USER " + user)
print("\n".join(out))
'

# Prints the config fields that differ between two images (labels minus the deploy bookkeeping ones).
config_diff_py='
import json, subprocess, sys
def config(ref):
    cfg = json.loads(subprocess.check_output(["docker", "image", "inspect", "--format", "{{json .Config}}", ref])) or {}
    labels = {k: v for k, v in (cfg.get("Labels") or {}).items() if k not in ("org.hvac.deploy-base", "org.hvac.flattened-from")}
    fields = {k: cfg.get(k) or None for k in ("User", "Env", "Entrypoint", "Cmd", "WorkingDir", "ExposedPorts", "StopSignal", "Healthcheck", "Volumes")}
    fields["Labels"] = labels or None
    return fields
a, b = config(sys.argv[1]), config(sys.argv[2])
for key in a:
    if a[key] != b[key]:
        print(f"{key}: {json.dumps(a[key])} != {json.dumps(b[key])}")
'

# Reads a tar stream on stdin and prints one sorted line per entry, without mtimes.
manifest_py='
import hashlib, sys, tarfile
rows = []
with tarfile.open(fileobj=sys.stdin.buffer, mode="r|") as tf:
    for m in tf:
        digest = ""
        if m.isfile():
            f = tf.extractfile(m)
            h = hashlib.sha256()
            for chunk in iter(lambda: f.read(1 << 20), b""):
                h.update(chunk)
            digest = h.hexdigest()
        name = m.name.rstrip("/")
        rows.append(f"{name}\t{m.type!r}\t{oct(m.mode)}\t{m.uid}:{m.gid}\t{m.size if m.isfile() else 0}\t{m.linkname}\t{digest}")
print("\n".join(sorted(rows)))
'

work="$(mktemp -d)"
containers=()
accepted=0
cleanup() {
  local container
  for container in "${containers[@]+"${containers[@]}"}"; do
    docker rm -v "$container" >/dev/null 2>&1 || true
  done
  rm -rf "$work"
  if (( ! accepted )); then
    docker image rm "$output" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

mkdir -p "$work/context"
python3 -c "$dockerfile_py" "$image" "$kind" "$migrator_base" > "$work/context/Dockerfile"
echo "rebuilding the deploy base of $image as $output"
DOCKER_BUILDKIT=1 docker build --no-cache --pull=false -t "$output" "$work/context"

differences="$(python3 -c "$config_diff_py" "$image" "$output")"
if [[ -n "$differences" ]]; then
  echo "error: the rebuilt base of $image has a different config:" >&2
  printf '%s\n' "$differences" >&2
  exit 1
fi

manifest_of() {
  local ref="$1"
  local container
  container="$(docker create "$ref" </dev/null)"
  containers+=("$container")
  docker export "$container" | python3 -c "$manifest_py"
}
manifest_of "$image" > "$work/running.manifest"
manifest_of "$output" > "$work/rebuilt.manifest"
if ! cmp -s "$work/running.manifest" "$work/rebuilt.manifest"; then
  echo "error: the rebuilt base of $image has different files:" >&2
  diff "$work/running.manifest" "$work/rebuilt.manifest" | grep '^[<>]' | cut -f1 | head -n 20 >&2 || true
  exit 1
fi

accepted=1
echo "verified $output: same config and files as $image"
