package commandservice

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandauth"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

// MemoryAuthorityStore adapts the in-memory Service state machine to both AuthorityStore
// and RuntimeStore with single-use grant tracking for unit and gateway BFF tests.
type MemoryAuthorityStore struct {
	svc        *Service
	mu         sync.Mutex
	now        func() time.Time
	order      []string
	usedGrants map[string]struct{}
	ListErr    error
}

func NewMemoryAuthorityStore(now func() time.Time) *MemoryAuthorityStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryAuthorityStore{
		svc:        New(now),
		now:        now,
		usedGrants: map[string]struct{}{},
	}
}

func (m *MemoryAuthorityStore) Close() {
	if m != nil && m.svc != nil {
		m.svc.Close()
	}
}

func (m *MemoryAuthorityStore) Submit(_ context.Context, req commandmodel.SubmitRequest) (SubmitResult, error) {
	res, err := m.svc.Submit(req)
	if err != nil {
		return SubmitResult{}, err
	}
	if !res.Replayed {
		m.mu.Lock()
		m.order = append(m.order, res.Intent.ID)
		m.mu.Unlock()
	}
	return res, nil
}

func (m *MemoryAuthorityStore) Get(_ context.Context, tenantID, commandID string) (commandmodel.CommandIntent, error) {
	intent, err := m.svc.Get(commandID)
	if err != nil {
		return commandmodel.CommandIntent{}, err
	}
	if tenantID != "" && intent.TenantID != tenantID {
		return commandmodel.CommandIntent{}, ErrCommandNotFound
	}
	return intent, nil
}

func (m *MemoryAuthorityStore) List(_ context.Context, tenantID, siteID string, limit int) ([]commandmodel.CommandIntent, error) {
	m.mu.Lock()
	listErr := m.ListErr
	ids := append([]string(nil), m.order...)
	m.mu.Unlock()
	if listErr != nil {
		return nil, listErr
	}
	if limit <= 0 {
		limit = 50
	}
	out := make([]commandmodel.CommandIntent, 0, limit)
	for i := len(ids) - 1; i >= 0 && len(out) < limit; i-- {
		intent, err := m.svc.Get(ids[i])
		if err != nil || intent.TenantID != tenantID || intent.SiteID != siteID {
			continue
		}
		out = append(out, intent)
	}
	return out, nil
}

func (m *MemoryAuthorityStore) SetControlFailureNotifier(notifier ControlFailureNotifier) {
	m.svc.SetControlFailureNotifier(notifier)
}

func (m *MemoryAuthorityStore) SetCommandUpdatedAtForTest(commandID string, updatedAt time.Time) {
	m.svc.mu.Lock()
	defer m.svc.mu.Unlock()
	if intent, ok := m.svc.intents[commandID]; ok {
		ts := updatedAt.UTC()
		intent.UpdatedAt = ts
		if intent.CreatedAt.After(ts) {
			intent.CreatedAt = ts
		}
	}
}

func (m *MemoryAuthorityStore) Approve(_ context.Context, req commandmodel.ApproveRequest) (commandmodel.CommandIntent, error) {
	intent, err := m.svc.Get(req.CommandID)
	if err != nil {
		return commandmodel.CommandIntent{}, err
	}
	if req.TenantID != "" && intent.TenantID != req.TenantID {
		return commandmodel.CommandIntent{}, ErrCommandNotFound
	}
	return m.svc.Approve(req)
}

func (m *MemoryAuthorityStore) Reconcile(_ context.Context, req commandmodel.ReconcileRequest) (commandmodel.CommandIntent, error) {
	m.svc.mu.Lock()
	defer m.svc.mu.Unlock()
	intent, ok := m.svc.intents[req.CommandID]
	if !ok || (req.TenantID != "" && intent.TenantID != req.TenantID) {
		return commandmodel.CommandIntent{}, ErrCommandNotFound
	}
	now := m.now().UTC()
	decision, err := DecideOperatorReconcile(*intent, req, now)
	if err != nil {
		return commandmodel.CommandIntent{}, err
	}
	transition(intent, decision.IntentFinal, decision.Reason, req.Authorization.PrincipalID, now, "reconcile", req.Authorization.GrantID)
	return cloneIntent(*intent), nil
}

