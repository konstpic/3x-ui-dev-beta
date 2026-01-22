package singbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/mhsanaei/3x-ui/v2/util/common"
)

// SingBoxAPI is an HTTP client for managing sing-box core configuration, inbounds, outbounds, and statistics.
type SingBoxAPI struct {
	baseURL      string
	httpClient   *http.Client
	isConnected  bool
}

// Init connects to the sing-box API server.
// sing-box uses HTTP API (not gRPC like xray).
func (s *SingBoxAPI) Init(apiPort int) error {
	if apiPort <= 0 || apiPort > math.MaxUint16 {
		return fmt.Errorf("invalid sing-box API port: %d", apiPort)
	}

	s.baseURL = fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	s.httpClient = &http.Client{
		Timeout: 10 * time.Second,
	}
	s.isConnected = true

	// Test connection
	if err := s.ping(); err != nil {
		s.isConnected = false
		return fmt.Errorf("failed to connect to sing-box API: %w", err)
	}

	return nil
}

// ping tests the connection to sing-box API.
func (s *SingBoxAPI) ping() error {
	// sing-box doesn't have a simple ping endpoint, so we'll try to get stats
	// If we can reach the API, connection is good
	resp, err := s.httpClient.Get(s.baseURL + "/stats")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// IsConnected checks if the HTTP connection is still active.
func (s *SingBoxAPI) IsConnected() bool {
	return s.isConnected && s.httpClient != nil
}

// Close closes the HTTP client connection.
func (s *SingBoxAPI) Close() {
	s.isConnected = false
	s.httpClient = nil
}

// GetTraffic queries traffic statistics from the sing-box core, optionally resetting counters.
// sing-box uses HTTP API with /stats endpoint.
func (s *SingBoxAPI) GetTraffic(reset bool) ([]*Traffic, []*ClientTraffic, error) {
	if !s.isConnected {
		return nil, nil, common.NewError("sing-box API is not initialized")
	}

	// sing-box stats endpoint format: GET /stats?reset=true
	url := s.baseURL + "/stats"
	if reset {
		url += "?reset=true"
	}

	resp, err := s.httpClient.Get(url)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query sing-box stats: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("sing-box API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse sing-box stats format
	// sing-box returns stats in format: {"inbound>>>tag>>>traffic>>>uplink": 123, ...}
	var stats map[string]int64
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, nil, fmt.Errorf("failed to parse stats: %w", err)
	}

	tagTrafficMap := make(map[string]*Traffic)
	emailTrafficMap := make(map[string]*ClientTraffic)

	for key, value := range stats {
		// Parse sing-box stat keys:
		// "inbound>>>tag>>>traffic>>>uplink" / "downlink"
		// "user>>>email>>>traffic>>>uplink" / "downlink"
		if matches := parseTrafficKey(key); matches != nil {
			if matches["type"] == "inbound" || matches["type"] == "outbound" {
				tag := matches["tag"]
				isInbound := matches["type"] == "inbound"
				isDown := matches["direction"] == "downlink"

				traffic, ok := tagTrafficMap[tag]
				if !ok {
					traffic = &Traffic{
						IsInbound:  isInbound,
						IsOutbound: !isInbound,
						Tag:        tag,
					}
					tagTrafficMap[tag] = traffic
				}

				if isDown {
					traffic.Down = value
				} else {
					traffic.Up = value
				}
			} else if matches["type"] == "user" {
				email := matches["email"]
				isDown := matches["direction"] == "downlink"

				clientTraffic, ok := emailTrafficMap[email]
				if !ok {
					clientTraffic = &ClientTraffic{
						Email: email,
					}
					emailTrafficMap[email] = clientTraffic
				}

				if isDown {
					clientTraffic.Down = value
				} else {
					clientTraffic.Up = value
				}
			}
		}
	}

	return mapToSlice(tagTrafficMap), mapToSlice(emailTrafficMap), nil
}

// parseTrafficKey parses sing-box stat key format.
// Returns a map with parsed components or nil if format doesn't match.
func parseTrafficKey(key string) map[string]string {
	// Format: "inbound>>>tag>>>traffic>>>uplink" or "user>>>email>>>traffic>>>uplink"
	parts := bytes.Split([]byte(key), []byte(">>>"))
	if len(parts) != 4 {
		return nil
	}

	result := make(map[string]string)
	result["type"] = string(parts[0])
	
	if string(parts[0]) == "inbound" || string(parts[0]) == "outbound" {
		result["tag"] = string(parts[1])
	} else if string(parts[0]) == "user" {
		result["email"] = string(parts[1])
	}
	
	if string(parts[2]) == "traffic" {
		result["direction"] = string(parts[3])
	}

	return result
}

// mapToSlice converts a map of pointers to a slice of pointers.
func mapToSlice[T any](m map[string]*T) []*T {
	result := make([]*T, 0, len(m))
	for _, v := range m {
		result = append(result, v)
	}
	return result
}

// AddInbound adds a new inbound configuration to the sing-box core via HTTP API.
func (s *SingBoxAPI) AddInbound(inbound []byte) error {
	// sing-box uses HTTP POST to /inbounds endpoint
	url := s.baseURL + "/inbounds"
	resp, err := s.httpClient.Post(url, "application/json", bytes.NewReader(inbound))
	if err != nil {
		return fmt.Errorf("failed to add inbound: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to add inbound: status %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// DelInbound removes an inbound configuration from the sing-box core by tag.
func (s *SingBoxAPI) DelInbound(tag string) error {
	// sing-box uses HTTP DELETE to /inbounds/{tag} endpoint
	url := fmt.Sprintf("%s/inbounds/%s", s.baseURL, tag)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete inbound: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete inbound: status %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// AddUser adds a user to an inbound in the sing-box core.
func (s *SingBoxAPI) AddUser(protocol, inboundTag string, user map[string]interface{}) error {
	// sing-box uses HTTP POST to /inbounds/{tag}/users endpoint
	url := fmt.Sprintf("%s/inbounds/%s/users", s.baseURL, inboundTag)
	
	userJSON, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("failed to marshal user: %w", err)
	}

	resp, err := s.httpClient.Post(url, "application/json", bytes.NewReader(userJSON))
	if err != nil {
		return fmt.Errorf("failed to add user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to add user: status %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// RemoveUser removes a user from an inbound in the sing-box core by email.
func (s *SingBoxAPI) RemoveUser(inboundTag, email string) error {
	// sing-box uses HTTP DELETE to /inbounds/{tag}/users/{email} endpoint
	url := fmt.Sprintf("%s/inbounds/%s/users/%s", s.baseURL, inboundTag, email)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to remove user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to remove user: status %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}
