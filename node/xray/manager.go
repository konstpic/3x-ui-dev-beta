// Package xray provides XRAY Core management for the node service.
package xray

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/konstpic/sharx-code/v2/config"
	"github.com/konstpic/sharx-code/v2/logger"
	"github.com/konstpic/sharx-code/v2/util/json_util"
	"github.com/konstpic/sharx-code/v2/xray"
)

// NodeStats represents traffic and online clients statistics from a node.
type NodeStats struct {
	Traffic       []*xray.Traffic       `json:"traffic"`
	ClientTraffic []*xray.ClientTraffic `json:"clientTraffic"`
	OnlineClients []string               `json:"onlineClients"`
}

// Manager manages the XRAY Core process lifecycle.
type Manager struct {
	process *xray.Process
	lock    sync.Mutex
	config  *xray.Config
}

// NewManager creates a new XRAY manager instance.
func NewManager() *Manager {
	m := &Manager{}
	// Download geo files if missing
	m.downloadGeoFiles()
	// Try to load config from file on startup
	m.LoadConfigFromFile()
	return m
}

// downloadGeoFiles downloads geo data files if they are missing.
// These files are required for routing rules that use geoip/geosite matching.
func (m *Manager) downloadGeoFiles() {
	// Possible bin folder paths (in order of priority)
	binPaths := []string{
		"bin",
		"/app/bin",
		"./bin",
	}

	var binPath string
	for _, path := range binPaths {
		if _, err := os.Stat(path); err == nil {
			binPath = path
			break
		}
	}

	if binPath == "" {
		logger.Debug("No bin folder found, skipping geo files download")
		return
	}

	// List of geo files to download
	geoFiles := []struct {
		URL      string
		FileName string
	}{
		{"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat", "geoip.dat"},
		{"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat", "geosite.dat"},
		{"https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geoip.dat", "geoip_IR.dat"},
		{"https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geosite.dat", "geosite_IR.dat"},
		{"https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geoip.dat", "geoip_RU.dat"},
		{"https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geosite.dat", "geosite_RU.dat"},
	}

	downloadFile := func(url, destPath string) error {
		resp, err := http.Get(url)
		if err != nil {
			return fmt.Errorf("failed to download: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("bad status: %d", resp.StatusCode)
		}

		file, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}
		defer file.Close()

		_, err = io.Copy(file, resp.Body)
		if err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}

		return nil
	}

	for _, file := range geoFiles {
		destPath := filepath.Join(binPath, file.FileName)
		
		// Check if file already exists
		if _, err := os.Stat(destPath); err == nil {
			logger.Debugf("Geo file %s already exists, skipping download", file.FileName)
			continue
		}

		logger.Infof("Downloading geo file: %s", file.FileName)
		if err := downloadFile(file.URL, destPath); err != nil {
			logger.Warningf("Failed to download %s: %v", file.FileName, err)
		} else {
			logger.Infof("Successfully downloaded %s", file.FileName)
		}
	}
}

