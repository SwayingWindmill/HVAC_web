package concentrator

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
)

const (
	OperationReadStatus  = "READ_STATUS"
	OperationWriteParams = "WRITE_PARAMS"
	OperationReadEIDs    = "READ_EIDS"
	OperationWriteEIDs   = "WRITE_EIDS"

	OutcomeApplied      = "APPLIED"
	OutcomeRejected     = "REJECTED"
	OutcomeFailed       = "FAILED"
	OutcomeTimeout      = "TIMEOUT"
	OutcomeStagedShadow = "STAGED_SHADOW"

	EIDSlotCount    = 200
	defaultAuditCap = 20
)

var (
	ErrInvalidRequest       = errors.New("concentrator request is invalid")
	ErrProtectedUID         = errors.New("RS485 unitId is protected")
	ErrReasonRequired       = errors.New("change reason is required")
	ErrParamsInvalid        = errors.New("concentrator parameters are invalid")
	ErrDuplicateEID         = errors.New("duplicate non-zero EID detected")
	ErrEIDSlotsInvalid      = errors.New("concentrator EID slots are invalid")
	ErrTransportUnavailable = errors.New("concentrator MQTT transport is unavailable")
	ErrRPCTimeout           = errors.New("concentrator MQTT RPC timed out")
	ErrEdgeRejected         = errors.New("gateway rejected concentrator request")
	ErrEdgeFailed           = errors.New("gateway failed executing concentrator request")
)

func IsProtectedUID(unitID int) bool {
	return connectivity.IsProtectedConcentratorUID(unitID)
}

type Params = connectivity.ConcentratorShadowParams

type ParamsPatch struct {
	ModbusID        *int `json:"modbusId,omitempty"`
	BaudRateCode    *int `json:"baudRateCode,omitempty"`
	OffLineTime     *int `json:"offLineTime,omitempty"`
	DataUplinkCycle *int `json:"dataUplinkCycle,omitempty"`
	LocalNet        *int `json:"localNet,omitempty"`
	TimeMark        *int `json:"timeMark,omitempty"`
	RfTxPower       *int `json:"rfTxPower,omitempty"`
	RfFrequency     *int `json:"rfFrequency,omitempty"`
	RfBandWidth     *int `json:"rfBandWidth,omitempty"`
	RfSf            *int `json:"rfSf,omitempty"`
	RfLeadCode      *int `json:"rfLeadCode,omitempty"`
	TxTimeout       *int `json:"txTimeout,omitempty"`
	RxTimeout       *int `json:"rxTimeout,omitempty"`
	RssiOut         *int `json:"rssiOut,omitempty"`
}

func (p ParamsPatch) validate() (map[string]int, error) {
	raw := map[string]int{}
	type bound struct {
		name string
		ptr  *int
		min  int
		max  int
	}
	fields := []bound{
		{"modbusId", p.ModbusID, 1, 247},
		{"baudRateCode", p.BaudRateCode, 0, 5},
		{"offLineTime", p.OffLineTime, 1, 65535},
		{"dataUplinkCycle", p.DataUplinkCycle, 1, 65535},
		{"localNet", p.LocalNet, 0, 65535},
		{"timeMark", p.TimeMark, 0, 3},
		{"rfTxPower", p.RfTxPower, 0, 30},
		{"rfFrequency", p.RfFrequency, 100, 1000},
		{"rfBandWidth", p.RfBandWidth, 0, 2},
		{"rfSf", p.RfSf, 5, 12},
		{"rfLeadCode", p.RfLeadCode, 1, 65535},
		{"txTimeout", p.TxTimeout, 1, 65535},
		{"rxTimeout", p.RxTimeout, 1, 65535},
		{"rssiOut", p.RssiOut, 0, 1},
	}
	for _, field := range fields {
		if field.ptr == nil {
			continue
		}
		v := *field.ptr
		if v < field.min || v > field.max {
			return nil, fmt.Errorf("%w: %s=%d out of range [%d,%d]", ErrParamsInvalid, field.name, v, field.min, field.max)
		}
		if field.name == "modbusId" && IsProtectedUID(v) {
			return nil, fmt.Errorf("%w: modbusId %d is protected on RS485 bus", ErrProtectedUID, v)
		}
		raw[field.name] = v
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty parameter patch", ErrParamsInvalid)
	}
	return raw, nil
}

type ChannelReading struct {
	Channel      int      `json:"channel"`
	EID          uint32   `json:"eid"`
	Online       bool     `json:"online"`
	SensorType   int      `json:"sensorType"`
	SensorModel  string   `json:"sensorModel"`
	TemperatureC *float64 `json:"temperatureC"`
	VoltageMv    *int     `json:"voltageMv"`
	RssiDbm      *int     `json:"rssiDbm"`
}

type EIDSlot = connectivity.ConcentratorShadowEIDSlot

type StatusResponse struct {
	GatewayID    string                                `json:"gatewayId"`
	UnitID       int                                   `json:"unitId"`
	RespondedAt  time.Time                             `json:"respondedAt"`
	Params       Params                                `json:"params"`
	Channels     []ChannelReading                      `json:"channels"`
	RecentAudits []connectivity.ConcentratorAuditEntry `json:"recentAudits"`
}

type ParamsUpdateRequest struct {
	Params         ParamsPatch `json:"params"`
	Reason         string      `json:"reason"`
	StageIfOffline bool        `json:"stageIfOffline,omitempty"`
}

type ParamsWriteResult struct {
	GatewayID       string                              `json:"gatewayId"`
	UnitID          int                                 `json:"unitId"`
	RequestID       string                              `json:"requestId"`
	WrittenCount    int                                 `json:"writtenCount"`
	Params          Params                              `json:"params"`
	Audit           connectivity.ConcentratorAuditEntry `json:"audit"`
	Staged          bool                                `json:"staged,omitempty"`
	DesiredRevision int64                               `json:"desiredRevision,omitempty"`
}

