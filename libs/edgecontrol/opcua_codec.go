package edgecontrol

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

const (
	MaxOPCUAChunkBytes = 1 << 20

	OPCUAMsgHello       = "HEL"
	OPCUAMsgAcknowledge = "ACK"
	OPCUAMsgError       = "ERR"
	OPCUAMsgOpenChannel = "OPN"
	OPCUAMsgClose       = "CLO"
	OPCUAMsgMessage     = "MSG"

	OPCUASecurityPolicyNoneURI           = "http://opcfoundation.org/UA/SecurityPolicy#None"
	OPCUASecurityPolicyBasic256Sha256URI = "http://opcfoundation.org/UA/SecurityPolicy#Basic256Sha256"

	OPCUAServiceOpenSecureChannelReq    uint32 = 446
	OPCUAServiceOpenSecureChannelResp   uint32 = 449
	OPCUAServiceCreateSessionReq        uint32 = 461
	OPCUAServiceCreateSessionResp       uint32 = 464
	OPCUAServiceActivateSessionReq      uint32 = 467
	OPCUAServiceActivateSessionResp     uint32 = 470
	OPCUAServiceCloseSessionReq         uint32 = 473
	OPCUAServiceCloseSessionResp        uint32 = 476
	OPCUAServiceReadReq                 uint32 = 631
	OPCUAServiceReadResp                uint32 = 634
	OPCUAServiceWriteReq                uint32 = 673
	OPCUAServiceWriteResp               uint32 = 676
	OPCUAServiceCreateMonitoredItemsReq uint32 = 751
	OPCUAServiceCreateMonitoredItemsRsp uint32 = 754
	OPCUAServiceCreateSubscriptionReq   uint32 = 787
	OPCUAServiceCreateSubscriptionResp  uint32 = 790
	OPCUAServicePublishReq              uint32 = 826
	OPCUAServicePublishResp             uint32 = 829

	OPCUAStatusGood       uint32 = 0x00000000
	OPCUAStatusBadTimeout uint32 = 0x800A0000
	OPCUAStatusBadNodeID  uint32 = 0x80340000
	OPCUAStatusBadAccess  uint32 = 0x803B0000
	OPCUAStatusBadSec     uint32 = 0x80540000

	opcuaMacSize      = 32
	opcuaFiletimeUnix = int64(116444736000000000) // 100ns ticks between 1601-01-01 and 1970-01-01
)

// OPCUACryptoKeys stores symmetric keys derived for Basic256Sha256 Sign / SignAndEncrypt.
type OPCUACryptoKeys struct {
	SigningKey [32]byte
	EncryptKey [32]byte
	IV         [16]byte
}

// DeriveOPCUABasic256Sha256Keys implements OPC-UA Part 6 P_SHA256 symmetric key derivation.
func DeriveOPCUABasic256Sha256Keys(clientNonce, serverNonce []byte) (clientKeys, serverKeys OPCUACryptoKeys) {
	cRaw := opcuaPSHA256(serverNonce, clientNonce, 80)
	sRaw := opcuaPSHA256(clientNonce, serverNonce, 80)
	copy(clientKeys.SigningKey[:], cRaw[0:32])
	copy(clientKeys.EncryptKey[:], cRaw[32:64])
	copy(clientKeys.IV[:], cRaw[64:80])
	copy(serverKeys.SigningKey[:], sRaw[0:32])
	copy(serverKeys.EncryptKey[:], sRaw[32:64])
	copy(serverKeys.IV[:], sRaw[64:80])
	return clientKeys, serverKeys
}

func opcuaPSHA256(secret, seed []byte, outLen int) []byte {
	out := make([]byte, 0, outLen)
	a := append([]byte(nil), seed...)
	for len(out) < outLen {
		mac := hmac.New(sha256.New, secret)
		mac.Write(a)
		a = mac.Sum(nil)

		mac2 := hmac.New(sha256.New, secret)
		mac2.Write(a)
		mac2.Write(seed)
		out = append(out, mac2.Sum(nil)...)
	}
	return out[:outLen]
}

// OPCUASecurityModeCode maps normalized SecurityMode string to IEC 62541 MessageSecurityMode enum.
func OPCUASecurityModeCode(mode string) uint32 {
	switch mode {
	case OPCUASecurityModeSign:
		return 2
	case OPCUASecurityModeSignAndEncrypt:
		return 3
	default:
		return 1
	}
}

// OPCUASecurityPolicyURI maps policy constant to IEC 62541 URI.
func OPCUASecurityPolicyURI(policy string) string {
	if policy == OPCUASecurityPolicyBasic256Sha256 {
		return OPCUASecurityPolicyBasic256Sha256URI
	}
	return OPCUASecurityPolicyNoneURI
}

// --- Framing I/O ---

func ReadOPCUAFrame(r io.Reader) (msgType string, frame []byte, err error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", nil, err
	}
	msgType = string(hdr[0:3])
	size := int(binary.LittleEndian.Uint32(hdr[4:8]))
	if size < 8 || size > MaxOPCUAChunkBytes {
		return "", nil, fmt.Errorf("OPC-UA frame size %d out of bounds [8,%d]", size, MaxOPCUAChunkBytes)
	}
	frame = make([]byte, size)
	copy(frame[:8], hdr[:])
	if size > 8 {
		if _, err := io.ReadFull(r, frame[8:]); err != nil {
			return "", nil, err
		}
	}
	return msgType, frame, nil
}

// EncodeOPCUAHello builds an IEC 62541 HEL packet.
func EncodeOPCUAHello(endpointURL string) []byte {
	var body []byte
	body = appendUInt32(body, 0)     // ProtocolVersion
	body = appendUInt32(body, 65536) // ReceiveBufferSize
	body = appendUInt32(body, 65536) // SendBufferSize
	body = appendUInt32(body, MaxOPCUAChunkBytes)
	body = appendUInt32(body, 16) // MaxChunkCount
	body = appendUAString(body, endpointURL)
	return wrapOPCUAHeader(OPCUAMsgHello, body)
}

