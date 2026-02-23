// Package tlsconf derives deterministic TLS credentials from a passphrase.
//
// The private key is derived deterministically via HKDF so both sides produce
// the same key from the same passphrase. The certificate is generated with
// crypto/rand (not deterministic) but clients verify the server's public key
// directly via VerifyPeerCertificate rather than pinning the cert itself.
//
// Same passphrase → public keys match → connection succeeds, traffic encrypted.
// Different passphrases → public keys differ → connection fails immediately.
// No certificate distribution, no CA, no PKI.
//
// Key derivation:
//
//	HKDF-SHA256(ikm=passphrase, salt="suffuse-tls-v1", info="private-key")
//	→ 64 bytes → reduced mod curve order → deterministic ECDSA P-256 key
package tlsconf

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"time"

	"golang.org/x/crypto/hkdf"
	"google.golang.org/grpc/credentials"
)

// DefaultPassphrase is used when no --token flag is provided.
const DefaultPassphrase = "suffuse"

// ServerConfig returns a *tls.Config for use with tls.NewListener and the
// matching gRPC client TransportCredentials.
//
// NextProtos ["h2", "http/1.1"] lets ALPN negotiate correctly for both gRPC
// and HTTP/JSON clients on the same listener.
func ServerConfig(passphrase string) (serverCfg *tls.Config, clientCreds credentials.TransportCredentials, err error) {
	key, err := deriveKey(passphrase)
	if err != nil {
		return nil, nil, fmt.Errorf("tlsconf: derive key: %w", err)
	}

	certPEM, err := selfSignedCert(key)
	if err != nil {
		return nil, nil, fmt.Errorf("tlsconf: cert: %w", err)
	}

	keyPEM, err := marshalKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("tlsconf: marshal key: %w", err)
	}

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("tlsconf: key pair: %w", err)
	}

	serverCfg = &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"h2", "http/1.1"},
		MinVersion:   tls.VersionTLS13,
	}

	// Derive the expected public key bytes once for the client verifier.
	expectedPub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("tlsconf: marshal pubkey: %w", err)
	}

	clientCreds = credentials.NewTLS(&tls.Config{
		// Skip normal cert chain verification — we verify the public key instead.
		InsecureSkipVerify: true, //nolint:gosec
		ServerName:         "suffuse",
		MinVersion:         tls.VersionTLS13,
		// VerifyPeerCertificate checks that the server's public key matches
		// the key derived from our passphrase. Wrong passphrase = different
		// key = connection rejected.
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("tlsconf: server presented no certificate")
			}
			cert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("tlsconf: parse server cert: %w", err)
			}
			pub, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
			if err != nil {
				return fmt.Errorf("tlsconf: marshal server pubkey: %w", err)
			}
			if !bytes.Equal(pub, expectedPub) {
				return fmt.Errorf("tlsconf: server public key does not match passphrase")
			}
			return nil
		},
	})

	return serverCfg, clientCreds, nil
}

// ClientCredentials returns gRPC TransportCredentials derived from passphrase.
func ClientCredentials(passphrase string) (credentials.TransportCredentials, error) {
	_, creds, err := ServerConfig(passphrase)
	return creds, err
}

// deriveKey derives a deterministic ECDSA P-256 private key from passphrase.
func deriveKey(passphrase string) (*ecdsa.PrivateKey, error) {
	r := hkdf.New(sha256.New, []byte(passphrase), []byte("suffuse-tls-v1"), []byte("private-key"))
	buf := make([]byte, 64)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("hkdf read: %w", err)
	}

	curve := elliptic.P256()
	N := curve.Params().N
	k := new(big.Int).SetBytes(buf)
	k.Mod(k, new(big.Int).Sub(N, big.NewInt(1)))
	k.Add(k, big.NewInt(1)) // ensure k ∈ [1, N-1]

	key := new(ecdsa.PrivateKey)
	key.PublicKey.Curve = curve
	key.D = k
	key.PublicKey.X, key.PublicKey.Y = curve.ScalarBaseMult(k.Bytes())
	return key, nil
}

// selfSignedCert generates a self-signed certificate for key using crypto/rand.
// The cert contents don't matter for authentication — only the public key is
// verified by clients.
func selfSignedCert(key *ecdsa.PrivateKey) ([]byte, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "suffuse"},
		DNSNames:              []string{"suffuse"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(100 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), nil
}

func marshalKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}
