package simulator

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	modbus "github.com/simonvetter/modbus"
)

func TestVirtualATV630ExposesOnlyReleaseCandidateRegistersOverRealTCP(t *testing.T) {
	plant := NewPlant(testPlantConfig(), testStaticScenario(), time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC))
	endpoint := reserveTCPAddress(t)
	server, err := NewVirtualATV630Server(endpoint, plant)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })

	client := openVirtualATV630Client(t, endpoint)

	status, err := client.ReadRegisters(3201, 2, modbus.HOLDING_REGISTER)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 2 || status[0] != 0x0237 || status[1] != 500 {
		t.Fatalf("unexpected ETA/RFR readback: %#v", status)
	}
	lastFault, err := client.ReadRegister(7121, modbus.HOLDING_REGISTER)
	if err != nil {
		t.Fatal(err)
	}
	if lastFault != 0 {
		t.Fatalf("healthy virtual drive exposed a current fault: %d", lastFault)
	}
	commandWord, err := client.ReadRegister(8501, modbus.HOLDING_REGISTER)
	if err != nil {
		t.Fatal(err)
	}
	if commandWord != 15 {
		t.Fatalf("running virtual drive did not expose enabled CMD state: %d", commandWord)
	}
	frequencyReference, err := client.ReadRegister(8502, modbus.HOLDING_REGISTER)
	if err != nil {
		t.Fatal(err)
	}
	if frequencyReference != 500 {
		t.Fatalf("unexpected LFR readback: %d", frequencyReference)
	}
	assertVirtualATV630RejectsFunction16(t, endpoint, 8502, 300)
	assertVirtualATV630Register(t, client, 8502, 500)
	if _, err := client.ReadRegister(3203, modbus.HOLDING_REGISTER); err == nil {
		t.Fatal("virtual ATV630 exposed a simulator-only holding register")
	}
}

func TestVirtualATV630WritesDrivePlantAndLaterReadsPhysicalState(t *testing.T) {
	config := testPlantConfig()
	config.ChilledWaterPump.InitiallyRunning = false
	plant := NewPlant(config, testStaticScenario(), time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC))
	plant.Tick(2 * time.Minute)

	endpoint := reserveTCPAddress(t)
	server, err := NewVirtualATV630Server(endpoint, plant)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	client := openVirtualATV630Client(t, endpoint)

	assertVirtualATV630Register(t, client, 3201, 0x0250)
	if err := client.WriteRegister(8502, 400); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		command uint16
		eta     uint16
	}{
		{command: 6, eta: 0x0231},
		{command: 7, eta: 0x0233},
		{command: 15, eta: 0x0237},
	} {
		if err := client.WriteRegister(8501, step.command); err != nil {
			t.Fatal(err)
		}
		assertVirtualATV630Register(t, client, 3201, step.eta)
	}

	before := plant.Snapshot().Devices[config.ChilledWaterPump.ID]["frequencyHz"].(float64)
	if before >= 1 {
		t.Fatalf("START mutated physical readback before Plant time advanced: %.3f Hz", before)
	}
	plant.Tick(20 * time.Second)
	readback, err := client.ReadRegister(3202, modbus.HOLDING_REGISTER)
	if err != nil {
		t.Fatal(err)
	}
	if readback <= 1 || readback >= 400 {
		t.Fatalf("later RFR did not reflect reacting Plant dynamics: raw=%d", readback)
	}
	if got := plant.Snapshot().Devices[config.ChilledWaterPump.ID]["flowRateM3h"].(float64); got <= 0 {
		t.Fatalf("protocol START/LFR did not reach CHWP actuator path: flow=%.3f", got)
	}

	if err := client.WriteRegister(8501, 7); err != nil {
		t.Fatal(err)
	}
	assertVirtualATV630Register(t, client, 3201, 0x0233)
	plant.Tick(time.Minute)
	stoppedReadback, err := client.ReadRegister(3202, modbus.HOLDING_REGISTER)
	if err != nil {
		t.Fatal(err)
	}
	if stoppedReadback >= readback {
		t.Fatalf("STOP did not produce later independent physical decay: before=%d after=%d", readback, stoppedReadback)
	}
}

