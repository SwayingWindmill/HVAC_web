package core

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func TestCursorIsBoundToActionParentAndScope(t *testing.T) {
	codec, err := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	claims := testGrantClaims(registryauth.ActionAssetList)
	cursor, err := codec.Encode("site-assets", testSiteA1, registryauth.ActionAssetList, claims, "Chiller 1", testAssetA1)
	if err != nil {
		t.Fatal(err)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(strings.Split(cursor, ".")[0])
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatal(err)
	}
	for _, claim := range []string{"v", "route", "scopeHash", "filterHash", "order", "last", "queryRevision"} {
		if _, ok := payload[claim]; !ok {
			t.Fatalf("cursor claim %q is missing: %s", claim, payloadBytes)
		}
	}
	page, err := codec.Decode(cursor, "site-assets", testSiteA1, registryauth.ActionAssetList, claims)
	if err != nil {
		t.Fatal(err)
	}
	if page.DisplayName != "Chiller 1" || page.ID != testAssetA1 {
		t.Fatalf("decoded page = %#v", page)
	}

	for name, decode := range map[string]func() error{
		"action": func() error {
			_, err := codec.Decode(cursor, "site-assets", testSiteA1, registryauth.ActionAssetRead, claims)
			return err
		},
		"parent": func() error {
			_, err := codec.Decode(cursor, "site-assets", testSiteA2, registryauth.ActionAssetList, claims)
			return err
		},
		"scope": func() error {
			changed := claims
			changed.AllowedSiteIDs = []string{testSiteA2}
			_, err := codec.Decode(cursor, "site-assets", testSiteA1, registryauth.ActionAssetList, changed)
			return err
		},
		"tamper": func() error {
			_, err := codec.Decode(cursor+"x", "site-assets", testSiteA1, registryauth.ActionAssetList, claims)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := decode(); err == nil {
				t.Fatal("cursor unexpectedly accepted")
			}
		})
	}
}
