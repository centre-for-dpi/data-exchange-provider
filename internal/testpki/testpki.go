// Package testpki is an in-memory PKI for tests only: a network CA that signs node, gateway and
// consumer-application certificates (as the portal's step-ca does), and a foreign CA. It lives
// outside _test.go files so several packages' tests can share it; production code never imports it.
package testpki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"sync/atomic"
	"time"
)

var serial atomic.Int64

// Issuer is a CA certificate and its key.
type Issuer struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
}

// PKI is the network CA, the certificates the tests need and a foreign CA.
type PKI struct {
	CA             Issuer
	Foreign        Issuer
	Roots          *x509.CertPool
	Node           tls.Certificate // DNS:<nodeName>, serverAuth
	Gateway        tls.Certificate // CN=<gatewayClientName>, clientAuth, from the network CA
	ConsumerApp    tls.Certificate // CN=app-consumidora-123, clientAuth, from the network CA
	ForeignGateway tls.Certificate // CN=<gatewayClientName>, clientAuth, from the foreign CA
}

// New builds the PKI for one node and its entity's gateway client name.
func New(nodeName, gatewayClientName string) *PKI {
	ca, foreign := NewCA("DEN test network CA"), NewCA("Some other CA")
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	return &PKI{
		CA: ca, Foreign: foreign, Roots: roots,
		Node:           ca.Issue("den-node", []string{nodeName}, x509.ExtKeyUsageServerAuth),
		Gateway:        ca.Issue(gatewayClientName, nil, x509.ExtKeyUsageClientAuth),
		ConsumerApp:    ca.Issue("app-consumidora-123", nil, x509.ExtKeyUsageClientAuth),
		ForeignGateway: foreign.Issue(gatewayClientName, nil, x509.ExtKeyUsageClientAuth),
	}
}

// NewCA makes a self-signed CA.
func NewCA(name string) Issuer {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial.Add(1)), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	cert, _ := x509.ParseCertificate(der)
	return Issuer{cert, key}
}

// NewIntermediate makes an intermediate CA signed by ca (step-ca signs leaves with one).
func (ca Issuer) NewIntermediate(name string) Issuer {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial.Add(1)), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, MaxPathLenZero: true,
	}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, &key.PublicKey, ca.Key)
	cert, _ := x509.ParseCertificate(der)
	return Issuer{cert, key}
}

// Issue makes a leaf certificate with a fresh key. DNS names that parse as IPs become IP SANs.
func (ca Issuer) Issue(cn string, names []string, usage x509.ExtKeyUsage) tls.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der := ca.sign(cn, names, usage, &key.PublicKey)
	return tls.Certificate{Certificate: [][]byte{der, ca.Cert.Raw}, PrivateKey: key}
}

func (ca Issuer) sign(cn string, names []string, usage x509.ExtKeyUsage, pub any) []byte {
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial.Add(1)), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else {
			tpl.DNSNames = append(tpl.DNSNames, n)
		}
	}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, pub, ca.Key)
	return der
}

// SignCSR signs a node CSR as the portal does: the subject from the CSR, the node's DNS name as
// the only SAN, serverAuth and clientAuth (the node serves and calls the control channel).
func (ca Issuer) SignCSR(csrPEM, dnsName string) (string, error) {
	block, _ := pem.Decode([]byte(csrPEM))
	if block == nil {
		return "", errors.New("not a PEM CSR")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return "", err
	}
	if err := csr.CheckSignature(); err != nil {
		return "", err
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial.Add(1)), Subject: csr.Subject, DNSNames: []string{dnsName},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, csr.PublicKey, ca.Key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), nil
}

// CertPEM is the CA certificate in PEM.
func (ca Issuer) CertPEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Cert.Raw}))
}
