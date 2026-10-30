package adapter

import "testing"

// Event IDs are how a redelivered reading is recognised as the same reading, so the
// bytes that feed them must never change. These values were produced before the
// hashing was rewritten to stream into SHA-256.
func TestDeterministicPointEventIDIsStable(t *testing.T) {
	unit := "°C"
	cases := []struct {
		name      string
		messageID string
		timestamp int64
		point     EnvelopePoint
		partition string
		want      string
	}{
		{"number", " msg-001 ", 1759132800123, EnvelopePoint{Code: "chws_temp", Value: 7.25, Quality: 192, Unit: &unit}, sourcePartition("gw-1", "chiller-01", "chws_temp"), "0199947c-547b-737f-a3d3-142c4900850f"},
		{"bool", "msg-002", 0, EnvelopePoint{Code: "run", Value: true, Quality: 0}, sourcePartition("gw-1", "pump-01", "run"), "00000000-0000-7e5e-b9ef-a70401b04858"},
		{"string", "", 1<<48 - 1, EnvelopePoint{Code: "mode", Value: "AUTO", Quality: 255}, "", "ffffffff-ffff-7802-9637-5d3c2eb22db5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deterministicPointEventID(tc.messageID, tc.timestamp, tc.point, tc.partition)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("event ID = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDeterministicPointEventIDRejectsOutOfRangeTimestamps(t *testing.T) {
	for _, timestamp := range []int64{-1, 1 << 48} {
		if _, err := deterministicPointEventID("m", timestamp, EnvelopePoint{Code: "c", Value: 1}, "p"); err == nil {
			t.Fatalf("timestamp %d accepted", timestamp)
		}
	}
}