// LoadConfigFromFile attempts to load XRAY configuration from config.json file.
// It checks multiple possible locations: bin/config.json, config/config.json, and ./config.json
func (m *Manager) LoadConfigFromFile() error {
	// Possible config file paths (in order of priority)
	configPaths := []string{
		"bin/config.json",
		"config/config.json",
		"./config.json",
		"/app/bin/config.json",
		"/app/config/config.json",
	}

	var configData []byte
	var configPath string

	// Try each path until we find a valid config file
	for _, path := range configPaths {
		if _, statErr := os.Stat(path); statErr == nil {
			var readErr error
			configData, readErr = os.ReadFile(path)
			if readErr == nil {
				configPath = path
				break
			}
		}
	}

	// If no config file found, that's okay - node will wait for config from panel
	if configPath == "" {
		logger.Debug("No config.json found, node will wait for configuration from panel")
		return nil
	}

	// Validate JSON
	var configJSON json.RawMessage
	if err := json.Unmarshal(configData, &configJSON); err != nil {
		logger.Warningf("Config file %s contains invalid JSON: %v", configPath, err)
		return fmt.Errorf("invalid JSON in config file: %w", err)
	}

	// Parse full config
	var config xray.Config
	if err := json.Unmarshal(configData, &config); err != nil {
		logger.Warningf("Failed to parse config from %s: %v", configPath, err)
		return fmt.Errorf("failed to parse config: %w", err)
	}

	// Check if API inbound exists, if not add it
	hasAPIInbound := false
	for _, inbound := range config.InboundConfigs {
		if inbound.Tag == "api" {
			hasAPIInbound = true
			break
		}
	}

	// If no API inbound found, add a default one
	if !hasAPIInbound {
		logger.Debug("No API inbound found in config, adding default API inbound")
		apiInbound := xray.InboundConfig{
			Tag:      "api",
			Port:     62789, // Default API port
			Protocol: "tunnel",
			Listen:   json_util.RawMessage(`"127.0.0.1"`),
			Settings: json_util.RawMessage(`{"address":"127.0.0.1"}`),
		}
		// Add API inbound at the beginning
		config.InboundConfigs = append([]xray.InboundConfig{apiInbound}, config.InboundConfigs...)
		// Update configData with the new inbound
		configData, _ = json.MarshalIndent(&config, "", "  ")
	}

	// Check if config has inbounds (after adding API inbound)
	if len(config.InboundConfigs) == 0 {
		logger.Debug("Config file found but no inbounds configured, skipping XRAY start")
		return nil
	}

	// Apply the loaded config (this will start XRAY)
	logger.Infof("Loading XRAY configuration from %s", configPath)
	if err := m.ApplyConfig(configData); err != nil {
		logger.Errorf("Failed to apply config from file: %v", err)
		return fmt.Errorf("failed to apply config: %w", err)
	}

	logger.Info("XRAY started successfully from config file")
	return nil
}

// IsRunning returns true if XRAY is currently running.
func (m *Manager) IsRunning() bool {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.process != nil && m.process.IsRunning()
}

// GetStatus returns the current status of XRAY.
func (m *Manager) GetStatus() map[string]interface{} {
	m.lock.Lock()
	defer m.lock.Unlock()

	status := map[string]interface{}{
		"running": m.process != nil && m.process.IsRunning(),
		"version": "Unknown",
		"uptime":  0,
	}

	if m.process != nil && m.process.IsRunning() {
		status["version"] = m.process.GetVersion()
		status["uptime"] = m.process.GetUptime()
	}

	return status
}

// ApplyConfig applies a new XRAY configuration and restarts if needed.
func (m *Manager) ApplyConfig(configJSON []byte) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	var newConfig xray.Config
	if err := json.Unmarshal(configJSON, &newConfig); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	// If XRAY is running and config is the same, skip restart
	if m.process != nil && m.process.IsRunning() {
		oldConfig := m.process.GetConfig()
		if oldConfig != nil && oldConfig.Equals(&newConfig) {
			logger.Info("Config unchanged, skipping restart")
			return nil
		}
		// Stop existing process
		if err := m.process.Stop(); err != nil {
			logger.Warningf("Failed to stop existing XRAY: %v", err)
		}
	}

	// Start new process with new config
	m.config = &newConfig
	m.process = xray.NewProcess(&newConfig)
	if err := m.process.Start(); err != nil {
		return fmt.Errorf("failed to start XRAY: %w", err)
	}

	logger.Info("XRAY configuration applied successfully")
	return nil
}

// Reload reloads XRAY configuration without full restart (if supported).
// Falls back to restart if reload is not available.
func (m *Manager) Reload() error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.process == nil || !m.process.IsRunning() {
		return errors.New("XRAY is not running")
	}

	// XRAY doesn't support hot reload, so we need to restart
	// Save current config
	if m.config == nil {
		return errors.New("no config to reload")
	}

	// Stop and restart
	if err := m.process.Stop(); err != nil {
		return fmt.Errorf("failed to stop XRAY: %w", err)
	}

	m.process = xray.NewProcess(m.config)
	if err := m.process.Start(); err != nil {
		return fmt.Errorf("failed to restart XRAY: %w", err)
	}

	logger.Info("XRAY reloaded successfully")
	return nil
}

