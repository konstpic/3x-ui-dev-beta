package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/web/global"
	"github.com/mhsanaei/3x-ui/v2/web/service"
	"github.com/mhsanaei/3x-ui/v2/web/websocket"

	"github.com/gin-gonic/gin"
)

var filenameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-.]+$`)

// ServerController handles server management and status-related operations.
type ServerController struct {
	BaseController

	serverService  service.ServerService
	settingService service.SettingService
	panelService   service.PanelService

	lastStatus *service.Status

	lastVersions        []string
	lastGetVersionsTime int64 // unix seconds
}

// NewServerController creates a new ServerController, initializes routes, and starts background tasks.
func NewServerController(g *gin.RouterGroup) *ServerController {
	a := &ServerController{}
	a.initRouter(g)
	a.startTask()
	return a
}

// initRouter sets up the routes for server status, Xray management, and utility endpoints.
func (a *ServerController) initRouter(g *gin.RouterGroup) {

	g.GET("/status", a.status)
	g.GET("/cpuHistory/:bucket", a.getCpuHistoryBucket)
	g.GET("/getXrayVersion", a.getXrayVersion)
	g.GET("/getConfigJson", a.getConfigJson)
	g.GET("/getDb", a.getDb)
	g.GET("/getNewUUID", a.getNewUUID)
	g.GET("/getNewX25519Cert", a.getNewX25519Cert)
	g.GET("/getNewmldsa65", a.getNewmldsa65)
	g.GET("/getNewmlkem768", a.getNewmlkem768)
	g.GET("/getNewVlessEnc", a.getNewVlessEnc)

	g.POST("/stopXrayService", a.stopXrayService)
	g.POST("/restartXrayService", a.restartXrayService)
	g.POST("/installXray/:version", a.installXray)
	g.POST("/installXrayOnNodes/:version", a.installXrayOnNodes)
	g.POST("/updateGeofile", a.updateGeofile)
	g.POST("/updateGeofile/:fileName", a.updateGeofile)
	g.POST("/logs/:count", a.getLogs)
	g.POST("/xraylogs/:count", a.getXrayLogs)
	g.POST("/importDB", a.importDB)
	g.POST("/getNewEchCert", a.getNewEchCert)
	g.GET("/metrics", a.getMetrics)
}

// refreshStatus updates the cached server status and collects CPU history.
func (a *ServerController) refreshStatus() {
	a.lastStatus = a.serverService.GetStatus(a.lastStatus)
	// collect cpu history when status is fresh
	if a.lastStatus != nil {
		a.serverService.AppendCpuSample(time.Now(), a.lastStatus.Cpu)
		// Broadcast status update via WebSocket
		websocket.BroadcastStatus(a.lastStatus)
	}
}

// startTask initiates background tasks for continuous status monitoring.
func (a *ServerController) startTask() {
	webServer := global.GetWebServer()
	c := webServer.GetCron()
	c.AddFunc("@every 1s", func() {
		// Always refresh to keep CPU history collected continuously for real-time updates.
		// Sampling is lightweight and capped to ~6 hours in memory.
		a.refreshStatus()
	})
}

// status returns the current server status information.
func (a *ServerController) status(c *gin.Context) { jsonObj(c, a.lastStatus, nil) }

// getCpuHistoryBucket retrieves aggregated CPU usage history based on the specified time bucket.
func (a *ServerController) getCpuHistoryBucket(c *gin.Context) {
	bucketStr := c.Param("bucket")
	bucket, err := strconv.Atoi(bucketStr)
	if err != nil || bucket <= 0 {
		jsonMsg(c, "invalid bucket", fmt.Errorf("bad bucket"))
		return
	}
	allowed := map[int]bool{
		2:   true, // Real-time view
		30:  true, // 30s intervals
		60:  true, // 1m intervals
		120: true, // 2m intervals
		180: true, // 3m intervals
		300: true, // 5m intervals
	}
	if !allowed[bucket] {
		jsonMsg(c, "invalid bucket", fmt.Errorf("unsupported bucket"))
		return
	}
	points := a.serverService.AggregateCpuHistory(bucket, 60)
	jsonObj(c, points, nil)
}

// getXrayVersion retrieves available Xray versions, with caching for 1 minute.
func (a *ServerController) getXrayVersion(c *gin.Context) {
	now := time.Now().Unix()
	if now-a.lastGetVersionsTime <= 60 { // 1 minute cache
		jsonObj(c, a.lastVersions, nil)
		return
	}

	versions, err := a.serverService.GetXrayVersions()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "getVersion"), err)
		return
	}

	a.lastVersions = versions
	a.lastGetVersionsTime = now

	jsonObj(c, versions, nil)
}

// installXray installs or updates Xray to the specified version.
func (a *ServerController) installXray(c *gin.Context) {
	version := c.Param("version")
	err := a.serverService.UpdateXray(version)
	jsonMsg(c, I18nWeb(c, "pages.index.xraySwitchVersionPopover"), err)
}

// installXrayOnNodes installs Xray version on selected nodes.
func (a *ServerController) installXrayOnNodes(c *gin.Context) {
	version := c.Param("version")
	
	// Log request details for debugging
	contentType := c.ContentType()
	logger.Debugf("installXrayOnNodes: Content-Type=%s, version=%s", contentType, version)
	
	// Try to get nodeIds from JSON body first (if Content-Type is application/json)
	// This must be done BEFORE ShouldBind, which reads the body
	var nodeIdsFromJSON []int
	var hasNodeIdsInJSON bool
	
	if contentType == "application/json" {
		// Read raw body to extract nodeIds
		bodyBytes, err := c.GetRawData()
		if err == nil && len(bodyBytes) > 0 {
			logger.Debugf("installXrayOnNodes: Raw body: %s", string(bodyBytes))
			// Parse JSON to extract nodeIds
			var jsonData map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &jsonData); err == nil {
				logger.Debugf("installXrayOnNodes: Parsed JSON: %+v", jsonData)
				// Check for nodeIds array
				if nodeIdsVal, ok := jsonData["nodeIds"]; ok {
					hasNodeIdsInJSON = true
					if nodeIdsArray, ok := nodeIdsVal.([]interface{}); ok {
						for _, val := range nodeIdsArray {
							if num, ok := val.(float64); ok {
								nodeIdsFromJSON = append(nodeIdsFromJSON, int(num))
							} else if num, ok := val.(int); ok {
								nodeIdsFromJSON = append(nodeIdsFromJSON, num)
							}
						}
					}
					logger.Debugf("installXrayOnNodes: Extracted nodeIds from JSON: %v", nodeIdsFromJSON)
				}
			} else {
				logger.Warningf("installXrayOnNodes: Failed to parse JSON: %v", err)
			}
			// Restore body for ShouldBind
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}
	}
	
	var nodeIds []int
	var formBodyBytes []byte
	
	if hasNodeIdsInJSON {
		// Use nodeIds from JSON
		nodeIds = nodeIdsFromJSON
		logger.Debugf("installXrayOnNodes: Using nodeIds from JSON: %v", nodeIds)
	} else {
		// For form-urlencoded, read raw body first and save it
		formBodyBytes, _ = c.GetRawData()
		if len(formBodyBytes) > 0 {
			logger.Debugf("installXrayOnNodes: Raw body (form-urlencoded): %s", string(formBodyBytes))
			// Restore body for form parsing
			c.Request.Body = io.NopCloser(bytes.NewBuffer(formBodyBytes))
		}
		
		// Parse form
		if err := c.Request.ParseForm(); err == nil {
			logger.Debugf("installXrayOnNodes: Form values: %+v", c.Request.PostForm)
			logger.Debugf("installXrayOnNodes: PostForm values for 'nodeIds': %v", c.Request.PostForm["nodeIds"])
		} else {
			logger.Warningf("installXrayOnNodes: Failed to parse form: %v", err)
		}
		
		// Get from form-urlencoded data (nodeIds=1&nodeIds=2 format)
		// First check if the field exists
		_, hasNodeIds := c.GetPostForm("nodeIds")
		logger.Debugf("installXrayOnNodes: Has nodeIds in form: %v", hasNodeIds)
		
		nodeIdsStr := c.PostFormArray("nodeIds")
		logger.Debugf("installXrayOnNodes: Received nodeIds from form: %v (count: %d)", nodeIdsStr, len(nodeIdsStr))
		
		// Also try QueryArray in case it's in query string
		if len(nodeIdsStr) == 0 {
			nodeIdsStr = c.QueryArray("nodeIds")
			logger.Debugf("installXrayOnNodes: Received nodeIds from query: %v (count: %d)", nodeIdsStr, len(nodeIdsStr))
		}
		
		// If still empty, try to parse from raw body manually (for form-urlencoded)
		if len(nodeIdsStr) == 0 && len(formBodyBytes) > 0 {
			bodyStr := string(formBodyBytes)
			logger.Debugf("installXrayOnNodes: Attempting manual parse of body: %s", bodyStr)
			// Parse form-urlencoded manually: nodeIds=1&nodeIds=2
			parts := strings.Split(bodyStr, "&")
			for _, part := range parts {
				if strings.HasPrefix(part, "nodeIds=") {
					idStr := strings.TrimPrefix(part, "nodeIds=")
					// URL decode if needed
					if decoded, err := url.QueryUnescape(idStr); err == nil {
						idStr = decoded
					}
					idStr = strings.TrimSpace(idStr)
					if id, err := strconv.Atoi(idStr); err == nil && id > 0 {
						nodeIds = append(nodeIds, id)
						logger.Debugf("installXrayOnNodes: Manually parsed nodeId: %d", id)
					}
				}
			}
		} else {
			// Parse from PostFormArray
			for _, idStr := range nodeIdsStr {
				if idStr != "" {
					if id, err := strconv.Atoi(idStr); err == nil && id > 0 {
						nodeIds = append(nodeIds, id)
					} else {
						logger.Warningf("Invalid nodeId in array: %s (error: %v)", idStr, err)
					}
				}
			}
		}
		logger.Debugf("installXrayOnNodes: Final parsed nodeIds: %v", nodeIds)
	}
	
	if len(nodeIds) == 0 {
		jsonMsg(c, "No nodes selected", nil)
		return
	}
	
	logger.Debugf("Installing Xray version %s on nodes: %v", version, nodeIds)
	
	nodeService := service.NodeService{}
	var errors []string
	var success []string
	
	for _, nodeId := range nodeIds {
		node, err := nodeService.GetNode(nodeId)
		if err != nil {
			errors = append(errors, fmt.Sprintf("Node %d: %v", nodeId, err))
			continue
		}
		
		err = nodeService.InstallXrayVersion(node, version)
		if err != nil {
			errors = append(errors, fmt.Sprintf("Node %d (%s): %v", nodeId, node.Name, err))
		} else {
			success = append(success, fmt.Sprintf("Node %d (%s)", nodeId, node.Name))
		}
	}
	
	var message string
	if len(success) > 0 && len(errors) == 0 {
		message = fmt.Sprintf("Xray version %s installed successfully on %d node(s)", version, len(success))
	} else if len(success) > 0 && len(errors) > 0 {
		message = fmt.Sprintf("Installed on %d node(s), failed on %d node(s)", len(success), len(errors))
	} else {
		message = fmt.Sprintf("Failed to install on all nodes")
	}
	
	if len(errors) > 0 {
		message += ": " + errors[0] // Show first error
		if len(errors) > 1 {
			message += fmt.Sprintf(" (and %d more)", len(errors)-1)
		}
	}
	
	if len(errors) > 0 && len(success) == 0 {
		jsonMsg(c, message, fmt.Errorf("installation failed"))
	} else {
		jsonMsg(c, message, nil)
	}
}

// updateGeofile updates the specified geo file for Xray.
func (a *ServerController) updateGeofile(c *gin.Context) {
	fileName := c.Param("fileName")

	// Validate the filename for security (prevent path traversal attacks)
	if fileName != "" && !a.serverService.IsValidGeofileName(fileName) {
		jsonMsg(c, I18nWeb(c, "pages.index.geofileUpdatePopover"),
			fmt.Errorf("invalid filename: contains unsafe characters or path traversal patterns"))
		return
	}

	err := a.serverService.UpdateGeofile(fileName)
	jsonMsg(c, I18nWeb(c, "pages.index.geofileUpdatePopover"), err)
}

// stopXrayService stops the Xray service.
func (a *ServerController) stopXrayService(c *gin.Context) {
	err := a.serverService.StopXrayService()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.xray.stopError"), err)
		websocket.BroadcastXrayState("error", err.Error())
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.xray.stopSuccess"), err)
	websocket.BroadcastXrayState("stop", "")
	websocket.BroadcastNotification(
		I18nWeb(c, "pages.xray.stopSuccess"),
		"Xray service has been stopped",
		"warning",
	)
}

// restartXrayService restarts the Xray service.
func (a *ServerController) restartXrayService(c *gin.Context) {
	err := a.serverService.RestartXrayService()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.xray.restartError"), err)
		websocket.BroadcastXrayState("error", err.Error())
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.xray.restartSuccess"), err)
	websocket.BroadcastXrayState("running", "")
	websocket.BroadcastNotification(
		I18nWeb(c, "pages.xray.restartSuccess"),
		"Xray service has been restarted successfully",
		"success",
	)
}

// getLogs retrieves the application logs based on count, level, and syslog filters.
func (a *ServerController) getLogs(c *gin.Context) {
	count := c.Param("count")
	level := c.PostForm("level")
	syslog := c.PostForm("syslog")
	logs := a.serverService.GetLogs(count, level, syslog)
	jsonObj(c, logs, nil)
}

// getXrayLogs retrieves Xray logs with filtering options for direct, blocked, and proxy traffic.
func (a *ServerController) getXrayLogs(c *gin.Context) {
	count := c.Param("count")
	filter := c.PostForm("filter")
	showDirect := c.PostForm("showDirect")
	showBlocked := c.PostForm("showBlocked")
	showProxy := c.PostForm("showProxy")

	var freedoms []string
	var blackholes []string

	//getting tags for freedom and blackhole outbounds
	config, err := a.settingService.GetDefaultXrayConfig()
	if err == nil && config != nil {
		if cfgMap, ok := config.(map[string]any); ok {
			if outbounds, ok := cfgMap["outbounds"].([]any); ok {
				for _, outbound := range outbounds {
					if obMap, ok := outbound.(map[string]any); ok {
						switch obMap["protocol"] {
						case "freedom":
							if tag, ok := obMap["tag"].(string); ok {
								freedoms = append(freedoms, tag)
							}
						case "blackhole":
							if tag, ok := obMap["tag"].(string); ok {
								blackholes = append(blackholes, tag)
							}
						}
					}
				}
			}
		}
	}

	if len(freedoms) == 0 {
		freedoms = []string{"direct"}
	}
	if len(blackholes) == 0 {
		blackholes = []string{"blocked"}
	}

	nodeId := c.PostForm("nodeId")
	logs := a.serverService.GetXrayLogs(count, filter, showDirect, showBlocked, showProxy, freedoms, blackholes, nodeId)
	jsonObj(c, logs, nil)
}

// getConfigJson retrieves the Xray configuration as JSON.
func (a *ServerController) getConfigJson(c *gin.Context) {
	configJson, err := a.serverService.GetConfigJson()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.index.getConfigError"), err)
		return
	}
	jsonObj(c, configJson, nil)
}

// getDb downloads the database file.
func (a *ServerController) getDb(c *gin.Context) {
	db, err := a.serverService.GetDb()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.index.getDatabaseError"), err)
		return
	}

	filename := "x-ui-db-backup.sql"

	if !isValidFilename(filename) {
		c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid filename"))
		return
	}

	// Set the headers for the response
	c.Header("Content-Type", "application/sql")
	c.Header("Content-Disposition", "attachment; filename="+filename)

	// Write the file contents to the response
	c.Writer.Write(db)
}

func isValidFilename(filename string) bool {
	// Validate that the filename only contains allowed characters
	return filenameRegex.MatchString(filename)
}

// importDB imports a database file and restarts the container.
func (a *ServerController) importDB(c *gin.Context) {
	// Get the file from the request body
	file, _, err := c.Request.FormFile("db")
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.index.readDatabaseError"), err)
		return
	}
	defer file.Close()
	
	// Import database
	err = a.serverService.ImportDB(file)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.index.importDatabaseError"), err)
		return
	}
	
	// Restart container after successful import to ensure all services use new database
	if err := a.panelService.RestartContainer(time.Second * 3); err != nil {
		logger.Warningf("Failed to restart container after DB import: %v", err)
		// Don't fail the import if container restart fails, but log it
	}
	
	jsonObj(c, I18nWeb(c, "pages.index.importDatabaseSuccess"), nil)
}

// getNewX25519Cert generates a new X25519 certificate.
func (a *ServerController) getNewX25519Cert(c *gin.Context) {
	cert, err := a.serverService.GetNewX25519Cert()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.getNewX25519CertError"), err)
		return
	}
	jsonObj(c, cert, nil)
}

// getNewmldsa65 generates a new ML-DSA-65 key.
func (a *ServerController) getNewmldsa65(c *gin.Context) {
	cert, err := a.serverService.GetNewmldsa65()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.getNewmldsa65Error"), err)
		return
	}
	jsonObj(c, cert, nil)
}

// getNewEchCert generates a new ECH certificate for the given SNI.
func (a *ServerController) getNewEchCert(c *gin.Context) {
	sni := c.PostForm("sni")
	cert, err := a.serverService.GetNewEchCert(sni)
	if err != nil {
		jsonMsg(c, "get ech certificate", err)
		return
	}
	jsonObj(c, cert, nil)
}

// getNewVlessEnc generates a new VLESS encryption key.
func (a *ServerController) getNewVlessEnc(c *gin.Context) {
	out, err := a.serverService.GetNewVlessEnc()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.getNewVlessEncError"), err)
		return
	}
	jsonObj(c, out, nil)
}

// getNewUUID generates a new UUID.
func (a *ServerController) getNewUUID(c *gin.Context) {
	uuidResp, err := a.serverService.GetNewUUID()
	if err != nil {
		jsonMsg(c, "Failed to generate UUID", err)
		return
	}

	jsonObj(c, uuidResp, nil)
}

// getNewmlkem768 generates a new ML-KEM-768 key.
func (a *ServerController) getNewmlkem768(c *gin.Context) {
	out, err := a.serverService.GetNewmlkem768()
	if err != nil {
		jsonMsg(c, "Failed to generate mlkem768 keys", err)
		return
	}
	jsonObj(c, out, nil)
}

// getMetrics returns metrics in Prometheus format
func (a *ServerController) getMetrics(c *gin.Context) {
	metrics := service.CollectMetrics()
	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	c.String(http.StatusOK, metrics)
}
