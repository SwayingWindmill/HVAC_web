package edgecontrol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	BVLCTypeAnnexJ                uint8 = 0x81
	BVLCFuncOriginalUnicastNPDU   uint8 = 0x0A
	BVLCFuncOriginalBroadcastNPDU uint8 = 0x0B

	bacnetNPDUVersion uint8 = 0x01
	npduFlagExpecting uint8 = 0x04
	npduFlagDestSpec  uint8 = 0x20

	BACnetAPDUTypeConfirmedRequest   uint8 = 0x00
	BACnetAPDUTypeUnconfirmedRequest uint8 = 0x10
	BACnetAPDUTypeSimpleACK          uint8 = 0x20
	BACnetAPDUTypeComplexACK         uint8 = 0x30
	BACnetAPDUTypeError              uint8 = 0x50

	BACnetServiceIAm                        uint8 = 0
	BACnetServiceUnconfirmedCOVNotification uint8 = 2
	BACnetServiceWhoIs                      uint8 = 8

	BACnetServiceSubscribeCOV  uint8 = 5
	BACnetServiceReadProperty  uint8 = 12
	BACnetServiceWriteProperty uint8 = 15

	maxBACnetPacketBytes = 2048
)

const (
	bacnetAppTagNull             uint8 = 0
	bacnetAppTagBoolean          uint8 = 1
	bacnetAppTagUnsignedInt      uint8 = 2
	bacnetAppTagSignedInt        uint8 = 3
	bacnetAppTagReal             uint8 = 4
	bacnetAppTagDouble           uint8 = 5
	bacnetAppTagCharacterString  uint8 = 7
	bacnetAppTagEnumerated       uint8 = 9
	bacnetAppTagObjectIdentifier uint8 = 12
)

// BACnetAppValue models a typed ASHRAE 135 application value inside ReadProperty,
// WriteProperty, and COV notifications.
type BACnetAppValue struct {
	Type     DataType
	Null     bool
	Bool     bool
	Unsigned uint64
	Signed   int64
	Real     float32
	Double   float64
	Text     string
	Enum     uint32
	IsEnum   bool
	IsSigned bool
}

