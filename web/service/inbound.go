// Package service provides business logic services for the 3x-ui web panel,
// including inbound/outbound management, user administration, settings, and Xray integration.
package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/common"
	"github.com/mhsanaei/3x-ui/v2/web/cache"
	"github.com/mhsanaei/3x-ui/v2/xray"

	"gorm.io/gorm"
)

// InboundService provides business logic for managing Xray inbound configurations.
// It handles CRUD operations for inbounds, client management, traffic monitoring,
// and integration with the Xray API for real-time updates.
type InboundService struct {
	xrayApi xray.XrayAPI
}

// getXrayAPI gets or creates a cached XrayAPI connection using the connection pool.
// This reuses connections to avoid overhead. The connection is automatically managed by the pool.
func (s *InboundService) getXrayAPI(apiPort int) (*xray.XrayAPI, error) {
	if apiPort <= 0 {
		return nil, fmt.Errorf("invalid API port: %d", apiPort)
	}

	xrayService := XrayService{}
	api, cleanup, err := xrayService.GetOrCreateAPI(apiPort)
	if err != nil {
		return nil, err
	}
	// Note: cleanup is a no-op, connection stays in pool
	_ = cleanup
	return api, nil
}

// inboundUpdateMutexes provides per-inbound mutexes to prevent concurrent updates
var inboundUpdateMutexes = make(map[int]*sync.Mutex)
var inboundMutexLock sync.Mutex

// getInboundMutex returns a mutex for a specific inbound ID to prevent concurrent updates
func getInboundMutex(inboundId int) *sync.Mutex {
	inboundMutexLock.Lock()
	defer inboundMutexLock.Unlock()
	
	if mutex, exists := inboundUpdateMutexes[inboundId]; exists {
		return mutex
	}
	
	mutex := &sync.Mutex{}
	inboundUpdateMutexes[inboundId] = mutex
	return mutex
}

// updateInboundWithRetry updates an inbound with retry logic for database lock errors.
// It uses a per-inbound mutex to prevent concurrent updates and retries up to 3 times
// with exponential backoff (50ms, 100ms, 200ms).
func (s *InboundService) updateInboundWithRetry(inbound *model.Inbound) (*model.Inbound, bool, error) {
	// Use per-inbound mutex to prevent concurrent updates of the same inbound
	mutex := getInboundMutex(inbound.Id)
	mutex.Lock()
	defer mutex.Unlock()
	
	maxRetries := 3
	baseDelay := 50 * time.Millisecond
	
	var result *model.Inbound
	var needRestart bool
	var err error
	
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 50ms, 100ms, 200ms
			delay := baseDelay * time.Duration(1<<uint(attempt-1))
			logger.Debugf("Retrying inbound %d update (attempt %d/%d) after %v", inbound.Id, attempt+1, maxRetries, delay)
			time.Sleep(delay)
		}
		
		result, needRestart, err = s.UpdateInbound(inbound)
		if err == nil {
			return result, needRestart, nil
		}
		
		// Check if error is "database is locked"
		errStr := err.Error()
		if strings.Contains(errStr, "database is locked") || strings.Contains(errStr, "locked") {
			if attempt < maxRetries-1 {
				logger.Debugf("Database locked for inbound %d, will retry: %v", inbound.Id, err)
				continue
			}
			// Last attempt failed
			logger.Warningf("Failed to update inbound %d after %d retries: %v", inbound.Id, maxRetries, err)
			return result, needRestart, err
		}
		
		// For other errors, don't retry
		return result, needRestart, err
	}
	
	return result, needRestart, err
}

// GetInbounds retrieves all inbounds for a specific user.
// Returns a slice of inbound models with their associated client statistics.
// No caching - data is real-time via WebSocket.
func (s *InboundService) GetInbounds(userId int) ([]*model.Inbound, error) {
	db := database.GetDB()
	var result []*model.Inbound
	err := db.Model(model.Inbound{}).Preload("ClientStats").Where("user_id = ?", userId).Find(&result).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	
	// Enrich with node assignments
	nodeService := NodeService{}
	for _, inbound := range result {
		// Load all nodes for this inbound
		nodes, err := nodeService.GetNodesForInbound(inbound.Id)
		if err == nil && len(nodes) > 0 {
			nodeIds := make([]int, len(nodes))
			for i, node := range nodes {
				nodeIds[i] = node.Id
			}
			inbound.NodeIds = nodeIds
			// Don't set nodeId - it's deprecated and causes confusion
			// nodeId is only for backward compatibility when receiving data from old clients
		} else {
			// Ensure empty array if no nodes assigned
			inbound.NodeIds = []int{}
		}
		
		// Enrich client stats with UUID/SubId from inbound settings
		clients, _ := s.GetClients(inbound)
		if len(clients) == 0 || len(inbound.ClientStats) == 0 {
			continue
		}
		// Build a map email -> client
		cMap := make(map[string]model.Client, len(clients))
		for _, c := range clients {
			cMap[strings.ToLower(c.Email)] = c
		}
		for i := range inbound.ClientStats {
			email := strings.ToLower(inbound.ClientStats[i].Email)
			if c, ok := cMap[email]; ok {
				inbound.ClientStats[i].UUID = c.ID
				inbound.ClientStats[i].SubId = c.SubID
			}
		}
	}
	
	return result, nil
}

// GetAllInbounds retrieves all inbounds from the database.
// Returns a slice of all inbound models with their associated client statistics.
func (s *InboundService) GetAllInbounds() ([]*model.Inbound, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).Preload("ClientStats").Find(&inbounds).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	
	// Enrich with node assignments
	nodeService := NodeService{}
	for _, inbound := range inbounds {
		// Load all nodes for this inbound
		nodes, err := nodeService.GetNodesForInbound(inbound.Id)
		if err == nil && len(nodes) > 0 {
			nodeIds := make([]int, len(nodes))
			for i, node := range nodes {
				nodeIds[i] = node.Id
			}
			inbound.NodeIds = nodeIds
			// Don't set nodeId - it's deprecated and causes confusion
			// nodeId is only for backward compatibility when receiving data from old clients
		} else {
			// Ensure empty array if no nodes assigned
			inbound.NodeIds = []int{}
		}
		
		// Enrich client stats with UUID/SubId from inbound settings
		clients, _ := s.GetClients(inbound)
		if len(clients) == 0 || len(inbound.ClientStats) == 0 {
			continue
		}
		cMap := make(map[string]model.Client, len(clients))
		for _, c := range clients {
			cMap[strings.ToLower(c.Email)] = c
		}
		for i := range inbound.ClientStats {
			email := strings.ToLower(inbound.ClientStats[i].Email)
			if c, ok := cMap[email]; ok {
				inbound.ClientStats[i].UUID = c.ID
				inbound.ClientStats[i].SubId = c.SubID
			}
		}
	}
	return inbounds, nil
}

func (s *InboundService) GetInboundsByTrafficReset(period string) ([]*model.Inbound, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).Where("traffic_reset = ?", period).Find(&inbounds).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	return inbounds, nil
}

// generateInboundTag generates a unique tag for an inbound.
// In single-node mode: uses port (and listen address if specified)
// In multi-node mode: uses ID to ensure uniqueness (allows same port with different SNI)
func (s *InboundService) generateInboundTag(inbound *model.Inbound, multiMode bool) string {
	if multiMode {
		// In multi-node mode, use ID for uniqueness (allows same port with different SNI)
		if inbound.Id > 0 {
			return fmt.Sprintf("inbound-%d", inbound.Id)
		}
		// If ID not yet assigned (during creation), use port temporarily
		// Tag will be updated after creation when ID is available
		if inbound.Listen == "" || inbound.Listen == "0.0.0.0" || inbound.Listen == "::" || inbound.Listen == "::0" {
			return fmt.Sprintf("inbound-%v", inbound.Port)
		}
		return fmt.Sprintf("inbound-%v:%v", inbound.Listen, inbound.Port)
	}
	// Single-node mode: use port (and listen address if specified)
	if inbound.Listen == "" || inbound.Listen == "0.0.0.0" || inbound.Listen == "::" || inbound.Listen == "::0" {
		return fmt.Sprintf("inbound-%v", inbound.Port)
	}
	return fmt.Sprintf("inbound-%v:%v", inbound.Listen, inbound.Port)
}

