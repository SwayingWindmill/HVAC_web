package cd

import (
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func writeExe(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func commandEnv(bin string, extra ...string) []string {
	kept := make([]string, 0, len(os.Environ())+len(extra)+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "PATH=") {
			continue
		}
		kept = append(kept, entry)
	}
	kept = append(kept, "PATH="+bin+":"+os.Getenv("PATH"))
	return append(kept, extra...)
}

func TestPruneKeepsTheTwoNewestRollbackSets(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "docker.log")
	writeExe(t, bin, "docker", `#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
if [ "$1" = images ] && [ "$2" = --format ] && [ "$3" = '{{.Tag}}' ]; then
  printf '%s\n' before-20261020T000000Z before-20261027T000000Z 0.1.0-dev before-20261029T000000Z
  exit 0
fi
if [ "$1" = images ]; then
  printf '%s\t%s\n' hvac/energy-api before-20261020T000000Z
  printf '%s\t%s\n' hvac/nginx before-20261020T000000Z
  printf '%s\t%s\n' hvac/energy-api before-20261027T000000Z
  printf '%s\t%s\n' hvac/energy-api before-20261029T000000Z
  printf '%s\t%s\n' hvac/energy-api 0.1.0-dev
  printf '%s\t%s\n' hvac/energy-api deploy-base
  exit 0
fi
exit 0
`)
	cmd := exec.Command("bash", "prune-rollback-images.sh", "2")
	cmd.Dir = "."
	cmd.Env = commandEnv(bin, "DOCKER_LOG="+log)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prune: %v\n%s", err, output)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, "image rm hvac/energy-api:before-20261020T000000Z") || !strings.Contains(text, "image rm hvac/nginx:before-20261020T000000Z") {
		t.Fatalf("oldest set was not removed:\n%s", text)
	}
	if strings.Contains(text, "before-20261027T000000Z") && strings.Contains(text, "image rm hvac/energy-api:before-20261027T000000Z") {
		t.Fatalf("a kept set was removed:\n%s", text)
	}
	if strings.Contains(text, "image rm hvac/energy-api:0.1.0-dev") || strings.Contains(text, "image rm hvac/energy-api:before-20261029T000000Z") {
		t.Fatalf("running or newest tag was removed:\n%s", text)
	}
	if strings.Contains(text, "image rm hvac/energy-api:deploy-base") {
		t.Fatalf("the stable deploy base was removed:\n%s", text)
	}
	if !strings.Contains(text, "image prune -f") || !strings.Contains(text, "builder prune -f --filter until=2h") {
		t.Fatalf("dangling image or build cache prune missing:\n%s", text)
	}
}

// fakeImageStore is a docker stand-in that keeps tags, parents, labels and layer counts on disk,
// enough for apply-on-server.sh to build candidates and retag them.
func fakeImageStore(t *testing.T, bin string) string {
	t.Helper()
	store := t.TempDir()
	writeExe(t, bin, "docker", `#!/usr/bin/env bash
set -euo pipefail
s="$DOCKER_STORE"
mkdir -p "$s/tags" "$s/images"
printf '%s\n' "$*" >> "$s/calls.log"
key() { printf '%s' "$1" | tr '/:' '__'; }
resolve() {
  if [[ -d "$s/images/$1" ]]; then printf '%s\n' "$1"; return 0; fi
  if [[ -f "$s/tags/$(key "$1")" ]]; then cat "$s/tags/$(key "$1")"; return 0; fi
  return 1
}
case "$1" in
  ps) exit 0 ;;
  tag) id="$(resolve "$2")"; printf '%s\n' "$id" > "$s/tags/$(key "$3")" ;;
  image)
    case "$2" in
      rm) rm -f "$s/tags/$(key "$3")" ;;
      prune) exit 0 ;;
      inspect)
        if [[ "$3" == --format ]]; then
          id="$(resolve "$5")" || exit 1
          case "$4" in
            *Labels*) cat "$s/images/$id/label" 2>/dev/null || printf '<no value>\n' ;;
            *.Id*) printf '%s\n' "$id" ;;
          esac
        else
          resolve "$3" >/dev/null || exit 1
        fi ;;
    esac ;;
  build)
    tag="$4"; ctx="$5"
    from="$(sed -n 's/^FROM //p' "$ctx/Dockerfile")"
    parent="$(resolve "$from")"
    id="sha256:$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
    mkdir -p "$s/images/$id"
    echo $(( $(cat "$s/images/$parent/layers") + 1 )) > "$s/images/$id/layers"
    sed -n 's/^LABEL org.hvac.deploy-base=//p' "$ctx/Dockerfile" > "$s/images/$id/label"
    [[ -s "$s/images/$id/label" ]] || rm "$s/images/$id/label"
    printf '%s\n' "$id" > "$s/tags/$(key "$tag")"
    printf 'build %s FROM %s\n' "$tag" "$from" >> "$s/builds.log" ;;
esac
`)
	return store
}

