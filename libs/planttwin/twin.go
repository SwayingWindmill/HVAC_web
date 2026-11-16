package planttwin

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

var (
	ErrSiteNotFound  = errors.New("plant twin site not found")
	ErrInvalidScope  = errors.New("plant twin scope must contain valid tenantId and siteId UUIDs")
	ErrInvalidSchema = errors.New("plant twin view row violates contract")
)

// SiteScope identifies the tenant and site slice loaded into a SiteTwin.
type SiteScope struct {
	TenantID string `json:"tenantId"`
	SiteID   string `json:"siteId"`
}

// Validate checks that TenantID and SiteID are non-empty UUID strings.
func (s SiteScope) Validate() error {
	if !isValidUUID(s.TenantID) || !isValidUUID(s.SiteID) {
		return ErrInvalidScope
	}
	return nil
}

func (s SiteScope) normalized() SiteScope {
	return SiteScope{
		TenantID: strings.ToLower(strings.TrimSpace(s.TenantID)),
		SiteID:   strings.ToLower(strings.TrimSpace(s.SiteID)),
	}
}

func isValidUUID(raw string) bool {
	v := strings.TrimSpace(raw)
	if len(v) != 36 || v[8] != '-' || v[13] != '-' || v[18] != '-' || v[23] != '-' {
		return false
	}
	if v == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	compact := strings.ReplaceAll(v, "-", "")
	if len(compact) != 32 {
		return false
	}
	_, err := hex.DecodeString(compact)
	return err == nil
}

