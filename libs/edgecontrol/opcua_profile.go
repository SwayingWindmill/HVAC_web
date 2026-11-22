package edgecontrol

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DriverTypeOPCUA           = "OPC_UA"
	DeclarativeOPCUAFactoryID = "declarative-opc-ua"

	OPCUASecurityPolicyNone           = "None"
	OPCUASecurityPolicyBasic256Sha256 = "Basic256Sha256"
	OPCUASecurityModeNone             = "None"
	OPCUASecurityModeSign             = "Sign"
	OPCUASecurityModeSignAndEncrypt   = "SignAndEncrypt"

	OPCUAAuthAnonymous   = "ANONYMOUS"
	OPCUAAuthCertificate = "CERTIFICATE"
	OPCUAAuthUsername    = "USERNAME"

	defaultOPCUASubscriptionIntervalMS = 1000
)

// OPCUANodeIDType distinguishes IEC 62541 Part 6 NodeId identifier encodings.
type OPCUANodeIDType string

const (
	OPCUANodeIDNumeric    OPCUANodeIDType = "i"
	OPCUANodeIDString     OPCUANodeIDType = "s"
	OPCUANodeIDGUID       OPCUANodeIDType = "g"
	OPCUANodeIDByteString OPCUANodeIDType = "b"
)

// OPCUANodeID represents a canonical parsed OPC-UA NodeId (`ns=<u16>;<type>=<val>`).
type OPCUANodeID struct {
	NamespaceIndex uint16
	IdentifierType OPCUANodeIDType
	Numeric        uint32
	StringValue    string
	Opaque         []byte
}

// ParseOPCUANodeID parses standard `ns=<u16>;i=<u32>`, `s=<str>`, `g=<uuid>`, or `b=<base64>` NodeIds.
// When `ns=<u16>;` is omitted, NamespaceIndex defaults to 0.
func ParseOPCUANodeID(raw string) (OPCUANodeID, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 512 || containsModbusControlChar(trimmed) {
		return OPCUANodeID{}, fmt.Errorf("invalid OPC-UA nodeId %q", raw)
	}
	ns := uint16(0)
	rest := trimmed
	if strings.HasPrefix(rest, "ns=") {
		semi := strings.IndexByte(rest, ';')
		if semi <= 3 || semi == len(rest)-1 {
			return OPCUANodeID{}, fmt.Errorf("invalid namespace prefix in nodeId %q", raw)
		}
		parsedNS, err := strconv.ParseUint(rest[3:semi], 10, 16)
		if err != nil {
			return OPCUANodeID{}, fmt.Errorf("invalid namespaceIndex in nodeId %q: %w", raw, err)
		}
		ns = uint16(parsedNS)
		rest = rest[semi+1:]
	}
	if len(rest) < 3 || rest[1] != '=' {
		return OPCUANodeID{}, fmt.Errorf("invalid OPC-UA nodeId identifier in %q", raw)
	}
	kind := OPCUANodeIDType(rest[:1])
	body := rest[2:]
	out := OPCUANodeID{NamespaceIndex: ns, IdentifierType: kind}
	switch kind {
	case OPCUANodeIDNumeric:
		val, err := strconv.ParseUint(body, 10, 32)
		if err != nil || strconv.FormatUint(val, 10) != body {
			return OPCUANodeID{}, fmt.Errorf("invalid numeric OPC-UA nodeId %q", raw)
		}
		out.Numeric = uint32(val)
	case OPCUANodeIDString:
		if body == "" || utf8.RuneCountInString(body) > 256 {
			return OPCUANodeID{}, fmt.Errorf("invalid string OPC-UA nodeId %q", raw)
		}
		out.StringValue = body
	case OPCUANodeIDGUID:
		if _, err := parseOPCUAGUIDBytes(body); err != nil {
			return OPCUANodeID{}, fmt.Errorf("invalid GUID OPC-UA nodeId %q: %w", raw, err)
		}
		out.StringValue = strings.ToLower(body)
	case OPCUANodeIDByteString:
		decoded, err := base64.StdEncoding.DecodeString(body)
		if err != nil || len(decoded) == 0 || len(decoded) > 256 {
			return OPCUANodeID{}, fmt.Errorf("invalid opaque OPC-UA nodeId %q", raw)
		}
		out.Opaque = decoded
		out.StringValue = base64.StdEncoding.EncodeToString(decoded)
	default:
		return OPCUANodeID{}, fmt.Errorf("unsupported OPC-UA nodeId identifier type %q", kind)
	}
	return out, nil
}

