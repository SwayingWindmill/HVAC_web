package cd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeObservabilityDocker = `#!/bin/sh
printf '%s\n' "$*" >> "$DOCKER_LOG"
case "$1" in
  ps)
    case "$*" in
      *service=prometheus*) echo cid-prometheus ;;
      *service=alertmanager*) [ -n "$NO_ALERTMANAGER" ] || echo cid-alertmanager ;;
    esac
    ;;
  inspect)
    case "$*" in
      *Config.Image*) echo prom/test:pinned ;;
      *) echo /srv/runtime/alert-receiver ;;
    esac
    ;;
  run)
    case "$*" in
      *promtool*) [ -z "$FAIL_PROMTOOL" ] || { echo 'FAILED: parsing YAML' >&2; exit 1; } ;;
    esac
    ;;
  exec)
    case "$*" in
      */metrics*)
        echo "prometheus_config_last_reload_successful ${RELOAD_OK:-1}"
        echo "alertmanager_config_last_reload_successful ${RELOAD_OK:-1}"
        ;;
    esac
    ;;
esac
exit 0
`

// observabilityTree writes the files observability-config.sh fingerprints under root.
func observabilityTree(t *testing.T, root, rule string) {
	t.Helper()
	files := map[string]string{
		"deploy/platform/phase1/observability/prometheus.yaml":               "rule_files: [/etc/prometheus/alerts/*.yaml]\n",
		"deploy/platform/phase1/observability/alertmanager.yaml":             "route: {receiver: none}\n",
		"deploy/platform/phase1/observability/render-alertmanager-config.sh": "#!/bin/sh\n",
		"deploy/platform/phase1/observability/render-and-run.sh":             "#!/bin/sh\n",
		"infra/observability/alerts/host-resource.yaml":                      rule,
		"infra/observability/alerts/host-resource.rules.test.yml":            "tests: []\n",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func sourceTar(t *testing.T, rule string) string {
	t.Helper()
	src := t.TempDir()
	observabilityTree(t, src, rule)
	out := filepath.Join(t.TempDir(), "source.tar")
	if output, err := exec.Command("tar", "-C", src, "-cf", out, "deploy", "infra").CombinedOutput(); err != nil {
		t.Fatalf("tar: %v\n%s", err, output)
	}
	return out
}

func runObservability(t *testing.T, env []string, args ...string) (string, string, error) {
	t.Helper()
	bin := t.TempDir()
	writeExe(t, bin, "docker", fakeObservabilityDocker)
	log := filepath.Join(t.TempDir(), "docker.log")
	cmd := exec.Command("bash", append([]string{"observability-config.sh"}, args...)...)
	cmd.Env = commandEnv(bin, append([]string{"DOCKER_LOG=" + log, "HVAC_OBSERVABILITY_POLL_SECONDS=0"}, env...)...)
	output, err := cmd.CombinedOutput()
	logged, _ := os.ReadFile(log)
	return string(output), string(logged), err
}

func TestObservabilityCheckRejectsInvalidPrometheusConfig(t *testing.T) {
	platform := t.TempDir()
	tarball := sourceTar(t, "groups: [broken\n")
	output, docker, err := runObservability(t, []string{"FAIL_PROMTOOL=1"}, "check", tarball, platform, "hvac-platform")
	if err == nil {
		t.Fatalf("check accepted an invalid config:\n%s", output)
	}
	if !strings.Contains(output, "Prometheus config or alert rules are invalid") {
		t.Fatalf("missing explanation:\n%s", output)
	}
	if !strings.Contains(docker, "--pull never") || !strings.Contains(docker, "--entrypoint /bin/promtool") || !strings.Contains(docker, "prom/test:pinned check config /etc/prometheus/prometheus.yml") {
		t.Fatalf("promtool did not run from the running image:\n%s", docker)
	}
	if strings.Contains(docker, "restart") {
		t.Fatalf("check restarted a container:\n%s", docker)
	}
}

func TestObservabilityCheckValidatesAlertmanagerWithTheReceiverFiles(t *testing.T) {
	platform := t.TempDir()
	tarball := sourceTar(t, "groups: []\n")
	output, docker, err := runObservability(t, nil, "check", tarball, platform, "hvac-platform")
	if err != nil {
		t.Fatalf("check: %v\n%s", err, output)
	}
	if !strings.Contains(docker, "-v /srv/runtime/alert-receiver:/etc/alertmanager/secrets:ro") || !strings.Contains(docker, "amtool check-config") {
		t.Fatalf("alertmanager was not rendered and checked with the receiver files:\n%s", docker)
	}
}

func TestObservabilityApplyRestartsChangedServicesOnce(t *testing.T) {
	platform := t.TempDir()
	observabilityTree(t, platform, "groups: []\n")

	output, docker, err := runObservability(t, nil, "apply", platform, "hvac-platform")
	if err != nil {
		t.Fatalf("first apply: %v\n%s", err, output)
	}
	if !strings.Contains(docker, "restart cid-prometheus") || !strings.Contains(docker, "restart cid-alertmanager") {
		t.Fatalf("first apply without a record must restart both:\n%s", docker)
	}
	if !strings.Contains(docker, "exec cid-prometheus wget -q -T 5 -O - http://127.0.0.1:9090/-/ready") || !strings.Contains(docker, "http://127.0.0.1:9093/metrics") {
		t.Fatalf("readiness and reload metric were not checked:\n%s", docker)
	}
	state := string(mustRead(t, filepath.Join(platform, "observability-applied.tsv")))
	if !strings.Contains(state, "prometheus\t") || !strings.Contains(state, "alertmanager\t") {
		t.Fatalf("applied state not recorded:\n%s", state)
	}

	_, docker, err = runObservability(t, nil, "apply", platform, "hvac-platform")
	if err != nil || strings.Contains(docker, "restart") {
		t.Fatalf("unchanged config was restarted again (err %v):\n%s", err, docker)
	}
	_, docker, err = runObservability(t, nil, "check", sourceTar(t, "groups: []\n"), platform, "hvac-platform")
	if err != nil || strings.Contains(docker, "run ") {
		t.Fatalf("unchanged config was validated again (err %v):\n%s", err, docker)
	}

	// A rule change restarts Prometheus only. Test files are not loaded, so they do not count.
	observabilityTree(t, platform, "groups: [{name: changed, rules: []}]\n")
	if err := os.WriteFile(filepath.Join(platform, "infra/observability/alerts/host-resource.rules.test.yml"), []byte("tests: [x]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, docker, err = runObservability(t, nil, "apply", platform, "hvac-platform")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(docker, "restart cid-prometheus") || strings.Contains(docker, "restart cid-alertmanager") {
		t.Fatalf("a rule change must restart Prometheus only:\n%s", docker)
	}
}

func TestObservabilityApplyFailsWhenTheReloadDidNotSucceed(t *testing.T) {
	platform := t.TempDir()
	observabilityTree(t, platform, "groups: []\n")
	output, _, err := runObservability(t, []string{"RELOAD_OK=0", "HVAC_OBSERVABILITY_READY_SECONDS=0", "NO_ALERTMANAGER=1"}, "apply", platform, "hvac-platform")
	if err == nil {
		t.Fatalf("apply passed with prometheus_config_last_reload_successful 0:\n%s", output)
	}
	if !strings.Contains(output, "prometheus did not become ready") || !strings.Contains(output, "alertmanager is not running; skipped") {
		t.Fatalf("unexpected output:\n%s", output)
	}
	state, _ := os.ReadFile(filepath.Join(platform, "observability-applied.tsv"))
	if strings.Contains(string(state), "prometheus\t") {
		t.Fatalf("a failed apply was recorded as applied:\n%s", state)
	}
}

func TestApplyOnServerValidatesBeforeExtractingAndAppliesAfterSmoke(t *testing.T) {
	body := string(mustRead(t, "apply-on-server.sh"))
	check := strings.Index(body, `observability-config.sh" check`)
	extract := strings.Index(body, `tar -C "$platform_dir" -xf "$release/source.tar"`)
	smoke := strings.Index(body, `smoke-release.sh"; then`)
	apply := strings.Index(body, `observability-config.sh" apply`)
	prune := strings.Index(body, `prune-rollback-images.sh" 2`)
	sync := strings.Index(body, `observability-config.sh" sync`)
	restart := strings.Index(body, "if ! run_deploy; then")
	if sync < extract || sync > restart {
		t.Fatal("stale observability files must be removed right after extraction and before the restart")
	}
	if check < 0 || extract < 0 || smoke < 0 || apply < 0 || prune < 0 {
		t.Fatal("apply-on-server.sh does not call observability-config.sh check and apply")
	}
	if check > extract {
		t.Fatal("observability config must be validated before source.tar is extracted")
	}
	if apply < smoke || apply > prune {
		t.Fatal("observability config must be applied after the smoke checks and before pruning")
	}
}

// releaseTar packs files (path -> body) into a source.tar the way git archive lays out the repo.
func releaseTar(t *testing.T, files map[string]string) string {
	t.Helper()
	src := t.TempDir()
	writeTree(t, src, files)
	out := filepath.Join(t.TempDir(), "source.tar")
	if output, err := exec.Command("tar", append([]string{"-C", src, "-cf", out}, topLevel(t, src)...)...).CombinedOutput(); err != nil {
		t.Fatalf("tar: %v\n%s", err, output)
	}
	return out
}

// topLevel lists root's entries, so tar paths have no "./" prefix, like git archive.
func topLevel(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

var mirroredRelease = map[string]string{
	"deploy/platform/phase1/observability/prometheus.yaml":                                   "rule_files: [/etc/prometheus/alerts/*.yaml]\n",
	"deploy/platform/phase1/observability/grafana/provisioning/datasources/datasources.yaml": "apiVersion: 1\n",
	"deploy/platform/phase1/observability/grafana/provisioning/dashboards/dashboards.yaml":   "apiVersion: 1\n",
	"infra/observability/alerts/host-resource.yaml":                                          "groups: []\n",
	"infra/observability/alerts/host-resource.rules.test.yml":                                "tests: []\n",
	"infra/observability/dashboards/s0-platform.json":                                        "{}\n",
}

func TestObservabilitySyncRemovesFilesDeletedFromTheRelease(t *testing.T) {
	platform := t.TempDir()
	writeTree(t, platform, mirroredRelease)
	outside := filepath.Join(t.TempDir(), "keep.yaml")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := []string{
		"infra/observability/alerts/retired.yaml",
		"infra/observability/alerts/retired.rules.test.yml",
		"infra/observability/alerts/nested/old.yaml",
		"infra/observability/dashboards/retired.json",
		"deploy/platform/phase1/observability/grafana/provisioning/datasources/retired.yaml",
	}
	// Outside the mirrored directories: deleted from the repo too, but sync must not touch them.
	untouched := []string{
		"infra/observability/prometheus.yaml",
		"infra/observability/alerts-archive/old.yaml",
		"deploy/platform/phase1/observability/retired.yaml",
		"deploy/platform/phase1/runtime/alert-receiver/wecom.url",
		"deploy/platform/phase1/environments/server.runtime.env",
		"observability-applied.tsv",
	}
	for _, name := range append(append([]string{}, stale...), untouched...) {
		writeTree(t, platform, map[string]string{name: "stale\n"})
	}
	if err := os.Symlink(outside, filepath.Join(platform, "infra/observability/alerts/linked.yaml")); err != nil {
		t.Fatal(err)
	}

	output, docker, err := runObservability(t, nil, "sync", releaseTar(t, mirroredRelease), platform)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, output)
	}
	for _, name := range append(stale, "infra/observability/alerts/linked.yaml") {
		if exists(filepath.Join(platform, name)) {
			t.Errorf("stale %s was not removed:\n%s", name, output)
		}
		if !strings.Contains(output, "removed "+name) {
			t.Errorf("removal of %s was not logged:\n%s", name, output)
		}
	}
	if exists(filepath.Join(platform, "infra/observability/alerts/nested")) {
		t.Error("the emptied nested directory was left behind")
	}
	for name := range mirroredRelease {
		if !exists(filepath.Join(platform, name)) {
			t.Errorf("release file %s was removed", name)
		}
	}
	for _, name := range untouched {
		if !exists(filepath.Join(platform, name)) {
			t.Errorf("%s is outside the mirrored directories but was removed", name)
		}
	}
	if string(mustRead(t, outside)) != "outside\n" {
		t.Error("the symlink target outside the platform tree was changed")
	}
	if docker != "" {
		t.Errorf("sync must not call docker:\n%s", docker)
	}

	// Once in sync, the host fingerprints like the release, so apply restarts only on a real change.
	output, _, err = runObservability(t, nil, "sync", releaseTar(t, mirroredRelease), platform)
	if err != nil || strings.Contains(output, "removed ") {
		t.Fatalf("second sync removed files (err %v):\n%s", err, output)
	}
}

func TestObservabilitySyncLeavesDirectoriesTheReleaseDoesNotShip(t *testing.T) {
	platform := t.TempDir()
	writeTree(t, platform, mirroredRelease)
	onlyAlerts := map[string]string{"infra/observability/alerts/host-resource.yaml": "groups: []\n"}
	output, _, err := runObservability(t, nil, "sync", releaseTar(t, onlyAlerts), platform)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, output)
	}
	if !exists(filepath.Join(platform, "infra/observability/dashboards/s0-platform.json")) ||
		!exists(filepath.Join(platform, "deploy/platform/phase1/observability/grafana/provisioning/datasources/datasources.yaml")) {
		t.Fatalf("a release without dashboards or provisioning emptied them:\n%s", output)
	}
	if !strings.Contains(output, "the release has no files under infra/observability/dashboards; left as is") {
		t.Fatalf("missing explanation:\n%s", output)
	}
	if exists(filepath.Join(platform, "infra/observability/alerts/host-resource.rules.test.yml")) {
		t.Fatalf("alerts dir was not mirrored:\n%s", output)
	}
}

