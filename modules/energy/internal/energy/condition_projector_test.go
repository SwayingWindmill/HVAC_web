package energy

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubConditionInputReader struct {
	inputs  []ConditionSampleInput
	err     error
	gotFrom time.Time
	gotTo   time.Time
	calls   int
}

func (s *stubConditionInputReader) ReadConditionInputs(_ context.Context, from, to time.Time) ([]ConditionSampleInput, error) {
	s.calls++
	s.gotFrom = from
	s.gotTo = to
	if s.err != nil {
		return nil, s.err
	}
	return append([]ConditionSampleInput(nil), s.inputs...), nil
}

type stubConditionAnchorWriter struct {
	anchors []SiteConditionAnchor
	batches [][]SiteConditionAnchor
	err     error
	calls   int
}

func (s *stubConditionAnchorWriter) InsertConditionAnchors(_ context.Context, anchors []SiteConditionAnchor) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	copied := append([]SiteConditionAnchor(nil), anchors...)
	s.batches = append(s.batches, copied)
	s.anchors = append(s.anchors, copied...)
	return nil
}

func TestNewConditionAnchorProjectorValidationAndDefaults(t *testing.T) {
	reader := &stubConditionInputReader{}
	writer := &stubConditionAnchorWriter{}

	if _, err := NewConditionAnchorProjector(ConditionAnchorProjectorConfig{Writer: writer}); err == nil {
		t.Fatal("expected error when Reader is nil")
	}
	if _, err := NewConditionAnchorProjector(ConditionAnchorProjectorConfig{Reader: reader}); err == nil {
		t.Fatal("expected error when Writer is nil")
	}

	projector, err := NewConditionAnchorProjector(ConditionAnchorProjectorConfig{Reader: reader, Writer: writer})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if projector.lookback != defaultConditionAnchorLookback || projector.now == nil {
		t.Fatalf("expected default lookback=3h and non-nil now, got lookback=%v nowNil=%t", projector.lookback, projector.now == nil)
	}
}

func TestConditionAnchorProjectorSyncOnceAggregatesAndStampsVersion(t *testing.T) {
	now := time.Date(2026, 10, 12, 10, 25, 30, 0, time.FixedZone("CST", 8*3600))
	nowUTC := now.UTC()

	h07 := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	h08 := h07.Add(time.Hour)
	reader := &stubConditionInputReader{
		inputs: []ConditionSampleInput{
			{
				TenantID:          testTenantID,
				SiteID:            testSiteID,
				Hour:              h08,
				OutdoorWetBulbC:   float64Pointer(22.0),
				OutdoorDryBulbC:   float64Pointer(29.0),
				ClosedLoopTicks:   6,
				TotalControlTicks: 10,
			},
			{
				TenantID:        testTenantID,
				SiteID:          testSiteID,
				Hour:            h07,
				OutdoorWetBulbC: float64Pointer(21.0),
				OutdoorDryBulbC: float64Pointer(27.0),
			},
			{
				TenantID:        testTenantID,
				SiteID:          testSiteID,
				Hour:            h07.Add(15 * time.Minute),
				OutdoorWetBulbC: float64Pointer(23.0),
				OutdoorDryBulbC: float64Pointer(29.0),
			},
			// Invalid tenant UUID is filtered out cleanly by BuildSiteConditionAnchors.
			{
				TenantID:        "bad-uuid",
				SiteID:          testSiteID,
				Hour:            h07,
				OutdoorWetBulbC: float64Pointer(20.0),
			},
		},
	}
	writer := &stubConditionAnchorWriter{}

	projector, err := NewConditionAnchorProjector(ConditionAnchorProjectorConfig{
		Reader:   reader,
		Writer:   writer,
		Lookback: 3 * time.Hour,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	synced, err := projector.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("SyncOnce failed: %v", err)
	}
	if synced != 2 || writer.calls != 1 || len(writer.anchors) != 2 {
		t.Fatalf("synced=%d calls=%d anchors=%d, want 2/1/2", synced, writer.calls, len(writer.anchors))
	}

	wantFrom := nowUTC.Add(-3 * time.Hour).Truncate(time.Hour)
	if !reader.gotFrom.Equal(wantFrom) || !reader.gotTo.Equal(nowUTC) {
		t.Fatalf("window = [%v, %v), want [%v, %v)", reader.gotFrom, reader.gotTo, wantFrom, nowUTC)
	}

	wantVersion := uint64(nowUTC.UnixMilli())
	a0 := writer.anchors[0]
	if !a0.Hour.Equal(h07) || a0.SampleCount != 2 || a0.CalcVersion != wantVersion || !a0.UpdatedAt.Equal(nowUTC) {
		t.Fatalf("unexpected first anchor: %+v", a0)
	}
	assertFloatPtrClose(t, "a0 wet bulb", a0.OutdoorWetBulbC, 22.0)
	assertFloatPtrClose(t, "a0 dry bulb", a0.OutdoorDryBulbC, 28.0)

	a1 := writer.anchors[1]
	if !a1.Hour.Equal(h08) || a1.SampleCount != 1 || a1.CalcVersion != wantVersion {
		t.Fatalf("unexpected second anchor: %+v", a1)
	}
	assertFloatPtrClose(t, "a1 ai share", a1.AIClosedLoopShare, 0.6)
}

func TestConditionAnchorProjectorHandlesEmptyAndErrors(t *testing.T) {
	emptyReader := &stubConditionInputReader{}
	writer := &stubConditionAnchorWriter{}
	projector, err := NewConditionAnchorProjector(ConditionAnchorProjectorConfig{
		Reader: emptyReader,
		Writer: writer,
		Now:    func() time.Time { return time.Date(2026, 10, 12, 5, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	synced, err := projector.SyncOnce(context.Background())
	if err != nil || synced != 0 || writer.calls != 0 {
		t.Fatalf("empty SyncOnce got synced=%d err=%v calls=%d", synced, err, writer.calls)
	}

	// Only invalid UUID rows -> BuildSiteConditionAnchors returns empty -> no writer call.
	emptyReader.inputs = []ConditionSampleInput{{TenantID: "invalid", SiteID: testSiteID, Hour: time.Now()}}
	synced, err = projector.SyncOnce(context.Background())
	if err != nil || synced != 0 || writer.calls != 0 {
		t.Fatalf("invalid-only SyncOnce got synced=%d err=%v calls=%d", synced, err, writer.calls)
	}

	emptyReader.err = errors.New("clickhouse timeout")
	if _, err := projector.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected reader error to propagate")
	}

	emptyReader.err = nil
	emptyReader.inputs = []ConditionSampleInput{{TenantID: testTenantID, SiteID: testSiteID, Hour: time.Date(2026, 10, 12, 4, 0, 0, 0, time.UTC)}}
	writer.err = errors.New("write denied")
	if _, err := projector.SyncOnce(context.Background()); err == nil {
		t.Fatal("expected writer error to propagate")
	}
}