// String renders canonical OPC-UA NodeId notation (`ns=0;i=85`).
func (n OPCUANodeID) String() string {
	switch n.IdentifierType {
	case OPCUANodeIDNumeric:
		return fmt.Sprintf("ns=%d;i=%d", n.NamespaceIndex, n.Numeric)
	case OPCUANodeIDString:
		return fmt.Sprintf("ns=%d;s=%s", n.NamespaceIndex, n.StringValue)
	case OPCUANodeIDGUID:
		return fmt.Sprintf("ns=%d;g=%s", n.NamespaceIndex, n.StringValue)
	case OPCUANodeIDByteString:
		enc := n.StringValue
		if enc == "" && len(n.Opaque) > 0 {
			enc = base64.StdEncoding.EncodeToString(n.Opaque)
		}
		return fmt.Sprintf("ns=%d;b=%s", n.NamespaceIndex, enc)
	default:
		return ""
	}
}

func parseOPCUAGUIDBytes(s string) ([16]byte, error) {
	var out [16]byte
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return out, errors.New("expected 8-4-4-4-12 UUID format")
	}
	clean := strings.ReplaceAll(s, "-", "")
	var raw [16]byte
	for i := 0; i < 16; i++ {
		v, err := strconv.ParseUint(clean[i*2:i*2+2], 16, 8)
		if err != nil {
			return out, err
		}
		raw[i] = byte(v)
	}
	// IEC 62541 GUID binary ordering: Data1 LE (4), Data2 LE (2), Data3 LE (2), Data4 BE (8).
	out[0], out[1], out[2], out[3] = raw[3], raw[2], raw[1], raw[0]
	out[4], out[5] = raw[5], raw[4]
	out[6], out[7] = raw[7], raw[6]
	copy(out[8:], raw[8:])
	return out, nil
}

func formatOPCUAGUIDBytes(b [16]byte) string {
	var canonical [16]byte
	canonical[0], canonical[1], canonical[2], canonical[3] = b[3], b[2], b[1], b[0]
	canonical[4], canonical[5] = b[5], b[4]
	canonical[6], canonical[7] = b[7], b[6]
	copy(canonical[8:], b[8:])
	return fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		canonical[0], canonical[1], canonical[2], canonical[3],
		canonical[4], canonical[5],
		canonical[6], canonical[7],
		canonical[8], canonical[9],
		canonical[10], canonical[11], canonical[12], canonical[13], canonical[14], canonical[15])
}

// OPCUANodeInput describes one wire/shadow OPC-UA Node-to-Channel mapping.
type OPCUANodeInput struct {
	PointCode       string  `json:"pointCode"`
	SemanticChannel string  `json:"semanticChannel,omitempty"`
	SafetyBoundary  string  `json:"safetyBoundary,omitempty"`
	NodeID          string  `json:"nodeId"`
	NamespaceIndex  *int    `json:"namespaceIndex,omitempty"`
	DataType        string  `json:"dataType,omitempty"`
	Writable        bool    `json:"writable,omitempty"`
	Subscribe       bool    `json:"subscribe,omitempty"`
	Scale           float64 `json:"scale,omitempty"`
	Offset          float64 `json:"offset,omitempty"`
	PollPriority    string  `json:"pollPriority"`
	Unit            string  `json:"unit,omitempty"`
}

// OPCUADriverSpecInput configures OPC-UA session security and monitored/polled node lists.
type OPCUADriverSpecInput struct {
	SecurityPolicy         string           `json:"securityPolicy"`
	SecurityMode           string           `json:"securityMode"`
	AuthMode               string           `json:"authMode,omitempty"`
	Username               string           `json:"username,omitempty"`
	CredentialRef          string           `json:"credentialRef,omitempty"`
	SubscriptionIntervalMS int              `json:"subscriptionIntervalMs,omitempty"`
	Nodes                  []OPCUANodeInput `json:"nodes"`
}

// Clone returns a deep copy of OPCUADriverSpecInput (nil-safe).
func (o *OPCUADriverSpecInput) Clone() *OPCUADriverSpecInput {
	if o == nil {
		return nil
	}
	cp := *o
	cp.Nodes = make([]OPCUANodeInput, len(o.Nodes))
	for i, n := range o.Nodes {
		cloned := n
		if n.NamespaceIndex != nil {
			ns := *n.NamespaceIndex
			cloned.NamespaceIndex = &ns
		}
		cp.Nodes[i] = cloned
	}
	return &cp
}