// ForceReload forcefully reloads XRAY even if it's not running or hung.
// It stops XRAY if running, loads config from file if available, and restarts.
func (m *Manager) ForceReload() error {
	m.lock.Lock()
	defer m.lock.Unlock()

	// Stop XRAY if it's running (even if hung)
	if m.process != nil {
		// Try to stop gracefully, but don't fail if it's hung
		_ = m.process.Stop()
		// Give it a moment to stop
		time.Sleep(500 * time.Millisecond)
		// Force kill if still running
		if m.process.IsRunning() {
			logger.Warning("XRAY process appears hung, forcing stop")
			// Process will be cleaned up by finalizer or on next start
		}
		m.process = nil
	}

	// Try to load config from file first (if available)
	configPaths := []string{
		"bin/config.json",
		"config/config.json",
		"./config.json",
		"/app/bin/config.json",
		"/app/config/config.json",
	}
	
	var configData []byte
	var configPath string
	
	// Find config file
	for _, path := range configPaths {
		if _, statErr := os.Stat(path); statErr == nil {
			var readErr error
			configData, readErr = os.ReadFile(path)
			if readErr == nil {
				configPath = path
				break
			}
		}
	}
	
	// If config file found, try to use it
	if configPath != "" {
		var config xray.Config
		if err := json.Unmarshal(configData, &config); err == nil {
			// Check if config has inbounds
			if len(config.InboundConfigs) > 0 {
				// Check if API inbound exists
				hasAPIInbound := false
				for _, inbound := range config.InboundConfigs {
					if inbound.Tag == "api" {
						hasAPIInbound = true
						break
					}
				}
				
				// Add API inbound if missing
				if !hasAPIInbound {
					apiInbound := xray.InboundConfig{
						Tag:      "api",
						Port:     62789,
						Protocol: "tunnel",
						Listen:   json_util.RawMessage(`"127.0.0.1"`),
						Settings: json_util.RawMessage(`{"address":"127.0.0.1"}`),
					}
					config.InboundConfigs = append([]xray.InboundConfig{apiInbound}, config.InboundConfigs...)
					configData, _ = json.MarshalIndent(&config, "", "  ")
				}
				
				// Apply config from file
				m.config = &config
				m.process = xray.NewProcess(&config)
				if err := m.process.Start(); err == nil {
					logger.Infof("XRAY force reloaded successfully from config file %s", configPath)
					return nil
				}
			}
		}
		// If loading from file failed, continue with saved config
	}

	// If no config file, try to use saved config
	if m.config == nil {
		return errors.New("no config available to reload")
	}

	// Restart with saved config
	m.process = xray.NewProcess(m.config)
	if err := m.process.Start(); err != nil {
		return fmt.Errorf("failed to restart XRAY: %w", err)
	}

	logger.Info("XRAY force reloaded successfully")
	return nil
}

// Stop stops the XRAY process.
func (m *Manager) Stop() error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.process == nil || !m.process.IsRunning() {
		return nil
	}

	return m.process.Stop()
}

// GetStats returns traffic and online clients statistics from XRAY.
func (m *Manager) GetStats(reset bool) (*NodeStats, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.process == nil || !m.process.IsRunning() {
		return nil, errors.New("XRAY is not running")
	}

	// Get API port from process
	apiPort := m.process.GetAPIPort()
	if apiPort == 0 {
		return nil, errors.New("XRAY API port is not available")
	}

	// Create XrayAPI instance and initialize
	xrayAPI := &xray.XrayAPI{}
	if err := xrayAPI.Init(apiPort); err != nil {
		return nil, fmt.Errorf("failed to initialize XrayAPI: %w", err)
	}
	defer xrayAPI.Close()

	// Get traffic statistics
	traffics, clientTraffics, err := xrayAPI.GetTraffic(reset)
	if err != nil {
		return nil, fmt.Errorf("failed to get traffic: %w", err)
	}

	// Get online clients from process
	onlineClients := m.process.GetOnlineClients()

	// Also check online clients from traffic (clients with traffic > 0)
	onlineFromTraffic := make(map[string]bool)
	for _, ct := range clientTraffics {
		if ct.Up+ct.Down > 0 {
			onlineFromTraffic[ct.Email] = true
		}
	}

	// Merge online clients
	onlineSet := make(map[string]bool)
	for _, email := range onlineClients {
		onlineSet[email] = true
	}
	for email := range onlineFromTraffic {
		onlineSet[email] = true
	}

	onlineList := make([]string, 0, len(onlineSet))
	for email := range onlineSet {
		onlineList = append(onlineList, email)
	}

	return &NodeStats{
		Traffic:       traffics,
		ClientTraffic: clientTraffics,
		OnlineClients: onlineList,
	}, nil
}