func DecodeOPCUAHello(frame []byte) (endpointURL string, err error) {
	if len(frame) < 28 || string(frame[:3]) != OPCUAMsgHello {
		return "", errors.New("invalid OPC-UA HEL frame")
	}
	ep, _, err := readUAString(frame, 28)
	return ep, err
}

func EncodeOPCUAAcknowledge() []byte {
	var body []byte
	body = appendUInt32(body, 0)
	body = appendUInt32(body, 65536)
	body = appendUInt32(body, 65536)
	body = appendUInt32(body, MaxOPCUAChunkBytes)
	body = appendUInt32(body, 16)
	return wrapOPCUAHeader(OPCUAMsgAcknowledge, body)
}

func DecodeOPCUAAcknowledge(frame []byte) error {
	if len(frame) < 28 || string(frame[:3]) != OPCUAMsgAcknowledge {
		return errors.New("invalid OPC-UA ACK frame")
	}
	return nil
}

func EncodeOPCUAErrorFrame(statusCode uint32, reason string) []byte {
	var body []byte
	body = appendUInt32(body, statusCode)
	body = appendUAString(body, reason)
	return wrapOPCUAHeader(OPCUAMsgError, body)
}

func DecodeOPCUAErrorFrame(frame []byte) (uint32, string, error) {
	if len(frame) < 16 || string(frame[:3]) != OPCUAMsgError {
		return 0, "", errors.New("invalid OPC-UA ERR frame")
	}
	code := binary.LittleEndian.Uint32(frame[8:12])
	reason, _, _ := readUAString(frame, 12)
	return code, reason, nil
}

func wrapOPCUAHeader(msgType string, body []byte) []byte {
	total := 8 + len(body)
	frame := make([]byte, 8, total)
	copy(frame[0:3], msgType)
	frame[3] = 'F'
	binary.LittleEndian.PutUint32(frame[4:8], uint32(total))
	return append(frame, body...)
}

// --- OPN Frame Encoding ---

type OPCUAOpenChannelFrame struct {
	SecureChannelID uint32
	SecurityPolicy  string
	SeqNum          uint32
	RequestID       uint32
	ServiceID       uint32
	SecurityMode    uint32
	ClientNonce     []byte
	ServerNonce     []byte
	TokenChannelID  uint32
	TokenID         uint32
	RevisedLifetime uint32
}

func EncodeOPCUAOpenRequest(f OPCUAOpenChannelFrame) []byte {
	var body []byte
	body = appendUInt32(body, f.SecureChannelID)
	body = appendUAString(body, OPCUASecurityPolicyURI(f.SecurityPolicy))
	body = appendUAByteString(body, nil) // SenderCertificate (symmetric PSK/nonce path)
	body = appendUAByteString(body, nil) // ReceiverThumbprint
	body = appendUInt32(body, f.SeqNum)
	body = appendUInt32(body, f.RequestID)
	body = appendExpandedNumericNodeID(body, OPCUAServiceOpenSecureChannelReq)
	body = appendUInt32(body, 0) // ClientProtocolVersion
	body = appendUInt32(body, 0) // SecurityTokenRequestType: Issue
	body = appendUInt32(body, f.SecurityMode)
	body = appendUAByteString(body, f.ClientNonce)
	body = appendUInt32(body, 3600000)
	return wrapOPCUAHeader(OPCUAMsgOpenChannel, body)
}

func DecodeOPCUAOpenRequest(frame []byte) (OPCUAOpenChannelFrame, error) {
	var out OPCUAOpenChannelFrame
	if len(frame) < 28 || string(frame[:3]) != OPCUAMsgOpenChannel {
		return out, errors.New("invalid OPC-UA OPN request")
	}
	out.SecureChannelID = binary.LittleEndian.Uint32(frame[8:12])
	pos := 12
	polURI, pos, err := readUAString(frame, pos)
	if err != nil {
		return out, err
	}
	if polURI == OPCUASecurityPolicyBasic256Sha256URI {
		out.SecurityPolicy = OPCUASecurityPolicyBasic256Sha256
	} else {
		out.SecurityPolicy = OPCUASecurityPolicyNone
	}
	_, pos, err = readUAByteString(frame, pos)
	if err != nil {
		return out, err
	}
	_, pos, err = readUAByteString(frame, pos)
	if err != nil {
		return out, err
	}
	if pos+12 > len(frame) {
		return out, errors.New("truncated OPN sequence header")
	}
	out.SeqNum = binary.LittleEndian.Uint32(frame[pos : pos+4])
	out.RequestID = binary.LittleEndian.Uint32(frame[pos+4 : pos+8])
	pos += 8
	svc, pos, err := readExpandedNumericNodeID(frame, pos)
	if err != nil || svc != OPCUAServiceOpenSecureChannelReq || pos+12 > len(frame) {
		return out, errors.New("unexpected OPN service node")
	}
	pos += 8 // ClientProtocolVersion + RequestType
	out.SecurityMode = binary.LittleEndian.Uint32(frame[pos : pos+4])
	pos += 4
	nonce, _, err := readUAByteString(frame, pos)
	if err != nil {
		return out, err
	}
	out.ClientNonce = nonce
	return out, nil
}

func EncodeOPCUAOpenResponse(f OPCUAOpenChannelFrame) []byte {
	var body []byte
	body = appendUInt32(body, f.TokenChannelID)
	body = appendUAString(body, OPCUASecurityPolicyURI(f.SecurityPolicy))
	body = appendUAByteString(body, nil)
	body = appendUAByteString(body, nil)
	body = appendUInt32(body, f.SeqNum)
	body = appendUInt32(body, f.RequestID)
	body = appendExpandedNumericNodeID(body, OPCUAServiceOpenSecureChannelResp)
	body = appendUInt32(body, 0) // ServerProtocolVersion
	body = appendUInt32(body, f.TokenChannelID)
	body = appendUInt32(body, f.TokenID)
	body = appendUInt64(body, uint64(time.Now().UnixNano()/100+opcuaFiletimeUnix))
	body = appendUInt32(body, f.RevisedLifetime)
	body = appendUAByteString(body, f.ServerNonce)
	return wrapOPCUAHeader(OPCUAMsgOpenChannel, body)
}