func seedImage(t *testing.T, store, tag string, layers int, label string) string {
	t.Helper()
	id := "sha256:seed" + strings.NewReplacer("/", "", ":", "", ".", "").Replace(tag)
	dir := filepath.Join(store, "images", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layers"), []byte(strconv.Itoa(layers)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if label != "" {
		if err := os.WriteFile(filepath.Join(dir, "label"), []byte(label+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(store, "tags"), 0o755); err != nil {
		t.Fatal(err)
	}
	key := strings.NewReplacer("/", "_", ":", "_").Replace(tag)
	if err := os.WriteFile(filepath.Join(store, "tags", key), []byte(id+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return id
}

func imageOf(t *testing.T, store, tag string) (id string, layers int, label string) {
	t.Helper()
	key := strings.NewReplacer("/", "_", ":", "_").Replace(tag)
	id = strings.TrimSpace(string(mustRead(t, filepath.Join(store, "tags", key))))
	layers, err := strconv.Atoi(strings.TrimSpace(string(mustRead(t, filepath.Join(store, "images", id, "layers")))))
	if err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(store, "images", id, "label")); err == nil {
		label = strings.TrimSpace(string(body))
	}
	return id, layers, label
}

// applyRelease stages a one-service release whose helpers are stubs and runs apply-on-server.sh.
func applyRelease(t *testing.T, bin, store, platform string, run int) string {
	t.Helper()
	release := t.TempDir()
	for _, dir := range []string{"bins/energy-api", "src"} {
		if err := os.MkdirAll(filepath.Join(release, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"REVISION":                 strings.Repeat("a", 40) + "\n",
		"services.tsv":             "energy-api\thvac/energy-api:0.1.0-dev\tgo\trequired\t./cmd/energy-api\n",
		"bins/healthcheck":         "hc",
		"bins/energy-api/service":  "svc" + strconv.Itoa(run),
		"src/README":               "tree",
		"rollback.sh":              "#!/bin/sh\nexit 0\n",
		"smoke-release.sh":         "#!/bin/sh\nexit 0\n",
		"prune-rollback-images.sh": "#!/bin/sh\nexit 0\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(release, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("tar", "-C", filepath.Join(release, "src"), "-cf", filepath.Join(release, "source.tar"), ".").CombinedOutput(); err != nil {
		t.Fatalf("tar: %v\n%s", err, out)
	}
	cmd := exec.Command("bash", "apply-on-server.sh", release)
	cmd.Env = commandEnv(bin, "DOCKER_STORE="+store, "HVAC_PLATFORM_DIR="+platform, "HVAC_DEPLOY_SCRIPT="+writeExe(t, bin, "restart", "#!/bin/sh\nexit 0\n"), "FAKE_STAMP=2026102"+strconv.Itoa(run)+"T000000Z")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("apply run %d: %v\n%s", run, err, output)
	}
	return string(output)
}

func applyFixture(t *testing.T) (bin, store, platform string) {
	bin = t.TempDir()
	store = fakeImageStore(t, bin)
	writeExe(t, bin, "sudo", "#!/bin/sh\n[ \"$1\" = -n ] && shift\nexec \"$@\"\n")
	writeExe(t, bin, "date", "#!/bin/sh\nprintf '%s\\n' \"$FAKE_STAMP\"\n")
	return bin, store, t.TempDir()
}

func TestApplyBuildsEveryDeployFromAStableBase(t *testing.T) {
	bin, store, platform := applyFixture(t)
	running := seedImage(t, store, "hvac/energy-api:0.1.0-dev", 12, "")

	output := applyRelease(t, bin, store, platform, 1)
	if !strings.Contains(output, "stable base: hvac/energy-api:deploy-base is the running hvac/energy-api:0.1.0-dev") {
		t.Fatalf("first deploy did not adopt the running image as the base:\n%s", output)
	}
	baseID, baseLayers, _ := imageOf(t, store, "hvac/energy-api:deploy-base")
	if baseID != running || baseLayers != 12 {
		t.Fatalf("deploy-base = %s (%d layers), want the running %s", baseID, baseLayers, running)
	}
	for run := 2; run <= 4; run++ {
		applyRelease(t, bin, store, platform, run)
		_, layers, label := imageOf(t, store, "hvac/energy-api:0.1.0-dev")
		if layers != 13 || label != running {
			t.Fatalf("deploy %d: image has %d layers (label %q); want 13 on base %s", run, layers, label, running)
		}
		if id, _, _ := imageOf(t, store, "hvac/energy-api:deploy-base"); id != running {
			t.Fatalf("deploy %d moved deploy-base to %s", run, id)
		}
		// The previous image stays reachable for rollback.
		_, prevLayers, _ := imageOf(t, store, "hvac/energy-api:before-2026102"+strconv.Itoa(run)+"T000000Z")
		if prevLayers != 13 {
			t.Fatalf("deploy %d: before tag has %d layers, want the previous deploy's 13", run, prevLayers)
		}
	}
	builds := string(mustRead(t, filepath.Join(store, "builds.log")))
	if strings.Contains(builds, "FROM hvac/energy-api:0.1.0-dev") || strings.Count(builds, "FROM hvac/energy-api:deploy-base") != 4 {
		t.Fatalf("candidates must be built FROM deploy-base only:\n%s", builds)
	}
}

func TestApplyRecoversTheBaseFromTheImageLabel(t *testing.T) {
	bin, store, platform := applyFixture(t)
	original := seedImage(t, store, "hvac/energy-api:original", 3, "")
	seedImage(t, store, "hvac/energy-api:0.1.0-dev", 4, original)

	output := applyRelease(t, bin, store, platform, 1)
	if strings.Contains(output, "stable base:") {
		t.Fatalf("a labelled image must not become the base:\n%s", output)
	}
	if id, _, _ := imageOf(t, store, "hvac/energy-api:deploy-base"); id != original {
		t.Fatalf("deploy-base = %s, want the labelled %s", id, original)
	}
	if _, layers, label := imageOf(t, store, "hvac/energy-api:0.1.0-dev"); layers != 4 || label != original {
		t.Fatalf("image has %d layers (label %q), want 4 on %s", layers, label, original)
	}

	// A label naming an image that no longer exists falls back to the running image, once.
	bin, store, platform = applyFixture(t)
	running := seedImage(t, store, "hvac/energy-api:0.1.0-dev", 4, "sha256:gone")
	output = applyRelease(t, bin, store, platform, 1)
	if id, _, _ := imageOf(t, store, "hvac/energy-api:deploy-base"); id != running || !strings.Contains(output, "stable base:") {
		t.Fatalf("deploy-base = %s, want the running %s\n%s", id, running, output)
	}
}

func TestRollbackRetagsFromTheManifestAndRestarts(t *testing.T) {
	platform := t.TempDir()
	if err := os.MkdirAll(filepath.Join(platform, "rollbacks"), 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := "20261029T120000Z"
	manifest := "hvac/energy-api:0.1.0-dev\thvac/energy-api:before-" + stamp + "\n"
	if err := os.WriteFile(filepath.Join(platform, "rollbacks", stamp+".tsv"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(platform, "rollbacks", "latest"), []byte(stamp+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "docker.log")
	ran := filepath.Join(t.TempDir(), "restarted")
	writeExe(t, bin, "docker", `#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
if [ "$1" = image ] && [ "$2" = inspect ]; then exit 0; fi
if [ "$1" = tag ]; then exit 0; fi
echo "unexpected docker $*" >&2
exit 1
`)
	deploy := writeExe(t, bin, "deploy.sh", "#!/bin/sh\ntouch \"$RESTARTED\"\n")
	writeExe(t, bin, "sudo", "#!/bin/sh\nshift\nexec \"$@\"\n")
	cmd := exec.Command("bash", "rollback.sh")
	cmd.Env = commandEnv(bin, "DOCKER_LOG="+log, "HVAC_PLATFORM_DIR="+platform, "HVAC_DEPLOY_SCRIPT="+deploy, "RESTARTED="+ran)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rollback: %v\n%s", err, output)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "tag hvac/energy-api:before-"+stamp+" hvac/energy-api:0.1.0-dev") {
		t.Fatalf("retag missing:\n%s", got)
	}
	if _, err := os.Stat(ran); err != nil {
		t.Fatal("restart script was not run")
	}
}

func TestSmokePassesHealthLoginAndDiscovery(t *testing.T) {
	bin := t.TempDir()
	writeExe(t, bin, "curl", `#!/bin/sh
url=
for arg in "$@"; do
  case "$arg" in
    http*) url=$arg ;;
  esac
done
case "$url" in
  */healthz) printf 'ok\n'; exit 0 ;;
  */sign-in|*/login) exit 0 ;;
  *openid-configuration) printf '%s\n' '{"issuer":"https://hvac.example"}'; exit 0 ;;
esac
echo "unexpected $url" >&2
exit 1
`)
	cmd := exec.Command("bash", "smoke-release.sh")
	cmd.Env = commandEnv(bin, "HVAC_PUBLIC_ORIGIN=https://hvac.example", "HVAC_PUBLIC_HOST=hvac.example", "SMOKE_ATTEMPTS=1", "SMOKE_SLEEP=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke: %v\n%s", err, output)
	}
}

func TestSmokeFailsWhenHealthzIsDown(t *testing.T) {
	bin := t.TempDir()
	writeExe(t, bin, "curl", "#!/bin/sh\nexit 22\n")
	cmd := exec.Command("bash", "smoke-release.sh")
	cmd.Env = commandEnv(bin, "HVAC_PUBLIC_ORIGIN=https://hvac.example", "SMOKE_ATTEMPTS=1", "SMOKE_SLEEP=0")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected smoke to fail")
	}
}

func TestSmokeWaitsForUnhealthyContainers(t *testing.T) {
	bin := t.TempDir()
	writeExe(t, bin, "curl", `#!/bin/sh
url=
for arg in "$@"; do case "$arg" in http*) url=$arg ;; esac; done
case "$url" in
  */healthz) printf 'ok\n' ;;
  *openid-configuration) printf '%s\n' '{"issuer":"x"}' ;;
esac
exit 0
`)
	state := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(state, []byte("unhealthy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeExe(t, bin, "docker", `#!/bin/sh
if [ "$1" != ps ]; then exit 0; fi
read -r status < "$SMOKE_STATE"
if [ "$status" = unhealthy ]; then
  printf '%s\n' healthy > "$SMOKE_STATE"
  printf '%s\n' 'hvac-platform-energy-api-1 Up 1 second (unhealthy)'
  exit 0
fi
printf '%s\n' 'hvac-platform-energy-api-1 Up 1 minute (healthy)'
exit 0
`)
	cmd := exec.Command("bash", "smoke-release.sh")
	cmd.Env = commandEnv(bin, "HVAC_PUBLIC_ORIGIN=https://hvac.example", "SMOKE_ATTEMPTS=2", "SMOKE_SLEEP=0", "SMOKE_CHECK_CONTAINERS=1", "SMOKE_STATE="+state)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke: %v\n%s", err, output)
	}
}

func stageRelease(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{
		filepath.Join(dir, "agent", "node_modules", "left-pad"),
		filepath.Join(dir, "agent", "dist", "bootstrap"),
		filepath.Join(dir, "agent", ".cache"),
	} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("REVISION", "abc\n")
	write("source.tar", "source")
	write("keep.txt", "keep")
	write("apply-on-server.sh", "#!/bin/sh\nexit 0\n")
	write("agent/node_modules/left-pad/index.js", "module.exports = 1\n")
	write("agent/dist/bootstrap/main.js", "console.log(1)\n")
	write("agent/dist/bootstrap/main.js.map", "{\"version\":3}\n")
	write("agent/dist/bootstrap/main.d.ts", "export {}\n")
	write("agent/package-lock.json", "{\"name\":\"agent\"}\n")
	write("agent/.cache/keep", "cache")
	return dir
}

func rejectSshpass(t *testing.T, bin string) {
	t.Helper()
	writeExe(t, bin, "sshpass", "#!/bin/sh\necho \"sshpass must not run in key mode: $*\" >&2\nexit 99\n")
}

// expectedUploadTimeout matches upload_timeout_for_bytes in push-release.sh.
func expectedUploadTimeout(bytes int) int {
	const perMin = 512 * 1024
	seconds := (bytes*60 + perMin - 1) / perMin
	padded := seconds + seconds/4 + 180
	if padded < 180 {
		padded = 180
	}
	return padded
}

var uploadTimeoutLine = regexp.MustCompile(`release upload timeout: (\d+)s for (\d+) bytes`)

func parseUploadTimeout(t *testing.T, output string) (timeout, bytes int) {
	t.Helper()
	match := uploadTimeoutLine.FindStringSubmatch(output)
	if match == nil {
		t.Fatalf("timeout line missing:\n%s", output)
	}
	timeout, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatal(err)
	}
	bytes, err = strconv.Atoi(match[2])
	if err != nil {
		t.Fatal(err)
	}
	return timeout, bytes
}

func fakeCurl(t *testing.T, bin string) {
	t.Helper()
	writeExe(t, bin, "curl", `#!/bin/sh
url=
for arg in "$@"; do
  case "$arg" in
    http*) url=$arg ;;
  esac
done
case "$url" in
  */healthz) printf 'ok\n'; exit 0 ;;
  */sign-in|*/login) exit 0 ;;
  *openid-configuration) printf '%s\n' '{"issuer":"https://hvac.example"}'; exit 0 ;;
esac
exit 0
`)
}

func TestUploadPacksOneGzipArchiveWithoutNodeModulesAndThenApplies(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "ssh.log")
	sink := filepath.Join(t.TempDir(), "payload")
	rejectSshpass(t, bin)
	writeExe(t, bin, "ssh", `#!/bin/sh
last=
for arg in "$@"; do last=$arg; done
printf '%s\n' "$last" >> "$SSH_LOG"
case "$last" in
  "command -v zstd"*) exit 1 ;;
  "test -f "*) exit 1 ;;
  "cat > "*) cat > "$UPLOAD_SINK"; exit 0 ;;
  "gzip -dc "*) exit 0 ;;
  "test -s "*) exit 0 ;;
  "bash "*) printf 'APPLY\n' >> "$SSH_LOG"; exit 0 ;;
  "rm -rf "*) exit 0 ;;
