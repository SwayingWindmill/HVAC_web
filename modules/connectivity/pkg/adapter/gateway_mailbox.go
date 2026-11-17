package adapter

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/eclipse/paho.golang/paho"
)

const (
	walMagic             = "GSP2"
	walHeaderSize  int64 = 144
	walMaxGateway        = 130
	walFrameHeader int64 = 8 // uint32 bodyLen + uint32 crc32
	walCompactMin  int64 = 4 << 20
)

var errSpoolFrameCorrupt = errors.New("gateway spool frame is corrupt")

type queuedPublish struct {
	packet *paho.Publish
	endSeq int64
}

type overflowEntry struct {
	topic        string
	qos          byte
	retain       bool
	packetID     uint16
	logicalStart int64
	frameLen     int64
	endSeq       int64
}

type enqueueResult struct {
	memDepth      int
	overflowDepth int
	shed          bool
}

// gatewayMailbox maintains a bounded, per-Gateway WAL disk spool with an in-RAM hot cache.
// Accepted uplinks are durably appended to spoolFile before acknowledging to MQTT so slow
// Gateways never stall healthy Gateways in the shared broker inflight window, while
// remaining isolated in per-Gateway FIFO order.
type gatewayMailbox struct {
	gatewayID        string
	memCap           int
	overflowMax      int
	overflowMaxBytes int64
	compactMinBytes  int64
	spoolPath        string

	mu              sync.Mutex
	mem             []queuedPublish
	memHead         int
	memCount        int
	overflow        []overflowEntry
	overflowHead    int
	promoting       int
	spoolFile       *os.File
	spoolSize       int64
	fileBaseSeq     int64
	consumedLogical int64
	closed          bool
	notify          chan struct{}
}

func newGatewayMailbox(gatewayID string, memCap, overflowMax int, overflowMaxBytes int64, overflowDir string) *gatewayMailbox {
	return &gatewayMailbox{
		gatewayID:        gatewayID,
		memCap:           max(1, memCap),
		overflowMax:      max(0, overflowMax),
		overflowMaxBytes: overflowMaxBytes,
		compactMinBytes:  walCompactMin,
		spoolPath:        filepath.Join(overflowDir, safeGatewayFileName(gatewayID)),
		mem:              make([]queuedPublish, max(1, memCap)),
		notify:           make(chan struct{}, 1),
	}
}

func safeGatewayFileName(gatewayID string) string {
	var builder strings.Builder
	builder.Grow(len(gatewayID))
	clean := gatewayID != "" && gatewayID != "." && gatewayID != ".."
	for index := 0; index < len(gatewayID); index++ {
		c := gatewayID[index]
		isSafe := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
		if isSafe {
			builder.WriteByte(c)
		} else {
			clean = false
			builder.WriteByte('_')
		}
	}
	if clean {
		return builder.String() + ".spool"
	}
	sum := sha256.Sum256([]byte(gatewayID))
	prefix := builder.String()
	if prefix == "" || prefix == "." || prefix == ".." {
		prefix = "gateway"
	}
	return fmt.Sprintf("%s-%s.spool", prefix, hex.EncodeToString(sum[:4]))
}

func (mb *gatewayMailbox) overflowLenLocked() int {
	return len(mb.overflow) - mb.overflowHead
}

func (mb *gatewayMailbox) activeBytesLocked() int64 {
	if mb.spoolFile == nil {
		return 0
	}
	writtenLogical := mb.fileBaseSeq + (mb.spoolSize - walHeaderSize)
	if writtenLogical <= mb.consumedLogical {
		return 0
	}
	return writtenLogical - mb.consumedLogical
}

func (mb *gatewayMailbox) depth() (memDepth, overflowDepth int) {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	return mb.memCount + mb.promoting, mb.overflowLenLocked()
}

func (mb *gatewayMailbox) signalLocked() {
	select {
	case mb.notify <- struct{}{}:
	default:
	}
}

func (mb *gatewayMailbox) pushMemLocked(item queuedPublish) {
	tail := (mb.memHead + mb.memCount) % mb.memCap
	mb.mem[tail] = item
	mb.memCount++
}

func (mb *gatewayMailbox) popMemLocked() (queuedPublish, bool) {
	if mb.memCount == 0 {
		return queuedPublish{}, false
	}
	item := mb.mem[mb.memHead]
	mb.mem[mb.memHead] = queuedPublish{}
	mb.memHead = (mb.memHead + 1) % mb.memCap
	mb.memCount--
	return item, true
}

