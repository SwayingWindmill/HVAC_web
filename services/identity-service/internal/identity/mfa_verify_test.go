package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

type stubTOTPStepUpVerifier struct {
	secret          string
	lastUsedCounter int64
	failedAttempts  int
	locked          bool
	notConfigured   bool
	now             time.Time
	lastUserID      string
	lastCode        string
}

func (s *stubTOTPStepUpVerifier) VerifyTOTPByUserID(_ context.Context, userID, passcode string, _ *MFAProtector, now time.Time) error {
	s.lastUserID = userID
	s.lastCode = passcode
	if s.notConfigured {
		return ErrMFANotConfigured
	}
	if s.locked {
		return ErrMFALocked
	}
	counter, ok := validateTOTP(s.secret, passcode, now, s.lastUsedCounter)
	if ok {
		s.lastUsedCounter = counter
		s.failedAttempts = 0
		return nil
	}
	err := classifyTOTPFailure(s.secret, passcode, now, s.lastUsedCounter)
	s.failedAttempts++
	if s.failedAttempts >= 5 {
		s.locked = true
		return ErrMFALocked
	}
	return err
}

func TestInternalTOTPVerifyEndpoint(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := GenerateTOTP("HVAC", "operator@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	userID := "01900000-0000-7000-8000-000000000111"
	verifier := &stubTOTPStepUpVerifier{secret: secret, lastUsedCounter: -1, now: now}
	server := &Server{
		issuer:        "https://issuer.example.test",
		gatewayKey:    &key.PublicKey,
		gatewaySPIFFE: "spiffe://hvac.local/platform-gateway",
		now:           func() time.Time { return now },
		totpVerifier:  verifier,
	}

	signGrant := func(subject string) string {
		grant, err := identitycontext.SignDelegation(key, identitycontext.DelegationClaims{
			Issuer:           server.gatewaySPIFFE,
			ExecutingService: server.gatewaySPIFFE,
			Subject:          subject,
			SubjectIssuer:    server.issuer,
			Audience:         MFATOTPVerifyAudience,
			TenantID:         "01900000-0000-7000-8000-000000000001",
			Actions:          []string{MFATOTPVerifyAction},
			Scopes:           []string{"session:sess-1"},
			PolicyRevision:   "p-1",
			SessionID:        "sess-1",
			IssuedAt:         now.Unix(),
			ExpiresAt:        now.Add(time.Minute).Unix(),
			TokenID:          "tok-1",
		})
		if err != nil {
			t.Fatal(err)
		}
		return grant
	}

	// Unauthenticated request fails closed.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, InternalMFATOTPVerifyURL, strings.NewReader(`{"code":"123456"}`))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing grant status=%d", rec.Code)
	}

	// Non-UUID subject in grant fails closed.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, InternalMFATOTPVerifyURL, strings.NewReader(`{"code":"123456"}`))
	req.Header.Set("X-Delegation-Grant", signGrant("not-a-uuid"))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("non-uuid subject status=%d", rec.Code)
	}

	// Malformed 5-digit code fails with 400 mfa_code_invalid.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, InternalMFATOTPVerifyURL, strings.NewReader(`{"code":"12345"}`))
	req.Header.Set("X-Delegation-Grant", signGrant(userID))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "mfa_code_invalid") {
		t.Fatalf("malformed code status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Valid TOTP code verifies cleanly and updates lastUsedCounter.
	validCode, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, InternalMFATOTPVerifyURL, strings.NewReader(`{"code":"`+validCode+`"}`))
	req.Header.Set("X-Delegation-Grant", signGrant(userID))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid TOTP status=%d body=%s", rec.Code, rec.Body.String())
	}
	var okBody map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&okBody); err != nil || okBody["verified"] != true {
		t.Fatalf("unexpected success body %#v err=%v", okBody, err)
	}
	if verifier.lastUserID != userID {
		t.Fatalf("verified user=%q want %q", verifier.lastUserID, userID)
	}

	// Replaying the same code in the same window returns 409 mfa_code_replayed.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, InternalMFATOTPVerifyURL, strings.NewReader(`{"code":"`+validCode+`"}`))
	req.Header.Set("X-Delegation-Grant", signGrant(userID))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "mfa_code_replayed") {
		t.Fatalf("replay status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Wrong code returns 401 mfa_code_invalid, and locks on 5th failed attempt (423 mfa_locked).
	wrongCode := "000000"
	if wrongCode == validCode {
		wrongCode = "999999"
	}
	for attempt := range 4 {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, InternalMFATOTPVerifyURL, strings.NewReader(`{"code":"`+wrongCode+`"}`))
		req.Header.Set("X-Delegation-Grant", signGrant(userID))
		server.Handler().ServeHTTP(rec, req)
		if attempt < 3 && (rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "mfa_code_invalid")) {
			t.Fatalf("attempt %d status=%d body=%s", attempt, rec.Code, rec.Body.String())
		}
		if attempt == 3 && (rec.Code != http.StatusLocked || !strings.Contains(rec.Body.String(), "mfa_locked")) {
			t.Fatalf("lockout status=%d body=%s", rec.Code, rec.Body.String())
		}
	}

	// Unconfigured MFA returns 412 mfa_not_configured.
	verifier.notConfigured = true
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, InternalMFATOTPVerifyURL, strings.NewReader(`{"code":"123456"}`))
	req.Header.Set("X-Delegation-Grant", signGrant(userID))
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusPreconditionFailed || !strings.Contains(rec.Body.String(), "mfa_not_configured") {
		t.Fatalf("unconfigured status=%d body=%s", rec.Code, rec.Body.String())
	}
	_ = errors.New("")
}
