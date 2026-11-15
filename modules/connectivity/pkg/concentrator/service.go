package concentrator

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/modules/connectivity/pkg/connectivity"
)

const (
	OperationReadStatus  = "READ_STATUS"
	OperationWriteParams = "WRITE_PARAMS"
	OperationReadEIDs    = "READ_EIDS"
	OperationWriteEIDs   = "WRITE_EIDS"

	OutcomeApplied  = "APPLIED"
	OutcomeRejected = "REJECTED"
	OutcomeFailed   = "FAILED"
	OutcomeTimeout  = "TIMEOUT"

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
	switch unitID {
	case 2, 3, 4, 5, 10, 31:
		return true
	default:
		return false
	}
}

type Params struct {
	ModbusID        int `json:"modbusId"`
	BaudRateCode    int `json:"baudRateCode"`
	OffLineTime     int `json:"offLineTime"`
	DataUplinkCycle int `json:"dataUplinkCycle"`
	LocalNet        int `json:"localNet"`
	TimeMark        int `json:"timeMark"`
	RfTxPower       int `json:"rfTxPower"`
	RfFrequency     int `json:"rfFrequency"`
	RfBandWidth     int `json:"rfBandWidth"`
	RfSf            int `json:"rfSf"`
	RfLeadCode      int `json:"rfLeadCode"`
	TxTimeout       int `json:"txTimeout"`
	RxTimeout       int `json:"rxTimeout"`
	RssiOut         int `json:"rssiOut"`
}

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

type EIDSlot struct {
	Slot int    `json:"slot"`
	EID  uint32 `json:"eid"`
}

type StatusResponse struct {
	GatewayID    string                                `json:"gatewayId"`
	UnitID       int                                   `json:"unitId"`
	RespondedAt  time.Time                             `json:"respondedAt"`
	Params       Params                                `json:"params"`
	Channels     []ChannelReading                      `json:"channels"`
	RecentAudits []connectivity.ConcentratorAuditEntry `json:"recentAudits"`
}

type ParamsUpdateRequest struct {
	Params ParamsPatch `json:"params"`
	Reason string      `json:"reason"`
}

type ParamsWriteResult struct {
	GatewayID    string                              `json:"gatewayId"`
	UnitID       int                                 `json:"unitId"`
	RequestID    string                              `json:"requestId"`
	WrittenCount int                                 `json:"writtenCount"`
	Params       Params                              `json:"params"`
	Audit        connectivity.ConcentratorAuditEntry `json:"audit"`
}

type EIDTableResponse struct {
	GatewayID    string                                `json:"gatewayId"`
	UnitID       int                                   `json:"unitId"`
	RespondedAt  time.Time                             `json:"respondedAt"`
	Slots        []EIDSlot                             `json:"slots"`
	RecentAudits []connectivity.ConcentratorAuditEntry `json:"recentAudits"`
}

type EIDUpdateRequest struct {
	Slots  []EIDSlot `json:"slots"`
	Reason string    `json:"reason"`
}

type EIDWriteResult struct {
	GatewayID    string                              `json:"gatewayId"`
	UnitID       int                                 `json:"unitId"`
	RequestID    string                              `json:"requestId"`
	UpdatedSlots int                                 `json:"updatedSlots"`
	ChunkCount   int                                 `json:"chunkCount"`
	Slots        []EIDSlot                           `json:"slots"`
	Audit        connectivity.ConcentratorAuditEntry `json:"audit"`
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

	mu    sync.Mutex
	locks map[unitKey]*unitLock
}

func NewService(store Store, rpc RPCClient) *Service {
	return &Service{
		store: store,
		rpc:   rpc,
		locks: map[unitKey]*unitLock{},
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

func (s *Service) prepare(ctx context.Context, claims registryauth.GrantClaims, gatewayID string, unitID int) (tenantID string, release func(), err error) {
	if s == nil || s.store == nil || s.rpc == nil {
		return "", nil, ErrTransportUnavailable
	}
	if err := validateUnitID(unitID); err != nil {
		return "", nil, err
	}
	tenantID, err = s.store.ResolveGatewayForOperator(ctx, claims, gatewayID)
	if err != nil {
		return "", nil, err
	}
	release, err = s.lockUnit(ctx, gatewayID, unitID)
	if err != nil {
		return "", nil, err
	}
	return tenantID, release, nil
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
	tenantID, release, err := s.prepare(ctx, claims, gatewayID, unitID)
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
	return ParamsWriteResult{
		GatewayID:    gatewayID,
		UnitID:       unitID,
		RequestID:    req.RequestID,
		WrittenCount: resp.WrittenCount,
		Params:       *resp.Params,
		Audit:        auditEntry,
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
	tenantID, release, err := s.prepare(ctx, claims, gatewayID, unitID)
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
	return EIDWriteResult{
		GatewayID:    gatewayID,
		UnitID:       unitID,
		RequestID:    req.RequestID,
		UpdatedSlots: resp.UpdatedSlots,
		ChunkCount:   resp.ChunkCount,
		Slots:        resp.Slots,
		Audit:        auditEntry,
	}, nil
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
	return s.store.RecordConcentratorAudit(ctx, tenantID, gatewayID, connectivity.ConcentratorAuditEntry{
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
