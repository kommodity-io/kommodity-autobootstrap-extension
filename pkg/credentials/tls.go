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
)

// GenerateTLSConfig creates a TLS configuration with a client certificate
// that has the os:admin role, using the provided CA certificate and key.
func GenerateTLSConfig(caCertB64, caKeyB64 string) (*tls.Config, error) {
	// Decode base64 CA certificate
	caCertPEM, err := base64.StdEncoding.DecodeString(caCertB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode CA certificate: %w", err)
	}

	// Decode base64 CA key
	caKeyPEM, err := base64.StdEncoding.DecodeString(caKeyB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode CA key: %w", err)
	}

	// Parse CA certificate
	caCertBlock, _ := pem.Decode(caCertPEM)
	if caCertBlock == nil {
		return nil, fmt.Errorf("failed to parse CA certificate PEM")
	}

	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	// Parse CA private key
	caKeyBlock, _ := pem.Decode(caKeyPEM)
	if caKeyBlock == nil {
		return nil, fmt.Errorf("failed to parse CA key PEM")
	}

	caKey, err := parsePrivateKey(caKeyBlock)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA private key: %w", err)
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
		NotAfter:              time.Now().Add(24 * time.Hour), // Short-lived cert
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