func (s *InboundService) checkPortExist(listen string, port int, ignoreId int) (bool, error) {
	db := database.GetDB()
	if listen == "" || listen == "0.0.0.0" || listen == "::" || listen == "::0" {
		db = db.Model(model.Inbound{}).Where("port = ?", port)
	} else {
		db = db.Model(model.Inbound{}).
			Where("port = ?", port).
			Where(
				db.Model(model.Inbound{}).Where(
					"listen = ?", listen,
				).Or(
					"listen = \"\"",
				).Or(
					"listen = \"0.0.0.0\"",
				).Or(
					"listen = \"::\"",
				).Or(
					"listen = \"::0\""))
	}
	if ignoreId > 0 {
		db = db.Where("id != ?", ignoreId)
	}
	var count int64
	err := db.Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetClients retrieves clients for an inbound.
// Always uses ClientEntity (new architecture).
func (s *InboundService) GetClients(inbound *model.Inbound) ([]model.Client, error) {
	clientService := ClientService{}
	
	// Get clients from ClientEntity (new architecture)
	clientEntities, err := clientService.GetClientsForInbound(inbound.Id)
	if err != nil {
		return nil, err
	}
	
	// Convert ClientEntity to Client
	clients := make([]model.Client, len(clientEntities))
	for i, entity := range clientEntities {
		clients[i] = clientService.ConvertClientEntityToClient(entity)
	}
	return clients, nil
}

// BuildSettingsFromClientEntities builds Settings JSON for Xray from ClientEntity.
// This method creates a minimal Settings structure with only fields needed by Xray.
func (s *InboundService) BuildSettingsFromClientEntities(inbound *model.Inbound, clientEntities []*model.ClientEntity) (string, error) {
	// Parse existing settings to preserve other fields (like encryption for VLESS)
	var settings map[string]any
	if inbound.Settings != "" {
		json.Unmarshal([]byte(inbound.Settings), &settings)
	}
	if settings == nil {
		settings = make(map[string]any)
	}
	
	// Build clients array for Xray (only minimal fields)
	var xrayClients []map[string]any
	for _, entity := range clientEntities {
		// Skip disabled clients or clients with expired status
		if !entity.Enable || entity.Status == "expired_traffic" || entity.Status == "expired_time" {
			continue
		}
		
		client := make(map[string]any)
		client["email"] = entity.Email
		
		switch inbound.Protocol {
		case model.Trojan:
			client["password"] = entity.Password
		case model.Shadowsocks:
			// For Shadowsocks, we need to get method from settings
			if method, ok := settings["method"].(string); ok {
				client["method"] = method
			}
			client["password"] = entity.Password
		case model.VMESS, model.VLESS:
			client["id"] = entity.UUID
			if inbound.Protocol == model.VMESS {
				if entity.Security != "" {
					client["security"] = entity.Security
				}
			}
			if inbound.Protocol == model.VLESS && entity.Flow != "" {
				client["flow"] = entity.Flow
			}
		}
		
		xrayClients = append(xrayClients, client)
	}
	
	settings["clients"] = xrayClients
	settingsJSON, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	
	return string(settingsJSON), nil
}

func (s *InboundService) getAllEmails() ([]string, error) {
	db := database.GetDB()
	var emails []string
	
	// Get emails from ClientEntity (new architecture only)
	err := db.Model(&model.ClientEntity{}).Pluck("email", &emails).Error
	if err != nil {
		return nil, err
	}
	
	return emails, nil
}

func (s *InboundService) contains(slice []string, str string) bool {
	lowerStr := strings.ToLower(str)
	for _, s := range slice {
		if strings.ToLower(s) == lowerStr {
			return true
		}
	}
	return false
}

func (s *InboundService) checkEmailsExistForClients(clients []model.Client) (string, error) {
	allEmails, err := s.getAllEmails()
	if err != nil {
		return "", err
	}
	var emails []string
	for _, client := range clients {
		if client.Email != "" {
			if s.contains(emails, client.Email) {
				return client.Email, nil
			}
			if s.contains(allEmails, client.Email) {
				return client.Email, nil
			}
			emails = append(emails, client.Email)
		}
	}
	return "", nil
}

func (s *InboundService) checkEmailExistForInbound(inbound *model.Inbound) (string, error) {
	clients, err := s.GetClients(inbound)
	if err != nil {
		return "", err
	}
	allEmails, err := s.getAllEmails()
	if err != nil {
		return "", err
	}
	var emails []string
	for _, client := range clients {
		if client.Email != "" {
			if s.contains(emails, client.Email) {
				return client.Email, nil
			}
			if s.contains(allEmails, client.Email) {
				return client.Email, nil
			}
			emails = append(emails, client.Email)
		}
	}
	return "", nil
}

// AddInbound creates a new inbound configuration.
// It validates port uniqueness, client email uniqueness, and required fields,
// then saves the inbound to the database and optionally adds it to the running Xray instance.
// Returns the created inbound, whether Xray needs restart, and any error.
func (s *InboundService) AddInbound(inbound *model.Inbound) (*model.Inbound, bool, error) {
	// Check port uniqueness only in single-node mode
	// In multi-node mode, same port is allowed (with different SNI), but one node cannot have two inbounds with same port
	settingService := SettingService{}
	multiMode, _ := settingService.GetMultiNodeMode()
	if !multiMode {
		exist, err := s.checkPortExist(inbound.Listen, inbound.Port, 0)
		if err != nil {
			return inbound, false, err
		}
		if exist {
			return inbound, false, common.NewError("Port already exists:", inbound.Port)
		}
	}

	existEmail, err := s.checkEmailExistForInbound(inbound)
	if err != nil {
		return inbound, false, err
	}
	if existEmail != "" {
		return inbound, false, common.NewError("Duplicate email:", existEmail)
	}

	clients, err := s.GetClients(inbound)
	if err != nil {
		return inbound, false, err
	}

	// Ensure created_at and updated_at on clients in settings
	if len(clients) > 0 {
		var settings map[string]any
		if err2 := json.Unmarshal([]byte(inbound.Settings), &settings); err2 == nil && settings != nil {
			now := time.Now().Unix() * 1000
			updatedClients := make([]model.Client, 0, len(clients))
			for _, c := range clients {
				if c.CreatedAt == 0 {
					c.CreatedAt = now
				}
				c.UpdatedAt = now
				updatedClients = append(updatedClients, c)
			}
			settings["clients"] = updatedClients
			if bs, err3 := json.MarshalIndent(settings, "", "  "); err3 == nil {
				inbound.Settings = string(bs)
			} else {
				logger.Debug("Unable to marshal inbound settings with timestamps:", err3)
			}
		} else if err2 != nil {
			logger.Debug("Unable to parse inbound settings for timestamps:", err2)
		}
	}

	// Secure client ID (only validate if clients are provided)
	// Allow creating inbounds without clients
	if len(clients) > 0 {
		for _, client := range clients {
			switch inbound.Protocol {
			case "trojan":
				if client.Password == "" {
					return inbound, false, common.NewError("empty client ID")
				}
			case "shadowsocks":
				if client.Email == "" {
					return inbound, false, common.NewError("empty client ID")
				}
			default:
				if client.ID == "" {
					return inbound, false, common.NewError("empty client ID")
				}
			}
		}
	}

	db := database.GetDB()
	
	err = db.Transaction(func(tx *gorm.DB) error {
		return tx.Save(inbound).Error
	})
	
	if err != nil {
		return inbound, false, err
	}
	
	// In multi-node mode, update tag with ID after creation to ensure uniqueness
	if multiMode && inbound.Id > 0 {
		newTag := s.generateInboundTag(inbound, multiMode)
		if inbound.Tag != newTag {
			inbound.Tag = newTag
			if err := db.Model(inbound).Update("tag", newTag).Error; err != nil {
				logger.Warningf("Failed to update inbound tag after creation: %v", err)
			} else {
				logger.Debugf("Updated inbound tag to %s after creation (multi-node mode)", newTag)
			}
		}
	}
	
	// Invalidate cache for this user's inbounds
	if inbound.UserId > 0 {
		cache.InvalidateInbounds(inbound.UserId)
	}
	
	// Note: ClientStats are no longer managed here - clients are managed through ClientEntity
	// Traffic is stored directly in ClientEntity table

	needRestart := false
	if inbound.Enable {
		if p != nil {
			apiPort := p.GetAPIPort()
			api, err := s.getXrayAPI(apiPort)
			if err == nil {
				inboundJson, err1 := json.MarshalIndent(inbound.GenXrayInboundConfig(), "", "  ")
				if err1 != nil {
					logger.Debug("Unable to marshal inbound config:", err1)
				} else {
					err1 = api.AddInbound(inboundJson)
					if err1 == nil {
						logger.Debug("New inbound added by api:", inbound.Tag)
					} else {
						logger.Debug("Unable to add inbound by api:", err1)
						needRestart = true
					}
				}
			} else {
				logger.Debug("Failed to get XrayAPI connection:", err)
				needRestart = true
			}
		} else {
			logger.Debugf("[DEBUG-AGENT] AddInbound service: Xray process is nil, skipping live Xray API add; inbound will be applied on next restart")
		}
	}

	// Send notification about inbound creation
	tgbotService := Tgbot{}
	if tgbotService.IsRunning() {
		tgbotService.NotifyInboundCreated(inbound)
	}

	return inbound, needRestart, err
}

// DelInbound deletes an inbound configuration by ID.
// It removes the inbound from the database and the running Xray instance if active.
// Returns whether Xray needs restart and any error.
func (s *InboundService) DelInbound(id int) (bool, error) {
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] DelInbound: START, inboundId=%d", id)
	// #endregion
	
	db := database.GetDB()
	if db == nil {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] DelInbound: ERROR db is nil, inboundId=%d", id)
		// #endregion
		return false, fmt.Errorf("database connection is nil")
	}

	var tag string
	needRestart := false
	result := db.Model(model.Inbound{}).Select("tag").Where("id = ? and enable = ?", id, true).First(&tag)
	if result.Error == nil {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] DelInbound: Xray API delete, inboundId=%d, tag=%s", id, tag)
		// #endregion
		
		// Check multi-node mode
		settingService := SettingService{}
		multiMode, _ := settingService.GetMultiNodeMode()
		
		if multiMode {
			// Multi-node mode: remove from all nodes assigned to this inbound
			nodeService := NodeService{}
			nodes, err := nodeService.GetNodesForInbound(id)
			if err == nil && len(nodes) > 0 {
				// Remove inbound from all nodes via API (async, non-blocking)
				for _, node := range nodes {
					go func(n *model.Node) {
						if err := nodeService.RemoveInboundFromNode(n, tag); err != nil {
							logger.Warningf("DelInbound: failed to remove inbound %s from node %s via API: %v", tag, n.Name, err)
						} else {
							logger.Infof("DelInbound: removed inbound %s from node %s via API (instant)", tag, n.Name)
						}
					}(node)
				}
				logger.Debugf("DelInbound: using fast API delete for inbound %s on %d node(s)", tag, len(nodes))
			} else {
				logger.Debugf("DelInbound: no nodes found for inbound %d, skipping API delete", id)
			}
		} else if p != nil {
			// Single mode: remove from local Xray via API
			apiPort := p.GetAPIPort()
			api, err := s.getXrayAPI(apiPort)
			if err == nil {
				err1 := api.DelInbound(tag)
				if err1 == nil {
					logger.Debug("Inbound deleted by api:", tag)
				} else {
					logger.Debug("Unable to delete inbound by api:", err1)
					needRestart = true
				}
			} else {
				logger.Debug("Failed to get XrayAPI connection:", err)
				needRestart = true
			}
		} else {
			logger.Debugf("[DEBUG-AGENT] DelInbound: Xray process is nil, skipping live Xray API delete; inbound removal will apply on next restart, inboundId=%d", id)
		}
	} else {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] DelInbound: inbound not enabled or not found, inboundId=%d, error=%v", id, result.Error)
		// #endregion
		logger.Debug("No enabled inbound founded to removing by api", tag)
	}

	// Get inbound data FIRST before deleting anything (needed for userId and clients)
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] DelInbound: getting inbound, inboundId=%d", id)
	// #endregion
	inbound, err := s.GetInbound(id)
	if err != nil {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] DelInbound: ERROR getting inbound, inboundId=%d, error=%v", id, err)
		// #endregion
		return false, err
	}
	userId := inbound.UserId
	
	// Delete client traffics of inbounds
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] DelInbound: deleting client_traffics, inboundId=%d", id)
	// #endregion
	err = db.Where("inbound_id = ?", id).Delete(xray.ClientTraffic{}).Error
	if err != nil {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] DelInbound: ERROR deleting client_traffics, inboundId=%d, error=%v", id, err)
		// #endregion
		return false, err
	}
	
	// Delete node mappings for this inbound (cascade delete)
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] DelInbound: deleting inbound_node_mappings, inboundId=%d", id)
	// #endregion
	err = db.Where("inbound_id = ?", id).Delete(&model.InboundNodeMapping{}).Error
	if err != nil {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] DelInbound: ERROR deleting inbound_node_mappings, inboundId=%d, error=%v", id, err)
		// #endregion
		return false, err
	}
	
	// Delete client_inbound_mappings for this inbound
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] DelInbound: deleting client_inbound_mappings, inboundId=%d", id)
	// #endregion
	err = db.Where("inbound_id = ?", id).Delete(&model.ClientInboundMapping{}).Error
	if err != nil {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] DelInbound: ERROR deleting client_inbound_mappings, inboundId=%d, error=%v", id, err)
		// #endregion
		return false, err
	}
	
	// Delete host_inbound_mappings for this inbound
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] DelInbound: deleting host_inbound_mappings, inboundId=%d", id)
	// #endregion
	err = db.Where("inbound_id = ?", id).Delete(&model.HostInboundMapping{}).Error
	if err != nil {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] DelInbound: ERROR deleting host_inbound_mappings, inboundId=%d, error=%v", id, err)
		// #endregion
		return false, err
	}
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] DelInbound: getting clients, inboundId=%d, userId=%d", id, userId)
	// #endregion
	clients, err := s.GetClients(inbound)
	if err != nil {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] DelInbound: ERROR getting clients, inboundId=%d, error=%v", id, err)
		// #endregion
		return false, err
	}
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] DelInbound: deleting client IPs, inboundId=%d, clientsCount=%d", id, len(clients))
	// #endregion
	for _, client := range clients {
		err := s.DelClientIPs(db, client.Email)
		if err != nil {
			// #region agent log
			logger.Debugf("[DEBUG-AGENT] DelInbound: ERROR deleting client IPs, inboundId=%d, email=%s, error=%v", id, client.Email, err)
			// #endregion
			return false, err
		}
	}

	// #region agent log
	logger.Debugf("[DEBUG-AGENT] DelInbound: before Delete, inboundId=%d, userId=%d", id, userId)
	// #endregion
	
	err = db.Delete(model.Inbound{}, id).Error
	
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] DelInbound: after Delete, inboundId=%d, error=%v, rowsAffected=%d", id, err, db.RowsAffected)
	// #endregion
	
	if err == nil && userId > 0 {
		// Invalidate cache for this user's inbounds
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] DelInbound: before cache invalidation, userId=%d", userId)
		// #endregion
		cacheErr := cache.InvalidateInbounds(userId)
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] DelInbound: after cache invalidation, userId=%d, error=%v", userId, cacheErr)
		// #endregion
		
		// Send notification about inbound deletion (only if deletion was successful)
		if err == nil {
			tgbotService := Tgbot{}
			if tgbotService.IsRunning() {
				tgbotService.NotifyInboundDeleted(inbound)
			}
		}
	}
	return needRestart, err
}

