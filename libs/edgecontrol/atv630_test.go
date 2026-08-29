package edgecontrol

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestATV630ProtocolReleaseCandidatePinsSchneiderMinimalMap(t *testing.T) {
	candidate := ATV630ProtocolReleaseCandidate()
	if candidate.Status != ATV630ProtocolCandidate || candidate.HardwareCertified {
		t.Fatalf("ATV630 mapping overstated release state: %#v", candidate)
	}
	if candidate.Manufacturer != "Schneider Electric" || candidate.Model != "ATV630" || candidate.Transport != "EMBEDDED_MODBUS_TCP" || candidate.ControlProfile != "CIA402_DRIVECOM" {
		t.Fatalf("ATV630 candidate identity/profile drifted: %#v", candidate)
	}
	if candidate.EmbeddedEthernetManual != "EAV64327 v03" || candidate.CommunicationParameters != "EAV64332 v4.6 (2026-05-01)" {
		t.Fatalf("ATV630 mapping references drifted: %#v", candidate)
	}
	want := []ATV630ProtocolParameter{
		{Code: "ETA", LogicalAddress: 3201, RegisterCount: 1, RawDataType: ATV630RawBitString16, Scale: 1, Unit: "", ByteOrder: ATV630ByteOrderBigEndian, WordOrder: ATV630WordOrderNotApplicable, Access: ATV630ProtocolReadOnly, ReadFunctionCode: ModbusFunctionReadHoldingRegisters},
		{Code: "RFR", LogicalAddress: 3202, RegisterCount: 1, RawDataType: ATV630RawSigned16, Scale: 0.1, Unit: "Hz", ByteOrder: ATV630ByteOrderBigEndian, WordOrder: ATV630WordOrderNotApplicable, Access: ATV630ProtocolReadOnly, ReadFunctionCode: ModbusFunctionReadHoldingRegisters},
		{Code: "LFT", LogicalAddress: 7121, RegisterCount: 1, RawDataType: ATV630RawEnumeration16, Scale: 1, Unit: "", ByteOrder: ATV630ByteOrderBigEndian, WordOrder: ATV630WordOrderNotApplicable, Access: ATV630ProtocolReadOnly, ReadFunctionCode: ModbusFunctionReadHoldingRegisters},
		{Code: "CMD", LogicalAddress: 8501, RegisterCount: 1, RawDataType: ATV630RawBitString16, Scale: 1, Unit: "", ByteOrder: ATV630ByteOrderBigEndian, WordOrder: ATV630WordOrderNotApplicable, Access: ATV630ProtocolReadWrite, ReadFunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteSingleRegister},
		{Code: "LFR", LogicalAddress: 8502, RegisterCount: 1, RawDataType: ATV630RawSigned16, Scale: 0.1, Unit: "Hz", ByteOrder: ATV630ByteOrderBigEndian, WordOrder: ATV630WordOrderNotApplicable, Access: ATV630ProtocolReadWrite, ReadFunctionCode: ModbusFunctionReadHoldingRegisters, WriteFunctionCode: ModbusFunctionWriteSingleRegister},
	}
	if !reflect.DeepEqual(candidate.Parameters, want) {
		t.Fatalf("ATV630 minimal mapping drifted:\n got: %#v\nwant: %#v", candidate.Parameters, want)
	}
}

