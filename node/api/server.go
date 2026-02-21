// Package api provides REST API endpoints for the node service.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v2/logger"
	nodeConfig "github.com/mhsanaei/3x-ui/v2/node/config"
	nodeLogs "github.com/mhsanaei/3x-ui/v2/node/logs"
	"github.com/mhsanaei/3x-ui/v2/node/xray"
	"github.com/gin-gonic/gin"
)

// try executes a function and recovers from panics, logging them as warnings
func try(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			logger.Warningf("Non-critical operation failed (recovered): %v", r)
		}
	}()
	fn()
}

// Server provides REST API for managing the node.
type Server struct {
	port       int
	apiKey     string
	xrayManager *xray.Manager
	httpServer *http.Server
	certFile   string
	keyFile    string
}

// NewServer creates a new API server instance.
func NewServer(port int, apiKey string, xrayManager *xray.Manager) *Server {
	return &Server{
		port:        port,
		apiKey:      apiKey,
		xrayManager: xrayManager,
	}
}

// SetTLS sets TLS certificate files for HTTPS.
func (s *Server) SetTLS(certFile, keyFile string) {
	s.certFile = certFile
	s.keyFile = keyFile
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	
	// Add request logging middleware
	router.Use(func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method
		
		c.Next()
		
		latency := time.Since(start)
		status := c.Writer.Status()
		logger.Debugf("%s %s - %d - %v", method, path, status, latency)
	})
	
	router.Use(s.authMiddleware())

	// Health check endpoint (no auth required)
	router.GET("/health", s.health)

	// Registration endpoint (no auth required, used for initial setup)
	router.POST("/api/v1/register", s.register)

	// API endpoints (require auth)
	api := router.Group("/api/v1")
	{
		api.POST("/apply-config", s.applyConfig)
		api.POST("/reload", s.reload)
		api.POST("/force-reload", s.forceReload)
		api.POST("/install-xray/:version", s.installXray)
		api.GET("/status", s.status)
		api.GET("/stats", s.stats)
		api.GET("/logs", s.getLogs)
		api.GET("/service-logs", s.getServiceLogs)
		api.POST("/add-user", s.addUser)
		api.POST("/remove-user", s.removeUser)
		api.POST("/update-inbound", s.updateInbound)
		api.POST("/remove-inbound", s.removeInbound)
	}

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      router,
		ReadTimeout:  30 * time.Second,  // Increased for large configs
		WriteTimeout: 30 * time.Second,  // Increased for large responses
		IdleTimeout:  120 * time.Second, // Keep connections alive longer
	}

	if s.certFile != "" && s.keyFile != "" {
		logger.Infof("API server listening on port %d with HTTPS (cert: %s, key: %s)", s.port, s.certFile, s.keyFile)
		return s.httpServer.ListenAndServeTLS(s.certFile, s.keyFile)
	}
	
	logger.Infof("API server listening on port %d", s.port)
	return s.httpServer.ListenAndServe()
}

// Stop stops the HTTP server.
func (s *Server) Stop() error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Close()
}

// authMiddleware validates API key from Authorization header.
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip auth for health and registration endpoints
		if c.Request.URL.Path == "/health" || c.Request.URL.Path == "/api/v1/register" {
			c.Next()
			return
		}

		// Log incoming request for debugging
		logger.Debugf("Incoming request: %s %s from %s", c.Request.Method, c.Request.URL.Path, c.ClientIP())

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			logger.Warningf("Request to %s rejected: missing Authorization header", c.Request.URL.Path)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing Authorization header"})
			c.Abort()
			return
		}

		// Support both "Bearer <key>" and direct key
		apiKey := authHeader
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			apiKey = authHeader[7:]
		}

		if apiKey != s.apiKey {
			logger.Warningf("Request to %s rejected: invalid API key (received: %s..., expected: %s...)", 
				c.Request.URL.Path, apiKey[:min(8, len(apiKey))], s.apiKey[:min(8, len(s.apiKey))])
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
			c.Abort()
			return
		}

		logger.Debugf("Request to %s authenticated successfully", c.Request.URL.Path)
		c.Next()
	}
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// health returns the health status of the node.
func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"service": "3x-ui-node",
	})
}

