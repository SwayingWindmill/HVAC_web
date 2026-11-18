package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
	"github.com/quanlaihe/hvac-web/services/optimization-service/internal/optimization"
)

type stubMonitorRunner struct {
	calls atomic.Int32
	fn    func(ctx context.Context, call int32) error
}

func (s *stubMonitorRunner) RunMonitorPass(ctx context.Context, _ time.Time) (optimization.MonitorPassSummary, error) {
	call := s.calls.Add(1)
	if s.fn != nil {
		return optimization.MonitorPassSummary{}, s.fn(ctx, call)
	}
	return optimization.MonitorPassSummary{}, nil
}

type stubReconciler struct {
	calls atomic.Int32
	fn    func(ctx context.Context, call int32) (int, error)
}

func (s *stubReconciler) Reconcile(ctx context.Context, _ time.Time, _ int) (int, error) {
	call := s.calls.Add(1)
	if s.fn != nil {
		return s.fn(ctx, call)
	}
	return 0, nil
}

func TestRunMonitorLoopAndReconcileLoopRespectContextCancellation(t *testing.T) {
	monitorCtx, monitorCancel := context.WithCancel(t.Context())
	defer monitorCancel()

	monitor := &stubMonitorRunner{
		fn: func(ctx context.Context, call int32) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Errorf("monitor pass context missing timeout deadline")
			}
			if call >= 2 {
				monitorCancel()
			}
			return nil
		},
	}
	doneMonitor := make(chan struct{})
	go func() {
		defer close(doneMonitor)
		runMonitorLoop(monitorCtx, 5*time.Millisecond, monitor, nil)
	}()
	select {
	case <-doneMonitor:
	case <-time.After(2 * time.Second):
		t.Fatal("runMonitorLoop did not stop after context cancellation")
	}
	if monitor.calls.Load() < 2 {
		t.Fatalf("monitor calls=%d, want >= 2", monitor.calls.Load())
	}

	reconcileCtx, reconcileCancel := context.WithCancel(t.Context())
	defer reconcileCancel()
	observedParentCancel := make(chan bool, 1)
	reconciler := &stubReconciler{
		fn: func(ctx context.Context, call int32) (int, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Errorf("reconcile context missing timeout deadline")
			}
			if call == 1 {
				reconcileCancel()
				select {
				case <-ctx.Done():
					observedParentCancel <- true
				case <-time.After(time.Second):
					observedParentCancel <- false
				}
			}
			return 0, nil
		},
	}
	doneReconcile := make(chan struct{})
	go func() {
		defer close(doneReconcile)
		runReconcileLoop(reconcileCtx, 5*time.Millisecond, reconciler, nil)
	}()
	select {
	case <-doneReconcile:
	case <-time.After(2 * time.Second):
		t.Fatal("runReconcileLoop did not stop after context cancellation")
	}
	if !<-observedParentCancel {
		t.Fatal("reconcile context did not derive from parent ctx")
	}
}

func TestRunSupervisedRecoversPanicAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var logs bytes.Buffer
	logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
	metrics := observability.NewRegistry()
	var wg sync.WaitGroup
	var attempts atomic.Int32

	runSupervised(ctx, &wg, logger, metrics, "optimization.test_worker", 5*time.Millisecond, func() {
		if attempts.Add(1) == 1 {
			panic("boom")
		}
		cancel()
	})

	wg.Wait()
	if attempts.Load() < 2 {
		t.Fatalf("attempts=%d, want >= 2 after panic restart", attempts.Load())
	}
	output := logs.String()
	if !strings.Contains(output, `"background_goroutine_panicked"`) || !strings.Contains(output, `"[REDACTED]"`) {
		t.Fatalf("expected redacted panic recovery log, got %s", output)
	}
}

func TestShutdownServersAndWaitBackground(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: optimization.HealthHandler(), ReadHeaderTimeout: time.Second}
	var wg sync.WaitGroup
	wg.Add(1)
	serveErr := make(chan error, 1)
	go func() {
		defer wg.Done()
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	if err := shutdownServers(2*time.Second, srv, nil); err != nil {
		t.Fatalf("shutdownServers err=%v", err)
	}
	waitBackground(&wg, 2*time.Second, nil)
	select {
	case err := <-serveErr:
		t.Fatalf("unexpected server error: %v", err)
	default:
	}
}

type recordedJobFail struct {
	jobID     string
	errorCode string
	retryable bool
	errMsg    string
}

type stubJobFinalizer struct {
	mu    sync.Mutex
	fails []recordedJobFail
}

func (s *stubJobFinalizer) Fail(_ context.Context, job schedulerjob.Job, errorCode string, executionErr error, retryable bool, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := ""
	if executionErr != nil {
		msg = executionErr.Error()
	}
	s.fails = append(s.fails, recordedJobFail{
		jobID:     job.JobID,
		errorCode: errorCode,
		retryable: retryable,
		errMsg:    msg,
	})
	return nil
}