func (s *InboundService) GetInbound(id int) (*model.Inbound, error) {
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] GetInbound: START, inboundId=%d", id)
	// #endregion
	db := database.GetDB()
	if db == nil {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] GetInbound: ERROR db is nil, inboundId=%d", id)
		// #endregion
		return nil, fmt.Errorf("database connection is nil")
	}
	inbound := &model.Inbound{}
	err := db.Model(model.Inbound{}).First(inbound, id).Error
	if err != nil {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] GetInbound: ERROR, inboundId=%d, error=%v, errorType=%T", id, err, err)
		// #endregion
		return nil, err
	}
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] GetInbound: SUCCESS, inboundId=%d, userId=%d", id, inbound.UserId)
	// #endregion
	
	// Enrich with node assignments
	nodeService := NodeService{}
	nodes, err := nodeService.GetNodesForInbound(inbound.Id)
	if err == nil && len(nodes) > 0 {
		nodeIds := make([]int, len(nodes))
		for i, node := range nodes {
			nodeIds[i] = node.Id
		}
		inbound.NodeIds = nodeIds
		// Don't set nodeId - it's deprecated and causes confusion
		// nodeId is only for backward compatibility when receiving data from old clients
	} else {
		// Ensure empty array if no nodes assigned
		inbound.NodeIds = []int{}
	}
	
	return inbound, nil
}

// UpdateInbound modifies an existing inbound configuration.
// It validates changes, updates the database, and syncs with the running Xray instance.
// Returns the updated inbound, whether Xray needs restart, and any error.
func (s *InboundService) UpdateInbound(inbound *model.Inbound) (*model.Inbound, bool, error) {
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] UpdateInbound service: START, inboundId=%d, listen=%s, port=%d, protocol=%s", inbound.Id, inbound.Listen, inbound.Port, inbound.Protocol)
	// #endregion

	// Check port uniqueness only in single-node mode
	// In multi-node mode, same port is allowed (with different SNI), but one node cannot have two inbounds with same port
	settingService := SettingService{}
	multiMode, _ := settingService.GetMultiNodeMode()
	if !multiMode {
		exist, err := s.checkPortExist(inbound.Listen, inbound.Port, inbound.Id)
		if err != nil {
			// #region agent log
			logger.Debugf("[DEBUG-AGENT] UpdateInbound service: checkPortExist error, inboundId=%d, error=%v", inbound.Id, err)
			// #endregion
			return inbound, false, err
		}
		if exist {
			// #region agent log
			logger.Debugf("[DEBUG-AGENT] UpdateInbound service: port already exists, inboundId=%d, port=%d", inbound.Id, inbound.Port)
			// #endregion
			return inbound, false, common.NewError("Port already exists:", inbound.Port)
		}
	}

	oldInbound, err := s.GetInbound(inbound.Id)
	if err != nil {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] UpdateInbound service: GetInbound error, inboundId=%d, error=%v", inbound.Id, err)
		// #endregion
		return inbound, false, err
	}

	// Save original values for change detection (before modifying oldInbound)
	originalOldInbound := *oldInbound
	tag := oldInbound.Tag

	db := database.GetDB()
	
	// updateClientTraffics is no longer needed - clients are managed through ClientEntity
	// Settings JSON is generated from ClientEntity via BuildSettingsFromClientEntities
	// No need to sync client_traffics as traffic is stored directly in ClientEntity

	// Ensure created_at and updated_at exist in inbound.Settings clients
	{
		var oldSettings map[string]any
		_ = json.Unmarshal([]byte(oldInbound.Settings), &oldSettings)
		emailToCreated := map[string]int64{}
		emailToUpdated := map[string]int64{}
		if oldSettings != nil {
			if oc, ok := oldSettings["clients"].([]any); ok {
				for _, it := range oc {
					if m, ok2 := it.(map[string]any); ok2 {
						if email, ok3 := m["email"].(string); ok3 {
							switch v := m["created_at"].(type) {
							case float64:
								emailToCreated[email] = int64(v)
							case int64:
								emailToCreated[email] = v
							}
							switch v := m["updated_at"].(type) {
							case float64:
								emailToUpdated[email] = int64(v)
							case int64:
								emailToUpdated[email] = v
							}
						}
					}
				}
			}
		}
		var newSettings map[string]any
		if err2 := json.Unmarshal([]byte(inbound.Settings), &newSettings); err2 == nil && newSettings != nil {
			now := time.Now().Unix() * 1000
			if nSlice, ok := newSettings["clients"].([]any); ok {
				for i := range nSlice {
					if m, ok2 := nSlice[i].(map[string]any); ok2 {
						email, _ := m["email"].(string)
						if _, ok3 := m["created_at"]; !ok3 {
							if v, ok4 := emailToCreated[email]; ok4 && v > 0 {
								m["created_at"] = v
							} else {
								m["created_at"] = now
							}
						}
						// Preserve client's updated_at if present; do not bump on parent inbound update
						if _, hasUpdated := m["updated_at"]; !hasUpdated {
							if v, ok4 := emailToUpdated[email]; ok4 && v > 0 {
								m["updated_at"] = v
							}
						}
						nSlice[i] = m
					}
				}
				newSettings["clients"] = nSlice
				if bs, err3 := json.MarshalIndent(newSettings, "", "  "); err3 == nil {
					inbound.Settings = string(bs)
				}
			}
		}
	}

	oldInbound.Up = inbound.Up
	oldInbound.Down = inbound.Down
	oldInbound.Total = inbound.Total
	oldInbound.Remark = inbound.Remark
	oldInbound.Enable = inbound.Enable
	oldInbound.ExpiryTime = inbound.ExpiryTime
	oldInbound.TrafficReset = inbound.TrafficReset
	oldInbound.Listen = inbound.Listen
	oldInbound.Port = inbound.Port
	oldInbound.Protocol = inbound.Protocol
	oldInbound.Settings = inbound.Settings
	oldInbound.StreamSettings = inbound.StreamSettings
	oldInbound.Sniffing = inbound.Sniffing
	// Generate tag based on mode (multi-node uses ID for uniqueness)
	oldInbound.Tag = s.generateInboundTag(inbound, multiMode)

	needRestart := false

	// Check if only Settings changed (clients list) - if so, we can use fast API update
	// If port/protocol/streamSettings changed, we need full config reload
	onlySettingsChanged := oldInbound.Port == inbound.Port &&
		oldInbound.Protocol == inbound.Protocol &&
		oldInbound.Listen == inbound.Listen &&
		oldInbound.Tag == tag &&
		oldInbound.Settings != inbound.Settings &&
		oldInbound.StreamSettings == inbound.StreamSettings &&
		oldInbound.Sniffing == inbound.Sniffing

	// Check multi-node mode (reuse variables already declared above)
	// settingService and multiMode are already declared at the beginning of the function

	// Use fast API update if:
	// 1. Only Settings changed (clients list), OR
	// 2. In single mode and Xray is running locally
	useFastAPI := onlySettingsChanged || (p != nil && !multiMode)

	if useFastAPI {
		// Fast path: Use API to update inbound (instant, no restart)
		if multiMode {
			// Multi-node mode: update on all nodes assigned to this inbound
			nodeService := NodeService{}
			nodes, err := nodeService.GetNodesForInbound(inbound.Id)
			if err == nil && len(nodes) > 0 && inbound.Enable {
				// Generate new config with updated Settings
				inboundJson, err2 := json.MarshalIndent(oldInbound.GenXrayInboundConfig(), "", "  ")
				if err2 == nil {
					// Update inbound on all nodes via API (async, non-blocking)
					for _, node := range nodes {
						go func(n *model.Node) {
							if err := nodeService.UpdateInboundOnNode(n, inboundJson); err != nil {
								logger.Warningf("Failed to update inbound %s on node %s via API: %v", tag, n.Name, err)
							} else {
								logger.Infof("Updated inbound %s on node %s via API (instant)", tag, n.Name)
							}
						}(node)
					}
					logger.Debugf("UpdateInbound: using fast API update for inbound %s on %d node(s)", tag, len(nodes))
				} else {
					logger.Debugf("Unable to marshal inbound config for API update: %v", err2)
					needRestart = true
				}
			}
		} else if p != nil {
			// Single mode: update local Xray via API
			apiPort := p.GetAPIPort()
			api, err := s.getXrayAPI(apiPort)
			if err == nil {
				// Always delete old inbound first to ensure clean state
				if api.DelInbound(tag) == nil {
					logger.Debug("Old inbound deleted by api:", tag)
				} else {
					logger.Debug("Failed to delete old inbound by api (may not exist):", tag)
					// Continue anyway - inbound might not exist yet
				}

				if inbound.Enable {
					// Generate new config with updated Settings (which excludes disabled clients)
					inboundJson, err2 := json.MarshalIndent(oldInbound.GenXrayInboundConfig(), "", "  ")
					if err2 != nil {
						logger.Debug("Unable to marshal updated inbound config:", err2)
						needRestart = true
					} else {
						// Add new inbound with updated config (disabled clients are already excluded from Settings)
						err2 = api.AddInbound(inboundJson)
						if err2 == nil {
							logger.Debug("Updated inbound added by api:", oldInbound.Tag)
						} else {
							logger.Debug("Unable to update inbound by api:", err2)
							needRestart = true
						}
					}
				} else {
					// Inbound is disabled - it's already deleted, nothing to add
					logger.Debug("Inbound is disabled, not adding to Xray:", tag)
				}
			} else {
				logger.Debug("Failed to get XrayAPI connection:", err)
				needRestart = true
			}
		}
	} else {
		// Slow path: Full config reload needed (port/protocol/streamSettings changed)
		logger.Debugf("UpdateInbound: full config reload needed for inbound %s (port/protocol/streamSettings changed)", tag)
		needRestart = true
	}

	// #region agent log
	logger.Debugf("[DEBUG-AGENT] UpdateInbound: before transaction, inboundId=%d, userId=%d", inbound.Id, oldInbound.UserId)
	// #endregion
	
	err = db.Transaction(func(tx *gorm.DB) error {
		saveErr := tx.Save(oldInbound).Error
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] UpdateInbound: Save result, inboundId=%d, error=%v", inbound.Id, saveErr)
		// #endregion
		return saveErr
	})
	
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] UpdateInbound: after transaction, inboundId=%d, error=%v", inbound.Id, err)
	// #endregion
	
	if err == nil {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] UpdateInbound service: DB transaction success, inboundId=%d, needRestart=%v", inbound.Id, needRestart)
		// #endregion
		// Invalidate cache for this user's inbounds
		if oldInbound.UserId > 0 {
			// #region agent log
			logger.Debugf("[DEBUG-AGENT] UpdateInbound: before cache invalidation, userId=%d", oldInbound.UserId)
			// #endregion
			cacheErr := cache.InvalidateInbounds(oldInbound.UserId)
			// #region agent log
			logger.Debugf("[DEBUG-AGENT] UpdateInbound: after cache invalidation, userId=%d, error=%v", oldInbound.UserId, cacheErr)
			// #endregion
		}
	} else {
		// #region agent log
		logger.Debugf("[DEBUG-AGENT] UpdateInbound service: DB transaction FAILED, inboundId=%d, error=%v", inbound.Id, err)
		// #endregion
	}

	// Send notification about inbound update (only if update was successful and something actually changed)
	if err == nil {
		// Check if anything actually changed (not just Settings which can change due to client updates)
		// Use originalOldInbound for comparison (before oldInbound was modified)
		hasRealChanges := originalOldInbound.Remark != inbound.Remark ||
			originalOldInbound.Port != inbound.Port ||
			originalOldInbound.Protocol != inbound.Protocol ||
			originalOldInbound.Enable != inbound.Enable ||
			originalOldInbound.Listen != inbound.Listen ||
			originalOldInbound.StreamSettings != inbound.StreamSettings ||
			originalOldInbound.Sniffing != inbound.Sniffing ||
			originalOldInbound.ExpiryTime != inbound.ExpiryTime ||
			originalOldInbound.TrafficReset != inbound.TrafficReset
		
		if hasRealChanges {
			tgbotService := Tgbot{}
			if tgbotService.IsRunning() {
				// Pass originalOldInbound to notification for accurate change tracking
				tgbotService.NotifyInboundUpdated(inbound, &originalOldInbound)
			}
		}
	}

	return inbound, needRestart, err
}