func DecodeOPCUAOpenResponse(frame []byte) (OPCUAOpenChannelFrame, error) {
	var out OPCUAOpenChannelFrame
	if len(frame) < 28 || string(frame[:3]) != OPCUAMsgOpenChannel {
		return out, errors.New("invalid OPC-UA OPN response")
	}
	out.SecureChannelID = binary.LittleEndian.Uint32(frame[8:12])
	pos := 12
	polURI, pos, err := readUAString(frame, pos)
	if err != nil {
		return out, err
	}
	if polURI == OPCUASecurityPolicyBasic256Sha256URI {
		out.SecurityPolicy = OPCUASecurityPolicyBasic256Sha256
	} else {
		out.SecurityPolicy = OPCUASecurityPolicyNone
	}
	_, pos, err = readUAByteString(frame, pos)
	if err != nil {
		return out, err
	}
	_, pos, err = readUAByteString(frame, pos)
	if err != nil {
		return out, err
	}
	if pos+8 > len(frame) {
		return out, errors.New("truncated OPN response seq header")
	}
	out.SeqNum = binary.LittleEndian.Uint32(frame[pos : pos+4])
	out.RequestID = binary.LittleEndian.Uint32(frame[pos+4 : pos+8])
	pos += 8
	svc, pos, err := readExpandedNumericNodeID(frame, pos)
	if err != nil || svc != OPCUAServiceOpenSecureChannelResp || pos+24 > len(frame) {
		return out, errors.New("unexpected OPN response service")
	}
	pos += 4 // ServerProtocolVersion
	out.TokenChannelID = binary.LittleEndian.Uint32(frame[pos : pos+4])
	out.TokenID = binary.LittleEndian.Uint32(frame[pos+4 : pos+8])
	out.RevisedLifetime = binary.LittleEndian.Uint32(frame[pos+16 : pos+20])
	pos += 20
	out.ServerNonce, _, err = readUAByteString(frame, pos)
	return out, err
}

// --- Symmetric Secure Message Encoding / Decoding ---

func EncodeOPCUASecureMessage(msgType string, channelID, tokenID, seqNum, reqID uint32, body []byte, secMode string, keys *OPCUACryptoKeys) ([]byte, error) {
	var plain []byte
	plain = appendUInt32(plain, seqNum)
	plain = appendUInt32(plain, reqID)
	plain = append(plain, body...)

	if secMode == OPCUASecurityModeNone || secMode == "" {
		hdr := make([]byte, 16)
		copy(hdr[0:3], msgType)
		hdr[3] = 'F'
		binary.LittleEndian.PutUint32(hdr[4:8], uint32(16+len(plain)))
		binary.LittleEndian.PutUint32(hdr[8:12], channelID)
		binary.LittleEndian.PutUint32(hdr[12:16], tokenID)
		return append(hdr, plain...), nil
	}
	if keys == nil {
		return nil, errors.New("Basic256Sha256 keys are required")
	}

	if secMode == OPCUASecurityModeSign {
		totalLen := 16 + len(plain) + opcuaMacSize
		unsigned := make([]byte, 16+len(plain))
		copy(unsigned[0:3], msgType)
		unsigned[3] = 'F'
		binary.LittleEndian.PutUint32(unsigned[4:8], uint32(totalLen))
		binary.LittleEndian.PutUint32(unsigned[8:12], channelID)
		binary.LittleEndian.PutUint32(unsigned[12:16], tokenID)
		copy(unsigned[16:], plain)
		mac := hmac.New(sha256.New, keys.SigningKey[:])
		mac.Write(unsigned)
		return append(unsigned, mac.Sum(nil)...), nil
	}

	if secMode != OPCUASecurityModeSignAndEncrypt {
		return nil, fmt.Errorf("unsupported OPC-UA securityMode %q", secMode)
	}

	// OPC-UA Part 6 padding before signing and AES-256-CBC encryption:
	// encryptedPayload = seqHdr(8) + body + padBytes + sig(32) must be multiple of 16 bytes.
	rem := (len(plain) + opcuaMacSize + 1) % aes.BlockSize
	padCount := 1
	if rem != 0 {
		padCount += aes.BlockSize - rem
	}
	padByte := byte(padCount - 1)
	paddedPlain := make([]byte, len(plain), len(plain)+padCount)
	copy(paddedPlain, plain)
	for i := 0; i < padCount; i++ {
		paddedPlain = append(paddedPlain, padByte)
	}

	totalLen := 16 + len(paddedPlain) + opcuaMacSize
	hdr := make([]byte, 16)
	copy(hdr[0:3], msgType)
	hdr[3] = 'F'
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(totalLen))
	binary.LittleEndian.PutUint32(hdr[8:12], channelID)
	binary.LittleEndian.PutUint32(hdr[12:16], tokenID)

	mac := hmac.New(sha256.New, keys.SigningKey[:])
	mac.Write(hdr)
	mac.Write(paddedPlain)
	sig := mac.Sum(nil)

	toEncrypt := append(paddedPlain, sig...)
	blk, err := aes.NewCipher(keys.EncryptKey[:])
	if err != nil {
		return nil, err
	}
	encrypted := make([]byte, len(toEncrypt))
	cipher.NewCBCEncrypter(blk, keys.IV[:]).CryptBlocks(encrypted, toEncrypt)
	return append(hdr, encrypted...), nil
}