type EIDTableResponse struct {
	GatewayID    string                                `json:"gatewayId"`
	UnitID       int                                   `json:"unitId"`
	RespondedAt  time.Time                             `json:"respondedAt"`
	Slots        []EIDSlot                             `json:"slots"`
	RecentAudits []connectivity.ConcentratorAuditEntry `json:"recentAudits"`
}

type EIDUpdateRequest struct {
	Slots          []EIDSlot `json:"slots"`
	Reason         string    `json:"reason"`
	StageIfOffline bool      `json:"stageIfOffline,omitempty"`
}

type EIDWriteResult struct {
	GatewayID       string                              `json:"gatewayId"`
	UnitID          int                                 `json:"unitId"`
	RequestID       string                              `json:"requestId"`
	UpdatedSlots    int                                 `json:"updatedSlots"`
	ChunkCount      int                                 `json:"chunkCount"`
	Slots           []EIDSlot                           `json:"slots"`
	Audit           connectivity.ConcentratorAuditEntry `json:"audit"`
	Staged          bool                                `json:"staged,omitempty"`
	DesiredRevision int64                               `json:"desiredRevision,omitempty"`
}

type RPCRequest struct {
	SchemaVersion string         `json:"schemaVersion"`
	RequestID     string         `json:"requestId"`
	GatewayID     string         `json:"gatewayId"`
	UnitID        int            `json:"unitId"`
	Operation     string         `json:"operation"`
	IssuedAt      int64          `json:"issuedAt"`
	ExpireAt      int64          `json:"expireAt"`
	Params        map[string]int `json:"params,omitempty"`
	Slots         []EIDSlot      `json:"slots,omitempty"`
}

type RPCResponse struct {
	SchemaVersion string           `json:"schemaVersion"`
	RequestID     string           `json:"requestId"`
	GatewayID     string           `json:"gatewayId"`
	UnitID        int              `json:"unitId"`
	Operation     string           `json:"operation"`
	Status        string           `json:"status"`
	ErrorCode     string           `json:"errorCode,omitempty"`
	ErrorMessage  string           `json:"errorMessage,omitempty"`
	RespondedAt   int64            `json:"respondedAt"`
	Params        *Params          `json:"params,omitempty"`
	Channels      []ChannelReading `json:"channels,omitempty"`
	Slots         []EIDSlot        `json:"slots,omitempty"`
	WrittenCount  int              `json:"writtenCount,omitempty"`
	UpdatedSlots  int              `json:"updatedSlots,omitempty"`
	ChunkCount    int              `json:"chunkCount,omitempty"`
}

type Store interface {
	Clock() time.Time
	ResolveGatewayForOperator(ctx context.Context, claims registryauth.GrantClaims, gatewayID string) (tenantID string, err error)
	RecordConcentratorAudit(ctx context.Context, tenantID, gatewayID string, entry connectivity.ConcentratorAuditEntry) (connectivity.ConcentratorAuditEntry, error)
	ListConcentratorAudits(ctx context.Context, tenantID, gatewayID string, unitID, limit int) ([]connectivity.ConcentratorAuditEntry, error)
	LoadGatewayConfigShadowByGatewayID(ctx context.Context, gatewayID string) (connectivity.GatewayConfigShadow, error)
	UpsertConcentratorShadowState(ctx context.Context, tenantID, gatewayID string, unitID int, params *connectivity.ConcentratorShadowParams, eids []connectivity.ConcentratorShadowEIDSlot, actorID, reason string, appliedToEdge bool) (int64, error)
	RecordConcentratorReconcileResult(ctx context.Context, tenantID, gatewayID string, desiredRevision int64, reconciledUnits map[string]connectivity.ConcentratorUnitShadow, renamedUnits map[string]string, failedDrifts []connectivity.DriftDetail, errorMessage *string, gatewayUnreachable bool) error
}

type RPCClient interface {
	Call(ctx context.Context, req RPCRequest) (RPCResponse, error)
}

type unitKey struct {
	gatewayID string
	unitID    int
}

type unitLock struct {
	sem  chan struct{}
	refs int
}

type Service struct {
	store Store
	rpc   RPCClient

	mu             sync.Mutex
	locks          map[unitKey]*unitLock
	reconcileLocks map[string]*unitLock
	lastSeenParams map[unitKey]Params
}

func NewService(store Store, rpc RPCClient) *Service {
	return &Service{
		store:          store,
		rpc:            rpc,
		locks:          map[unitKey]*unitLock{},
		reconcileLocks: map[string]*unitLock{},
		lastSeenParams: map[unitKey]Params{},
	}
}