func (mb *gatewayMailbox) peekOverflowAtLocked(offset int) (overflowEntry, bool) {
	idx := mb.overflowHead + offset
	if idx < 0 || idx >= len(mb.overflow) {
		return overflowEntry{}, false
	}
	return mb.overflow[idx], true
}

func (mb *gatewayMailbox) advanceOverflowLocked(count int) {
	if count <= 0 {
		return
	}
	for i := 0; i < count && mb.overflowHead < len(mb.overflow); i++ {
		mb.overflow[mb.overflowHead] = overflowEntry{}
		mb.overflowHead++
	}
	if mb.overflowHead == len(mb.overflow) {
		mb.overflow = mb.overflow[:0]
		mb.overflowHead = 0
	} else if mb.overflowHead >= 64 && mb.overflowHead*2 >= len(mb.overflow) {
		remaining := len(mb.overflow) - mb.overflowHead
		copy(mb.overflow, mb.overflow[mb.overflowHead:])
		clear(mb.overflow[remaining:])
		mb.overflow = mb.overflow[:remaining]
		mb.overflowHead = 0
	}
}

func encodeWALHeader(gatewayID string, consumedPhys int64) []byte {
	hdr := make([]byte, walHeaderSize)
	copy(hdr[:4], walMagic)
	binary.BigEndian.PutUint64(hdr[4:12], uint64(max(walHeaderSize, consumedPhys)))
	gwBytes := []byte(gatewayID)
	if len(gwBytes) > walMaxGateway {
		gwBytes = gwBytes[:walMaxGateway]
	}
	binary.BigEndian.PutUint16(hdr[12:14], uint16(len(gwBytes)))
	copy(hdr[14:], gwBytes)
	return hdr
}

func decodeWALHeader(hdr []byte) (consumedPhys int64, gatewayID string, ok bool) {
	if len(hdr) < int(walHeaderSize) || string(hdr[:4]) != walMagic {
		return 0, "", false
	}
	rawConsumed := binary.BigEndian.Uint64(hdr[4:12])
	gwLen := int(binary.BigEndian.Uint16(hdr[12:14]))
	if gwLen == 0 || gwLen > walMaxGateway || rawConsumed < uint64(walHeaderSize) || rawConsumed > math.MaxInt64 {
		return 0, "", false
	}
	return int64(rawConsumed), string(hdr[14 : 14+gwLen]), true
}

func writeConsumedPhysOffset(file *os.File, physOffset int64) error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(max(walHeaderSize, physOffset)))
	_, err := file.WriteAt(buf[:], 4)
	return err
}

func (mb *gatewayMailbox) ensureSpoolOpenLocked() error {
	if mb.spoolFile != nil {
		return nil
	}
	file, err := os.OpenFile(mb.spoolPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open gateway spool: %w", err)
	}
	hdr := encodeWALHeader(mb.gatewayID, walHeaderSize)
	if written, err := file.WriteAt(hdr, 0); err != nil || written != len(hdr) {
		_ = file.Close()
		if err == nil {
			err = errors.New("short write to gateway spool header")
		}
		return fmt.Errorf("write gateway spool header: %w", err)
	}
	mb.spoolFile = file
	mb.spoolSize = walHeaderSize
	return nil
}

func encodeWALFrame(topic string, payload []byte) ([]byte, error) {
	if len(topic) > math.MaxUint16 {
		return nil, errors.New("MQTT topic exceeds WAL frame limit")
	}
	bodyLen := 2 + len(topic) + len(payload)
	if bodyLen > math.MaxUint32 {
		return nil, errors.New("MQTT payload exceeds WAL frame limit")
	}
	frame := make([]byte, int(walFrameHeader)+bodyLen)
	binary.BigEndian.PutUint32(frame[0:4], uint32(bodyLen))
	binary.BigEndian.PutUint16(frame[8:10], uint16(len(topic)))
	copy(frame[10:10+len(topic)], topic)
	copy(frame[10+len(topic):], payload)
	sum := crc32.ChecksumIEEE(frame[walFrameHeader:])
	binary.BigEndian.PutUint32(frame[4:8], sum)
	return frame, nil
}

