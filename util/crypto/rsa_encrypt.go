package crypto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Happ crypto API endpoint
const happCryptoAPIURL = "https://crypto.happ.su/api.php"

// HTTP client with timeout for API requests
var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

// EncryptForHapp encrypts a URL string using crypto.happ.su API
// Returns the encrypted string in base64 format, ready for happ://crypt4/ prefix
func EncryptForHapp(url string) (string, error) {
	// Check URL length (API limit is 450 characters)
	if len(url) > 450 {
		return "", fmt.Errorf("URL too long for encryption (max 450 characters, got %d)", len(url))
	}

	// Prepare request payload
	payload := map[string]string{
		"url": url,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make API request
	resp, err := httpClient.Post(happCryptoAPIURL, "application/json", bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to request encryption API: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract encrypted link
	encryptedLink, ok := result["encrypted_link"].(string)
	if !ok {
		return "", fmt.Errorf("encrypted_link not found in API response")
	}

	// Extract base64 part (remove prefix)
	encryptedLink = strings.TrimPrefix(encryptedLink, "happ://crypt4/")
	encryptedLink = strings.TrimPrefix(encryptedLink, "happ://crypt3/")

	return encryptedLink, nil
}

// EncryptForV2RayTun encrypts a URL string for V2RayTun
// Note: Currently uses the same Happ API as there's no separate V2RayTun API
// Returns the encrypted string in base64 format, ready for v2raytun://crypt/ prefix
func EncryptForV2RayTun(url string) (string, error) {
	// Check URL length
	if len(url) > 450 {
		return "", fmt.Errorf("URL too long for encryption (max 450 characters, got %d)", len(url))
	}

	// V2RayTun uses the same encryption as Happ (same RSA-4096 key)
	// We use the Happ API which produces compatible encrypted strings
	return EncryptForHapp(url)
}