esac
exit 0
`)
	fakeCurl(t, bin)
	staging := stageRelease(t)
	cmd := exec.Command("bash", "push-release.sh")
	cmd.Env = commandEnv(bin,
		"SSH_LOG="+log,
		"UPLOAD_SINK="+sink,
		"DEPLOY_SSH_KEY=test-key\n",
		"DEPLOY_UPLOAD_ATTEMPTS=1",
		"DEPLOY_UPLOAD_TIMEOUT_SECONDS=30",
		"DEPLOY_UPLOAD_RETRY_SLEEP=0",
		"PRODUCTION_RELEASE_DIR="+staging,
		"SMOKE_ATTEMPTS=1",
		"SMOKE_SLEEP=0",
		"HVAC_PUBLIC_ORIGIN=https://hvac.example",
		"HVAC_PUBLIC_HOST=hvac.example",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("push: %v\n%s", err, output)
	}
	text := string(mustRead(t, log))
	if !strings.Contains(text, "gzip -dc ") || !strings.Contains(text, "APPLY") {
		t.Fatalf("expected one gzip extract and then apply:\n%s", text)
	}
	if strings.Contains(text, "tar -cf") {
		t.Fatalf("upload still streams an uncompressed tar:\n%s", text)
	}
	listing := exec.Command("bash", "-c", "gzip -dc \"$1\" | tar -t", "bash", sink)
	listed, err := listing.CombinedOutput()
	if err != nil {
		t.Fatalf("archive: %v\n%s", err, listed)
	}
	names := string(listed)
	for _, want := range []string{"agent/dist/bootstrap/main.js", "agent/package-lock.json", "keep.txt", "source.tar"} {
		if !strings.Contains(names, want) {
			t.Fatalf("archive missing %s:\n%s", want, names)
		}
	}
	for _, banned := range []string{"node_modules", ".js.map", ".d.ts", ".cache"} {
		if strings.Contains(names, banned) {
			t.Fatalf("archive still contains %s:\n%s", banned, names)
		}
	}
	if !strings.Contains(string(output), "B/s") {
		t.Fatalf("throughput log missing:\n%s", output)
	}
	gotTimeout, gotBytes := parseUploadTimeout(t, string(output))
	if gotTimeout != 30 || gotBytes < 1 {
		t.Fatalf("override timeout was not honored: %ds for %d bytes", gotTimeout, gotBytes)
	}
}

func TestUploadRetriesThenCleansUpWithoutApply(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "ssh.log")
	count := filepath.Join(t.TempDir(), "count")
	if err := os.WriteFile(count, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rejectSshpass(t, bin)
	writeExe(t, bin, "ssh", `#!/bin/sh
