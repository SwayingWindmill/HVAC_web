package gateway

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/cmd/energy-api/internal/platformapi"
	"github.com/quanlaihe/hvac-web/libs/alarmauth"
	"github.com/quanlaihe/hvac-web/libs/alarmmodel"
	"github.com/quanlaihe/hvac-web/libs/identitycontext"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/sessionstore"
	"github.com/quanlaihe/hvac-web/modules/alarm/pkg/alarmservice"
)

// The Alarm routes the Gateway serves are held to contracts/http/s4-alarm-public.openapi.json: the
// routes match, and every answer the Gateway writes, success or failure, is valid for its status.

func TestAlarmRoutesMatchThePublicContract(t *testing.T) {
	contract := loadAlarmContract(t)
	served := map[string]bool{}
	for _, operation := range contract.operations() {
		path := strings.ReplaceAll(operation.path, "{alarmId}", gatewayAlarmID)
		route, ok := matchPublicAlarmRoute(path)
		if !ok || route.template != operation.path {
			t.Fatalf("contract route %s %s is not served: %#v", operation.method, operation.path, route)
		}
		if method := alarmRouteMethod(route); method != operation.method {
			t.Fatalf("contract route %s %s is served for %s", operation.method, operation.path, method)
		}
		served[operation.method+" "+operation.path] = true
	}
	// Every Alarm route the Gateway serves outside the Site rules (platform contract) is in the contract.
	for _, path := range []string{"/api/v1/alarms", "/api/v1/alarms/" + gatewayAlarmID, "/api/v1/alarms/" + gatewayAlarmID + "/ack", "/api/v1/alarms/" + gatewayAlarmID + "/assign"} {
		route, _ := matchPublicAlarmRoute(path)
		if !served[alarmRouteMethod(route)+" "+route.template] {
			t.Fatalf("Gateway serves %s %s, which the contract does not publish", alarmRouteMethod(route), route.template)
		}
	}
}