func DecodeOPCUASecureMessage(frame []byte, secMode string, keys *OPCUACryptoKeys) (channelID, tokenID, seqNum, reqID uint32, body []byte, err error) {
	if len(frame) < 24 {
		return 0, 0, 0, 0, nil, errors.New("truncated OPC-UA secure frame")
	}
	channelID = binary.LittleEndian.Uint32(frame[8:12])
	tokenID = binary.LittleEndian.Uint32(frame[12:16])

	var plain []byte
	switch secMode {
	case "", OPCUASecurityModeNone:
		plain = frame[16:]
	case OPCUASecurityModeSign:
		if keys == nil || len(frame) < 24+opcuaMacSize {
			return 0, 0, 0, 0, nil, errors.New("missing keys or truncated signed OPC-UA frame")
		}
		signedPart := frame[:len(frame)-opcuaMacSize]
		sig := frame[len(frame)-opcuaMacSize:]
		mac := hmac.New(sha256.New, keys.SigningKey[:])
		mac.Write(signedPart)
		if !hmac.Equal(sig, mac.Sum(nil)) {
			return 0, 0, 0, 0, nil, errors.New("OPC-UA HMAC-SHA256 verification failed")
		}
		plain = signedPart[16:]
	case OPCUASecurityModeSignAndEncrypt:
		if keys == nil || len(frame) < 16+aes.BlockSize {
			return 0, 0, 0, 0, nil, errors.New("missing keys or truncated encrypted OPC-UA frame")
		}
		enc := frame[16:]
		if len(enc)%aes.BlockSize != 0 || len(enc) < opcuaMacSize+aes.BlockSize {
			return 0, 0, 0, 0, nil, errors.New("invalid AES-256-CBC ciphertext block alignment")
		}
		blk, cErr := aes.NewCipher(keys.EncryptKey[:])
		if cErr != nil {
			return 0, 0, 0, 0, nil, cErr
		}
		dec := make([]byte, len(enc))
		cipher.NewCBCDecrypter(blk, keys.IV[:]).CryptBlocks(dec, enc)

		dataAndPad := dec[:len(dec)-opcuaMacSize]
		sig := dec[len(dec)-opcuaMacSize:]
		mac := hmac.New(sha256.New, keys.SigningKey[:])
		mac.Write(frame[:16])
		mac.Write(dataAndPad)
		if !hmac.Equal(sig, mac.Sum(nil)) {
			return 0, 0, 0, 0, nil, errors.New("OPC-UA encrypted HMAC-SHA256 verification failed")
		}
		if len(dataAndPad) < 9 {
			return 0, 0, 0, 0, nil, errors.New("truncated decrypted OPC-UA payload")
		}
		padVal := int(dataAndPad[len(dataAndPad)-1])
		padCount := padVal + 1
		if padCount < 1 || padCount > aes.BlockSize || padCount > len(dataAndPad)-8 {
			return 0, 0, 0, 0, nil, errors.New("invalid OPC-UA symmetric padding")
		}
		for i := 0; i < padCount; i++ {
			if dataAndPad[len(dataAndPad)-1-i] != byte(padVal) {
				return 0, 0, 0, 0, nil, errors.New("corrupt OPC-UA symmetric padding byte")
			}
		}
		plain = dataAndPad[:len(dataAndPad)-padCount]
	default:
		return 0, 0, 0, 0, nil, fmt.Errorf("unsupported OPC-UA securityMode %q", secMode)
	}

	if len(plain) < 8 {
		return 0, 0, 0, 0, nil, errors.New("missing OPC-UA sequence header")
	}
	seqNum = binary.LittleEndian.Uint32(plain[0:4])
	reqID = binary.LittleEndian.Uint32(plain[4:8])
	body = append([]byte(nil), plain[8:]...)
	return channelID, tokenID, seqNum, reqID, body, nil
}

// --- Service Body Codecs ---

// OPCUADataValue models a typed UA DataValue returned by Read/Publish and passed to Write.
type OPCUADataValue struct {
	HasValue   bool
	Value      Value
	StatusCode uint32
	ObservedAt time.Time
}

type OPCUAReadResult struct {
	NodeID OPCUANodeID
	Value  OPCUADataValue
}

type OPCUAWriteValue struct {
	NodeID OPCUANodeID
	Value  Value
}

type OPCUAMonitoredNotification struct {
	ClientHandle uint32
	Value        OPCUADataValue
}

func EncodeOPCUACreateSessionRequest(authToken uint32, sessionName string) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServiceCreateSessionReq)
	b = appendUInt32(b, authToken)
	b = appendUAString(b, sessionName)
	return b
}

func DecodeOPCUACreateSessionRequest(b []byte) (sessionName string, err error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServiceCreateSessionReq || pos+4 > len(b) {
		return "", errors.New("invalid CreateSessionRequest")
	}
	pos += 4
	name, _, err := readUAString(b, pos)
	return name, err
}

func EncodeOPCUACreateSessionResponse(status, sessionID uint32, serverNonce []byte) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServiceCreateSessionResp)
	b = appendUInt32(b, status)
	b = appendUInt32(b, sessionID)
	b = appendUAByteString(b, serverNonce)
	return b
}

func DecodeOPCUACreateSessionResponse(b []byte) (status, sessionID uint32, serverNonce []byte, err error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServiceCreateSessionResp || pos+8 > len(b) {
		return 0, 0, nil, errors.New("invalid CreateSessionResponse")
	}
	status = binary.LittleEndian.Uint32(b[pos : pos+4])
	sessionID = binary.LittleEndian.Uint32(b[pos+4 : pos+8])
	nonce, _, err := readUAByteString(b, pos+8)
	return status, sessionID, nonce, err
}

func EncodeOPCUAActivateSessionRequest(authToken uint32, authMode, username, password string) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServiceActivateSessionReq)
	b = appendUInt32(b, authToken)
	b = appendUAString(b, authMode)
	b = appendUAString(b, username)
	b = appendUAString(b, password)
	return b
}