// NumericFloat64 extracts numeric content across Unsigned/Signed/Real/Double/Enum/Bool.
func (v BACnetAppValue) NumericFloat64() (float64, bool) {
	switch {
	case v.Null:
		return 0, false
	case v.IsEnum:
		return float64(v.Enum), true
	case v.IsSigned:
		return float64(v.Signed), true
	case v.Type == DataTypeDouble:
		return v.Double, true
	case v.Type == DataTypeFloat:
		return float64(v.Real), true
	case v.Type == DataTypeInteger || v.Type == DataTypeLong:
		return float64(v.Unsigned), true
	case v.Type == DataTypeBoolean:
		if v.Bool {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

// BACnetPacket represents a decoded BACnet/IP BVLC + NPDU + APDU frame.
type BACnetPacket struct {
	BVLCFunction uint8
	NetworkNum   uint16
	PDUType      uint8
	InvokeID     uint8
	Service      uint8
	Payload      []byte
}

// BACnetPropertyValue pairs a property identifier with a BACnet application value.
type BACnetPropertyValue struct {
	PropertyID uint32
	Value      BACnetAppValue
}

// BACnetIAMAnnouncement carries decoded Who-Is discovery response fields.
type BACnetIAMAnnouncement struct {
	DeviceID     BACnetObjectID
	MaxAPDU      uint32
	Segmentation uint32
	VendorID     uint32
}

// BACnetCOVNotification carries decoded UnconfirmedCOVNotification values.
type BACnetCOVNotification struct {
	SubscriberProcessID uint32
	InitiatingDeviceID  BACnetObjectID
	MonitoredObjectID   BACnetObjectID
	TimeRemainingSec    uint32
	Properties          []BACnetPropertyValue
}

// EncodeBACnetPacket wraps NPDU + APDU bytes in a BVLC Annex J datagram.
func EncodeBACnetPacket(pkt BACnetPacket) ([]byte, error) {
	bvlcFunc := pkt.BVLCFunction
	if bvlcFunc == 0 {
		bvlcFunc = BVLCFuncOriginalUnicastNPDU
	}
	var apdu []byte
	switch pkt.PDUType {
	case BACnetAPDUTypeConfirmedRequest:
		apdu = make([]byte, 4+len(pkt.Payload))
		apdu[0] = BACnetAPDUTypeConfirmedRequest
		apdu[1] = 0x05 // Max segments unspecified, max APDU 1476 bytes.
		apdu[2] = pkt.InvokeID
		apdu[3] = pkt.Service
		copy(apdu[4:], pkt.Payload)
	case BACnetAPDUTypeUnconfirmedRequest:
		apdu = make([]byte, 2+len(pkt.Payload))
		apdu[0] = BACnetAPDUTypeUnconfirmedRequest
		apdu[1] = pkt.Service
		copy(apdu[2:], pkt.Payload)
	case BACnetAPDUTypeSimpleACK:
		apdu = []byte{BACnetAPDUTypeSimpleACK, pkt.InvokeID, pkt.Service}
	case BACnetAPDUTypeComplexACK:
		apdu = make([]byte, 3+len(pkt.Payload))
		apdu[0] = BACnetAPDUTypeComplexACK
		apdu[1] = pkt.InvokeID
		apdu[2] = pkt.Service
		copy(apdu[3:], pkt.Payload)
	case BACnetAPDUTypeError:
		apdu = make([]byte, 3+len(pkt.Payload))
		apdu[0] = BACnetAPDUTypeError
		apdu[1] = pkt.InvokeID
		apdu[2] = pkt.Service
		copy(apdu[3:], pkt.Payload)
	default:
		return nil, fmt.Errorf("unsupported BACnet PDU type 0x%02x", pkt.PDUType)
	}

	npdu := []byte{bacnetNPDUVersion, 0x00}
	if pkt.PDUType == BACnetAPDUTypeConfirmedRequest {
		npdu[1] |= npduFlagExpecting
	}
	if pkt.NetworkNum > 0 {
		npdu[1] |= npduFlagDestSpec
		dnet := make([]byte, 4)
		binary.BigEndian.PutUint16(dnet[:2], pkt.NetworkNum)
		dnet[2] = 0x00 // Global broadcast on remote DNET.
		dnet[3] = 0xFF // HopCount 255.
		npdu = append(npdu, dnet...)
	}

	totalLen := 4 + len(npdu) + len(apdu)
	if totalLen > maxBACnetPacketBytes {
		return nil, fmt.Errorf("BACnet packet size %d exceeds %d bytes", totalLen, maxBACnetPacketBytes)
	}
	frame := make([]byte, 4, totalLen)
	frame[0] = BVLCTypeAnnexJ
	frame[1] = bvlcFunc
	binary.BigEndian.PutUint16(frame[2:4], uint16(totalLen))
	frame = append(frame, npdu...)
	frame = append(frame, apdu...)
	return frame, nil
}

// DecodeBACnetPacket parses an Annex J BVLC + NPDU + APDU frame.
func DecodeBACnetPacket(raw []byte) (BACnetPacket, error) {
	if len(raw) < 8 || len(raw) > maxBACnetPacketBytes {
		return BACnetPacket{}, fmt.Errorf("invalid BACnet frame length %d", len(raw))
	}
	if raw[0] != BVLCTypeAnnexJ {
		return BACnetPacket{}, fmt.Errorf("unsupported BVLC type 0x%02x", raw[0])
	}
	declaredLen := int(binary.BigEndian.Uint16(raw[2:4]))
	if declaredLen < 8 || declaredLen > len(raw) {
		return BACnetPacket{}, fmt.Errorf("corrupt BVLC length %d (received %d)", declaredLen, len(raw))
	}
	bvlcFunc := raw[1]
	npdu := raw[4:declaredLen]
	if len(npdu) < 2 || npdu[0] != bacnetNPDUVersion {
		return BACnetPacket{}, errors.New("unsupported BACnet NPDU version")
	}
	ctrl := npdu[1]
	if ctrl&0x80 != 0 {
		return BACnetPacket{}, errors.New("unexpected NPDU network layer message")
	}
	pos := 2
	var netNum uint16
	if ctrl&npduFlagDestSpec != 0 {
		if pos+3 > len(npdu) {
			return BACnetPacket{}, errors.New("truncated NPDU DNET header")
		}
		netNum = binary.BigEndian.Uint16(npdu[pos : pos+2])
		dlen := int(npdu[pos+2])
		pos += 3 + dlen
	}
	if ctrl&0x08 != 0 { // SNET present
		if pos+3 > len(npdu) {
			return BACnetPacket{}, errors.New("truncated NPDU SNET header")
		}
		slen := int(npdu[pos+2])
		pos += 3 + slen
	}
	if ctrl&npduFlagDestSpec != 0 {
		if pos >= len(npdu) {
			return BACnetPacket{}, errors.New("truncated NPDU HopCount")
		}
		pos++
	}
	if pos >= len(npdu) {
		return BACnetPacket{}, errors.New("missing BACnet APDU")
	}
	apdu := npdu[pos:]
	pduType := apdu[0] & 0xF0
	out := BACnetPacket{BVLCFunction: bvlcFunc, NetworkNum: netNum, PDUType: pduType}

	switch pduType {
	case BACnetAPDUTypeConfirmedRequest:
		if len(apdu) < 4 {
			return BACnetPacket{}, errors.New("truncated BACnet Confirmed-Request APDU")
		}
		hdrLen := 4
		if apdu[0]&0x08 != 0 { // Segmented
			hdrLen += 2
		}
		if len(apdu) < hdrLen {
			return BACnetPacket{}, errors.New("truncated segmented Confirmed-Request")
		}
		out.InvokeID = apdu[2]
		out.Service = apdu[hdrLen-1]
		out.Payload = append([]byte(nil), apdu[hdrLen:]...)
	case BACnetAPDUTypeUnconfirmedRequest:
		if len(apdu) < 2 {
			return BACnetPacket{}, errors.New("truncated BACnet Unconfirmed-Request APDU")
		}
		out.Service = apdu[1]
		out.Payload = append([]byte(nil), apdu[2:]...)
	case BACnetAPDUTypeSimpleACK:
		if len(apdu) < 3 {
			return BACnetPacket{}, errors.New("truncated BACnet SimpleACK APDU")
		}
		out.InvokeID = apdu[1]
		out.Service = apdu[2]
	case BACnetAPDUTypeComplexACK, BACnetAPDUTypeError:
		if len(apdu) < 3 {
			return BACnetPacket{}, errors.New("truncated BACnet ComplexACK/Error APDU")
		}
		hdrLen := 3
		if pduType == BACnetAPDUTypeComplexACK && apdu[0]&0x08 != 0 {
			hdrLen += 2
		}
		if len(apdu) < hdrLen {
			return BACnetPacket{}, errors.New("truncated segmented ComplexACK")
		}
		out.InvokeID = apdu[1]
		out.Service = apdu[hdrLen-1]
		out.Payload = append([]byte(nil), apdu[hdrLen:]...)
	default:
		return BACnetPacket{}, fmt.Errorf("unsupported BACnet APDU type 0x%02x", pduType)
	}
	return out, nil
}

// --- Service Payload Encoders & Decoders ---

func EncodeBACnetWhoIsPayload(lowLimit, highLimit *uint32) []byte {
	var out []byte
	if lowLimit != nil && highLimit != nil {
		out = appendBACnetContextUint(out, 0, uint64(*lowLimit))
		out = appendBACnetContextUint(out, 1, uint64(*highLimit))
	}
	return out
}

func DecodeBACnetWhoIsPayload(raw []byte) (low, high *uint32, err error) {
	pos := 0
	for pos < len(raw) {
		tagNum, isCtx, length, isOpen, isClose, nextPos, err := readBACnetTagHeader(raw, pos)
		if err != nil || isOpen || isClose || !isCtx || nextPos+length > len(raw) {
			return nil, nil, errors.New("invalid Who-Is limits")
		}
		val, err := decodeBACnetUnsigned(raw[nextPos : nextPos+length])
		if err != nil {
			return nil, nil, err
		}
		v32 := uint32(val)
		switch tagNum {
		case 0:
			low = &v32
		case 1:
			high = &v32
		}
		pos = nextPos + length
	}
	return low, high, nil
}

func EncodeBACnetIAmPayload(ann BACnetIAMAnnouncement) ([]byte, error) {
	if ann.DeviceID.Type == "" {
		ann.DeviceID.Type = BACnetObjDevice
	}
	var out []byte
	var err error
	out, err = appendBACnetAppObjectID(out, ann.DeviceID)
	if err != nil {
		return nil, err
	}
	out = appendBACnetAppUnsigned(out, uint64(ann.MaxAPDU))
	out = appendBACnetAppEnumerated(out, ann.Segmentation)
	out = appendBACnetAppUnsigned(out, uint64(ann.VendorID))
	return out, nil
}

func DecodeBACnetIAmPayload(raw []byte) (BACnetIAMAnnouncement, error) {
	var out BACnetIAMAnnouncement
	pos := 0
	objID, pos, err := readBACnetAppObjectID(raw, pos)
	if err != nil {
		return out, err
	}
	maxAPDU, pos, err := readBACnetAppUint(raw, pos, bacnetAppTagUnsignedInt)
	if err != nil {
		return out, err
	}
	seg, pos, err := readBACnetAppUint(raw, pos, bacnetAppTagEnumerated)
	if err != nil {
		return out, err
	}
	vendor, _, err := readBACnetAppUint(raw, pos, bacnetAppTagUnsignedInt)
	if err != nil {
		return out, err
	}
	out.DeviceID = objID
	out.MaxAPDU = uint32(maxAPDU)
	out.Segmentation = uint32(seg)
	out.VendorID = uint32(vendor)
	return out, nil
}

func EncodeBACnetReadPropertyRequest(objID BACnetObjectID, propID uint32) ([]byte, error) {
	var out []byte
	var err error
	out, err = appendBACnetContextObjectID(out, 0, objID)
	if err != nil {
		return nil, err
	}
	out = appendBACnetContextUint(out, 1, uint64(propID))
	return out, nil
}

func DecodeBACnetReadPropertyRequest(raw []byte) (BACnetObjectID, uint32, error) {
	pos := 0
	objID, pos, err := readBACnetContextObjectID(raw, pos, 0)
	if err != nil {
		return BACnetObjectID{}, 0, err
	}
	prop, _, err := readBACnetContextUint(raw, pos, 1)
	if err != nil {
		return BACnetObjectID{}, 0, err
	}
	return objID, uint32(prop), nil
}

func EncodeBACnetReadPropertyACK(objID BACnetObjectID, propID uint32, val BACnetAppValue) ([]byte, error) {
	var out []byte
	var err error
	out, err = appendBACnetContextObjectID(out, 0, objID)
	if err != nil {
		return nil, err
	}
	out = appendBACnetContextUint(out, 1, uint64(propID))
	out = append(out, bacnetOpeningTag(3))
	out, err = appendBACnetAppValue(out, val)
	if err != nil {
		return nil, err
	}
	out = append(out, bacnetClosingTag(3))
	return out, nil
}

func DecodeBACnetReadPropertyACK(raw []byte) (BACnetObjectID, uint32, BACnetAppValue, error) {
	pos := 0
	objID, pos, err := readBACnetContextObjectID(raw, pos, 0)
	if err != nil {
		return BACnetObjectID{}, 0, BACnetAppValue{}, err
	}
	prop, pos, err := readBACnetContextUint(raw, pos, 1)
	if err != nil {
		return BACnetObjectID{}, 0, BACnetAppValue{}, err
	}
	pos, err = expectBACnetOpeningTag(raw, pos, 3)
	if err != nil {
		return BACnetObjectID{}, 0, BACnetAppValue{}, err
	}
	val, pos, err := readBACnetAppValue(raw, pos)
	if err != nil {
		return BACnetObjectID{}, 0, BACnetAppValue{}, err
	}
	if _, err := expectBACnetClosingTag(raw, pos, 3); err != nil {
		return BACnetObjectID{}, 0, BACnetAppValue{}, err
	}
	return objID, uint32(prop), val, nil
}

func EncodeBACnetWritePropertyRequest(objID BACnetObjectID, propID uint32, val BACnetAppValue, priority *uint8) ([]byte, error) {
	var out []byte
	var err error
	out, err = appendBACnetContextObjectID(out, 0, objID)
	if err != nil {
		return nil, err
	}
	out = appendBACnetContextUint(out, 1, uint64(propID))
	out = append(out, bacnetOpeningTag(3))
	out, err = appendBACnetAppValue(out, val)
	if err != nil {
		return nil, err
	}
	out = append(out, bacnetClosingTag(3))
	if priority != nil && *priority >= 1 && *priority <= 16 {
		out = appendBACnetContextUint(out, 4, uint64(*priority))
	}
	return out, nil
}

func DecodeBACnetWritePropertyRequest(raw []byte) (BACnetObjectID, uint32, BACnetAppValue, *uint8, error) {
	pos := 0
	objID, pos, err := readBACnetContextObjectID(raw, pos, 0)
	if err != nil {
		return BACnetObjectID{}, 0, BACnetAppValue{}, nil, err
	}
	prop, pos, err := readBACnetContextUint(raw, pos, 1)
	if err != nil {
		return BACnetObjectID{}, 0, BACnetAppValue{}, nil, err
	}
	pos, err = expectBACnetOpeningTag(raw, pos, 3)
	if err != nil {
		return BACnetObjectID{}, 0, BACnetAppValue{}, nil, err
	}
	val, pos, err := readBACnetAppValue(raw, pos)
	if err != nil {
		return BACnetObjectID{}, 0, BACnetAppValue{}, nil, err
	}
	pos, err = expectBACnetClosingTag(raw, pos, 3)
	if err != nil {
		return BACnetObjectID{}, 0, BACnetAppValue{}, nil, err
	}
	var prio *uint8
	if pos < len(raw) {
		pVal, _, err := readBACnetContextUint(raw, pos, 4)
		if err != nil || pVal < 1 || pVal > 16 {
			return BACnetObjectID{}, 0, BACnetAppValue{}, nil, errors.New("invalid WriteProperty priority")
		}
		p := uint8(pVal)
		prio = &p
	}
	return objID, uint32(prop), val, prio, nil
}

func EncodeBACnetSubscribeCOVRequest(procID uint32, objID BACnetObjectID, confirmed bool, lifetimeSec uint32) ([]byte, error) {
	var out []byte
	var err error
	out = appendBACnetContextUint(out, 0, uint64(procID))
	out, err = appendBACnetContextObjectID(out, 1, objID)
	if err != nil {
		return nil, err
	}
	out = appendBACnetContextBool(out, 2, confirmed)
	out = appendBACnetContextUint(out, 3, uint64(lifetimeSec))
	return out, nil
}

func DecodeBACnetSubscribeCOVRequest(raw []byte) (procID uint32, objID BACnetObjectID, confirmed bool, lifetimeSec uint32, err error) {
	pos := 0
	p, pos, err := readBACnetContextUint(raw, pos, 0)
	if err != nil {
		return 0, BACnetObjectID{}, false, 0, err
	}
	objID, pos, err = readBACnetContextObjectID(raw, pos, 1)
	if err != nil {
		return 0, BACnetObjectID{}, false, 0, err
	}
	if pos < len(raw) {
		tagNum, isCtx, length, _, _, nextPos, tErr := readBACnetTagHeader(raw, pos)
		if tErr == nil && isCtx && tagNum == 2 && nextPos+length <= len(raw) && length > 0 {
			confirmed = raw[nextPos] != 0
			pos = nextPos + length
		}
	}
	var life uint64
	if pos < len(raw) {
		l, _, lErr := readBACnetContextUint(raw, pos, 3)
		if lErr != nil {
			return 0, BACnetObjectID{}, false, 0, lErr
		}
		life = l
	}
	return uint32(p), objID, confirmed, uint32(life), nil
}

func EncodeBACnetUnconfirmedCOVNotification(msg BACnetCOVNotification) ([]byte, error) {
	if msg.InitiatingDeviceID.Type == "" {
		msg.InitiatingDeviceID.Type = BACnetObjDevice
	}
	var out []byte
	var err error
	out = appendBACnetContextUint(out, 0, uint64(msg.SubscriberProcessID))
	out, err = appendBACnetContextObjectID(out, 1, msg.InitiatingDeviceID)
	if err != nil {
		return nil, err
	}
	out, err = appendBACnetContextObjectID(out, 2, msg.MonitoredObjectID)
	if err != nil {
		return nil, err
	}
	out = appendBACnetContextUint(out, 3, uint64(msg.TimeRemainingSec))
	out = append(out, bacnetOpeningTag(4))
	for _, pv := range msg.Properties {
		out = appendBACnetContextUint(out, 0, uint64(pv.PropertyID))
		out = append(out, bacnetOpeningTag(2))
		out, err = appendBACnetAppValue(out, pv.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, bacnetClosingTag(2))
	}
	out = append(out, bacnetClosingTag(4))
	return out, nil
}

func DecodeBACnetUnconfirmedCOVNotification(raw []byte) (BACnetCOVNotification, error) {
	var msg BACnetCOVNotification
	pos := 0
	proc, pos, err := readBACnetContextUint(raw, pos, 0)
	if err != nil {
		return msg, err
	}
	devID, pos, err := readBACnetContextObjectID(raw, pos, 1)
	if err != nil {
		return msg, err
	}
	monID, pos, err := readBACnetContextObjectID(raw, pos, 2)
	if err != nil {
		return msg, err
	}
	rem, pos, err := readBACnetContextUint(raw, pos, 3)
	if err != nil {
		return msg, err
	}
	pos, err = expectBACnetOpeningTag(raw, pos, 4)
	if err != nil {
		return msg, err
	}
	for pos < len(raw) {
		if raw[pos] == bacnetClosingTag(4) {
			break
		}
		propID, nextPos, pErr := readBACnetContextUint(raw, pos, 0)
		if pErr != nil {
			return msg, pErr
		}
		nextPos, pErr = expectBACnetOpeningTag(raw, nextPos, 2)
		if pErr != nil {
			return msg, pErr
		}
		val, afterVal, vErr := readBACnetAppValue(raw, nextPos)
		if vErr != nil {
			return msg, vErr
		}
		afterVal, cErr := expectBACnetClosingTag(raw, afterVal, 2)
		if cErr != nil {
			return msg, cErr
		}
		msg.Properties = append(msg.Properties, BACnetPropertyValue{PropertyID: uint32(propID), Value: val})
		pos = afterVal
	}
	msg.SubscriberProcessID = uint32(proc)
	msg.InitiatingDeviceID = devID
	msg.MonitoredObjectID = monID
	msg.TimeRemainingSec = uint32(rem)
	return msg, nil
}

func EncodeBACnetErrorPayload(errClass, errCode uint32) []byte {
	var out []byte
	out = appendBACnetAppEnumerated(out, errClass)
	out = appendBACnetAppEnumerated(out, errCode)
	return out
}

func DecodeBACnetErrorPayload(raw []byte) (errClass, errCode uint32, err error) {
	c, pos, err := readBACnetAppUint(raw, 0, bacnetAppTagEnumerated)
	if err != nil {
		return 0, 0, err
	}
	code, _, err := readBACnetAppUint(raw, pos, bacnetAppTagEnumerated)
	if err != nil {
		return 0, 0, err
	}
	return uint32(c), uint32(code), nil
}

// --- ASN.1 Primitive Tag Helpers ---

func bacnetOpeningTag(tag uint8) byte { return (tag << 4) | 0x0E }
func bacnetClosingTag(tag uint8) byte { return (tag << 4) | 0x0F }

func appendBACnetTagHeader(dst []byte, tagNum uint8, isCtx bool, length int) []byte {
	var b byte
	if tagNum < 15 {
		b |= tagNum << 4
	} else {
		b |= 0xF0
	}
	if isCtx {
		b |= 0x08
	}
	switch {
	case length <= 4:
		b |= byte(length)
		dst = append(dst, b)
		if tagNum >= 15 {
			dst = append(dst, tagNum)
		}
	case length <= 253:
		b |= 0x05
		dst = append(dst, b)
		if tagNum >= 15 {
			dst = append(dst, tagNum)
		}
		dst = append(dst, byte(length))
	default:
		b |= 0x05
		dst = append(dst, b)
		if tagNum >= 15 {
			dst = append(dst, tagNum)
		}
		dst = append(dst, 254, byte(length>>8), byte(length))
	}
	return dst
}

func readBACnetTagHeader(raw []byte, pos int) (tagNum uint8, isCtx bool, length int, isOpen bool, isClose bool, nextPos int, err error) {
	if pos >= len(raw) {
		return 0, false, 0, false, false, pos, errors.New("unexpected end of BACnet ASN.1 stream")
	}
	hdr := raw[pos]
	pos++
	tagNum = hdr >> 4
	isCtx = hdr&0x08 != 0
	lvt := hdr & 0x07
	if tagNum == 0x0F {
		if pos >= len(raw) {
			return 0, false, 0, false, false, pos, errors.New("truncated extended BACnet tag number")
		}
		tagNum = raw[pos]
		pos++
	}
	if isCtx && lvt == 6 {
		return tagNum, true, 0, true, false, pos, nil
	}
	if isCtx && lvt == 7 {
		return tagNum, true, 0, false, true, pos, nil
	}
	if lvt < 5 {
		return tagNum, isCtx, int(lvt), false, false, pos, nil
	}
	if pos >= len(raw) {
		return 0, false, 0, false, false, pos, errors.New("truncated extended BACnet tag length")
	}
	ext := raw[pos]
	pos++
	switch {
	case ext < 254:
		return tagNum, isCtx, int(ext), false, false, pos, nil
	case ext == 254:
		if pos+2 > len(raw) {
			return 0, false, 0, false, false, pos, errors.New("truncated uint16 BACnet length")
		}
		l := int(binary.BigEndian.Uint16(raw[pos : pos+2]))
		return tagNum, isCtx, l, false, false, pos + 2, nil
	default:
		return 0, false, 0, false, false, pos, errors.New("unsupported 32-bit BACnet extended length")
	}
}

func expectBACnetOpeningTag(raw []byte, pos int, wantTag uint8) (int, error) {
	tag, isCtx, _, isOpen, _, nextPos, err := readBACnetTagHeader(raw, pos)
	if err != nil {
		return pos, err
	}
	if !isCtx || !isOpen || tag != wantTag {
		return pos, fmt.Errorf("expected BACnet opening tag [%d]", wantTag)
	}
	return nextPos, nil
}

func expectBACnetClosingTag(raw []byte, pos int, wantTag uint8) (int, error) {
	tag, isCtx, _, _, isClose, nextPos, err := readBACnetTagHeader(raw, pos)
	if err != nil {
		return pos, err
	}
	if !isCtx || !isClose || tag != wantTag {
		return pos, fmt.Errorf("expected BACnet closing tag [/%d]", wantTag)
	}
	return nextPos, nil
}

func encodeBACnetUnsignedBytes(v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	idx := 0
	for idx < 7 && buf[idx] == 0 {
		idx++
	}
	return buf[idx:]
}

func decodeBACnetUnsigned(b []byte) (uint64, error) {
	if len(b) == 0 || len(b) > 8 {
		return 0, fmt.Errorf("invalid BACnet unsigned length %d", len(b))
	}
	var out uint64
	for _, x := range b {
		out = (out << 8) | uint64(x)
	}
	return out, nil
}

func decodeBACnetSigned(b []byte) (int64, error) {
	if len(b) == 0 || len(b) > 8 {
		return 0, fmt.Errorf("invalid BACnet signed length %d", len(b))
	}
	var u uint64
	if b[0]&0x80 != 0 {
		u = ^uint64(0)
	}
	for _, x := range b {
		u = (u << 8) | uint64(x)
	}
	return int64(u), nil
}

func encodeBACnetSignedBytes(v int64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(v))
	idx := 0
	for idx < 7 {
		if (buf[idx] == 0x00 && buf[idx+1]&0x80 == 0) || (buf[idx] == 0xFF && buf[idx+1]&0x80 != 0) {
			idx++
			continue
		}
		break
	}
	return buf[idx:]
}

func appendBACnetAppUnsigned(dst []byte, v uint64) []byte {
	b := encodeBACnetUnsignedBytes(v)
	dst = appendBACnetTagHeader(dst, bacnetAppTagUnsignedInt, false, len(b))
	return append(dst, b...)
}

func appendBACnetAppEnumerated(dst []byte, v uint32) []byte {
	b := encodeBACnetUnsignedBytes(uint64(v))
	dst = appendBACnetTagHeader(dst, bacnetAppTagEnumerated, false, len(b))
	return append(dst, b...)
}

func appendBACnetContextUint(dst []byte, tagNum uint8, v uint64) []byte {
	b := encodeBACnetUnsignedBytes(v)
	dst = appendBACnetTagHeader(dst, tagNum, true, len(b))
	return append(dst, b...)
}

func appendBACnetContextBool(dst []byte, tagNum uint8, v bool) []byte {
	dst = appendBACnetTagHeader(dst, tagNum, true, 1)
	if v {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func encodeBACnetObjectIDBytes(id BACnetObjectID) ([4]byte, error) {
	var b [4]byte
	code := id.Type.Code()
	if code == 0xFFFF {
		return b, fmt.Errorf("unsupported BACnet objectType %q", id.Type)
	}
	if id.Instance > maxBACnetObjectInstance {
		return b, fmt.Errorf("BACnet objectInstance %d out of range", id.Instance)
	}
	raw := (uint32(code) << 22) | (id.Instance & 0x3FFFFF)
	binary.BigEndian.PutUint32(b[:], raw)
	return b, nil
}

func decodeBACnetObjectIDBytes(b []byte) (BACnetObjectID, error) {
	if len(b) != 4 {
		return BACnetObjectID{}, fmt.Errorf("BACnet ObjectIdentifier requires 4 bytes, got %d", len(b))
	}
	raw := binary.BigEndian.Uint32(b)
	code := uint16(raw >> 22)
	inst := raw & 0x3FFFFF
	objType, ok := BACnetObjectTypeFromCode(code)
	if !ok {
		return BACnetObjectID{}, fmt.Errorf("unknown BACnet wire object type code %d", code)
	}
	return BACnetObjectID{Type: objType, Instance: inst}, nil
}

func appendBACnetAppObjectID(dst []byte, id BACnetObjectID) ([]byte, error) {
	b, err := encodeBACnetObjectIDBytes(id)
	if err != nil {
		return nil, err
	}
	dst = appendBACnetTagHeader(dst, bacnetAppTagObjectIdentifier, false, 4)
	return append(dst, b[:]...), nil
}

func appendBACnetContextObjectID(dst []byte, tagNum uint8, id BACnetObjectID) ([]byte, error) {
	b, err := encodeBACnetObjectIDBytes(id)
	if err != nil {
		return nil, err
	}
	dst = appendBACnetTagHeader(dst, tagNum, true, 4)
	return append(dst, b[:]...), nil
}

func appendBACnetAppValue(dst []byte, val BACnetAppValue) ([]byte, error) {
	switch {
	case val.Null:
		return append(dst, 0x00), nil
	case val.IsEnum:
		return appendBACnetAppEnumerated(dst, val.Enum), nil
	case val.IsSigned:
		b := encodeBACnetSignedBytes(val.Signed)
		dst = appendBACnetTagHeader(dst, bacnetAppTagSignedInt, false, len(b))
		return append(dst, b...), nil
	case val.Type == DataTypeBoolean:
		if val.Bool {
			return append(dst, (bacnetAppTagBoolean<<4)|0x01), nil
		}
		return append(dst, bacnetAppTagBoolean<<4), nil
	case val.Type == DataTypeFloat:
		dst = appendBACnetTagHeader(dst, bacnetAppTagReal, false, 4)
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], math.Float32bits(val.Real))
		return append(dst, b[:]...), nil
	case val.Type == DataTypeDouble:
		dst = appendBACnetTagHeader(dst, bacnetAppTagDouble, false, 8)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], math.Float64bits(val.Double))
		return append(dst, b[:]...), nil
	case val.Type == DataTypeString:
		payload := append([]byte{0x00}, val.Text...)
		dst = appendBACnetTagHeader(dst, bacnetAppTagCharacterString, false, len(payload))
		return append(dst, payload...), nil
	case val.Type == DataTypeInteger || val.Type == DataTypeLong:
		return appendBACnetAppUnsigned(dst, val.Unsigned), nil
	default:
		return nil, fmt.Errorf("unsupported BACnet value type %q", val.Type)
	}
}

