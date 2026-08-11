package gateway

import (
	"testing"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestCanonicalRegistrySuccessAcceptsCentralPlantSite(t *testing.T) {
	raw := []byte(`{
		"items":[{
			"id":"018f3e00-1000-7000-8000-000000000001",
			"tenantId":"018f3d00-0000-7000-8000-000000000001",
			"owningOrganizationId":"018f3e00-0000-7000-8000-000000000001",
			"code":"central-plant",
			"displayName":"中央机房",
			"timezone":"Asia/Shanghai",
			"status":"ACTIVE",
			"revision":1,
			"createdAt":"2026-07-28T16:16:16.000Z",
			"updatedAt":"2026-07-28T16:16:16.000Z"
		}],
		"nextCursor":null,
		"hasMore":false
	}`)
	if _, err := canonicalRegistrySuccess(registryauth.ActionSiteList, "", raw); err != nil {
		t.Fatalf("canonicalRegistrySuccess() error = %v", err)
	}
}