// applyConfig applies a new XRAY configuration.
func (s *Server) applyConfig(c *gin.Context) {
	logger.Infof("Apply config request received")
	
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.Errorf("Failed to read request body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
		return
	}

	logger.Infof("Request body read, size: %d bytes", len(body))

	// Try to parse as JSON with optional panelUrl field
	var requestData struct {
		Config   json.RawMessage `json:"config"`
		PanelURL string          `json:"panelUrl,omitempty"`
	}

	// First try to parse as new format with panelUrl
	if err := json.Unmarshal(body, &requestData); err == nil && requestData.PanelURL != "" {
		// New format: { "config": {...}, "panelUrl": "http://..." }
		logger.Infof("Parsed request with panelUrl: %s", requestData.PanelURL)
		body = requestData.Config
		// Set panel URL for log pusher in background to avoid blocking
		go try(func() {
			nodeLogs.SetPanelURL(requestData.PanelURL)
			logger.Infof("Panel URL updated in log pusher: %s", requestData.PanelURL)
		})
	} else {
		// Old format: just JSON config, validate it
		logger.Infof("Parsing as old format (no panelUrl)")
		var configJSON json.RawMessage
		if err := json.Unmarshal(body, &configJSON); err != nil {
			logger.Errorf("Invalid JSON: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
			return
		}
	}

	logger.Infof("Applying XRAY configuration...")
	if err := s.xrayManager.ApplyConfig(body); err != nil {
		logger.Errorf("Failed to apply config: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Infof("Configuration applied successfully, sending response")
	c.JSON(http.StatusOK, gin.H{"message": "Configuration applied successfully"})
	logger.Infof("Apply config response sent")
}

// reload reloads XRAY configuration.
func (s *Server) reload(c *gin.Context) {
	if err := s.xrayManager.Reload(); err != nil {
		logger.Errorf("Failed to reload: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "XRAY reloaded successfully"})
}

// forceReload forcefully reloads XRAY even if it's hung or not running.
func (s *Server) forceReload(c *gin.Context) {
	if err := s.xrayManager.ForceReload(); err != nil {
		logger.Errorf("Failed to force reload: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "XRAY force reloaded successfully"})
}

// status returns the current status of XRAY.
func (s *Server) status(c *gin.Context) {
	status := s.xrayManager.GetStatus()
	c.JSON(http.StatusOK, status)
}

// stats returns traffic and online clients statistics from XRAY.
func (s *Server) stats(c *gin.Context) {
	logger.Debugf("Stats request received")
	
	// Get reset parameter (default: false)
	reset := c.DefaultQuery("reset", "false") == "true"
	logger.Debugf("Getting stats (reset=%v)", reset)

	stats, err := s.xrayManager.GetStats(reset)
	if err != nil {
		logger.Errorf("Failed to get stats: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Debugf("Stats retrieved successfully, sending response")
	c.JSON(http.StatusOK, stats)
	logger.Debugf("Stats response sent")
}

// getLogs returns XRAY access logs from the node.
func (s *Server) getLogs(c *gin.Context) {
	// Get query parameters
	countStr := c.DefaultQuery("count", "100")
	filter := c.DefaultQuery("filter", "")

	count, err := strconv.Atoi(countStr)
	if err != nil || count < 1 || count > 10000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid count parameter (must be 1-10000)"})
		return
	}

	logs, err := s.xrayManager.GetLogs(count, filter)
	if err != nil {
		logger.Errorf("Failed to get logs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

// getServiceLogs returns service application logs from the node (node service logs and XRAY core logs).
func (s *Server) getServiceLogs(c *gin.Context) {
	// Get query parameters
	countStr := c.DefaultQuery("count", "100")
	level := c.DefaultQuery("level", "debug")

	count, err := strconv.Atoi(countStr)
	if err != nil || count < 1 || count > 10000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid count parameter (must be 1-10000)"})
		return
	}

	// Get logs from logger buffer
	logs := logger.GetLogs(count, level)
	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

// register handles node registration from the panel.
// This endpoint receives an API key from the panel and saves it persistently.
// No authentication required - this is the initial setup step.
func (s *Server) register(c *gin.Context) {
	type RegisterRequest struct {
		ApiKey      string `json:"apiKey" binding:"required"`      // API key generated by panel
		PanelURL    string `json:"panelUrl,omitempty"`              // Panel URL (optional)
		NodeAddress string `json:"nodeAddress,omitempty"`          // Node address (optional)
	}

	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Errorf("Registration failed: invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	logger.Infof("Registration request received: API key length=%d, PanelURL=%s, NodeAddress=%s", 
		len(req.ApiKey), req.PanelURL, req.NodeAddress)

	// Check if node is already registered
	logger.Infof("Checking if node is already registered...")
	existingConfig := nodeConfig.GetConfig()
	logger.Infof("Existing config check complete. API key present: %v", existingConfig.ApiKey != "")
	if existingConfig.ApiKey != "" {
		logger.Warningf("Node is already registered. Rejecting registration attempt to prevent overwriting existing API key")
		c.JSON(http.StatusConflict, gin.H{
			"error": "Node is already registered. API key cannot be overwritten",
			"message": "This node has already been registered. If you need to re-register, please remove the node-config.json file first",
		})
		return
	}

	// Save API key to config file (only if not already registered)
	logger.Infof("Saving API key to config file...")
	if err := nodeConfig.SetApiKey(req.ApiKey, false); err != nil {
		logger.Errorf("Failed to save API key: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save API key: " + err.Error()})
		return
	}
	logger.Infof("API key saved to config file")

	// Update API key in server (for immediate use)
	s.apiKey = req.ApiKey
	logger.Infof("API key updated in server")

	// Save panel URL to config if provided
	if req.PanelURL != "" {
		logger.Infof("Saving panel URL to config: %s", req.PanelURL)
		if err := nodeConfig.SetPanelURL(req.PanelURL); err != nil {
			logger.Warningf("Failed to save panel URL: %v", err)
		} else {
			logger.Infof("Panel URL saved to config file")
		}
	}

	// Save node address if provided
	if req.NodeAddress != "" {
		logger.Infof("Saving node address to config: %s", req.NodeAddress)
		if err := nodeConfig.SetNodeAddress(req.NodeAddress); err != nil {
			logger.Warningf("Failed to save node address: %v", err)
		} else {
			logger.Infof("Node address saved to config file")
		}
	}

	logger.Infof("All registration steps completed, preparing response...")
	logger.Infof("Node registered successfully with API key (length: %d)", len(req.ApiKey))
	
	// Send response immediately (before initializing pusher to avoid any blocking)
	response := gin.H{
		"message": "Node registered successfully",
		"apiKey":  req.ApiKey, // Return API key for confirmation
	}
	logger.Infof("Sending registration response: %+v", response)
	
	// Use c.JSON to send response
	c.JSON(http.StatusOK, response)
	
	// Flush response to ensure it's sent
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	
	logger.Infof("Registration response sent successfully")
	
	// Initialize/update log pusher with API key and panel URL AFTER sending response
	// This ensures response is sent immediately without any blocking
	logger.Infof("Starting log pusher initialization in background...")
	go try(func() {
		// First, ensure API key is set in pusher (this initializes pusher if needed)
		nodeLogs.UpdateApiKey(req.ApiKey)
		logger.Infof("Log pusher API key updated")
		
		if req.PanelURL != "" {
			// Set panel URL (pusher should be initialized now with API key)
			nodeLogs.SetPanelURL(req.PanelURL)
			logger.Infof("Log pusher enabled: sending logs to %s", req.PanelURL)
		} else {
			logger.Infof("Log pusher API key set (panel URL will be set when config is applied)")
		}
	})
	logger.Infof("Log pusher initialization started (non-blocking)")
}

// installXray installs or updates Xray to the specified version.
func (s *Server) installXray(c *gin.Context) {
	version := c.Param("version")
	if version == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Version parameter is required"})
		return
	}

	// Remove 'v' prefix if present
	if strings.HasPrefix(version, "v") {
		version = version[1:]
	}

	logger.Infof("Installing Xray version %s", version)
	if err := s.xrayManager.InstallXrayVersion(version); err != nil {
		logger.Errorf("Failed to install Xray version %s: %v", version, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Xray version %s installed successfully", version),
		"version": version,
	})
}

// addUser adds a user to an inbound via Xray API (instant, no restart).
func (s *Server) addUser(c *gin.Context) {
	var req struct {
		Protocol   string                 `json:"protocol" binding:"required"`
		InboundTag string                 `json:"inboundTag" binding:"required"`
		User       map[string]interface{} `json:"user" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	email, _ := req.User["email"].(string)
	logger.Infof("Add user request received: email=%s, protocol=%s, inboundTag=%s", email, req.Protocol, req.InboundTag)

	if !s.xrayManager.IsRunning() {
		logger.Warningf("Cannot add user %s: XRAY is not running", email)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "XRAY is not running"})
		return
	}

	// Add user via Xray API (instant, no restart needed)
	if err := s.xrayManager.AddUser(req.Protocol, req.InboundTag, req.User); err != nil {
		// Check if user already exists (this is OK - user is already in Xray)
		if strings.Contains(err.Error(), "already exists") {
			logger.Infof("User %s already exists in Xray (protocol=%s, inboundTag=%s) - this is OK", email, req.Protocol, req.InboundTag)
			c.JSON(http.StatusOK, gin.H{"message": "User already exists"})
			return
		}
		logger.Errorf("Failed to add user %s via API: %v", email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Infof("User added successfully via API: %s (protocol=%s, inboundTag=%s)", email, req.Protocol, req.InboundTag)
	c.JSON(http.StatusOK, gin.H{"message": "User added successfully"})
}

// removeUser removes a user from an inbound via Xray API (instant, no restart).
func (s *Server) removeUser(c *gin.Context) {
	var req struct {
		InboundTag string `json:"inboundTag" binding:"required"`
		Email      string `json:"email" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	logger.Infof("Remove user request received: email=%s, inboundTag=%s", req.Email, req.InboundTag)

	if !s.xrayManager.IsRunning() {
		logger.Warningf("Cannot remove user %s: XRAY is not running", req.Email)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "XRAY is not running"})
		return
	}

	// Remove user via Xray API (instant, no restart needed)
	if err := s.xrayManager.RemoveUser(req.InboundTag, req.Email); err != nil {
		// Check if user not found (this is OK - might already be removed)
		if strings.Contains(err.Error(), "not found") {
			logger.Infof("User %s already removed or not found in inbound %s", req.Email, req.InboundTag)
			c.JSON(http.StatusOK, gin.H{"message": "User already removed or not found"})
			return
		}
		logger.Errorf("Failed to remove user %s via API: %v", req.Email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Infof("User removed successfully via API: %s from inbound %s", req.Email, req.InboundTag)
	c.JSON(http.StatusOK, gin.H{"message": "User removed successfully"})
}

// removeInbound removes an inbound configuration via Xray API (instant, no restart).
func (s *Server) removeInbound(c *gin.Context) {
	var req struct {
		Tag string `json:"tag" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warningf("Remove inbound request failed: invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	logger.Infof("Remove inbound request received: tag=%s", req.Tag)

	if !s.xrayManager.IsRunning() {
		logger.Warningf("Cannot remove inbound %s: XRAY is not running", req.Tag)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "XRAY is not running"})
		return
	}

	// Remove inbound via Xray API (instant, no restart needed)
	if err := s.xrayManager.DelInbound(req.Tag); err != nil {
		logger.Errorf("Failed to remove inbound %s via API: %v", req.Tag, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Infof("Inbound removed successfully via API: %s", req.Tag)
	c.JSON(http.StatusOK, gin.H{"message": "Inbound removed successfully"})
}

// updateInbound updates an inbound configuration via Xray API (instant, no restart).
func (s *Server) updateInbound(c *gin.Context) {
	var req struct {
		InboundConfig json.RawMessage `json:"inboundConfig" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warningf("Update inbound request failed: invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Parse inbound config to get tag for logging
	var inboundJSON map[string]interface{}
	if err := json.Unmarshal(req.InboundConfig, &inboundJSON); err == nil {
		if tag, ok := inboundJSON["tag"].(string); ok {
			logger.Infof("Update inbound request received: tag=%s", tag)
		}
	}

	if !s.xrayManager.IsRunning() {
		logger.Warningf("Cannot update inbound: XRAY is not running")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "XRAY is not running"})
		return
	}

	// Update inbound via Xray API (instant, no restart needed)
	if err := s.xrayManager.UpdateInbound(req.InboundConfig); err != nil {
		logger.Errorf("Failed to update inbound via API: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	tag, _ := inboundJSON["tag"].(string)
	logger.Infof("Inbound updated successfully via API: %s", tag)
	c.JSON(http.StatusOK, gin.H{"message": "Inbound updated successfully"})
}
