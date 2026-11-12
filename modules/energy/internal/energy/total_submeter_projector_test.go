package energy

import (
	"context"
	"testing"
	"time"
)

type fakeSubmeterStore struct {
	sites   []PricedSite
	syncs   map[string]TotalSubmeterSync
	rows    map[uint64][]TotalSubMeter
	dropped []uint64
}

func (store *fakeSubmeterStore) SubmeterSites(context.Context, time.Time) ([]PricedSite, error) {
	return store.sites, nil
}

func (store *fakeSubmeterStore) TotalSubmeterSync(_ context.Context, site PricedSite) (TotalSubmeterSync, bool, error) {
	sync, ok := store.syncs[site.SiteID]
	return sync, ok, nil
}

func (store *fakeSubmeterStore) WriteTotalSubmeters(_ context.Context, sync TotalSubmeterSync, subMeters []TotalSubMeter) error {
	store.rows[sync.Generation] = append([]TotalSubMeter(nil), subMeters...)
	return nil
}

func (store *fakeSubmeterStore) RecordTotalSubmeterSync(_ context.Context, sync TotalSubmeterSync) error {
	store.syncs[sync.SiteID] = sync
	return nil
}

func (store *fakeSubmeterStore) DropTotalSubmetersBefore(_ context.Context, sync TotalSubmeterSync) error {
	store.dropped = append(store.dropped, sync.Generation)
	return nil
}

type fakeSubmeterSource struct {
	sources SiteTotalElectricitySources
}

func (source *fakeSubmeterSource) TotalElectricitySources(context.Context, string, string) (SiteTotalElectricitySources, error) {
	return source.sources, nil
}

func TestTotalSubmeterProjectorSwapsGenerationsAndSkipsUnchanged(t *testing.T) {
	site := PricedSite{
		TenantID: "018f1d00-0000-7000-8000-000000000001",
		SiteID:   "018f1e00-1000-7000-8000-000000000001",
	}
	store := &fakeSubmeterStore{
		sites: []PricedSite{site},
		syncs: map[string]TotalSubmeterSync{},
		rows:  map[uint64][]TotalSubMeter{},
	}
	source := &fakeSubmeterSource{
		sources: SiteTotalElectricitySources{
			Synthesized: true,
			SubMeters: []TotalSubMeter{
				{PointID: "018f1e00-6000-7000-8000-000000000001", DeviceID: "018f1e00-4000-7000-8000-000000000001", MeterName: "3号电表（A机组）", Ordinal: 0},
				{PointID: "018f1e00-6000-7000-8000-000000000002", DeviceID: "018f1e00-4000-7000-8000-000000000002", MeterName: "4号电表（B机组）", Ordinal: 1},
			},
		},
	}
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	projector, err := NewTotalSubmeterProjector(TotalSubmeterProjectorConfig{
		Reader:  store,
		Writer:  store,
		Sources: source,
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := projector.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstSync := store.syncs[site.SiteID]
	if !firstSync.Synthesized || len(store.rows[firstSync.Generation]) != 2 || len(store.dropped) != 1 {
		t.Fatalf("first sync = %#v rows=%#v dropped=%v", firstSync, store.rows, store.dropped)
	}

	// Unchanged sources do not write a second generation.
	now = now.Add(5 * time.Minute)
	if err := projector.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.syncs[site.SiteID].Generation != firstSync.Generation || len(store.dropped) != 1 {
		t.Fatalf("expected unchanged sync to be a no-op, got %#v", store.syncs[site.SiteID])
	}

	// Switching back to a physical total meter records synthesized=false under a higher generation.
	source.sources = SiteTotalElectricitySources{Synthesized: false}
	now = now.Add(5 * time.Minute)
	if err := projector.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	secondSync := store.syncs[site.SiteID]
	if secondSync.Synthesized || secondSync.Generation <= firstSync.Generation || len(store.rows[secondSync.Generation]) != 0 {
		t.Fatalf("second sync = %#v rows=%#v", secondSync, store.rows[secondSync.Generation])
	}
}