// enqueue appends a message to the per-Gateway WAL spool without blocking on the worker.
// When count or byte limits are reached, shed=true is returned so the caller closes the
// connection without ACK and lets the broker redeliver later.
func (mb *gatewayMailbox) enqueue(received *paho.Publish) (enqueueResult, error) {
	frame, err := encodeWALFrame(received.Topic, received.Payload)
	if err != nil {
		return enqueueResult{}, err
	}
	frameLen := int64(len(frame))

	mb.mu.Lock()
	if mb.closed {
		mb.mu.Unlock()
		return enqueueResult{}, errors.New("gateway mailbox closed")
	}
	totalDepth := mb.memCount + mb.promoting + mb.overflowLenLocked()
	totalCap := mb.memCap + mb.overflowMax
	if totalDepth >= totalCap || (mb.overflowMaxBytes > 0 && mb.activeBytesLocked()+frameLen > mb.overflowMaxBytes) {
		res := enqueueResult{
			memDepth:      mb.memCount + mb.promoting,
			overflowDepth: mb.overflowLenLocked(),
			shed:          true,
		}
		mb.mu.Unlock()
		return res, nil
	}
	if err := mb.ensureSpoolOpenLocked(); err != nil {
		mb.mu.Unlock()
		return enqueueResult{}, err
	}
	entryPhys := mb.spoolSize
	if written, err := mb.spoolFile.WriteAt(frame, entryPhys); err != nil || written != len(frame) {
		mb.mu.Unlock()
		if err == nil {
			err = errors.New("short write to gateway spool")
		}
		return enqueueResult{}, fmt.Errorf("write gateway spool: %w", err)
	}
	mb.spoolSize += frameLen
	logicalStart := mb.fileBaseSeq + (entryPhys - walHeaderSize)
	logicalEnd := logicalStart + frameLen

	if mb.overflowLenLocked() == 0 && mb.promoting == 0 && mb.memCount < mb.memCap {
		packet := *received
		packet.Payload = append([]byte(nil), received.Payload...)
		mb.pushMemLocked(queuedPublish{packet: &packet, endSeq: logicalEnd})
	} else {
		mb.overflow = append(mb.overflow, overflowEntry{
			topic:        received.Topic,
			qos:          received.QoS,
			retain:       received.Retain,
			packetID:     received.PacketID,
			logicalStart: logicalStart,
			frameLen:     frameLen,
			endSeq:       logicalEnd,
		})
	}
	res := enqueueResult{
		memDepth:      mb.memCount + mb.promoting,
		overflowDepth: mb.overflowLenLocked(),
	}
	mb.signalLocked()
	mb.mu.Unlock()
	return res, nil
}

// popBatch drains up to maxItems in strict FIFO order and promotes head overflow records
// back into RAM. Entries are removed from overflow only after they are successfully read
// and CRC-verified; a corrupt frame at head is quarantined/dropped in FIFO order, while a
// transient I/O error halts immediately so later messages are never promoted out of order.
func (mb *gatewayMailbox) popBatch(maxItems int) (items []queuedPublish, memDepth, overflowDepth int, readErrors int) {
	if maxItems < 1 {
		maxItems = 1
	}
	mb.mu.Lock()
	for len(items) < maxItems {
		item, ok := mb.popMemLocked()
		if !ok {
			break
		}
		items = append(items, item)
	}

	needRead := maxItems - len(items)
	availOverflow := mb.overflowLenLocked()
	wantRead := min(needRead, availOverflow)
	wantPromote := min(max(0, mb.memCap-mb.memCount), availOverflow-wantRead)
	totalPeek := wantRead + wantPromote
	if totalPeek == 0 {
		memDepth = mb.memCount
		overflowDepth = availOverflow
		mb.mu.Unlock()
		return items, memDepth, overflowDepth, 0
	}

	peeked := make([]overflowEntry, totalPeek)
	for i := 0; i < totalPeek; i++ {
		peeked[i], _ = mb.peekOverflowAtLocked(i)
	}
	spool := mb.spoolFile
	baseSeq := mb.fileBaseSeq
	mb.promoting = totalPeek
	mb.mu.Unlock()

	consumedCount := 0
	var skippedSeq int64
	promoted := make([]queuedPublish, 0, wantPromote)
	for i, entry := range peeked {
		physOffset := walHeaderSize + (entry.logicalStart - baseSeq)
		item, err := readSpooledPublish(spool, physOffset, entry)
		if err != nil {
			if errors.Is(err, errSpoolFrameCorrupt) && (len(items) > 0 || len(promoted) > 0) {
				// Leave corrupt frame at overflow head for the next popBatch so
				// consumedLogical never jumps past preceding valid items.
				break
			}
			readErrors++
			if errors.Is(err, errSpoolFrameCorrupt) {
				consumedCount++
				skippedSeq = entry.endSeq
				continue
			}
			// Do not advance past unread valid entries if disk I/O fails transiently.
			break
		}
		consumedCount++
		if i < wantRead {
			items = append(items, item)
		} else {
			promoted = append(promoted, item)
		}
	}

	mb.mu.Lock()
	mb.advanceOverflowLocked(consumedCount)
	if skippedSeq > mb.consumedLogical && len(items) == 0 && len(promoted) == 0 {
		mb.consumedLogical = skippedSeq
		if mb.spoolFile != nil {
			_ = writeConsumedPhysOffset(mb.spoolFile, walHeaderSize+(mb.consumedLogical-mb.fileBaseSeq))
		}
	}
	for _, item := range promoted {
		mb.pushMemLocked(item)
	}
	mb.promoting = 0
	memDepth = mb.memCount
	overflowDepth = mb.overflowLenLocked()
	mb.mu.Unlock()
	return items, memDepth, overflowDepth, readErrors
}