// OPCUANodeSpec is the validated runtime representation of one OPC-UA variable mapping.
type OPCUANodeSpec struct {
	PointCode       string
	SemanticChannel SemanticChannel
	SafetyBoundary  SafetyBoundaryClass
	NodeID          OPCUANodeID
	DataType        DataType
	Writable        bool
	Subscribe       bool
	Scale           float64
	Offset          float64
	PollPriority    DataPriority
	Unit            string
}

// EffectiveSafetyBoundary returns EDGE-003 boundary classification for this OPC-UA variable.
func (s OPCUANodeSpec) EffectiveSafetyBoundary() SafetyBoundaryClass {
	if s.SafetyBoundary == SafetyBoundaryHardRealtime {
		return SafetyBoundaryHardRealtime
	}
	if _, protected := ClassifyHardRealtimeProtection(s.PointCode, string(s.SemanticChannel), nil); protected {
		return SafetyBoundaryHardRealtime
	}
	if s.SafetyBoundary == SafetyBoundarySoftRealtime {
		return SafetyBoundarySoftRealtime
	}
	return ""
}

// DeclarativeOPCUAProfile is the validated runtime profile for DeclarativeOPCUAAdapter.
type DeclarativeOPCUAProfile struct {
	ComponentID            string
	Alias                  string
	Endpoint               string
	SecurityPolicy         string
	SecurityMode           string
	AuthMode               string
	Username               string
	CredentialRef          string
	SubscriptionIntervalMS int
	CapabilityProfile      CapabilityProfileID
	Timeout                time.Duration
	TimeoutMS              int
	Retries                int
	Nodes                  []OPCUANodeSpec
}