func (s *Service) lockGatewayReconcile(ctx context.Context, gatewayID string) (func(), error) {
	s.mu.Lock()
	if s.reconcileLocks == nil {
		s.reconcileLocks = map[string]*unitLock{}
	}
	entry, exists := s.reconcileLocks[gatewayID]
	if !exists {
		entry = &unitLock{sem: make(chan struct{}, 1)}
		s.reconcileLocks[gatewayID] = entry
	}
	entry.refs++
	s.mu.Unlock()

	release := func() {
		<-entry.sem
		s.mu.Lock()
		entry.refs--
		if entry.refs == 0 && s.reconcileLocks[gatewayID] == entry {
			delete(s.reconcileLocks, gatewayID)
		}
		s.mu.Unlock()
	}

	select {
	case entry.sem <- struct{}{}:
		return release, nil
	case <-ctx.Done():
		s.mu.Lock()
		entry.refs--
		if entry.refs == 0 && s.reconcileLocks[gatewayID] == entry {
			delete(s.reconcileLocks, gatewayID)
		}
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

func validateUnitID(unitID int) error {
	if unitID < 1 || unitID > 247 {
		return fmt.Errorf("%w: unitId=%d must be in [1,247]", ErrInvalidRequest, unitID)
	}
	if IsProtectedUID(unitID) {
		return fmt.Errorf("%w: unitId %d is protected on RS485 bus", ErrProtectedUID, unitID)
	}
	return nil
}

func validateReason(reason string) (string, error) {
	trimmed := strings.TrimSpace(reason)
	if len(trimmed) == 0 || len(trimmed) > 512 {
		return "", ErrReasonRequired
	}
	return trimmed, nil
}

func validateEIDSlots(slots []EIDSlot) ([]EIDSlot, map[string]any, error) {
	if len(slots) != EIDSlotCount {
		return nil, nil, fmt.Errorf("%w: expected %d slots, got %d", ErrEIDSlotsInvalid, EIDSlotCount, len(slots))
	}
	ordered := make([]EIDSlot, EIDSlotCount)
	seenSlots := make([]bool, EIDSlotCount+1)
	seenEIDs := make(map[uint32]int, EIDSlotCount)
	nonZeroCount := 0
	for _, item := range slots {
		if item.Slot < 1 || item.Slot > EIDSlotCount || seenSlots[item.Slot] {
			return nil, nil, fmt.Errorf("%w: invalid or duplicate slot %d", ErrEIDSlotsInvalid, item.Slot)
		}
		seenSlots[item.Slot] = true
		if item.EID > 0 {
			nonZeroCount++
			if prevSlot, dup := seenEIDs[item.EID]; dup {
				return nil, nil, fmt.Errorf("%w: EID %d appears in slots %d and %d", ErrDuplicateEID, item.EID, prevSlot, item.Slot)
			}
			seenEIDs[item.EID] = item.Slot
		}
		ordered[item.Slot-1] = item
	}
	return ordered, map[string]any{"slotCount": EIDSlotCount, "nonZeroCount": nonZeroCount}, nil
}

func (s *Service) lockUnit(ctx context.Context, gatewayID string, unitID int) (func(), error) {
	key := unitKey{gatewayID: gatewayID, unitID: unitID}
	s.mu.Lock()
	entry, ok := s.locks[key]
	if !ok {
		entry = &unitLock{sem: make(chan struct{}, 1)}
		s.locks[key] = entry
	}
	entry.refs++
	s.mu.Unlock()

	releaseRef := func() {
		s.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(s.locks, key)
		}
		s.mu.Unlock()
	}

	select {
	case entry.sem <- struct{}{}:
		return func() {
			<-entry.sem
			releaseRef()
		}, nil
	case <-ctx.Done():
		releaseRef()
		return nil, fmt.Errorf("%w: %v", ErrRPCTimeout, ctx.Err())
	}
}

func (s *Service) prepare(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, unitID int) (string, func(), error) {
	return s.prepareLocked(ctx, claims, gatewayID, unitID, false)
}

// prepareWrite acquires lockGatewayReconcile before lockUnit (matching ReconcileConcentratorShadow
// lock ordering) so online writes cannot interleave between per-unit hardware writes and shadow persist.
func (s *Service) prepareWrite(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, unitID int) (string, func(), error) {
	return s.prepareLocked(ctx, claims, gatewayID, unitID, true)
}

func (s *Service) prepareLocked(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, unitID int, lockGateway bool) (string, func(), error) {
	if s == nil || s.store == nil || s.rpc == nil {
		return "", nil, ErrTransportUnavailable
	}
	if err := validateUnitID(unitID); err != nil {
		return "", nil, err
	}
	tenantID, err := s.store.ResolveGatewayForOperator(ctx, claims, gatewayID)
	if err != nil {
		return "", nil, err
	}
	releaseGateway := func() {}
	if lockGateway {
		releaseGateway, err = s.lockGatewayReconcile(ctx, gatewayID)
		if err != nil {
			return "", nil, fmt.Errorf("%w: %v", ErrRPCTimeout, err)
		}
	}
	releaseUnit, err := s.lockUnit(ctx, gatewayID, unitID)
	if err != nil {
		releaseGateway()
		return "", nil, err
	}
	return tenantID, func() {
		releaseUnit()
		releaseGateway()
	}, nil
}

func (s *Service) newRequest(ctx context.Context, gatewayID string, unitID int, operation string) (RPCRequest, error) {
	now := s.store.Clock()
	requestID, err := connectivity.NewUUIDv7At(now)
	if err != nil {
		return RPCRequest{}, err
	}
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		deadline = now.Add(defaultRPCTimeout)
	}
	return RPCRequest{
		SchemaVersion: rpcSchemaVersion,
		RequestID:     requestID,
		GatewayID:     gatewayID,
		UnitID:        unitID,
		Operation:     operation,
		IssuedAt:      now.UnixMilli(),
		ExpireAt:      deadline.UTC().UnixMilli(),
	}, nil
}

func (s *Service) Status(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, unitID int) (StatusResponse, error) {
	tenantID, release, err := s.prepare(ctx, claims, gatewayID, unitID)
	if err != nil {
		return StatusResponse{}, err
	}
	defer release()

	req, err := s.newRequest(ctx, gatewayID, unitID, OperationReadStatus)
	if err != nil {
		return StatusResponse{}, err
	}
	resp, err := s.rpc.Call(ctx, req)
	if err != nil {
		return StatusResponse{}, err
	}
	if err := mapRPCOutcomeError(resp); err != nil {
		return StatusResponse{}, err
	}
	if resp.Params == nil {
		return StatusResponse{}, fmt.Errorf("%w: missing params in READ_STATUS reply", ErrEdgeFailed)
	}
	s.rememberParams(gatewayID, unitID, *resp.Params)
	audits, err := s.store.ListConcentratorAudits(ctx, tenantID, gatewayID, unitID, defaultAuditCap)
	if err != nil {
		return StatusResponse{}, err
	}
	channels := resp.Channels
	if channels == nil {
		channels = []ChannelReading{}
	}
	return StatusResponse{
		GatewayID:    gatewayID,
		UnitID:       unitID,
		RespondedAt:  millisToTime(resp.RespondedAt, s.store.Clock()),
		Params:       *resp.Params,
		Channels:     channels,
		RecentAudits: audits,
	}, nil
}