func (m *MemoryAuthorityStore) Cancel(_ context.Context, req commandmodel.CancelRequest) (commandmodel.CommandIntent, error) {
	intent, err := m.svc.Get(req.CommandID)
	if err != nil {
		return commandmodel.CommandIntent{}, err
	}
	if req.TenantID != "" && intent.TenantID != req.TenantID {
		return commandmodel.CommandIntent{}, ErrCommandNotFound
	}
	return m.svc.Cancel(req)
}

func (m *MemoryAuthorityStore) CancelPending(_ context.Context, req commandmodel.CancelPendingRequest) ([]string, error) {
	return m.svc.CancelPending(req)
}

func (m *MemoryAuthorityStore) AcquireDevicePriorityLock(_ context.Context, lock commandmodel.DevicePriorityLock) (commandmodel.DevicePriorityLock, error) {
	return m.svc.AcquireDevicePriorityLock(lock)
}

func (m *MemoryAuthorityStore) ReleaseDevicePriorityLock(_ context.Context, tenantID, deviceID string) error {
	return m.svc.ReleaseDevicePriorityLock(tenantID, deviceID)
}

func (m *MemoryAuthorityStore) GetDevicePriorityLock(_ context.Context, tenantID, deviceID string) (*commandmodel.DevicePriorityLock, error) {
	return m.svc.GetDevicePriorityLock(tenantID, deviceID)
}

func (m *MemoryAuthorityStore) ClaimDispatch(_ context.Context, tenantID, leaseOwner string, leaseFor time.Duration) (commandmodel.DispatchEnvelope, error) {
	_, _ = m.svc.RejectPriorityLockedQueued(tenantID)

	m.mu.Lock()
	ids := append([]string(nil), m.order...)
	m.mu.Unlock()

	if leaseFor <= 0 {
		leaseFor = 30 * time.Second
	}
	type queuedCandidate struct {
		id    string
		order int
		rank  int
	}
	var candidates []queuedCandidate
	for idx, id := range ids {
		intent, err := m.svc.Get(id)
		if err != nil || intent.TenantID != tenantID || intent.Status != commandmodel.IntentQueued {
			continue
		}
		candidates = append(candidates, queuedCandidate{
			id:    id,
			order: idx,
			rank:  commandmodel.PriorityRank(intent.PriorityTier),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank > candidates[j].rank
		}
		return candidates[i].order < candidates[j].order
	})

	now := m.now().UTC()
	leaseUntil := now.Add(leaseFor)
	for _, candidate := range candidates {
		if !m.svc.canClaimQueuedIntent(candidate.id, now) {
			continue
		}
		env, prepErr := m.svc.PrepareDispatch(candidate.id, leaseOwner, leaseUntil)
		if prepErr != nil {
			continue
		}
		return env, nil
	}
	return commandmodel.DispatchEnvelope{}, ErrNoDispatchAvailable
}

func (m *MemoryAuthorityStore) ResolveDispatch(_ context.Context, env commandmodel.DispatchEnvelope, res commandmodel.ConnectorResult) error {
	return m.svc.ResolveDispatch(env, res)
}

func (m *MemoryAuthorityStore) ClaimVerification(_ context.Context, tenantID, leaseOwner string, leaseFor time.Duration) (commandmodel.VerificationEnvelope, error) {
	m.mu.Lock()
	ids := append([]string(nil), m.order...)
	m.mu.Unlock()
	if leaseFor <= 0 {
		leaseFor = 15 * time.Second
	}
	leaseUntil := m.now().UTC().Add(leaseFor)
	for _, id := range ids {
		intent, err := m.svc.Get(id)
		if err != nil || intent.TenantID != tenantID || intent.Status != commandmodel.IntentDispatching {
			continue
		}
		env, prepErr := m.svc.PrepareVerification(id, leaseOwner, leaseUntil)
		if prepErr != nil {
			continue
		}
		return env, nil
	}
	return commandmodel.VerificationEnvelope{}, ErrVerificationNotAvailable
}

func (m *MemoryAuthorityStore) ResolveVerification(_ context.Context, env commandmodel.VerificationEnvelope, res commandmodel.VerificationResult) error {
	return m.svc.ResolveVerification(env, res)
}

