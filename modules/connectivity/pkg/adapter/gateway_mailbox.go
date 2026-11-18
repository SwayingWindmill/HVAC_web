package adapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
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
	"time"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/mqttclient"
)

const (
	walMagic             = "GSP2"
	walHeaderSize  int64 = 144
	walMaxGateway        = 130
	walFrameHeader int64 = 8 // uint32 bodyLen + uint32 crc32
	walCompactMin  int64 = 4 << 20
	liveBurstLimit       = 8
)

var errSpoolFrameCorrupt = errors.New("gateway spool frame is corrupt")

type queuedPublish struct {
	packet     *paho.Publish
	endSeq     int64
	receivedAt time.Time
	isReplay   bool
}

type overflowEntry struct {
	topic        string
	qos          byte
	retain       bool
	packetID     uint16
	logicalStart int64
	frameLen     int64
	endSeq       int64
	receivedAt   time.Time
}

type enqueueResult struct {
	memDepth      int
	overflowDepth int
	shed          bool
}

type mailboxLane struct {
	gatewayID       string
	isReplay        bool
	memCap          int
	spoolPath       string
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
}

// gatewayMailbox maintains bounded, per-Gateway live and replay WAL disk spools with a shared
// in-RAM hot cache. Live events, statuses, and telemetry drain ahead of replay backlogs while
// a bounded starvation guard yields one replay dequeue after liveBurstLimit consecutive live items.
type gatewayMailbox struct {
	gatewayID        string
	memCap           int
	overflowMax      int
	overflowMaxBytes int64
	compactMinBytes  int64
	autoBackfillLag  time.Duration

	mu sync.Mutex
	mailboxLane
	replay         mailboxLane
	liveBurstCount int
	closed         bool
	notify         chan struct{}
}

func newGatewayMailbox(gatewayID string, memCap, overflowMax int, overflowMaxBytes int64, overflowDir string) *gatewayMailbox {
	capMem := max(1, memCap)
	return &gatewayMailbox{
		gatewayID:        gatewayID,
		memCap:           capMem,
		overflowMax:      max(0, overflowMax),
		overflowMaxBytes: overflowMaxBytes,
		compactMinBytes:  walCompactMin,
		mailboxLane: mailboxLane{
			gatewayID: gatewayID,
			memCap:    capMem,
			spoolPath: filepath.Join(overflowDir, safeGatewayFileName(gatewayID)),
			mem:       make([]queuedPublish, capMem),
		},
		replay: mailboxLane{
			gatewayID: gatewayID,
			isReplay:  true,
			memCap:    capMem,
			spoolPath: filepath.Join(overflowDir, safeGatewayReplayFileName(gatewayID)),
			mem:       make([]queuedPublish, capMem),
		},
		notify: make(chan struct{}, 1),
	}
}

func safeGatewayFileName(gatewayID string) string {
	return safeGatewaySpoolFileName(gatewayID, "")
}

func safeGatewayReplayFileName(gatewayID string) string {
	return safeGatewaySpoolFileName(gatewayID, "-replay")
}

func safeGatewaySpoolFileName(gatewayID, laneSuffix string) string {
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
		return builder.String() + laneSuffix + ".spool"
	}
	sum := sha256.Sum256([]byte(gatewayID))
	prefix := builder.String()
	if prefix == "" || prefix == "." || prefix == ".." {
		prefix = "gateway"
	}
	return fmt.Sprintf("%s-%s%s.spool", prefix, hex.EncodeToString(sum[:4]), laneSuffix)
}

func (mb *gatewayMailbox) live() *mailboxLane {
	return &mb.mailboxLane
}

func (lane *mailboxLane) overflowLenLocked() int {
	return len(lane.overflow) - lane.overflowHead
}

func (lane *mailboxLane) itemCountLocked() int {
	return lane.memCount + lane.overflowLenLocked()
}

func (lane *mailboxLane) activeBytesLocked() int64 {
	if lane.spoolFile == nil {
		return 0
	}
	writtenLogical := lane.fileBaseSeq + (lane.spoolSize - walHeaderSize)
	if writtenLogical <= lane.consumedLogical {
		return 0
	}
	return writtenLogical - lane.consumedLogical
}