func DecodeOPCUAActivateSessionRequest(b []byte) (authToken uint32, authMode, username, password string, err error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServiceActivateSessionReq || pos+4 > len(b) {
		return 0, "", "", "", errors.New("invalid ActivateSessionRequest")
	}
	authToken = binary.LittleEndian.Uint32(b[pos : pos+4])
	pos += 4
	authMode, pos, err = readUAString(b, pos)
	if err != nil {
		return 0, "", "", "", err
	}
	username, pos, err = readUAString(b, pos)
	if err != nil {
		return 0, "", "", "", err
	}
	password, _, err = readUAString(b, pos)
	return authToken, authMode, username, password, err
}

func EncodeOPCUAActivateSessionResponse(status uint32) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServiceActivateSessionResp)
	b = appendUInt32(b, status)
	return b
}

func DecodeOPCUAActivateSessionResponse(b []byte) (uint32, error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServiceActivateSessionResp || pos+4 > len(b) {
		return 0, errors.New("invalid ActivateSessionResponse")
	}
	return binary.LittleEndian.Uint32(b[pos : pos+4]), nil
}

func EncodeOPCUAReadRequest(nodes []OPCUANodeID) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServiceReadReq)
	b = appendUInt32(b, uint32(len(nodes)))
	for _, nid := range nodes {
		b = appendOPCUANodeIDBinary(b, nid)
	}
	return b
}

func DecodeOPCUAReadRequest(b []byte) ([]OPCUANodeID, error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServiceReadReq || pos+4 > len(b) {
		return nil, errors.New("invalid OPC-UA ReadRequest")
	}
	count := int(binary.LittleEndian.Uint32(b[pos : pos+4]))
	pos += 4
	if count < 0 || count > 256 {
		return nil, fmt.Errorf("invalid OPC-UA ReadRequest node count %d", count)
	}
	nodes := make([]OPCUANodeID, count)
	for i := 0; i < count; i++ {
		nid, nextPos, nErr := readOPCUANodeIDBinary(b, pos)
		if nErr != nil {
			return nil, nErr
		}
		nodes[i] = nid
		pos = nextPos
	}
	return nodes, nil
}

func EncodeOPCUAReadResponse(status uint32, values []OPCUADataValue) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServiceReadResp)
	b = appendUInt32(b, status)
	b = appendUInt32(b, uint32(len(values)))
	for _, dv := range values {
		b = appendOPCUADataValue(b, dv)
	}
	return b
}

func DecodeOPCUAReadResponse(b []byte) (uint32, []OPCUADataValue, error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServiceReadResp || pos+8 > len(b) {
		return 0, nil, errors.New("invalid OPC-UA ReadResponse")
	}
	status := binary.LittleEndian.Uint32(b[pos : pos+4])
	count := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
	pos += 8
	if count < 0 || count > 256 {
		return 0, nil, errors.New("invalid OPC-UA ReadResponse value count")
	}
	vals := make([]OPCUADataValue, count)
	for i := 0; i < count; i++ {
		dv, nextPos, dErr := readOPCUADataValue(b, pos)
		if dErr != nil {
			return 0, nil, dErr
		}
		vals[i] = dv
		pos = nextPos
	}
	return status, vals, nil
}

func EncodeOPCUAWriteRequest(writes []OPCUAWriteValue) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServiceWriteReq)
	b = appendUInt32(b, uint32(len(writes)))
	for _, w := range writes {
		b = appendOPCUANodeIDBinary(b, w.NodeID)
		b = appendOPCUADataValue(b, OPCUADataValue{HasValue: true, Value: w.Value, StatusCode: OPCUAStatusGood, ObservedAt: time.Now().UTC()})
	}
	return b
}

func DecodeOPCUAWriteRequest(b []byte) ([]OPCUAWriteValue, error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServiceWriteReq || pos+4 > len(b) {
		return nil, errors.New("invalid OPC-UA WriteRequest")
	}
	count := int(binary.LittleEndian.Uint32(b[pos : pos+4]))
	pos += 4
	if count < 0 || count > 256 {
		return nil, errors.New("invalid OPC-UA WriteRequest count")
	}
	out := make([]OPCUAWriteValue, count)
	for i := 0; i < count; i++ {
		nid, nextPos, nErr := readOPCUANodeIDBinary(b, pos)
		if nErr != nil {
			return nil, nErr
		}
		dv, afterDV, dErr := readOPCUADataValue(b, nextPos)
		if dErr != nil {
			return nil, dErr
		}
		out[i] = OPCUAWriteValue{NodeID: nid, Value: dv.Value}
		pos = afterDV
	}
	return out, nil
}

func EncodeOPCUAWriteResponse(status uint32, results []uint32) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServiceWriteResp)
	b = appendUInt32(b, status)
	b = appendUInt32(b, uint32(len(results)))
	for _, rc := range results {
		b = appendUInt32(b, rc)
	}
	return b
}

func DecodeOPCUAWriteResponse(b []byte) (uint32, []uint32, error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServiceWriteResp || pos+8 > len(b) {
		return 0, nil, errors.New("invalid OPC-UA WriteResponse")
	}
	status := binary.LittleEndian.Uint32(b[pos : pos+4])
	count := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
	pos += 8
	if count < 0 || count > 256 || pos+count*4 > len(b) {
		return 0, nil, errors.New("invalid OPC-UA WriteResponse count")
	}
	results := make([]uint32, count)
	for i := 0; i < count; i++ {
		results[i] = binary.LittleEndian.Uint32(b[pos+i*4 : pos+(i+1)*4])
	}
	return status, results, nil
}

func EncodeOPCUACreateSubscriptionRequest(intervalMS uint32) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServiceCreateSubscriptionReq)
	b = appendUInt32(b, intervalMS)
	return b
}

