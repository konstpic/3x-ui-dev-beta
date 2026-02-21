package job

import (
	"encoding/json"
	"time"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/web/service"
	"github.com/mhsanaei/3x-ui/v2/web/websocket"
	"github.com/mhsanaei/3x-ui/v2/xray"

	"github.com/valyala/fasthttp"
)

// XrayTrafficJob collects and processes traffic statistics from Xray, updating the database and optionally informing external APIs.
type XrayTrafficJob struct {
	settingService  service.SettingService
	xrayService     service.XrayService
	inboundService  service.InboundService
	outboundService service.OutboundService
}

// NewXrayTrafficJob creates a new traffic collection job instance.
func NewXrayTrafficJob() *XrayTrafficJob {
	return new(XrayTrafficJob)
}

// Run collects traffic statistics from Xray and updates the database, triggering restart if needed.
// In multi-node mode, it broadcasts WebSocket events using data from database (updated by CollectNodeStats).
// In single-node mode, it collects traffic from local Xray and updates database.
func (j *XrayTrafficJob) Run() {
	// Check if multi-node mode is enabled
	multiMode, err := j.settingService.GetMultiNodeMode()
	if err != nil {
		logger.Warningf("Failed to get multi-node mode setting: %v", err)
		multiMode = false
	}

	if multiMode {
		// In multi-node mode, traffic is collected by CollectNodeStats job
		// We just need to broadcast WebSocket events using data from database
		j.broadcastWebSocketEvents()
		return
	}

	// Single-node mode: collect traffic from local Xray
	if !j.xrayService.IsXrayRunning() {
		return
	}
	traffics, clientTraffics, err := j.xrayService.GetXrayTraffic()
	if err != nil {
		return
	}
	
	err, needRestart0 := j.inboundService.AddTraffic(traffics, clientTraffics)
	if err != nil {
		logger.Warning("add inbound traffic failed:", err)
	}
	err, needRestart1 := j.outboundService.AddTraffic(traffics, clientTraffics)
	if err != nil {
		logger.Warning("add outbound traffic failed:", err)
	}
	if ExternalTrafficInformEnable, err := j.settingService.GetExternalTrafficInformEnable(); ExternalTrafficInformEnable {
		j.informTrafficToExternalAPI(traffics, clientTraffics)
	} else if err != nil {
		logger.Warning("get ExternalTrafficInformEnable failed:", err)
	}
	if needRestart0 || needRestart1 {
		j.xrayService.SetToNeedRestart()
	}

	// Broadcast WebSocket events (same for both modes)
	j.broadcastWebSocketEvents()
}

