package notificationservice

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writeReloaderFixtureFiles(t *testing.T, dir, recipientsJSON, routingJSON string) (string, string) {
	t.Helper()
	recipientsPath := filepath.Join(dir, "recipients.json")
	routingDir := filepath.Join(dir, "sites")
	if err := os.MkdirAll(routingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recipientsPath, []byte(recipientsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(routingDir, "songyaxuan.alarm-routing.json"), []byte(routingJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	return recipientsPath, routingDir
}

func validReloaderRecipientsJSON(email, wecom string) string {
	return `{"recipients":{"duty":{"name":"值班","email":"` + email + `","wecomUserId":"` + wecom + `"},"lead":{"name":"主管","email":"lead@example.test","wecomUserId":"lead-secret-id"}}}`
}

func validReloaderRoutingJSON(holdMinutes, groupWindow int) string {
	return `{
  "schemaVersion": 1,
  "siteId": "` + deliveryTestSiteID + `",
  "siteName": "松雅轩",
  "timezone": "Asia/Shanghai",
  "holdMinutes": ` + itoa(holdMinutes) + `,
  "maxAgeHours": 12,
  "rateLimitPerHour": 6,
  "quietHours": {"start": "22:00", "end": "07:00", "bypassSeverity": "CRITICAL"},
  "rootCauseAlarmType": "reports-interrupted",
  "suppressWhenRootCause": ["reading-gap/"],
  "routes": [
    {
      "name": "major",
      "minSeverity": "MAJOR",
      "cooldownMinutes": 10,
      "groupWindowSeconds": ` + itoa(groupWindow) + `,
      "groupMaxItems": 10,
      "groupBy": "severity",
      "notifyAck": true,
      "notifyRecovery": true,
      "steps": [
        {"afterMinutes": 0, "recipients": ["duty"], "channels": ["wecom_app", "smtp"]},
        {"afterMinutes": 15, "recipients": ["lead"], "channels": ["wecom_app", "wecom_bot"]}
      ]
    }
  ]
}`
}

func TestRoutingReloaderFailClosedAndAtomicReloadRollback(t *testing.T) {
	dir := t.TempDir()
	recipientsPath, routingDir := writeReloaderFixtureFiles(
		t,
		dir,
		validReloaderRecipientsJSON("duty-secret@example.test", "duty-wecom-secret"),
		validReloaderRoutingJSON(3, 60),
	)

	if _, err := NewRoutingReloader(filepath.Join(dir, "missing.json"), routingDir, nil); err == nil {
		t.Fatal("expected initial load to fail closed on missing recipients file")
	}
	if _, err := NewRoutingReloader(recipientsPath, filepath.Join(dir, "missing-sites"), nil); err == nil {
		t.Fatal("expected initial load to fail closed on missing routing dir")
	}

	var mu sync.Mutex
	var outcomes []string
	reloader, err := NewRoutingReloader(recipientsPath, routingDir, func(outcome string) {
		mu.Lock()
		outcomes = append(outcomes, outcome)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("NewRoutingReloader failed: %v", err)
	}

	var logBuf bytes.Buffer
	reloader.logger.Store(slog.New(slog.NewJSONHandler(&logBuf, nil)))

	snap0 := reloader.Snapshot()
	if snap0 == nil || snap0.ReloadCount != 0 || snap0.Digest == "" || snap0.LastErrorCode != "" {
		t.Fatalf("unexpected initial snapshot: %+v", snap0)
	}

	// Unchanged files: returns (false, digest, nil) without swapping pointer or calling onReload.
	changed, digest, err := reloader.ReloadNow(context.Background())
	if err != nil || changed || digest != snap0.Digest {
		t.Fatalf("unchanged ReloadNow = (%v, %q, %v), want (false, %q, nil)", changed, digest, err, snap0.Digest)
	}
	if reloader.Snapshot() != snap0 {
		t.Fatal("unchanged ReloadNow swapped snapshot pointer")
	}

	// Valid change: atomically swaps pointer and invokes onReload("success").
	if err := os.WriteFile(filepath.Join(routingDir, "songyaxuan.alarm-routing.json"), []byte(validReloaderRoutingJSON(5, 120)), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, newDigest, err := reloader.ReloadNow(context.Background())
	if err != nil || !changed || newDigest == "" || newDigest == snap0.Digest {
		t.Fatalf("valid ReloadNow = (%v, %q, %v)", changed, newDigest, err)
	}
	snap1 := reloader.Snapshot()
	if snap1 == snap0 || snap1.ReloadCount != 1 || snap1.LastErrorCode != "" {
		t.Fatalf("unexpected snapshot after reload: %+v", snap1)
	}
	site1, ok := snap1.Routing.Site(deliveryTestSiteID)
	if !ok || site1.HoldMinutes != 5 || site1.Routes[0].GroupWindowSeconds != 120 {
		t.Fatalf("reloaded site routing mismatch: ok=%v site=%+v", ok, site1)
	}

	// Inject corrupt routing JSON: retains snap1 routing/recipients, records bounded error code, logs sanitized error.
	if err := os.WriteFile(filepath.Join(routingDir, "songyaxuan.alarm-routing.json"), []byte(`{"schemaVersion": 1, "bad": true`), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, errDigest, err := reloader.ReloadNow(context.Background())
	if err == nil || changed || errDigest != snap1.Digest {
		t.Fatalf("corrupt ReloadNow = (%v, %q, %v), want error with retained digest %q", changed, errDigest, err, snap1.Digest)
	}
	snapAfterErr := reloader.Snapshot()
	if snapAfterErr.Digest != snap1.Digest || snapAfterErr.ReloadCount != 1 || snapAfterErr.LastErrorCode != ReloadErrorRoutingInvalid {
		t.Fatalf("unexpected snapshot after failed reload: %+v", snapAfterErr)
	}
	if snapAfterErr.Routing != snap1.Routing || snapAfterErr.Recipients != snap1.Recipients {
		t.Fatal("failed reload did not retain previous known-good routing/recipients pointers")
	}
	if !strings.Contains(logBuf.String(), "alarm_routing_reload_failed") || !strings.Contains(logBuf.String(), ReloadErrorRoutingInvalid) {
		t.Fatalf("expected sanitized failure log, got %s", logBuf.String())
	}
	for _, secret := range []string{dir, "duty-secret@example.test", "duty-wecom-secret", "lead-secret-id"} {
		if strings.Contains(logBuf.String(), secret) {
			t.Fatalf("reload failure log leaked sensitive/path data %q: %s", secret, logBuf.String())
		}
	}

	// Inject recipient missing contact for smtp: classified as ROUTING_RECIPIENT_MISMATCH.
	if err := os.WriteFile(filepath.Join(routingDir, "songyaxuan.alarm-routing.json"), []byte(validReloaderRoutingJSON(5, 120)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recipientsPath, []byte(`{"recipients":{"duty":{"name":"值班","wecomUserId":"duty-wecom-secret"},"lead":{"name":"主管","wecomUserId":"lead-secret-id"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, _, err := reloader.ReloadNow(context.Background()); err == nil || changed {
		t.Fatalf("expected cross-validation mismatch failure, got changed=%v err=%v", changed, err)
	}
	if gotCode := reloader.Snapshot().LastErrorCode; gotCode != ReloadErrorRecipientMismatch {
		t.Fatalf("LastErrorCode = %q, want %q", gotCode, ReloadErrorRecipientMismatch)
	}

	// Fix recipients and verify error code clears and reload count advances.
	if err := os.WriteFile(recipientsPath, []byte(validReloaderRecipientsJSON("duty-updated@example.test", "duty-wecom-updated")), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, _, err = reloader.ReloadNow(context.Background())
	if err != nil || !changed {
		t.Fatalf("recovery ReloadNow failed: changed=%v err=%v", changed, err)
	}
	snapRecovered := reloader.Snapshot()
	if snapRecovered.ReloadCount != 2 || snapRecovered.LastErrorCode != "" || snapRecovered.Recipients.Recipients["duty"].Email != "duty-updated@example.test" {
		t.Fatalf("unexpected recovered snapshot: %+v", snapRecovered)
	}

	mu.Lock()
	gotOutcomes := append([]string(nil), outcomes...)
	mu.Unlock()
	if strings.Join(gotOutcomes, ",") != "success,failed,failed,success" {
		t.Fatalf("onReload outcomes = %v, want [success failed failed success]", gotOutcomes)
	}
}

func TestRoutingReloaderConcurrentReadsReloadsAndBadInjectionsRace(t *testing.T) {
	dir := t.TempDir()
	recipientsPath, routingDir := writeReloaderFixtureFiles(
		t,
		dir,
		validReloaderRecipientsJSON("duty@example.test", "duty-wecom"),
		validReloaderRoutingJSON(3, 60),
	)
	reloader, err := NewRoutingReloader(recipientsPath, routingDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	reloader.logger.Store(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				snap := reloader.Snapshot()
				if snap == nil || snap.Routing == nil || snap.Recipients == nil {
					t.Error("snapshot became nil during concurrent reload")
					return
				}
				site, ok := snap.Routing.Site(deliveryTestSiteID)
				if !ok || len(site.Routes) == 0 {
					t.Error("known-good site routing missing during concurrent reload")
					return
				}
				if summary, ok := reloader.SiteRoutingSummary(deliveryTestSiteID); !ok || summary.SiteID != deliveryTestSiteID {
					t.Error("SiteRoutingSummary failed during concurrent reload")
					return
				}
			}
		}()
	}

	routingFile := filepath.Join(routingDir, "songyaxuan.alarm-routing.json")
	for worker := range 3 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for step := range 25 {
				switch (step + id) % 3 {
				case 0:
					_ = os.WriteFile(routingFile, []byte(validReloaderRoutingJSON(1+(step%10), 60)), 0o600)
				case 1:
					_ = os.WriteFile(routingFile, []byte(`{"schemaVersion":1,"broken":true}`), 0o600)
				case 2:
					_ = os.WriteFile(routingFile, []byte(validReloaderRoutingJSON(2+(step%10), 90)), 0o600)
				}
				_, _, _ = reloader.ReloadNow(ctx)
			}
		}(worker)
	}

	wg.Wait()
	// Final valid write must always succeed cleanly.
	if err := os.WriteFile(routingFile, []byte(validReloaderRoutingJSON(4, 60)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reloader.ReloadNow(ctx); err != nil {
		t.Fatalf("final ReloadNow failed: %v", err)
	}
}

func TestRoutingReloaderRunRecoversPanicAndStopsOnCancel(t *testing.T) {
	dir := t.TempDir()
	recipientsPath, routingDir := writeReloaderFixtureFiles(
		t,
		dir,
		validReloaderRecipientsJSON("duty@example.test", "duty-wecom"),
		validReloaderRoutingJSON(3, 60),
	)
	var panics atomic.Int32
	panickedCh := make(chan struct{})
	recoveredCh := make(chan struct{})
	var reloader *RoutingReloader
	var err error
	reloader, err = NewRoutingReloader(recipientsPath, routingDir, func(outcome string) {
		count := panics.Add(1)
		if count == 1 {
			close(panickedCh)
			panic("simulated onReload panic")
		}
		if site, ok := reloader.Snapshot().Routing.Site(deliveryTestSiteID); ok && site.HoldMinutes == 7 {
			select {
			case <-recoveredCh:
			default:
				close(recoveredCh)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	var logBuf syncBuffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		reloader.Run(ctx, 10*time.Millisecond, logger)
	}()

	if err := os.WriteFile(filepath.Join(routingDir, "songyaxuan.alarm-routing.json"), []byte(validReloaderRoutingJSON(6, 60)), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-panickedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("background Run did not tick and trigger panic callback")
	}

	// Verify Run loop stayed alive after recovering the tick panic and processes another change.
	if err := os.WriteFile(filepath.Join(routingDir, "songyaxuan.alarm-routing.json"), []byte(validReloaderRoutingJSON(7, 60)), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-recoveredCh:
	case <-time.After(2 * time.Second):
		t.Fatal("background Run did not continue reloading after panic recovery")
	}
	if !strings.Contains(logBuf.String(), "GOROUTINE_PANIC_RECOVERED") {
		t.Fatalf("expected GOROUTINE_PANIC_RECOVERED log, got %s", logBuf.String())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop on context cancellation")
	}
}

func TestSiteRoutingSummaryRedactsAllSecretsAndPaths(t *testing.T) {
	dir := t.TempDir()
	recipientsPath, routingDir := writeReloaderFixtureFiles(
		t,
		dir,
		validReloaderRecipientsJSON("duty-private-email@example.test", "wx-duty-secret-id"),
		validReloaderRoutingJSON(3, 60),
	)
	reloader, err := NewRoutingReloader(recipientsPath, routingDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	summary, ok := reloader.SiteRoutingSummary(deliveryTestSiteID)
	if !ok {
		t.Fatal("expected SiteRoutingSummary to find configured site")
	}
	if summary.SiteName != "松雅轩" || summary.HoldMinutes != 3 || summary.MaxAgeHours != 12 || summary.RateLimitPerHour != 6 {
		t.Fatalf("unexpected summary header: %+v", summary)
	}
	if len(summary.Routes) != 1 || summary.Routes[0].GroupWindowSeconds != 60 || summary.Routes[0].GroupMaxItems != 10 || summary.Routes[0].GroupBy != "severity" {
		t.Fatalf("unexpected summary routes: %+v", summary.Routes)
	}
	if len(summary.Routes[0].Steps) != 2 || summary.Routes[0].Steps[0].RecipientCount != 1 || summary.Routes[0].Steps[1].RecipientCount != 1 {
		t.Fatalf("unexpected summary steps: %+v", summary.Routes[0].Steps)
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	for _, forbidden := range []string{"duty-private-email", "wx-duty-secret-id", "lead@example.test", "lead-secret-id", `"duty"`, `"lead"`, dir, recipientsPath, routingDir} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("SiteRoutingSummary leaked forbidden string %q in JSON: %s", forbidden, encoded)
		}
	}
	if _, ok := reloader.SiteRoutingSummary("0190f000-0010-7000-8000-999999999999"); ok {
		t.Fatal("expected unknown site to return ok=false")
	}
	if _, ok := (*RoutingReloader)(nil).SiteRoutingSummary(deliveryTestSiteID); ok {
		t.Fatal("expected nil reloader to return ok=false")
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