func (lane *mailboxLane) hasUnconsumedLocked() bool {
	return lane.memCount+lane.promoting+lane.overflowLenLocked() > 0 || lane.activeBytesLocked() > 0
}

func (mb *gatewayMailbox) memDepthLocked() int {
	return mb.live().memCount + mb.live().promoting + mb.replay.memCount + mb.replay.promoting
}

func (mb *gatewayMailbox) overflowDepthLocked() int {
	return mb.live().overflowLenLocked() + mb.replay.overflowLenLocked()
}

func (mb *gatewayMailbox) depth() (memDepth, overflowDepth int) {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	return mb.memDepthLocked(), mb.overflowDepthLocked()
}

func (mb *gatewayMailbox) signalLocked() {
	select {
	case mb.notify <- struct{}{}:
	default:
	}
}

func (lane *mailboxLane) pushMemLocked(item queuedPublish) {
	tail := (lane.memHead + lane.memCount) % lane.memCap
	lane.mem[tail] = item
	lane.memCount++
}

func (lane *mailboxLane) popMemLocked() (queuedPublish, bool) {
	if lane.memCount == 0 {
		return queuedPublish{}, false
	}
	item := lane.mem[lane.memHead]
	lane.mem[lane.memHead] = queuedPublish{}
	lane.memHead = (lane.memHead + 1) % lane.memCap
	lane.memCount--
	return item, true
}

func (lane *mailboxLane) popMemUpToLocked(limit int) []queuedPublish {
	if limit <= 0 || lane.memCount == 0 {
		return nil
	}
	count := min(limit, lane.memCount)
	out := make([]queuedPublish, 0, count)
	for i := 0; i < count; i++ {
		item, ok := lane.popMemLocked()
		if !ok {
			break
		}
		out = append(out, item)
	}
	return out
}

func (lane *mailboxLane) peekOverflowAtLocked(offset int) (overflowEntry, bool) {
	idx := lane.overflowHead + offset
	if idx < 0 || idx >= len(lane.overflow) {
		return overflowEntry{}, false
	}
	return lane.overflow[idx], true
}

func (lane *mailboxLane) advanceOverflowLocked(count int) {
	if count <= 0 {
		return
	}
	for i := 0; i < count && lane.overflowHead < len(lane.overflow); i++ {
		lane.overflow[lane.overflowHead] = overflowEntry{}
		lane.overflowHead++
	}
	if lane.overflowHead == len(lane.overflow) {
		lane.overflow = lane.overflow[:0]
		lane.overflowHead = 0
	} else if lane.overflowHead >= 64 && lane.overflowHead*2 >= len(lane.overflow) {
		remaining := len(lane.overflow) - lane.overflowHead
		copy(lane.overflow, lane.overflow[lane.overflowHead:])
		clear(lane.overflow[remaining:])
		lane.overflow = lane.overflow[:remaining]
		lane.overflowHead = 0
	}
}

func (lane *mailboxLane) prepareOverflowReadLocked(wantRead, wantPromote int) (peeked []overflowEntry, spool *os.File, baseSeq int64) {
	totalPeek := wantRead + wantPromote
	if totalPeek <= 0 {
		return nil, nil, 0
	}
	peeked = make([]overflowEntry, totalPeek)
	for i := 0; i < totalPeek; i++ {
		peeked[i], _ = lane.peekOverflowAtLocked(i)
	}
	lane.promoting = totalPeek
	return peeked, lane.spoolFile, lane.fileBaseSeq
}

