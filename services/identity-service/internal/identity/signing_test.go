package identity

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSigningKeyFileLoadsStableIdentity(t *testing.T) {
	encoded, err := GenerateSigningKeyPEM()
	if err != nil {
		t.Fatalf("GenerateSigningKeyPEM() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "signing-key.pem")
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	first, err := LoadSigningKeyFile(path)
	if err != nil {
		t.Fatalf("first LoadSigningKeyFile() error = %v", err)
	}
	second, err := LoadSigningKeyFile(path)
	if err != nil {
		t.Fatalf("second LoadSigningKeyFile() error = %v", err)
	}
	if first.Kid == "" || first.Kid != second.Kid {
		t.Fatalf("stable kid expected, got %q then %q", first.Kid, second.Kid)
	}
	if !reflect.DeepEqual(first.PublicJWK, second.PublicJWK) {
		t.Fatal("public JWK changed while loading the same key file")
	}
}