func readBACnetAppValue(raw []byte, pos int) (BACnetAppValue, int, error) {
	tagNum, isCtx, length, isOpen, isClose, nextPos, err := readBACnetTagHeader(raw, pos)
	if err != nil {
		return BACnetAppValue{}, pos, err
	}
	if isCtx || isOpen || isClose {
		return BACnetAppValue{}, pos, fmt.Errorf("expected BACnet application tag, got ctx=%v", isCtx)
	}
	if tagNum == bacnetAppTagBoolean {
		return BACnetAppValue{Type: DataTypeBoolean, Bool: length != 0}, nextPos, nil
	}
	if tagNum == bacnetAppTagNull {
		return BACnetAppValue{Null: true}, nextPos, nil
	}
	if nextPos+length > len(raw) {
		return BACnetAppValue{}, pos, errors.New("truncated BACnet application payload")
	}
	payload := raw[nextPos : nextPos+length]
	end := nextPos + length
	switch tagNum {
	case bacnetAppTagUnsignedInt:
		u, err := decodeBACnetUnsigned(payload)
		return BACnetAppValue{Type: DataTypeInteger, Unsigned: u}, end, err
	case bacnetAppTagSignedInt:
		s, err := decodeBACnetSigned(payload)
		return BACnetAppValue{Type: DataTypeInteger, Signed: s, IsSigned: true}, end, err
	case bacnetAppTagReal:
		if len(payload) != 4 {
			return BACnetAppValue{}, pos, errors.New("BACnet Real requires 4 bytes")
		}
		f := math.Float32frombits(binary.BigEndian.Uint32(payload))
		return BACnetAppValue{Type: DataTypeFloat, Real: f}, end, nil
	case bacnetAppTagDouble:
		if len(payload) != 8 {
			return BACnetAppValue{}, pos, errors.New("BACnet Double requires 8 bytes")
		}
		d := math.Float64frombits(binary.BigEndian.Uint64(payload))
		return BACnetAppValue{Type: DataTypeDouble, Double: d}, end, nil
	case bacnetAppTagEnumerated:
		u, err := decodeBACnetUnsigned(payload)
		return BACnetAppValue{Type: DataTypeInteger, Enum: uint32(u), IsEnum: true}, end, err
	case bacnetAppTagCharacterString:
		if len(payload) == 0 {
			return BACnetAppValue{Type: DataTypeString, Text: ""}, end, nil
		}
		return BACnetAppValue{Type: DataTypeString, Text: string(payload[1:])}, end, nil
	default:
		return BACnetAppValue{}, pos, fmt.Errorf("unsupported BACnet application tag %d", tagNum)
	}
}