last=
for arg in "$@"; do last=$arg; done
printf '%s\n' "$last" >> "$SSH_LOG"
case "$last" in
  "command -v zstd"*) exit 1 ;;
  "cat > "*)
    n=$(tr -d '[:space:]' < "$UPLOAD_COUNT")
    echo $((n + 1)) > "$UPLOAD_COUNT"
    if [ "$n" -lt 1 ]; then exit 1; fi
    cat > /dev/null
    exit 0
    ;;
  "gzip -dc "*) exit 0 ;;
  "test -s "*) exit 0 ;;
  "bash "*) printf 'APPLY\n' >> "$SSH_LOG"; exit 0 ;;
esac
exit 0
`)
	fakeCurl(t, bin)
	cmd := exec.Command("bash", "push-release.sh")
	cmd.Env = commandEnv(bin,
		"SSH_LOG="+log,
		"UPLOAD_COUNT="+count,
		"DEPLOY_SSH_KEY=test-key\n",
		"DEPLOY_UPLOAD_ATTEMPTS=2",
		"DEPLOY_UPLOAD_TIMEOUT_SECONDS=30",
		"DEPLOY_UPLOAD_RETRY_SLEEP=0",
		"PRODUCTION_RELEASE_DIR="+stageRelease(t),
		"SMOKE_ATTEMPTS=1",
		"SMOKE_SLEEP=0",
		"HVAC_PUBLIC_ORIGIN=https://hvac.example",
		"HVAC_PUBLIC_HOST=hvac.example",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("push: %v\n%s", err, output)
	}
	text := string(mustRead(t, log))
	if strings.Count(text, "cat > ") != 2 {
		t.Fatalf("expected two upload attempts:\n%s", text)
	}
	if !strings.Contains(text, "rm -rf ") || !strings.Contains(text, "APPLY") {
		t.Fatalf("retry should clean the remote dir and apply only after success:\n%s", text)
	}
	if strings.Count(text, "\nAPPLY\n")+strings.Count(text, "APPLY\n") > 2 && strings.Count(text, "APPLY") != 1 {
		t.Fatalf("apply ran more than once:\n%s", text)
	}
}

func TestUploadTimeoutRemovesRemoteDirAndDoesNotApply(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "ssh.log")
	rejectSshpass(t, bin)
	writeExe(t, bin, "ssh", `#!/bin/sh
last=
for arg in "$@"; do last=$arg; done
printf '%s\n' "$last" >> "$SSH_LOG"
case "$last" in
  "command -v zstd"*) exit 1 ;;
  "cat > "*) sleep 30; exit 0 ;;
esac
exit 0
`)
	cmd := exec.Command("bash", "push-release.sh")
	cmd.Env = commandEnv(bin,
		"SSH_LOG="+log,
		"DEPLOY_SSH_KEY=test-key\n",
		"DEPLOY_UPLOAD_ATTEMPTS=1",
		"DEPLOY_UPLOAD_TIMEOUT_SECONDS=1",
		"DEPLOY_UPLOAD_RETRY_SLEEP=0",
		"PRODUCTION_RELEASE_DIR="+stageRelease(t),
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a timed-out upload to fail:\n%s", output)
	}
	text := string(mustRead(t, log))
	if !strings.Contains(text, "rm -rf ") {
		t.Fatalf("timed-out upload did not remove the remote dir:\n%s", text)
	}
	if strings.Contains(text, "apply-on-server.sh") {
		t.Fatalf("apply ran after a timed-out upload:\n%s", text)
	}
	if !strings.Contains(string(output), "timed out") || !strings.Contains(string(output), "was not run") {
		t.Fatalf("timeout log missing:\n%s", output)
	}
}

func TestUploadUsesZstdWhenBothEndsHaveIt(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "ssh.log")
	zstdLog := filepath.Join(t.TempDir(), "zstd.log")
	rejectSshpass(t, bin)
	writeExe(t, bin, "zstd", `#!/bin/sh