func DecodeOPCUACreateSubscriptionRequest(b []byte) (uint32, error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServiceCreateSubscriptionReq || pos+4 > len(b) {
		return 0, errors.New("invalid CreateSubscriptionRequest")
	}
	return binary.LittleEndian.Uint32(b[pos : pos+4]), nil
}

func EncodeOPCUACreateSubscriptionResponse(status, subID, revisedIntervalMS uint32) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServiceCreateSubscriptionResp)
	b = appendUInt32(b, status)
	b = appendUInt32(b, subID)
	b = appendUInt32(b, revisedIntervalMS)
	return b
}

func DecodeOPCUACreateSubscriptionResponse(b []byte) (status, subID, revisedIntervalMS uint32, err error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServiceCreateSubscriptionResp || pos+12 > len(b) {
		return 0, 0, 0, errors.New("invalid CreateSubscriptionResponse")
	}
	return binary.LittleEndian.Uint32(b[pos : pos+4]), binary.LittleEndian.Uint32(b[pos+4 : pos+8]), binary.LittleEndian.Uint32(b[pos+8 : pos+12]), nil
}

type OPCUAMonitoredItemSpec struct {
	ClientHandle uint32
	NodeID       OPCUANodeID
}

func EncodeOPCUACreateMonitoredItemsRequest(subID uint32, items []OPCUAMonitoredItemSpec) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServiceCreateMonitoredItemsReq)
	b = appendUInt32(b, subID)
	b = appendUInt32(b, uint32(len(items)))
	for _, it := range items {
		b = appendUInt32(b, it.ClientHandle)
		b = appendOPCUANodeIDBinary(b, it.NodeID)
	}
	return b
}

func DecodeOPCUACreateMonitoredItemsRequest(b []byte) (uint32, []OPCUAMonitoredItemSpec, error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServiceCreateMonitoredItemsReq || pos+8 > len(b) {
		return 0, nil, errors.New("invalid CreateMonitoredItemsRequest")
	}
	subID := binary.LittleEndian.Uint32(b[pos : pos+4])
	count := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
	pos += 8
	if count < 0 || count > 256 {
		return 0, nil, errors.New("invalid MonitoredItems count")
	}
	items := make([]OPCUAMonitoredItemSpec, count)
	for i := 0; i < count; i++ {
		if pos+4 > len(b) {
			return 0, nil, errors.New("truncated MonitoredItem clientHandle")
		}
		handle := binary.LittleEndian.Uint32(b[pos : pos+4])
		nid, nextPos, nErr := readOPCUANodeIDBinary(b, pos+4)
		if nErr != nil {
			return 0, nil, nErr
		}
		items[i] = OPCUAMonitoredItemSpec{ClientHandle: handle, NodeID: nid}
		pos = nextPos
	}
	return subID, items, nil
}

func EncodeOPCUACreateMonitoredItemsResponse(status uint32, results []uint32) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServiceCreateMonitoredItemsRsp)
	b = appendUInt32(b, status)
	b = appendUInt32(b, uint32(len(results)))
	for _, rc := range results {
		b = appendUInt32(b, rc)
	}
	return b
}

func DecodeOPCUACreateMonitoredItemsResponse(b []byte) (uint32, []uint32, error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServiceCreateMonitoredItemsRsp || pos+8 > len(b) {
		return 0, nil, errors.New("invalid CreateMonitoredItemsResponse")
	}
	status := binary.LittleEndian.Uint32(b[pos : pos+4])
	count := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
	pos += 8
	if count < 0 || count > 256 || pos+count*4 > len(b) {
		return 0, nil, errors.New("invalid CreateMonitoredItemsResponse count")
	}
	out := make([]uint32, count)
	for i := 0; i < count; i++ {
		out[i] = binary.LittleEndian.Uint32(b[pos+i*4 : pos+(i+1)*4])
	}
	return status, out, nil
}

func EncodeOPCUAPublishRequest(subID uint32) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServicePublishReq)
	b = appendUInt32(b, subID)
	return b
}

func DecodeOPCUAPublishRequest(b []byte) (uint32, error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServicePublishReq || pos+4 > len(b) {
		return 0, errors.New("invalid PublishRequest")
	}
	return binary.LittleEndian.Uint32(b[pos : pos+4]), nil
}

func EncodeOPCUAPublishResponse(status, subID uint32, notifications []OPCUAMonitoredNotification) []byte {
	var b []byte
	b = appendExpandedNumericNodeID(b, OPCUAServicePublishResp)
	b = appendUInt32(b, status)
	b = appendUInt32(b, subID)
	b = appendUInt32(b, uint32(len(notifications)))
	for _, n := range notifications {
		b = appendUInt32(b, n.ClientHandle)
		b = appendOPCUADataValue(b, n.Value)
	}
	return b
}

func DecodeOPCUAPublishResponse(b []byte) (status, subID uint32, notifications []OPCUAMonitoredNotification, err error) {
	svc, pos, err := readExpandedNumericNodeID(b, 0)
	if err != nil || svc != OPCUAServicePublishResp || pos+12 > len(b) {
		return 0, 0, nil, errors.New("invalid PublishResponse")
	}
	status = binary.LittleEndian.Uint32(b[pos : pos+4])
	subID = binary.LittleEndian.Uint32(b[pos+4 : pos+8])
	count := int(binary.LittleEndian.Uint32(b[pos+8 : pos+12]))
	pos += 12
	if count < 0 || count > 256 {
		return 0, 0, nil, errors.New("invalid PublishResponse notification count")
	}
	out := make([]OPCUAMonitoredNotification, count)
	for i := 0; i < count; i++ {
		if pos+4 > len(b) {
			return 0, 0, nil, errors.New("truncated notification clientHandle")
		}
		handle := binary.LittleEndian.Uint32(b[pos : pos+4])
		dv, nextPos, dErr := readOPCUADataValue(b, pos+4)
		if dErr != nil {
			return 0, 0, nil, dErr
		}
		out[i] = OPCUAMonitoredNotification{ClientHandle: handle, Value: dv}
		pos = nextPos
	}
	return status, subID, out, nil
}

