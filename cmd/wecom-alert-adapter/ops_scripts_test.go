package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "deploy", "platform", "phase1", "observability", "render-alertmanager-config.sh")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRenderAlertmanagerLeavesTheNoneReceiverWhenUnconfigured(t *testing.T) {
	root := repoRoot(t)
	template := filepath.Join(root, "deploy", "platform", "phase1", "observability", "alertmanager.yaml")
	out := filepath.Join(t.TempDir(), "alertmanager.yml")
	cmd := exec.Command("sh", filepath.Join(root, "deploy", "platform", "phase1", "observability", "render-alertmanager-config.sh"), t.TempDir(), out, template)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v\n%s", err, output)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "receiver: none") || strings.Contains(string(got), "qyapi.weixin.qq.com") {
		t.Fatalf("unconfigured config = %s", got)
	}
}

func TestRenderAlertmanagerWeComDoesNotEmbedTheRobotURL(t *testing.T) {
	root := repoRoot(t)
	secrets := t.TempDir()
	if err := os.WriteFile(filepath.Join(secrets, "wecom-webhook-url"), []byte("https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=super-secret-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "alertmanager.yml")
	template := filepath.Join(root, "deploy", "platform", "phase1", "observability", "alertmanager.yaml")
	cmd := exec.Command("sh", filepath.Join(root, "deploy", "platform", "phase1", "observability", "render-alertmanager-config.sh"), secrets, out, template)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v\n%s", err, output)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if strings.Contains(text, "super-secret-key") || strings.Contains(text, "qyapi.weixin.qq.com") {
		t.Fatalf("robot URL leaked into alertmanager config:\n%s", text)
	}
	if !strings.Contains(text, "http://wecom-alert-adapter:9095/alertmanager") || !strings.Contains(text, "receiver: notify") {
		t.Fatalf("missing adapter webhook:\n%s", text)
	}
	if strings.Contains(text, "email_configs") {
		t.Fatalf("smtp was not configured:\n%s", text)
	}
}

func TestRenderAlertmanagerSMTPKeepsThePasswordInTheFile(t *testing.T) {
	root := repoRoot(t)
	secrets := t.TempDir()
	env := "SMTP_SMARTHOST=smtp.example.com:465\nSMTP_FROM=alerts@example.com\nSMTP_AUTH_USERNAME=alerts@example.com\nSMTP_TO=ops@example.com\nSMTP_REQUIRE_TLS=false\n"
	if err := os.WriteFile(filepath.Join(secrets, "smtp.env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "smtp-password"), []byte("s3cret-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "alertmanager.yml")
	template := filepath.Join(root, "deploy", "platform", "phase1", "observability", "alertmanager.yaml")
	cmd := exec.Command("sh", filepath.Join(root, "deploy", "platform", "phase1", "observability", "render-alertmanager-config.sh"), secrets, out, template)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v\n%s", err, output)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if strings.Contains(text, "s3cret-password") {
		t.Fatalf("smtp password leaked:\n%s", text)
	}
	if !strings.Contains(text, "smtp_auth_password_file:") || !strings.Contains(text, `to: "ops@example.com"`) {
		t.Fatalf("smtp receiver missing:\n%s", text)
	}
}

func TestRenderAlertmanagerRejectsABadWeComURL(t *testing.T) {
	root := repoRoot(t)
	secrets := t.TempDir()
	if err := os.WriteFile(filepath.Join(secrets, "wecom-webhook-url"), []byte("https://example.invalid/hook\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join(root, "deploy", "platform", "phase1", "observability", "render-alertmanager-config.sh"), secrets, filepath.Join(t.TempDir(), "out.yml"), filepath.Join(root, "deploy", "platform", "phase1", "observability", "alertmanager.yaml"))
	if err := cmd.Run(); err == nil {
		t.Fatal("expected a bad URL to fail")
	}
}

func TestHostTextfileReadsTheBackupStampAndContainerHealth(t *testing.T) {
	root := repoRoot(t)
	runtime := t.TempDir()
	if err := os.MkdirAll(filepath.Join(runtime, "backups", "postgres", "base", "20261028T193000Z"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runtime, "observability"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime, "observability", "postgres-backup.success"), []byte("1760000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	docker := filepath.Join(bin, "docker")
	script := `#!/bin/sh
if [ "$1" = info ]; then exit 0; fi
if [ "$1" = ps ]; then printf 'abc energy-api\n'; exit 0; fi
if [ "$1" = inspect ]; then printf '4 unhealthy\n'; exit 0; fi
echo "unexpected: $*" >&2
exit 1
`
	if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join(root, "deploy", "platform", "phase1", "observability", "host-textfile.sh"))
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "PHASE1_RUNTIME_DIR="+runtime, "HVAC_COMPOSE_PROJECT=hvac-platform")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("textfile: %v\n%s", err, output)
	}
	got, err := os.ReadFile(filepath.Join(runtime, "observability", "textfile", "host.prom"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		`hvac_backup_last_success_timestamp_seconds{store="postgres"} 1760000000`,
		`hvac_backup_last_success_timestamp_seconds{store="clickhouse"} 0`,
		`hvac_container_restarts{service="energy-api"} 4`,
		`hvac_container_unhealthy{service="energy-api"} 1`,
		"hvac_container_metrics_up 1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
}