func TestATV630AdapterProjectsRawDriveStateIntoVariableSpeedPumpChannels(t *testing.T) {
	transport := &recordingModbusRegisterTransport{reads: map[uint16][]uint16{
		3201: {0x0237, uint16(int16(437))},
		7121: {16},
	}}
	adapter := newTestATV630Adapter(t, transport)
	host, err := NewHost()
	if err != nil {
		t.Fatal(err)
	}
	if err := host.RegisterAdapter(adapter); err != nil {
		t.Fatalf("ATV630 adapter does not satisfy VARIABLE_SPEED_PUMP capability: %v", err)
	}
	at := time.Unix(7_000, 0).UTC()

	updates, err := adapter.Poll(t.Context(), at)
	if err != nil {
		t.Fatal(err)
	}
	assertATV630Update(t, updates, "chwp01/RunState", StringValue("RUNNING"))
	assertATV630Update(t, updates, "chwp01/Frequency", DoubleValue(43.7))
	assertATV630Update(t, updates, "chwp01/FaultCode", StringValue(""))

	transport.reads[3201] = []uint16{0x0008, uint16(int16(437))}
	updates, err = adapter.Poll(t.Context(), at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	assertATV630Update(t, updates, "chwp01/RunState", StringValue("FAULT"))
	assertATV630Update(t, updates, "chwp01/FaultCode", StringValue("16"))
}

func TestATV630AdapterAdvancesStartOnlyAfterDriveComETAProgression(t *testing.T) {
	transport := &recordingModbusRegisterTransport{reads: map[uint16][]uint16{}}
	adapter := newTestATV630Adapter(t, transport)
	effective := BooleanValue(true)
	decision := Decision{Address: "chwp01/StartCommand", Accepted: true, Effective: &effective}
	at := time.Unix(7_100, 0).UTC()

	steps := []struct {
		eta  uint16
		want recordedModbusWrite
	}{
		{eta: 0x0250, want: recordedModbusWrite{8501, 6}},
		{eta: 0x0231, want: recordedModbusWrite{8501, 7}},
		{eta: 0x0233, want: recordedModbusWrite{8501, 15}},
	}
	for index, step := range steps {
		transport.reads[3201] = []uint16{step.eta, 0}
		if _, err := adapter.Poll(t.Context(), at.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatal(err)
		}
		results, err := adapter.Apply(t.Context(), ProcessImage{}, []Decision{decision})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || !results[0].Success || results[0].Code != "IN_PROGRESS" {
			t.Fatalf("unexpected in-progress START result at ETA=%#04x: %#v", step.eta, results)
		}
		if got := transport.writes[len(transport.writes)-1]; got != step.want {
			t.Fatalf("unexpected START write at ETA=%#04x: got=%#v want=%#v", step.eta, got, step.want)
		}
	}

	transport.reads[3201] = []uint16{0x0237, 0}
	if _, err := adapter.Poll(t.Context(), at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	completed, err := adapter.Apply(t.Context(), ProcessImage{}, []Decision{decision})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed) != 1 || !completed[0].Success || completed[0].Code != "APPLIED" {
		t.Fatalf("START did not complete after operation-enabled ETA: %#v", completed)
	}
	if want := []recordedModbusWrite{{8501, 6}, {8501, 7}, {8501, 15}}; !reflect.DeepEqual(transport.writes, want) {
		t.Fatalf("ATV630 START advanced without ETA-confirmed cycles or rewrote operation enabled: got=%#v want=%#v", transport.writes, want)
	}
}

func TestATV630AdapterTranslatesGovernedSemanticCommandsIntoDriveComWrites(t *testing.T) {
	tests := []struct {
		name      string
		address   string
		effective Value
		want      []recordedModbusWrite
	}{
		{name: "stop", address: "chwp01/StopCommand", effective: BooleanValue(true), want: []recordedModbusWrite{{8501, 7}}},
		{name: "reset fault", address: "chwp01/ResetFaultCommand", effective: BooleanValue(true), want: []recordedModbusWrite{{8501, 128}, {8501, 0}}},
		{name: "set frequency", address: "chwp01/FrequencySetpoint", effective: DoubleValue(43.5), want: []recordedModbusWrite{{8502, 435}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &recordingModbusRegisterTransport{}
			adapter := newTestATV630Adapter(t, transport)
			effective := test.effective
			results, err := adapter.Apply(t.Context(), ProcessImage{}, []Decision{{Address: test.address, Accepted: true, Effective: &effective}})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || !results[0].Success || !reflect.DeepEqual(transport.writes, test.want) {
				t.Fatalf("unexpected governed ATV630 write: results=%#v writes=%#v want=%#v", results, transport.writes, test.want)
			}
		})
	}
}

func newTestATV630Adapter(t *testing.T, transport ModbusRegisterTransport) *ATV630DeviceAdapter {
	t.Helper()
	adapter, err := NewATV630DeviceAdapter(ATV630DeviceAdapterConfig{
		ComponentID: "chwp01",
		Alias:       "CHWP-01 ATV630",
		UnitID:      1,
		Transport:   transport,
		PointIDs: ATV630PointIDs{
			RunState: "point-run-state", FaultCode: "point-fault-code",
			StartCommand: "point-start-command", StopCommand: "point-stop-command", ResetFaultCommand: "point-reset-fault-command",
			Frequency: "point-frequency", FrequencySetpoint: "point-frequency-setpoint",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func assertATV630Update(t *testing.T, updates []ChannelUpdate, address string, want Value) {
	t.Helper()
	for _, update := range updates {
		if update.Address == address {
			if !reflect.DeepEqual(update.Sample.Value, want) || update.Sample.Quality != QualityGood {
				t.Fatalf("unexpected %s update: %#v want=%#v", address, update, want)
			}
			return
		}
	}
	t.Fatalf("missing ATV630 update for %s: %#v", address, updates)
}

type recordedModbusWrite struct {
	address uint16
	value   uint16
}

type recordingModbusRegisterTransport struct {
	reads  map[uint16][]uint16
	writes []recordedModbusWrite
}

func (transport *recordingModbusRegisterTransport) Read(_ context.Context, task ModbusReadTask) ([]uint16, error) {
	return slices.Clone(transport.reads[task.Address]), nil
}

func (transport *recordingModbusRegisterTransport) Write(_ context.Context, task ModbusWriteTask) error {
	transport.writes = append(transport.writes, recordedModbusWrite{address: task.Address, value: task.Values[0]})
	return nil
}