func (mb *gatewayMailbox) markConsumed(logicalEnd int64) {
	if logicalEnd <= 0 {
		return
	}
	mb.mu.Lock()
	defer mb.mu.Unlock()
	if logicalEnd > mb.consumedLogical {
		mb.consumedLogical = logicalEnd
	}
	if mb.spoolFile == nil {
		return
	}
	writtenLogical := mb.fileBaseSeq + (mb.spoolSize - walHeaderSize)
	if mb.memCount == 0 && mb.promoting == 0 && mb.overflowLenLocked() == 0 && mb.consumedLogical >= writtenLogical {
		if mb.spoolSize > walHeaderSize {
			_ = mb.spoolFile.Truncate(walHeaderSize)
			mb.spoolSize = walHeaderSize
		}
		mb.fileBaseSeq = mb.consumedLogical
		_ = writeConsumedPhysOffset(mb.spoolFile, walHeaderSize)
		return
	}

	reclaimable := mb.consumedLogical - mb.fileBaseSeq
	totalData := mb.spoolSize - walHeaderSize
	_ = writeConsumedPhysOffset(mb.spoolFile, walHeaderSize+reclaimable)
	if mb.promoting == 0 && reclaimable > 0 && reclaimable >= mb.compactMinBytes && reclaimable*2 >= totalData {
		mb.compactSpoolLocked(reclaimable)
	}
}

func (mb *gatewayMailbox) compactSpoolLocked(reclaimable int64) {
	srcOffset := walHeaderSize + reclaimable
	liveBytes := mb.spoolSize - srcOffset
	if liveBytes < 0 || mb.spoolFile == nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(mb.spoolPath), filepath.Base(mb.spoolPath)+".compact.*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return
	}
	hdr := encodeWALHeader(mb.gatewayID, walHeaderSize)
	if wn, werr := tmp.WriteAt(hdr, 0); werr != nil || wn != len(hdr) {
		return
	}
	dstOffset := walHeaderSize
	buf := make([]byte, 64*1024)
	copied := int64(0)
	for copied < liveBytes {
		toRead := min(int64(len(buf)), liveBytes-copied)
		n, rerr := mb.spoolFile.ReadAt(buf[:toRead], srcOffset+copied)
		if n > 0 {
			if wn, werr := tmp.WriteAt(buf[:n], dstOffset+copied); werr != nil || wn != n {
				return
			}
			copied += int64(n)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) && copied == liveBytes {
				break
			}
			return
		}
	}
	if err := tmp.Sync(); err != nil {
		return
	}
	if err := os.Rename(tmpName, mb.spoolPath); err != nil {
		return
	}
	cleanupTmp = false
	oldFile := mb.spoolFile
	mb.spoolFile = tmp
	mb.fileBaseSeq += reclaimable
	mb.spoolSize = walHeaderSize + liveBytes
	_ = oldFile.Close()
}

func readWALFrame(file *os.File, physOffset int64) (topic string, payload []byte, frameLen int64, err error) {
	var hdr [walFrameHeader]byte
	n, err := file.ReadAt(hdr[:], physOffset)
	if err != nil || n != len(hdr) {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return "", nil, 0, err
	}
	bodyLen := binary.BigEndian.Uint32(hdr[0:4])
	expectedCRC := binary.BigEndian.Uint32(hdr[4:8])
	if bodyLen < 2 || int64(bodyLen) > 16<<20 {
		return "", nil, 0, errSpoolFrameCorrupt
	}
	body := make([]byte, bodyLen)
	bn, berr := file.ReadAt(body, physOffset+walFrameHeader)
	if berr != nil && !errors.Is(berr, io.EOF) {
		return "", nil, 0, berr
	}
	if bn != len(body) {
		return "", nil, 0, io.ErrUnexpectedEOF
	}
	if crc32.ChecksumIEEE(body) != expectedCRC {
		return "", nil, 0, errSpoolFrameCorrupt
	}
	topicLen := int(binary.BigEndian.Uint16(body[0:2]))
	if 2+topicLen > len(body) {
		return "", nil, 0, errSpoolFrameCorrupt
	}
	return string(body[2 : 2+topicLen]), body[2+topicLen:], walFrameHeader + int64(bodyLen), nil
}

