package optimization

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func writePKCS8PrivateKeyPEM(t *testing.T, path string, key any) {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	block := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: der,
	})
	if err := os.WriteFile(path, block, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func TestLoadEdgeModelSigningKeyFromFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	validPath := filepath.Join(dir, "ota-signing-key.pem")
	writePKCS8PrivateKeyPEM(t, validPath, edPriv)

	keyID, loaded, err := LoadEdgeModelSigningKeyFromFile("  custom-ota-v2  ", validPath)
	if err != nil {
		t.Fatalf("LoadEdgeModelSigningKeyFromFile(explicit): %v", err)
	}
	if keyID != "custom-ota-v2" {
		t.Fatalf("keyID = %q, want custom-ota-v2", keyID)
	}
	if !loaded.Equal(edPriv) {
		t.Fatal("loaded Ed25519 key does not equal source private key")
	}

	defaultID, loadedDefault, err := LoadEdgeModelSigningKeyFromFile("   ", validPath)
	if err != nil {
		t.Fatalf("LoadEdgeModelSigningKeyFromFile(default keyID): %v", err)
	}
	if defaultID != DefaultEdgeModelSigningKeyID {
		t.Fatalf("default keyID = %q, want %q", defaultID, DefaultEdgeModelSigningKeyID)
	}
	if !loadedDefault.Equal(edPriv) {
		t.Fatal("loaded Ed25519 key with default ID does not equal source private key")
	}

	ecdsaPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaPath := filepath.Join(dir, "ecdsa-key.pem")
	writePKCS8PrivateKeyPEM(t, ecdsaPath, ecdsaPriv)

	invalidPEMPath := filepath.Join(dir, "not-pem.pem")
	if err := os.WriteFile(invalidPEMPath, []byte("not-a-pem"), 0o600); err != nil {
		t.Fatal(err)
	}

	trailingPEMPath := filepath.Join(dir, "trailing.pem")
	validBytes, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trailingPEMPath, append(validBytes, []byte("EXTRA_BYTES\n")...), 0o600); err != nil {
		t.Fatal(err)
	}

	wrongTypePath := filepath.Join(dir, "cert-type.pem")
	if err := os.WriteFile(wrongTypePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1, 2, 3}}), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "empty path", path: "   "},
		{name: "missing file", path: filepath.Join(dir, "does-not-exist.pem")},
		{name: "directory path", path: dir},
		{name: "invalid PEM", path: invalidPEMPath},
		{name: "trailing data", path: trailingPEMPath},
		{name: "wrong PEM block type", path: wrongTypePath},
		{name: "non-Ed25519 PKCS#8 key", path: ecdsaPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := LoadEdgeModelSigningKeyFromFile("", tc.path); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestOptimizationServiceSignsEdgeModelBundlesWithPKCS8PEMKey(t *testing.T) {
	t.Parallel()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pemPath := filepath.Join(t.TempDir(), "ota-signing-key.pem")
	writePKCS8PrivateKeyPEM(t, pemPath, priv)

	keyID, loadedPriv, err := LoadEdgeModelSigningKeyFromFile("", pemPath)
	if err != nil {
		t.Fatalf("LoadEdgeModelSigningKeyFromFile: %v", err)
	}

	fixture := newOptimizationFixture(t)
	store := newMemoryEdgeModelStore()
	fixture.service.
		WithEdgeModelStore(store).
		WithEdgeModelSigningKey(keyID, loadedPriv).
		WithSiteVisibility(stubSiteVisibility{tenantID: testTenant, siteID: testSite})

	gwID := "018f3d00-2000-7000-8000-000000000042"
	min1, max1 := 6.5, 10.5
	maxDev1 := 1.5
	artV1, err := fixture.service.CreateEdgeModelArtifact(t.Context(), testTenant, testSite, "operator:signing-test", CreateEdgeModelArtifactRequest{
		Domain:             edgecontrol.EdgeModelDomainControl,
		ModelKey:           "chiller-cop-opt",
		Version:            "1.0.0",
		MaxShadowDeviation: &maxDev1,
		GatewayID:          gwID,
		Hyperparams: edgecontrol.EdgeModelHyperparams{
			COPCoefficients:        []float64{3.8, 0.06, -0.002},
			SupplyTempSetpointMinC: &min1,
			SupplyTempSetpointMaxC: &max1,
		},
	})
	if err != nil {
		t.Fatalf("CreateEdgeModelArtifact(v1): %v", err)
	}

	min2, max2 := 7.0, 11.0
	maxDev2 := 1.0
	artV2, err := fixture.service.CreateEdgeModelArtifact(t.Context(), testTenant, testSite, "operator:signing-test", CreateEdgeModelArtifactRequest{
		Domain:             edgecontrol.EdgeModelDomainControl,
		ModelKey:           "chiller-cop-opt",
		Version:            "1.1.0",
		MaxShadowDeviation: &maxDev2,
		GatewayID:          gwID,
		Hyperparams: edgecontrol.EdgeModelHyperparams{
			COPCoefficients:        []float64{4.0, 0.05, -0.002},
			SupplyTempSetpointMinC: &min2,
			SupplyTempSetpointMaxC: &max2,
		},
	})
	if err != nil {
		t.Fatalf("CreateEdgeModelArtifact(v2): %v", err)
	}

	for _, id := range []string{artV1.ID, artV2.ID} {
		if _, err := fixture.service.ApproveEdgeModelArtifact(t.Context(), testTenant, testSite, id, "reviewer:signing-test", ApproveEdgeModelRequest{
			Decision: "APPROVED",
			Reason:   "signing integration check",
		}); err != nil {
			t.Fatalf("ApproveEdgeModelArtifact(%s): %v", id, err)
		}
	}

	if _, err := fixture.service.DeployEdgeModelArtifact(t.Context(), testTenant, testSite, DeployEdgeModelRequest{
		ArtifactID:  artV1.ID,
		Environment: "PRODUCTION",
		GatewayID:   gwID,
	}); err != nil {
		t.Fatalf("DeployEdgeModelArtifact(PRODUCTION): %v", err)
	}
	if _, err := fixture.service.DeployEdgeModelArtifact(t.Context(), testTenant, testSite, DeployEdgeModelRequest{
		ArtifactID:  artV2.ID,
		Environment: "SHADOW",
		GatewayID:   gwID,
	}); err != nil {
		t.Fatalf("DeployEdgeModelArtifact(SHADOW): %v", err)
	}

	ws, err := fixture.service.GetEdgeModelWorkspace(t.Context(), testTenant, testSite)
	if err != nil {
		t.Fatalf("GetEdgeModelWorkspace: %v", err)
	}
	if len(ws.DesiredBundles) != 2 {
		t.Fatalf("desiredBundles count = %d, want 2", len(ws.DesiredBundles))
	}

	wire, err := json.Marshal(ws.DesiredBundles)
	if err != nil {
		t.Fatalf("marshal desiredBundles: %v", err)
	}
	var decoded []edgecontrol.EdgeModelBundle
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("unmarshal desiredBundles: %v", err)
	}

	trustedKeys := map[string]string{
		DefaultEdgeModelSigningKeyID: base64.StdEncoding.EncodeToString(pub),
	}
	for _, b := range decoded {
		if b.KeyID != DefaultEdgeModelSigningKeyID {
			t.Fatalf("bundle %s keyId = %q, want %q", b.Version, b.KeyID, DefaultEdgeModelSigningKeyID)
		}
		if b.Signature == "" {
			t.Fatalf("bundle %s missing signature", b.Version)
		}
		if err := edgecontrol.ValidateEdgeModelBundle(b, trustedKeys, true); err != nil {
			t.Fatalf("ValidateEdgeModelBundle(%s): %v", b.Version, err)
		}
	}

	tampered := decoded[0].Clone()
	tampered.Version = "9.9.9"
	if err := edgecontrol.ValidateEdgeModelBundle(tampered, trustedKeys, true); !errors.Is(err, edgecontrol.ErrEdgeModelSignature) {
		t.Fatalf("expected ErrEdgeModelSignature for tampered bundle, got %v", err)
	}
	if err := edgecontrol.ValidateEdgeModelBundle(decoded[0], map[string]string{"other-key": trustedKeys[DefaultEdgeModelSigningKeyID]}, true); !errors.Is(err, edgecontrol.ErrEdgeModelSignature) {
		t.Fatalf("expected ErrEdgeModelSignature for unknown keyId, got %v", err)
	}
}