func (s *Service) UpdateParams(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, unitID int, input ParamsUpdateRequest) (ParamsWriteResult, error) {
	reason, err := validateReason(input.Reason)
	if err != nil {
		return ParamsWriteResult{}, err
	}
	patchMap, err := input.Params.validate()
	if err != nil {
		return ParamsWriteResult{}, err
	}
	tenantID, release, err := s.prepareWrite(ctx, claims, gatewayID, unitID)
	if err != nil {
		return ParamsWriteResult{}, err
	}
	defer release()

	req, err := s.newRequest(ctx, gatewayID, unitID, OperationWriteParams)
	if err != nil {
		return ParamsWriteResult{}, err
	}
	req.Params = patchMap

	diffMap := map[string]any{"requested": patchMap}
	resp, callErr := s.rpc.Call(ctx, req)
	if input.StageIfOffline && (errors.Is(callErr, ErrTransportUnavailable) || errors.Is(callErr, ErrRPCTimeout)) {
		stageCtx, stageCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stageCancel()
		stagedParams, hasBaseline := s.buildStagedParams(stageCtx, gatewayID, unitID, input.Params)
		if !hasBaseline {
			return ParamsWriteResult{}, fmt.Errorf("%w: cannot stage partial parameter patch without known unit baseline", ErrParamsInvalid)
		}
		desiredRev, shadowErr := s.store.UpsertConcentratorShadowState(stageCtx, tenantID, gatewayID, unitID, &stagedParams, nil, claims.PrincipalID, reason, false)
		if shadowErr != nil {
			return ParamsWriteResult{}, shadowErr
		}
		stagedDiff := cloneMap(diffMap)
		stagedDiff["staged"] = true
		stagedDiff["desiredRevision"] = desiredRev
		stagedDiff["transportError"] = callErr.Error()
		auditEntry, auditErr := s.store.RecordConcentratorAudit(stageCtx, tenantID, gatewayID, connectivity.ConcentratorAuditEntry{
			UnitID:     unitID,
			ActorID:    claims.PrincipalID,
			Operation:  OperationWriteParams,
			RequestID:  req.RequestID,
			Outcome:    OutcomeStagedShadow,
			Reason:     reason,
			Diff:       stagedDiff,
			OccurredAt: s.store.Clock(),
		})
		if auditErr != nil {
			return ParamsWriteResult{}, auditErr
		}
		return ParamsWriteResult{
			GatewayID:       gatewayID,
			UnitID:          unitID,
			RequestID:       req.RequestID,
			WrittenCount:    0,
			Params:          stagedParams,
			Audit:           auditEntry,
			Staged:          true,
			DesiredRevision: desiredRev,
		}, nil
	}
	auditEntry, auditErr := s.persistWriteAudit(ctx, tenantID, claims.PrincipalID, gatewayID, unitID, OperationWriteParams, req.RequestID, reason, diffMap, resp, callErr)
	if callErr != nil {
		return ParamsWriteResult{}, callErr
	}
	if err := mapRPCOutcomeError(resp); err != nil {
		return ParamsWriteResult{}, err
	}
	if auditErr != nil {
		return ParamsWriteResult{}, auditErr
	}
	if resp.Params == nil {
		return ParamsWriteResult{}, fmt.Errorf("%w: missing params in WRITE_PARAMS reply", ErrEdgeFailed)
	}
	s.rememberParams(gatewayID, unitID, *resp.Params)
	desiredRev, shadowErr := s.store.UpsertConcentratorShadowState(ctx, tenantID, gatewayID, unitID, resp.Params, nil, claims.PrincipalID, reason, true)
	if shadowErr != nil {
		return ParamsWriteResult{}, shadowErr
	}
	return ParamsWriteResult{
		GatewayID:       gatewayID,
		UnitID:          unitID,
		RequestID:       req.RequestID,
		WrittenCount:    resp.WrittenCount,
		Params:          *resp.Params,
		Audit:           auditEntry,
		DesiredRevision: desiredRev,
	}, nil
}

func (s *Service) EIDs(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, unitID int) (EIDTableResponse, error) {
	tenantID, release, err := s.prepare(ctx, claims, gatewayID, unitID)
	if err != nil {
		return EIDTableResponse{}, err
	}
	defer release()

	req, err := s.newRequest(ctx, gatewayID, unitID, OperationReadEIDs)
	if err != nil {
		return EIDTableResponse{}, err
	}
	resp, err := s.rpc.Call(ctx, req)
	if err != nil {
		return EIDTableResponse{}, err
	}
	if err := mapRPCOutcomeError(resp); err != nil {
		return EIDTableResponse{}, err
	}
	if len(resp.Slots) != EIDSlotCount {
		return EIDTableResponse{}, fmt.Errorf("%w: expected %d slots in reply, got %d", ErrEdgeFailed, EIDSlotCount, len(resp.Slots))
	}
	audits, err := s.store.ListConcentratorAudits(ctx, tenantID, gatewayID, unitID, defaultAuditCap)
	if err != nil {
		return EIDTableResponse{}, err
	}
	return EIDTableResponse{
		GatewayID:    gatewayID,
		UnitID:       unitID,
		RespondedAt:  millisToTime(resp.RespondedAt, s.store.Clock()),
		Slots:        resp.Slots,
		RecentAudits: audits,
	}, nil
}

