package edgecontrol

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordedModbusRead struct {
	fc   ModbusFunctionCode
	addr uint16
	qty  uint16
}

type stubDeclarativeTransport struct {
	mu          sync.Mutex
	holding     map[uint16]uint16
	input       map[uint16]uint16
	readErr     error
	writeErr    error
	reads       []recordedModbusRead
	writes      []ModbusWriteTask
	closedCount atomic.Int32
}

func newStubDeclarativeTransport() *stubDeclarativeTransport {
	return &stubDeclarativeTransport{
		holding: make(map[uint16]uint16),
		input:   make(map[uint16]uint16),
	}
}

func (s *stubDeclarativeTransport) Read(_ context.Context, task ModbusReadTask) ([]uint16, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = append(s.reads, recordedModbusRead{
		fc:   task.FunctionCode,
		addr: task.Address,
		qty:  task.Quantity,
	})
	if s.readErr != nil {
		return nil, s.readErr
	}
	out := make([]uint16, task.Quantity)
	src := s.holding
	if task.FunctionCode == ModbusFunctionReadInputRegisters {
		src = s.input
	}
	for i := uint16(0); i < task.Quantity; i++ {
		out[i] = src[task.Address+i]
	}
	return out, nil
}

func (s *stubDeclarativeTransport) Write(_ context.Context, task ModbusWriteTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := task
	cp.Values = append([]uint16(nil), task.Values...)
	s.writes = append(s.writes, cp)
	if s.writeErr != nil {
		return s.writeErr
	}
	for i, v := range task.Values {
		s.holding[task.Address+uint16(i)] = v
	}
	return nil
}

func (s *stubDeclarativeTransport) Close() error {
	s.closedCount.Add(1)
	return nil
}

func (s *stubDeclarativeTransport) snapshotReads() []recordedModbusRead {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]recordedModbusRead, len(s.reads))
	copy(cp, s.reads)
	return cp
}

func (s *stubDeclarativeTransport) clearReads() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = nil
}

func (s *stubDeclarativeTransport) setReadError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readErr = err
}

func (s *stubDeclarativeTransport) setHolding(addr uint16, vals ...uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, v := range vals {
		s.holding[addr+uint16(i)] = v
	}
}

func TestCoalesceModbusReadBlocks(t *testing.T) {
	t.Parallel()

	specs := []ModbusRegisterSpec{
		// Same FC3 + HIGH cadence, gap = 3 (100+1 -> 104) => coalesced into [100, qty 5]
		{PointCode: "p1", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 100, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityHigh},
		{PointCode: "p2", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 104, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityHigh},
		// Gap = 5 from 105 -> 110 => new block [110, qty 2]
		{PointCode: "p3", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 110, RegisterCount: 2, RawType: ModbusRawFloat32, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityHigh},
		// Overlapping BIT_BOOL on FC4 addr 300 should coalesce into single register block
		{PointCode: "b0", FunctionCode: ModbusFunctionReadInputRegisters, Address: 300, RegisterCount: 1, RawType: ModbusRawBitBool, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, BitIndex: 0, PollPriority: PriorityHigh},
		{PointCode: "b5", FunctionCode: ModbusFunctionReadInputRegisters, Address: 300, RegisterCount: 1, RawType: ModbusRawBitBool, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, BitIndex: 5, PollPriority: PriorityHigh},
		// MEDIUM cadence kept separate from HIGH cadence even if adjacent
		{PointCode: "m1", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 101, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityMedium},
	}
	// Span > 64 check via dense chain (gap <= 4): 200, 204, ..., 260 (span 61) + 262 (span 63), then 265 (span 66 > 64 => split)
	for addr := uint16(200); addr <= 260; addr += 4 {
		specs = append(specs, ModbusRegisterSpec{
			PointCode: "s_" + string(rune('a'+(addr-200)/4)), FunctionCode: ModbusFunctionReadHoldingRegisters,
			Address: addr, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityVeryHigh,
		})
	}
	specs = append(specs,
		ModbusRegisterSpec{PointCode: "s_tail", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 262, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityVeryHigh},
		ModbusRegisterSpec{PointCode: "s_over", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 265, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityVeryHigh},
	)

	blocks := CoalesceModbusReadBlocks(specs)
	if len(blocks) != 6 {
		t.Fatalf("CoalesceModbusReadBlocks returned %d blocks, want 6: %+v", len(blocks), blocks)
	}

	findBlock := func(fc ModbusFunctionCode, start uint16) (ModbusReadBlock, bool) {
		for _, b := range blocks {
			if b.FunctionCode == fc && b.StartAddress == start {
				return b, true
			}
		}
		return ModbusReadBlock{}, false
	}

	b100, ok := findBlock(ModbusFunctionReadHoldingRegisters, 100)
	if !ok || b100.Quantity != 5 || len(b100.Specs) != 2 {
		t.Fatalf("expected coalesced FC3 block at 100 qty 5 specs 2, got %+v (found=%v)", b100, ok)
	}
	b110, ok := findBlock(ModbusFunctionReadHoldingRegisters, 110)
	if !ok || b110.Quantity != 2 || len(b110.Specs) != 1 {
		t.Fatalf("expected split FC3 block at 110 qty 2, got %+v (found=%v)", b110, ok)
	}
	b200, ok := findBlock(ModbusFunctionReadHoldingRegisters, 200)
	if !ok || b200.Quantity != 63 || len(b200.Specs) != 17 {
		t.Fatalf("expected bounded FC3 block at 200 qty 63 specs 17, got %+v (found=%v)", b200, ok)
	}
	b265, ok := findBlock(ModbusFunctionReadHoldingRegisters, 265)
	if !ok || b265.Quantity != 1 || len(b265.Specs) != 1 {
		t.Fatalf("expected overflow split FC3 block at 265 qty 1, got %+v (found=%v)", b265, ok)
	}
	b300, ok := findBlock(ModbusFunctionReadInputRegisters, 300)
	if !ok || b300.Quantity != 1 || len(b300.Specs) != 2 {
		t.Fatalf("expected shared FC4 bit block at 300 qty 1 specs 2, got %+v (found=%v)", b300, ok)
	}
	b101Med, ok := findBlock(ModbusFunctionReadHoldingRegisters, 101)
	if !ok || b101Med.Cadence != 2 || b101Med.Quantity != 1 {
		t.Fatalf("expected MEDIUM block at 101 cadence 2, got %+v (found=%v)", b101Med, ok)
	}
}

