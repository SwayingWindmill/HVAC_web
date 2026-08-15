package identitycontext_test

import (
	"strings"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/identitycontext"
)

func TestEffectiveAuthorizationAcceptsCanonicalCapabilitySet(t *testing.T) {
	authorization := identitycontext.EffectiveAuthorization{
		CapabilitySetVersion: identitycontext.CapabilitySetVersion,
		PolicyRevision:       "cap-v2:registry-and-telemetry",
		Capabilities: []identitycontext.Capability{
			identitycontext.CapabilitySiteList,
			identitycontext.CapabilitySiteRead,
			identitycontext.CapabilityTelemetryBatchRead,
		},
	}
	if err := authorization.Validate(); err != nil {
		t.Fatalf("valid effective authorization rejected: %v", err)
	}
}

func TestEffectiveAuthorizationRejectsMalformedCapabilitySets(t *testing.T) {
	tests := []struct {
		name          string
		authorization identitycontext.EffectiveAuthorization
	}{
		{
			name: "unsupported version",
			authorization: identitycontext.EffectiveAuthorization{
				CapabilitySetVersion: 1,
				PolicyRevision:       "registry-read:7",
				Capabilities:         []identitycontext.Capability{},
			},
		},
		{
			name: "missing policy revision",
			authorization: identitycontext.EffectiveAuthorization{
				CapabilitySetVersion: identitycontext.CapabilitySetVersion,
				Capabilities:         []identitycontext.Capability{},
			},
		},
		{
			name: "oversized policy revision",
			authorization: identitycontext.EffectiveAuthorization{
				CapabilitySetVersion: identitycontext.CapabilitySetVersion,
				PolicyRevision:       strings.Repeat("x", 129),
				Capabilities:         []identitycontext.Capability{},
			},
		},
		{
			name: "duplicate capability",
			authorization: identitycontext.EffectiveAuthorization{
				CapabilitySetVersion: identitycontext.CapabilitySetVersion,
				PolicyRevision:       "registry-read:7",
				Capabilities: []identitycontext.Capability{
					identitycontext.CapabilitySiteRead,
					identitycontext.CapabilitySiteRead,
				},
			},
		},
		{
			name: "unsupported capability",
			authorization: identitycontext.EffectiveAuthorization{
				CapabilitySetVersion: identitycontext.CapabilitySetVersion,
				PolicyRevision:       "registry-read:7",
				Capabilities:         []identitycontext.Capability{"role.admin"},
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if err := testCase.authorization.Validate(); err == nil {
				t.Fatal("malformed effective authorization was accepted")
			}
		})
	}
}