func (m *MemoryAuthorityStore) PrepareConnectorEvidence(context.Context, commandmodel.PreparedConnectorEvidence) error {
	return nil
}

func (m *MemoryAuthorityStore) CompleteConnectorEvidence(context.Context, commandmodel.CompletedConnectorEvidence) error {
	return nil
}

func (m *MemoryAuthorityStore) SetAwaitingHighRiskApprovalForTest(commandID string) {
	m.svc.mu.Lock()
	defer m.svc.mu.Unlock()
	if intent, ok := m.svc.intents[commandID]; ok {
		intent.Risk = commandmodel.RiskHigh
		intent.RiskSnapshot.Level = commandmodel.RiskHigh
		intent.ApprovalPolicy = commandmodel.ApprovalSingleApprover
		intent.Status = commandmodel.IntentAwaitingApproval
	}
}

func (m *MemoryAuthorityStore) ConsumeCommandGrant(claims commandauth.GrantClaims, policyRev string, revocationRev uint64) (commandauth.UseStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, used := m.usedGrants[claims.TokenID]
	if !used {
		m.usedGrants[claims.TokenID] = struct{}{}
	}
	return commandauth.UseStatus{
		CurrentPolicyRevision:     policyRev,
		CurrentRevocationRevision: revocationRev,
		Replayed:                  used,
	}, nil
}

type FakePlantTwin struct {
	mu                sync.Mutex
	Assets            map[string]AssetDTO
	Models            map[string]SiteAssetModelDTO
	ControlStates     map[string]SiteControlStateView
	Schedules         map[string]OperatingScheduleDTO
	Exceptions        map[string][]OperatingExceptionDTO
	BoundDevices      map[string][]string
	DispatchErr       error
	SiteControlRevSeq int64
	AssetLockRevSeq   int64
}

func NewFakePlantTwin() *FakePlantTwin {
	return &FakePlantTwin{
		Assets:        map[string]AssetDTO{},
		Models:        map[string]SiteAssetModelDTO{},
		ControlStates: map[string]SiteControlStateView{},
		Schedules:     map[string]OperatingScheduleDTO{},
		Exceptions:    map[string][]OperatingExceptionDTO{},
		BoundDevices:  map[string][]string{},
	}
}

func (f *FakePlantTwin) GetSiteOperatingSchedule(_ context.Context, _ OperatorIdentity, siteID string) (OperatingScheduleDTO, []OperatingExceptionDTO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sched, ok := f.Schedules[siteID]
	if !ok {
		return OperatingScheduleDTO{}, nil, notFoundControlError("The requested Site operating schedule was not found.")
	}
	return sched, append([]OperatingExceptionDTO(nil), f.Exceptions[siteID]...), nil
}

func (f *FakePlantTwin) GetAsset(_ context.Context, operator OperatorIdentity, assetID string) (AssetDTO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	asset, ok := f.Assets[assetID]
	if !ok || asset.TenantID != operator.TenantID {
		return AssetDTO{}, notFoundControlError("The requested Asset was not found.")
	}
	return asset, nil
}

func (f *FakePlantTwin) GetSiteAssetModel(_ context.Context, operator OperatorIdentity, siteID string) (SiteAssetModelDTO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	model, ok := f.Models[siteID]
	if !ok || model.TenantID != operator.TenantID {
		return SiteAssetModelDTO{}, notFoundControlError("The requested Site was not found.")
	}
	return model, nil
}

func (f *FakePlantTwin) GetSiteControlState(_ context.Context, operator OperatorIdentity, siteID string) (SiteControlStateView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if state, ok := f.ControlStates[siteID]; ok {
		if state.TenantID != "" && state.TenantID != operator.TenantID {
			return SiteControlStateView{}, notFoundControlError("The requested Site was not found.")
		}
		return cloneSiteControlStateView(state), nil
	}
	model, known := f.Models[siteID]
	if !known || model.TenantID != operator.TenantID {
		return SiteControlStateView{}, notFoundControlError("The requested Site was not found.")
	}
	return SiteControlStateView{
		TenantID:    operator.TenantID,
		SiteID:      siteID,
		ControlMode: SiteControlModeRemoteAuto,
		AssetLocks:  []AssetControlLockView{},
	}, nil
}

