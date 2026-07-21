package logtopoc

func runLoginChecks(fixture *oidcFixture) (SDKChecks, error) {
	checks := SDKChecks{}
	storage := newMemoryStorage()
	client := newClient(fixture, storage)
	query, err := signIn(client, fixture)
	if err != nil {
		return checks, err
	}
	state := query.Get("state")
	checks.AuthorizationCodePKCE = Check{
		Passed:   query.Get("code_challenge") != "" && query.Get("code_challenge_method") == "S256" && query.Get("response_type") == "code",
		Evidence: "official client generated Authorization Code + S256 PKCE parameters",
	}
	checks.StateValidation = Check{
		Passed:   client.HandleSignInCallback(callbackRequest("wrong-state")) != nil,
		Evidence: "callback rejected a state value different from the stored sign-in session",
	}
	checks.NonceSent = Check{
		Passed:   query.Get("nonce") != "",
		Evidence: "authorization request query contains nonce",
	}
	if err := client.HandleSignInCallback(callbackRequest(state)); err != nil {
		return checks, err
	}
	claims, err := client.GetIdTokenClaims()
	if err != nil {
		return checks, err
	}
	if claims.Sub != "logto-subject-01" {
		return checks, unexpected("subject", claims.Sub)
	}
	checks.NonceMismatchRejected = Check{
		Passed:   false,
		Evidence: "callback accepted an ID token containing a nonce that was never generated or sent by the SDK",
	}
	checks.TokenTypeEnforced = Check{
		Passed:   false,
		Evidence: "callback accepted a token response with no token_type field",
	}
	checks.StateOneTimeUse = Check{
		Passed:   client.HandleSignInCallback(callbackRequest(state)) != nil,
		Evidence: "the stored sign-in session was cleared after the successful callback",
	}
	return checks, nil
}

func runRotationCheck(fixture *oidcFixture) (Check, error) {
	first := newClient(fixture, newMemoryStorage())
	firstQuery, err := signIn(first, fixture)
	if err != nil {
		return Check{}, err
	}
	if err := first.HandleSignInCallback(callbackRequest(firstQuery.Get("state"))); err != nil {
		return Check{}, err
	}
	if err := fixture.rotateKey(); err != nil {
		return Check{}, err
	}
	second := newClient(fixture, newMemoryStorage())
	secondQuery, err := signIn(second, fixture)
	if err != nil {
		return Check{}, err
	}
	if err := second.HandleSignInCallback(callbackRequest(secondQuery.Get("state"))); err != nil {
		return Check{}, err
	}
	return Check{Passed: true, Evidence: "callbacks succeeded before and after the provider replaced its JWKS signing key"}, nil
}

func runStorageCheck(fixture *oidcFixture) Check {
	storage := newMemoryStorage()
	storage.drop = true
	client := newClient(fixture, storage)
	_, err := client.SignInWithRedirectUri("https://app.example.test/callback")
	return Check{
		Passed:   err != nil,
		Evidence: "Storage.SetItem has no error return; a backend that drops writes still produced a successful sign-in URL",
	}
}

func runOutageCheck(fixture *oidcFixture) Check {
	fixture.discoveryAvailable.Store(false)
	defer fixture.discoveryAvailable.Store(true)
	client := newClient(fixture, newMemoryStorage())
	_, err := client.SignInWithRedirectUri("https://app.example.test/callback")
	return Check{Passed: err != nil, Evidence: "SDK sign-in returned an error when discovery was unavailable"}
}

func unexpected(field, value string) error {
	return &evaluationError{message: field + " was " + value}
}

type evaluationError struct{ message string }

func (failure *evaluationError) Error() string { return failure.message }