func (lane *mailboxLane) finishOverflowReadLocked(consumedCount int, skippedSeq int64, promoted []queuedPublish, hadValid bool) {
	lane.advanceOverflowLocked(consumedCount)
	if skippedSeq > lane.consumedLogical && !hadValid {
		lane.consumedLogical = skippedSeq
		if lane.spoolFile != nil {
			_ = writeConsumedPhysOffset(lane.spoolFile, walHeaderSize+(lane.consumedLogical-lane.fileBaseSeq))
		}
	}
	for _, item := range promoted {
		lane.pushMemLocked(item)
	}
	lane.promoting = 0
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

func (lane *mailboxLane) ensureSpoolOpenLocked() error {
	if lane.spoolFile != nil {
		return nil
	}
	file, err := os.OpenFile(lane.spoolPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open gateway spool: %w", err)
	}
	hdr := encodeWALHeader(lane.gatewayID, walHeaderSize)
	if written, err := file.WriteAt(hdr, 0); err != nil || written != len(hdr) {
		_ = file.Close()
		if err == nil {
			err = errors.New("short write to gateway spool header")
		}
		return fmt.Errorf("write gateway spool header: %w", err)
	}
	lane.spoolFile = file
	lane.spoolSize = walHeaderSize
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

type replayEnvelopeProbe struct {
	Replay    bool            `json:"replay"`
	Timestamp json.RawMessage `json:"timestamp"`
	TS        json.RawMessage `json:"ts"`
	Payload   *struct {
		Devices []struct {
			DeviceTimestamp json.RawMessage `json:"deviceTimestamp"`
		} `json:"devices"`
	} `json:"payload"`
	Devs map[string]struct {
		TS json.RawMessage `json:"ts"`
	} `json:"devs"`
}

func isReplayUplink(topic string, payload []byte, receivedAt time.Time, autoBackfillLag time.Duration) bool {
	parsedTopic, err := ParseMessageTopic(mqttclient.StripSharePrefix(topic))
	if err != nil || parsedTopic.MessageType != MessageTypeTelemetry {
		return false
	}
	var probe replayEnvelopeProbe
	if err := json.Unmarshal(payload, &probe); err != nil {
		return false
	}
	if probe.Replay {
		return true
	}
	if autoBackfillLag <= 0 {
		return false
	}
	if probe.Payload != nil && len(probe.Payload.Devices) > 0 {
		for _, dev := range probe.Payload.Devices {
			tsRaw := dev.DeviceTimestamp
			if len(bytes.TrimSpace(tsRaw)) == 0 {
				tsRaw = probe.Timestamp
			}
			if isTimestampLagged(tsRaw, receivedAt, autoBackfillLag) {
				return true
			}
		}
		return false
	}
	if len(probe.Devs) > 0 {
		for _, dev := range probe.Devs {
			tsRaw := dev.TS
			if len(bytes.TrimSpace(tsRaw)) == 0 {
				tsRaw = probe.TS
			}
			if isTimestampLagged(tsRaw, receivedAt, autoBackfillLag) {
				return true
			}
		}
		return false
	}
	if len(bytes.TrimSpace(probe.Timestamp)) > 0 {
		return isTimestampLagged(probe.Timestamp, receivedAt, autoBackfillLag)
	}
	if len(bytes.TrimSpace(probe.TS)) > 0 {
		return isTimestampLagged(probe.TS, receivedAt, autoBackfillLag)
	}
	return false
}

func isTimestampLagged(raw json.RawMessage, receivedAt time.Time, lag time.Duration) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	ms, err := decodeFlexibleTimestamp(raw)
	if err != nil {
		return false
	}
	sampledAt := time.UnixMilli(ms).UTC()
	return receivedAt.Sub(sampledAt) > lag
}

func (mb *gatewayMailbox) enqueue(received *paho.Publish) (enqueueResult, error) {
	return mb.enqueueAt(received, time.Now().UTC())
}

// enqueueAt appends a message to the per-Gateway live or replay WAL spool without blocking on
// the worker. When count or byte limits are reached, shed=true is returned so the caller closes
// the connection without ACK and lets the broker redeliver later.
func (mb *gatewayMailbox) enqueueAt(received *paho.Publish, receivedAt time.Time) (enqueueResult, error) {
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	} else {
		receivedAt = receivedAt.UTC()
	}
	frame, err := encodeWALFrame(received.Topic, received.Payload)
	if err != nil {
		return enqueueResult{}, err
	}
	frameLen := int64(len(frame))
	isReplay := isReplayUplink(received.Topic, received.Payload, receivedAt, mb.autoBackfillLag)

	mb.mu.Lock()
	if mb.closed {
		mb.mu.Unlock()
		return enqueueResult{}, errors.New("gateway mailbox closed")
	}
	totalDepth := mb.live().itemCountLocked() + mb.replay.itemCountLocked()
	totalCap := mb.memCap + mb.overflowMax
	effectiveCap := totalCap
	effectiveMaxBytes := mb.overflowMaxBytes
	if isReplay && mb.overflowMax >= 16 {
		effectiveCap -= max(1, mb.overflowMax/8)
		if effectiveMaxBytes >= 1<<20 {
			effectiveMaxBytes -= effectiveMaxBytes / 8
		}
	}
	totalActiveBytes := mb.live().activeBytesLocked() + mb.replay.activeBytesLocked()
	if totalDepth >= effectiveCap || (effectiveMaxBytes > 0 && totalActiveBytes+frameLen > effectiveMaxBytes) {
		res := enqueueResult{
			memDepth:      mb.memDepthLocked(),
			overflowDepth: mb.overflowDepthLocked(),
			shed:          true,
		}
		mb.mu.Unlock()
		return res, nil
	}
	lane := mb.live()
	if isReplay {
		lane = &mb.replay
	}
	if err := lane.ensureSpoolOpenLocked(); err != nil {
		mb.mu.Unlock()
		return enqueueResult{}, err
	}
	entryPhys := lane.spoolSize
	if written, err := lane.spoolFile.WriteAt(frame, entryPhys); err != nil || written != len(frame) {
		mb.mu.Unlock()
		if err == nil {
			err = errors.New("short write to gateway spool")
		}
		return enqueueResult{}, fmt.Errorf("write gateway spool: %w", err)
	}
	lane.spoolSize += frameLen
	logicalStart := lane.fileBaseSeq + (entryPhys - walHeaderSize)
	logicalEnd := logicalStart + frameLen

	ramUsed := mb.memDepthLocked()
	if lane.overflowLenLocked() == 0 && lane.promoting == 0 && ramUsed < mb.memCap {
		packet := *received
		packet.Payload = append([]byte(nil), received.Payload...)
		lane.pushMemLocked(queuedPublish{
			packet:     &packet,
			endSeq:     logicalEnd,
			receivedAt: receivedAt,
			isReplay:   isReplay,
		})
	} else {
		lane.overflow = append(lane.overflow, overflowEntry{
			topic:        received.Topic,
			qos:          received.QoS,
			retain:       received.Retain,
			packetID:     received.PacketID,
			logicalStart: logicalStart,
			frameLen:     frameLen,
			endSeq:       logicalEnd,
			receivedAt:   receivedAt,
		})
	}
	res := enqueueResult{
		memDepth:      mb.memDepthLocked(),
		overflowDepth: mb.overflowDepthLocked(),
	}
	mb.signalLocked()
	mb.mu.Unlock()
	return res, nil
}

