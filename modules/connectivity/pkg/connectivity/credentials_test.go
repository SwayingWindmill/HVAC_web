package connectivity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testLeafCertificate(t *testing.T, commonName string, notBefore, notAfter time.Time) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestVerifyActiveCredentialCacheSingleflightAndInvalidation(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	store := &Store{now: func() time.Time { return now }}
	svc := NewCredentialService(store, &CertificateAuthority{})

	gwA := "0194f000-2000-7000-8000-00000000000a"
	gwB := "0194f000-2000-7000-8000-00000000000b"
	certA := testLeafCertificate(t, gwA, now.Add(-time.Hour), now.Add(time.Hour))
	certB := testLeafCertificate(t, gwB, now.Add(-time.Hour), now.Add(time.Hour))
	certShort := testLeafCertificate(t, gwA, now.Add(-time.Hour), now.Add(5*time.Second))

	if _, err := svc.VerifyActiveCredential(context.Background(), nil); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("nil peer err=%v, want ErrEnrollmentRejected", err)
	}
	badCN := testLeafCertificate(t, "not-a-uuid", now.Add(-time.Hour), now.Add(time.Hour))
	if _, err := svc.VerifyActiveCredential(context.Background(), badCN); !errors.Is(err, ErrEnrollmentRejected) {
		t.Fatalf("invalid CN err=%v, want ErrEnrollmentRejected", err)
	}

	// 32 concurrent callers coalesce to 1 underlying DB lookup.
	var lookups atomic.Int32
	gate := make(chan struct{})
	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			id, err := svc.verifyActiveCredentialWith(context.Background(), certA, func(_ context.Context, p *x509.Certificate, _ time.Time) (string, error) {
				lookups.Add(1)
				<-gate
				return p.Subject.CommonName, nil
			})
			if err != nil || id != gwA {
				t.Errorf("concurrent verify got=(%q,%v), want=(%q,nil)", id, err, gwA)
			}
		}()
	}
	time.Sleep(25 * time.Millisecond)
	close(gate)
	wg.Wait()
	if lookups.Load() != 1 {
		t.Fatalf("concurrent VerifyActiveCredential lookups=%d, want 1", lookups.Load())
	}

	// Warm cache hits 0 lookups for 14s, expires at 15s.
	now = now.Add(14 * time.Second)
	id, err := svc.verifyActiveCredentialWith(context.Background(), certA, func(context.Context, *x509.Certificate, time.Time) (string, error) {
		t.Fatal("expected 15s cache hit at +14s")
		return "", nil
	})
	if err != nil || id != gwA {
		t.Fatalf("cached verify got=(%q,%v)", id, err)
	}

	now = now.Add(1 * time.Second)
	var afterExpireCalls atomic.Int32
	id, err = svc.verifyActiveCredentialWith(context.Background(), certA, func(_ context.Context, p *x509.Certificate, _ time.Time) (string, error) {
		afterExpireCalls.Add(1)
		return p.Subject.CommonName, nil
	})
	if err != nil || id != gwA || afterExpireCalls.Load() != 1 {
		t.Fatalf("expired verify got=(%q,%v) calls=%d", id, err, afterExpireCalls.Load())
	}

	// Warm gwB, then InvalidateGatewayCache(gwA) busts only gwA immediately.
	if _, err := svc.verifyActiveCredentialWith(context.Background(), certB, func(_ context.Context, p *x509.Certificate, _ time.Time) (string, error) {
		return p.Subject.CommonName, nil
	}); err != nil {
		t.Fatalf("warm gwB failed: %v", err)
	}
	store.InvalidateGatewayCache(gwA)
	var postInvalidateCalls atomic.Int32
	if _, err := svc.verifyActiveCredentialWith(context.Background(), certA, func(_ context.Context, p *x509.Certificate, _ time.Time) (string, error) {
		postInvalidateCalls.Add(1)
		return p.Subject.CommonName, nil
	}); err != nil || postInvalidateCalls.Load() != 1 {
		t.Fatalf("InvalidateGatewayCache did not bust gwA: err=%v calls=%d", err, postInvalidateCalls.Load())
	}
	if _, err := svc.verifyActiveCredentialWith(context.Background(), certB, func(context.Context, *x509.Certificate, time.Time) (string, error) {
		t.Fatal("InvalidateGatewayCache(gwA) unexpectedly evicted gwB")
		return "", nil
	}); err != nil {
		t.Fatalf("gwB cache check failed: %v", err)
	}

	// Negative errors are never cached so re-enable / retry hits DB immediately.
	store.InvalidateGatewayCache(gwA)
	var negCalls atomic.Int32
	if _, err := svc.verifyActiveCredentialWith(context.Background(), certA, func(context.Context, *x509.Certificate, time.Time) (string, error) {
		negCalls.Add(1)
		return "", ErrCredentialRevoked
	}); !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("expected ErrCredentialRevoked, got %v", err)
	}
	negCalls.Store(0)
	for range 2 {
		_, _ = svc.verifyActiveCredentialWith(context.Background(), certA, func(context.Context, *x509.Certificate, time.Time) (string, error) {
			negCalls.Add(1)
			return "", ErrCredentialRevoked
		})
	}
	if negCalls.Load() != 2 {
		t.Fatalf("negative result was cached: calls=%d, want 2", negCalls.Load())
	}

	// Certificate NotAfter shorter than 15s caps TTL at peer.NotAfter.
	store.InvalidateGatewayCache(gwA)
	now = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	var shortCalls atomic.Int32
	if _, err := svc.verifyActiveCredentialWith(context.Background(), certShort, func(_ context.Context, p *x509.Certificate, _ time.Time) (string, error) {
		shortCalls.Add(1)
		return p.Subject.CommonName, nil
	}); err != nil {
		t.Fatalf("short cert warm failed: %v", err)
	}
	now = now.Add(4 * time.Second)
	if _, err := svc.verifyActiveCredentialWith(context.Background(), certShort, func(context.Context, *x509.Certificate, time.Time) (string, error) {
		t.Fatal("short cert should still be cached at +4s")
		return "", nil
	}); err != nil {
		t.Fatalf("short cert hit failed: %v", err)
	}
	now = now.Add(1 * time.Second)
	if _, err := svc.verifyActiveCredentialWith(context.Background(), certShort, func(_ context.Context, p *x509.Certificate, _ time.Time) (string, error) {
		shortCalls.Add(1)
		return p.Subject.CommonName, nil
	}); err != nil || shortCalls.Load() != 2 {
		t.Fatalf("short cert did not expire at peer.NotAfter: err=%v calls=%d", err, shortCalls.Load())
	}

	// Caller 1 cancellation does not break waiting peer.
	store.InvalidateGatewayCache(gwA)
	started := make(chan struct{})
	release := make(chan struct{})
	ctx1, cancel1 := context.WithCancel(context.Background())
	err1Ch := make(chan error, 1)
	go func() {
		_, err := svc.verifyActiveCredentialWith(ctx1, certA, func(queryCtx context.Context, p *x509.Certificate, _ time.Time) (string, error) {
			close(started)
			<-release
			if queryCtx.Err() != nil {
				return "", queryCtx.Err()
			}
			return p.Subject.CommonName, nil
		})
		err1Ch <- err
	}()
	<-started
	res2Ch := make(chan string, 1)
	err2Ch := make(chan error, 1)
	go func() {
		id, err := svc.verifyActiveCredentialWith(context.Background(), certA, func(context.Context, *x509.Certificate, time.Time) (string, error) {
			return "", errors.New("should coalesce")
		})
		res2Ch <- id
		err2Ch <- err
	}()
	time.Sleep(15 * time.Millisecond)
	cancel1()
	if err := <-err1Ch; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller 1 err=%v, want context.Canceled", err)
	}
	close(release)
	if err := <-err2Ch; err != nil {
		t.Fatalf("caller 2 failed after caller 1 cancel: %v", err)
	}
	if got := <-res2Ch; got != gwA {
		t.Fatalf("caller 2 got=%q, want %q", got, gwA)
	}
}