// updateClientTraffics is removed - clients are now managed through ClientEntity
// Traffic is stored directly in ClientEntity table, no need to sync with client_traffics

func (s *InboundService) AddInboundClient(data *model.Inbound) (bool, error) {
	// Get clients from new data (these are the clients to add)
	clients, err := s.GetClients(data)
	if err != nil {
		return false, err
	}

	if len(clients) == 0 {
		return false, common.NewError("No clients to add")
	}

	// Get inbound to get userId
	oldInbound, err := s.GetInbound(data.Id)
	if err != nil {
		return false, err
	}

	// Validate client IDs
	for _, client := range clients {
		switch oldInbound.Protocol {
		case "trojan":
			if client.Password == "" {
				return false, common.NewError("empty client ID")
			}
		case "shadowsocks":
			if client.Email == "" {
				return false, common.NewError("empty client ID")
			}
		default:
			if client.ID == "" {
				return false, common.NewError("empty client ID")
			}
		}
	}

	// Note: Email uniqueness is checked by ClientService.AddClient per user
	// No need to check globally here as clients are user-specific

	// Use ClientService to add clients
	clientService := ClientService{}
	needRestart := false

	// Add each client using ClientService
	for _, client := range clients {
		// Convert Client to ClientEntity
		clientEntity := clientService.ConvertClientToEntity(&client, oldInbound.UserId)
		// Set inbound assignment
		clientEntity.InboundIds = []int{data.Id}

		// Add client using ClientService (this handles Settings update automatically)
		clientNeedRestart, err := clientService.AddClient(oldInbound.UserId, clientEntity)
		if err != nil {
			return false, err
		}
		if clientNeedRestart {
			needRestart = true
		}
	}

	return needRestart, nil
}

func (s *InboundService) DelInboundClient(inboundId int, clientId string) (bool, error) {
	// Ensure xrayTemplateConfig is valid before operations that may trigger Xray restart.
	// This is critical when updating only the panel image without updating the database.
	settingService := SettingService{}
	if err := settingService.EnsureXrayTemplateConfigValid(); err != nil {
		logger.Warningf("Failed to ensure xrayTemplateConfig is valid in DelInboundClient: %v", err)
		// Continue anyway; we'll still try to perform the operation.
	}

	// Get inbound to find the client
	oldInbound, err := s.GetInbound(inboundId)
	if err != nil {
		logger.Error("Load Old Data Error")
		return false, err
	}

	// Get all clients for this inbound (from ClientEntity)
	oldClients, err := s.GetClients(oldInbound)
	if err != nil {
		return false, err
	}

	// Find client by clientId (UUID/password/email depending on protocol)
	var targetEmail string
	client_key := "id"
	if oldInbound.Protocol == "trojan" {
		client_key = "password"
	}
	if oldInbound.Protocol == "shadowsocks" {
		client_key = "email"
	}

	for _, client := range oldClients {
		var c_id string
		switch client_key {
		case "password":
			c_id = client.Password
		case "email":
			c_id = client.Email
		default:
			c_id = client.ID
		}
		if c_id == clientId {
			targetEmail = client.Email
			break
		}
	}

	if targetEmail == "" {
		return false, common.NewError("Client not found")
	}

	// Find ClientEntity by email
	clientService := ClientService{}
	clientEntity, err := clientService.GetClientByEmail(oldInbound.UserId, targetEmail)
	if err != nil {
		return false, common.NewError("ClientEntity not found")
	}

	// Check if this is the only client in the inbound
	if len(oldClients) <= 1 {
		return false, common.NewError("no client remained in Inbound")
	}

	// Delete client using ClientService (this handles Settings update automatically)
	needRestart, err := clientService.DeleteClient(oldInbound.UserId, clientEntity.Id)
	if err != nil {
		return false, err
	}

	return needRestart, nil
}

func (s *InboundService) UpdateInboundClient(data *model.Inbound, clientId string) (bool, error) {
	// Ensure xrayTemplateConfig is valid before operations that may trigger Xray restart.
	// This is critical when updating only the panel image without updating the database.
	settingService := SettingService{}
	if err := settingService.EnsureXrayTemplateConfigValid(); err != nil {
		logger.Warningf("Failed to ensure xrayTemplateConfig is valid in UpdateInboundClient: %v", err)
		// Continue anyway; we'll still try to perform the operation.
	}

	// Get new client data
	newClients, err := s.GetClients(data)
	if err != nil {
		return false, err
	}

	if len(newClients) == 0 {
		return false, common.NewError("No client data provided")
	}

	newClient := newClients[0]

	// Get inbound to find the old client
	oldInbound, err := s.GetInbound(data.Id)
	if err != nil {
		return false, err
	}

	// Get all clients for this inbound (from ClientEntity)
	oldClients, err := s.GetClients(oldInbound)
	if err != nil {
		return false, err
	}

	// Find old client by clientId (UUID/password/email depending on protocol)
	var oldEmail string
	client_key := "id"
	if oldInbound.Protocol == "trojan" {
		client_key = "password"
	}
	if oldInbound.Protocol == "shadowsocks" {
		client_key = "email"
	}

	for _, oldClient := range oldClients {
		var oldClientId string
		switch client_key {
		case "password":
			oldClientId = oldClient.Password
		case "email":
			oldClientId = oldClient.Email
		default:
			oldClientId = oldClient.ID
		}
		if clientId == oldClientId {
			oldEmail = oldClient.Email
			break
		}
	}

	if oldEmail == "" {
		return false, common.NewError("Client not found")
	}

	// Check for duplicate email if email changed
	if newClient.Email != "" && strings.ToLower(newClient.Email) != strings.ToLower(oldEmail) {
		existEmail, err := s.checkEmailsExistForClients(newClients)
		if err != nil {
			return false, err
		}
		if existEmail != "" {
			return false, common.NewError("Duplicate email:", existEmail)
		}
	}

	// Find ClientEntity by old email
	clientService := ClientService{}
	clientEntity, err := clientService.GetClientByEmail(oldInbound.UserId, oldEmail)
	if err != nil {
		return false, common.NewError("ClientEntity not found")
	}

	// Convert new Client to ClientEntity and update
	updatedEntity := clientService.ConvertClientToEntity(&newClient, oldInbound.UserId)
	updatedEntity.Id = clientEntity.Id
	// Preserve created_at
	updatedEntity.CreatedAt = clientEntity.CreatedAt
	// Preserve inbound assignments
	updatedEntity.InboundIds = clientEntity.InboundIds

	// Update client using ClientService (this handles Settings update automatically)
	needRestart, err := clientService.UpdateClient(oldInbound.UserId, updatedEntity)
	if err != nil {
		return false, err
	}

	return needRestart, nil
}