// GetLogs returns XRAY access logs from the log file.
// Returns raw log lines as strings.
func (m *Manager) GetLogs(count int, filter string) ([]string, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.process == nil || !m.process.IsRunning() {
		return nil, errors.New("XRAY is not running")
	}

	// Get access log path from current config
	var pathToAccessLog string
	if m.config != nil && len(m.config.LogConfig) > 0 {
		var logConfig map[string]interface{}
		if err := json.Unmarshal(m.config.LogConfig, &logConfig); err == nil {
			if access, ok := logConfig["access"].(string); ok {
				pathToAccessLog = access
			}
		}
	}

	// Fallback to reading from file if not in config
	if pathToAccessLog == "" {
		var err error
		pathToAccessLog, err = xray.GetAccessLogPath()
		if err != nil {
			return nil, fmt.Errorf("failed to get access log path: %w", err)
		}
	}

	if pathToAccessLog == "none" || pathToAccessLog == "" {
		return []string{}, nil // No logs configured
	}

	file, err := os.Open(pathToAccessLog)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.Contains(line, "api -> api") {
			continue // Skip empty lines and API calls
		}

		if filter != "" && !strings.Contains(line, filter) {
			continue // Apply filter if provided
		}

		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read log file: %w", err)
	}

	// Return last 'count' lines
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}

	return lines, nil
}

// GetProcess returns the Xray process (for internal use by API server).
func (m *Manager) GetProcess() *xray.Process {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.process
}

// AddUser adds a user to an inbound via Xray API (instant, no restart).
func (m *Manager) AddUser(protocol, inboundTag string, user map[string]interface{}) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.process == nil || !m.process.IsRunning() {
		return errors.New("XRAY is not running")
	}

	apiPort := m.process.GetAPIPort()
	if apiPort == 0 {
		return errors.New("XRAY API port is not available")
	}

	// Initialize XrayAPI
	xrayAPI := &xray.XrayAPI{}
	if err := xrayAPI.Init(apiPort); err != nil {
		return fmt.Errorf("failed to init XrayAPI: %w", err)
	}
	defer xrayAPI.Close()

	// Add user via Xray API (instant, no restart needed)
	if err := xrayAPI.AddUser(protocol, inboundTag, user); err != nil {
		return fmt.Errorf("failed to add user: %w", err)
	}

	// Update config file and restart Xray asynchronously in background to sync config
	// This ensures config file is fully synchronized without blocking the response
	go func() {
		// Update config file first
		if err := m.updateConfigFileAfterUserAddition(inboundTag, user); err != nil {
			logger.Warningf("Failed to update config file after adding user: %v", err)
		}
		// Then reload to apply updated config
		if err := m.Reload(); err != nil {
			logger.Warningf("Failed to reload Xray after adding user: %v", err)
		} else {
			logger.Debugf("Xray reloaded successfully after adding user (config synced)")
		}
	}()

	return nil
}

// RemoveUser removes a user from an inbound via Xray API (instant, no restart).
func (m *Manager) RemoveUser(inboundTag, email string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.process == nil || !m.process.IsRunning() {
		return errors.New("XRAY is not running")
	}

	apiPort := m.process.GetAPIPort()
	if apiPort == 0 {
		return errors.New("XRAY API port is not available")
	}

	// Initialize XrayAPI
	xrayAPI := &xray.XrayAPI{}
	if err := xrayAPI.Init(apiPort); err != nil {
		return fmt.Errorf("failed to init XrayAPI: %w", err)
	}
	defer xrayAPI.Close()

	// Remove user via Xray API (instant, no restart needed)
	if err := xrayAPI.RemoveUser(inboundTag, email); err != nil {
		// Check if user not found (this is OK - might already be removed)
		if strings.Contains(err.Error(), "not found") {
			return nil // User already removed, consider it success
		}
		return fmt.Errorf("failed to remove user: %w", err)
	}

	// Update config file and restart Xray asynchronously in background to sync config
	// This ensures config file is fully synchronized without blocking the response
	go func() {
		// Update config file first
		if err := m.updateConfigFileAfterUserRemoval(inboundTag, email); err != nil {
			logger.Warningf("Failed to update config file after removing user %s: %v", email, err)
		}
		// Then reload to apply updated config
		if err := m.Reload(); err != nil {
			logger.Warningf("Failed to reload Xray after removing user %s: %v", email, err)
		} else {
			logger.Debugf("Xray reloaded successfully after removing user %s (config synced)", email)
		}
	}()

	return nil
}