// popBatch drains up to maxItems, prioritizing the live lane ahead of the replay lane with a
// bounded starvation guard (1 replay dequeue after liveBurstLimit consecutive live dequeues).
// Each lane keeps strict FIFO order and promotes head overflow records back into RAM.
func (mb *gatewayMailbox) popBatch(maxItems int) (items []queuedPublish, memDepth, overflowDepth int, readErrors int) {
	if maxItems < 1 {
		maxItems = 1
	}
	mb.mu.Lock()
	liveAvail := mb.live().itemCountLocked()
	replayAvail := mb.replay.itemCountLocked()
	if liveAvail == 0 && replayAvail == 0 {
		mb.liveBurstCount = 0
		mb.mu.Unlock()
		return nil, 0, 0, 0
	}

	plan := make([]bool, 0, min(maxItems, liveAvail+replayAvail))
	burst := mb.liveBurstCount
	liveRem := liveAvail
	replayRem := replayAvail
	wantLive := 0
	wantReplay := 0
	for len(plan) < maxItems && (liveRem > 0 || replayRem > 0) {
		if liveRem == 0 || (replayRem > 0 && burst >= liveBurstLimit) {
			plan = append(plan, true)
			replayRem--
			wantReplay++
			burst = 0
		} else {
			plan = append(plan, false)
			liveRem--
			wantLive++
			burst++
		}
	}

	liveMemItems := mb.live().popMemUpToLocked(wantLive)
	replayMemItems := mb.replay.popMemUpToLocked(wantReplay)

	liveNeedOvf := wantLive - len(liveMemItems)
	replayNeedOvf := wantReplay - len(replayMemItems)

	ramUsed := mb.live().memCount + mb.replay.memCount
	ramHeadroom := max(0, mb.memCap-ramUsed)
	livePromote := min(ramHeadroom, max(0, mb.live().overflowLenLocked()-liveNeedOvf))
	replayPromote := min(max(0, ramHeadroom-livePromote), max(0, mb.replay.overflowLenLocked()-replayNeedOvf))

	livePeek, liveSpool, liveBaseSeq := mb.live().prepareOverflowReadLocked(liveNeedOvf, livePromote)
	replayPeek, replaySpool, replayBaseSeq := mb.replay.prepareOverflowReadLocked(replayNeedOvf, replayPromote)

	if len(livePeek) == 0 && len(replayPeek) == 0 {
		items = assemblePlannedBatch(plan, liveMemItems, replayMemItems)
		mb.updateBurstCountLocked(items)
		memDepth = mb.live().memCount + mb.replay.memCount
		overflowDepth = mb.overflowDepthLocked()
		mb.mu.Unlock()
		return items, memDepth, overflowDepth, 0
	}
	mb.mu.Unlock()

	liveOvfItems, livePromoted, liveConsumed, liveSkippedSeq, liveErrs := readLaneOverflow(liveSpool, liveBaseSeq, livePeek, liveNeedOvf, len(liveMemItems) > 0, false)
	replayOvfItems, replayPromoted, replayConsumed, replaySkippedSeq, replayErrs := readLaneOverflow(replaySpool, replayBaseSeq, replayPeek, replayNeedOvf, len(replayMemItems) > 0, true)

	liveItems := append(liveMemItems, liveOvfItems...)
	replayItems := append(replayMemItems, replayOvfItems...)
	items = assemblePlannedBatch(plan, liveItems, replayItems)
	readErrors = liveErrs + replayErrs

	mb.mu.Lock()
	mb.live().finishOverflowReadLocked(liveConsumed, liveSkippedSeq, livePromoted, len(liveItems) > 0 || len(livePromoted) > 0)
	mb.replay.finishOverflowReadLocked(replayConsumed, replaySkippedSeq, replayPromoted, len(replayItems) > 0 || len(replayPromoted) > 0)
	mb.updateBurstCountLocked(items)
	memDepth = mb.live().memCount + mb.replay.memCount
	overflowDepth = mb.overflowDepthLocked()
	mb.mu.Unlock()
	return items, memDepth, overflowDepth, readErrors
}