func TestVirtualATV630StuckHighRemainsPhysicalAndAppearsOnlyInLaterRFR(t *testing.T) {
	config := testPlantConfig()
	plant := NewPlant(config, testStaticScenario(), time.Date(2026, 8, 28, 10, 30, 0, 0, time.UTC))
	plant.Tick(time.Minute)
	plant.SetCHWPStuckHighDisturbance(true)

	endpoint := reserveTCPAddress(t)
	server, err := NewVirtualATV630Server(endpoint, plant)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	client := openVirtualATV630Client(t, endpoint)

	if err := client.WriteRegister(8502, 300); err != nil {
		t.Fatal(err)
	}
	assertVirtualATV630Register(t, client, 8502, 300)
	plant.Tick(time.Minute)
	stuckReadback, err := client.ReadRegister(3202, modbus.HOLDING_REGISTER)
	if err != nil {
		t.Fatal(err)
	}
	if stuckReadback < 495 {
		t.Fatalf("stuck-high physical disturbance did not hold actual RFR high: raw=%d", stuckReadback)
	}
	if got := plant.Snapshot().Devices[config.ChilledWaterPump.ID]["flowRateM3h"].(float64); got < config.ChilledWaterPump.RatedFlowM3H*0.99 {
		t.Fatalf("stuck-high protocol readback was not backed by physical CHWP flow: %.3f", got)
	}

	plant.SetCHWPStuckHighDisturbance(false)
	plant.Tick(time.Minute)
	recoveredReadback, err := client.ReadRegister(3202, modbus.HOLDING_REGISTER)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredReadback >= stuckReadback || recoveredReadback <= 300 {
		t.Fatalf("RFR did not dynamically recover toward governed LFR: stuck=%d recovered=%d", stuckReadback, recoveredReadback)
	}
}

func TestVirtualATV630FaultReadbackAndResetUsePlantState(t *testing.T) {
	config := testPlantConfig()
	plant := NewPlant(config, testStaticScenario(), time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC))
	if !plant.SetFault(config.ChilledWaterPump.ID, "16") {
		t.Fatal("failed to apply physical CHWP drive fault")
	}

	endpoint := reserveTCPAddress(t)
	server, err := NewVirtualATV630Server(endpoint, plant)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	client := openVirtualATV630Client(t, endpoint)

	assertVirtualATV630Register(t, client, 3201, 0x0008)
	if err := client.WriteRegister(8501, 128); err != nil {
		t.Fatal(err)
	}
	if err := client.WriteRegister(8501, 0); err != nil {
		t.Fatal(err)
	}
	assertVirtualATV630Register(t, client, 3201, 0x0250)
	assertVirtualATV630Register(t, client, 7121, 16)
}

func reserveTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func openVirtualATV630Client(t *testing.T, endpoint string) *modbus.ModbusClient {
	t.Helper()
	client, err := modbus.NewClient(&modbus.ClientConfiguration{URL: "tcp://" + endpoint, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Open(); err != nil {
		t.Fatal(err)
	}
	if err := client.SetUnitId(1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func assertVirtualATV630Register(t *testing.T, client *modbus.ModbusClient, address, want uint16) {
	t.Helper()
	got, err := client.ReadRegister(address, modbus.HOLDING_REGISTER)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("unexpected virtual ATV630 register %d: got=%#04x want=%#04x", address, got, want)
	}
}

func assertVirtualATV630RejectsFunction16(t *testing.T, endpoint string, address, value uint16) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	request := make([]byte, 15)
	binary.BigEndian.PutUint16(request[0:2], 0x1234)
	binary.BigEndian.PutUint16(request[4:6], 9)
	request[6] = 1
	request[7] = 16
	binary.BigEndian.PutUint16(request[8:10], address)
	binary.BigEndian.PutUint16(request[10:12], 1)
	request[12] = 2
	binary.BigEndian.PutUint16(request[13:15], value)
	if _, err := connection.Write(request); err != nil {
		t.Fatal(err)
	}

	response := make([]byte, 9)
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(response[0:2]) != 0x1234 || binary.BigEndian.Uint16(response[4:6]) != 3 || response[6] != 1 || response[7] != 0x90 || response[8] != 1 {
		t.Fatalf("FC16 did not receive Modbus Illegal Function: %x", response)
	}
}