func TestDeclarativeModbusPriorityPollingCadence(t *testing.T) {
	t.Parallel()

	transport := newStubDeclarativeTransport()
	transport.setHolding(1, 10)
	transport.setHolding(20, 20)
	transport.setHolding(40, 30)
	transport.setHolding(60, 40)

	profile := DeclarativeModbusProfile{
		ComponentID: "PLC-CADENCE",
		DriverType:  ModbusDriverTCP,
		Endpoint:    "127.0.0.1:5020",
		UnitID:      1,
		TimeoutMS:   500,
		Retries:     1,
		Registers: []ModbusRegisterSpec{
			{PointCode: "vh_pt", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 1, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityVeryHigh},
			{PointCode: "h_pt", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 20, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityHigh},
			{PointCode: "m_pt", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 40, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityMedium},
			{PointCode: "l_pt", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 60, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityLow},
		},
	}

	adapter, err := NewDeclarativeModbusAdapter(profile, transport)
	if err != nil {
		t.Fatalf("NewDeclarativeModbusAdapter() error = %v", err)
	}

	baseTime := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

	readMap := func(updates []ChannelUpdate) map[string]ChannelUpdate {
		m := make(map[string]ChannelUpdate, len(updates))
		for _, u := range updates {
			m[u.Address] = u
		}
		return m
	}

	// Cycle 0: all 4 priorities polled
	updates, err := adapter.Poll(context.Background(), baseTime)
	if err != nil {
		t.Fatalf("cycle 0 Poll() error = %v", err)
	}
	if len(transport.snapshotReads()) != 4 {
		t.Fatalf("cycle 0 expected 4 block reads, got %d", len(transport.snapshotReads()))
	}
	if got := readMap(updates)["PLC-CADENCE/l_pt"].Sample.Value.Double; got != 40 {
		t.Fatalf("cycle 0 low value = %v, want 40", got)
	}

	// Update underlying registers while cycle 1 runs: VH and H refresh, M and L serve cached
	transport.clearReads()
	transport.setHolding(1, 11)
	transport.setHolding(20, 21)
	transport.setHolding(40, 31)
	transport.setHolding(60, 41)

	updates, err = adapter.Poll(context.Background(), baseTime.Add(time.Second))
	if err != nil {
		t.Fatalf("cycle 1 Poll() error = %v", err)
	}
	reads := transport.snapshotReads()
	if len(reads) != 2 {
		t.Fatalf("cycle 1 expected 2 reads (VERY_HIGH+HIGH), got %d: %+v", len(reads), reads)
	}
	um := readMap(updates)
	if um["PLC-CADENCE/vh_pt"].Sample.Value.Double != 11 || um["PLC-CADENCE/h_pt"].Sample.Value.Double != 21 {
		t.Fatalf("cycle 1 did not refresh VH/H: %+v", um)
	}
	if um["PLC-CADENCE/m_pt"].Sample.Value.Double != 30 || um["PLC-CADENCE/l_pt"].Sample.Value.Double != 40 {
		t.Fatalf("cycle 1 should serve cached M=30 L=40, got M=%v L=%v", um["PLC-CADENCE/m_pt"].Sample.Value.Double, um["PLC-CADENCE/l_pt"].Sample.Value.Double)
	}

	// Cycle 2: VH, H, and MEDIUM polled; LOW still cached (40)
	transport.clearReads()
	updates, err = adapter.Poll(context.Background(), baseTime.Add(2*time.Second))
	if err != nil {
		t.Fatalf("cycle 2 Poll() error = %v", err)
	}
	if len(transport.snapshotReads()) != 3 {
		t.Fatalf("cycle 2 expected 3 reads (VH+H+M), got %d", len(transport.snapshotReads()))
	}
	um = readMap(updates)
	if um["PLC-CADENCE/m_pt"].Sample.Value.Double != 31 {
		t.Fatalf("cycle 2 should refresh M=31, got %v", um["PLC-CADENCE/m_pt"].Sample.Value.Double)
	}
	if um["PLC-CADENCE/l_pt"].Sample.Value.Double != 40 {
		t.Fatalf("cycle 2 should still cache L=40, got %v", um["PLC-CADENCE/l_pt"].Sample.Value.Double)
	}

	// Cycle 3: only VH and H
	transport.clearReads()
	_, err = adapter.Poll(context.Background(), baseTime.Add(3*time.Second))
	if err != nil {
		t.Fatalf("cycle 3 Poll() error = %v", err)
	}
	if len(transport.snapshotReads()) != 2 {
		t.Fatalf("cycle 3 expected 2 reads, got %d", len(transport.snapshotReads()))
	}

	// Cycle 4: all 4 blocks including LOW refreshed to 41
	transport.clearReads()
	updates, err = adapter.Poll(context.Background(), baseTime.Add(4*time.Second))
	if err != nil {
		t.Fatalf("cycle 4 Poll() error = %v", err)
	}
	if len(transport.snapshotReads()) != 4 {
		t.Fatalf("cycle 4 expected 4 reads, got %d", len(transport.snapshotReads()))
	}
	if got := readMap(updates)["PLC-CADENCE/l_pt"].Sample.Value.Double; got != 41 {
		t.Fatalf("cycle 4 low value = %v, want 41", got)
	}
}