func PeekOPCUAServiceID(b []byte) (uint32, error) {
	id, _, err := readExpandedNumericNodeID(b, 0)
	return id, err
}

// --- Primitive Binary Encoders / Decoders ---

func appendUInt32(b []byte, v uint32) []byte {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	return append(b, buf[:]...)
}

func appendUInt64(b []byte, v uint64) []byte {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	return append(b, buf[:]...)
}

func appendUAString(b []byte, s string) []byte {
	if s == "" {
		return appendUInt32(b, 0xFFFFFFFF)
	}
	b = appendUInt32(b, uint32(len(s)))
	return append(b, s...)
}

func readUAString(b []byte, pos int) (string, int, error) {
	if pos+4 > len(b) {
		return "", pos, errors.New("truncated OPC-UA string length")
	}
	l := int32(binary.LittleEndian.Uint32(b[pos : pos+4]))
	pos += 4
	if l <= 0 {
		return "", pos, nil
	}
	if int(l) > len(b)-pos {
		return "", pos, errors.New("truncated OPC-UA string bytes")
	}
	return string(b[pos : pos+int(l)]), pos + int(l), nil
}

func appendUAByteString(b []byte, v []byte) []byte {
	if len(v) == 0 {
		return appendUInt32(b, 0xFFFFFFFF)
	}
	b = appendUInt32(b, uint32(len(v)))
	return append(b, v...)
}

func readUAByteString(b []byte, pos int) ([]byte, int, error) {
	if pos+4 > len(b) {
		return nil, pos, errors.New("truncated OPC-UA ByteString length")
	}
	l := int32(binary.LittleEndian.Uint32(b[pos : pos+4]))
	pos += 4
	if l <= 0 {
		return nil, pos, nil
	}
	if int(l) > len(b)-pos {
		return nil, pos, errors.New("truncated OPC-UA ByteString bytes")
	}
	out := append([]byte(nil), b[pos:pos+int(l)]...)
	return out, pos + int(l), nil
}

func appendExpandedNumericNodeID(b []byte, id uint32) []byte {
	return append(b, 0x01, 0x00, byte(id), byte(id>>8))
}

func readExpandedNumericNodeID(b []byte, pos int) (uint32, int, error) {
	if pos >= len(b) {
		return 0, pos, errors.New("missing OPC-UA NodeId encoding byte")
	}
	switch b[pos] {
	case 0x00:
		if pos+2 > len(b) {
			return 0, pos, errors.New("truncated TwoByte NodeId")
		}
		return uint32(b[pos+1]), pos + 2, nil
	case 0x01:
		if pos+4 > len(b) {
			return 0, pos, errors.New("truncated FourByte NodeId")
		}
		return uint32(binary.LittleEndian.Uint16(b[pos+2 : pos+4])), pos + 4, nil
	case 0x02:
		if pos+7 > len(b) {
			return 0, pos, errors.New("truncated Numeric NodeId")
		}
		return binary.LittleEndian.Uint32(b[pos+3 : pos+7]), pos + 7, nil
	default:
		return 0, pos, fmt.Errorf("unsupported service NodeId encoding 0x%02x", b[pos])
	}
}

func appendOPCUANodeIDBinary(b []byte, nid OPCUANodeID) []byte {
	switch nid.IdentifierType {
	case OPCUANodeIDNumeric:
		b = append(b, 0x02, byte(nid.NamespaceIndex), byte(nid.NamespaceIndex>>8))
		return appendUInt32(b, nid.Numeric)
	case OPCUANodeIDString:
		b = append(b, 0x03, byte(nid.NamespaceIndex), byte(nid.NamespaceIndex>>8))
		return appendUAString(b, nid.StringValue)
	case OPCUANodeIDGUID:
		guidBytes, _ := parseOPCUAGUIDBytes(nid.StringValue)
		b = append(b, 0x04, byte(nid.NamespaceIndex), byte(nid.NamespaceIndex>>8))
		return append(b, guidBytes[:]...)
	case OPCUANodeIDByteString:
		b = append(b, 0x05, byte(nid.NamespaceIndex), byte(nid.NamespaceIndex>>8))
		return appendUAByteString(b, nid.Opaque)
	default:
		b = append(b, 0x00, 0x00)
		return b
	}
}

func readOPCUANodeIDBinary(b []byte, pos int) (OPCUANodeID, int, error) {
	if pos >= len(b) {
		return OPCUANodeID{}, pos, errors.New("missing OPC-UA NodeId")
	}
	enc := b[pos]
	pos++
	switch enc {
	case 0x00:
		if pos >= len(b) {
			return OPCUANodeID{}, pos, errors.New("truncated TwoByte NodeId")
		}
		return OPCUANodeID{IdentifierType: OPCUANodeIDNumeric, Numeric: uint32(b[pos])}, pos + 1, nil
	case 0x01:
		if pos+3 > len(b) {
			return OPCUANodeID{}, pos, errors.New("truncated FourByte NodeId")
		}
		return OPCUANodeID{NamespaceIndex: uint16(b[pos]), IdentifierType: OPCUANodeIDNumeric, Numeric: uint32(binary.LittleEndian.Uint16(b[pos+1 : pos+3]))}, pos + 3, nil
	case 0x02:
		if pos+6 > len(b) {
			return OPCUANodeID{}, pos, errors.New("truncated Numeric NodeId")
		}
		ns := binary.LittleEndian.Uint16(b[pos : pos+2])
		id := binary.LittleEndian.Uint32(b[pos+2 : pos+6])
		return OPCUANodeID{NamespaceIndex: ns, IdentifierType: OPCUANodeIDNumeric, Numeric: id}, pos + 6, nil
	case 0x03:
		if pos+2 > len(b) {
			return OPCUANodeID{}, pos, errors.New("truncated String NodeId namespace")
		}
		ns := binary.LittleEndian.Uint16(b[pos : pos+2])
		s, nextPos, err := readUAString(b, pos+2)
		return OPCUANodeID{NamespaceIndex: ns, IdentifierType: OPCUANodeIDString, StringValue: s}, nextPos, err
	case 0x04:
		if pos+18 > len(b) {
			return OPCUANodeID{}, pos, errors.New("truncated GUID NodeId")
		}
		ns := binary.LittleEndian.Uint16(b[pos : pos+2])
		var g [16]byte
		copy(g[:], b[pos+2:pos+18])
		return OPCUANodeID{NamespaceIndex: ns, IdentifierType: OPCUANodeIDGUID, StringValue: formatOPCUAGUIDBytes(g)}, pos + 18, nil
	case 0x05:
		if pos+2 > len(b) {
			return OPCUANodeID{}, pos, errors.New("truncated Opaque NodeId")
		}
		ns := binary.LittleEndian.Uint16(b[pos : pos+2])
		raw, nextPos, err := readUAByteString(b, pos+2)
		return OPCUANodeID{NamespaceIndex: ns, IdentifierType: OPCUANodeIDByteString, Opaque: raw}, nextPos, err
	default:
		return OPCUANodeID{}, pos, fmt.Errorf("unsupported NodeId binary encoding 0x%02x", enc)
	}
}

