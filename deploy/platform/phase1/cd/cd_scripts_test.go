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
