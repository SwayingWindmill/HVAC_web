package optimization

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/intelligencemodel"
	"github.com/quanlaihe/hvac-web/libs/observability"
)

type stubActiveSolverResolver struct {
	mu        sync.Mutex
	selection ActiveSolverSelection
	err       error
	calls     int
	lastLoop  string
}

func (stub *stubActiveSolverResolver) ResolveActiveSolver(_ context.Context, _, _, loopKey string, _ time.Time) (ActiveSolverSelection, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.calls++
	stub.lastLoop = loopKey
	if stub.err != nil {
		return ActiveSolverSelection{}, stub.err
	}
	return stub.selection, nil
}

type spySolverAdapter struct {
	calls int
	inner SolverAdapter
}

func (spy *spySolverAdapter) Recommend(ctx context.Context, req Request, at time.Time) (Recommendation, error) {
	spy.calls++
	return spy.inner.Recommend(ctx, req, at)
}

func TestSanitizeServingRefStripsUserinfoQueryAndFragment(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "builtin baseline",
			in:   "  builtin://hvac-recommendation-v1  ",
			want: "builtin://hvac-recommendation-v1",
		},
		{
			name: "builtin cop model with query",
			in:   "BUILTIN://chiller-cop-model?debug=1#v2",
			want: "builtin://chiller-cop-model",
		},
		{
			name: "https strips userinfo query and fragment",
			in:   "HTTPS://user:secret@solver.internal:8443/v1/recommend?apiKey=abc#section",
			want: "https://solver.internal:8443/v1/recommend",
		},
		{
			name: "empty rejected",
			in:   "   ",
			want: "",
		},
		{
			name: "relative path without scheme or host rejected",
			in:   "/v1/recommend",
			want: "",
		},
		{
			name: "missing scheme rejected",
			in:   "://missing-scheme",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeServingRef(tc.in); got != tc.want {
				t.Fatalf("sanitizeServingRef(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseAllowedSolverHostsAndAllowlistRejection(t *testing.T) {
	if got := ParseAllowedSolverHosts(" , "); len(got) != 0 {
		t.Fatalf("expected empty allowlist, got %v", got)
	}
	allowed := ParseAllowedSolverHosts(" ML.Internal , 127.0.0.1 , ml.internal, solver.local:8443 ")
	if len(allowed) != 3 || allowed[0] != "ml.internal" || allowed[1] != "127.0.0.1" || allowed[2] != "solver.local:8443" {
		t.Fatalf("unexpected normalized allowlist: %v", allowed)
	}

	var remoteHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req := validRequest()
	at := req.ValidFrom.Add(time.Minute)

	// Default empty allowlist must never dial any remote HTTP host.
	defaultAdapter := NewRoutingSolverAdapter(RoutingSolverConfig{
		Resolver: &stubActiveSolverResolver{
			selection: ActiveSolverSelection{
				ServingRef: server.URL + "/solve",
				Source:     SolverSourceSiteLoopCanary,
				LoopKey:    DefaultSolverLoopKey,
			},
		},
	})
	rec, err := defaultAdapter.Recommend(t.Context(), req, at)
	if err != nil {
		t.Fatalf("empty allowlist fallback failed: %v", err)
	}
	if remoteHits.Load() != 0 {
		t.Fatalf("expected 0 remote HTTP calls with empty allowlist, got %d", remoteHits.Load())
	}
	if rec.Approval != intelligencemodel.RecommendationDraft {
		t.Fatalf("expected DRAFT recommendation, got %s", rec.Approval)
	}

	// Non-matching allowlist must reject and fall back without hitting server.
	mismatchedAdapter := NewRoutingSolverAdapter(RoutingSolverConfig{
		Resolver: &stubActiveSolverResolver{
			selection: ActiveSolverSelection{
				ServingRef: server.URL + "/solve",
				Source:     SolverSourceDeploymentBinding,
				LoopKey:    DefaultSolverLoopKey,
			},
		},
		AllowedHosts: []string{"other-host.internal"},
	})
	if _, err := mismatchedAdapter.Recommend(t.Context(), req, at); err != nil {
		t.Fatalf("mismatched allowlist fallback failed: %v", err)
	}
	if remoteHits.Load() != 0 {
		t.Fatalf("expected 0 remote HTTP calls for unallowlisted host, got %d", remoteHits.Load())
	}
}

func TestRoutingSolverAdapterRoutesDefaultAndCOPModelWithFallback(t *testing.T) {
	at := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)

	t.Run("default solver and resolver error fallback", func(t *testing.T) {
		baselineSpy := &spySolverAdapter{inner: HVACRecommendationSolver{}}
		resolver := &stubActiveSolverResolver{err: errors.New("db unavailable")}
		adapter := NewRoutingSolverAdapter(RoutingSolverConfig{
			Resolver: resolver,
			Baseline: baselineSpy,
		})
		rec, err := adapter.Recommend(t.Context(), validRequest(), at)
		if err != nil {
			t.Fatalf("expected baseline fallback on resolver error, got %v", err)
		}
		if baselineSpy.calls != 1 || resolver.lastLoop != DefaultSolverLoopKey {
			t.Fatalf("baselineCalls=%d loop=%q", baselineSpy.calls, resolver.lastLoop)
		}
		if rec.Candidate.SupplyTempC <= validRequest().Baseline.SupplyTempC {
			t.Fatalf("expected HVACRecommendationSolver warmer candidate, got %.2f", rec.Candidate.SupplyTempC)
		}
	})

	t.Run("builtin chiller cop model promotes when sufficient data and falls back otherwise", func(t *testing.T) {
		sufficientHistory := &fixedPlantHistory{buckets: syntheticPlant(at, 14*96, true)}
		baselineSpy := &spySolverAdapter{inner: HVACRecommendationSolver{}}
		adapter := NewRoutingSolverAdapter(RoutingSolverConfig{
			Resolver: &stubActiveSolverResolver{
				selection: ActiveSolverSelection{
					ServingRef: ChillerCOPBuiltinSolverRef,
					Source:     SolverSourceSiteLoopCanary,
					LoopKey:    DefaultSolverLoopKey,
				},
			},
			Baseline: baselineSpy,
			COPModel: &COPModelSolver{History: sufficientHistory},
		})
		rec, err := adapter.Recommend(t.Context(), copRequest(), at)
		if err != nil {
			t.Fatalf("COP model routing failed: %v", err)
		}
		if baselineSpy.calls != 0 {
			t.Fatalf("expected COP model to serve without falling back, baselineCalls=%d", baselineSpy.calls)
		}
		if rec.Candidate.SupplyTempC <= copRequest().Baseline.SupplyTempC {
			t.Fatalf("expected COP recommendation supply temp increase, got %.2f", rec.Candidate.SupplyTempC)
		}

		insufficientAdapter := NewRoutingSolverAdapter(RoutingSolverConfig{
			Resolver: &stubActiveSolverResolver{
				selection: ActiveSolverSelection{
					ServingRef: ChillerCOPBuiltinSolverRef,
					Source:     SolverSourceSiteLoopCanary,
					LoopKey:    DefaultSolverLoopKey,
				},
			},
			Baseline: baselineSpy,
			COPModel: &COPModelSolver{History: &fixedPlantHistory{}},
		})
		if _, err := insufficientAdapter.Recommend(t.Context(), copRequest(), at); err != nil {
			t.Fatalf("expected fallback to baseline when COP data insufficient: %v", err)
		}
		if baselineSpy.calls != 1 {
			t.Fatalf("expected fallback call to baseline, got %d", baselineSpy.calls)
		}
	})
}

func TestRoutingSolverAdapterRemoteHTTPTracingProvenanceAndFallbacks(t *testing.T) {
	req := validRequest()
	at := req.ValidFrom.Add(time.Minute)

	t.Run("remote http success enforces provenance and injects traceparent", func(t *testing.T) {
		var seenTraceparent string
		var seenMethod string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenMethod = r.Method
			seenTraceparent = r.Header.Get("traceparent")
			w.Header().Set("Content-Type", "application/json")
			// Intentionally spoof tenantId/siteId/approval to prove server enforces frozen provenance.
			_ = json.NewEncoder(w).Encode(remoteSolverResponse{
				TenantID:    "01990000-3000-7000-8000-999999999999",
				SiteID:      "01990000-5000-7000-8000-999999999999",
				Approval:    string(intelligencemodel.RecommendationApproved),
				SupplyTempC: ptr(7.8),
			})
		}))
		defer server.Close()

		parsedURL, err := url.Parse(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		baselineSpy := &spySolverAdapter{inner: HVACRecommendationSolver{}}
		adapter := NewRoutingSolverAdapter(RoutingSolverConfig{
			Resolver: &stubActiveSolverResolver{
				selection: ActiveSolverSelection{
					ServingRef: server.URL + "/solve?secret=1",
					Source:     SolverSourceSiteLoopCanary,
					LoopKey:    DefaultSolverLoopKey,
				},
			},
			Baseline:     baselineSpy,
			AllowedHosts: []string{parsedURL.Hostname()},
			HTTPClient:   server.Client(),
		})

		tracer := observability.NewTracer("optimization-test", nil)
		ctx := observability.ContextWithRemoteParent(t.Context(), tracer, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "")
		rec, err := adapter.Recommend(ctx, req, at)
		if err != nil {
			t.Fatalf("remote HTTP solver failed: %v", err)
		}
		if seenMethod != http.MethodPost {
			t.Fatalf("expected POST, got %s", seenMethod)
		}
		if !strings.HasPrefix(seenTraceparent, "00-4bf92f3577b34da6a3ce929d0e0e4736-") {
			t.Fatalf("expected propagated W3C traceparent, got %q", seenTraceparent)
		}
		if baselineSpy.calls != 0 {
			t.Fatalf("expected remote solver success without fallback, baselineCalls=%d", baselineSpy.calls)
		}
		if rec.Candidate.SupplyTempC != 7.8 {
			t.Fatalf("expected remote supplyTempC 7.8, got %.2f", rec.Candidate.SupplyTempC)
		}
		if rec.TenantID != req.TenantID || rec.SiteID != req.SiteID || rec.Approval != intelligencemodel.RecommendationDraft {
			t.Fatalf("provenance not enforced: tenant=%s site=%s approval=%s", rec.TenantID, rec.SiteID, rec.Approval)
		}
	})

	t.Run("bumpless fallback on 5xx timeout malformed payload constraint violation and open breaker", func(t *testing.T) {
		var mode atomic.Int32
		var hits atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			switch mode.Load() {
			case 0:
				w.WriteHeader(http.StatusServiceUnavailable)
			case 1:
				time.Sleep(150 * time.Millisecond)
				w.WriteHeader(http.StatusOK)
			case 2:
				_, _ = w.Write([]byte(`{"supplyTempC":7.5,"unexpectedField":true}`))
			case 3:
				// Violates SupplyTempMinC (6.0) in validRequest().
				_, _ = w.Write([]byte(`{"supplyTempC":4.5}`))
			case 4:
				// Invalid risk level outside {LOW, MEDIUM, HIGH}.
				_, _ = w.Write([]byte(`{"supplyTempC":7.5,"risk":{"level":"EXTREME","comfortMarginC":1.0}}`))
			case 5:
				// Fabricated expectedImpact far diverging from baseline - candidate delta.
				_, _ = w.Write([]byte(`{"supplyTempC":7.5,"expectedImpact":{"energySavingKwhPerDay":99999,"costSavingPerDay":99999}}`))
			}
		}))
		defer server.Close()

		parsedURL, err := url.Parse(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		baselineExpected, err := (HVACRecommendationSolver{}).Recommend(t.Context(), req, at)
		if err != nil {
			t.Fatal(err)
		}

		// Test 5xx + circuit breaker opening after 2 failures.
		breaker := intelligencemodel.NewCircuitBreaker(intelligencemodel.CircuitBreakerConfig{
			FailureThreshold: 2,
			Cooldown:         30 * time.Second,
		})
		adapter := NewRoutingSolverAdapter(RoutingSolverConfig{
			Resolver: &stubActiveSolverResolver{
				selection: ActiveSolverSelection{ServingRef: server.URL + "/solve", Source: SolverSourceSiteLoopCanary, LoopKey: DefaultSolverLoopKey},
			},
			AllowedHosts: []string{parsedURL.Hostname()},
			HTTPClient:   server.Client(),
			Breaker:      breaker,
			Timeout:      40 * time.Millisecond,
		})

		mode.Store(0) // 503 #1
		rec1, err := adapter.Recommend(t.Context(), req, at)
		if err != nil || rec1.Candidate.SupplyTempC != baselineExpected.Candidate.SupplyTempC {
			t.Fatalf("5xx #1 fallback mismatch: rec=%+v err=%v", rec1.Candidate, err)
		}
		rec2, err := adapter.Recommend(t.Context(), req, at)
		if err != nil || rec2.Candidate.SupplyTempC != baselineExpected.Candidate.SupplyTempC {
			t.Fatalf("5xx #2 fallback mismatch: rec=%+v err=%v", rec2.Candidate, err)
		}
		hitsBeforeOpen := hits.Load()
		rec3, err := adapter.Recommend(t.Context(), req, at)
		if err != nil || rec3.Candidate.SupplyTempC != baselineExpected.Candidate.SupplyTempC {
			t.Fatalf("open breaker fallback mismatch: rec=%+v err=%v", rec3.Candidate, err)
		}
		if hits.Load() != hitsBeforeOpen {
			t.Fatalf("expected open circuit breaker to skip HTTP dial, hits %d -> %d", hitsBeforeOpen, hits.Load())
		}

		// Timeout, unknown field JSON, constraint violation, invalid risk level, and fabricated impact each fall back cleanly.
		for _, testMode := range []int32{1, 2, 3, 4, 5} {
			mode.Store(testMode)
			fresh := NewRoutingSolverAdapter(RoutingSolverConfig{
				Resolver: &stubActiveSolverResolver{
					selection: ActiveSolverSelection{ServingRef: server.URL + "/solve", Source: SolverSourceSiteLoopCanary, LoopKey: DefaultSolverLoopKey},
				},
				AllowedHosts: []string{parsedURL.Hostname()},
				HTTPClient:   server.Client(),
				Timeout:      40 * time.Millisecond,
			})
			rec, err := fresh.Recommend(t.Context(), req, at)
			if err != nil {
				t.Fatalf("mode %d fallback returned error: %v", testMode, err)
			}
			if rec.Candidate.SupplyTempC != baselineExpected.Candidate.SupplyTempC {
				t.Fatalf("mode %d supplyTempC = %.2f, want baseline %.2f", testMode, rec.Candidate.SupplyTempC, baselineExpected.Candidate.SupplyTempC)
			}
		}

		// Saturated bulkhead falls back without hitting server.
		bulkhead := intelligencemodel.NewBulkhead(1)
		if !bulkhead.TryAcquire() {
			t.Fatal("failed to saturate bulkhead")
		}
		defer bulkhead.Release()
		hitsBeforeBulkhead := hits.Load()
		satAdapter := NewRoutingSolverAdapter(RoutingSolverConfig{
			Resolver: &stubActiveSolverResolver{
				selection: ActiveSolverSelection{ServingRef: server.URL + "/solve", Source: SolverSourceSiteLoopCanary, LoopKey: DefaultSolverLoopKey},
			},
			AllowedHosts: []string{parsedURL.Hostname()},
			HTTPClient:   server.Client(),
			Bulkhead:     bulkhead,
		})
		recSat, err := satAdapter.Recommend(t.Context(), req, at)
		if err != nil || recSat.Candidate.SupplyTempC != baselineExpected.Candidate.SupplyTempC {
			t.Fatalf("saturated bulkhead fallback mismatch: rec=%+v err=%v", recSat.Candidate, err)
		}
		if hits.Load() != hitsBeforeBulkhead {
			t.Fatalf("expected saturated bulkhead not to dial HTTP server, hits %d -> %d", hitsBeforeBulkhead, hits.Load())
		}
	})
}
