package credentials

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

const (
	// AdminRole is the Talos API admin role that grants full access.
	AdminRole = "os:admin"

	// CertValidityDuration is the validity period for generated client certificates.
	// Short-lived certificates reduce the window of exposure if compromised.
	CertValidityDuration = 24 * time.Hour
)

// GenerateTLSConfig creates a TLS configuration with a client certificate
// that has the os:admin role, using the provided CA certificate and key.
func GenerateTLSConfig(caCertB64, caKeyB64 string) (*tls.Config, error) {
	// Parse CA certificate
	caCert, err := parseCACertificate(caCertB64)
	if err != nil {
		return nil, fmt.Errorf("CA certificate: %w", err)
	}

	// Parse CA private key
	caKey, err := parseCAPrivateKey(caKeyB64)
	if err != nil {
		return nil, fmt.Errorf("CA private key: %w", err)
	}

	// Generate a new key pair for the client certificate
	clientPub, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate client key pair: %w", err)
	}

	// Create client certificate template
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	clientCertTemplate := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{AdminRole}, // This grants os:admin role
			CommonName:   "autobootstrap-extension",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(CertValidityDuration),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	// Sign the client certificate with the CA
	clientCertDER, err := x509.CreateCertificate(rand.Reader, clientCertTemplate, caCert, clientPub, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create client certificate: %w", err)
	}

	// Create TLS certificate
	clientCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCertDER})
	clientKeyPEM, err := marshalED25519PrivateKey(clientPriv)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal client key: %w", err)
	}

	clientTLSCert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS key pair: %w", err)
	}

	// Create CA certificate pool
	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(caCert)

	return &tls.Config{
		Certificates: []tls.Certificate{clientTLSCert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// parseCACertificate decodes and parses a base64-encoded PEM CA certificate.
func parseCACertificate(caCertB64 string) (*x509.Certificate, error) {
	if caCertB64 == "" {
		return nil, fmt.Errorf("certificate data is empty")
	}

	caCertPEM, err := base64.StdEncoding.DecodeString(caCertB64)
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}

	if len(caCertPEM) == 0 {
		return nil, fmt.Errorf("decoded certificate data is empty")
	}

	block, _ := pem.Decode(caCertPEM)
	if block == nil {
		return nil, fmt.Errorf("PEM decode failed: no valid PEM block found")
	}

	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("unexpected PEM block type %q, expected CERTIFICATE", block.Type)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("x509 parse failed: %w", err)
	}

	return cert, nil
}

// parseCAPrivateKey decodes and parses a base64-encoded PEM CA private key.
func parseCAPrivateKey(caKeyB64 string) (any, error) {
	if caKeyB64 == "" {
		return nil, fmt.Errorf("key data is empty")
	}

	caKeyPEM, err := base64.StdEncoding.DecodeString(caKeyB64)
	if err != nil {
		return nil, fmt.Errorf("base64 decode failed: %w", err)
	}

	if len(caKeyPEM) == 0 {
		return nil, fmt.Errorf("decoded key data is empty")
	}

	block, _ := pem.Decode(caKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("PEM decode failed: no valid PEM block found")
	}

	key, err := parsePrivateKey(block)
	if err != nil {
		return nil, fmt.Errorf("parse failed: %w", err)
	}

	return key, nil
}

// parsePrivateKey parses a PEM block into a private key.
// Supports ED25519, ECDSA, and RSA keys.
func parsePrivateKey(block *pem.Block) (any, error) {
	switch block.Type {
	case "ED25519 PRIVATE KEY":
		return x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		return x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		// Try PKCS8 first, then other formats
		if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			return key, nil
		}
		if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
			return key, nil
		}
		if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			return key, nil
		}
		return nil, fmt.Errorf("unsupported key type: %s", block.Type)
	}
}

// marshalED25519PrivateKey marshals an ED25519 private key to PEM format.
func marshalED25519PrivateKey(key ed25519.PrivateKey) ([]byte, error) {
	keyBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}), nil
}
