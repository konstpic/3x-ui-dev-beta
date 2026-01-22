// Package singbox provides Sing-box Core management for the node service.
package singbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/singbox"
)

// Manager manages the Sing-box Core process lifecycle.
type Manager struct {
	process *singbox.Process
	lock    sync.Mutex
	config  *singbox.Config
}

// NewManager creates a new Sing-box manager instance.
func NewManager() *Manager {
	m := &Manager{}
	// Download geo files if missing
	m.downloadGeoFiles()
	// Try to load config from file on startup
	m.LoadConfigFromFile()
	return m
}

// downloadGeoFiles downloads geo data files if they are missing.
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

	// List of geo files to download (same as Xray)
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

// LoadConfigFromFile attempts to load Sing-box configuration from config.json file.
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
	var config singbox.Config
	if err := json.Unmarshal(configData, &config); err != nil {
		logger.Warningf("Config file %s contains invalid Sing-box config: %v", configPath, err)
		return fmt.Errorf("invalid Sing-box config: %w", err)
	}

	// Check if config has inbounds
	if len(config.Inbounds) == 0 {
		logger.Debug("Config file found but no inbounds configured, skipping Sing-box start")
		return nil
	}

	// Apply the loaded config (this will start Sing-box)
	logger.Infof("Loading Sing-box configuration from %s", configPath)
	if err := m.ApplyConfig(configData); err != nil {
		logger.Errorf("Failed to apply config from file: %v", err)
		return fmt.Errorf("failed to apply config: %w", err)
	}

	logger.Info("Sing-box started successfully from config file")
	return nil
}

// IsRunning returns true if Sing-box is currently running.
func (m *Manager) IsRunning() bool {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.process != nil && m.process.IsRunning()
}

// GetStatus returns the current status of Sing-box.
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

// ApplyConfig applies a new Sing-box configuration and restarts if needed.
func (m *Manager) ApplyConfig(configJSON []byte) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	var newConfig singbox.Config
	if err := json.Unmarshal(configJSON, &newConfig); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	// If Sing-box is running and config is the same, skip restart
	if m.process != nil && m.process.IsRunning() {
		oldConfig := m.process.GetConfig()
		if oldConfig != nil && oldConfig.Equals(&newConfig) {
			logger.Info("Config unchanged, skipping restart")
			return nil
		}
		// Stop existing process
		if err := m.process.Stop(); err != nil {
			logger.Warningf("Failed to stop existing Sing-box: %v", err)
		}
	}

	// Start new process with new config
	m.config = &newConfig
	m.process = singbox.NewProcess(&newConfig)
	if err := m.process.Start(); err != nil {
		return fmt.Errorf("failed to start Sing-box: %w", err)
	}

	logger.Info("Sing-box configuration applied successfully")
	return nil
}

// Reload reloads Sing-box configuration without full restart (if supported).
func (m *Manager) Reload() error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.process == nil || !m.process.IsRunning() {
		return errors.New("Sing-box is not running")
	}

	// Sing-box doesn't support hot reload, so we need to restart
	if m.config == nil {
		return errors.New("no config to reload")
	}

	// Stop and restart
	if err := m.process.Stop(); err != nil {
		return fmt.Errorf("failed to stop Sing-box: %w", err)
	}

	m.process = singbox.NewProcess(m.config)
	if err := m.process.Start(); err != nil {
		return fmt.Errorf("failed to start Sing-box: %w", err)
	}

	logger.Info("Sing-box reloaded successfully")
	return nil
}

// ForceReload stops and restarts Sing-box with current config.
func (m *Manager) ForceReload() error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.config == nil {
		return errors.New("no config to reload")
	}

	if m.process != nil && m.process.IsRunning() {
		if err := m.process.Stop(); err != nil {
			logger.Warningf("Failed to stop Sing-box: %v", err)
		}
	}

	m.process = singbox.NewProcess(m.config)
	if err := m.process.Start(); err != nil {
		return fmt.Errorf("failed to start Sing-box: %w", err)
	}

	logger.Info("Sing-box force reloaded successfully")
	return nil
}

// Stop stops the Sing-box process.
func (m *Manager) Stop() error {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.process == nil {
		return nil
	}

	if err := m.process.Stop(); err != nil {
		return fmt.Errorf("failed to stop Sing-box: %w", err)
	}

	m.process = nil
	logger.Info("Sing-box stopped successfully")
	return nil
}

// GetTraffic returns traffic statistics from Sing-box.
func (m *Manager) GetTraffic(reset bool) ([]*singbox.Traffic, []*singbox.ClientTraffic, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.process == nil || !m.process.IsRunning() {
		return nil, nil, errors.New("Sing-box is not running")
	}

	// Use API to get traffic
	apiPort := m.process.GetAPIPort()
	if apiPort <= 0 {
		return nil, nil, errors.New("Sing-box API port not configured")
	}

	api := &singbox.SingBoxAPI{}
	if err := api.Init(apiPort); err != nil {
		return nil, nil, fmt.Errorf("failed to initialize API: %w", err)
	}
	defer api.Close()

	return api.GetTraffic(reset)
}

// GetNodeStats returns traffic and online clients statistics.
func (m *Manager) GetNodeStats(reset bool) (*NodeStats, error) {
	traffic, clientTraffic, err := m.GetTraffic(reset)
	if err != nil {
		return nil, err
	}

	// Extract online clients from traffic
	onlineClients := make([]string, 0)
	for _, ct := range clientTraffic {
		if ct.Enable {
			onlineClients = append(onlineClients, ct.Email)
		}
	}

	return &NodeStats{
		Traffic:       traffic,
		ClientTraffic: clientTraffic,
		OnlineClients: onlineClients,
	}, nil
}

// NodeStats represents traffic and online clients statistics from a node.
type NodeStats struct {
	Traffic       []*singbox.Traffic       `json:"traffic"`
	ClientTraffic []*singbox.ClientTraffic `json:"clientTraffic"`
	OnlineClients []string                 `json:"onlineClients"`
}