// UpdateInbound updates an inbound configuration via Xray API (instant, no restart).
// This is faster than full config reload - it uses DelInbound + AddInbound.
func (m *Manager) UpdateInbound(inboundConfig []byte) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.process == nil || !m.process.IsRunning() {
		return errors.New("XRAY is not running")
	}

	apiPort := m.process.GetAPIPort()
	if apiPort == 0 {
		return errors.New("XRAY API port is not available")
	}

	// Parse inbound config to get tag
	var inboundJSON map[string]interface{}
	if err := json.Unmarshal(inboundConfig, &inboundJSON); err != nil {
		return fmt.Errorf("failed to parse inbound config: %w", err)
	}

	tag, ok := inboundJSON["tag"].(string)
	if !ok || tag == "" {
		return errors.New("inbound tag is required")
	}

	// Initialize XrayAPI
	xrayAPI := &xray.XrayAPI{}
	if err := xrayAPI.Init(apiPort); err != nil {
		return fmt.Errorf("failed to init XrayAPI: %w", err)
	}
	defer xrayAPI.Close()

	// Remove old inbound first
	if err := xrayAPI.DelInbound(tag); err != nil {
		// Log but continue - inbound might not exist yet
		logger.Debugf("Failed to delete old inbound %s (may not exist): %v", tag, err)
	}

	// Add updated inbound
	if err := xrayAPI.AddInbound(inboundConfig); err != nil {
		return fmt.Errorf("failed to add updated inbound: %w", err)
	}

	logger.Infof("Inbound %s updated successfully via API (instant)", tag)
	return nil
}

// DelInbound removes an inbound configuration via Xray API (instant, no restart).
func (m *Manager) DelInbound(tag string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.process == nil || !m.process.IsRunning() {
		return errors.New("XRAY is not running")
	}

	apiPort := m.process.GetAPIPort()
	if apiPort == 0 {
		return errors.New("XRAY API port is not available")
	}

	// Initialize XrayAPI
	xrayAPI := &xray.XrayAPI{}
	if err := xrayAPI.Init(apiPort); err != nil {
		return fmt.Errorf("failed to init XrayAPI: %w", err)
	}
	defer xrayAPI.Close()

	// Remove inbound via Xray API (instant, no restart needed)
	if err := xrayAPI.DelInbound(tag); err != nil {
		// Check if inbound not found (this is OK - might already be removed)
		if strings.Contains(err.Error(), "not found") {
			logger.Debugf("Inbound %s already removed or not found - this is OK", tag)
			return nil // Already removed, consider it success
		}
		return fmt.Errorf("failed to remove inbound: %w", err)
	}

	logger.Infof("Inbound %s removed successfully via API (instant)", tag)
	return nil
}

