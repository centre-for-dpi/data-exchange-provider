// THROWAWAY SPIKE. Test PKI generated in memory: the network CA, the node certificate
// (DNS:rid-node-<id>, as the portal issues it), the gateway's client certificate for the entity
// (CN=rid-gateway.<slug>), a consumer application's certificate from the SAME network CA, and a
// certificate with the gateway's name from a foreign CA.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"time"
)

type testPKI struct {
	caPool        *x509.CertPool
	node          tls.Certificate
	gateway       tls.Certificate
	consumerApp   tls.Certificate
	foreignGateway tls.Certificate
}

type issuer struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newCA(name string) issuer {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	cert, _ := x509.ParseCertificate(der)
	return issuer{cert, key}
}

func (ca issuer) issue(cn string, dns []string, usage x509.ExtKeyUsage) tls.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     dns,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func newTestPKI(nodeName, gatewayClientName string) testPKI {
	network := newCA("RID test network CA")
	foreign := newCA("Some other CA")
	pool := x509.NewCertPool()
	pool.AddCert(network.cert)
	return testPKI{
		caPool:         pool,
		node:           network.issue("rid-node", []string{nodeName}, x509.ExtKeyUsageServerAuth),
		gateway:        network.issue(gatewayClientName, nil, x509.ExtKeyUsageClientAuth),
		consumerApp:    network.issue("app-consumidora-123", nil, x509.ExtKeyUsageClientAuth),
		foreignGateway: foreign.issue(gatewayClientName, nil, x509.ExtKeyUsageClientAuth),
	}
}