func TestAlarmAnswersConformToThePublicContract(t *testing.T) {
	contract := loadAlarmContract(t)
	fixture := newAlarmContractFixture(t, nil)
	alarm := fixture.publish(t)
	detail := "/api/v1/alarms/" + alarm.AlarmID
	unknown := "/api/v1/alarms/01910000-9999-7000-8000-000000000001"

	cases := []struct {
		name        string
		method      string
		path        string
		template    string
		body        string
		idempotency string
		status      int
		code        string
	}{
		{name: "list", method: http.MethodGet, path: "/api/v1/alarms?siteId=" + fixture.siteID + "&limit=10", template: "/api/v1/alarms", status: http.StatusOK},
		{name: "detail", method: http.MethodGet, path: detail, template: "/api/v1/alarms/{alarmId}", status: http.StatusOK},
		{name: "acknowledge", method: http.MethodPost, path: detail + "/ack", template: "/api/v1/alarms/{alarmId}/ack", idempotency: "contract-ack-0001", status: http.StatusOK},
		{name: "assign", method: http.MethodPost, path: detail + "/assign", template: "/api/v1/alarms/{alarmId}/assign", body: `{"expectedVersion":2,"reason":"Contract check","assigneeId":"technician-1"}`, idempotency: "contract-assign-0001", status: http.StatusOK},
		{name: "stale assignment", method: http.MethodPost, path: detail + "/assign", template: "/api/v1/alarms/{alarmId}/assign", body: `{"expectedVersion":1,"reason":"Contract check","assigneeId":"technician-2"}`, idempotency: "contract-assign-0002", status: http.StatusConflict, code: "ALARM_VERSION_CONFLICT"},
		{name: "invalid list query", method: http.MethodGet, path: "/api/v1/alarms?siteId=" + fixture.siteID + "&limit=0", template: "/api/v1/alarms", status: http.StatusBadRequest, code: "INVALID_ARGUMENT"},
		{name: "unknown alarm", method: http.MethodGet, path: unknown, template: "/api/v1/alarms/{alarmId}", status: http.StatusNotFound, code: "ALARM_NOT_FOUND"},
		{name: "hidden site", method: http.MethodGet, path: "/api/v1/alarms?siteId=01910000-0001-7000-8000-0000000000ff", template: "/api/v1/alarms", status: http.StatusNotFound, code: "SITE_NOT_FOUND"},
	}
	for _, test := range cases {
		recorder := fixture.serve(test.method, test.path, test.body, test.idempotency)
		if recorder.Code != test.status {
			t.Fatalf("%s: status=%d want=%d body=%s", test.name, recorder.Code, test.status, recorder.Body.String())
		}
		contract.assertResponse(t, test.name, test.method, test.template, recorder)
		if test.name == "list" && !strings.Contains(recorder.Body.String(), alarm.AlarmID) {
			t.Fatalf("list does not carry the published Alarm: %s", recorder.Body.String())
		}
		if test.code != "" {
			assertAlarmErrorCode(t, test.name, recorder, test.code)
		}
	}

	fixture.deny.Store(true)
	recorder := fixture.serve(http.MethodGet, detail, "", "")
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("denied: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	contract.assertResponse(t, "denied", http.MethodGet, "/api/v1/alarms/{alarmId}", recorder)
	assertAlarmErrorCode(t, "denied", recorder, "FORBIDDEN")
}

// An owner whose answer drifts from the contract is not passed through: the Gateway's validation
// hook turns it into an Internal error, itself valid for the contract.
func TestAlarmAnswerOutsideTheContractIsWithheld(t *testing.T) {
	contract := loadAlarmContract(t)
	var elsewhere map[string]any
	drifts := map[string]func(map[string]any){
		// A whole, valid Alarm, but of another Site than the one decided and signed for.
		"another Site's Alarm": func(alarm map[string]any) {
			for key := range alarm {
				delete(alarm, key)
			}
			for key, value := range elsewhere {
				alarm[key] = value
			}
		},
		"undeclared field":   func(alarm map[string]any) { alarm["internalNote"] = "owner-only" },
		"invalid projection": func(alarm map[string]any) { alarm["currentSeverity"] = "UNHEARD_OF" },
	}
	for name, drift := range drifts {
		fixture := newAlarmContractFixture(t, func(owner AlarmOperations) AlarmOperations {
			return driftedAlarmOperations{AlarmOperations: owner, mutate: drift}
		})
		alarm := fixture.publish(t)
		other := fixture.publishAt(t, "01910000-0001-7000-8000-0000000000ff")
		raw, _ := json.Marshal(other)
		elsewhere = map[string]any{}
		_ = json.Unmarshal(raw, &elsewhere)
		recorder := fixture.serve(http.MethodGet, "/api/v1/alarms/"+alarm.AlarmID, "", "")
		for _, leaked := range []string{other.AlarmID, "owner-only", "UNHEARD_OF"} {
			if recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), leaked) {
				t.Fatalf("%s: drifted owner answer passed through: status=%d body=%s", name, recorder.Code, recorder.Body.String())
			}
		}
		contract.assertResponse(t, name, http.MethodGet, "/api/v1/alarms/{alarmId}", recorder)
		assertAlarmErrorCode(t, name, recorder, "INTERNAL_ERROR")
	}
}

type driftedAlarmOperations struct {
	AlarmOperations
	mutate func(map[string]any)
}

func (operations driftedAlarmOperations) Execute(ctx context.Context, request *http.Request, route publicAlarmRoute, serviceContext string) ([]byte, int, *ownerFailure) {
	body, status, failure := operations.AlarmOperations.Execute(ctx, request, route, serviceContext)
	if failure != nil || status != http.StatusOK {
		return body, status, failure
	}
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return body, status, failure
	}
	operations.mutate(value)
	drifted, _ := json.Marshal(value)
	return drifted, status, nil
}

// signedAlarmOwner stands in for the Alarm Service: like it, it serves only what the Gateway's
// signed context grants, and it serves from the tenant that context names.
type signedAlarmOwner struct {
	t      *testing.T
	signer *ecdsa.PublicKey
	store  AlarmOperations
}

func (owner signedAlarmOwner) ResolveScope(ctx context.Context, tenantID, alarmID, serviceContext string) (alarmScope, *ownerFailure) {
	return owner.store.ResolveScope(ctx, tenantID, alarmID, serviceContext)
}