printf '%s\n' "$*" >> "$ZSTD_LOG"
if [ "$1" = --version ] || [ "$1" = -V ]; then
  printf '%s\n' '*** Zstandard CLI (64-bit) v1.5.5 ***'
  exit 0
fi
gzip -c
`)
	writeExe(t, bin, "ssh", `#!/bin/sh
last=
for arg in "$@"; do last=$arg; done
printf '%s\n' "$last" >> "$SSH_LOG"
case "$last" in
  "command -v zstd"*) exit 0 ;;
  "zstd -V"*) printf '%s\n' '*** Zstandard CLI (64-bit) v1.5.5 ***'; exit 0 ;;
  "test -f "*) exit 1 ;;
  "cat > "*) cat > /dev/null; exit 0 ;;
  "zstd -dc "*) exit 0 ;;
  "test -s "*) exit 0 ;;
  "bash "*) printf 'APPLY\n' >> "$SSH_LOG"; exit 0 ;;
esac
exit 0
`)
	fakeCurl(t, bin)
	cmd := exec.Command("bash", "push-release.sh")
	cmd.Env = commandEnv(bin,
		"SSH_LOG="+log,
		"ZSTD_LOG="+zstdLog,
		"DEPLOY_SSH_KEY=test-key\n",
		"DEPLOY_UPLOAD_ATTEMPTS=1",
		"DEPLOY_UPLOAD_TIMEOUT_SECONDS=30",
		"DEPLOY_UPLOAD_RETRY_SLEEP=0",
		"PRODUCTION_RELEASE_DIR="+stageRelease(t),
		"SMOKE_ATTEMPTS=1",
		"SMOKE_SLEEP=0",
		"HVAC_PUBLIC_ORIGIN=https://hvac.example",
		"HVAC_PUBLIC_HOST=hvac.example",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("push: %v\n%s", err, output)
	}
	zstdText := string(mustRead(t, zstdLog))
	if !strings.Contains(zstdText, "-c") || !strings.Contains(zstdText, "--long=27") || !strings.Contains(zstdText, "-19") {
		t.Fatalf("local zstd was not the long-window compress:\n%s", zstdText)
	}
	if !strings.Contains(string(mustRead(t, log)), "zstd -dc --long=27") {
		t.Fatalf("remote extract was not long-window zstd:\n%s", mustRead(t, log))
	}
}

func TestUploadSkipsLongZstdWhenRemoteIsOld(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "ssh.log")
	zstdLog := filepath.Join(t.TempDir(), "zstd.log")
	rejectSshpass(t, bin)
	writeExe(t, bin, "zstd", `#!/bin/sh
printf '%s\n' "$*" >> "$ZSTD_LOG"
if [ "$1" = -V ]; then
  printf '%s\n' '*** Zstandard CLI (64-bit) v1.5.5 ***'
  exit 0
fi
gzip -c
`)
	writeExe(t, bin, "ssh", `#!/bin/sh
last=
for arg in "$@"; do last=$arg; done
printf '%s\n' "$last" >> "$SSH_LOG"
case "$last" in
  "command -v zstd"*) exit 0 ;;
  "zstd -V"*) printf '%s\n' '*** Zstandard CLI (64-bit) v1.3.8 ***'; exit 0 ;;
  "test -f "*) exit 1 ;;
  "cat > "*) cat > /dev/null; exit 0 ;;
  "zstd -dc "*) exit 0 ;;
  "test -s "*) exit 0 ;;
  "bash "*) exit 0 ;;
esac
exit 0
`)
	fakeCurl(t, bin)
	cmd := exec.Command("bash", "push-release.sh")
	cmd.Env = commandEnv(bin,
		"SSH_LOG="+log,
		"ZSTD_LOG="+zstdLog,
		"DEPLOY_SSH_KEY=test-key\n",
		"DEPLOY_UPLOAD_ATTEMPTS=1",
		"DEPLOY_UPLOAD_TIMEOUT_SECONDS=30",
		"DEPLOY_UPLOAD_RETRY_SLEEP=0",
		"PRODUCTION_RELEASE_DIR="+stageRelease(t),
		"SMOKE_ATTEMPTS=1",
		"SMOKE_SLEEP=0",
		"HVAC_PUBLIC_ORIGIN=https://hvac.example",
		"HVAC_PUBLIC_HOST=hvac.example",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("push: %v\n%s", err, output)
	}
	for _, line := range strings.Split(string(mustRead(t, zstdLog)), "\n") {
		if !strings.Contains(line, "-c") {
			continue
		}
		if strings.Contains(line, "--long") || !strings.Contains(line, "-3") {
			t.Fatalf("old remote zstd should stay on -3:\n%s", line)
		}
	}
	sshText := string(mustRead(t, log))
	if strings.Contains(sshText, "--long") {
		t.Fatalf("decode used a long window the remote does not support:\n%s", sshText)
	}
	if !strings.Contains(sshText, "zstd -dc ") {
		t.Fatalf("remote extract was not zstd:\n%s", sshText)
	}
}

func TestPasswordModeUploadIsWrappedWithSshpass(t *testing.T) {
	bin := t.TempDir()
	sshLog := filepath.Join(t.TempDir(), "ssh.log")
	passLog := filepath.Join(t.TempDir(), "sshpass.log")
	sink := filepath.Join(t.TempDir(), "payload")
	const password = "s3cret-deploy-password"
	writeExe(t, bin, "sshpass", `#!/bin/sh
printf '%s\n' "$*" >> "$SSHPASS_LOG"
if [ "$1" != "-e" ]; then
  echo "sshpass: first argument must be -e" >&2
  exit 1
fi
if [ -z "${SSHPASS:-}" ]; then
  echo "sshpass: SSHPASS is empty" >&2
  exit 1
fi
case " $* " in
  *" ${SSHPASS} "*) echo "sshpass: password is in argv" >&2; exit 1 ;;
esac
export DEPLOY_SSH_WRAPPED=1
shift
exec "$@"
`)
	writeExe(t, bin, "ssh", `#!/bin/sh