func (f *FakePlantTwin) SaveSiteControlMode(_ context.Context, operator OperatorIdentity, siteID, controlMode, reason string, meta ControlMutationMeta) (SiteControlStateView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := f.ControlStates[siteID]
	if meta.ExpectedRevision != state.Revision {
		return SiteControlStateView{}, &ControlError{
			HTTPStatus: http.StatusConflict, Code: "REGISTRY_REVISION_CONFLICT",
			Title: "Control state conflict", Detail: "The expected control state revision is stale.",
		}
	}
	f.SiteControlRevSeq++
	id := fmt.Sprintf("018f3e00-9900-7000-8000-%012d", f.SiteControlRevSeq)
	updatedBy := operator.Subject
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	state.ID = &id
	state.TenantID = operator.TenantID
	state.SiteID = siteID
	state.ControlMode = controlMode
	state.Reason = reason
	state.UpdatedBy = &updatedBy
	state.UpdatedAt = &updatedAt
	state.Revision = state.Revision + 1
	if state.AssetLocks == nil {
		state.AssetLocks = []AssetControlLockView{}
	}
	f.ControlStates[siteID] = state
	return cloneSiteControlStateView(state), nil
}

func (f *FakePlantTwin) SaveAssetControlLock(_ context.Context, operator OperatorIdentity, assetID, siteID, lockMode, reason string, meta ControlMutationMeta) (AssetControlLockView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := f.ControlStates[siteID]
	if state.TenantID == "" {
		state.TenantID = operator.TenantID
		state.SiteID = siteID
		state.ControlMode = SiteControlModeRemoteAuto
	}
	var currentRev int64
	idx := -1
	for i := range state.AssetLocks {
		if state.AssetLocks[i].AssetID == assetID {
			currentRev = state.AssetLocks[i].Revision
			idx = i
			break
		}
	}
	if meta.ExpectedRevision != currentRev {
		return AssetControlLockView{}, &ControlError{
			HTTPStatus: http.StatusConflict, Code: "REGISTRY_REVISION_CONFLICT",
			Title: "Control state conflict", Detail: "The expected asset lock revision is stale.",
		}
	}
	f.AssetLockRevSeq++
	updatedBy := operator.Subject
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	saved := AssetControlLockView{
		ID:             fmt.Sprintf("018f3e00-9800-7000-8000-%012d", f.AssetLockRevSeq),
		TenantID:       operator.TenantID,
		SiteID:         siteID,
		AssetID:        assetID,
		LockMode:       lockMode,
		Reason:         reason,
		UpdatedBy:      &updatedBy,
		UpdatedAt:      &updatedAt,
		Revision:       currentRev + 1,
		BoundDeviceIDs: append([]string(nil), f.BoundDevices[assetID]...),
	}
	if idx >= 0 {
		state.AssetLocks[idx] = saved
	} else {
		state.AssetLocks = append(state.AssetLocks, saved)
	}
	f.ControlStates[siteID] = state
	return saved, nil
}

func (f *FakePlantTwin) GetDispatchContext(_ context.Context, tenantID, siteID, deviceID, pointID string) (ControlDispatchContext, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DispatchErr != nil {
		return ControlDispatchContext{}, f.DispatchErr
	}
	state := f.ControlStates[siteID]
	controlMode := state.ControlMode
	if controlMode == "" {
		controlMode = SiteControlModeRemoteAuto
	}
	out := ControlDispatchContext{
		TenantID:                tenantID,
		SiteID:                  siteID,
		DeviceID:                deviceID,
		PointID:                 pointID,
		ControlMode:             controlMode,
		AssetLocks:              append([]AssetControlLockView(nil), state.AssetLocks...),
		ActiveMaintenanceWindow: state.ActiveMaintenanceWindow,
		FeedbackPointKey:        "run_state",
		Interlocks:              []commandmodel.InterlockCheck{},
	}
	if model, ok := f.Models[siteID]; ok {
		for _, rel := range model.Relationships {
			if rel.FromType == "POINT" && rel.FromID == pointID && rel.Role == "CONTROLS" && rel.ToType == "ASSET" {
				out.AssetID = rel.ToID
				break
			}
		}
		for _, pt := range model.TelemetryPoints {
			if pt.ID == pointID && pt.ReportingDeviceID == deviceID {
				if mode, _ := pt.SourceMetadata["actuationMode"].(string); mode != "" {
					out.ActuationMode = mode
				}
				if fk, _ := pt.SourceMetadata["feedbackPointKey"].(string); fk != "" {
					out.FeedbackPointKey = fk
				}
				if checks, err := parsePointInterlockChecks(pt.SourceMetadata, model); err == nil && len(checks) > 0 {
					out.Interlocks = checks
				}
				break
			}
		}
	}
	return out, nil
}

