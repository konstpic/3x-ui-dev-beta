// Package logs provides log pushing functionality for sending logs from node to panel in real-time.
package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v2/logger"
)

// LogPusher sends logs to the panel in real-time.
type LogPusher struct {
	panelURL   string
	apiKey     string
	nodeAddress string // Node's own address for identification
	logBuffer  []string
	bufferMu   sync.Mutex
	client     *http.Client
	enabled    bool
	lastPush   time.Time
	pushTicker *time.Ticker
	stopCh     chan struct{}
}

var (
	pusher     *LogPusher
	pusherOnce sync.Once
	pusherMu   sync.RWMutex
)

// InitLogPusher initializes the log pusher if panel URL and API key are configured.
// nodeAddress is the address of this node (e.g., "http://192.168.0.7:8080") for identification.
func InitLogPusher(nodeAddress string) {
	pusherOnce.Do(func() {
		// Try to get API key from (in order of priority):
		// 1. Environment variable
		// 2. Saved config file
		apiKey := os.Getenv("NODE_API_KEY")
		if apiKey == "" {
			// Try to load from saved config
			cfg := getNodeConfig()
			if cfg != nil && cfg.ApiKey != "" {
				apiKey = cfg.ApiKey
				logger.Debug("Using API key from saved configuration for log pusher")
			}
		}

		if apiKey == "" {
			logger.Debug("Log pusher disabled: no API key found (will be enabled after registration)")
			return
		}

		// Try to get panel URL from environment variable first, then from saved config
		panelURL := os.Getenv("PANEL_URL")
		if panelURL == "" {
			cfg := getNodeConfig()
			if cfg != nil && cfg.PanelURL != "" {
				panelURL = cfg.PanelURL
				logger.Debug("Using panel URL from saved configuration for log pusher")
			}
		}

		pusher = &LogPusher{
			panelURL: panelURL,
			apiKey:   apiKey,
			nodeAddress: nodeAddress,
			logBuffer: make([]string, 0, 10),
			client: &http.Client{
				Timeout: 5 * time.Second,
			},
			enabled:  panelURL != "", // Enable only if panel URL is set
			stopCh:   make(chan struct{}),
		}

		if pusher.enabled {
			// Start periodic push (every 2 seconds or when buffer is full)
			pusher.pushTicker = time.NewTicker(2 * time.Second)
			go pusher.run()
			logger.Debugf("Log pusher initialized: sending logs to %s", panelURL)
		} else {
			logger.Debug("Log pusher initialized but disabled: waiting for panel URL")
		}
	})
}

// nodeConfigData represents the node configuration structure.
type nodeConfigData struct {
	ApiKey      string `json:"apiKey"`
	PanelURL    string `json:"panelUrl"`
	NodeAddress string `json:"nodeAddress"`
}

// getNodeConfig is a helper to get node config without circular dependency.
// It reads the config file directly to avoid importing the config package.
func getNodeConfig() *nodeConfigData {
	configPaths := []string{"bin/node-config.json", "config/node-config.json", "./node-config.json", "/app/bin/node-config.json", "/app/config/node-config.json"}
	
	for _, path := range configPaths {
		if data, err := os.ReadFile(path); err == nil {
			var config nodeConfigData
			if err := json.Unmarshal(data, &config); err == nil {
				return &config
			}
		}
	}
	return nil
}

// SetPanelURL sets the panel URL and enables the log pusher.
// PANEL_URL from environment variable has priority and won't be overwritten.
func SetPanelURL(url string) {
	pusherMu.Lock()
	defer pusherMu.Unlock()

	// Check if PANEL_URL is set in environment - it has priority
	envPanelURL := os.Getenv("PANEL_URL")
	if envPanelURL != "" {
		// Environment variable has priority, ignore URL from config
		if pusher != nil && pusher.panelURL == envPanelURL {
			// Already set from env, don't update
			return
		}
		// Use environment variable instead
		url = envPanelURL
		logger.Debugf("Using PANEL_URL from environment: %s (ignoring config URL)", envPanelURL)
	}

	if pusher == nil {
		// Initialize if not already initialized
		apiKey := os.Getenv("NODE_API_KEY")
		if apiKey == "" {
			// Try to load from saved config
			cfg := getNodeConfig()
			if cfg != nil && cfg.ApiKey != "" {
				apiKey = cfg.ApiKey
			}
		}
		
		if apiKey == "" {
			logger.Debug("Cannot set panel URL: no API key found")
			return
		}

		// Get node address from environment if not provided
		nodeAddress := os.Getenv("NODE_ADDRESS")
		if nodeAddress == "" {
			cfg := getNodeConfig()
			if cfg != nil && cfg.NodeAddress != "" {
				nodeAddress = cfg.NodeAddress
			}
		}
		
		pusher = &LogPusher{
			apiKey:   apiKey,
			nodeAddress: nodeAddress,
			logBuffer: make([]string, 0, 10),
			client: &http.Client{
				Timeout: 5 * time.Second,
			},
			stopCh: make(chan struct{}),
		}
	}

	if url == "" {
		logger.Debug("Panel URL cleared, disabling log pusher")
		pusher.enabled = false
		if pusher.pushTicker != nil {
			pusher.pushTicker.Stop()
			pusher.pushTicker = nil
		}
		return
	}

	wasEnabled := pusher.enabled
	pusher.panelURL = url
	pusher.enabled = true

	if !wasEnabled && pusher.pushTicker == nil {
		// Start periodic push if it wasn't running
		pusher.pushTicker = time.NewTicker(2 * time.Second)
		go pusher.run()
		// Don't log here to avoid recursion - log will be sent via pusher
	} else if wasEnabled && pusher.panelURL != url {
		// Don't log here to avoid recursion
	}
}