func (owner signedAlarmOwner) Execute(ctx context.Context, request *http.Request, route publicAlarmRoute, serviceContext string) ([]byte, int, *ownerFailure) {
	claims, err := identitycontext.VerifyDelegation(owner.signer, serviceContext)
	if err != nil || claims.Audience != "alarm-service" || len(claims.Actions) != 1 || claims.Actions[0] != string(route.action) ||
		!commandTestContains(claims.Scopes, "site:"+route.siteID) || (route.alarmID != "" && !commandTestContains(claims.Scopes, "alarm:"+route.alarmID)) {
		owner.t.Fatalf("Alarm owner called without a context for %s %s: %+v %v", route.action, route.template, claims, err)
	}
	session := bffSession{Session: sessionstore.Session{TenantID: claims.TenantID, Principal: identitycontext.UserPrincipal{Subject: claims.Subject}}}
	request = request.WithContext(context.WithValue(request.Context(), routeSessionContextKey, session))
	return owner.store.Execute(ctx, request, route, serviceContext)
}

func alarmRouteMethod(route publicAlarmRoute) string {
	if route.action == alarmauth.ActionRead {
		return http.MethodGet
	}
	return http.MethodPost
}

func assertAlarmErrorCode(t *testing.T, name string, recorder *httptest.ResponseRecorder, code string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != code {
		t.Fatalf("%s: code=%q want=%q body=%s", name, envelope.Error.Code, code, recorder.Body.String())
	}
}

type alarmContractFixture struct {
	handler   http.Handler
	store     alarmservice.Store
	tenantID  string
	siteID    string
	sessionID string
	deny      atomic.Bool
}

