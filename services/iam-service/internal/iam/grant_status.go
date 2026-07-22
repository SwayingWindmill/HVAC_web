package iam

import (
	"context"
	"errors"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

const RegistryGrantStatusPath = registryauth.GrantStatusPath

type RegistryGrantStatus = registryauth.GrantStatus

type RegistryGrantStatusStore interface {
	LookupRegistryGrantStatus(context.Context, string, string) (RegistryGrantStatus, error)
}

type StaticRegistryGrantStatusStore struct {
	PolicyRevision  string
	RevokedTokenIDs map[string]struct{}
	Err             error
}

func (store StaticRegistryGrantStatusStore) LookupRegistryGrantStatus(_ context.Context, _ string, tokenID string) (RegistryGrantStatus, error) {
	if store.Err != nil {
		return RegistryGrantStatus{}, store.Err
	}
	if store.PolicyRevision == "" {
		return RegistryGrantStatus{}, errors.New("active IAM Registry policy is missing")
	}
	_, revoked := store.RevokedTokenIDs[tokenID]
	return RegistryGrantStatus{CurrentPolicyRevision: store.PolicyRevision, Revoked: revoked}, nil
}
