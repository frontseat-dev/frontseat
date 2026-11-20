// Package testauthority is a certificate authority of a test's own making, and
// a certificate it signed for a server on this machine. Nothing of it is
// kept: each test makes its own, and none is in the repository.
package testauthority

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Authority is what a client is given to know the server by, and what the
// server shows.
type Authority struct {
	// File is a PEM file of the authority's certificate.
	File string
	// Server is the server's certificate and key: for 127.0.0.1 and
	// localhost, or for the names it was made for.
	Server tls.Certificate
	// Certificate and Key are Server as PEM, for a server that reads files.
	Certificate, Key []byte
}

// New makes an authority and a server's certificate, good for an hour.
func New(t testing.TB) Authority {
	t.Helper()
	return Named(t)
}

// Named is New for a server known by other names than this machine's
// own: one a client that dials the loopback does not take for its server.
// A name that is an address is the certificate's address.
func Named(t testing.TB, names ...string) Authority {
	t.Helper()
	signer, signed := sign(t, "a test's authority", nil, nil, nil)
	server, _ := sign(t, "a test's server", &signer, signed, names)
	file := filepath.Join(t.TempDir(), "authority.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: signed.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(server.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	return Authority{
		File:        file,
		Server:      server,
		Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate[0]}),
		Key:         pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}),
	}
}

// sign makes a key and its certificate: an authority's own where no
// signer is given, a server's signed by the authority otherwise.
func sign(t testing.TB, name string, signer *tls.Certificate, authority *x509.Certificate, names []string) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
	}
	parent, signingKey := template, any(key)
	if signer == nil {
		template.IsCA, template.KeyUsage = true, x509.KeyUsageCertSign
	} else {
		// A server's, and a caller's where a server knows its callers
		// by certificate, as a gateway does.
		template.KeyUsage, template.ExtKeyUsage = x509.KeyUsageDigitalSignature, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}
		template.IPAddresses, template.DNSNames = []net.IP{net.IPv4(127, 0, 0, 1)}, []string{"localhost"}
		if len(names) > 0 {
			template.IPAddresses, template.DNSNames = nil, nil
			for _, each := range names {
				if ip := net.ParseIP(each); ip != nil {
					template.IPAddresses = append(template.IPAddresses, ip)
				} else {
					template.DNSNames = append(template.DNSNames, each)
				}
			}
		}
		parent, signingKey = authority, signer.PrivateKey
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, signingKey)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, parsed
}
