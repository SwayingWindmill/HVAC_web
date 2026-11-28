package core

import (
	"errors"
	"sort"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

func validUUIDv7(value string) bool {
	return uuidv7.IsValidAnyCase(value)
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