func newAlarmContractFixture(t *testing.T, operations func(AlarmOperations) AlarmOperations) *alarmContractFixture {
	t.Helper()
	now := time.Date(2026, 8, 24, 2, 0, 0, 0, time.UTC)
	store, err := alarmservice.NewMemoryStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &alarmContractFixture{store: store, tenantID: "01910000-0000-7000-8000-000000000001", siteID: gatewayAlarmSiteID}
	signer := commandTestSigner(t)
	var owner AlarmOperations = signedAlarmOwner{t: t, signer: &signer.PublicKey, store: newDirectAlarmAdapter(store)}
	if operations != nil {
		owner = operations(owner)
	}
	alarmConfig := &AlarmConfig{Operations: owner, BackendAudience: "alarm-service", Timeout: time.Second}
	// The repository's route ownership puts the Alarm routes on the path production takes.
	ownership, err := ownershipregistry.Parse(readRepositoryFile(t, "contracts", "ownership", "route-ownership.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := sessionstore.NewMemoryStore()
	configured := NewHandler(Config{
		Now:          func() time.Time { return now },
		RouteManager: ownershipregistry.NewManager(ownership, ownershipregistry.NewMemoryAuditSink(), func() time.Time { return now }),
		Identity: &IdentityConfig{
			OIDCIssuer: "https://issuer.example.test", OIDCClientID: "client", OIDCRedirectURI: "https://web.example.test/api/v1/auth/callback",
			PublicOrigin: "https://web.example.test", IAMURL: "https://iam.example.test", IAMAudience: "iam-service",
			ExecutingWorkloadSPIFFE: "spiffe://hvac.local/platform-gateway", PolicyRevision: "identity-policy-1", DelegationSigner: signer,
			TokenEncryptionKey: make([]byte, 32), SessionStore: sessions, SessionTTL: time.Hour, DelegationTTL: 30 * time.Second,
			IAMHTTPClient: fixture.iamClient(t, signer, now),
		},
		Registry: &RegistryConfig{CoreBaseURL: "https://registry.example.test", CoreHTTPClient: fixture.registryClient(now), CoreTimeout: time.Second},
		Alarm:    alarmConfig,
	}).(*handler)
	fixture.handler = configured
	ciphertext, err := configured.identity.encryptBytes([]byte("alarm-contract-csrf"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.CreateSession(context.Background(), sessionstore.Session{
		Client: sessionstore.ClientBrowser, ID: "alarm-contract-session",
		Principal: identitycontext.UserPrincipal{Subject: "alarm-operator", Issuer: "https://issuer.example.test", Roles: []string{"operator"}},
		TenantID:  fixture.tenantID, CSRFTokenCiphertext: ciphertext, ExpiresAt: now.Add(time.Hour),
	}, sessionstore.MutationContext{
		Action: "SESSION_CREATED", Result: "SUCCEEDED", PolicyRevision: "identity-policy-1", CorrelationID: "alarm-contract",
		TraceID: strings.Repeat("a", 32), Traceparent: "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01",
		ExecutingService: "platform-gateway", ExecutingSPIFFEID: "spiffe://hvac.local/platform-gateway", OccurredAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.sessionID = created.ID
	return fixture
}

func (fixture *alarmContractFixture) publish(t *testing.T) alarmmodel.Alarm {
	t.Helper()
	return fixture.publishAt(t, fixture.siteID)
}

func (fixture *alarmContractFixture) publishAt(t *testing.T, siteID string) alarmmodel.Alarm {
	t.Helper()
	alarm, err := fixture.store.Publish(context.Background(), fixture.tenantID, siteID, alarmservice.Publication{
		AlarmType: "SUPPLY_TEMPERATURE_DRIFT", SourceType: alarmmodel.SourceSiteRule,
		SourceReference: "rule:central-plant-temperature-drift:v3", RuleRevision: "alarm-policy-10",
		Title: "Supply temperature drift", Summary: "Supply temperature is outside the governed operating band.",
		Severity: alarmmodel.SeverityMajor, OccurredAt: "2026-08-24T01:00:00Z",
		Evidence:  []alarmmodel.EvidenceReference{{Kind: "telemetry-snapshot", Reference: "snapshot:contract", CapturedAt: "2026-08-24T01:00:00Z"}},
		ActorType: "WORKLOAD", ActorID: "alarm-evaluator", CorrelationID: "contract-publication",
	})
	if err != nil {
		t.Fatal(err)
	}
	return alarm
}

func (fixture *alarmContractFixture) serve(method, path, body, idempotencyKey string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "https://web.example.test"+path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fixture.sessionID})
	if method != http.MethodGet {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://web.example.test")
		request.Header.Set("X-CSRF-Token", "alarm-contract-csrf")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	return recorder
}

func (fixture *alarmContractFixture) iamClient(t *testing.T, signer *ecdsa.PrivateKey, now time.Time) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		parent, err := identitycontext.VerifyDelegation(&signer.PublicKey, request.Header.Get("X-Delegation-Grant"))
		if err != nil {
			t.Fatalf("invalid Gateway delegation: %v", err)
		}
		switch request.URL.Path {
		case "/internal/v1/registry/decision":
			var input registryauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil {
				t.Fatal("invalid Registry decision request")
			}
			decision := registryauth.Decision{
				Allowed: true, PrincipalID: "01910000-5000-7000-8000-000000000001", SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: fixture.tenantID, AllowedSiteIDs: []string{fixture.siteID}, Actions: []registryauth.Action{input.Action},
				PolicyRevision: "registry-read:1/iam:1", ReasonCode: registryauth.ReasonAllowSiteRole, DecidedAt: now.Format(time.RFC3339Nano),
			}
			claims := registryauth.GrantClaims{
				Issuer: "spiffe://hvac.local/iam-service", Presenter: "spiffe://hvac.local/platform-gateway", Audience: "platform-core-service",
				PrincipalID: decision.PrincipalID, SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject, TenantID: fixture.tenantID,
				AllowedSiteIDs: []string{fixture.siteID}, Actions: []registryauth.Action{input.Action}, PolicyRevision: decision.PolicyRevision,
				DecisionReason: registryauth.ReasonAllowSiteRole, SessionID: parent.SessionID, ParentTokenID: parent.TokenID,
				IssuedAt: now.Unix(), ExpiresAt: now.Add(30 * time.Second).Unix(), TokenID: randomURLToken(16),
			}
			return telemetryJSONResponse(http.StatusOK, registryauth.DecisionResponse{Decision: decision, DelegationGrant: commandUnsignedRegistryGrant(claims)}), nil
		case alarmDecisionPath:
			var input alarmauth.DecisionRequest
			if json.NewDecoder(request.Body).Decode(&input) != nil || input.TenantID != fixture.tenantID {
				t.Fatal("invalid Alarm decision request")
			}
			allowed := !fixture.deny.Load() && input.SiteID == fixture.siteID
			reason := alarmauth.ReasonDenyScope
			if allowed {
				reason = alarmauth.ReasonAllowExactScope
			}
			return telemetryJSONResponse(http.StatusOK, alarmauth.DecisionResponse{Decision: alarmauth.Decision{
				Allowed: allowed, PrincipalID: "01910000-5000-7000-8000-000000000001", SubjectIssuer: parent.SubjectIssuer, Subject: parent.Subject,
				TenantID: input.TenantID, SiteID: input.SiteID, AlarmID: input.AlarmID, Action: input.Action,
				PolicyRevision: "alarm-policy-3", ReasonCode: reason, DecidedAt: now.Format(time.RFC3339Nano),
			}}), nil
		default:
			t.Fatalf("unexpected IAM path %s", request.URL.Path)
			return nil, nil
		}
	})}
}

func (fixture *alarmContractFixture) registryClient(now time.Time) *http.Client {
	stamp := now.Format("2006-01-02T15:04:05.000Z")
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/internal/v1/registry/sites/"+fixture.siteID {
			return telemetryJSONResponse(http.StatusOK, platformapi.Site{
				ID: fixture.siteID, TenantID: fixture.tenantID, Code: "PLANT-01", DisplayName: "Central Plant", Timezone: "Asia/Shanghai",
				Status: "ACTIVE", Revision: 1, CreatedAt: stamp, UpdatedAt: stamp,
			}), nil
		}
		return telemetryJSONResponse(http.StatusNotFound, map[string]any{"code": "RESOURCE_NOT_FOUND"}), nil
	})}
}