func TestDeclarativeModbusFaultBackoffAndRecovery(t *testing.T) {
	t.Parallel()

	transport := newStubDeclarativeTransport()
	transport.setHolding(10, 250)
	transport.setHolding(30, 50)

	profile := DeclarativeModbusProfile{
		ComponentID: "CHILLER-FAULT",
		DriverType:  ModbusDriverTCP,
		Endpoint:    "127.0.0.1:5020",
		UnitID:      1,
		TimeoutMS:   500,
		Retries:     0,
		Registers: []ModbusRegisterSpec{
			{PointCode: "supply_temp", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 10, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 0.1, PollPriority: PriorityHigh},
			{PointCode: "run_hours", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 30, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityLow},
		},
	}

	adapter, err := NewDeclarativeModbusAdapter(profile, transport)
	if err != nil {
		t.Fatalf("NewDeclarativeModbusAdapter() error = %v", err)
	}
	host, err := NewHost()
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	if err := host.RegisterAdapter(adapter); err != nil {
		t.Fatalf("RegisterAdapter() error = %v", err)
	}
	if err := host.Start(nil); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	t0 := time.Date(2026, 11, 17, 11, 0, 0, 0, time.UTC)
	if _, err := host.RunCycle(context.Background(), t0); err != nil {
		t.Fatalf("initial RunCycle error = %v", err)
	}

	// Inject fault at t0+1s -> failures=1, backoff 2s (until t0+3s), channels become QualityBad(COMMUNICATION_FAULT)
	transport.setReadError(errors.New("modbus timeout"))
	transport.clearReads()
	res, err := host.RunCycle(context.Background(), t0.Add(time.Second))
	if err != nil {
		t.Fatalf("RunCycle should absorb device poll error into image quality, got %v", err)
	}
	snap, ok := res.Cycle.Image.Get("CHILLER-FAULT/supply_temp")
	if !ok || snap.Sample.Quality != QualityBad || snap.Sample.QualityReason != QualityReasonCommunicationFault {
		t.Fatalf("expected QualityBad(COMMUNICATION_FAULT), got %+v (ok=%v)", snap, ok)
	}
	status := adapter.FaultStatus()
	if !status.FaultActive || status.ConsecutiveFailures != 1 || !status.NextRetryAt.Equal(t0.Add(3*time.Second)) {
		t.Fatalf("unexpected fault status after 1st failure: %+v", status)
	}

	// During backoff (t0+2s < t0+3s): transport must NOT be hit, channels remain QualityBad
	transport.clearReads()
	if _, err := host.RunCycle(context.Background(), t0.Add(2*time.Second)); err != nil {
		t.Fatalf("backoff RunCycle error = %v", err)
	}
	if len(transport.snapshotReads()) != 0 {
		t.Fatalf("expected 0 bus reads during backoff, got %d", len(transport.snapshotReads()))
	}

	// At nextRetryAt (t0+3s): retries once, fails -> failures=2, backoff=4s (until t0+7s)
	transport.clearReads()
	if _, err := host.RunCycle(context.Background(), t0.Add(3*time.Second)); err != nil {
		t.Fatalf("retry RunCycle error = %v", err)
	}
	if len(transport.snapshotReads()) != 1 {
		t.Fatalf("expected 1 retry read at backoff expiry, got %d", len(transport.snapshotReads()))
	}
	status = adapter.FaultStatus()
	if status.ConsecutiveFailures != 2 || !status.NextRetryAt.Equal(t0.Add(7*time.Second)) {
		t.Fatalf("unexpected fault status after 2nd failure: %+v", status)
	}

	// Keep failing past 150 failures to prove backoff caps at 5m
	cursor := status.NextRetryAt
	for i := 3; i <= 155; i++ {
		if _, err := adapter.Poll(context.Background(), cursor); !errors.Is(err, ErrCommunicationFault) {
			t.Fatalf("expected ErrCommunicationFault on failure %d, got %v", i, err)
		}
		st := adapter.FaultStatus()
		expectedDelay := time.Duration(i) * 2 * time.Second
		if expectedDelay > 5*time.Minute {
			expectedDelay = 5 * time.Minute
		}
		if gotDelay := st.NextRetryAt.Sub(cursor); gotDelay != expectedDelay {
			t.Fatalf("failure %d backoff = %s, want %s", i, gotDelay, expectedDelay)
		}
		cursor = st.NextRetryAt
	}

	// Clear error and advance to cursor: recovery must immediately reset backoff AND refresh even LOW cadence cached blocks
	transport.setReadError(nil)
	transport.setHolding(10, 72)
	transport.setHolding(30, 88)
	transport.clearReads()

	res, err = host.RunCycle(context.Background(), cursor)
	if err != nil {
		t.Fatalf("recovery RunCycle error = %v", err)
	}
	if len(transport.snapshotReads()) != 2 {
		t.Fatalf("recovery should re-read all invalidated blocks (2), got %d", len(transport.snapshotReads()))
	}
	status = adapter.FaultStatus()
	if status.FaultActive || status.ConsecutiveFailures != 0 || !status.NextRetryAt.IsZero() {
		t.Fatalf("fault status should clear immediately on recovery: %+v", status)
	}
	snap, _ = res.Cycle.Image.Get("CHILLER-FAULT/supply_temp")
	if snap.Sample.Quality != QualityGood || snap.Sample.QualityReason != "" || math.Abs(snap.Sample.Value.Double-7.2) > 1e-6 {
		t.Fatalf("unexpected recovered supply_temp snapshot: %+v", snap)
	}
	lowSnap, _ := res.Cycle.Image.Get("CHILLER-FAULT/run_hours")
	if lowSnap.Sample.Quality != QualityGood || lowSnap.Sample.Value.Double != 88 {
		t.Fatalf("unexpected recovered run_hours snapshot: %+v", lowSnap)
	}
}