// ValidateOPCUADriverInput validates an OPC-UA driver specification across Gateway shadow,
// Registry DEVICE templates, and Edge runtime adapter initialization.
func ValidateOPCUADriverInput(input ModbusDriverInput, opts ModbusDriverValidationOptions) (DeclarativeOPCUAProfile, error) {
	if !strings.EqualFold(strings.TrimSpace(input.DriverType), DriverTypeOPCUA) {
		return DeclarativeOPCUAProfile{}, fmt.Errorf("expected driverType %s, got %q", DriverTypeOPCUA, input.DriverType)
	}
	if input.UnitID != nil || len(input.Registers) > 0 || input.BACnet != nil {
		return DeclarativeOPCUAProfile{}, errors.New("OPC_UA driver cannot specify unitId, registers, or bacnet configuration")
	}
	if input.OPCUA == nil {
		return DeclarativeOPCUAProfile{}, errors.New("OPC_UA driver requires opcua configuration")
	}

	hdr, err := validateDriverHeader(input, opts)
	if err != nil {
		return DeclarativeOPCUAProfile{}, err
	}
	if hdr.endpoint != "" {
		if err := validateOPCUAEndpoint(hdr.endpoint); err != nil {
			return DeclarativeOPCUAProfile{}, err
		}
	}

	spec := input.OPCUA
	policy := strings.TrimSpace(spec.SecurityPolicy)
	mode := strings.TrimSpace(spec.SecurityMode)
	switch policy {
	case OPCUASecurityPolicyNone:
		if mode != OPCUASecurityModeNone {
			return DeclarativeOPCUAProfile{}, fmt.Errorf("opcua securityPolicy None requires securityMode None, got %q", spec.SecurityMode)
		}
	case OPCUASecurityPolicyBasic256Sha256:
		if mode != OPCUASecurityModeSign && mode != OPCUASecurityModeSignAndEncrypt {
			return DeclarativeOPCUAProfile{}, fmt.Errorf("opcua securityPolicy Basic256Sha256 requires securityMode Sign or SignAndEncrypt, got %q", spec.SecurityMode)
		}
	default:
		return DeclarativeOPCUAProfile{}, fmt.Errorf("unsupported opcua securityPolicy %q", spec.SecurityPolicy)
	}

	authMode := strings.ToUpper(strings.TrimSpace(spec.AuthMode))
	if authMode == "" {
		authMode = OPCUAAuthAnonymous
	}
	username := strings.TrimSpace(spec.Username)
	credRef := strings.TrimSpace(spec.CredentialRef)
	switch authMode {
	case OPCUAAuthAnonymous:
		if username != "" {
			return DeclarativeOPCUAProfile{}, errors.New("opcua authMode ANONYMOUS cannot specify username")
		}
	case OPCUAAuthCertificate:
	case OPCUAAuthUsername:
		if username == "" || credRef == "" {
			return DeclarativeOPCUAProfile{}, errors.New("opcua authMode USERNAME requires username and credentialRef")
		}
	default:
		return DeclarativeOPCUAProfile{}, fmt.Errorf("unsupported opcua authMode %q", spec.AuthMode)
	}

	subInterval := spec.SubscriptionIntervalMS
	if subInterval == 0 {
		subInterval = defaultOPCUASubscriptionIntervalMS
	}
	if subInterval < 50 || subInterval > 60000 {
		return DeclarativeOPCUAProfile{}, fmt.Errorf("opcua.subscriptionIntervalMs %d must be in [50,60000]", spec.SubscriptionIntervalMS)
	}

	if len(spec.Nodes) < 1 || len(spec.Nodes) > 256 {
		return DeclarativeOPCUAProfile{}, fmt.Errorf("opcua.nodes count %d must be in [1,256]", len(spec.Nodes))
	}

	seenPoints := make(map[string]struct{}, len(spec.Nodes))
	seenNodeIDs := make(map[string]struct{}, len(spec.Nodes))
	seenSemantics := make(map[SemanticChannel]struct{}, len(spec.Nodes))
	validatedNodes := make([]OPCUANodeSpec, len(spec.Nodes))

	for idx, n := range spec.Nodes {
		parsed, err := validateOPCUANodeInput(n)
		if err != nil {
			return DeclarativeOPCUAProfile{}, fmt.Errorf("opcua.nodes[%d] (%s): %w", idx, n.PointCode, err)
		}
		if _, dup := seenPoints[parsed.PointCode]; dup {
			return DeclarativeOPCUAProfile{}, fmt.Errorf("duplicate OPC-UA node pointCode %q", parsed.PointCode)
		}
		seenPoints[parsed.PointCode] = struct{}{}

		canonID := parsed.NodeID.String()
		if _, dup := seenNodeIDs[canonID]; dup {
			return DeclarativeOPCUAProfile{}, fmt.Errorf("duplicate OPC-UA nodeId %q", canonID)
		}
		seenNodeIDs[canonID] = struct{}{}

		access := AccessReadOnly
		if parsed.Writable {
			access = AccessReadWrite
		}
		if err := validateCapabilityChannelAlignment(hdr.capabilityProfile, hdr.profileReqs, seenSemantics, parsed.SemanticChannel, parsed.DataType, access); err != nil {
			return DeclarativeOPCUAProfile{}, err
		}
		validatedNodes[idx] = parsed
	}

	return DeclarativeOPCUAProfile{
		ComponentID:            hdr.componentID,
		Endpoint:               hdr.endpoint,
		SecurityPolicy:         policy,
		SecurityMode:           mode,
		AuthMode:               authMode,
		Username:               username,
		CredentialRef:          credRef,
		SubscriptionIntervalMS: subInterval,
		CapabilityProfile:      hdr.capabilityProfile,
		Timeout:                hdr.timeout,
		TimeoutMS:              hdr.timeoutMS,
		Retries:                hdr.retries,
		Nodes:                  validatedNodes,
	}, nil
}

func validateOPCUAEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid OPC-UA endpoint %q: %w", endpoint, err)
	}
	if !strings.EqualFold(parsed.Scheme, "opc.tcp") {
		return fmt.Errorf("OPC-UA endpoint %q must use opc.tcp:// scheme", endpoint)
	}
	return validateHostPortEndpoint(parsed.Host, "OPC-UA")
}

func validateOPCUANodeInput(in OPCUANodeInput) (OPCUANodeSpec, error) {
	semantic, boundary, priority, unit, err := validatePointCommonMeta(in.PointCode, in.SemanticChannel, in.SafetyBoundary, in.PollPriority, in.Writable, in.Unit)
	if err != nil {
		return OPCUANodeSpec{}, err
	}
	nodeID, err := ParseOPCUANodeID(in.NodeID)
	if err != nil {
		return OPCUANodeSpec{}, err
	}
	if in.NamespaceIndex != nil {
		if *in.NamespaceIndex < 0 || *in.NamespaceIndex > 65535 {
			return OPCUANodeSpec{}, fmt.Errorf("namespaceIndex %d must be in [0,65535]", *in.NamespaceIndex)
		}
		if uint16(*in.NamespaceIndex) != nodeID.NamespaceIndex {
			return OPCUANodeSpec{}, fmt.Errorf("namespaceIndex %d does not match nodeId namespace %d", *in.NamespaceIndex, nodeID.NamespaceIndex)
		}
	}
	dt, err := parseDriverChannelDataType(in.DataType, DataTypeDouble)
	if err != nil {
		return OPCUANodeSpec{}, err
	}
	scale := in.Scale
	if math.IsNaN(scale) || math.IsInf(scale, 0) {
		return OPCUANodeSpec{}, errors.New("scale must be a finite number")
	}
	if scale == 0 {
		scale = 1
	}
	offset := in.Offset
	if math.IsNaN(offset) || math.IsInf(offset, 0) {
		return OPCUANodeSpec{}, errors.New("offset must be a finite number")
	}
	if offset == 0 {
		offset = 0
	}
	return OPCUANodeSpec{
		PointCode:       strings.TrimSpace(in.PointCode),
		SemanticChannel: semantic,
		SafetyBoundary:  boundary,
		NodeID:          nodeID,
		DataType:        dt,
		Writable:        in.Writable,
		Subscribe:       in.Subscribe,
		Scale:           scale,
		Offset:          offset,
		PollPriority:    priority,
		Unit:            unit,
	}, nil
}