func (mb *gatewayMailbox) updateBurstCountLocked(items []queuedPublish) {
	burst := mb.liveBurstCount
	for _, item := range items {
		if item.isReplay {
			burst = 0
		} else {
			burst++
		}
	}
	if mb.live().itemCountLocked() == 0 && mb.replay.itemCountLocked() == 0 {
		burst = 0
	}
	mb.liveBurstCount = burst
}

func assemblePlannedBatch(plan []bool, liveItems, replayItems []queuedPublish) []queuedPublish {
	items := make([]queuedPublish, 0, len(liveItems)+len(replayItems))
	liveIdx := 0
	replayIdx := 0
	for _, isReplay := range plan {
		if isReplay {
			if replayIdx < len(replayItems) {
				items = append(items, replayItems[replayIdx])
				replayIdx++
			}
		} else if liveIdx < len(liveItems) {
			items = append(items, liveItems[liveIdx])
			liveIdx++
		}
	}
	if liveIdx < len(liveItems) {
		items = append(items, liveItems[liveIdx:]...)
	}
	if replayIdx < len(replayItems) {
		items = append(items, replayItems[replayIdx:]...)
	}
	return items
}

func readLaneOverflow(
	spool *os.File,
	baseSeq int64,
	peeked []overflowEntry,
	wantRead int,
	hasEarlierValid bool,
	isReplay bool,
) (items []queuedPublish, promoted []queuedPublish, consumedCount int, skippedSeq int64, readErrors int) {
	if len(peeked) == 0 {
		return nil, nil, 0, 0, 0
	}
	wantPromote := len(peeked) - wantRead
	if wantPromote > 0 {
		promoted = make([]queuedPublish, 0, wantPromote)
	}
	for i, entry := range peeked {
		physOffset := walHeaderSize + (entry.logicalStart - baseSeq)
		item, err := readSpooledPublish(spool, physOffset, entry, isReplay)
		if err != nil {
			if errors.Is(err, errSpoolFrameCorrupt) && (hasEarlierValid || len(items) > 0 || len(promoted) > 0) {
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
	return items, promoted, consumedCount, skippedSeq, readErrors
}

func (mb *gatewayMailbox) markConsumed(logicalEnd int64) {
	mb.markLaneConsumed(logicalEnd, 0)
}

func (mb *gatewayMailbox) markBatchConsumed(items []queuedPublish) {
	var liveEnd, replayEnd int64
	for _, item := range items {
		if item.isReplay {
			if item.endSeq > replayEnd {
				replayEnd = item.endSeq
			}
		} else if item.endSeq > liveEnd {
			liveEnd = item.endSeq
		}
	}
	mb.markLaneConsumed(liveEnd, replayEnd)
}

func (mb *gatewayMailbox) markLaneConsumed(liveEnd, replayEnd int64) {
	if liveEnd <= 0 && replayEnd <= 0 {
		return
	}
	mb.mu.Lock()
	defer mb.mu.Unlock()
	if liveEnd > 0 {
		mb.live().markConsumedLocked(liveEnd, mb.compactMinBytes)
	}
	if replayEnd > 0 {
		mb.replay.markConsumedLocked(replayEnd, mb.compactMinBytes)
	}
}

func (lane *mailboxLane) markConsumedLocked(logicalEnd, compactMinBytes int64) {
	if logicalEnd <= 0 {
		return
	}
	if logicalEnd > lane.consumedLogical {
		lane.consumedLogical = logicalEnd
	}
	if lane.spoolFile == nil {
		return
	}
	writtenLogical := lane.fileBaseSeq + (lane.spoolSize - walHeaderSize)
	if lane.memCount == 0 && lane.promoting == 0 && lane.overflowLenLocked() == 0 && lane.consumedLogical >= writtenLogical {
		if lane.spoolSize > walHeaderSize {
			_ = lane.spoolFile.Truncate(walHeaderSize)
			lane.spoolSize = walHeaderSize
		}
		lane.fileBaseSeq = lane.consumedLogical
		_ = writeConsumedPhysOffset(lane.spoolFile, walHeaderSize)
		return
	}

	reclaimable := lane.consumedLogical - lane.fileBaseSeq
	totalData := lane.spoolSize - walHeaderSize
	_ = writeConsumedPhysOffset(lane.spoolFile, walHeaderSize+reclaimable)
	if lane.promoting == 0 && reclaimable > 0 && reclaimable >= compactMinBytes && reclaimable*2 >= totalData {
		lane.compactSpoolLocked(reclaimable)
	}
}

func (lane *mailboxLane) compactSpoolLocked(reclaimable int64) {
	srcOffset := walHeaderSize + reclaimable
	liveBytes := lane.spoolSize - srcOffset
	if liveBytes < 0 || lane.spoolFile == nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(lane.spoolPath), filepath.Base(lane.spoolPath)+".compact.*")
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
	hdr := encodeWALHeader(lane.gatewayID, walHeaderSize)
	if wn, werr := tmp.WriteAt(hdr, 0); werr != nil || wn != len(hdr) {
		return
	}
	dstOffset := walHeaderSize
	buf := make([]byte, 64*1024)
	copied := int64(0)
	for copied < liveBytes {
		toRead := min(int64(len(buf)), liveBytes-copied)
		n, rerr := lane.spoolFile.ReadAt(buf[:toRead], srcOffset+copied)
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
	if err := os.Rename(tmpName, lane.spoolPath); err != nil {
		return
	}
	cleanupTmp = false
	oldFile := lane.spoolFile
	lane.spoolFile = tmp
	lane.fileBaseSeq += reclaimable
	lane.spoolSize = walHeaderSize + liveBytes
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

func readSpooledPublish(file *os.File, physOffset int64, entry overflowEntry, isReplay bool) (queuedPublish, error) {
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
		endSeq:     entry.endSeq,
		receivedAt: entry.receivedAt,
		isReplay:   isReplay,
	}, nil
}

func recoverSpoolMailboxes(overflowDir string, memCap, overflowMax int, overflowMaxBytes int64) (map[string]*gatewayMailbox, int, error) {
	entries, err := os.ReadDir(overflowDir)
	if err != nil {
		return nil, 0, err
	}
	// Recover non-replay spools first so live items receive in-RAM hot cache priority.
	sort.Slice(entries, func(i, j int) bool {
		iReplay := strings.HasSuffix(entries[i].Name(), "-replay.spool")
		jReplay := strings.HasSuffix(entries[j].Name(), "-replay.spool")
		if iReplay != jReplay {
			return !iReplay
		}
		return entries[i].Name() < entries[j].Name()
	})
	mailboxes := make(map[string]*gatewayMailbox)
	recoveredCount := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".spool") {
			continue
		}
		path := filepath.Join(overflowDir, entry.Name())
		count := recoverSpoolFile(path, entry.Name(), mailboxes, memCap, overflowMax, overflowMaxBytes)
		recoveredCount += count
	}
	return mailboxes, recoveredCount, nil
}

func recoverSpoolFile(
	path, fileName string,
	mailboxes map[string]*gatewayMailbox,
	memCap, overflowMax int,
	overflowMaxBytes int64,
) int {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return 0
	}
	info, err := file.Stat()
	if err != nil || info.Size() < walHeaderSize {
		_ = file.Close()
		return 0
	}
	hdr := make([]byte, walHeaderSize)
	if n, err := file.ReadAt(hdr, 0); err != nil || n != len(hdr) {
		_ = file.Close()
		return 0
	}
	consumedPhys, gatewayID, ok := decodeWALHeader(hdr)
	if !ok {
		_ = file.Close()
		return 0
	}
	if consumedPhys >= info.Size() {
		_ = file.Truncate(walHeaderSize)
		_ = writeConsumedPhysOffset(file, walHeaderSize)
		_ = file.Close()
		return 0
	}

	isReplay := fileName == safeGatewayReplayFileName(gatewayID)
	mb, existing := mailboxes[gatewayID]
	if !existing {
		mb = newGatewayMailbox(gatewayID, memCap, overflowMax, overflowMaxBytes, filepath.Dir(path))
	}
	lane := mb.live()
	if isReplay {
		lane = &mb.replay
	}
	lane.spoolPath = path
	lane.spoolFile = file
	lane.consumedLogical = consumedPhys - walHeaderSize

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
		if lane.overflowLenLocked() == 0 && mb.memDepthLocked() < mb.memCap {
			lane.pushMemLocked(queuedPublish{
				packet:   &paho.Publish{Topic: topic, Payload: payload, QoS: 1},
				endSeq:   logicalEnd,
				isReplay: isReplay,
			})
		} else {
			lane.overflow = append(lane.overflow, overflowEntry{
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
	lane.spoolSize = pos
	if recovered == 0 {
		_ = file.Truncate(walHeaderSize)
		_ = writeConsumedPhysOffset(file, walHeaderSize)
		_ = file.Close()
		lane.spoolFile = nil
		lane.spoolSize = 0
		lane.consumedLogical = 0
		return 0
	}
	if !existing {
		mailboxes[gatewayID] = mb
	}
	mb.signalLocked()
	return recovered
}

func (lane *mailboxLane) closeLocked(removeFile bool) (*os.File, string) {
	file := lane.spoolFile
	lane.spoolFile = nil
	if !removeFile {
		return file, ""
	}
	return file, lane.spoolPath
}

func (mb *gatewayMailbox) hasUnconsumed() bool {
	if mb == nil {
		return false
	}
	mb.mu.Lock()
	defer mb.mu.Unlock()
	return mb.live().hasUnconsumedLocked() || mb.replay.hasUnconsumedLocked()
}

func (mb *gatewayMailbox) close(removeFile bool) {
	mb.mu.Lock()
	if mb.closed {
		mb.mu.Unlock()
		return
	}
	mb.closed = true
	liveFile, livePath := mb.live().closeLocked(removeFile)
	replayFile, replayPath := mb.replay.closeLocked(removeFile)
	mb.mu.Unlock()

	for _, target := range []struct {
		file *os.File
		path string
	}{
		{file: liveFile, path: livePath},
		{file: replayFile, path: replayPath},
	} {
		if target.file != nil {
			_ = target.file.Close()
			if removeFile && target.path != "" {
				_ = os.Remove(target.path)
			}
		}
	}
}
