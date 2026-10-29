package cd

import (
	"os"
	"os/exec"
	"path/filepath"
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
	if !strings.Contains(text, "image prune -f") || !strings.Contains(text, "builder prune -f --filter until=24h") {
		t.Fatalf("dangling image or build cache prune missing:\n%s", text)
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
	if err := os.MkdirAll(filepath.Join(dir, "agent", "node_modules", "left-pad"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("REVISION", "abc\n")
	write("source.tar", "source")
	write("apply-on-server.sh", "#!/bin/sh\nexit 0\n")
	write("agent/node_modules/left-pad/index.js", "module.exports = 1\n")
	return dir
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

func TestUploadSendsNodeModulesInOneGzipArchiveAndThenApplies(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "ssh.log")
	sink := filepath.Join(t.TempDir(), "payload")
	writeExe(t, bin, "ssh", `#!/bin/sh
last=
for arg in "$@"; do last=$arg; done
printf '%s\n' "$last" >> "$SSH_LOG"
case "$last" in
  "command -v zstd"*) exit 1 ;;
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
	if !strings.Contains(string(listed), "agent/node_modules/left-pad/index.js") {
		t.Fatalf("node_modules missing from the single archive:\n%s", listed)
	}
	if !strings.Contains(string(output), "B/s") {
		t.Fatalf("throughput log missing:\n%s", output)
	}
}

func TestUploadRetriesThenCleansUpWithoutApply(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "ssh.log")
	count := filepath.Join(t.TempDir(), "count")
	if err := os.WriteFile(count, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	writeExe(t, bin, "zstd", `#!/bin/sh
printf '%s\n' "$*" >> "$ZSTD_LOG"
if [ "$1" = --version ]; then echo zstd; exit 0; fi
gzip -c
`)
	writeExe(t, bin, "ssh", `#!/bin/sh
last=
for arg in "$@"; do last=$arg; done
printf '%s\n' "$last" >> "$SSH_LOG"
case "$last" in
  "command -v zstd"*) exit 0 ;;
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
	if !strings.Contains(string(mustRead(t, zstdLog)), "-c") {
		t.Fatalf("local zstd was not used:\n%s", mustRead(t, zstdLog))
	}
	if !strings.Contains(string(mustRead(t, log)), "zstd -dc ") {
		t.Fatalf("remote extract was not zstd:\n%s", mustRead(t, log))
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