// updateConfigFileAfterUserRemoval updates the config file after removing a user via API.
// This ensures the config file stays in sync with the running Xray instance.
func (m *Manager) updateConfigFileAfterUserRemoval(inboundTag, email string) error {
	if m.config == nil {
		return errors.New("no config available to update")
	}

	// Find the inbound by tag
	for i := range m.config.InboundConfigs {
		if m.config.InboundConfigs[i].Tag == inboundTag {
			// Parse settings JSON
			var settings map[string]interface{}
			if err := json.Unmarshal(m.config.InboundConfigs[i].Settings, &settings); err != nil {
				return fmt.Errorf("failed to parse settings: %w", err)
			}

			// Get clients array
			clients, ok := settings["clients"].([]interface{})
			if !ok {
				// Try to handle case where clients might be a different type
				if clientsRaw, ok := settings["clients"]; ok {
					if clientsArray, ok := clientsRaw.([]interface{}); ok {
						clients = clientsArray
					} else {
						return fmt.Errorf("clients is not an array")
					}
				} else {
					return nil // No clients to remove
				}
			}

			// Remove user by email
			found := false
			newClients := make([]interface{}, 0, len(clients))
			for _, client := range clients {
				clientMap, ok := client.(map[string]interface{})
				if !ok {
					continue
				}
				clientEmail, ok := clientMap["email"].(string)
				if !ok {
					continue
				}
				if strings.EqualFold(clientEmail, email) {
					found = true
					continue // Skip this client
				}
				newClients = append(newClients, client)
			}

			if !found {
				logger.Debugf("User %s not found in config file for inbound %s (may have been already removed)", email, inboundTag)
				return nil // User not in config, that's OK
			}

			// Update settings with new clients array
			settings["clients"] = newClients
			updatedSettings, err := json.Marshal(settings)
			if err != nil {
				return fmt.Errorf("failed to marshal updated settings: %w", err)
			}

			m.config.InboundConfigs[i].Settings = updatedSettings

			// Save config to file
			return m.saveConfigToFile()
		}
	}

	logger.Debugf("Inbound %s not found in config", inboundTag)
	return nil // Inbound not in config, that's OK
}

// updateConfigFileAfterUserAddition updates the config file after adding a user via API.
// This ensures the config file stays in sync with the running Xray instance.
func (m *Manager) updateConfigFileAfterUserAddition(inboundTag string, user map[string]interface{}) error {
	if m.config == nil {
		return errors.New("no config available to update")
	}

	userEmail, ok := user["email"].(string)
	if !ok {
		return errors.New("user email not found")
	}

	// Find the inbound by tag
	for i := range m.config.InboundConfigs {
		if m.config.InboundConfigs[i].Tag == inboundTag {
			// Parse settings JSON
			var settings map[string]interface{}
			if err := json.Unmarshal(m.config.InboundConfigs[i].Settings, &settings); err != nil {
				return fmt.Errorf("failed to parse settings: %w", err)
			}

			// Get clients array
			clients, ok := settings["clients"].([]interface{})
			if !ok {
				// Initialize clients array if it doesn't exist
				clients = make([]interface{}, 0)
			}

			// Check if user already exists
			for _, client := range clients {
				clientMap, ok := client.(map[string]interface{})
				if !ok {
					continue
				}
				clientEmail, ok := clientMap["email"].(string)
				if !ok {
					continue
				}
				if strings.EqualFold(clientEmail, userEmail) {
					logger.Debugf("User %s already exists in config file for inbound %s", userEmail, inboundTag)
					return nil // User already in config, that's OK
				}
			}

			// Add user to clients array
			clients = append(clients, user)

			// Update settings with new clients array
			settings["clients"] = clients
			updatedSettings, err := json.Marshal(settings)
			if err != nil {
				return fmt.Errorf("failed to marshal updated settings: %w", err)
			}

			m.config.InboundConfigs[i].Settings = updatedSettings

			// Save config to file
			return m.saveConfigToFile()
		}
	}

	logger.Debugf("Inbound %s not found in config", inboundTag)
	return nil // Inbound not in config, that's OK
}

// saveConfigToFile saves the current config to the config file.
func (m *Manager) saveConfigToFile() error {
	if m.config == nil {
		return errors.New("no config to save")
	}

	// Marshal config to JSON
	configJSON, err := json.MarshalIndent(m.config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Try to find config file path
	configPaths := []string{
		"bin/config.json",
		"config/config.json",
		"./config.json",
		"/app/bin/config.json",
		"/app/config/config.json",
	}

	var configPath string
	for _, path := range configPaths {
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
			configPath = path
			break
		}
	}

	if configPath == "" {
		// No existing config file found, try to use default path
		configPath = "bin/config.json"
		// Create directory if it doesn't exist
		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}
	}

	// Write config to file
	if err := os.WriteFile(configPath, configJSON, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	logger.Debugf("Config file updated: %s", configPath)
	return nil
}