func normalizeID(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// PlantUnit describes one active physical plant asset and its control lock state.
type PlantUnit struct {
	AssetID                string   `json:"assetId"`
	AssetCode              string   `json:"assetCode"`
	AssetName              string   `json:"assetName"`
	AssetType              string   `json:"assetType"`
	RatedPowerKW           *float64 `json:"ratedPowerKw,omitempty"`
	RatedCapacityKW        *float64 `json:"ratedCapacityKw,omitempty"`
	FeederAuxiliaryPowerKW float64  `json:"feederAuxiliaryPowerKw,omitempty"`
	LockMode               string   `json:"lockMode"`
	LockReason             string   `json:"lockReason,omitempty"`
}

// TwinDevice describes one active gateway or field device endpoint in the site.
type TwinDevice struct {
	DeviceID   string `json:"deviceId"`
	DeviceCode string `json:"deviceCode"`
	DeviceType string `json:"deviceType"`
	SourceKey  string `json:"sourceKey,omitempty"`
}

// DeviceBinding associates a physical PlantUnit with a TwinDevice under a role.
type DeviceBinding struct {
	AssetID     string `json:"assetId"`
	DeviceID    string `json:"deviceId"`
	BindingRole string `json:"bindingRole"`
}

// PointBinding maps an active point on a reporting device to its measurement or
// control subject and canonical SemanticRole.
type PointBinding struct {
	PointID         string          `json:"pointId"`
	DeviceID        string          `json:"deviceId"`
	DeviceCode      string          `json:"deviceCode"`
	DeviceSourceKey string          `json:"deviceSourceKey,omitempty"`
	PointCode       string          `json:"pointCode"`
	WireSourceKey   string          `json:"wireSourceKey"`
	PointType       string          `json:"pointType"`
	Unit            string          `json:"unit,omitempty"`
	Writable        bool            `json:"writable"`
	SourceMetadata  json.RawMessage `json:"sourceMetadata,omitempty"`
	SubjectType     string          `json:"subjectType"`
	SpaceID         string          `json:"spaceId,omitempty"`
	AssetID         string          `json:"assetId,omitempty"`
	BindingRole     string          `json:"bindingRole"`
	SemanticRole    SemanticRole    `json:"semanticRole,omitempty"`
	IsCopied        bool            `json:"isCopied,omitempty"`
}

// HydraulicEdge represents a directed hydraulic SERVES pairing between two assets.
type HydraulicEdge struct {
	FromAssetID      string `json:"fromAssetId"`
	ToAssetID        string `json:"toAssetId"`
	RelationshipRole string `json:"relationshipRole"`
}

// ScheduleSlot defines a cooling window in site-local time (1=Monday..7=Sunday).
type ScheduleSlot struct {
	DayOfWeek         int      `json:"dayOfWeek,omitempty"`
	StartMinute       int      `json:"startMinute"`
	EndMinute         int      `json:"endMinute"`
	TargetSupplyTempC *float64 `json:"targetSupplyTempC,omitempty"`
}

// ScheduleException represents an approved full-day date-range override.
type ScheduleException struct {
	ID               string         `json:"id"`
	Title            string         `json:"title"`
	Kind             string         `json:"kind"`
	StartDate        string         `json:"startDate"`
	EndDate          string         `json:"endDate"`
	ReplacementSlots []ScheduleSlot `json:"replacementSlots,omitempty"`
	Reason           string         `json:"reason,omitempty"`
}

// OperatingSchedule holds the site's weekly cooling slots, grace buffers, and exceptions.
type OperatingSchedule struct {
	ScheduleID          string              `json:"scheduleId,omitempty"`
	Timezone            string              `json:"timezone"`
	PreCoolLeadMinutes  int                 `json:"preCoolLeadMinutes"`
	CoastDownLagMinutes int                 `json:"coastDownLagMinutes"`
	Revision            int64               `json:"revision"`
	Slots               []ScheduleSlot      `json:"slots,omitempty"`
	Exceptions          []ScheduleException `json:"exceptions,omitempty"`
}

// SiteTwin is the self-contained semantic model of a site at LoadedAt.
type SiteTwin struct {
	Scope             SiteScope         `json:"scope"`
	LoadedAt          time.Time         `json:"loadedAt"`
	Timezone          string            `json:"timezone"`
	SiteControlMode   string            `json:"siteControlMode"`
	SiteControlReason string            `json:"siteControlReason,omitempty"`
	Units             []PlantUnit       `json:"units"`
	Devices           []TwinDevice      `json:"devices"`
	DeviceBindings    []DeviceBinding   `json:"deviceBindings"`
	PointBindings     []PointBinding    `json:"pointBindings"`
	HydraulicEdges    []HydraulicEdge   `json:"hydraulicEdges"`
	Schedule          OperatingSchedule `json:"schedule"`
	Graph             *HydraulicGraph   `json:"-"`
}

// NewSiteTwin normalizes twin slices, derives IsCopied flags, and constructs Graph.
func NewSiteTwin(twin SiteTwin) (SiteTwin, error) {
	cloned := cloneSiteTwin(twin)
	if strings.TrimSpace(cloned.SiteControlMode) == "" {
		cloned.SiteControlMode = ControlModeRemoteAuto
	}
	if strings.TrimSpace(cloned.Schedule.Timezone) == "" {
		cloned.Schedule.Timezone = strings.TrimSpace(cloned.Timezone)
	}
	for i := range cloned.Units {
		if strings.TrimSpace(cloned.Units[i].LockMode) == "" {
			cloned.Units[i].LockMode = LockModeUnlocked
		}
	}
	for i := range cloned.PointBindings {
		cloned.PointBindings[i].IsCopied = effectiveIsCopied(cloned.PointBindings[i])
	}
	graph, err := NewHydraulicGraph(cloned.Units, cloned.HydraulicEdges)
	if err != nil {
		return SiteTwin{}, err
	}
	cloned.Graph = graph
	return cloned, nil
}

// IsCopiedMetadata reports whether source_metadata marks the point as copied from a peer.
func IsCopiedMetadata(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	var meta struct {
		Copied bool `json:"copied"`
	}
	if err := json.Unmarshal(trimmed, &meta); err != nil {
		return false
	}
	return meta.Copied
}

func effectiveIsCopied(pb PointBinding) bool {
	return pb.IsCopied || IsCopiedMetadata(pb.SourceMetadata)
}

// UnitByID returns the matching PlantUnit for assetID.
func (t SiteTwin) UnitByID(assetID string) (PlantUnit, bool) {
	target := normalizeID(assetID)
	if target == "" {
		return PlantUnit{}, false
	}
	for _, u := range t.Units {
		if normalizeID(u.AssetID) == target {
			return clonePlantUnit(u), true
		}
	}
	return PlantUnit{}, false
}

// UnitsByType returns units matching any of assetTypes (or all units when empty),
// sorted deterministically by AssetCode then AssetID.
func (t SiteTwin) UnitsByType(assetTypes ...string) []PlantUnit {
	allowed := makeTypeSet(assetTypes)
	var out []PlantUnit
	for _, u := range t.Units {
		if len(allowed) > 0 {
			if _, ok := allowed[strings.ToUpper(strings.TrimSpace(u.AssetType))]; !ok {
				continue
			}
		}
		out = append(out, clonePlantUnit(u))
	}
	sortPlantUnits(out)
	return out
}

// ResolveAssetPoints returns all active point bindings on assetID for role, ordered
// with primary non-copied DESCRIBES points before copied or control bindings.
func (t SiteTwin) ResolveAssetPoints(assetID string, role SemanticRole) []PointBinding {
	targetAsset := normalizeID(assetID)
	targetRole := normalizeSemanticRole(role)
	if targetAsset == "" || targetRole == "" {
		return nil
	}
	var out []PointBinding
	for _, pb := range t.PointBindings {
		if normalizeSemanticRole(pb.SemanticRole) != targetRole {
			continue
		}
		if normalizeID(pb.AssetID) != targetAsset {
			continue
		}
		subj := strings.ToUpper(strings.TrimSpace(pb.SubjectType))
		if subj != "" && subj != SubjectTypeAsset {
			continue
		}
		copied := clonePointBinding(pb)
		copied.IsCopied = effectiveIsCopied(pb)
		out = append(out, copied)
	}
	sortPointBindings(out)
	return out
}

// ResolveAssetPoint returns the highest-priority point binding on assetID for role.
func (t SiteTwin) ResolveAssetPoint(assetID string, role SemanticRole) (PointBinding, bool) {
	pts := t.ResolveAssetPoints(assetID, role)
	if len(pts) == 0 {
		return PointBinding{}, false
	}
	return pts[0], true
}

// ResolveSubjectPoints returns all point bindings matching subjectType and role,
// sorted with the standard priority rules.
func (t SiteTwin) ResolveSubjectPoints(subjectType string, role SemanticRole) []PointBinding {
	targetSubj := strings.ToUpper(strings.TrimSpace(subjectType))
	targetRole := normalizeSemanticRole(role)
	if targetSubj == "" || targetRole == "" {
		return nil
	}
	var out []PointBinding
	for _, pb := range t.PointBindings {
		if strings.ToUpper(strings.TrimSpace(pb.SubjectType)) != targetSubj {
			continue
		}
		if normalizeSemanticRole(pb.SemanticRole) != targetRole {
			continue
		}
		copied := clonePointBinding(pb)
		copied.IsCopied = effectiveIsCopied(pb)
		out = append(out, copied)
	}
	sortPointBindings(out)
	return out
}

// PointsByRole returns all point bindings matching role across SITE, SPACE, and ASSET subjects.
func (t SiteTwin) PointsByRole(role SemanticRole) []PointBinding {
	targetRole := normalizeSemanticRole(role)
	if targetRole == "" {
		return nil
	}
	var out []PointBinding
	for _, pb := range t.PointBindings {
		if normalizeSemanticRole(pb.SemanticRole) != targetRole {
			continue
		}
		copied := clonePointBinding(pb)
		copied.IsCopied = effectiveIsCopied(pb)
		out = append(out, copied)
	}
	sortPointBindings(out)
	return out
}

// CommandRoutesForAsset returns active writable COMMAND points bound directly to
// assetID or via a device bound to assetID when the point has no explicit subject.
func (t SiteTwin) CommandRoutesForAsset(assetID string) []PointBinding {
	targetAsset := normalizeID(assetID)
	if targetAsset == "" {
		return nil
	}
	boundDevices := make(map[string]struct{})
	for _, b := range t.DeviceBindings {
		if normalizeID(b.AssetID) == targetAsset {
			if devKey := normalizeID(b.DeviceID); devKey != "" {
				boundDevices[devKey] = struct{}{}
			}
		}
	}

	byPoint := make(map[string]PointBinding)
	for _, pb := range t.PointBindings {
		if !pb.Writable || !strings.EqualFold(strings.TrimSpace(pb.PointType), PointTypeCommand) {
			continue
		}
		subj := strings.ToUpper(strings.TrimSpace(pb.SubjectType))
		role := strings.ToUpper(strings.TrimSpace(pb.BindingRole))
		pbAsset := normalizeID(pb.AssetID)

		matched := false
		switch {
		case pbAsset != "":
			matched = pbAsset == targetAsset && (subj == "" || subj == SubjectTypeAsset) && (role == "" || role == BindingControls)
		case subj == "":
			_, onBoundDevice := boundDevices[normalizeID(pb.DeviceID)]
			matched = onBoundDevice && (role == "" || role == BindingControls)
		}
		if !matched {
			continue
		}

		ptKey := normalizeID(pb.PointID)
		if ptKey == "" {
			ptKey = strings.ToLower(pb.DeviceID + "\x00" + pb.PointCode)
		}
		candidate := clonePointBinding(pb)
		candidate.IsCopied = effectiveIsCopied(pb)
		if existing, exists := byPoint[ptKey]; !exists || (existing.AssetID == "" && candidate.AssetID != "") {
			byPoint[ptKey] = candidate
		}
	}

	out := make([]PointBinding, 0, len(byPoint))
	for _, pb := range byPoint {
		out = append(out, pb)
	}
	sortPointBindings(out)
	return out
}

// DevicesForAsset returns active devices bound to assetID, optionally filtered by binding roles.
func (t SiteTwin) DevicesForAsset(assetID string, roles ...string) []TwinDevice {
	targetAsset := normalizeID(assetID)
	if targetAsset == "" {
		return nil
	}
	roleFilter := makeTypeSet(roles)
	devByKey := make(map[string]TwinDevice, len(t.Devices))
	for _, d := range t.Devices {
		devByKey[normalizeID(d.DeviceID)] = d
	}

	seen := make(map[string]struct{})
	var out []TwinDevice
	for _, b := range t.DeviceBindings {
		if normalizeID(b.AssetID) != targetAsset {
			continue
		}
		if len(roleFilter) > 0 {
			if _, ok := roleFilter[strings.ToUpper(strings.TrimSpace(b.BindingRole))]; !ok {
				continue
			}
		}
		devKey := normalizeID(b.DeviceID)
		if _, dup := seen[devKey]; dup {
			continue
		}
		if dev, ok := devByKey[devKey]; ok {
			seen[devKey] = struct{}{}
			out = append(out, dev)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeviceCode != out[j].DeviceCode {
			return out[i].DeviceCode < out[j].DeviceCode
		}
		return out[i].DeviceID < out[j].DeviceID
	})
	return out
}

// EvaluateSchedule evaluates the twin's operating schedule at the given instant.
func (t SiteTwin) EvaluateSchedule(at time.Time) (ScheduleEvaluation, error) {
	sched := t.Schedule
	if strings.TrimSpace(sched.Timezone) == "" {
		sched.Timezone = t.Timezone
	}
	return EvaluateOperatingSchedule(at, sched)
}

func normalizeSemanticRole(role SemanticRole) SemanticRole {
	return SemanticRole(strings.ToUpper(strings.TrimSpace(string(role))))
}

func makeTypeSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		if clean := strings.ToUpper(strings.TrimSpace(v)); clean != "" {
			set[clean] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func sortPlantUnits(units []PlantUnit) {
	sort.Slice(units, func(i, j int) bool {
		if units[i].AssetCode != units[j].AssetCode {
			return units[i].AssetCode < units[j].AssetCode
		}
		return units[i].AssetID < units[j].AssetID
	})
}

func sortPointBindings(bindings []PointBinding) {
	sort.Slice(bindings, func(i, j int) bool {
		a, b := bindings[i], bindings[j]
		aCopied, bCopied := effectiveIsCopied(a), effectiveIsCopied(b)
		if aCopied != bCopied {
			return !aCopied
		}
		aDescribes := strings.EqualFold(strings.TrimSpace(a.BindingRole), BindingDescribes)
		bDescribes := strings.EqualFold(strings.TrimSpace(b.BindingRole), BindingDescribes)
		if aDescribes != bDescribes {
			return aDescribes
		}
		if a.DeviceCode != b.DeviceCode {
			return a.DeviceCode < b.DeviceCode
		}
		if a.PointCode != b.PointCode {
			return a.PointCode < b.PointCode
		}
		if a.PointID != b.PointID {
			return a.PointID < b.PointID
		}
		if a.SubjectType != b.SubjectType {
			return a.SubjectType < b.SubjectType
		}
		if a.AssetID != b.AssetID {
			return a.AssetID < b.AssetID
		}
		return a.SpaceID < b.SpaceID
	})
}

func cloneFloatPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	copied := *v
	return &copied
}

func clonePlantUnit(u PlantUnit) PlantUnit {
	u.RatedPowerKW = cloneFloatPtr(u.RatedPowerKW)
	u.RatedCapacityKW = cloneFloatPtr(u.RatedCapacityKW)
	return u
}

func clonePointBinding(pb PointBinding) PointBinding {
	if pb.SourceMetadata != nil {
		meta := make([]byte, len(pb.SourceMetadata))
		copy(meta, pb.SourceMetadata)
		pb.SourceMetadata = meta
	}
	return pb
}

func cloneScheduleSlot(s ScheduleSlot) ScheduleSlot {
	s.TargetSupplyTempC = cloneFloatPtr(s.TargetSupplyTempC)
	return s
}

func cloneOperatingSchedule(src OperatingSchedule) OperatingSchedule {
	out := OperatingSchedule{
		ScheduleID:          src.ScheduleID,
		Timezone:            src.Timezone,
		PreCoolLeadMinutes:  src.PreCoolLeadMinutes,
		CoastDownLagMinutes: src.CoastDownLagMinutes,
		Revision:            src.Revision,
	}
	if len(src.Slots) > 0 {
		out.Slots = make([]ScheduleSlot, len(src.Slots))
		for i, s := range src.Slots {
			out.Slots[i] = cloneScheduleSlot(s)
		}
	}
	if len(src.Exceptions) > 0 {
		out.Exceptions = make([]ScheduleException, len(src.Exceptions))
		for i, exc := range src.Exceptions {
			out.Exceptions[i] = cloneScheduleException(exc)
		}
	}
	return out
}

func cloneScheduleException(exc ScheduleException) ScheduleException {
	out := exc
	if len(exc.ReplacementSlots) > 0 {
		out.ReplacementSlots = make([]ScheduleSlot, len(exc.ReplacementSlots))
		for i, s := range exc.ReplacementSlots {
			out.ReplacementSlots[i] = cloneScheduleSlot(s)
		}
	}
	return out
}

func cloneSiteTwin(src SiteTwin) SiteTwin {
	out := SiteTwin{
		Scope:             src.Scope,
		LoadedAt:          src.LoadedAt,
		Timezone:          src.Timezone,
		SiteControlMode:   src.SiteControlMode,
		SiteControlReason: src.SiteControlReason,
		Schedule:          cloneOperatingSchedule(src.Schedule),
	}
	if len(src.Units) > 0 {
		out.Units = make([]PlantUnit, len(src.Units))
		for i, u := range src.Units {
			out.Units[i] = clonePlantUnit(u)
		}
	}
	if len(src.Devices) > 0 {
		out.Devices = append([]TwinDevice(nil), src.Devices...)
	}
	if len(src.DeviceBindings) > 0 {
		out.DeviceBindings = append([]DeviceBinding(nil), src.DeviceBindings...)
	}
	if len(src.PointBindings) > 0 {
		out.PointBindings = make([]PointBinding, len(src.PointBindings))
		for i, pb := range src.PointBindings {
			out.PointBindings[i] = clonePointBinding(pb)
		}
	}
	if len(src.HydraulicEdges) > 0 {
		out.HydraulicEdges = append([]HydraulicEdge(nil), src.HydraulicEdges...)
	}
	return out
}