func TestDeclarativeModbusCodecAndWrites(t *testing.T) {
	t.Parallel()

	transport := newStubDeclarativeTransport()

	wordFromBytes := func(hi, lo byte) uint16 {
		return uint16(hi)<<8 | uint16(lo)
	}

	// Target float32 value: 12.5 => IEEE-754 bits 0x41480000 (bytes: 0x41, 0x48, 0x00, 0x00)
	transport.setHolding(10, wordFromBytes(0x41, 0x48), wordFromBytes(0x00, 0x00))
	transport.setHolding(12, wordFromBytes(0x00, 0x00), wordFromBytes(0x41, 0x48))
	transport.setHolding(14, wordFromBytes(0x48, 0x41), wordFromBytes(0x00, 0x00))
	transport.setHolding(16, wordFromBytes(0x00, 0x00), wordFromBytes(0x48, 0x41))

	// INT16 negative with offset/scale: raw -250 * 0.1 + 5.0 = -20.0 (LE byte order: 0xFF06 swapped -> 0x06FF)
	rawI16 := int16(-250)
	negRaw := uint16(rawI16)
	transport.setHolding(20, (negRaw>>8)|((negRaw&0xFF)<<8))
	// UINT32 = 70000 => 0x00011170, LE word order => [0x1170, 0x0001]
	transport.setHolding(22, 0x1170, 0x0001)
	// INT32 = -50000 => 0xFFFF3CB0, BE/BE => [0xFFFF, 0x3CB0]
	transport.setHolding(24, 0xFFFF, 0x3CB0)
	// BIT_BOOL bits 2 and 11 at addr 26
	transport.setHolding(26, (1<<2)|(1<<11))
	// ENUM16 value 2 => "COOLING"
	transport.setHolding(27, 2)
	// NaN float32 => 0x7FC00000
	transport.setHolding(28, 0x7FC0, 0x0000)

	profile := DeclarativeModbusProfile{
		ComponentID: "CODEC-DEV",
		DriverType:  ModbusDriverTCP,
		Endpoint:    "127.0.0.1:5020",
		UnitID:      1,
		TimeoutMS:   500,
		Retries:     0,
		Registers: []ModbusRegisterSpec{
			{PointCode: "f_be_be", FunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteMultipleRegisters, Address: 10, RegisterCount: 2, RawType: ModbusRawFloat32, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, Offset: 0, PollPriority: PriorityHigh},
			{PointCode: "f_be_le", FunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteMultipleRegisters, Address: 12, RegisterCount: 2, RawType: ModbusRawFloat32, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderLittleEndian, Scale: 1, Offset: 0, PollPriority: PriorityHigh},
			{PointCode: "f_le_be", FunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteMultipleRegisters, Address: 14, RegisterCount: 2, RawType: ModbusRawFloat32, ByteOrder: ModbusByteOrderLittleEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, Offset: 0, PollPriority: PriorityHigh},
			{PointCode: "f_le_le", FunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteMultipleRegisters, Address: 16, RegisterCount: 2, RawType: ModbusRawFloat32, ByteOrder: ModbusByteOrderLittleEndian, WordOrder: ModbusWordOrderLittleEndian, Scale: 1, Offset: 0, PollPriority: PriorityHigh},
			{PointCode: "i16_le", FunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteSingleRegister, Address: 20, RegisterCount: 1, RawType: ModbusRawInt16, ByteOrder: ModbusByteOrderLittleEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 0.1, Offset: 5, PollPriority: PriorityHigh},
			{PointCode: "u32_le", FunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteMultipleRegisters, Address: 22, RegisterCount: 2, RawType: ModbusRawUint32, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderLittleEndian, Scale: 1, Offset: 0, PollPriority: PriorityHigh},
			{PointCode: "i32_be", FunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteMultipleRegisters, Address: 24, RegisterCount: 2, RawType: ModbusRawInt32, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, Offset: 0, PollPriority: PriorityHigh},
			{PointCode: "bit_2", FunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteSingleRegister, Address: 26, RegisterCount: 1, RawType: ModbusRawBitBool, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, BitIndex: 2, Scale: 1, PollPriority: PriorityHigh},
			{PointCode: "bit_3", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 26, RegisterCount: 1, RawType: ModbusRawBitBool, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, BitIndex: 3, Scale: 1, PollPriority: PriorityHigh},
			{PointCode: "mode_enum", FunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteSingleRegister, Address: 27, RegisterCount: 1, RawType: ModbusRawEnum16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityHigh, EnumMap: map[string]string{"0": "OFF", "2": "COOLING"}},
			{PointCode: "f_nan", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 28, RegisterCount: 2, RawType: ModbusRawFloat32, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityHigh},
		},
	}

	adapter, err := NewDeclarativeModbusAdapter(profile, transport)
	if err != nil {
		t.Fatalf("NewDeclarativeModbusAdapter() error = %v", err)
	}

	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	updates, err := adapter.Poll(context.Background(), now)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	byAddr := make(map[string]Sample, len(updates))
	for _, u := range updates {
		byAddr[u.Address] = u.Sample
	}

	for _, name := range []string{"f_be_be", "f_be_le", "f_le_be", "f_le_le"} {
		s := byAddr["CODEC-DEV/"+name]
		if s.Quality != QualityGood || math.Abs(s.Value.Double-12.5) > 1e-6 {
			t.Fatalf("%s decoded as %+v, want 12.5", name, s)
		}
	}
	if s := byAddr["CODEC-DEV/i16_le"]; s.Quality != QualityGood || math.Abs(s.Value.Double-(-20.0)) > 1e-6 {
		t.Fatalf("i16_le = %+v, want -20.0", s)
	}
	if s := byAddr["CODEC-DEV/u32_le"]; s.Value.Double != 70000 {
		t.Fatalf("u32_le = %v, want 70000", s.Value.Double)
	}
	if s := byAddr["CODEC-DEV/i32_be"]; s.Value.Double != -50000 {
		t.Fatalf("i32_be = %v, want -50000", s.Value.Double)
	}
	if !byAddr["CODEC-DEV/bit_2"].Value.Boolean || byAddr["CODEC-DEV/bit_3"].Value.Boolean {
		t.Fatalf("unexpected bits: bit_2=%+v bit_3=%+v", byAddr["CODEC-DEV/bit_2"], byAddr["CODEC-DEV/bit_3"])
	}
	if byAddr["CODEC-DEV/mode_enum"].Value.String != "COOLING" {
		t.Fatalf("mode_enum = %+v, want COOLING", byAddr["CODEC-DEV/mode_enum"])
	}
	if byAddr["CODEC-DEV/f_nan"].Quality != QualityBad || byAddr["CODEC-DEV/f_nan"].QualityReason != QualityReasonNonFiniteFloat {
		t.Fatalf("expected f_nan QualityBad(NON_FINITE_FLOAT), got %+v", byAddr["CODEC-DEV/f_nan"])
	}

	// Test FC6 and FC16 writes + round-trip + invalid rejections
	vI16 := DoubleValue(-15.0) // (-15 - 5) / 0.1 = -200 => 0xFF38, LE swapped => 0x38FF
	vF32 := DoubleValue(27.5)
	vU32 := DoubleValue(131072) // 0x00020000
	vNaN := DoubleValue(math.NaN())
	vInf := DoubleValue(math.Inf(1))
	vReadOnly := BooleanValue(false)
	vEnum := StringValue("OFF")
	vOverflow := DoubleValue(1e9)

	decisions := []Decision{
		{Address: "CODEC-DEV/i16_le", Accepted: true, Effective: &vI16},
		{Address: "CODEC-DEV/f_le_le", Accepted: true, Effective: &vF32},
		{Address: "CODEC-DEV/u32_le", Accepted: true, Effective: &vU32},
		{Address: "CODEC-DEV/mode_enum", Accepted: true, Effective: &vEnum},
		{Address: "CODEC-DEV/f_le_be", Accepted: true, Effective: &vNaN},
		{Address: "CODEC-DEV/f_be_be", Accepted: true, Effective: &vInf},
		{Address: "CODEC-DEV/bit_3", Accepted: true, Effective: &vReadOnly},
		{Address: "CODEC-DEV/i16_le", Accepted: true, Effective: &vOverflow},
	}

	results, err := adapter.Apply(context.Background(), ProcessImage{}, decisions)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	resMap := make(map[string][]DeviceWriteResult)
	for _, r := range results {
		resMap[r.Address] = append(resMap[r.Address], r)
	}
	if !resMap["CODEC-DEV/i16_le"][0].Success || resMap["CODEC-DEV/i16_le"][1].Code != "VALUE_OUT_OF_RANGE" {
		t.Fatalf("unexpected i16_le write results: %+v", resMap["CODEC-DEV/i16_le"])
	}
	if resMap["CODEC-DEV/f_le_be"][0].Code != "INVALID_WRITE_VALUE" || resMap["CODEC-DEV/f_be_be"][0].Code != "INVALID_WRITE_VALUE" {
		t.Fatalf("expected NaN/Inf rejection, got %+v / %+v", resMap["CODEC-DEV/f_le_be"], resMap["CODEC-DEV/f_be_be"])
	}
	if resMap["CODEC-DEV/bit_3"][0].Code != "CHANNEL_READ_ONLY" {
		t.Fatalf("expected CHANNEL_READ_ONLY on bit_3, got %+v", resMap["CODEC-DEV/bit_3"])
	}

	// Re-poll after valid writes to prove round-trip decoding of FC6 & FC16 encoded registers
	updates, err = adapter.Poll(context.Background(), now.Add(time.Second))
	if err != nil {
		t.Fatalf("post-write Poll() error = %v", err)
	}
	for _, u := range updates {
		byAddr[u.Address] = u.Sample
	}
	if math.Abs(byAddr["CODEC-DEV/i16_le"].Value.Double-(-15.0)) > 1e-6 {
		t.Fatalf("round-trip i16_le = %v, want -15.0", byAddr["CODEC-DEV/i16_le"].Value.Double)
	}
	if math.Abs(byAddr["CODEC-DEV/f_le_le"].Value.Double-27.5) > 1e-5 {
		t.Fatalf("round-trip f_le_le = %v, want 27.5", byAddr["CODEC-DEV/f_le_le"].Value.Double)
	}
	if byAddr["CODEC-DEV/u32_le"].Value.Double != 131072 {
		t.Fatalf("round-trip u32_le = %v, want 131072", byAddr["CODEC-DEV/u32_le"].Value.Double)
	}
	if byAddr["CODEC-DEV/mode_enum"].Value.String != "OFF" {
		t.Fatalf("round-trip mode_enum = %v, want OFF", byAddr["CODEC-DEV/mode_enum"].Value.String)
	}
}