type stubSiteCycleLocker struct {
	mu           sync.Mutex
	heldSites    map[string]bool
	forceErr     error
	releaseCount atomic.Int32
}

func (l *stubSiteCycleLocker) AcquireSiteCycleLock(_ context.Context, tenantID, siteID string) (func(), bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.forceErr != nil {
		return nil, false, l.forceErr
	}
	if l.heldSites == nil {
		l.heldSites = make(map[string]bool)
	}
	key := tenantID + ":" + siteID
	if l.heldSites[key] {
		return nil, false, nil
	}
	l.heldSites[key] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			delete(l.heldSites, key)
			l.mu.Unlock()
			l.releaseCount.Add(1)
		})
	}, true, nil
}

func TestAcquireSiteCycleLockForJobContentionAndRelease(t *testing.T) {
	job1 := schedulerjob.Job{
		JobID:      "01990000-1111-7000-8000-000000000001",
		TenantID:   "01990000-3000-7000-8000-000000000001",
		SiteID:     "01990000-5000-7000-8000-000000000001",
		ScheduleID: "",
	}
	job2 := schedulerjob.Job{
		JobID:      "01990000-1111-7000-8000-000000000002",
		TenantID:   job1.TenantID,
		SiteID:     job1.SiteID,
		ScheduleID: "",
	}

	t.Run("concurrent replicas with empty schedule_id serialize cleanly", func(t *testing.T) {
		locker := &stubSiteCycleLocker{}
		queue := &stubJobFinalizer{}
		start := make(chan struct{})
		var wg sync.WaitGroup
		lockedFlags := make([]bool, 2)
		releases := make([]func(), 2)

		for idx, job := range []schedulerjob.Job{job1, job2} {
			wg.Add(1)
			go func(i int, j schedulerjob.Job) {
				defer wg.Done()
				<-start
				releases[i], lockedFlags[i] = acquireSiteCycleLockForJob(context.Background(), queue, locker, j, nil)
			}(idx, job)
		}
		close(start)
		wg.Wait()

		wins := 0
		for i, locked := range lockedFlags {
			if locked {
				wins++
				releases[i]()
			}
		}
		if wins != 1 {
			t.Fatalf("expected exactly 1 replica to acquire site lock, got wins=%d", wins)
		}
		if len(queue.fails) != 1 || queue.fails[0].errorCode != "OPTIMIZATION_SITE_CYCLE_ACTIVE" || !queue.fails[0].retryable {
			t.Fatalf("expected retryable OPTIMIZATION_SITE_CYCLE_ACTIVE fail for losing replica, got %+v", queue.fails)
		}
		if locker.releaseCount.Load() != 1 {
			t.Fatalf("expected winner release count=1, got %d", locker.releaseCount.Load())
		}

		// After release, another cycle on the same site can acquire cleanly.
		rel, locked := acquireSiteCycleLockForJob(context.Background(), queue, locker, job2, nil)
		if !locked || rel == nil {
			t.Fatal("expected site lock re-acquisition after release")
		}
		rel()
	})

	t.Run("lock acquisition error fails job as retryable", func(t *testing.T) {
		locker := &stubSiteCycleLocker{forceErr: errors.New("pg pool closed")}
		queue := &stubJobFinalizer{}
		rel, locked := acquireSiteCycleLockForJob(context.Background(), queue, locker, job1, nil)
		if locked || rel != nil {
			t.Fatalf("expected locked=false on error, got locked=%v relNonNil=%t", locked, rel != nil)
		}
		if len(queue.fails) != 1 || queue.fails[0].errorCode != "OPTIMIZATION_SITE_LOCK_FAILED" || !queue.fails[0].retryable {
			t.Fatalf("expected retryable OPTIMIZATION_SITE_LOCK_FAILED, got %+v", queue.fails)
		}
	})
}

func TestNewTelemetryClientsUsesEnvAndMTLS(t *testing.T) {
	dir := t.TempDir()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	spiffeURI, _ := url.Parse("spiffe://hvac.local/optimization-service")
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "opt"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{spiffeURI},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "client.crt")
	keyPath := filepath.Join(dir, "client.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyDER}), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("OPTIMIZATION_TELEMETRY_URL", "https://telemetry-runtime-service:18446")
	liveClient, healthClient, err := newTelemetryClients(certPath, keyPath, caPath)
	if err != nil {
		t.Fatalf("newTelemetryClients failed: %v", err)
	}
	if liveClient == nil || healthClient == nil {
		t.Fatalf("expected non-nil telemetry clients: live=%v health=%v", liveClient, healthClient)
	}

	t.Setenv("OPTIMIZATION_TELEMETRY_URL", "://invalid-url")
	if _, _, err := newTelemetryClients(certPath, keyPath, caPath); err == nil {
		t.Fatal("expected invalid OPTIMIZATION_TELEMETRY_URL error")
	}
}