// UpdateApiKey updates the API key in the log pusher.
// This is called after node registration to enable log pushing.
// If pusher is not initialized, it will be initialized with the API key.
func UpdateApiKey(apiKey string) {
	pusherMu.Lock()
	defer pusherMu.Unlock()

	if pusher == nil {
		// Initialize pusher if it doesn't exist yet
		// Get node address from environment first (fastest)
		nodeAddress := os.Getenv("NODE_ADDRESS")
		if nodeAddress == "" {
			// Try to get from config, but use default if not found (don't block)
			// Use a quick read without blocking
			cfg := getNodeConfig()
			if cfg != nil && cfg.NodeAddress != "" {
				nodeAddress = cfg.NodeAddress
			}
		}
		if nodeAddress == "" {
			// Default node address (will be updated later)
			nodeAddress = "http://127.0.0.1:8080"
		}

		pusher = &LogPusher{
			apiKey:      apiKey,
			nodeAddress: nodeAddress,
			logBuffer:   make([]string, 0, 10),
			client: &http.Client{
				Timeout: 5 * time.Second,
			},
			stopCh:  make(chan struct{}),
			enabled: false, // Will be enabled when panel URL is set
		}
	} else {
		pusher.apiKey = apiKey
	}
	
	// If pusher is enabled but wasn't running, start it
	// Do this in a goroutine to avoid blocking
	if pusher.enabled && pusher.pushTicker == nil && pusher.panelURL != "" {
		pusher.pushTicker = time.NewTicker(2 * time.Second)
		go pusher.run()
		// Don't log here to avoid recursion - log will be sent via pusher
	}
}

// PushLog adds a log entry to the buffer for sending to panel.
func PushLog(logLine string) {
	pusherMu.RLock()
	pusherLocal := pusher
	pusherMu.RUnlock()
	
	if pusherLocal == nil {
		// Don't log here to avoid infinite loop
		return
	}
	
	if !pusherLocal.enabled {
		// Don't log here to avoid infinite loop
		return
	}

	// Skip logs that already contain node prefix to avoid infinite loop
	// These are logs that came from panel and shouldn't be sent back
	if strings.Contains(logLine, "[Node:") {
		return
	}

	// Skip logs about log pushing itself to avoid infinite loop
	if strings.Contains(logLine, "Logs pushed:") || 
	   strings.Contains(logLine, "Failed to push logs") ||
	   strings.Contains(logLine, "Log pusher") ||
	   strings.Contains(logLine, "Panel URL") {
		return
	}

	pusherLocal.bufferMu.Lock()
	defer pusherLocal.bufferMu.Unlock()

	pusherLocal.logBuffer = append(pusherLocal.logBuffer, logLine)

	// If buffer is getting large, push immediately
	if len(pusherLocal.logBuffer) >= 10 {
		go pusherLocal.push()
	}
}

// run periodically pushes logs to panel.
func (lp *LogPusher) run() {
	for {
		select {
		case <-lp.pushTicker.C:
			lp.bufferMu.Lock()
			if len(lp.logBuffer) > 0 {
				logsToPush := make([]string, len(lp.logBuffer))
				copy(logsToPush, lp.logBuffer)
				lp.logBuffer = lp.logBuffer[:0]
				lp.bufferMu.Unlock()

				go lp.pushLogs(logsToPush)
			} else {
				lp.bufferMu.Unlock()
			}
		case <-lp.stopCh:
			return
		}
	}
}

// push immediately pushes current buffer to panel.
func (lp *LogPusher) push() {
	lp.bufferMu.Lock()
	if len(lp.logBuffer) == 0 {
		lp.bufferMu.Unlock()
		return
	}

	logsToPush := make([]string, len(lp.logBuffer))
	copy(logsToPush, lp.logBuffer)
	lp.logBuffer = lp.logBuffer[:0]
	lp.bufferMu.Unlock()

	lp.pushLogs(logsToPush)
}

// pushLogs sends logs to the panel.
func (lp *LogPusher) pushLogs(logs []string) {
	if len(logs) == 0 {
		return
	}

	// Construct panel URL
	panelEndpoint := lp.panelURL
	if panelEndpoint[len(panelEndpoint)-1] != '/' {
		panelEndpoint += "/"
	}
	panelEndpoint += "panel/api/node/push-logs"

	// Don't log here to avoid recursion - this function is called from logger

	// Prepare request
	reqBody := map[string]interface{}{
		"apiKey": lp.apiKey,
		"logs":   logs,
	}
	// Add node address for identification (in case multiple nodes share the same API key)
	if lp.nodeAddress != "" {
		reqBody["nodeAddress"] = lp.nodeAddress
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		// Don't log here to avoid recursion - use fmt.Printf or os.Stderr
		fmt.Fprintf(os.Stderr, "Failed to marshal log push request: %v\n", err)
		return
	}

	req, err := http.NewRequest("POST", panelEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		// Don't log here to avoid recursion
		fmt.Fprintf(os.Stderr, "Failed to create log push request: %v\n", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")

	// Use context with timeout to avoid blocking forever
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := lp.client.Do(req)
	if err != nil {
		// Don't log here to avoid recursion - errors are expected if panel is unreachable
		// Silently fail - this is non-critical
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		// Don't log here to avoid recursion
		// Silently fail - this is non-critical
		_ = body // Suppress unused variable warning
		return
	}

	lp.lastPush = time.Now()
}

// Stop stops the log pusher.
func Stop() {
	if pusher != nil && pusher.pushTicker != nil {
		pusher.pushTicker.Stop()
		close(pusher.stopCh)
		// Push remaining logs
		pusher.push()
	}
}
