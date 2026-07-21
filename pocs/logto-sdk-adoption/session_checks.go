package logtopoc

import (
	"fmt"
	"net/http"

	logtoclient "github.com/logto-io/go/v2/client"
)

func seedAuthenticatedStorage(storage *memoryStorage, label string) {
	storage.SetItem(logtoclient.StorageKeyIdToken, opaqueValue(label+"-identity"))
	storage.SetItem(logtoclient.StorageKeyRefreshToken, opaqueValue(label+"-rotation"))
}

func runRefreshCheck(fixture *oidcFixture) (Check, error) {
	storage := newMemoryStorage()
	seedAuthenticatedStorage(storage, "shared")
	first := newClient(fixture, storage)
	second := newClient(fixture, storage)
	fixture.refreshRequests.Store(0)
	start := make(chan struct{})
	errorsChannel := make(chan error, 2)
	for _, candidate := range []*logtoclient.LogtoClient{first, second} {
		go func(client *logtoclient.LogtoClient) {
			<-start
			_, err := client.GetAccessToken("")
			errorsChannel <- err
		}(candidate)
	}
	close(start)
	for range 2 {
		if err := <-errorsChannel; err != nil {
			return Check{}, err
		}
	}
	requests := fixture.refreshRequests.Load()
	return Check{
		Passed:   requests == 1,
		Evidence: fmt.Sprintf("two SDK clients sharing one durable storage issued %d refresh requests", requests),
	}, nil
}

func runSignOutChecks(fixture *oidcFixture) (observable Check, localClear Check) {
	storage := newMemoryStorage()
	seedAuthenticatedStorage(storage, "signout")
	client := newClient(fixture, storage)
	fixture.revokeStatus.Store(http.StatusInternalServerError)
	fixture.revokeRequests.Store(0)
	signOutURL, signOutErr := client.SignOut("https://app.example.test/signed-out")
	observable = Check{
		Passed:   signOutErr != nil,
		Evidence: "revocation endpoint returned HTTP 500; SDK returned " + errorText(signOutErr),
	}
	localClear = Check{
		Passed:   storage.GetItem(logtoclient.StorageKeyIdToken) == "" && storage.GetItem(logtoclient.StorageKeyRefreshToken) == "" && signOutURL != "",
		Evidence: "SDK cleared local credential storage and generated an end-session URL",
	}
	return observable, localClear
}

func runOrganizationClaimCheck(fixture *oidcFixture) (Check, error) {
	storage := newMemoryStorage()
	seedAuthenticatedStorage(storage, "organization")
	client := newClient(fixture, storage)
	claims, err := client.GetOrganizationTokenClaims("org-01")
	if err != nil {
		return Check{}, err
	}
	acceptedForged := claims.Jti == "attacker-jti" && claims.Iss == "https://attacker.invalid"
	return Check{
		Passed:   !acceptedForged,
		Evidence: "organization claims method accepted a value signed by a key absent from the provider JWKS",
	}, nil
}