if [ "${DEPLOY_SSH_WRAPPED:-}" != 1 ]; then
  echo "ssh was not wrapped with sshpass" >&2
  exit 1
fi
last=
for arg in "$@"; do last=$arg; done
printf '%s\n' "$last" >> "$SSH_LOG"
case "$last" in
  "command -v zstd"*) exit 1 ;;
  "test -f "*) exit 1 ;;
  "cat >> "*) echo "did not expect resume" >&2; exit 1 ;;
  "cat > "*) cat > "$UPLOAD_SINK"; exit 0 ;;
  "gzip -dc "*) exit 0 ;;
  "test -s "*) exit 0 ;;
  "bash "*) printf 'APPLY\n' >> "$SSH_LOG"; exit 0 ;;
  "rm -rf "*) exit 0 ;;
  "mkdir -p "*) exit 0 ;;
esac
echo "unexpected ssh: $last" >&2
exit 1
`)
	fakeCurl(t, bin)
	cmd := exec.Command("bash", "push-release.sh")
	cmd.Env = commandEnv(bin,
		"SSH_LOG="+sshLog,
		"SSHPASS_LOG="+passLog,
		"UPLOAD_SINK="+sink,
		"DEPLOY_SSH_PASSWORD="+password,
		"DEPLOY_UPLOAD_ATTEMPTS=1",
		"DEPLOY_UPLOAD_TIMEOUT_SECONDS=30",
		"DEPLOY_UPLOAD_RETRY_SLEEP=0",
		"PRODUCTION_RELEASE_DIR="+stageRelease(t),
		"SMOKE_ATTEMPTS=1",
		"SMOKE_SLEEP=0",
		"HVAC_PUBLIC_ORIGIN=https://hvac.example",
		"HVAC_PUBLIC_HOST=hvac.example",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("push: %v\n%s", err, output)
	}
	text := string(output)
	if strings.Contains(text, password) {
		t.Fatal("password appeared in the deploy log")
	}
	passText := string(mustRead(t, passLog))
	if strings.Contains(passText, password) || strings.Contains(passText, "-p "+password) {
		t.Fatalf("password appeared in sshpass argv:\n%s", passText)
	}
	sawUpload := false
	for _, line := range strings.Split(strings.TrimSpace(passText), "\n") {
		if !strings.HasPrefix(line, "-e ") || !strings.Contains(line, "ssh") {
			t.Fatalf("sshpass invocation is not sshpass -e ssh: %s", line)
		}
		if strings.Contains(line, "cat >") {
			sawUpload = true
		}
	}
	if !sawUpload {
		t.Fatalf("upload was not wrapped with sshpass -e:\n%s", passText)
	}
	if !strings.Contains(string(mustRead(t, sshLog)), "APPLY") {
		t.Fatal("apply did not run after the wrapped upload")
	}
}

func TestUploadResumesAPartialArchive(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "ssh.log")
	part := filepath.Join(t.TempDir(), "part")
	full := filepath.Join(t.TempDir(), "full")
	done := filepath.Join(t.TempDir(), "done")
	rejectSshpass(t, bin)
	writeExe(t, bin, "ssh", `#!/bin/sh
last=
for arg in "$@"; do last=$arg; done
printf '%s\n' "$last" >> "$SSH_LOG"
case "$last" in
  "command -v zstd"*) exit 1 ;;
  "rm -rf "*) rm -f "$REMOTE_PART"; exit 0 ;;
  "mkdir -p "*) exit 0 ;;
  "test -f "*)
    if [ -s "$REMOTE_PART" ]; then exit 0; fi
    exit 1
    ;;
  "wc -c < "*) wc -c < "$REMOTE_PART"; exit 0 ;;
  "cat >> "*)
    cat >> "$REMOTE_PART"
    cp "$REMOTE_PART" "$REMOTE_DONE"
    if cmp -s "$REMOTE_PART" "$REMOTE_FULL"; then
      printf '%s\n' 'RESUME_OK' >> "$SSH_LOG"
      exit 0
    fi
    echo "resumed archive does not match" >&2
    exit 1
    ;;
  "cat > "*)
    cat > "$REMOTE_FULL"
    size=$(wc -c < "$REMOTE_FULL" | tr -d '[:space:]')
    half=$((size / 2))
    if [ "$half" -lt 1 ]; then echo "archive too small" >&2; exit 1; fi
    head -c "$half" "$REMOTE_FULL" > "$REMOTE_PART"
    exit 1
    ;;
  "gzip -dc "*) exit 0 ;;
  "test -s "*) exit 0 ;;
  "bash "*) printf 'APPLY\n' >> "$SSH_LOG"; exit 0 ;;
esac
echo "unexpected ssh: $last" >&2
exit 1
`)
	fakeCurl(t, bin)
	cmd := exec.Command("bash", "push-release.sh")
	cmd.Env = commandEnv(bin,
		"SSH_LOG="+log,
		"REMOTE_PART="+part,
		"REMOTE_FULL="+full,
		"REMOTE_DONE="+done,
		"DEPLOY_SSH_KEY=test-key\n",
		"DEPLOY_UPLOAD_ATTEMPTS=2",
		"DEPLOY_UPLOAD_TIMEOUT_SECONDS=30",
		"DEPLOY_UPLOAD_RETRY_SLEEP=0",
		"PRODUCTION_RELEASE_DIR="+stageRelease(t),
		"SMOKE_ATTEMPTS=1",
		"SMOKE_SLEEP=0",
		"HVAC_PUBLIC_ORIGIN=https://hvac.example",
		"HVAC_PUBLIC_HOST=hvac.example",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("push: %v\n%s", err, output)
	}
	text := string(mustRead(t, log))
	if !strings.Contains(text, "RESUME_OK") || !strings.Contains(text, "cat >> ") || !strings.Contains(text, "APPLY") {
		t.Fatalf("partial upload was not resumed before apply:\n%s", text)
	}
	if strings.Count(text, "cat > ") != 1 {
		t.Fatalf("resume should not start the upload over:\n%s", text)
	}
}

func TestUploadTimeoutScalesWithArchiveSize(t *testing.T) {
	run := func(t *testing.T, staging string, override string) (int, int) {
		t.Helper()
		bin := t.TempDir()
		rejectSshpass(t, bin)
		writeExe(t, bin, "ssh", `#!/bin/sh