func (s *InboundService) AddTraffic(inboundTraffics []*xray.Traffic, clientTraffics []*xray.ClientTraffic) (error, bool) {
	var err error
	db := database.GetDB()
	tx := db.Begin()

	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()
	// Client traffic is now handled by ClientService
	// Inbound traffic will be synchronized as sum of all its clients' traffic
	clientService := ClientService{}
	clientsToDisable, _, err := clientService.AddClientTraffic(tx, clientTraffics, s)
	if err != nil {
		return err, false
	}
	
	// Note: We no longer update inbound traffic directly from Xray API
	// Instead, inbound traffic is synchronized as sum of all its clients' traffic in AddClientTraffic
	// This ensures consistency between inbound and client traffic

	needRestart0, count, err := s.autoRenewClients(tx)
	if err != nil {
		logger.Warning("Error in renew clients:", err)
	} else if count > 0 {
		logger.Debugf("%v clients renewed", count)
	}

	// NOTE: disableInvalidClients is no longer needed - client disabling is handled by ClientService.AddClientTraffic
	// which updates ClientEntity.Enable and client_traffics.enable, and then DisableClientsByEmail handles Xray API removal
	// and Settings update. This ensures proper separation: clients are managed individually, not as part of inbound.
	
	// NOTE: disableInvalidInbounds is disabled - inbound should NOT be blocked by traffic limits.
	// Inbound is only a container for clients and should show statistics (sum of all clients' traffic).
	// Traffic limits are managed at the client level only.
	// If inbound needs to be disabled, it should be done manually via Enable flag, not automatically by traffic.
	needRestart1 := false
	needRestart2 := false
	
	// Disable clients in new architecture (ClientEntity) after transaction commits
	// This is done outside the transaction to avoid nested transactions
	// The client_traffics.enable has already been updated in addClientTraffic
	// Now we need to sync ClientEntity.Enable and remove from Xray API
	// IMPORTANT: Only process if we have clients to disable AND transaction was successful
	if len(clientsToDisable) > 0 && err == nil {
		logger.Debugf("AddTraffic: %d clients need to be disabled: %v", len(clientsToDisable), clientsToDisable)
		// Run in goroutine to avoid blocking traffic updates
		go func() {
			clientService := ClientService{}
			needRestart3, err := clientService.DisableClientsByEmail(clientsToDisable, s)
			if err != nil {
				logger.Warning("Error in disabling clients in new architecture:", err)
			} else if needRestart3 {
				// Restart Xray if needed (e.g., if API removal failed)
				xrayService := XrayService{
					inboundService: *s,
					settingService: SettingService{},
					nodeService:    NodeService{},
				}
				if err := xrayService.RestartXray(false); err != nil {
					logger.Warningf("Failed to restart Xray after client removal: %v", err)
				} else {
					logger.Infof("Xray restarted after client removal")
				}
			}
		}()
	} else if len(clientsToDisable) > 0 {
		logger.Debugf("AddTraffic: %d clients to disable but transaction failed, skipping", len(clientsToDisable))
	}
	
	return nil, (needRestart0 || needRestart1 || needRestart2)
}