func TestDeclarativeModbusProfileValidationRejectsInvalidSpecs(t *testing.T) {
	t.Parallel()

	base := DeclarativeModbusProfile{
		ComponentID: "VALID-PLC",
		DriverType:  ModbusDriverTCP,
		Endpoint:    "127.0.0.1:5020",
		UnitID:      1,
		TimeoutMS:   1000,
		Retries:     1,
		Registers: []ModbusRegisterSpec{
			{PointCode: "chiller_temp", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 0, RegisterCount: 1, RawType: ModbusRawInt16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 0.1, PollPriority: PriorityHigh},
		},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("expected valid profile, got %v", err)
	}

	// FC4 input register cannot specify WriteFunctionCode
	badFC4Write := base
	badFC4Write.Registers = []ModbusRegisterSpec{{
		PointCode: "ro_input", FunctionCode: ModbusFunctionReadInputRegisters, WriteFunctionCode: ModbusFunctionWriteSingleRegister,
		Address: 0, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityHigh,
	}}
	if err := badFC4Write.Validate(); err == nil {
		t.Fatal("expected error for FC4 with writeFunctionCode")
	}

	// FC6 cannot have registerCount 2
	badFC6Count := base
	badFC6Count.Registers = []ModbusRegisterSpec{{
		PointCode: "wide_fc6", FunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteSingleRegister,
		Address: 0, RegisterCount: 2, RawType: ModbusRawFloat32, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityHigh,
	}}
	if err := badFC6Count.Validate(); err == nil {
		t.Fatal("expected error for FC6 with registerCount 2")
	}

	// Zero scale rejected for numeric, BIT_BOOL, and ENUM16 registers
	for _, rawType := range []ModbusRawType{ModbusRawUint16, ModbusRawBitBool, ModbusRawEnum16} {
		badScale := base
		spec := ModbusRegisterSpec{
			PointCode: "zero_scale", FunctionCode: ModbusFunctionReadHoldingRegisters,
			Address: 0, RegisterCount: 1, RawType: rawType, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 0, PollPriority: PriorityHigh,
		}
		if rawType == ModbusRawEnum16 {
			spec.EnumMap = map[string]string{"0": "OFF"}
		}
		badScale.Registers = []ModbusRegisterSpec{spec}
		if err := badScale.Validate(); err == nil {
			t.Fatalf("expected error for zero scale on %s register", rawType)
		}
	}

	// Address + RegisterCount exceeding 16-bit space rejected
	badSpace := base
	badSpace.Registers = []ModbusRegisterSpec{{
		PointCode: "overflow_addr", FunctionCode: ModbusFunctionReadHoldingRegisters,
		Address: 65535, RegisterCount: 2, RawType: ModbusRawFloat32, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityHigh,
	}}
	if err := badSpace.Validate(); err == nil {
		t.Fatal("expected error for address + registerCount > 65536")
	}

	// Non-uint16 or non-canonical ENUM16 keys rejected
	for _, badKey := range []string{"01", "-1", "65536", "abc"} {
		badEnum := base
		badEnum.Registers = []ModbusRegisterSpec{{
			PointCode: "bad_enum", FunctionCode: ModbusFunctionReadHoldingRegisters,
			Address: 0, RegisterCount: 1, RawType: ModbusRawEnum16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityHigh,
			EnumMap: map[string]string{badKey: "BAD"},
		}}
		if err := badEnum.Validate(); err == nil {
			t.Fatalf("expected error for ENUM16 key %q", badKey)
		}
	}

	// Invalid TCP endpoints rejected
	for _, badEndpoint := range []string{"not-host-port", ":502", "127.0.0.1:0", "127.0.0.1:70000", "127.0.0.1:http"} {
		badEp := base
		badEp.Endpoint = badEndpoint
		if err := badEp.Validate(); err == nil {
			t.Fatalf("expected error for invalid TCP endpoint %q", badEndpoint)
		}
	}

	// Unknown capabilityProfile rejected
	badCap := base
	badCap.CapabilityProfile = "chiller.york.v1"
	if err := badCap.Validate(); err == nil {
		t.Fatal("expected error for unknown capabilityProfile")
	}

	// Duplicate BIT_BOOL (functionCode, address, bitIndex) rejected
	dupBit := base
	dupBit.Registers = []ModbusRegisterSpec{
		{PointCode: "bit_a", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 10, RegisterCount: 1, RawType: ModbusRawBitBool, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, BitIndex: 4, Scale: 1, PollPriority: PriorityHigh},
		{PointCode: "bit_b", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 10, RegisterCount: 1, RawType: ModbusRawBitBool, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, BitIndex: 4, Scale: 1, PollPriority: PriorityHigh},
	}
	if err := dupBit.Validate(); err == nil {
		t.Fatal("expected error for duplicate BIT_BOOL bit mapping")
	}

	// SemanticChannel capability mismatch rejected when CapabilityProfile set
	badSemantic := base
	badSemantic.CapabilityProfile = ProfileChiller
	badSemantic.Registers = []ModbusRegisterSpec{{
		PointCode: "bad_run_state", SemanticChannel: SemanticRunState,
		FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 0, RegisterCount: 1,
		RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityHigh,
	}}
	if err := badSemantic.Validate(); err == nil {
		t.Fatal("expected error for wrong DataType on SemanticRunState")
	}
}