// Clone returns a deep copy of DeclarativeOPCUAProfile.
func (p DeclarativeOPCUAProfile) Clone() DeclarativeOPCUAProfile {
	out := p
	out.Nodes = make([]OPCUANodeSpec, len(p.Nodes))
	for i, node := range p.Nodes {
		copied := node
		if node.NodeID.Opaque != nil {
			copied.NodeID.Opaque = append([]byte(nil), node.NodeID.Opaque...)
		}
		out.Nodes[i] = copied
	}
	return out
}

// ToDriverInput converts a DeclarativeOPCUAProfile back into normalized wire ModbusDriverInput shape.
func (p DeclarativeOPCUAProfile) ToDriverInput(includeGatewayBindings bool) ModbusDriverInput {
	nodes := make([]OPCUANodeInput, len(p.Nodes))
	for i, spec := range p.Nodes {
		ns := int(spec.NodeID.NamespaceIndex)
		nodes[i] = OPCUANodeInput{
			PointCode:       spec.PointCode,
			SemanticChannel: string(spec.SemanticChannel),
			SafetyBoundary:  string(spec.SafetyBoundary),
			NodeID:          spec.NodeID.String(),
			NamespaceIndex:  &ns,
			DataType:        string(spec.DataType),
			Writable:        spec.Writable,
			Subscribe:       spec.Subscribe,
			Scale:           spec.Scale,
			Offset:          spec.Offset,
			PollPriority:    string(spec.PollPriority),
			Unit:            spec.Unit,
		}
	}
	out := ModbusDriverInput{
		DeviceKey:         p.ComponentID,
		DriverType:        DriverTypeOPCUA,
		CapabilityProfile: string(p.CapabilityProfile),
		OPCUA: &OPCUADriverSpecInput{
			SecurityPolicy:         p.SecurityPolicy,
			SecurityMode:           p.SecurityMode,
			AuthMode:               p.AuthMode,
			Username:               p.Username,
			CredentialRef:          p.CredentialRef,
			SubscriptionIntervalMS: p.SubscriptionIntervalMS,
			Nodes:                  nodes,
		},
	}
	if includeGatewayBindings {
		if ep := strings.TrimSpace(p.Endpoint); ep != "" {
			out.Endpoint = &ep
		}
	}
	if p.TimeoutMS > 0 {
		t := p.TimeoutMS
		out.TimeoutMS = &t
	}
	r := p.Retries
	out.Retries = &r
	return out
}

func (p DeclarativeOPCUAProfile) validateWithTransport(hasExternalTransport bool) error {
	if !hasExternalTransport && strings.TrimSpace(p.Endpoint) == "" {
		return errors.New("opcua endpoint is required")
	}
	in := p.ToDriverInput(true)
	if !hasExternalTransport || strings.TrimSpace(p.Endpoint) != "" {
		if _, err := ValidateOPCUADriverInput(in, ModbusDriverValidationOptions{
			RequireDeviceKey:       true,
			RequireGatewayBindings: !hasExternalTransport,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ValidateOPCUAWriteAllowed enforces EDGE-003 before issuing physical OPC-UA node writes.
func ValidateOPCUAWriteAllowed(spec OPCUANodeSpec) error {
	if spec.EffectiveSafetyBoundary() == SafetyBoundaryHardRealtime {
		return fmt.Errorf("%w: point=%s", ErrHardRealtimeSafetyBoundary, spec.PointCode)
	}
	if !spec.Writable {
		return fmt.Errorf("OPC-UA point %s (%s) is read-only", spec.PointCode, spec.NodeID)
	}
	return nil
}