func (s *Service) UpdateEIDs(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, unitID int, input EIDUpdateRequest) (EIDWriteResult, error) {
	reason, err := validateReason(input.Reason)
	if err != nil {
		return EIDWriteResult{}, err
	}
	orderedSlots, diffSummary, err := validateEIDSlots(input.Slots)
	if err != nil {
		return EIDWriteResult{}, err
	}
	tenantID, release, err := s.prepareWrite(ctx, claims, gatewayID, unitID)
	if err != nil {
		return EIDWriteResult{}, err
	}
	defer release()

	req, err := s.newRequest(ctx, gatewayID, unitID, OperationWriteEIDs)
	if err != nil {
		return EIDWriteResult{}, err
	}
	req.Slots = orderedSlots

	resp, callErr := s.rpc.Call(ctx, req)
	if input.StageIfOffline && (errors.Is(callErr, ErrTransportUnavailable) || errors.Is(callErr, ErrRPCTimeout)) {
		stageCtx, stageCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stageCancel()
		desiredRev, shadowErr := s.store.UpsertConcentratorShadowState(stageCtx, tenantID, gatewayID, unitID, nil, orderedSlots, claims.PrincipalID, reason, false)
		if shadowErr != nil {
			return EIDWriteResult{}, shadowErr
		}
		stagedDiff := cloneMap(diffSummary)
		stagedDiff["staged"] = true
		stagedDiff["desiredRevision"] = desiredRev
		stagedDiff["transportError"] = callErr.Error()
		auditEntry, auditErr := s.store.RecordConcentratorAudit(stageCtx, tenantID, gatewayID, connectivity.ConcentratorAuditEntry{
			UnitID:     unitID,
			ActorID:    claims.PrincipalID,
			Operation:  OperationWriteEIDs,
			RequestID:  req.RequestID,
			Outcome:    OutcomeStagedShadow,
			Reason:     reason,
			Diff:       stagedDiff,
			OccurredAt: s.store.Clock(),
		})
		if auditErr != nil {
			return EIDWriteResult{}, auditErr
		}
		return EIDWriteResult{
			GatewayID:       gatewayID,
			UnitID:          unitID,
			RequestID:       req.RequestID,
			UpdatedSlots:    0,
			ChunkCount:      0,
			Slots:           orderedSlots,
			Audit:           auditEntry,
			Staged:          true,
			DesiredRevision: desiredRev,
		}, nil
	}
	if callErr == nil && resp.Status == "OK" {
		diffSummary["updatedSlots"] = resp.UpdatedSlots
		diffSummary["chunkCount"] = resp.ChunkCount
	}
	auditEntry, auditErr := s.persistWriteAudit(ctx, tenantID, claims.PrincipalID, gatewayID, unitID, OperationWriteEIDs, req.RequestID, reason, diffSummary, resp, callErr)
	if callErr != nil {
		return EIDWriteResult{}, callErr
	}
	if err := mapRPCOutcomeError(resp); err != nil {
		return EIDWriteResult{}, err
	}
	if auditErr != nil {
		return EIDWriteResult{}, auditErr
	}
	if len(resp.Slots) != EIDSlotCount {
		return EIDWriteResult{}, fmt.Errorf("%w: expected %d slots in WRITE_EIDS reply, got %d", ErrEdgeFailed, EIDSlotCount, len(resp.Slots))
	}
	desiredRev, shadowErr := s.store.UpsertConcentratorShadowState(ctx, tenantID, gatewayID, unitID, nil, resp.Slots, claims.PrincipalID, reason, true)
	if shadowErr != nil {
		return EIDWriteResult{}, shadowErr
	}
	return EIDWriteResult{
		GatewayID:       gatewayID,
		UnitID:          unitID,
		RequestID:       req.RequestID,
		UpdatedSlots:    resp.UpdatedSlots,
		ChunkCount:      resp.ChunkCount,
		Slots:           resp.Slots,
		Audit:           auditEntry,
		DesiredRevision: desiredRev,
	}, nil
}

func truncateRunes(value string, maxRunes int) string {
	trimmed := strings.TrimSpace(value)
	if maxRunes <= 0 || utf8.RuneCountInString(trimmed) <= maxRunes {
		return trimmed
	}
	count := 0
	for idx := range trimmed {
		if count == maxRunes {
			return trimmed[:idx]
		}
		count++
	}
	return trimmed
}

func nonEmptyDriftDetail(err error, fallback string) string {
	var msg string
	if err != nil {
		msg = err.Error()
	}
	if strings.TrimSpace(msg) == "" {
		msg = fallback
	}
	return truncateRunes(msg, 512)
}