func TestDeclarativeModbusBitBoolReadModifyWritePreservesSiblingBits(t *testing.T) {
	t.Parallel()

	transport := newStubDeclarativeTransport()
	transport.setHolding(10, 0x8805) // bits 0, 2, 11, 15 set (BE)
	transport.setHolding(11, 0x1234) // LE wire word: logical 0x3412 (bits 1, 4, 10, 12, 13 set)

	profile := DeclarativeModbusProfile{
		ComponentID: "BIT-RMW",
		DriverType:  ModbusDriverTCP,
		Endpoint:    "127.0.0.1:5020",
		UnitID:      1,
		TimeoutMS:   500,
		Retries:     0,
		Registers: []ModbusRegisterSpec{
			{PointCode: "be_bit_2", FunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteSingleRegister, Address: 10, RegisterCount: 1, RawType: ModbusRawBitBool, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, BitIndex: 2, Scale: 1, PollPriority: PriorityHigh},
			{PointCode: "be_bit_11", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 10, RegisterCount: 1, RawType: ModbusRawBitBool, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, BitIndex: 11, Scale: 1, PollPriority: PriorityHigh},
			{PointCode: "le_bit_3", FunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteMultipleRegisters, Address: 11, RegisterCount: 1, RawType: ModbusRawBitBool, ByteOrder: ModbusByteOrderLittleEndian, WordOrder: ModbusWordOrderBigEndian, BitIndex: 3, Scale: 1, PollPriority: PriorityHigh},
			{PointCode: "le_bit_4", FunctionCode: ModbusFunctionReadHoldingRegisters, Address: 11, RegisterCount: 1, RawType: ModbusRawBitBool, ByteOrder: ModbusByteOrderLittleEndian, WordOrder: ModbusWordOrderBigEndian, BitIndex: 4, Scale: 1, PollPriority: PriorityHigh},
		},
	}
	adapter, err := NewDeclarativeModbusAdapter(profile, transport)
	if err != nil {
		t.Fatalf("NewDeclarativeModbusAdapter() error = %v", err)
	}

	vFalse := BooleanValue(false)
	vTrue := BooleanValue(true)
	results, err := adapter.Apply(context.Background(), ProcessImage{}, []Decision{
		{Address: "BIT-RMW/be_bit_2", Accepted: true, Effective: &vFalse},
		{Address: "BIT-RMW/le_bit_3", Accepted: true, Effective: &vTrue},
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(results) != 2 || !results[0].Success || !results[1].Success {
		t.Fatalf("unexpected Apply() results: %+v", results)
	}

	if got := transport.holding[10]; got != 0x8801 {
		t.Fatalf("holding[10] after clearing bit 2 = 0x%04x, want 0x8801 (sibling bits preserved)", got)
	}
	// logical 0x3412 | (1<<3) = 0x341a -> LE wire word 0x1a34
	if got := transport.holding[11]; got != 0x1a34 {
		t.Fatalf("holding[11] after setting LE bit 3 = 0x%04x, want 0x1a34 (sibling bits preserved)", got)
	}

	// Re-set bit 2 -> 0x8805
	if _, err := adapter.Apply(context.Background(), ProcessImage{}, []Decision{
		{Address: "BIT-RMW/be_bit_2", Accepted: true, Effective: &vTrue},
	}); err != nil {
		t.Fatalf("Apply() set bit 2 error = %v", err)
	}
	if got := transport.holding[10]; got != 0x8805 {
		t.Fatalf("holding[10] after setting bit 2 = 0x%04x, want 0x8805", got)
	}

	// Pre-read failure aborts RMW without issuing Modbus write.
	writesBefore := len(transport.writes)
	transport.setReadError(errors.New("bus timeout"))
	if _, err := adapter.Apply(context.Background(), ProcessImage{}, []Decision{
		{Address: "BIT-RMW/be_bit_2", Accepted: true, Effective: &vFalse},
	}); err == nil {
		t.Fatal("expected RMW Apply error when read fails")
	}
	if len(transport.writes) != writesBefore {
		t.Fatal("RMW write must not be issued when holding register read fails")
	}
}

type closableDeviceAdapterWrapper struct {
	DeviceAdapter
	closeCount atomic.Int32
}

func (a *closableDeviceAdapterWrapper) Close() error {
	a.closeCount.Add(1)
	return nil
}

func TestHostReplaceOrRegisterDeviceConcurrentAndRollback(t *testing.T) {
	t.Parallel()

	tA := newStubDeclarativeTransport()
	tA.setHolding(1, 100)
	tB := newStubDeclarativeTransport()
	tB.setHolding(1, 50)
	tNewA := newStubDeclarativeTransport()
	tNewA.setHolding(2, 222)

	makeProfile := func(id string, addr uint16, pt string) DeclarativeModbusProfile {
		return DeclarativeModbusProfile{
			ComponentID: id,
			DriverType:  ModbusDriverTCP,
			Endpoint:    "127.0.0.1:5020",
			UnitID:      1,
			TimeoutMS:   500,
			Retries:     0,
			Registers: []ModbusRegisterSpec{
				{PointCode: pt, FunctionCode: ModbusFunctionReadHoldingRegisters, Address: addr, RegisterCount: 1, RawType: ModbusRawUint16, ByteOrder: ModbusByteOrderBigEndian, WordOrder: ModbusWordOrderBigEndian, Scale: 1, PollPriority: PriorityHigh},
			},
		}
	}

	adA, err := NewDeclarativeModbusAdapter(makeProfile("DEV-A", 1, "old_pt"), tA)
	if err != nil {
		t.Fatalf("NewDeclarativeModbusAdapter DEV-A error = %v", err)
	}
	closableA := &closableDeviceAdapterWrapper{DeviceAdapter: adA}

	adB, err := NewDeclarativeModbusAdapter(makeProfile("DEV-B", 1, "keep_pt"), tB)
	if err != nil {
		t.Fatalf("NewDeclarativeModbusAdapter DEV-B error = %v", err)
	}

	host, err := NewHost()
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	if err := host.RegisterAdapter(closableA); err != nil {
		t.Fatalf("RegisterAdapter DEV-A error = %v", err)
	}
	if err := host.RegisterAdapter(adB); err != nil {
		t.Fatalf("RegisterAdapter DEV-B error = %v", err)
	}
	if err := host.Start(nil); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	base := time.Date(2026, 11, 17, 13, 0, 0, 0, time.UTC)
	res, err := host.RunCycle(context.Background(), base)
	if err != nil {
		t.Fatalf("RunCycle initial error = %v", err)
	}
	if _, ok := res.Cycle.Image.Get("DEV-A/old_pt"); !ok {
		t.Fatal("expected DEV-A/old_pt before hot reload")
	}

	// Verify rollback if replacement violates registry (non-declarative component missing required ProfileChiller channels)
	badAdapter := newStubAutonomyDeviceAdapter(ComponentDescriptor{
		ID:        "DEV-A",
		Enabled:   true,
		Kind:      ComponentDeviceDriver,
		Type:      "CHILLER",
		FactoryID: "STANDARD",
		Version:   "1.0.0",
		Profiles:  []CapabilityProfileID{ProfileChiller},
	}, []ChannelDescriptor{
		{
			ComponentID: "DEV-A", ChannelID: "only_one", PointID: "p",
			DataType: DataTypeDouble, Access: AccessReadOnly, Category: ChannelCategoryOpenemsType,
			PollPriority: PriorityHigh, LocalPersistencePriority: PriorityHigh, RemotePersistencePriority: PriorityHigh,
			AggregationPriority: PriorityHigh, ResendPriority: PriorityHigh,
		},
	})
	if err := host.ReplaceOrRegisterDevice(badAdapter); err == nil {
		t.Fatal("expected ReplaceOrRegisterDevice to reject invalid replacement")
	}
	if got, ok := host.Adapter("DEV-A"); !ok || got != closableA {
		t.Fatal("expected rollback to keep original DEV-A adapter")
	}
	if closableA.closeCount.Load() != 0 {
		t.Fatal("original DEV-A adapter should not be closed after failed replacement")
	}

	// Valid replacement under concurrent RunCycle stress
	adNewA, err := NewDeclarativeModbusAdapter(makeProfile("DEV-A", 2, "new_pt"), tNewA)
	if err != nil {
		t.Fatalf("NewDeclarativeModbusAdapter replacement error = %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 15; i++ {
			_, _ = host.RunCycle(context.Background(), base.Add(time.Duration(i)*time.Second))
		}
	}()

	if err := host.ReplaceOrRegisterDevice(adNewA); err != nil {
		t.Fatalf("ReplaceOrRegisterDevice valid error = %v", err)
	}
	wg.Wait()

	if closableA.closeCount.Load() != 1 {
		t.Fatalf("expected old DEV-A adapter Close() called once, got %d", closableA.closeCount.Load())
	}

	res, err = host.RunCycle(context.Background(), base.Add(20*time.Second))
	if err != nil {
		t.Fatalf("post-replace RunCycle error = %v", err)
	}
	if _, ok := res.Cycle.Image.Get("DEV-A/old_pt"); ok {
		t.Fatal("expected old DEV-A/old_pt channel removed after hot reload")
	}
	if snap, ok := res.Cycle.Image.Get("DEV-A/new_pt"); !ok || snap.Sample.Value.Double != 222 {
		t.Fatalf("expected DEV-A/new_pt = 222 after hot reload, got %+v (ok=%v)", snap, ok)
	}
	if snap, ok := res.Cycle.Image.Get("DEV-B/keep_pt"); !ok || snap.Sample.Value.Double != 50 {
		t.Fatalf("expected unrelated DEV-B/keep_pt preserved = 50, got %+v (ok=%v)", snap, ok)
	}
}

type readbackOverrideTransport struct {
	*stubDeclarativeTransport
	overrideAddr uint16
	overrideVal  uint16
	useOverride  bool
	failReadback bool
}

func (r *readbackOverrideTransport) Read(ctx context.Context, task ModbusReadTask) ([]uint16, error) {
	if r.failReadback && len(r.writes) > 0 && task.Address == r.overrideAddr {
		return nil, errors.New("simulated readback transport error")
	}
	res, err := r.stubDeclarativeTransport.Read(ctx, task)
	if err != nil {
		return nil, err
	}
	if r.useOverride && len(r.writes) > 0 && task.Address == r.overrideAddr && len(res) > 0 {
		res[0] = r.overrideVal
	}
	return res, nil
}

func TestDeclarativeModbusImmediateLocalReadbackVerification(t *testing.T) {
	t.Parallel()

	baseProfile := DeclarativeModbusProfile{
		ComponentID: "PUMP-RB",
		DriverType:  ModbusDriverTCP,
		Endpoint:    "127.0.0.1:5020",
		UnitID:      1,
		TimeoutMS:   500,
		Retries:     1,
		Registers: []ModbusRegisterSpec{
			{
				PointCode:         "freq_sp",
				SemanticChannel:   SemanticFrequencySetpoint,
				FunctionCode:      ModbusFunctionReadHoldingRegisters,
				WriteFunctionCode: ModbusFunctionWriteSingleRegister,
				Address:           100,
				RegisterCount:     1,
				RawType:           ModbusRawUint16,
				ByteOrder:         ModbusByteOrderBigEndian,
				WordOrder:         ModbusWordOrderBigEndian,
				Scale:             0.1,
				PollPriority:      PriorityVeryHigh,
			},
		},
	}

	target := DoubleValue(42.5)
	decision := Decision{Address: "PUMP-RB/freq_sp", ControllerID: "intent", Accepted: true, Effective: &target}

	t.Run("readback matches target", func(t *testing.T) {
		t.Parallel()
		tr := newStubDeclarativeTransport()
		adapter, err := NewDeclarativeModbusAdapter(baseProfile, tr)
		if err != nil {
			t.Fatal(err)
		}
		results, err := adapter.Apply(context.Background(), ProcessImage{}, []Decision{decision})
		if err != nil {
			t.Fatalf("Apply() error = %v", err)
		}
		if len(results) != 1 || !results[0].Success || results[0].Code != "APPLIED" || !results[0].ReadbackVerified {
			t.Fatalf("expected APPLIED with ReadbackVerified=true, got %+v", results)
		}
		if results[0].ReadbackValue == nil || results[0].ReadbackValue.Double != 42.5 || results[0].ReadbackAt.IsZero() {
			t.Fatalf("expected ReadbackValue=42.5 and ReadbackAt populated, got %+v", results[0])
		}
	})

	t.Run("readback I/O error fails explicitly", func(t *testing.T) {
		t.Parallel()
		tr := &readbackOverrideTransport{stubDeclarativeTransport: newStubDeclarativeTransport(), overrideAddr: 100, failReadback: true}
		adapter, err := NewDeclarativeModbusAdapter(baseProfile, tr)
		if err != nil {
			t.Fatal(err)
		}
		results, err := adapter.Apply(context.Background(), ProcessImage{}, []Decision{decision})
		if err != nil {
			t.Fatalf("Apply() unexpected err = %v", err)
		}
		if len(results) != 1 || results[0].Success || results[0].Code != "LOCAL_READBACK_FAILED" || results[0].ReadbackVerified {
			t.Fatalf("expected LOCAL_READBACK_FAILED with ReadbackVerified=false, got %+v", results)
		}
	})

	t.Run("stuck register mismatch fails explicitly", func(t *testing.T) {
		t.Parallel()
		tr := &readbackOverrideTransport{
			stubDeclarativeTransport: newStubDeclarativeTransport(),
			overrideAddr:             100,
			overrideVal:              300, // 30.0 instead of 42.5
			useOverride:              true,
		}
		adapter, err := NewDeclarativeModbusAdapter(baseProfile, tr)
		if err != nil {
			t.Fatal(err)
		}
		results, err := adapter.Apply(context.Background(), ProcessImage{}, []Decision{decision})
		if err != nil {
			t.Fatalf("Apply() unexpected err = %v", err)
		}
		if len(results) != 1 || results[0].Success || results[0].Code != "LOCAL_READBACK_MISMATCH" || results[0].ReadbackVerified {
			t.Fatalf("expected LOCAL_READBACK_MISMATCH, got %+v", results)
		}
		if results[0].ReadbackValue == nil || results[0].ReadbackValue.Double != 30.0 {
			t.Fatalf("expected ReadbackValue=30.0 on mismatch, got %+v", results[0].ReadbackValue)
		}
	})
}