// alarmContract is the public Alarm contract with just enough JSON Schema (the keywords it uses) to
// check an answer against it.
type alarmContract struct {
	document map[string]any
}

func readRepositoryFile(t *testing.T, parts ...string) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(append([]string{filepath.Dir(file), "..", "..", "..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type alarmContractOperation struct {
	method string
	path   string
}

func loadAlarmContract(t *testing.T) alarmContract {
	t.Helper()
	raw := readRepositoryFile(t, "contracts", "http", "s4-alarm-public.openapi.json")
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	return alarmContract{document: document}
}

func (contract alarmContract) operations() []alarmContractOperation {
	var operations []alarmContractOperation
	for path, item := range contract.document["paths"].(map[string]any) {
		for method := range item.(map[string]any) {
			switch method {
			case "get", "post", "put", "patch", "delete":
				operations = append(operations, alarmContractOperation{method: strings.ToUpper(method), path: path})
			}
		}
	}
	sort.Slice(operations, func(i, j int) bool {
		return operations[i].path+operations[i].method < operations[j].path+operations[j].method
	})
	return operations
}

func (contract alarmContract) assertResponse(t *testing.T, name, method, template string, recorder *httptest.ResponseRecorder) {
	t.Helper()
	operation, ok := contract.document["paths"].(map[string]any)[template].(map[string]any)[strings.ToLower(method)].(map[string]any)
	if !ok {
		t.Fatalf("%s: %s %s is not in the contract", name, method, template)
	}
	responses := operation["responses"].(map[string]any)
	response, ok := responses[strconv.Itoa(recorder.Code)]
	if !ok {
		response = responses["default"]
	}
	resolved := contract.resolve(response.(map[string]any))
	schema := resolved["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	var value any
	decoder := json.NewDecoder(bytes.NewReader(recorder.Body.Bytes()))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("%s: answer is not JSON: %v", name, err)
	}
	if err := contract.validate(schema, value, "$"); err != nil {
		t.Fatalf("%s: %d answer breaks the contract: %v\n%s", name, recorder.Code, err, recorder.Body.String())
	}
}

func (contract alarmContract) resolve(node map[string]any) map[string]any {
	for {
		reference, ok := node["$ref"].(string)
		if !ok {
			return node
		}
		current := any(contract.document)
		for _, part := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
			current = current.(map[string]any)[part]
		}
		node = current.(map[string]any)
	}
}

func (contract alarmContract) validate(schema map[string]any, value any, at string) error {
	schema = contract.resolve(schema)
	for _, part := range listOf(schema["allOf"]) {
		if err := contract.validate(part.(map[string]any), value, at); err != nil {
			return err
		}
	}
	if options := listOf(schema["anyOf"]); len(options) > 0 {
		if contract.matching(options, value, at) == 0 {
			return fmt.Errorf("%s matches no anyOf branch", at)
		}
	}
	if options := listOf(schema["oneOf"]); len(options) > 0 {
		if contract.matching(options, value, at) != 1 {
			return fmt.Errorf("%s does not match exactly one oneOf branch", at)
		}
	}
	if types, ok := schema["type"]; ok && !matchesType(types, value) {
		return fmt.Errorf("%s is %T, want %v", at, value, types)
	}
	if constant, ok := schema["const"]; ok && fmt.Sprint(constant) != fmt.Sprint(value) {
		return fmt.Errorf("%s is %v, want %v", at, value, constant)
	}
	if enum := listOf(schema["enum"]); len(enum) > 0 {
		found := false
		for _, option := range enum {
			found = found || fmt.Sprint(option) == fmt.Sprint(value)
		}
		if !found {
			return fmt.Errorf("%s is %v, outside %v", at, value, enum)
		}
	}
	switch typed := value.(type) {
	case string:
		length := len([]rune(typed))
		if minimum, ok := number(schema["minLength"]); ok && float64(length) < minimum {
			return fmt.Errorf("%s is shorter than %v", at, minimum)
		}
		if maximum, ok := number(schema["maxLength"]); ok && float64(length) > maximum {
			return fmt.Errorf("%s is longer than %v", at, maximum)
		}
		if pattern, ok := schema["pattern"].(string); ok && !regexp.MustCompile(pattern).MatchString(typed) {
			return fmt.Errorf("%s=%q does not match %s", at, typed, pattern)
		}
		if schema["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, typed); err != nil {
				return fmt.Errorf("%s=%q is not a date-time", at, typed)
			}
		}
	case json.Number:
		parsed, _ := typed.Float64()
		if minimum, ok := number(schema["minimum"]); ok && parsed < minimum {
			return fmt.Errorf("%s=%v is below %v", at, parsed, minimum)
		}
		if maximum, ok := number(schema["maximum"]); ok && parsed > maximum {
			return fmt.Errorf("%s=%v is above %v", at, parsed, maximum)
		}
	case []any:
		if minimum, ok := number(schema["minItems"]); ok && float64(len(typed)) < minimum {
			return fmt.Errorf("%s has fewer than %v items", at, minimum)
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for index, item := range typed {
				if err := contract.validate(items, item, fmt.Sprintf("%s[%d]", at, index)); err != nil {
					return err
				}
			}
		}
	case map[string]any:
		for _, key := range listOf(schema["required"]) {
			if _, ok := typed[key.(string)]; !ok {
				return fmt.Errorf("%s lacks required %s", at, key)
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for key, field := range typed {
			if property, ok := properties[key].(map[string]any); ok {
				if err := contract.validate(property, field, at+"."+key); err != nil {
					return err
				}
				continue
			}
			switch extra := schema["additionalProperties"].(type) {
			case bool:
				if !extra && !declaredInAllOf(contract, schema, key) {
					return fmt.Errorf("%s has undeclared property %s", at, key)
				}
			case map[string]any:
				if err := contract.validate(extra, field, at+"."+key); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (contract alarmContract) matching(options []any, value any, at string) int {
	count := 0
	for _, option := range options {
		if contract.validate(option.(map[string]any), value, at) == nil {
			count++
		}
	}
	return count
}

func declaredInAllOf(contract alarmContract, schema map[string]any, key string) bool {
	for _, part := range listOf(schema["allOf"]) {
		resolved := contract.resolve(part.(map[string]any))
		if properties, ok := resolved["properties"].(map[string]any); ok {
			if _, found := properties[key]; found {
				return true
			}
		}
	}
	return false
}

func matchesType(types any, value any) bool {
	for _, kind := range append(listOf(types), types) {
		name, ok := kind.(string)
		if !ok {
			continue
		}
		switch name {
		case "object":
			if _, ok := value.(map[string]any); ok {
				return true
			}
		case "array":
			if _, ok := value.([]any); ok {
				return true
			}
		case "string":
			if _, ok := value.(string); ok {
				return true
			}
		case "boolean":
			if _, ok := value.(bool); ok {
				return true
			}
		case "null":
			if value == nil {
				return true
			}
		case "number":
			if _, ok := value.(json.Number); ok {
				return true
			}
		case "integer":
			if parsed, ok := value.(json.Number); ok {
				float, err := parsed.Float64()
				if err == nil && float == math.Trunc(float) {
					return true
				}
			}
		}
	}
	return false
}

func listOf(value any) []any {
	list, _ := value.([]any)
	return list
}

func number(value any) (float64, bool) {
	parsed, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	float, err := parsed.Float64()
	return float, err == nil
}