type FakeLiveEntry struct {
	Obs        *commandmodel.LivePointObservation
	EvidenceID string
	Err        error
}

type FakeTelemetryLive struct {
	mu     sync.Mutex
	Points map[string]FakeLiveEntry
	Calls  []string
}

func NewFakeTelemetryLive() *FakeTelemetryLive {
	return &FakeTelemetryLive{Points: map[string]FakeLiveEntry{}}
}

func (f *FakeTelemetryLive) Set(deviceID, pointKey string, obs *commandmodel.LivePointObservation, evidenceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Points[deviceID+"\x00"+pointKey] = FakeLiveEntry{Obs: obs, EvidenceID: evidenceID}
}

func (f *FakeTelemetryLive) SetError(deviceID, pointKey string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Points[deviceID+"\x00"+pointKey] = FakeLiveEntry{Err: err}
}

func (f *FakeTelemetryLive) ReadLivePoint(_ context.Context, _, _, deviceID, pointKey string) (*commandmodel.LivePointObservation, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := deviceID + "\x00" + pointKey
	f.Calls = append(f.Calls, k)
	entry, ok := f.Points[k]
	if !ok {
		return nil, "", nil
	}
	if entry.Err != nil {
		return nil, "", entry.Err
	}
	return entry.Obs, entry.EvidenceID, nil
}

type FakeIAMAuthorizer struct {
	mu                     sync.Mutex
	GrantSigner            crypto.Signer
	Now                    func() time.Time
	PrincipalID            string
	AllowedSites           []string
	DenyScheduleManage     bool
	DenyCommandAuth        bool
	CommandAuthorizeCalls  int
	RegistryAuthorizeCalls int
	seq                    int64
}

func (f *FakeIAMAuthorizer) AuthorizeRegistry(_ context.Context, operator OperatorIdentity, action string) (RegistryDecisionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RegistryAuthorizeCalls++
	if action == ActionScheduleManage && f.DenyScheduleManage {
		return RegistryDecisionResult{Allowed: false, PrincipalID: f.PrincipalID, ReasonCode: "DENY_ACTION"}, nil
	}
	sites := append([]string(nil), f.AllowedSites...)
	sort.Strings(sites)
	return RegistryDecisionResult{
		Allowed:        true,
		PrincipalID:    f.PrincipalID,
		AllowedSiteIDs: sites,
		Grant:          "test-registry-grant",
	}, nil
}

func (f *FakeIAMAuthorizer) AuthorizeCommand(
	_ context.Context,
	operator OperatorIdentity,
	purpose commandmodel.AuthorizationPurpose,
	siteID, deviceID string,
	capability commandmodel.Capability,
	capabilityRevision string,
) (string, commandauth.GrantClaims, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CommandAuthorizeCalls++
	if f.DenyCommandAuth {
		return "", commandauth.GrantClaims{}, &ControlError{
			HTTPStatus: http.StatusForbidden, Code: "COMMAND_AUTHORIZATION_DENIED",
			Title: "Command forbidden", Detail: "Denied by test IAM authorizer.",
		}
	}
	f.seq++
	now := f.Now().UTC()
	grantID := fmt.Sprintf("grant-cmd-%d", f.seq)
	claims := commandauth.GrantClaims{
		Issuer:                      "spiffe://hvac.local/iam-service",
		Presenter:                   "spiffe://hvac.local/command-service",
		Audience:                    "command-service",
		GrantID:                     grantID,
		Purpose:                     purpose,
		PrincipalID:                 f.PrincipalID,
		TenantID:                    operator.TenantID,
		SiteID:                      siteID,
		DeviceID:                    deviceID,
		Capability:                  capability,
		CapabilityRevision:          capabilityRevision,
		MaximumRisk:                 commandmodel.RiskCritical,
		PolicyRevision:              "policy-v1",
		EmergencyRevocationRevision: 1,
		IssuedAt:                    now.Unix(),
		ExpiresAt:                   now.Add(25 * time.Second).Unix(),
		TokenID:                     grantID,
	}
	signed, err := commandauth.SignGrant(f.GrantSigner, claims)
	if err != nil {
		return "", commandauth.GrantClaims{}, err
	}
	return signed, claims, nil
}