func (s *Service) ReconcileConcentratorShadow(ctx context.Context, gatewayID string) error {
	if s == nil || s.store == nil || s.rpc == nil {
		return ErrTransportUnavailable
	}
	releaseGateway, gwLockErr := s.lockGatewayReconcile(ctx, gatewayID)
	if gwLockErr != nil {
		return gwLockErr
	}
	defer releaseGateway()

	shadow, err := s.store.LoadGatewayConfigShadowByGatewayID(ctx, gatewayID)
	if err != nil {
		return err
	}
	if shadow.Desired == nil || len(shadow.Desired.ConcentratorConfigs) == 0 {
		return nil
	}
	unitKeys := sortedNumericUnitKeys(shadow.Desired.ConcentratorConfigs)
	actorID := shadow.Desired.UpdatedBy
	if actorID == "" {
		actorID = gatewayID
	}
	reconcileReason := truncateUTF8("config shadow reconcile: "+shadow.Desired.Reason, 512)
	reconciledUnits := make(map[string]connectivity.ConcentratorUnitShadow)
	renamedUnits := make(map[string]string)
	var failedDrifts []connectivity.DriftDetail
	var errMessages []string
	allUnreachable := true

	for _, unitKeyStr := range unitKeys {
		unitID, parseErr := strconv.Atoi(unitKeyStr)
		if parseErr != nil || validateUnitID(unitID) != nil {
			continue
		}
		desiredUnit := shadow.Desired.ConcentratorConfigs[unitKeyStr]
		var reportedUnit connectivity.ConcentratorUnitShadow
		if shadow.Reported != nil {
			reportedUnit = shadow.Reported.ConcentratorConfigs[unitKeyStr]
		}
		release, lockErr := s.lockUnit(ctx, gatewayID, unitID)
		if lockErr != nil {
			return lockErr
		}
		unitChanged := false
		effectiveUnitID := unitID
		targetKeyStr := unitKeyStr

		if desiredUnit.Params != nil && (reportedUnit.Params == nil || *reportedUnit.Params != *desiredUnit.Params) {
			patchMap := diffParamsToPatchMap(reportedUnit.Params, *desiredUnit.Params)
			if len(patchMap) > 0 {
				req, reqErr := s.newRequest(ctx, gatewayID, unitID, OperationWriteParams)
				if reqErr != nil {
					release()
					return reqErr
				}
				req.Params = patchMap
				diffMap := map[string]any{"requested": patchMap, "reconcileDesiredRevision": shadow.Desired.DesiredRevision}
				resp, callErr := s.rpc.Call(ctx, req)
				_, _ = s.persistWriteAudit(ctx, shadow.TenantID, actorID, gatewayID, unitID, OperationWriteParams, req.RequestID, reconcileReason, diffMap, resp, callErr)
				var rpcErr error
				if callErr != nil {
					rpcErr = callErr
				} else {
					rpcErr = mapRPCOutcomeError(resp)
				}
				if rpcErr == nil && resp.Params == nil {
					rpcErr = fmt.Errorf("%w: missing params in WRITE_PARAMS reply", ErrEdgeFailed)
				}
				if rpcErr != nil {
					if !errors.Is(rpcErr, ErrRPCTimeout) && !errors.Is(rpcErr, ErrTransportUnavailable) {
						allUnreachable = false
					}
					failedDrifts = append(failedDrifts, connectivity.DriftDetail{
						Section: connectivity.DriftSectionConcentrator,
						Target:  truncateRunes("unit:"+unitKeyStr+".params", 256),
						Reason:  connectivity.DriftReasonEdgeApplyFailed,
						Detail:  nonEmptyDriftDetail(rpcErr, "concentrator params write failed"),
					})
					errMessages = append(errMessages, fmt.Sprintf("unit %s params: %s", unitKeyStr, rpcErr.Error()))
				} else {
					s.rememberParams(gatewayID, unitID, *resp.Params)
					cp := *resp.Params
					reportedUnit.Params = &cp
					unitChanged = true
					if resp.Params.ModbusID > 0 && resp.Params.ModbusID != unitID {
						effectiveUnitID = resp.Params.ModbusID
						targetKeyStr = strconv.Itoa(effectiveUnitID)
						renamedUnits[unitKeyStr] = targetKeyStr
						release()
						release, lockErr = s.lockUnit(ctx, gatewayID, effectiveUnitID)
						if lockErr != nil {
							return lockErr
						}
					}
				}
			}
		}

		if !canonicalEIDsEqual(desiredUnit.EIDs, reportedUnit.EIDs) || (desiredUnit.EIDs != nil && reportedUnit.EIDs == nil) {
			targetSlots := expandEIDSlots(desiredUnit.EIDs)
			req, reqErr := s.newRequest(ctx, gatewayID, effectiveUnitID, OperationWriteEIDs)
			if reqErr != nil {
				release()
				return reqErr
			}
			req.Slots = targetSlots
			diffSummary := map[string]any{
				"slotCount":                EIDSlotCount,
				"nonZeroCount":             countNonZeroEIDs(targetSlots),
				"reconcileDesiredRevision": shadow.Desired.DesiredRevision,
			}
			resp, callErr := s.rpc.Call(ctx, req)
			if callErr == nil && resp.Status == "OK" {
				diffSummary["updatedSlots"] = resp.UpdatedSlots
				diffSummary["chunkCount"] = resp.ChunkCount
			}
			_, _ = s.persistWriteAudit(ctx, shadow.TenantID, actorID, gatewayID, effectiveUnitID, OperationWriteEIDs, req.RequestID, reconcileReason, diffSummary, resp, callErr)
			var rpcErr error
			if callErr != nil {
				rpcErr = callErr
			} else {
				rpcErr = mapRPCOutcomeError(resp)
			}
			if rpcErr == nil && len(resp.Slots) != EIDSlotCount {
				rpcErr = fmt.Errorf("%w: expected %d slots in WRITE_EIDS reply, got %d", ErrEdgeFailed, EIDSlotCount, len(resp.Slots))
			}
			if rpcErr != nil {
				if !errors.Is(rpcErr, ErrRPCTimeout) && !errors.Is(rpcErr, ErrTransportUnavailable) {
					allUnreachable = false
				}
				failedDrifts = append(failedDrifts, connectivity.DriftDetail{
					Section: connectivity.DriftSectionConcentrator,
					Target:  truncateRunes("unit:"+targetKeyStr+".eids", 256),
					Reason:  connectivity.DriftReasonEdgeApplyFailed,
					Detail:  nonEmptyDriftDetail(rpcErr, "concentrator EID write failed"),
				})
				errMessages = append(errMessages, fmt.Sprintf("unit %s eids: %s", targetKeyStr, rpcErr.Error()))
			} else {
				reportedUnit.EIDs = resp.Slots
				unitChanged = true
			}
		}

		release()
		if unitChanged {
			reconciledUnits[targetKeyStr] = reportedUnit
		}
	}

	var errMsgPtr *string
	if len(errMessages) > 0 {
		joined := truncateRunes(strings.Join(errMessages, "; "), 1024)
		errMsgPtr = &joined
	}
	if len(reconciledUnits) > 0 || len(renamedUnits) > 0 || len(failedDrifts) > 0 {
		gatewayUnreachable := len(failedDrifts) > 0 && len(reconciledUnits) == 0 && allUnreachable
		saveCtx := ctx
		if ctx.Err() != nil {
			var cancel context.CancelFunc
			saveCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
		}
		if err := s.store.RecordConcentratorReconcileResult(saveCtx, shadow.TenantID, gatewayID, shadow.Desired.DesiredRevision, reconciledUnits, renamedUnits, failedDrifts, errMsgPtr, gatewayUnreachable); err != nil {
			return err
		}
	}
	if len(failedDrifts) > 0 {
		return fmt.Errorf("%w: %s", ErrEdgeFailed, *errMsgPtr)
	}
	return nil
}

