package core

import (
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

func validUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded := make([]byte, 16)
	if _, err := hex.Decode(decoded, []byte(compact)); err != nil {
		return false
	}
	return decoded[6]>>4 == 7 && decoded[8]>>6 == 2
}

func validateGrantScopeIDs(claims registryauth.GrantClaims) error {
	if !validUUIDv7(claims.PrincipalID) || !validUUIDv7(claims.TenantID) {
		return errors.New("registry grant identity identifiers are invalid")
	}
	for _, values := range [][]string{
		claims.AllowedSiteIDs,
		claims.DeniedSiteIDs,
	} {
		for _, value := range values {
			if !validUUIDv7(value) {
				return errors.New("registry grant scope identifier is invalid")
			}
		}
	}
	return nil
}

func postgresUUIDArray(values []string) string {
	copyValues := append([]string(nil), values...)
	sort.Strings(copyValues)
	return "{" + strings.Join(copyValues, ",") + "}"
}

func normalizedLimit(value int) (int, error) {
	if value == 0 {
		return DefaultPageLimit, nil
	}
	if value < 1 || value > MaximumPageLimit {
		return 0, ErrInvalidPage
	}
	return value, nil
}

func normalizedPageRequest(page PageRequest) (PageRequest, error) {
	limit, err := normalizedLimit(page.Limit)
	if err != nil {
		return PageRequest{}, err
	}
	if (page.DisplayName == "") != (page.ID == "") {
		return PageRequest{}, ErrInvalidPage
	}
	if page.ID != "" && !validUUIDv7(page.ID) {
		return PageRequest{}, ErrInvalidPage
	}
	page.Limit = limit
	return page, nil
}
