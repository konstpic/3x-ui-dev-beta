package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
)

// Happ RSA-4096 Public Key
const happPublicKey = `-----BEGIN PUBLIC KEY-----
MIICIjANBgkqhkiG9w0BAQEFAAOCAg8AMIICCgKCAgEAlBetA0wjbaj+h7oJ/d/h
pNrXvAcuhOdFGEFcfCxSWyLzWk4SAQ05gtaEGZyetTax2uqagi9HT6lapUSUe2S8
nMLJf5K+LEs9TYrhhBdx/B0BGahA+lPJa7nUwp7WfUmSF4hir+xka5ApHjzkAQn6
cdG6FKtSPgq1rYRPd1jRf2maEHwiP/e/jqdXLPP0SFBjWTMt/joUDgE7v/IGGB0L
Q7mGPAlgmxwUHVqP4bJnZ//5sNLxWMjtYHOYjaV+lixNSfhFM3MdBndjpkmgSfmg
D5uYQYDL29TDk6Eu+xetUEqry8ySPjUbNWdDXCglQWMxDGjaqYXMWgxBA1UKjUBW
wbgr5yKTJ7mTqhlYEC9D5V/LOnKd6pTSvaMxkHXwk8hBWvUNWAxzAf5JZ7EVE3jt
0j682+/hnmL/hymUE44yMG1gCcWvSpB3BTlKoMnl4yrTakmdkbASeFRkN3iMRewa
IenvMhzJh1fq7xwX94otdd5eLB2vRFavrnhOcN2JJAkKTnx9dwQwFpGEkg+8U613
+Tfm/f82l56fFeoFN98dD2mUFLFZoeJ5CG81ZeXrH83niI0joX7rtoAZIPWzq3Y1
Zb/Zq+kK2hSIhphY172Uvs8X2Qp2ac9UoTPM71tURsA9IvPNvUwSIo/aKlX5KE3I
VE0tje7twWXL5Gb1sfcXRzsCAwEAAQ==
-----END PUBLIC KEY-----`

// V2RayTun RSA-4096 Public Key
const v2raytunPublicKey = `-----BEGIN PUBLIC KEY-----
MIICIjANBgkqhkiG9w0BAQEFAAOCAg8AMIICCgKCAgEArK77160gyNm0olpdA+WO
f1ClV4ndeRhBDYPQYs4hUq3YDwP1dQTdxaILcdnYJS2Wpfzqo8JiAvwhatBHJ2Kq
p/KSll5JSoqYAKj+1GdSF+nOCXc3wBeGR8mD6KdSRnoAE+x6wZcydNggQluClcx3
zTGjwWnBxUWfKlcQeHxHTtO+2i6Dga2o4it5J2uXOupEo9mrBZdc1BSKvrmoycMp
iaRF4YKRhwY1jZnEjx2BKA/xFQIDiIFQIAIKKPNKoIWbnQ66lEJSOr1DuIVGYgdr
xyupIQW3rvkGirybgx0+lIOn9J7c9doBDWHknOqGG0VeGKiVFMv5klG7KTsH89qe
nXfgrGQVEknAeGOrMPgjF+Zs52eHLeaWXCr4sCRgvAoPeBfMTfavu/Y0mnufD8SN
z8OdQSVT9jphXeM2YXtnwi971fsF97bykEK5ytco4zf9hgbEjioU7/cAvz20RyxY
EFCouOZsGXkwlUq+xDEPRIyQj2OwGl5xpjDJ4uAq5Shi4EUk01wUfRzTVDQIWJXC
O7Z9K4FcKRKY3m42fWr8fZl5rQbnmrLMLnD88n7VZBRkIhfnt7XHtTCVWBwCDqsG
ceUX0Xf+ZwQ8tNYfE5ipy6RlkuZD8Ddlpk8qhstCBu82igNfRcSsJ5KT36aAhfZ+
WYqjdHmjzdjEGJqpfg1K1JMCAwEAAQ==
-----END PUBLIC KEY-----`

// parsePublicKey parses a PEM-encoded RSA public key
func parsePublicKey(pemKey string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA public key")
	}

	return rsaPub, nil
}

// EncryptForHapp encrypts a URL string using Happ's RSA-4096 public key
// Returns the encrypted string in base64 format, ready for happ://crypt4/ prefix
func EncryptForHapp(url string) (string, error) {
	pubKey, err := parsePublicKey(happPublicKey)
	if err != nil {
		return "", fmt.Errorf("failed to parse Happ public key: %w", err)
	}

	// RSA encryption with OAEP padding
	// For RSA-4096, we can encrypt up to 446 bytes (4096/8 - 2*SHA256 - 2)
	// If URL is longer, we need to split it or use hybrid encryption
	// For simplicity, we'll encrypt directly if it fits, otherwise return error
	urlBytes := []byte(url)
	
	// Calculate maximum plaintext size for RSA-4096 with OAEP SHA-256
	maxPlaintextSize := (pubKey.N.BitLen() / 8) - 2*32 - 2 // 446 bytes for RSA-4096
	
	if len(urlBytes) > maxPlaintextSize {
		return "", fmt.Errorf("URL too long for RSA-4096 encryption (max %d bytes, got %d)", maxPlaintextSize, len(urlBytes))
	}

	hash := sha256.New()
	encrypted, err := rsa.EncryptOAEP(
		hash,
		rand.Reader,
		pubKey,
		urlBytes,
		nil, // No label
	)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt: %w", err)
	}

	// Return base64 encoded encrypted data (standard base64, not URL-safe)
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// EncryptForV2RayTun encrypts a URL string using V2RayTun's RSA-4096 public key
// Returns the encrypted string in base64 format, ready for v2raytun://crypt/ prefix
func EncryptForV2RayTun(url string) (string, error) {
	pubKey, err := parsePublicKey(v2raytunPublicKey)
	if err != nil {
		return "", fmt.Errorf("failed to parse V2RayTun public key: %w", err)
	}

	urlBytes := []byte(url)
	
	// Calculate maximum plaintext size for RSA-4096 with OAEP SHA-256
	maxPlaintextSize := (pubKey.N.BitLen() / 8) - 2*32 - 2 // 446 bytes for RSA-4096
	
	if len(urlBytes) > maxPlaintextSize {
		return "", fmt.Errorf("URL too long for RSA-4096 encryption (max %d bytes, got %d)", maxPlaintextSize, len(urlBytes))
	}

	hash := sha256.New()
	encrypted, err := rsa.EncryptOAEP(
		hash,
		rand.Reader,
		pubKey,
		urlBytes,
		nil, // No label
	)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt: %w", err)
	}

	// Return base64 encoded encrypted data (standard base64, not URL-safe)
	return base64.StdEncoding.EncodeToString(encrypted), nil
}