type TestControlHarness struct {
	Authority       *ControlAuthority
	Store           *MemoryAuthorityStore
	Twin            *FakePlantTwin
	Live            *FakeTelemetryLive
	IAM             *FakeIAMAuthorizer
	OperatorHandler http.Handler
	RuntimeHandler  http.Handler
	FailureEvents   chan ControlFailureEvent
	GatewaySigner   crypto.Signer
	GrantSigner     crypto.Signer
}

func NewControlAuthorityForTest(now func() time.Time) (*TestControlHarness, error) {
	if now == nil {
		now = time.Now
	}
	gatewayKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	grantKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	store := NewMemoryAuthorityStore(now)
	twin := NewFakePlantTwin()
	live := NewFakeTelemetryLive()
	iamAuth := &FakeIAMAuthorizer{
		GrantSigner: grantKey,
		Now:         now,
		PrincipalID: "018f3e00-5000-7000-8000-000000000001",
	}
	failureEvents := make(chan ControlFailureEvent, 32)
	notify := func(_ context.Context, evt ControlFailureEvent) {
		select {
		case failureEvents <- evt:
		default:
		}
	}
	store.SetControlFailureNotifier(notify)
	authority, err := NewControlAuthority(ControlAuthorityConfig{
		Store:                 store,
		Runtime:               store,
		Twin:                  twin,
		Live:                  live,
		IAM:                   iamAuth,
		CommandGrantPublicKey: &grantKey.PublicKey,
		CommandGrantIssuer:    "spiffe://hvac.local/iam-service",
		CommandGrantPresenter: "spiffe://hvac.local/command-service",
		CommandGrantAudience:  "command-service",
		CommandGrantUseChecker: func(claims commandauth.GrantClaims) (commandauth.UseStatus, error) {
			return store.ConsumeCommandGrant(claims, "policy-v1", 1)
		},
		ControlFailureNotifier: notify,
		Now:                    now,
	})
	if err != nil {
		return nil, err
	}
	opHandler, err := NewOperatorHTTPHandler(OperatorHTTPConfig{
		Authority:                  authority,
		GatewayDelegationPublicKey: &gatewayKey.PublicKey,
		GatewaySPIFFE:              "spiffe://hvac.local/platform-gateway",
		GatewayAudience:            "command-service",
		Now:                        now,
	})
	if err != nil {
		return nil, err
	}
	runtimeHandler, err := NewRuntimeHTTPHandler(RuntimeHTTPConfig{
		Store:                  authority,
		DispatcherSPIFFE:       "spiffe://hvac.local/command-dispatcher",
		VerifierSPIFFE:         "spiffe://hvac.local/command-verifier",
		ControlFailureNotifier: notify,
	})
	if err != nil {
		return nil, err
	}
	return &TestControlHarness{
		Authority:       authority,
		Store:           store,
		Twin:            twin,
		Live:            live,
		IAM:             iamAuth,
		OperatorHandler: opHandler,
		RuntimeHandler:  runtimeHandler,
		FailureEvents:   failureEvents,
		GatewaySigner:   gatewayKey,
		GrantSigner:     grantKey,
	}, nil
}

func cloneSiteControlStateView(in SiteControlStateView) SiteControlStateView {
	out := in
	out.AssetLocks = append([]AssetControlLockView(nil), in.AssetLocks...)
	return out
}

func init() {
	// Compile-time interface assertions.
	var _ PlantTwinPort = (*HTTPAdapters)(nil)
	var _ TelemetryLivePort = (*HTTPAdapters)(nil)
	var _ IAMAuthorizerPort = (*HTTPAdapters)(nil)
	var _ RuntimeStore = (*ControlAuthority)(nil)
	var _ error = (*ControlError)(nil)
	_ = errors.New
}
