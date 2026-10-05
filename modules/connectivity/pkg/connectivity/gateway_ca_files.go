package connectivity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// InitializeGatewayCA provisions only new CA files. The private key is mounted
// exclusively in Connectivity; nginx/Mosquitto receive individual public files.
func InitializeGatewayCA(directory string, workloadCA []byte, now time.Time) error {
	if _, err := os.Stat(filepath.Join(directory, "ca.crt")); err == nil {
		return initializeCredentialServer(directory, now)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "HVAC Gateway CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	public := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	for _, file := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"ca.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600},
		{"ca.crt", public, 0644},
		{"trusted-client-ca.pem", append(workloadCA, public...), 0644},
	} {
		f, err := os.OpenFile(filepath.Join(directory, file.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, file.mode)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(file.data)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return initializeCredentialServer(directory, now)
}

func initializeCredentialServer(directory string, now time.Time) error {
	cert, err := os.ReadFile(filepath.Join(directory, "ca.crt"))
	if err != nil {
		return err
	}
	private, err := os.ReadFile(filepath.Join(directory, "ca.key"))
	if err != nil {
		return err
	}
	ca, err := NewCertificateAuthority(cert, private, "tls://localhost:8883")
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "connectivity"}, DNSNames: []string{"connectivity"}, NotBefore: now.Add(-time.Hour), NotAfter: ca.certificate.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.certificate, &key.PublicKey, ca.signer)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	for _, file := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"connectivity.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644},
		{"connectivity.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600},
	} {
		f, err := os.OpenFile(filepath.Join(directory, file.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, file.mode)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(file.data)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