last=
for arg in "$@"; do last=$arg; done
case "$last" in
  "command -v zstd"*) exit 1 ;;
  "test -f "*) exit 1 ;;
  "cat > "*) cat > /dev/null; exit 0 ;;
  "gzip -dc "*) exit 0 ;;
  "test -s "*) exit 0 ;;
  "bash "*) exit 0 ;;
  "rm -rf "*) exit 0 ;;
esac
exit 0
`)
		fakeCurl(t, bin)
		extra := []string{
			"DEPLOY_SSH_KEY=test-key\n",
			"DEPLOY_UPLOAD_ATTEMPTS=1",
			"DEPLOY_UPLOAD_RETRY_SLEEP=0",
			"PRODUCTION_RELEASE_DIR=" + staging,
			"SMOKE_ATTEMPTS=1",
			"SMOKE_SLEEP=0",
			"HVAC_PUBLIC_ORIGIN=https://hvac.example",
			"HVAC_PUBLIC_HOST=hvac.example",
		}
		if override != "" {
			extra = append(extra, "DEPLOY_UPLOAD_TIMEOUT_SECONDS="+override)
		}
		cmd := exec.Command("bash", "push-release.sh")
		cmd.Env = commandEnv(bin, extra...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("push: %v\n%s", err, output)
		}
		return parseUploadTimeout(t, string(output))
	}

	tinyTimeout, tinyBytes := run(t, stageRelease(t), "")
	if tinyTimeout != expectedUploadTimeout(tinyBytes) {
		t.Fatalf("tiny archive timeout %d, want %d for %d bytes", tinyTimeout, expectedUploadTimeout(tinyBytes), tinyBytes)
	}
	if tinyTimeout >= 600 {
		t.Fatalf("tiny archive still uses the old 600s ceiling: %d", tinyTimeout)
	}

	large := stageRelease(t)
	payload := make([]byte, 512*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(large, "noise.bin"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	largeTimeout, largeBytes := run(t, large, "")
	if largeBytes <= tinyBytes {
		t.Fatalf("larger staging did not grow the archive (%d <= %d)", largeBytes, tinyBytes)
	}
	if largeTimeout != expectedUploadTimeout(largeBytes) || largeTimeout <= tinyTimeout {
		t.Fatalf("timeout did not scale: %ds for %d bytes (tiny %ds)", largeTimeout, largeBytes, tinyTimeout)
	}

	overrideTimeout, _ := run(t, large, "45")
	if overrideTimeout != 45 {
		t.Fatalf("DEPLOY_UPLOAD_TIMEOUT_SECONDS=45 was ignored: %d", overrideTimeout)
	}
}

func TestStageAgentOmitsNodeModulesAndSourcemaps(t *testing.T) {
	src := t.TempDir()
	for _, sub := range []string{
		filepath.Join(src, "dist", "bootstrap"),
		filepath.Join(src, "dist", "node_modules", "nested"),
		filepath.Join(src, "node_modules", "left-pad"),
		filepath.Join(src, ".cache"),
	} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", "{\"name\":\"agent\"}\n")
	write("package-lock.json", "{\"name\":\"agent\"}\n")
	write("dist/bootstrap/main.js", "console.log(1)\n")
	write("dist/bootstrap/main.js.map", "{}\n")
	write("dist/bootstrap/main.d.ts", "export {}\n")
	write("dist/tsconfig.tsbuildinfo", "{}\n")
	write("dist/node_modules/nested/index.js", "nope\n")
	write("node_modules/left-pad/index.js", "nope\n")
	write(".cache/keep", "cache\n")
	dest := t.TempDir()
	cmd := exec.Command("bash", "build-release.sh", "--stage-agent", src, dest)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stage-agent: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(dest, "dist", "bootstrap", "main.js")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "package-lock.json")); err != nil {
		t.Fatal(err)
	}
	banned := []string{
		filepath.Join(dest, "node_modules"),
		filepath.Join(dest, "dist", "node_modules"),
		filepath.Join(dest, ".cache"),
		filepath.Join(dest, "dist", "bootstrap", "main.js.map"),
		filepath.Join(dest, "dist", "bootstrap", "main.d.ts"),
		filepath.Join(dest, "dist", "tsconfig.tsbuildinfo"),
	}
	for _, path := range banned {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("staged agent still has %s", path)
		}
	}
}

func TestAgentImageInstallsFromTheLockfile(t *testing.T) {
	body := string(mustRead(t, "apply-on-server.sh"))
	if !strings.Contains(body, "npm ci --omit=dev --no-audit --no-fund") {
		t.Fatal("agent image does not install production dependencies from the lockfile")
	}
	if strings.Contains(body, "node_modules /app/node_modules") || strings.Contains(body, "cp -a \"$release/agent/node_modules\"") {
		t.Fatal("agent image still copies node_modules into the build")
	}
	if !strings.Contains(body, "package-lock.json") {
		t.Fatal("agent image build does not receive the lockfile")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// fakeSudo allows `sudo -n -l <cmd>` when SUDO_LIST_OK=1 and runs `sudo -n <cmd...>` directly.
const fakeSudo = `#!/bin/sh
[ "$1" = -n ] && shift
if [ "$1" = -l ]; then
  [ "$SUDO_LIST_OK" = 1 ] && exit 0
  exit 1