func TestObservabilitySyncSkipsASymlinkedDirectory(t *testing.T) {
	platform := t.TempDir()
	elsewhere := t.TempDir()
	writeTree(t, elsewhere, map[string]string{"retired.json": "{}\n"})
	if err := os.MkdirAll(filepath.Join(platform, "infra/observability"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(platform, "infra/observability/dashboards")); err != nil {
		t.Fatal(err)
	}
	output, _, err := runObservability(t, nil, "sync", releaseTar(t, mirroredRelease), platform)
	if err != nil {
		t.Fatalf("a symlinked directory must be skipped, not fail the deploy: %v\n%s", err, output)
	}
	if !exists(filepath.Join(elsewhere, "retired.json")) {
		t.Fatalf("sync deleted through a symlink:\n%s", output)
	}
	if !strings.Contains(output, "is not a plain directory") {
		t.Fatalf("missing warning:\n%s", output)
	}
}

// Through apply-on-server.sh with the real observability-config.sh: a rule file deleted from the repo
// is gone from the host after the deploy, and the rest of the platform tree is kept.
func TestApplyOnServerRemovesAlertRulesDeletedFromTheRepo(t *testing.T) {
	bin, store, platform := applyFixture(t)
	seedImage(t, store, "hvac/energy-api:0.1.0-dev", 3, "")
	writeTree(t, platform, map[string]string{
		"infra/observability/alerts/host-resource.yaml": "groups: []\n",
		"infra/observability/alerts/retired.yaml":       "groups: [{name: retired, rules: []}]\n",
		"deploy/platform/phase1/runtime/keep.txt":       "runtime\n",
	})
	script := string(mustRead(t, "observability-config.sh"))
	output := applyReleaseWith(t, bin, store, platform, 1, map[string]string{
		"infra/observability/alerts/host-resource.yaml":        "groups: []\n",
		"deploy/platform/phase1/cd/observability-config.sh":    script,
		"deploy/platform/phase1/observability/prometheus.yaml": "rule_files: [/etc/prometheus/alerts/*.yaml]\n",
	})
	if exists(filepath.Join(platform, "infra/observability/alerts/retired.yaml")) {
		t.Fatalf("the deleted rule file is still on the host:\n%s", output)
	}
	if !strings.Contains(output, "observability: removed infra/observability/alerts/retired.yaml") {
		t.Fatalf("removal not logged:\n%s", output)
	}
	if !exists(filepath.Join(platform, "infra/observability/alerts/host-resource.yaml")) ||
		!exists(filepath.Join(platform, "deploy/platform/phase1/runtime/keep.txt")) {
		t.Fatalf("sync removed files it does not own:\n%s", output)
	}
}