func (s *Service) rememberParams(gatewayID string, unitID int, params Params) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastSeenParams == nil {
		s.lastSeenParams = map[unitKey]Params{}
	}
	s.lastSeenParams[unitKey{gatewayID: gatewayID, unitID: unitID}] = params
	if params.ModbusID > 0 && params.ModbusID != unitID {
		s.lastSeenParams[unitKey{gatewayID: gatewayID, unitID: params.ModbusID}] = params
	}
}

func (p ParamsPatch) isComplete() bool {
	return p.ModbusID != nil &&
		p.BaudRateCode != nil &&
		p.OffLineTime != nil &&
		p.DataUplinkCycle != nil &&
		p.LocalNet != nil &&
		p.TimeMark != nil &&
		p.RfTxPower != nil &&
		p.RfFrequency != nil &&
		p.RfBandWidth != nil &&
		p.RfSf != nil &&
		p.RfLeadCode != nil &&
		p.TxTimeout != nil &&
		p.RxTimeout != nil &&
		p.RssiOut != nil
}

func (s *Service) buildStagedParams(ctx context.Context, gatewayID string, unitID int, patch ParamsPatch) (Params, bool) {
	var base Params
	found := false
	if shadow, err := s.store.LoadGatewayConfigShadowByGatewayID(ctx, gatewayID); err == nil {
		unitKeyStr := strconv.Itoa(unitID)
		if shadow.Desired != nil {
			if unit, ok := shadow.Desired.ConcentratorConfigs[unitKeyStr]; ok && unit.Params != nil {
				base = *unit.Params
				found = true
			}
		}
		if !found && shadow.Reported != nil {
			if unit, ok := shadow.Reported.ConcentratorConfigs[unitKeyStr]; ok && unit.Params != nil {
				base = *unit.Params
				found = true
			}
		}
	}
	if !found {
		s.mu.Lock()
		if cached, ok := s.lastSeenParams[unitKey{gatewayID: gatewayID, unitID: unitID}]; ok {
			base = cached
			found = true
		}
		s.mu.Unlock()
	}
	if !found {
		if !patch.isComplete() {
			return Params{}, false
		}
	}
	applyParamsPatch(&base, patch)
	return base, true
}

func defaultConcentratorParams(unitID int) Params {
	return Params{
		ModbusID:        unitID,
		BaudRateCode:    3,
		OffLineTime:     60,
		DataUplinkCycle: 30,
		LocalNet:        100,
		TimeMark:        1,
		RfTxPower:       20,
		RfFrequency:     470,
		RfBandWidth:     2,
		RfSf:            7,
		RfLeadCode:      8,
		TxTimeout:       3000,
		RxTimeout:       3000,
		RssiOut:         1,
	}
}

func applyParamsPatch(dst *Params, patch ParamsPatch) {
	if patch.ModbusID != nil {
		dst.ModbusID = *patch.ModbusID
	}
	if patch.BaudRateCode != nil {
		dst.BaudRateCode = *patch.BaudRateCode
	}
	if patch.OffLineTime != nil {
		dst.OffLineTime = *patch.OffLineTime
	}
	if patch.DataUplinkCycle != nil {
		dst.DataUplinkCycle = *patch.DataUplinkCycle
	}
	if patch.LocalNet != nil {
		dst.LocalNet = *patch.LocalNet
	}
	if patch.TimeMark != nil {
		dst.TimeMark = *patch.TimeMark
	}
	if patch.RfTxPower != nil {
		dst.RfTxPower = *patch.RfTxPower
	}
	if patch.RfFrequency != nil {
		dst.RfFrequency = *patch.RfFrequency
	}
	if patch.RfBandWidth != nil {
		dst.RfBandWidth = *patch.RfBandWidth
	}
	if patch.RfSf != nil {
		dst.RfSf = *patch.RfSf
	}
	if patch.RfLeadCode != nil {
		dst.RfLeadCode = *patch.RfLeadCode
	}
	if patch.TxTimeout != nil {
		dst.TxTimeout = *patch.TxTimeout
	}
	if patch.RxTimeout != nil {
		dst.RxTimeout = *patch.RxTimeout
	}
	if patch.RssiOut != nil {
		dst.RssiOut = *patch.RssiOut
	}
}