// InstallXrayVersion downloads and installs a specific version of Xray.
func (m *Manager) InstallXrayVersion(version string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	// 1. Stop xray before doing anything
	if m.process != nil && m.process.IsRunning() {
		if err := m.process.Stop(); err != nil {
			logger.Warningf("Failed to stop XRAY before update: %v", err)
		}
		// Wait a bit for process to stop
		time.Sleep(500 * time.Millisecond)
	}

	// 2. Download the zip
	zipFileName, err := m.downloadXRay(version)
	if err != nil {
		return fmt.Errorf("failed to download Xray: %w", err)
	}
	defer os.Remove(zipFileName)

	// 3. Extract the binary
	zipFile, err := os.Open(zipFileName)
	if err != nil {
		return fmt.Errorf("failed to open zip file: %w", err)
	}
	defer zipFile.Close()

	stat, err := zipFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat zip file: %w", err)
	}
	reader, err := zip.NewReader(zipFile, stat.Size())
	if err != nil {
		return fmt.Errorf("failed to create zip reader: %w", err)
	}

	// Helper to extract files
	copyZipFile := func(zipName string, fileName string) error {
		zipFile, err := reader.Open(zipName)
		if err != nil {
			return err
		}
		defer zipFile.Close()
		os.MkdirAll(filepath.Dir(fileName), 0755)
		os.Remove(fileName)
		file, err := os.OpenFile(fileName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, fs.ModePerm)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(file, zipFile)
		return err
	}

	// 4. Extract correct binary
	binPath := config.GetBinFolderPath()
	if binPath == "" {
		binPath = "bin"
	}
	
	var targetBinary string
	if runtime.GOOS == "windows" {
		targetBinary = filepath.Join(binPath, "xray-windows-amd64.exe")
		err = copyZipFile("xray.exe", targetBinary)
	} else {
		targetBinary = xray.GetBinaryPath()
		err = copyZipFile("xray", targetBinary)
	}
	if err != nil {
		return fmt.Errorf("failed to extract binary: %w", err)
	}

	logger.Infof("Xray version %s installed successfully to %s", version, targetBinary)

	// 5. Make binary executable (important for Linux/Unix)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(targetBinary, 0755); err != nil {
			logger.Warningf("Failed to set executable permissions on %s: %v", targetBinary, err)
		}
	}

	// 6. Restart xray if config exists (whether it was running or not)
	// If it was running, restart it. If it wasn't running but config exists, start it.
	if m.config != nil {
		wasRunning := m.process != nil && m.process.IsRunning()
		m.process = xray.NewProcess(m.config)
		if err := m.process.Start(); err != nil {
			logger.Warningf("Failed to start XRAY after update: %v", err)
			// Don't return error - installation was successful, just start failed
		} else {
			if wasRunning {
				logger.Infof("XRAY restarted successfully after version update")
			} else {
				logger.Infof("XRAY started successfully with new version")
			}
		}
	} else {
		logger.Info("No config available, XRAY will start when config is applied")
	}

	return nil
}

// downloadXRay downloads the Xray binary zip file for the specified version.
func (m *Manager) downloadXRay(version string) (string, error) {
	osName := runtime.GOOS
	arch := runtime.GOARCH

	switch osName {
	case "darwin":
		osName = "macos"
	case "windows":
		osName = "windows"
	}

	switch arch {
	case "amd64":
		arch = "64"
	case "arm64":
		arch = "arm64-v8a"
	case "armv7":
		arch = "arm32-v7a"
	case "armv6":
		arch = "arm32-v6"
	case "armv5":
		arch = "arm32-v5"
	case "386":
		arch = "32"
	case "s390x":
		arch = "s390x"
	}

	fileName := fmt.Sprintf("Xray-%s-%s.zip", osName, arch)
	
	// Ensure version has 'v' prefix for GitHub releases
	versionTag := version
	if !strings.HasPrefix(versionTag, "v") {
		versionTag = "v" + versionTag
	}
	
	url := fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/%s/%s", versionTag, fileName)
	
	logger.Infof("Downloading Xray %s from %s", versionTag, url)
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	os.Remove(fileName)
	file, err := os.Create(fileName)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return fileName, nil
}