func readSpooledPublish(file *os.File, physOffset int64, entry overflowEntry) (queuedPublish, error) {
	if file == nil {
		return queuedPublish{}, errors.New("gateway spool file is nil")
	}
	topic, payload, frameLen, err := readWALFrame(file, physOffset)
	if err != nil {
		return queuedPublish{}, err
	}
	if frameLen != entry.frameLen {
		return queuedPublish{}, errSpoolFrameCorrupt
	}
	if topic == "" {
		topic = entry.topic
	}
	return queuedPublish{
		packet: &paho.Publish{
			Topic:    topic,
			Payload:  payload,
			QoS:      entry.qos,
			Retain:   entry.retain,
			PacketID: entry.packetID,
		},
		endSeq: entry.endSeq,
	}, nil
}

func recoverSpoolMailboxes(overflowDir string, memCap, overflowMax int, overflowMaxBytes int64) (map[string]*gatewayMailbox, int, error) {
	entries, err := os.ReadDir(overflowDir)
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	mailboxes := make(map[string]*gatewayMailbox)
	recoveredCount := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".spool") {
			continue
		}
		path := filepath.Join(overflowDir, entry.Name())
		mb, count := recoverSingleSpool(path, memCap, overflowMax, overflowMaxBytes)
		if mb != nil {
			mailboxes[mb.gatewayID] = mb
			recoveredCount += count
		}
	}
	return mailboxes, recoveredCount, nil
}

func recoverSingleSpool(path string, memCap, overflowMax int, overflowMaxBytes int64) (*gatewayMailbox, int) {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, 0
	}
	info, err := file.Stat()
	if err != nil || info.Size() < walHeaderSize {
		_ = file.Close()
		return nil, 0
	}
	hdr := make([]byte, walHeaderSize)
	if n, err := file.ReadAt(hdr, 0); err != nil || n != len(hdr) {
		_ = file.Close()
		return nil, 0
	}
	consumedPhys, gatewayID, ok := decodeWALHeader(hdr)
	if !ok {
		_ = file.Close()
		return nil, 0
	}
	if consumedPhys >= info.Size() {
		_ = file.Truncate(walHeaderSize)
		_ = writeConsumedPhysOffset(file, walHeaderSize)
		_ = file.Close()
		return nil, 0
	}

	mb := newGatewayMailbox(gatewayID, memCap, overflowMax, overflowMaxBytes, filepath.Dir(path))
	mb.spoolPath = path
	mb.spoolFile = file
	mb.consumedLogical = consumedPhys - walHeaderSize

	pos := consumedPhys
	recovered := 0
	for pos < info.Size() {
		topic, payload, frameLen, readErr := readWALFrame(file, pos)
		if readErr != nil {
			// Truncate trailing partial frame left by an unclean process crash.
			_ = file.Truncate(pos)
			break
		}
		logicalStart := pos - walHeaderSize
		logicalEnd := logicalStart + frameLen
		if len(mb.overflow) == 0 && mb.memCount < mb.memCap {
			mb.pushMemLocked(queuedPublish{
				packet: &paho.Publish{Topic: topic, Payload: payload, QoS: 1},
				endSeq: logicalEnd,
			})
		} else {
			mb.overflow = append(mb.overflow, overflowEntry{
				topic:        topic,
				qos:          1,
				logicalStart: logicalStart,
				frameLen:     frameLen,
				endSeq:       logicalEnd,
			})
		}
		pos += frameLen
		recovered++
	}
	mb.spoolSize = pos
	if recovered == 0 {
		_ = file.Truncate(walHeaderSize)
		_ = writeConsumedPhysOffset(file, walHeaderSize)
		_ = file.Close()
		return nil, 0
	}
	mb.signalLocked()
	return mb, recovered
}

func (mb *gatewayMailbox) close(removeFile bool) {
	mb.mu.Lock()
	if mb.closed {
		mb.mu.Unlock()
		return
	}
	mb.closed = true
	file := mb.spoolFile
	mb.spoolFile = nil
	spoolPath := mb.spoolPath
	mb.mu.Unlock()

	if file != nil {
		_ = file.Close()
		if removeFile {
			_ = os.Remove(spoolPath)
		}
	}
}