fi
exec "$@"
`

func rollbackFixture(t *testing.T, withTreeScript bool) (platform, bin, ran string) {
	t.Helper()
	platform = t.TempDir()
	if err := os.MkdirAll(filepath.Join(platform, "rollbacks"), 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := "20261029T120000Z"
	manifest := "hvac/energy-api:0.1.0-dev\thvac/energy-api:before-" + stamp + "\n"
	if err := os.WriteFile(filepath.Join(platform, "rollbacks", stamp+".tsv"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(platform, "rollbacks", "latest"), []byte(stamp+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ran = filepath.Join(t.TempDir(), "restarted")
	if withTreeScript {
		cd := filepath.Join(platform, "deploy", "platform", "phase1", "cd")
		if err := os.MkdirAll(cd, 0o755); err != nil {
			t.Fatal(err)
		}
		writeExe(t, cd, "deploy.sh", "#!/bin/sh\nprintf '%s\\n' \"$0\" > \"$RESTARTED\"\n")
	}
	bin = t.TempDir()
	writeExe(t, bin, "docker", "#!/bin/sh\nexit 0\n")
	writeExe(t, bin, "sudo", fakeSudo)
	return platform, bin, ran
}

func TestRollbackDefaultsToTheDeployScriptInThePlatformTree(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("exercises the sudo path")
	}
	platform, bin, ran := rollbackFixture(t, true)
	cmd := exec.Command("bash", "rollback.sh")
	cmd.Env = commandEnv(bin, "HVAC_PLATFORM_DIR="+platform, "HVAC_DEPLOY_SCRIPT=", "RESTARTED="+ran, "SUDO_LIST_OK=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rollback: %v\n%s", err, output)
	}
	got, err := os.ReadFile(ran)
	if err != nil {
		t.Fatalf("tree deploy.sh was not run:\n%s", output)
	}
	want := filepath.Join(platform, "deploy", "platform", "phase1", "cd", "deploy.sh")
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("ran %q, want %q", got, want)
	}
}

func TestRollbackFallsBackToTheHostDeployScript(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("exercises the sudo path")
	}
	for name, tc := range map[string]struct {
		tree   bool
		listOK string
	}{
		"tree copy absent":           {tree: false, listOK: "1"},
		"sudo refuses the tree copy": {tree: true, listOK: "0"},
	} {
		t.Run(name, func(t *testing.T) {
			platform, bin, ran := rollbackFixture(t, tc.tree)
			cmd := exec.Command("bash", "rollback.sh")
			cmd.Env = commandEnv(bin, "HVAC_PLATFORM_DIR="+platform, "HVAC_DEPLOY_SCRIPT=", "RESTARTED="+ran, "SUDO_LIST_OK="+tc.listOK)
			output, _ := cmd.CombinedOutput()
			if !strings.Contains(string(output), "restart script: /opt/hvac-platform-tools/deploy.sh") {
				t.Fatalf("host fallback was not chosen:\n%s", output)
			}
			if _, err := os.Stat(ran); err == nil {
				t.Fatal("tree deploy.sh ran although the fallback was chosen")
			}
		})
	}
}

func deployFixture(t *testing.T, revisionFile string) (platform, bin, log string) {
	t.Helper()
	platform = t.TempDir()
	runtime := filepath.Join(platform, "deploy", "platform", "phase1", "runtime", "server")
	if err := os.MkdirAll(runtime, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime, "identity-reconcile.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if revisionFile != "" {
		if err := os.WriteFile(filepath.Join(platform, "REVISION"), []byte(revisionFile+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin = t.TempDir()
	log = filepath.Join(t.TempDir(), "calls.log")
	writeExe(t, bin, "node", `#!/bin/sh
printf 'node %s | pwd=%s build=%s runtime=%s env=%s\n' "$*" "$(pwd)" "$HVAC_WEB_BUILD_ID" "$PHASE1_RUNTIME_DIR" "$PHASE1_ENV_FILE" >> "$CALLS_LOG"
`)
	writeExe(t, bin, "docker", "#!/bin/sh\nprintf 'docker %s\\n' \"$*\" >> \"$CALLS_LOG\"\n")
	return platform, bin, log
}

func TestDeployScriptDerivesTheBuildIDFromTheRelease(t *testing.T) {
	revision := "0123456789abcdef0123456789abcdef01234567"
	for name, tc := range map[string]struct {
		file string
		args []string
		want string
	}{
		"argument wins":          {file: strings.Repeat("f", 40), args: []string{revision}, want: "0123456789ab"},
		"REVISION file fallback": {file: revision, want: "0123456789ab"},
	} {
		t.Run(name, func(t *testing.T) {
			platform, bin, log := deployFixture(t, tc.file)
			cmd := exec.Command("bash", append([]string{"deploy.sh"}, tc.args...)...)
			cmd.Env = commandEnv(bin, "HVAC_PLATFORM_DIR="+platform, "CALLS_LOG="+log)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("deploy.sh: %v\n%s", err, output)
			}
			calls := string(mustRead(t, log))
			if !strings.Contains(calls, "build="+tc.want+" ") || strings.Contains(calls, "fcc56818acd6") {
				t.Fatalf("HVAC_WEB_BUILD_ID is not %s:\n%s", tc.want, calls)
			}
			runtime := filepath.Join(platform, "deploy", "platform", "phase1", "runtime", "server")
			for _, want := range []string{
				"pwd=" + platform + " ",
				"runtime=" + runtime + " ",
				"env=" + filepath.Join(platform, "deploy", "platform", "phase1", "environments", "server.runtime.env"),
				"node scripts/issue-workload-identities.mjs",
				"node scripts/phase1-wsl-compose.mjs --profile migration run --rm phase1-migrator",
				"node scripts/local-environment.mjs administrator --no-build",
				"node scripts/site-onboarding.mjs deploy/sites/songyaxuan.site.json",
			} {
				if !strings.Contains(calls, want) {
					t.Fatalf("missing %q in:\n%s", want, calls)
				}
			}
		})
	}
}

func TestDeployScriptRefusesToRunWithoutARevision(t *testing.T) {
	platform, bin, log := deployFixture(t, "")
	cmd := exec.Command("bash", "deploy.sh")
	cmd.Env = commandEnv(bin, "HVAC_PLATFORM_DIR="+platform, "CALLS_LOG="+log)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "no release revision") {
		t.Fatalf("deploy.sh ran without a revision: %v\n%s", err, output)
	}
}

func TestReleaseShipsAndRunsTheRepositoryDeployScript(t *testing.T) {
	build := string(mustRead(t, "build-release.sh"))
	if !regexp.MustCompile(`for script in [^\n]*\bdeploy\.sh; do`).MatchString(build) {
		t.Fatal("build-release.sh does not copy deploy.sh into the release")
	}
	apply := string(mustRead(t, "apply-on-server.sh"))
	if strings.Contains(apply, "HVAC_DEPLOY_SCRIPT:-/opt") || !strings.Contains(apply, `sudo -n "$deploy_script" "$revision"`) {
		t.Fatal("apply-on-server.sh must default to the tree deploy.sh and pass the release revision")
	}
	if strings.Contains(string(mustRead(t, "rollback.sh")), "HVAC_DEPLOY_SCRIPT:-/opt") {
		t.Fatal("rollback.sh must default to the tree deploy.sh")
	}
	for _, line := range strings.Split(string(mustRead(t, "deploy.sh")), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") && strings.Contains(line, "/opt/") {
			t.Fatalf("deploy.sh must not hard-code a host path: %s", line)
		}
	}
}