// broadcastWebSocketEvents broadcasts all WebSocket events (clients, inbounds, outbounds, traffic)
// This works in both single-node and multi-node modes, using data from database.
func (j *XrayTrafficJob) broadcastWebSocketEvents() {
	// Get online clients and last online map for real-time status updates
	onlineClients := j.inboundService.GetOnlineClients()
	lastOnlineMap, err := j.inboundService.GetClientsLastOnline()
	if err != nil {
		logger.Warning("get clients last online failed:", err)
		lastOnlineMap = make(map[string]int64)
	}

	// Fetch updated inbounds from database with accumulated traffic values
	// This ensures frontend receives the actual total traffic, not just delta values
	updatedInbounds, err := j.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("get all inbounds for websocket failed:", err)
	}

	updatedOutbounds, err := j.outboundService.GetOutboundsTraffic()
	if err != nil {
		logger.Warning("get all outbounds for websocket failed:", err)
	}

	// Build traffic update (for compatibility, use empty arrays if no traffic data)
	// In multi-node mode, traffic is aggregated in database, so we don't need to send raw traffic
	trafficUpdate := map[string]interface{}{
		"traffics":       []interface{}{}, // Empty for multi-node, will be populated from inbounds/clients
		"clientTraffics": []interface{}{}, // Empty for multi-node, will be populated from clients
		"onlineClients":  onlineClients,
		"lastOnlineMap":  lastOnlineMap,
	}
	websocket.BroadcastTraffic(trafficUpdate)

	// Broadcast full inbounds update for real-time UI refresh
	if updatedInbounds != nil {
		websocket.BroadcastInbounds(updatedInbounds)
	}

	if updatedOutbounds != nil {
		websocket.BroadcastOutbounds(updatedOutbounds)
	}

	// Broadcast clients update for real-time traffic updates on clients page
	// Get all clients directly from ClientEntity (traffic is stored there)
	clientService := service.ClientService{}
	// Get clients for all users - frontend will filter by current user
	// We need to get all clients, so we'll query directly from DB
	db := database.GetDB()
	var allClients []*model.ClientEntity
	clientsErr := db.Find(&allClients).Error
	if clientsErr == nil {
		if len(allClients) > 0 {
			// Load inbound assignments and HWIDs for each client (like GetClients does)
			hwidService := service.ClientHWIDService{}
			now := time.Now().Unix() * 1000
			// Collect clients that need to be disabled (for API removal)
			clientsToDisable := make(map[string]string) // map[email]tag
			
			for _, client := range allClients {
				inboundIds, inboundErr := clientService.GetInboundIdsForClient(client.Id)
				if inboundErr == nil {
					client.InboundIds = inboundIds
				}
				// Load HWIDs for real-time updates
				hwids, hwidErr := hwidService.GetHWIDsForClient(client.Id)
				if hwidErr == nil {
					client.HWIDs = hwids
				}
				
				// Check and update status if expired (same logic as GetClients)
				totalUsed := client.Up + client.Down
				trafficLimit := int64(client.TotalGB * 1024 * 1024 * 1024)
				trafficExceeded := client.TotalGB > 0 && totalUsed >= trafficLimit
				timeExpired := client.ExpiryTime > 0 && client.ExpiryTime <= now
				
				if trafficExceeded || timeExpired {
					status := "expired_traffic"
					if timeExpired {
						status = "expired_time"
					}
					// Update status if changed (for real-time WebSocket updates)
					if client.Status != status {
						client.Status = status
						// Update in DB
						if err := db.Model(&model.ClientEntity{}).Where("id = ?", client.Id).Update("status", status).Error; err != nil {
							logger.Warningf("Failed to update status for client %s: %v", client.Email, err)
						}
						
						// Collect client for API removal if enabled
						if client.Enable && len(inboundIds) > 0 {
							// Get tag from first inbound
							var inbound model.Inbound
							if err := db.Where("id = ?", inboundIds[0]).First(&inbound).Error; err == nil {
								clientsToDisable[client.Email] = inbound.Tag
							}
						}
					}
				}
			}
			
			// Remove expired clients from Xray API (both local and nodes) asynchronously
			if len(clientsToDisable) > 0 {
				go func() {
					inboundService := service.InboundService{}
					_, err := clientService.DisableClientsByEmail(clientsToDisable, &inboundService)
					if err != nil {
						logger.Warningf("XrayTrafficJob: failed to disable expired clients via API: %v", err)
					}
				}()
			}
			
			logger.Debugf("Broadcasting %d clients via WebSocket for real-time updates", len(allClients))
			websocket.BroadcastClients(allClients)
		} else {
			logger.Debugf("No clients found to broadcast (empty database)")
		}
	} else {
		logger.Warningf("get all clients for websocket failed: %v", clientsErr)
	}
}

func (j *XrayTrafficJob) informTrafficToExternalAPI(inboundTraffics []*xray.Traffic, clientTraffics []*xray.ClientTraffic) {
	informURL, err := j.settingService.GetExternalTrafficInformURI()
	if err != nil {
		logger.Warning("get ExternalTrafficInformURI failed:", err)
		return
	}
	requestBody, err := json.Marshal(map[string]any{"clientTraffics": clientTraffics, "inboundTraffics": inboundTraffics})
	if err != nil {
		logger.Warning("parse client/inbound traffic failed:", err)
		return
	}
	request := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(request)
	request.Header.SetMethod("POST")
	request.Header.SetContentType("application/json; charset=UTF-8")
	request.SetBody([]byte(requestBody))
	request.SetRequestURI(informURL)
	response := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(response)
	if err := fasthttp.Do(request, response); err != nil {
		logger.Warning("POST ExternalTrafficInformURI failed:", err)
	}
}