func (s *InboundService) addInboundTraffic(tx *gorm.DB, traffics []*xray.Traffic) error {
	if len(traffics) == 0 {
		return nil
	}

	var err error

	for _, traffic := range traffics {
		if traffic.IsInbound {
			err = tx.Model(&model.Inbound{}).Where("tag = ?", traffic.Tag).
				Updates(map[string]any{
					"up":       gorm.Expr("up + ?", traffic.Up),
					"down":     gorm.Expr("down + ?", traffic.Down),
					"all_time": gorm.Expr("COALESCE(all_time, 0) + ?", traffic.Up+traffic.Down),
				}).Error
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// addClientTraffic is removed - now using ClientService.AddClientTraffic
// Traffic is managed through ClientEntity, not client_traffics table

func (s *InboundService) adjustTraffics(tx *gorm.DB, dbClientTraffics []*xray.ClientTraffic) ([]*xray.ClientTraffic, error) {
	inboundIds := make([]int, 0, len(dbClientTraffics))
	for _, dbClientTraffic := range dbClientTraffics {
		if dbClientTraffic.ExpiryTime < 0 {
			inboundIds = append(inboundIds, dbClientTraffic.InboundId)
		}
	}

	if len(inboundIds) > 0 {
		var inbounds []*model.Inbound
		err := tx.Model(model.Inbound{}).Where("id IN (?)", inboundIds).Find(&inbounds).Error
		if err != nil {
			return nil, err
		}
		for inbound_index := range inbounds {
			settings := map[string]any{}
			json.Unmarshal([]byte(inbounds[inbound_index].Settings), &settings)
			clients, ok := settings["clients"].([]any)
			if ok {
				var newClients []any
				for client_index := range clients {
					c := clients[client_index].(map[string]any)
					for traffic_index := range dbClientTraffics {
						if dbClientTraffics[traffic_index].ExpiryTime < 0 && c["email"] == dbClientTraffics[traffic_index].Email {
							oldExpiryTime := c["expiryTime"].(float64)
							newExpiryTime := (time.Now().Unix() * 1000) - int64(oldExpiryTime)
							c["expiryTime"] = newExpiryTime
							c["updated_at"] = time.Now().Unix() * 1000
							dbClientTraffics[traffic_index].ExpiryTime = newExpiryTime
							break
						}
					}
					// Backfill created_at and updated_at
					if _, ok := c["created_at"]; !ok {
						c["created_at"] = time.Now().Unix() * 1000
					}
					c["updated_at"] = time.Now().Unix() * 1000
					newClients = append(newClients, any(c))
				}
				settings["clients"] = newClients
				modifiedSettings, err := json.MarshalIndent(settings, "", "  ")
				if err != nil {
					return nil, err
				}

				inbounds[inbound_index].Settings = string(modifiedSettings)
			}
		}
		err = tx.Save(inbounds).Error
		if err != nil {
			logger.Warning("AddClientTraffic update inbounds ", err)
			logger.Error(inbounds)
		}
	}

	return dbClientTraffics, nil
}

func (s *InboundService) autoRenewClients(tx *gorm.DB) (bool, int64, error) {
	// check for time expired
	var traffics []*xray.ClientTraffic
	now := time.Now().Unix() * 1000
	var err, err1 error

	err = tx.Model(xray.ClientTraffic{}).Where("reset > 0 and expiry_time > 0 and expiry_time <= ?", now).Find(&traffics).Error
	if err != nil {
		return false, 0, err
	}
	// return if there is no client to renew
	if len(traffics) == 0 {
		return false, 0, nil
	}

	var inbound_ids []int
	var inbounds []*model.Inbound
	needRestart := false
	var clientsToAdd []struct {
		protocol string
		tag      string
		client   map[string]any
	}

	for _, traffic := range traffics {
		inbound_ids = append(inbound_ids, traffic.InboundId)
	}
	err = tx.Model(model.Inbound{}).Where("id IN ?", inbound_ids).Find(&inbounds).Error
	if err != nil {
		return false, 0, err
	}
	for inbound_index := range inbounds {
		settings := map[string]any{}
		json.Unmarshal([]byte(inbounds[inbound_index].Settings), &settings)
		clients := settings["clients"].([]any)
		for client_index := range clients {
			c := clients[client_index].(map[string]any)
			for traffic_index, traffic := range traffics {
				if traffic.Email == c["email"].(string) {
					newExpiryTime := traffic.ExpiryTime
					for newExpiryTime < now {
						newExpiryTime += (int64(traffic.Reset) * 86400000)
					}
					c["expiryTime"] = newExpiryTime
					traffics[traffic_index].ExpiryTime = newExpiryTime
					traffics[traffic_index].Down = 0
					traffics[traffic_index].Up = 0
					if !traffic.Enable {
						traffics[traffic_index].Enable = true
						clientsToAdd = append(clientsToAdd,
							struct {
								protocol string
								tag      string
								client   map[string]any
							}{
								protocol: string(inbounds[inbound_index].Protocol),
								tag:      inbounds[inbound_index].Tag,
								client:   c,
							})
					}
					clients[client_index] = any(c)
					break
				}
			}
		}
		settings["clients"] = clients
		newSettings, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return false, 0, err
		}
		inbounds[inbound_index].Settings = string(newSettings)
	}
	err = tx.Save(inbounds).Error
	if err != nil {
		return false, 0, err
	}
	err = tx.Save(traffics).Error
	if err != nil {
		return false, 0, err
	}
	if p != nil {
		err1 = s.xrayApi.Init(p.GetAPIPort())
		if err1 != nil {
			return true, int64(len(traffics)), nil
		}
		for _, clientToAdd := range clientsToAdd {
			err1 = s.xrayApi.AddUser(clientToAdd.protocol, clientToAdd.tag, clientToAdd.client)
			if err1 != nil {
				needRestart = true
			}
		}
		s.xrayApi.Close()
	}
	return needRestart, int64(len(traffics)), nil
}

func (s *InboundService) disableInvalidInbounds(tx *gorm.DB) (bool, int64, error) {
	now := time.Now().Unix() * 1000
	needRestart := false

	if p != nil {
		var tags []string
		err := tx.Table("inbounds").
			Select("inbounds.tag").
			Where("((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?)) and enable = ?", now, true).
			Scan(&tags).Error
		if err != nil {
			return false, 0, err
		}
		apiPort := p.GetAPIPort()
		api, err := s.getXrayAPI(apiPort)
		if err == nil {
			for _, tag := range tags {
				err1 := api.DelInbound(tag)
				if err1 == nil {
					logger.Debug("Inbound disabled by api:", tag)
				} else {
					logger.Debug("Error in disabling inbound by api:", err1)
					needRestart = true
				}
			}
		}
	}

	result := tx.Model(model.Inbound{}).
		Where("((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?)) and enable = ?", now, true).
		Update("enable", false)
	err := result.Error
	count := result.RowsAffected
	return needRestart, count, err
}

func (s *InboundService) disableInvalidClients(tx *gorm.DB) (bool, int64, error) {
	now := time.Now().Unix() * 1000
	needRestart := false

	if p != nil {
		var results []struct {
			Tag   string
			Email string
		}

		err := tx.Table("inbounds").
			Select("inbounds.tag, client_traffics.email").
			Joins("JOIN client_traffics ON inbounds.id = client_traffics.inbound_id").
			Where("((client_traffics.total > 0 AND client_traffics.up + client_traffics.down >= client_traffics.total) OR (client_traffics.expiry_time > 0 AND client_traffics.expiry_time <= ?)) AND client_traffics.enable = ?", now, true).
			Scan(&results).Error
		if err != nil {
			return false, 0, err
		}
		apiPort := p.GetAPIPort()
		api, err := s.getXrayAPI(apiPort)
		if err == nil {
			for _, result := range results {
				err1 := api.RemoveUser(result.Tag, result.Email)
				if err1 == nil {
					logger.Debug("Client disabled by api:", result.Email)
				} else {
					if strings.Contains(err1.Error(), fmt.Sprintf("User %s not found.", result.Email)) {
						logger.Debug("User is already disabled. Nothing to do more...")
					} else {
						logger.Debug("Error in disabling client by api:", err1)
						needRestart = true
					}
				}
			}
		}
	}
	result := tx.Model(xray.ClientTraffic{}).
		Where("((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?)) and enable = ?", now, true).
		Update("enable", false)
	err := result.Error
	count := result.RowsAffected
	return needRestart, count, err
}

func (s *InboundService) GetInboundTags() (string, error) {
	db := database.GetDB()
	var inboundTags []string
	err := db.Model(model.Inbound{}).Select("tag").Find(&inboundTags).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return "", err
	}
	tags, _ := json.Marshal(inboundTags)
	return string(tags), nil
}

func (s *InboundService) MigrationRemoveOrphanedTraffics() {
	db := database.GetDB()
	db.Exec(`
		DELETE FROM client_traffics
		WHERE email NOT IN (
			SELECT (client.value::jsonb)->>'email' as email
			FROM inbounds,
				jsonb_array_elements(
					CASE 
						WHEN jsonb_typeof((inbounds.settings::jsonb)->'clients') = 'array' 
						THEN (inbounds.settings::jsonb)->'clients'
						ELSE '[]'::jsonb
					END
				) AS client
		)
	`)
}

// AddClientStat, UpdateClientStat, DelClientStat are removed
// Clients are now managed through ClientEntity - traffic is stored directly in ClientEntity table
// These methods worked with deprecated client_traffics table

func (s *InboundService) UpdateClientIPs(tx *gorm.DB, oldEmail string, newEmail string) error {
	return tx.Model(model.InboundClientIps{}).Where("client_email = ?", oldEmail).Update("client_email", newEmail).Error
}

func (s *InboundService) DelClientIPs(tx *gorm.DB, email string) error {
	return tx.Where("client_email = ?", email).Delete(model.InboundClientIps{}).Error
}

func (s *InboundService) GetClientInboundByTrafficID(trafficId int) (traffic *xray.ClientTraffic, inbound *model.Inbound, err error) {
	db := database.GetDB()
	var traffics []*xray.ClientTraffic
	err = db.Model(xray.ClientTraffic{}).Where("id = ?", trafficId).Find(&traffics).Error
	if err != nil {
		logger.Warningf("Error retrieving ClientTraffic with trafficId %d: %v", trafficId, err)
		return nil, nil, err
	}
	if len(traffics) > 0 {
		inbound, err = s.GetInbound(traffics[0].InboundId)
		return traffics[0], inbound, err
	}
	return nil, nil, nil
}

func (s *InboundService) GetClientInboundByEmail(email string) (traffic *xray.ClientTraffic, inbound *model.Inbound, err error) {
	db := database.GetDB()
	var traffics []*xray.ClientTraffic
	err = db.Model(xray.ClientTraffic{}).Where("email = ?", email).Find(&traffics).Error
	if err != nil {
		// Only log real database errors, not "not found" cases
		logger.Warningf("Database error retrieving ClientTraffic with email %s: %v", email, err)
		return nil, nil, err
	}
	if len(traffics) > 0 {
		inbound, err = s.GetInbound(traffics[0].InboundId)
		return traffics[0], inbound, err
	}
	// Client not found - this is normal, return nil without error
	return nil, nil, nil
}

func (s *InboundService) GetClientByEmail(clientEmail string) (*xray.ClientTraffic, *model.Client, error) {
	traffic, inbound, err := s.GetClientInboundByEmail(clientEmail)
	if err != nil {
		return nil, nil, err
	}
	if inbound == nil {
		return nil, nil, common.NewError("Inbound Not Found For Email:", clientEmail)
	}

	clients, err := s.GetClients(inbound)
	if err != nil {
		return nil, nil, err
	}

	for _, client := range clients {
		if client.Email == clientEmail {
			return traffic, &client, nil
		}
	}

	return nil, nil, common.NewError("Client Not Found In Inbound For Email:", clientEmail)
}

func (s *InboundService) SetClientTelegramUserID(trafficId int, tgId int64) (bool, error) {
	traffic, inbound, err := s.GetClientInboundByTrafficID(trafficId)
	if err != nil {
		return false, err
	}
	if inbound == nil {
		return false, common.NewError("Inbound Not Found For Traffic ID:", trafficId)
	}

	clientEmail := traffic.Email

	oldClients, err := s.GetClients(inbound)
	if err != nil {
		return false, err
	}

	clientId := ""

	for _, oldClient := range oldClients {
		if oldClient.Email == clientEmail {
			switch inbound.Protocol {
			case "trojan":
				clientId = oldClient.Password
			case "shadowsocks":
				clientId = oldClient.Email
			default:
				clientId = oldClient.ID
			}
			break
		}
	}

	if len(clientId) == 0 {
		return false, common.NewError("Client Not Found For Email:", clientEmail)
	}

	var settings map[string]any
	err = json.Unmarshal([]byte(inbound.Settings), &settings)
	if err != nil {
		return false, err
	}
	clients := settings["clients"].([]any)
	var newClients []any
	for client_index := range clients {
		c := clients[client_index].(map[string]any)
		if c["email"] == clientEmail {
			c["tgId"] = tgId
			c["updated_at"] = time.Now().Unix() * 1000
			newClients = append(newClients, any(c))
		}
	}
	settings["clients"] = newClients
	modifiedSettings, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	inbound.Settings = string(modifiedSettings)
	needRestart, err := s.UpdateInboundClient(inbound, clientId)
	return needRestart, err
}

func (s *InboundService) checkIsEnabledByEmail(clientEmail string) (bool, error) {
	_, inbound, err := s.GetClientInboundByEmail(clientEmail)
	if err != nil {
		return false, err
	}
	if inbound == nil {
		return false, common.NewError("Inbound Not Found For Email:", clientEmail)
	}

	clients, err := s.GetClients(inbound)
	if err != nil {
		return false, err
	}

	isEnable := false

	for _, client := range clients {
		if client.Email == clientEmail {
			isEnable = client.Enable
			break
		}
	}

	return isEnable, err
}

func (s *InboundService) ToggleClientEnableByEmail(clientEmail string) (bool, bool, error) {
	_, inbound, err := s.GetClientInboundByEmail(clientEmail)
	if err != nil {
		return false, false, err
	}
	if inbound == nil {
		return false, false, common.NewError("Inbound Not Found For Email:", clientEmail)
	}

	oldClients, err := s.GetClients(inbound)
	if err != nil {
		return false, false, err
	}

	clientId := ""
	clientOldEnabled := false

	for _, oldClient := range oldClients {
		if oldClient.Email == clientEmail {
			switch inbound.Protocol {
			case "trojan":
				clientId = oldClient.Password
			case "shadowsocks":
				clientId = oldClient.Email
			default:
				clientId = oldClient.ID
			}
			clientOldEnabled = oldClient.Enable
			break
		}
	}

	if len(clientId) == 0 {
		return false, false, common.NewError("Client Not Found For Email:", clientEmail)
	}

	var settings map[string]any
	err = json.Unmarshal([]byte(inbound.Settings), &settings)
	if err != nil {
		return false, false, err
	}
	clients := settings["clients"].([]any)
	var newClients []any
	for client_index := range clients {
		c := clients[client_index].(map[string]any)
		if c["email"] == clientEmail {
			c["enable"] = !clientOldEnabled
			c["updated_at"] = time.Now().Unix() * 1000
			newClients = append(newClients, any(c))
		}
	}
	settings["clients"] = newClients
	modifiedSettings, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, false, err
	}
	inbound.Settings = string(modifiedSettings)

	needRestart, err := s.UpdateInboundClient(inbound, clientId)
	if err != nil {
		return false, needRestart, err
	}

	return !clientOldEnabled, needRestart, nil
}

// SetClientEnableByEmail sets client enable state to desired value; returns (changed, needRestart, error)
func (s *InboundService) SetClientEnableByEmail(clientEmail string, enable bool) (bool, bool, error) {
	current, err := s.checkIsEnabledByEmail(clientEmail)
	if err != nil {
		return false, false, err
	}
	if current == enable {
		return false, false, nil
	}
	newEnabled, needRestart, err := s.ToggleClientEnableByEmail(clientEmail)
	if err != nil {
		return false, needRestart, err
	}
	return newEnabled == enable, needRestart, nil
}

func (s *InboundService) ResetClientExpiryTimeByEmail(clientEmail string, expiry_time int64) (bool, error) {
	_, inbound, err := s.GetClientInboundByEmail(clientEmail)
	if err != nil {
		return false, err
	}
	if inbound == nil {
		return false, common.NewError("Inbound Not Found For Email:", clientEmail)
	}

	oldClients, err := s.GetClients(inbound)
	if err != nil {
		return false, err
	}

	clientId := ""

	for _, oldClient := range oldClients {
		if oldClient.Email == clientEmail {
			switch inbound.Protocol {
			case "trojan":
				clientId = oldClient.Password
			case "shadowsocks":
				clientId = oldClient.Email
			default:
				clientId = oldClient.ID
			}
			break
		}
	}

	if len(clientId) == 0 {
		return false, common.NewError("Client Not Found For Email:", clientEmail)
	}

	var settings map[string]any
	err = json.Unmarshal([]byte(inbound.Settings), &settings)
	if err != nil {
		return false, err
	}
	clients := settings["clients"].([]any)
	var newClients []any
	for client_index := range clients {
		c := clients[client_index].(map[string]any)
		if c["email"] == clientEmail {
			c["expiryTime"] = expiry_time
			c["updated_at"] = time.Now().Unix() * 1000
			newClients = append(newClients, any(c))
		}
	}
	settings["clients"] = newClients
	modifiedSettings, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	inbound.Settings = string(modifiedSettings)
	needRestart, err := s.UpdateInboundClient(inbound, clientId)
	return needRestart, err
}

