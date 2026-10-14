package gateway

import (
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestCanonicalRegistrySiteCarriesItsProfile(t *testing.T) {
	raw := []byte(`{
		"items":[{
			"id":"018f3e00-1000-7000-8000-000000000001",
			"tenantId":"018f3d00-0000-7000-8000-000000000001",
			"code":"central-plant",
			"displayName":"中央机房",
			"timezone":"Asia/Shanghai",
			"status":"ACTIVE",
			"revision":1,
			"createdAt":"2026-07-28T16:16:16.000Z",
			"updatedAt":"2026-07-28T16:16:16.000Z",
			"floorAreaM2":32000,
			"climateZone":"HOT_SUMMER_COLD_WINTER"
		}],
		"nextCursor":null,
		"hasMore":false
	}`)
	canonical, err := canonicalRegistrySuccess(registryauth.ActionSiteList, "", raw)
	if err != nil || !strings.Contains(string(canonical), `"floorAreaM2":32000`) {
		t.Fatalf("canonicalRegistrySuccess() = %s, error = %v", canonical, err)
	}
	// A Site without a recorded profile reads its fields as null.
	unrecorded := strings.Replace(strings.Replace(string(raw), `"floorAreaM2":32000`, `"floorAreaM2":null`, 1), `"HOT_SUMMER_COLD_WINTER"`, `null`, 1)
	if _, err := canonicalRegistrySuccess(registryauth.ActionSiteList, "", []byte(unrecorded)); err != nil {
		t.Fatalf("unrecorded profile error = %v", err)
	}
	unknownZone := strings.Replace(string(raw), `"HOT_SUMMER_COLD_WINTER"`, `"TROPICAL"`, 1)
	if _, err := canonicalRegistrySuccess(registryauth.ActionSiteList, "", []byte(unknownZone)); err == nil {
		t.Fatal("a climate zone outside GB 50176 was passed through")
	}
}
