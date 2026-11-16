package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
)

func writePair(t *testing.T, c tls.Certificate, certFile, keyFile string) {
	t.Helper()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Certificate[0]})
	keyDER, err := x509.MarshalPKCS8PrivateKey(c.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
}