func (s *InboundService) ResetClientTrafficLimitByEmail(clientEmail string, totalGB int) (bool, error) {
	if totalGB < 0 {
		return false, common.NewError("totalGB must be >= 0")
	}
	_, inbound, err := s.GetClientInboundByEmail(clientEmail)
	if err != nil {
		return false, err
	}
	if inbound == nil {
		return false, common.NewError("Inbound Not Found For Email:", clientEmail)
	}

	oldClients, err := s.GetClients(inbound)
	if err != nil {
		return false, err
	}

	clientId := ""

	for _, oldClient := range oldClients {
		if oldClient.Email == clientEmail {
			switch inbound.Protocol {
			case "trojan":
				clientId = oldClient.Password
			case "shadowsocks":
				clientId = oldClient.Email
			default:
				clientId = oldClient.ID
			}
			break
		}
	}

	if len(clientId) == 0 {
		return false, common.NewError("Client Not Found For Email:", clientEmail)
	}

	var settings map[string]any
	err = json.Unmarshal([]byte(inbound.Settings), &settings)
	if err != nil {
		return false, err
	}
	clients := settings["clients"].([]any)
	var newClients []any
	for client_index := range clients {
		c := clients[client_index].(map[string]any)
		if c["email"] == clientEmail {
			c["totalGB"] = totalGB * 1024 * 1024 * 1024
			c["updated_at"] = time.Now().Unix() * 1000
			newClients = append(newClients, any(c))
		}
	}
	settings["clients"] = newClients
	modifiedSettings, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, err
	}
	inbound.Settings = string(modifiedSettings)
	needRestart, err := s.UpdateInboundClient(inbound, clientId)
	return needRestart, err
}

func (s *InboundService) ResetClientTrafficByEmail(clientEmail string) error {
	db := database.GetDB()

	// Reset traffic stats in ClientTraffic table
	result := db.Model(xray.ClientTraffic{}).
		Where("email = ?", clientEmail).
		Updates(map[string]any{"enable": true, "up": 0, "down": 0})

	err := result.Error
	if err != nil {
		return err
	}

	return nil
}

func (s *InboundService) ResetClientTraffic(id int, clientEmail string) (bool, error) {
	needRestart := false

	traffic, err := s.GetClientTrafficByEmail(clientEmail)
	if err != nil {
		return false, err
	}

	if !traffic.Enable {
		inbound, err := s.GetInbound(id)
		if err != nil {
			return false, err
		}
		clients, err := s.GetClients(inbound)
		if err != nil {
			return false, err
		}
		for _, client := range clients {
			if client.Email == clientEmail && client.Enable {
				if p != nil {
					apiPort := p.GetAPIPort()
					api, err := s.getXrayAPI(apiPort)
					if err == nil {
						cipher := ""
						if string(inbound.Protocol) == "shadowsocks" {
							var oldSettings map[string]any
							err = json.Unmarshal([]byte(inbound.Settings), &oldSettings)
							if err != nil {
								return false, err
							}
							cipher = oldSettings["method"].(string)
						}
						err1 := api.AddUser(string(inbound.Protocol), inbound.Tag, map[string]any{
							"email":    client.Email,
							"id":       client.ID,
							"security": client.Security,
							"flow":     client.Flow,
							"password": client.Password,
							"cipher":   cipher,
						})
						if err1 == nil {
							logger.Debug("Client enabled due to reset traffic:", clientEmail)
						} else {
							logger.Debug("Error in enabling client by api:", err1)
							needRestart = true
						}
					}
				} else {
					logger.Debugf("[DEBUG-AGENT] ResetClientTraffic: Xray process is nil, skipping live AddUser; client will be enabled on next restart, email=%s", clientEmail)
				}
				break
			}
		}
	}

	traffic.Up = 0
	traffic.Down = 0
	traffic.Enable = true

	db := database.GetDB()
	err = db.Save(traffic).Error
	if err != nil {
		return false, err
	}

	return needRestart, nil
}

func (s *InboundService) ResetAllClientTraffics(id int) error {
	db := database.GetDB()
	now := time.Now().Unix() * 1000

	return db.Transaction(func(tx *gorm.DB) error {
		whereText := "inbound_id "
		if id == -1 {
			whereText += " > ?"
		} else {
			whereText += " = ?"
		}

		// Reset client traffics
		result := tx.Model(xray.ClientTraffic{}).
			Where(whereText, id).
			Updates(map[string]any{"enable": true, "up": 0, "down": 0})

		if result.Error != nil {
			return result.Error
		}

		// Update lastTrafficResetTime for the inbound(s)
		inboundWhereText := "id "
		if id == -1 {
			inboundWhereText += " > ?"
		} else {
			inboundWhereText += " = ?"
		}

		result = tx.Model(model.Inbound{}).
			Where(inboundWhereText, id).
			Update("last_traffic_reset_time", now)

		return result.Error
	})
}

func (s *InboundService) ResetAllTraffics() error {
	db := database.GetDB()

	result := db.Model(model.Inbound{}).
		Where("user_id > ?", 0).
		Updates(map[string]any{"up": 0, "down": 0})

	err := result.Error
	return err
}

func (s *InboundService) DelDepletedClients(id int) (err error) {
	db := database.GetDB()

	whereText := "reset = 0 and inbound_id "
	if id < 0 {
		whereText += "> ?"
	} else {
		whereText += "= ?"
	}

	// Only consider truly depleted clients: expired OR traffic exhausted
	now := time.Now().Unix() * 1000
	depletedClients := []xray.ClientTraffic{}
	err = db.Model(xray.ClientTraffic{}).
		Where(whereText+" and ((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?))", id, now).
		Select("inbound_id, GROUP_CONCAT(email) as email").
		Group("inbound_id").
		Find(&depletedClients).Error
	if err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, depletedClient := range depletedClients {
			emails := strings.Split(depletedClient.Email, ",")
			oldInbound, err := s.GetInbound(depletedClient.InboundId)
			if err != nil {
				return err
			}
			var oldSettings map[string]any
			err = json.Unmarshal([]byte(oldInbound.Settings), &oldSettings)
			if err != nil {
				return err
			}

			oldClients := oldSettings["clients"].([]any)
			var newClients []any
			for _, client := range oldClients {
				deplete := false
				c := client.(map[string]any)
				for _, email := range emails {
					if email == c["email"].(string) {
						deplete = true
						break
					}
				}
				if !deplete {
					newClients = append(newClients, client)
				}
			}
			if len(newClients) > 0 {
				oldSettings["clients"] = newClients

				newSettings, err := json.MarshalIndent(oldSettings, "", "  ")
				if err != nil {
					return err
				}

				oldInbound.Settings = string(newSettings)
				err = tx.Save(oldInbound).Error
				if err != nil {
					return err
				}
			} else {
				// Delete inbound if no client remains
				s.DelInbound(depletedClient.InboundId)
			}
		}

		// Delete stats only for truly depleted clients
		return tx.Where(whereText+" and ((total > 0 and up + down >= total) or (expiry_time > 0 and expiry_time <= ?))", id, now).Delete(xray.ClientTraffic{}).Error
	})
}

func (s *InboundService) GetClientTrafficTgBot(tgId int64) ([]*xray.ClientTraffic, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound

	// Retrieve inbounds where settings contain the given tgId
	err := db.Model(model.Inbound{}).Where("settings LIKE ?", fmt.Sprintf(`%%"tgId": %d%%`, tgId)).Find(&inbounds).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		logger.Errorf("Error retrieving inbounds with tgId %d: %v", tgId, err)
		return nil, err
	}

	var emails []string
	for _, inbound := range inbounds {
		clients, err := s.GetClients(inbound)
		if err != nil {
			logger.Errorf("Error retrieving clients for inbound %d: %v", inbound.Id, err)
			continue
		}
		for _, client := range clients {
			if client.TgID == tgId {
				emails = append(emails, client.Email)
			}
		}
	}

	var traffics []*xray.ClientTraffic
	err = db.Model(xray.ClientTraffic{}).Where("email IN ?", emails).Find(&traffics).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			logger.Warning("No ClientTraffic records found for emails:", emails)
			return nil, nil
		}
		logger.Errorf("Error retrieving ClientTraffic for emails %v: %v", emails, err)
		return nil, err
	}

	// Populate UUID and other client data for each traffic record
	for i := range traffics {
		if ct, client, e := s.GetClientByEmail(traffics[i].Email); e == nil && ct != nil && client != nil {
			traffics[i].Enable = client.Enable
			traffics[i].UUID = client.ID
			traffics[i].SubId = client.SubID
		}
	}

	return traffics, nil
}

func (s *InboundService) GetClientTrafficByEmail(email string) (traffic *xray.ClientTraffic, err error) {
	// Prefer retrieving along with client to reflect actual enabled state from inbound settings
	t, client, err := s.GetClientByEmail(email)
	if err != nil {
		// Check if it's a "not found" error (normal case) vs real error
		errMsg := err.Error()
		if strings.Contains(errMsg, "Not Found") {
			// Client not found - this is normal, log as debug instead of warning
			logger.Debugf("Client not found for email %s: %v", email, err)
		} else {
			// Real error (database error, etc.) - log as warning
			logger.Warningf("Error retrieving ClientTraffic with email %s: %v", email, err)
		}
		return nil, err
	}
	if t != nil && client != nil {
		t.Enable = client.Enable
		t.UUID = client.ID
		t.SubId = client.SubID
		return t, nil
	}
	// Client not found - return nil without error (normal case)
	return nil, nil
}

func (s *InboundService) UpdateClientTrafficByEmail(email string, upload int64, download int64) error {
	db := database.GetDB()

	result := db.Model(xray.ClientTraffic{}).
		Where("email = ?", email).
		Updates(map[string]any{"up": upload, "down": download})

	err := result.Error
	if err != nil {
		logger.Warningf("Error updating ClientTraffic with email %s: %v", email, err)
		return err
	}
	return nil
}

func (s *InboundService) GetClientTrafficByID(id string) ([]xray.ClientTraffic, error) {
	db := database.GetDB()
	var traffics []xray.ClientTraffic

	err := db.Model(xray.ClientTraffic{}).Where(`email IN(
		SELECT (client.value::jsonb)->>'email' as email
		FROM inbounds,
	  	jsonb_array_elements(
			CASE 
				WHEN jsonb_typeof((inbounds.settings::jsonb)->'clients') = 'array' 
				THEN (inbounds.settings::jsonb)->'clients'
				ELSE '[]'::jsonb
			END
		) AS client
		WHERE
	  	(client.value::jsonb)->>'id' in (?)
		)`, id).Find(&traffics).Error

	if err != nil {
		logger.Debug(err)
		return nil, err
	}
	// Reconcile enable flag with client settings per email to avoid stale DB value
	for i := range traffics {
		if ct, client, e := s.GetClientByEmail(traffics[i].Email); e == nil && ct != nil && client != nil {
			traffics[i].Enable = client.Enable
			traffics[i].UUID = client.ID
			traffics[i].SubId = client.SubID
		}
	}
	return traffics, err
}