func appendOPCUADataValue(b []byte, dv OPCUADataValue) []byte {
	var mask byte
	if dv.HasValue {
		mask |= 0x01
	}
	if dv.StatusCode != OPCUAStatusGood {
		mask |= 0x02
	}
	if !dv.ObservedAt.IsZero() {
		mask |= 0x04
	}
	b = append(b, mask)
	if dv.HasValue {
		b = appendOPCUAVariant(b, dv.Value)
	}
	if mask&0x02 != 0 {
		b = appendUInt32(b, dv.StatusCode)
	}
	if mask&0x04 != 0 {
		b = appendUInt64(b, uint64(dv.ObservedAt.UTC().UnixNano()/100+opcuaFiletimeUnix))
	}
	return b
}

func readOPCUADataValue(b []byte, pos int) (OPCUADataValue, int, error) {
	if pos >= len(b) {
		return OPCUADataValue{}, pos, errors.New("missing OPC-UA DataValue mask")
	}
	mask := b[pos]
	pos++
	dv := OPCUADataValue{StatusCode: OPCUAStatusGood}
	if mask&0x01 != 0 {
		v, nextPos, err := readOPCUAVariant(b, pos)
		if err != nil {
			return OPCUADataValue{}, pos, err
		}
		dv.HasValue = true
		dv.Value = v
		pos = nextPos
	}
	if mask&0x02 != 0 {
		if pos+4 > len(b) {
			return OPCUADataValue{}, pos, errors.New("truncated DataValue StatusCode")
		}
		dv.StatusCode = binary.LittleEndian.Uint32(b[pos : pos+4])
		pos += 4
	}
	if mask&0x04 != 0 {
		if pos+8 > len(b) {
			return OPCUADataValue{}, pos, errors.New("truncated DataValue SourceTimestamp")
		}
		ft := int64(binary.LittleEndian.Uint64(b[pos : pos+8]))
		if ft > opcuaFiletimeUnix {
			dv.ObservedAt = time.Unix(0, (ft-opcuaFiletimeUnix)*100).UTC()
		}
		pos += 8
	}
	return dv, pos, nil
}

func appendOPCUAVariant(b []byte, v Value) []byte {
	switch v.Type {
	case DataTypeBoolean:
		b = append(b, 1) // UA Boolean
		if v.Boolean {
			return append(b, 1)
		}
		return append(b, 0)
	case DataTypeInteger:
		b = append(b, 6) // UA Int32
		return appendUInt32(b, uint32(v.Integer))
	case DataTypeLong:
		b = append(b, 8) // UA Int64
		return appendUInt64(b, uint64(v.Long))
	case DataTypeFloat:
		b = append(b, 10) // UA Float
		return appendUInt32(b, math.Float32bits(v.Float))
	case DataTypeString:
		b = append(b, 12) // UA String
		return appendUAString(b, v.String)
	default:
		b = append(b, 11) // UA Double
		return appendUInt64(b, math.Float64bits(v.Double))
	}
}

func readOPCUAVariant(b []byte, pos int) (Value, int, error) {
	if pos >= len(b) {
		return Value{}, pos, errors.New("missing OPC-UA Variant type")
	}
	tid := b[pos]
	pos++
	switch tid {
	case 1:
		if pos >= len(b) {
			return Value{}, pos, errors.New("truncated UA Boolean")
		}
		return BooleanValue(b[pos] != 0), pos + 1, nil
	case 6:
		if pos+4 > len(b) {
			return Value{}, pos, errors.New("truncated UA Int32")
		}
		return IntegerValue(int32(binary.LittleEndian.Uint32(b[pos : pos+4]))), pos + 4, nil
	case 8:
		if pos+8 > len(b) {
			return Value{}, pos, errors.New("truncated UA Int64")
		}
		return LongValue(int64(binary.LittleEndian.Uint64(b[pos : pos+8]))), pos + 8, nil
	case 10:
		if pos+4 > len(b) {
			return Value{}, pos, errors.New("truncated UA Float")
		}
		return FloatValue(math.Float32frombits(binary.LittleEndian.Uint32(b[pos : pos+4]))), pos + 4, nil
	case 11:
		if pos+8 > len(b) {
			return Value{}, pos, errors.New("truncated UA Double")
		}
		return DoubleValue(math.Float64frombits(binary.LittleEndian.Uint64(b[pos : pos+8]))), pos + 8, nil
	case 12:
		s, nextPos, err := readUAString(b, pos)
		return StringValue(s), nextPos, err
	default:
		return Value{}, pos, fmt.Errorf("unsupported OPC-UA Variant type %d", tid)
	}
}
