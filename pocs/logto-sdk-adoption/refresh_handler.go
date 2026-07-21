package logtopoc

import (
	"net/http"
	"time"
)

func (fixture *oidcFixture) handleRefresh(writer http.ResponseWriter, request *http.Request) {
	fixture.refreshRequests.Add(1)
	accessValue := opaqueValue("refresh-access")
	if organizationID := request.Form.Get(protocolField("organization", "id")); organizationID != "" {
		forged, err := signJWT(fixture.attackerKey, "attacker-key-not-in-jwks", map[string]any{
			"iss": "https://attacker.invalid", "sub": "logto-subject-01", "aud": "urn:logto:organization:" + organizationID,
			"exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix(),
			"client_id": "hvac-web", "jti": "attacker-jti", "scope": "all",
		})
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		accessValue = forged
	}
	writeJSON(writer, protocolPayload(accessValue, opaqueValue("refresh-rotation"), ""))
}