func readBACnetAppObjectID(raw []byte, pos int) (BACnetObjectID, int, error) {
	tag, isCtx, length, _, _, nextPos, err := readBACnetTagHeader(raw, pos)
	if err != nil || isCtx || tag != bacnetAppTagObjectIdentifier || nextPos+length > len(raw) {
		return BACnetObjectID{}, pos, errors.New("expected BACnet ObjectIdentifier")
	}
	id, err := decodeBACnetObjectIDBytes(raw[nextPos : nextPos+length])
	return id, nextPos + length, err
}

func readBACnetContextObjectID(raw []byte, pos int, wantTag uint8) (BACnetObjectID, int, error) {
	tag, isCtx, length, _, _, nextPos, err := readBACnetTagHeader(raw, pos)
	if err != nil || !isCtx || tag != wantTag || nextPos+length > len(raw) {
		return BACnetObjectID{}, pos, fmt.Errorf("expected context BACnet ObjectIdentifier [%d]", wantTag)
	}
	id, err := decodeBACnetObjectIDBytes(raw[nextPos : nextPos+length])
	return id, nextPos + length, err
}

func readBACnetAppUint(raw []byte, pos int, wantTag uint8) (uint64, int, error) {
	tag, isCtx, length, _, _, nextPos, err := readBACnetTagHeader(raw, pos)
	if err != nil || isCtx || tag != wantTag || nextPos+length > len(raw) {
		return 0, pos, fmt.Errorf("expected BACnet unsigned/enum tag %d", wantTag)
	}
	u, err := decodeBACnetUnsigned(raw[nextPos : nextPos+length])
	return u, nextPos + length, err
}

func readBACnetContextUint(raw []byte, pos int, wantTag uint8) (uint64, int, error) {
	tag, isCtx, length, _, _, nextPos, err := readBACnetTagHeader(raw, pos)
	if err != nil || !isCtx || tag != wantTag || nextPos+length > len(raw) {
		return 0, pos, fmt.Errorf("expected BACnet context uint [%d]", wantTag)
	}
	u, err := decodeBACnetUnsigned(raw[nextPos : nextPos+length])
	return u, nextPos + length, err
}