func diffParamsToPatchMap(prev *Params, desired Params) map[string]int {
	type fieldPair struct {
		name string
		val  int
		old  int
	}
	var old Params
	if prev != nil {
		old = *prev
	}
	fields := []fieldPair{
		{"baudRateCode", desired.BaudRateCode, old.BaudRateCode},
		{"offLineTime", desired.OffLineTime, old.OffLineTime},
		{"dataUplinkCycle", desired.DataUplinkCycle, old.DataUplinkCycle},
		{"localNet", desired.LocalNet, old.LocalNet},
		{"timeMark", desired.TimeMark, old.TimeMark},
		{"rfTxPower", desired.RfTxPower, old.RfTxPower},
		{"rfFrequency", desired.RfFrequency, old.RfFrequency},
		{"rfBandWidth", desired.RfBandWidth, old.RfBandWidth},
		{"rfSf", desired.RfSf, old.RfSf},
		{"rfLeadCode", desired.RfLeadCode, old.RfLeadCode},
		{"txTimeout", desired.TxTimeout, old.TxTimeout},
		{"rxTimeout", desired.RxTimeout, old.RxTimeout},
		{"rssiOut", desired.RssiOut, old.RssiOut},
		{"modbusId", desired.ModbusID, old.ModbusID},
	}
	out := make(map[string]int, len(fields))
	for _, field := range fields {
		if prev == nil || field.val != field.old {
			out[field.name] = field.val
		}
	}
	return out
}

func expandEIDSlots(sparse []EIDSlot) []EIDSlot {
	slots := make([]EIDSlot, EIDSlotCount)
	for i := 0; i < EIDSlotCount; i++ {
		slots[i] = EIDSlot{Slot: i + 1, EID: 0}
	}
	for _, item := range sparse {
		if item.Slot >= 1 && item.Slot <= EIDSlotCount {
			slots[item.Slot-1].EID = item.EID
		}
	}
	return slots
}

func canonicalEIDsEqual(left, right []EIDSlot) bool {
	toMap := func(slots []EIDSlot) map[int]uint32 {
		out := make(map[int]uint32)
		for _, slot := range slots {
			if slot.EID > 0 {
				out[slot.Slot] = slot.EID
			}
		}
		return out
	}
	return maps.Equal(toMap(left), toMap(right))
}

func countNonZeroEIDs(slots []EIDSlot) int {
	count := 0
	for _, slot := range slots {
		if slot.EID > 0 {
			count++
		}
	}
	return count
}

func sortedNumericUnitKeys(units map[string]connectivity.ConcentratorUnitShadow) []string {
	keys := make([]string, 0, len(units))
	for k := range units {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		na, errA := strconv.Atoi(a)
		nb, errB := strconv.Atoi(b)
		if errA == nil && errB == nil {
			return na - nb
		}
		return strings.Compare(a, b)
	})
	return keys
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	cut := 0
	for cut < len(value) {
		_, size := utf8.DecodeRuneInString(value[cut:])
		if cut+size > maxBytes {
			break
		}
		cut += size
	}
	return value[:cut]
}

func (s *Service) persistWriteAudit(
	ctx context.Context,
	tenantID, actorID, gatewayID string,
	unitID int,
	operation, requestID, reason string,
	diff map[string]any,
	resp RPCResponse,
	callErr error,
) (connectivity.ConcentratorAuditEntry, error) {
	outcome := OutcomeApplied
	enrichedDiff := cloneMap(diff)
	switch {
	case errors.Is(callErr, ErrRPCTimeout):
		outcome = OutcomeTimeout
	case callErr != nil:
		outcome = OutcomeFailed
		enrichedDiff["error"] = callErr.Error()
	case strings.EqualFold(resp.Status, OutcomeRejected):
		outcome = OutcomeRejected
		if resp.ErrorCode != "" {
			enrichedDiff["errorCode"] = resp.ErrorCode
		}
	case !strings.EqualFold(resp.Status, "OK"):
		outcome = OutcomeFailed
		if resp.ErrorCode != "" {
			enrichedDiff["errorCode"] = resp.ErrorCode
		}
	}
	if resp.Status == "OK" && resp.Params != nil {
		enrichedDiff["verified"] = resp.Params
	}
	auditCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		auditCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
	}
	return s.store.RecordConcentratorAudit(auditCtx, tenantID, gatewayID, connectivity.ConcentratorAuditEntry{
		UnitID:     unitID,
		ActorID:    actorID,
		Operation:  operation,
		RequestID:  requestID,
		Outcome:    outcome,
		Reason:     reason,
		Diff:       enrichedDiff,
		OccurredAt: s.store.Clock(),
	})
}

func mapRPCOutcomeError(resp RPCResponse) error {
	if resp.Status == "OK" {
		return nil
	}
	switch resp.ErrorCode {
	case "CONCENTRATOR_UID_PROTECTED":
		return fmt.Errorf("%w: %s", ErrProtectedUID, resp.ErrorMessage)
	case "CONCENTRATOR_EID_DUPLICATE":
		return fmt.Errorf("%w: %s", ErrDuplicateEID, resp.ErrorMessage)
	case "CONCENTRATOR_PARAMS_INVALID":
		return fmt.Errorf("%w: %s", ErrParamsInvalid, resp.ErrorMessage)
	}
	if strings.EqualFold(resp.Status, OutcomeRejected) {
		return fmt.Errorf("%w: %s (%s)", ErrEdgeRejected, resp.ErrorCode, resp.ErrorMessage)
	}
	return fmt.Errorf("%w: %s (%s)", ErrEdgeFailed, resp.ErrorCode, resp.ErrorMessage)
}

func cloneMap(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func millisToTime(ms int64, fallback time.Time) time.Time {
	if ms <= 0 {
		return fallback.UTC()
	}
	return time.UnixMilli(ms).UTC()
}

func ParseUnitID(raw string) (int, error) {
	unitID, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%w: invalid unitId %q", ErrInvalidRequest, raw)
	}
	if err := validateUnitID(unitID); err != nil {
		return 0, err
	}
	return unitID, nil
}