func (s *InboundService) SearchClientTraffic(query string) (traffic *xray.ClientTraffic, err error) {
	db := database.GetDB()
	inbound := &model.Inbound{}
	traffic = &xray.ClientTraffic{}

	// Search for inbound settings that contain the query
	err = db.Model(model.Inbound{}).Where("settings LIKE ?", "%\""+query+"\"%").First(inbound).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			logger.Warningf("Inbound settings containing query %s not found: %v", query, err)
			return nil, err
		}
		logger.Errorf("Error searching for inbound settings with query %s: %v", query, err)
		return nil, err
	}

	traffic.InboundId = inbound.Id

	// Unmarshal settings to get clients
	settings := map[string][]model.Client{}
	if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
		logger.Errorf("Error unmarshalling inbound settings for inbound ID %d: %v", inbound.Id, err)
		return nil, err
	}

	clients := settings["clients"]
	for _, client := range clients {
		if (client.ID == query || client.Password == query) && client.Email != "" {
			traffic.Email = client.Email
			break
		}
	}

	if traffic.Email == "" {
		logger.Warningf("No client found with query %s in inbound ID %d", query, inbound.Id)
		return nil, gorm.ErrRecordNotFound
	}

	// Retrieve ClientTraffic based on the found email
	err = db.Model(xray.ClientTraffic{}).Where("email = ?", traffic.Email).First(traffic).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			logger.Warningf("ClientTraffic for email %s not found: %v", traffic.Email, err)
			return nil, err
		}
		logger.Errorf("Error retrieving ClientTraffic for email %s: %v", traffic.Email, err)
		return nil, err
	}

	return traffic, nil
}

func (s *InboundService) GetInboundClientIps(clientEmail string) (string, error) {
	db := database.GetDB()
	InboundClientIps := &model.InboundClientIps{}
	err := db.Model(model.InboundClientIps{}).Where("client_email = ?", clientEmail).First(InboundClientIps).Error
	if err != nil {
		return "", err
	}
	return InboundClientIps.Ips, nil
}

func (s *InboundService) ClearClientIps(clientEmail string) error {
	db := database.GetDB()

	result := db.Model(model.InboundClientIps{}).
		Where("client_email = ?", clientEmail).
		Update("ips", "")
	err := result.Error
	if err != nil {
		return err
	}
	return nil
}

func (s *InboundService) SearchInbounds(query string) ([]*model.Inbound, error) {
	db := database.GetDB()
	var inbounds []*model.Inbound
	err := db.Model(model.Inbound{}).Preload("ClientStats").Where("remark like ?", "%"+query+"%").Find(&inbounds).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	return inbounds, nil
}

func (s *InboundService) MigrationRequirements() {
	db := database.GetDB()
	
	err := db.Transaction(func(tx *gorm.DB) error {
		// Calculate and backfill all_time from up+down for inbounds and clients
		if err := tx.Exec(`
			UPDATE inbounds
			SET all_time = COALESCE(up, 0) + COALESCE(down, 0)
			WHERE COALESCE(all_time, 0) = 0 AND (COALESCE(up, 0) + COALESCE(down, 0)) > 0
		`).Error; err != nil {
			return err
		}
		
		if err := tx.Exec(`
			UPDATE client_traffics
			SET all_time = COALESCE(up, 0) + COALESCE(down, 0)
			WHERE COALESCE(all_time, 0) = 0 AND (COALESCE(up, 0) + COALESCE(down, 0)) > 0
		`).Error; err != nil {
			return err
		}

		// Fix inbounds based problems
		var inbounds []*model.Inbound
		if err := tx.Model(model.Inbound{}).Where("protocol IN (?)", []string{"vmess", "vless", "trojan"}).Find(&inbounds).Error; err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
	for inbound_index := range inbounds {
		settings := map[string]any{}
		json.Unmarshal([]byte(inbounds[inbound_index].Settings), &settings)
		clients, ok := settings["clients"].([]any)
		if ok {
			// Fix Client configuration problems
			var newClients []any
			for client_index := range clients {
				c := clients[client_index].(map[string]any)

				// Add email='' if it is not exists
				if _, ok := c["email"]; !ok {
					c["email"] = ""
				}

				// Convert string tgId to int64
				if _, ok := c["tgId"]; ok {
					var tgId any = c["tgId"]
					if tgIdStr, ok2 := tgId.(string); ok2 {
						tgIdInt64, err := strconv.ParseInt(strings.ReplaceAll(tgIdStr, " ", ""), 10, 64)
						if err == nil {
							c["tgId"] = tgIdInt64
						}
					}
				}

				// Remove "flow": "xtls-rprx-direct"
				if _, ok := c["flow"]; ok {
					if c["flow"] == "xtls-rprx-direct" {
						c["flow"] = ""
					}
				}
				// Backfill created_at and updated_at
				if _, ok := c["created_at"]; !ok {
					c["created_at"] = time.Now().Unix() * 1000
				}
				c["updated_at"] = time.Now().Unix() * 1000
				newClients = append(newClients, any(c))
			}
			settings["clients"] = newClients
			modifiedSettings, err := json.MarshalIndent(settings, "", "  ")
			if err != nil {
				return err
			}

			inbounds[inbound_index].Settings = string(modifiedSettings)
		}

		// Note: Client traffic is now stored in ClientEntity table
		// No need to create client_traffics records - they are deprecated
		}
		
		if err := tx.Save(inbounds).Error; err != nil {
			return err
		}

		// Remove orphaned traffics
		if err := tx.Where("inbound_id = 0").Delete(xray.ClientTraffic{}).Error; err != nil {
			return err
		}

		// Migrate old MultiDomain to External Proxy
		var externalProxy []struct {
			Id             int
			Port           int
			StreamSettings []byte
		}
		if err := tx.Raw(`select id, port, stream_settings
		from inbounds
		WHERE protocol in ('vmess','vless','trojan')
		  AND (stream_settings::jsonb)->>'security' = 'tls'
		  AND (stream_settings::jsonb)->'tlsSettings'->'settings'->'domains' IS NOT NULL`).Scan(&externalProxy).Error; err != nil {
			return err
		}

		if len(externalProxy) > 0 {
			for _, ep := range externalProxy {
				var reverses any
				var stream map[string]any
				if err := json.Unmarshal(ep.StreamSettings, &stream); err != nil {
					continue
				}
				if tlsSettings, ok := stream["tlsSettings"].(map[string]any); ok {
					if settings, ok := tlsSettings["settings"].(map[string]any); ok {
						if domains, ok := settings["domains"].([]any); ok {
							for _, domain := range domains {
								if domainMap, ok := domain.(map[string]any); ok {
									domainMap["forceTls"] = "same"
									domainMap["port"] = ep.Port
									if domainStr, ok := domainMap["domain"].(string); ok {
										domainMap["dest"] = domainStr
									}
									delete(domainMap, "domain")
								}
							}
						}
						reverses = settings["domains"]
						delete(settings, "domains")
					}
				}
				stream["externalProxy"] = reverses
				newStream, _ := json.MarshalIndent(stream, " ", "  ")
				if err := tx.Model(model.Inbound{}).Where("id = ?", ep.Id).Update("stream_settings", newStream).Error; err != nil {
					return err
				}
			}
		}

		// Note: PostgreSQL doesn't have INSTR function, use POSITION instead
		// But this migration might be for SQLite compatibility, so we'll skip it for PostgreSQL
		// If needed, use: WHERE POSITION('0.0.0.0:' IN tag) > 0
		
		return nil
	})
	
	if err != nil {
		logger.Warning("MigrationRequirements error:", err)
	}
}

func (s *InboundService) MigrateDB() {
	s.MigrationRequirements()
	s.MigrationRemoveOrphanedTraffics()
}

func (s *InboundService) GetOnlineClients() []string {
	if p == nil {
		return []string{}
	}
	return p.GetOnlineClients()
}

func (s *InboundService) GetClientsLastOnline() (map[string]int64, error) {
	db := database.GetDB()
	var rows []xray.ClientTraffic
	err := db.Model(&xray.ClientTraffic{}).Select("email, last_online").Find(&rows).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}
	result := make(map[string]int64, len(rows))
	for _, r := range rows {
		result[r.Email] = r.LastOnline
	}
	return result, nil
}

func (s *InboundService) FilterAndSortClientEmails(emails []string) ([]string, []string, error) {
	db := database.GetDB()

	// Step 1: Get ClientTraffic records for emails in the input list
	var clients []xray.ClientTraffic
	err := db.Where("email IN ?", emails).Find(&clients).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, nil, err
	}

	// Step 2: Sort clients by (Up + Down) descending
	sort.Slice(clients, func(i, j int) bool {
		return (clients[i].Up + clients[i].Down) > (clients[j].Up + clients[j].Down)
	})

	// Step 3: Extract sorted valid emails and track found ones
	validEmails := make([]string, 0, len(clients))
	found := make(map[string]bool)
	for _, client := range clients {
		validEmails = append(validEmails, client.Email)
		found[client.Email] = true
	}

	// Step 4: Identify emails that were not found in the database
	extraEmails := make([]string, 0)
	for _, email := range emails {
		if !found[email] {
			extraEmails = append(extraEmails, email)
		}
	}

	return validEmails, extraEmails, nil
}
func (s *InboundService) DelInboundClientByEmail(inboundId int, email string) (bool, error) {
	// Get inbound to get userId
	oldInbound, err := s.GetInbound(inboundId)
	if err != nil {
		logger.Error("Load Old Data Error")
		return false, err
	}

	// Get all clients for this inbound (from ClientEntity)
	oldClients, err := s.GetClients(oldInbound)
	if err != nil {
		return false, err
	}

	// Check if client exists
	found := false
	for _, client := range oldClients {
		if strings.ToLower(client.Email) == strings.ToLower(email) {
			found = true
			break
		}
	}

	if !found {
		return false, common.NewError(fmt.Sprintf("client with email %s not found", email))
	}

	// Check if this is the only client in the inbound
	if len(oldClients) <= 1 {
		return false, common.NewError("no client remained in Inbound")
	}

	// Find ClientEntity by email
	clientService := ClientService{}
	clientEntity, err := clientService.GetClientByEmail(oldInbound.UserId, email)
	if err != nil {
		return false, common.NewError("ClientEntity not found")
	}

	// Delete client using ClientService (this handles Settings update automatically)
	needRestart, err := clientService.DeleteClient(oldInbound.UserId, clientEntity.Id)
	if err != nil {
		return false, err
	}

	return needRestart, nil
}
