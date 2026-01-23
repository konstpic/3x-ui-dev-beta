// Package service provides Client management service.
package service

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/common"
	"github.com/mhsanaei/3x-ui/v2/util/random"
	"github.com/mhsanaei/3x-ui/v2/web/cache"
	"github.com/mhsanaei/3x-ui/v2/xray"

	"gorm.io/gorm"
)

// ClientService provides business logic for managing clients.
type ClientService struct{}

// GetClients retrieves all clients for a specific user.
// Also loads traffic statistics and last online time for each client.
// NOTE: No caching - data should be real-time (traffic, HWID, online status change frequently).
func (s *ClientService) GetClients(userId int) ([]*model.ClientEntity, error) {
	db := database.GetDB()
	var clients []*model.ClientEntity
	err := db.Where("user_id = ?", userId).Find(&clients).Error
	if err != nil {
		return nil, err
	}

	// Load inbound assignments, traffic statistics, and HWIDs for each client
	for _, client := range clients {
		// Load inbound assignments
		inboundIds, err := s.GetInboundIdsForClient(client.Id)
		if err == nil {
			client.InboundIds = inboundIds
		}

		// Traffic statistics are now stored directly in ClientEntity table
		// No need to load from client_traffics - fields are already loaded from DB
		
		// Check if client exceeded limits and update status if needed (but keep Enable = true)
		now := time.Now().Unix() * 1000
		totalUsed := client.Up + client.Down
		trafficLimit := int64(client.TotalGB * 1024 * 1024 * 1024)
		trafficExceeded := client.TotalGB > 0 && totalUsed >= trafficLimit
		timeExpired := client.ExpiryTime > 0 && client.ExpiryTime <= now
		
		// Update status if expired, but don't change Enable
		if trafficExceeded || timeExpired {
			status := "expired_traffic"
			if timeExpired {
				status = "expired_time"
			}
			// Only update if status changed
			if client.Status != status {
				client.Status = status
				err = db.Model(&model.ClientEntity{}).Where("id = ?", client.Id).Update("status", status).Error
				if err != nil {
					logger.Warningf("Failed to update status for client %s: %v", client.Email, err)
				}
				// Remove expired client from Xray API if it's enabled and just expired (both local and nodes)
				if client.Enable {
					settingService := SettingService{}
					multiMode, _ := settingService.GetMultiNodeMode()
					nodeService := NodeService{}
					inboundService := InboundService{}
					
					// Get all inbound IDs for this client
					clientInboundIds, err := s.GetInboundIdsForClient(client.Id)
					if err == nil {
						for _, inboundId := range clientInboundIds {
							inbound, err := inboundService.GetInbound(inboundId)
							if err != nil {
								continue
							}
							
							if multiMode {
								// Multi-node mode: remove from all nodes assigned to this inbound
								nodes, err := nodeService.GetNodesForInbound(inboundId)
								if err == nil {
									for _, node := range nodes {
										go func(n *model.Node) {
											if err := nodeService.RemoveUserFromNode(n, inbound.Tag, client.Email); err != nil {
												logger.Warningf("GetClients: failed to remove expired client %s from node %s via API: %v", client.Email, n.Name, err)
											} else {
												logger.Infof("GetClients: removed expired client %s from node %s via API (instant)", client.Email, n.Name)
											}
										}(node)
									}
								}
							} else {
								// Single mode: instantly update config.json and restart
								if p != nil && p.IsRunning() {
									processConfig := p.GetConfig()
									if processConfig != nil {
										// Instantly remove client from config.json
										if err := xray.UpdateConfigFileAfterUserRemoval(processConfig, inbound.Tag, client.Email); err != nil {
											logger.Warningf("GetClients: failed to instantly remove expired client %s from config.json: %v", client.Email, err)
										} else {
											logger.Infof("GetClients: instantly removed expired client %s from config.json (inbound: %s)", client.Email, inbound.Tag)
											// Schedule async restart to apply changes
											xrayService := XrayService{}
											go func() {
												if err := xrayService.RestartXray(false); err != nil {
													logger.Warningf("GetClients: failed to restart Xray after removing expired client: %v", err)
												} else {
													logger.Debugf("GetClients: Xray restarted successfully after removing expired client (config synced)")
												}
											}()
										}
									}
								}
							}
						}
					}
				}
			}
		}

		// Load HWIDs for this client
		hwidService := ClientHWIDService{}
		hwids, err := hwidService.GetHWIDsForClient(client.Id)
		if err == nil {
			client.HWIDs = hwids
		} else {
			logger.Warningf("Failed to load HWIDs for client %d: %v", client.Id, err)
		}
	}

	return clients, nil
}

// GetClient retrieves a client by ID.
// Traffic statistics are now stored directly in ClientEntity table.
func (s *ClientService) GetClient(id int) (*model.ClientEntity, error) {
	db := database.GetDB()
	var client model.ClientEntity
	err := db.First(&client, id).Error
	if err != nil {
		return nil, err
	}

	// Load inbound assignments
	inboundIds, err := s.GetInboundIdsForClient(client.Id)
	if err == nil {
		client.InboundIds = inboundIds
	}

	// Traffic statistics (Up, Down, AllTime, LastOnline) are already loaded from ClientEntity table
	// No need to load from client_traffics

	// Load HWIDs for this client
	hwidService := ClientHWIDService{}
	hwids, err := hwidService.GetHWIDsForClient(client.Id)
	if err == nil {
		client.HWIDs = hwids
	}

	return &client, nil
}

// GetClientByEmail retrieves a client by email for a specific user.
func (s *ClientService) GetClientByEmail(userId int, email string) (*model.ClientEntity, error) {
	db := database.GetDB()
	var client model.ClientEntity
	err := db.Where("user_id = ? AND email = ?", userId, strings.ToLower(email)).First(&client).Error
	if err != nil {
		return nil, err
	}

	// Load inbound assignments
	inboundIds, err := s.GetInboundIdsForClient(client.Id)
	if err == nil {
		client.InboundIds = inboundIds
	}

	return &client, nil
}

// GetInboundIdsForClient retrieves all inbound IDs assigned to a client.
func (s *ClientService) GetInboundIdsForClient(clientId int) ([]int, error) {
	db := database.GetDB()
	var mappings []model.ClientInboundMapping
	err := db.Where("client_id = ?", clientId).Find(&mappings).Error
	if err != nil {
		return nil, err
	}

	inboundIds := make([]int, len(mappings))
	for i, mapping := range mappings {
		inboundIds[i] = mapping.InboundId
	}

	return inboundIds, nil
}

// AddClient creates a new client.
// Returns whether Xray needs restart and any error.
func (s *ClientService) AddClient(userId int, client *model.ClientEntity) (bool, error) {
	// Validate email uniqueness for this user
	existing, err := s.GetClientByEmail(userId, client.Email)
	if err == nil && existing != nil {
		return false, common.NewError("Client with email already exists: ", client.Email)
	}

	// Generate UUID if not provided and needed
	if client.UUID == "" {
		newUUID, err := uuid.NewRandom()
		if err != nil {
			return false, common.NewError("Failed to generate UUID: ", err.Error())
		}
		client.UUID = newUUID.String()
	}

	// Generate SubID if not provided
	if client.SubID == "" {
		client.SubID = random.Seq(16)
	}

	// Normalize email to lowercase
	client.Email = strings.ToLower(client.Email)
	client.UserId = userId

	// Set timestamps
	now := time.Now().Unix()
	if client.CreatedAt == 0 {
		client.CreatedAt = now
	}
	client.UpdatedAt = now

	db := database.GetDB()
	tx := db.Begin()
	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()

	// Initialize traffic fields to 0 (they are stored in ClientEntity now)
	client.Up = 0
	client.Down = 0
	client.AllTime = 0
	client.LastOnline = 0
	
	// Set default status to "active" if not specified
	if client.Status == "" {
		client.Status = "active"
	}
	
	// Ensure GroupId is explicitly set (can be nil for no group)
	// This prevents foreign key constraint violations
	if client.GroupId != nil && *client.GroupId <= 0 {
		client.GroupId = nil
	}

	// Use Select to explicitly control which fields are inserted
	// This ensures that nil GroupId is properly handled as NULL
	fieldsToInsert := []string{
		"user_id", "email", "uuid", "security", "password", "flow",
		"limit_ip", "total_gb", "expiry_time", "enable", "status",
		"tg_id", "sub_id", "comment", "reset", "created_at", "updated_at",
		"up", "down", "all_time", "last_online", "hwid_enabled", "max_hwid",
	}
	// Add group_id only if it's not nil
	if client.GroupId != nil {
		fieldsToInsert = append(fieldsToInsert, "group_id")
	}
	
	err = tx.Select(fieldsToInsert).Create(client).Error
	if err != nil {
		return false, err
	}

	// Traffic statistics are now stored directly in ClientEntity table
	// No need to create separate client_traffics records

	// Assign to inbounds if provided
	if len(client.InboundIds) > 0 {
		err = s.AssignClientToInbounds(tx, client.Id, client.InboundIds)
		if err != nil {
			return false, err
		}
	}
	
	// Commit client transaction first to avoid nested transactions
	err = tx.Commit().Error
	if err != nil {
		return false, err
	}
	
	// Invalidate cache for this user's clients
	cache.InvalidateClients(userId)
	
	// Now update Settings for all assigned inbounds
	// This is done AFTER committing the client transaction to avoid nested transactions and database locks
	needRestart := false
	if len(client.InboundIds) > 0 {
		inboundService := InboundService{}
		for _, inboundId := range client.InboundIds {
			inbound, err := inboundService.GetInbound(inboundId)
			if err != nil {
				logger.Warningf("Failed to get inbound %d for settings update: %v", inboundId, err)
				continue
			}
			
			// Get all clients for this inbound (from ClientEntity)
			clientEntities, err := s.GetClientsForInbound(inboundId)
			if err != nil {
				logger.Warningf("Failed to get clients for inbound %d: %v", inboundId, err)
				continue
			}
			
			// Rebuild Settings from ClientEntity
			newSettings, err := inboundService.BuildSettingsFromClientEntities(inbound, clientEntities)
			if err != nil {
				logger.Warningf("Failed to build settings for inbound %d: %v", inboundId, err)
				continue
			}
			
			// Update inbound Settings (this will open its own transaction)
			// Use retry logic to handle database lock errors
			inbound.Settings = newSettings
			_, inboundNeedRestart, err := inboundService.updateInboundWithRetry(inbound)
			if err != nil {
				logger.Warningf("Failed to update inbound %d settings: %v", inboundId, err)
				// Continue with other inbounds
			} else if inboundNeedRestart {
				needRestart = true
			}
		}
	}

	return needRestart, nil
}

// UpdateClient updates an existing client.
// Returns whether Xray needs restart and any error.
func (s *ClientService) UpdateClient(userId int, client *model.ClientEntity) (bool, error) {
	// Check if client exists and belongs to user
	existing, err := s.GetClient(client.Id)
	if err != nil {
		return false, err
	}
	if existing.UserId != userId {
		return false, common.NewError("Client not found or access denied")
	}

	// Check email uniqueness if email changed
	if client.Email != "" && strings.ToLower(client.Email) != strings.ToLower(existing.Email) {
		existingByEmail, err := s.GetClientByEmail(userId, client.Email)
		if err == nil && existingByEmail != nil && existingByEmail.Id != client.Id {
			return false, common.NewError("Client with email already exists: ", client.Email)
		}
	}

	// Normalize email to lowercase if provided
	if client.Email != "" {
		client.Email = strings.ToLower(client.Email)
	}

	// Update timestamp
	client.UpdatedAt = time.Now().Unix()

	// Validate group_id if it's being set
	if client.GroupId != nil && *client.GroupId > 0 {
		groupService := ClientGroupService{}
		_, err := groupService.GetGroup(*client.GroupId, userId)
		if err != nil {
			// Group doesn't exist or doesn't belong to user
			// Set group_id to nil instead of failing
			logger.Warningf("Group %d not found for user %d, setting group_id to nil for client %d", *client.GroupId, userId, client.Id)
			client.GroupId = nil
		}
	}

	db := database.GetDB()
	tx := db.Begin()
	// Track if transaction was committed to avoid double rollback
	committed := false
	defer func() {
		// Only rollback if there was an error and transaction wasn't committed
		if err != nil && !committed {
			tx.Rollback()
		}
	}()

	// Update only provided fields
	updates := make(map[string]interface{})
	if client.Email != "" {
		updates["email"] = client.Email
	}
	if client.UUID != "" {
		updates["uuid"] = client.UUID
	}
	if client.Security != "" {
		updates["security"] = client.Security
	}
	if client.Password != "" {
		updates["password"] = client.Password
	}
	if client.Flow != "" {
		updates["flow"] = client.Flow
	}
	// Always update these fields - they can be 0 (unlimited/disabled) or empty
	updates["limit_ip"] = client.LimitIP
	updates["total_gb"] = client.TotalGB
	updates["expiry_time"] = client.ExpiryTime
	updates["enable"] = client.Enable
	updates["status"] = client.Status
	updates["tg_id"] = client.TgID
	updates["sub_id"] = client.SubID
	updates["comment"] = client.Comment
	updates["reset"] = client.Reset
	// Update group_id - can be nil (no group)
	// Only update if it's different from existing value
	if existing.GroupId == nil && client.GroupId == nil {
		// Both nil, no change needed
	} else if existing.GroupId != nil && client.GroupId != nil && *existing.GroupId == *client.GroupId {
		// Same value, no change needed
	} else {
		// Value changed, update it
		updates["group_id"] = client.GroupId
	}
	// Update HWID settings - GORM converts field names to snake_case automatically
	// HWIDEnabled -> hwid_enabled, MaxHWID -> max_hwid
	// Always update HWID settings (they should always be present when updating from the UI)
	updates["hwid_enabled"] = client.HWIDEnabled
	// Always update max_hwid, including 0 (which means unlimited)
	updates["max_hwid"] = client.MaxHWID
	updates["updated_at"] = client.UpdatedAt

	// First try to update with all fields including HWID
	err = tx.Model(&model.ClientEntity{}).Where("id = ? AND user_id = ?", client.Id, userId).Updates(updates).Error
	if err != nil {
		// If HWID columns don't exist, remove them and try again
		if strings.Contains(err.Error(), "no such column: hwid_enabled") || strings.Contains(err.Error(), "no such column: max_hwid") {
			delete(updates, "hwid_enabled")
			delete(updates, "max_hwid")
			err = tx.Model(&model.ClientEntity{}).Where("id = ? AND user_id = ?", client.Id, userId).Updates(updates).Error
		}
	}
	if err != nil {
		return false, err
	}
	
	// Get current inbound assignments to determine which inbounds need updating
	var currentMappings []model.ClientInboundMapping
	tx.Where("client_id = ?", client.Id).Find(&currentMappings)
	oldInboundIds := make(map[int]bool)
	for _, mapping := range currentMappings {
		oldInboundIds[mapping.InboundId] = true
	}
	
	// Track all affected inbounds (old + new) for settings update
	affectedInboundIds := make(map[int]bool)
	for inboundId := range oldInboundIds {
		affectedInboundIds[inboundId] = true
	}
	
	// Update inbound assignments if provided
	// Note: InboundIds is a slice, so we need to check if it was explicitly set
	// We'll always update if InboundIds is not nil (even if empty array means remove all)
	if client.InboundIds != nil {
		// Remove existing assignments
		err = tx.Where("client_id = ?", client.Id).Delete(&model.ClientInboundMapping{}).Error
		if err != nil {
			return false, err
		}

		// Add new assignments (if any)
		if len(client.InboundIds) > 0 {
			err = s.AssignClientToInbounds(tx, client.Id, client.InboundIds)
			if err != nil {
				return false, err
			}
			// Track new inbound IDs for settings update
			for _, inboundId := range client.InboundIds {
				affectedInboundIds[inboundId] = true
			}
		}
	}
	
	// Traffic statistics are now stored directly in ClientEntity table
	// No need to sync with client_traffics - all fields (TotalGB, ExpiryTime, Enable, Email) are in ClientEntity
	
	// Check if client was expired and is now no longer expired (traffic reset or limit increased)
	// Reload client to get updated values
	var updatedClient model.ClientEntity
	if err := tx.Where("id = ?", client.Id).First(&updatedClient).Error; err == nil {
		wasExpired := existing.Status == "expired_traffic" || existing.Status == "expired_time"
		
		// Check if client is no longer expired
		now := time.Now().Unix() * 1000
		totalUsed := updatedClient.Up + updatedClient.Down
		trafficLimit := int64(updatedClient.TotalGB * 1024 * 1024 * 1024)
		trafficExceeded := updatedClient.TotalGB > 0 && totalUsed >= trafficLimit
		timeExpired := updatedClient.ExpiryTime > 0 && updatedClient.ExpiryTime <= now
		
		// If client was expired but is no longer expired, reset status and re-add to Xray
		if wasExpired && !trafficExceeded && !timeExpired && updatedClient.Enable {
			updates["status"] = "active"
			if err := tx.Model(&model.ClientEntity{}).Where("id = ?", client.Id).Update("status", "active").Error; err == nil {
				updatedClient.Status = "active"
				logger.Infof("Client %s is no longer expired, status reset to active", updatedClient.Email)
			}
		}
	}
	
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] UpdateClient: before Commit, clientId=%d, userId=%d", client.Id, userId)
	// #endregion
	
	// Commit client transaction first to avoid nested transactions
	err = tx.Commit().Error
	committed = true
	
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] UpdateClient: after Commit, clientId=%d, error=%v", client.Id, err)
	// #endregion
	
	if err != nil {
		return false, err
	}
	
	// Invalidate cache for this user's clients
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] UpdateClient: before cache invalidation, userId=%d", userId)
	// #endregion
	cacheErr := cache.InvalidateClients(userId)
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] UpdateClient: after cache invalidation, userId=%d, error=%v", userId, cacheErr)
	// #endregion
	
	// Reload client from DB after commit to get latest status and values
	var finalClient model.ClientEntity
	db = database.GetDB()
	if err = db.Where("id = ?", client.Id).First(&finalClient).Error; err != nil {
		logger.Warningf("UpdateClient: failed to reload client %d after commit: %v", client.Id, err)
		finalClient = updatedClient // Fallback to previous value
	}
	
	// Now update Settings for all affected inbounds (old + new) ASYNCHRONOUSLY
	// This is needed even if InboundIds wasn't changed, because client data (UUID, password, etc.) might have changed
	// We do this AFTER committing the client transaction to avoid nested transactions and database locks
	// Run asynchronously to avoid blocking the HTTP response - changes will be applied immediately in background
	go func() {
		needRestart := false
		inboundService := InboundService{}
		settingService := SettingService{}
		multiMode, _ := settingService.GetMultiNodeMode()
		xrayService := XrayService{}
		
		// Check if enable status changed
		enableChanged := existing.Enable != finalClient.Enable
		wasExpired := existing.Status == "expired_traffic" || existing.Status == "expired_time"
		
		// Check if client is no longer expired (by traffic or time)
		now := time.Now().Unix() * 1000
		totalUsed := finalClient.Up + finalClient.Down
		trafficLimit := int64(finalClient.TotalGB * 1024 * 1024 * 1024)
		trafficExceeded := finalClient.TotalGB > 0 && totalUsed >= trafficLimit
		timeExpired := finalClient.ExpiryTime > 0 && finalClient.ExpiryTime <= now
		nowActive := (!trafficExceeded && !timeExpired) && (finalClient.Status == "active" || finalClient.Status == "")
		
		needsReAdd := wasExpired && nowActive && finalClient.Enable
		
		// Instant config update + async restart approach (no API needed)
		// 1. Update config.json instantly (add/remove client from Settings)
		// 2. Update DB Settings
		// 3. Async restart to apply changes
		logger.Infof("UpdateClient: enableChanged=%v, needsReAdd=%v, multiMode=%v, xrayRunning=%v", 
			enableChanged, needsReAdd, multiMode, xrayService.IsXrayRunning())
		
		// Single mode: instantly update config.json before restart
		if !multiMode {
			if xrayService.IsXrayRunning() {
				processConfig := xrayService.GetConfig()
				if processConfig != nil {
					clientInboundIds, err := s.GetInboundIdsForClient(client.Id)
					if err == nil {
						for _, inboundId := range clientInboundIds {
							inbound, err := inboundService.GetInbound(inboundId)
							if err != nil {
								continue
							}
							
							// Build client data for config update
							clientData := make(map[string]interface{})
							clientData["email"] = finalClient.Email
							
							switch inbound.Protocol {
							case model.Trojan:
								clientData["password"] = finalClient.Password
							case model.Shadowsocks:
								var settings map[string]interface{}
								json.Unmarshal([]byte(inbound.Settings), &settings)
								if method, ok := settings["method"].(string); ok {
									clientData["method"] = method
								}
								clientData["password"] = finalClient.Password
							case model.VMESS, model.VLESS:
								clientData["id"] = finalClient.UUID
								if inbound.Protocol == model.VMESS && finalClient.Security != "" {
									clientData["security"] = finalClient.Security
								}
								if inbound.Protocol == model.VLESS && finalClient.Flow != "" {
									clientData["flow"] = finalClient.Flow
								}
							}
							
							// Instantly update config.json
							if finalClient.Enable {
								// Add client to config.json
								if err := xray.UpdateConfigFileAfterUserAddition(processConfig, inbound.Tag, clientData); err != nil {
									logger.Warningf("UpdateClient: failed to instantly add client %s to config.json: %v", finalClient.Email, err)
								} else {
									logger.Infof("UpdateClient: instantly added client %s to config.json (inbound: %s)", finalClient.Email, inbound.Tag)
								}
							} else {
								// Remove client from config.json
								if err := xray.UpdateConfigFileAfterUserRemoval(processConfig, inbound.Tag, finalClient.Email); err != nil {
									logger.Warningf("UpdateClient: failed to instantly remove client %s from config.json: %v", finalClient.Email, err)
								} else {
									logger.Infof("UpdateClient: instantly removed client %s from config.json (inbound: %s)", finalClient.Email, inbound.Tag)
								}
							}
						}
					}
				}
			}
		}
		
		// Always need restart when enable status changes or client needs re-add
		if enableChanged || needsReAdd {
			needRestart = true
		}
		
		// Also need restart if client data changed (UUID, password, etc.)
		// because Settings need to be rebuilt with new client data
		if existing.UUID != finalClient.UUID || existing.Password != finalClient.Password || 
		   existing.Security != finalClient.Security || existing.Flow != finalClient.Flow {
			needRestart = true
		}
		
		// Update Settings for affected inbounds (needed to keep DB in sync)
		for inboundId := range affectedInboundIds {
			inbound, err := inboundService.GetInbound(inboundId)
			if err != nil {
				logger.Warningf("Failed to get inbound %d for settings update: %v", inboundId, err)
				continue
			}
			
			// Get all clients for this inbound (from ClientEntity)
			clientEntities, err := s.GetClientsForInbound(inboundId)
			if err != nil {
				logger.Warningf("Failed to get clients for inbound %d: %v", inboundId, err)
				continue
			}
			
			// Rebuild Settings from ClientEntity
			newSettings, err := inboundService.BuildSettingsFromClientEntities(inbound, clientEntities)
			if err != nil {
				logger.Warningf("Failed to build settings for inbound %d: %v", inboundId, err)
				continue
			}
			
			// Update inbound Settings in DB (to keep database in sync)
			// Use retry logic to handle database lock errors
			inbound.Settings = newSettings
			_, inboundNeedRestart, err := inboundService.updateInboundWithRetry(inbound)
			if err != nil {
				logger.Warningf("Failed to update inbound %d settings: %v", inboundId, err)
				// Continue with other inbounds
			} else if inboundNeedRestart {
				needRestart = true
			}
		}
		
		// Always restart in single mode (config.json already updated)
		if !multiMode {
			needRestart = true
		}
		
		// Restart Xray asynchronously in background to apply changes
		// This ensures config is fully synchronized without blocking the response
		// Fastest approach: instant config update + async restart (user gets instant response, restart happens in background)
		if needRestart {
			logger.Debugf("UpdateClient: scheduling async restart to apply changes")
			xrayService := XrayService{}
			xrayService.RestartXrayAsync(false)
		}
	}()

	// Return needRestart based on whether API operation was done
	// If API operation was done, no restart needed (handled asynchronously)
	// If API operation wasn't done, might need restart (handled asynchronously)
	// We return false here because restart is handled asynchronously in goroutine above
	// The controller will check needRestart from the goroutine result
	return false, nil
}

// DeleteClient deletes a client by ID.
// Returns whether Xray needs restart and any error.
func (s *ClientService) DeleteClient(userId int, id int) (bool, error) {
	// Check if client exists and belongs to user
	existing, err := s.GetClient(id)
	if err != nil {
		return false, err
	}
	if existing.UserId != userId {
		return false, common.NewError("Client not found or access denied")
	}
	
	// Get inbound assignments before deleting
	var mappings []model.ClientInboundMapping
	db := database.GetDB()
	err = db.Where("client_id = ?", id).Find(&mappings).Error
	if err != nil {
		return false, err
	}
	
	affectedInboundIds := make(map[int]bool)
	for _, mapping := range mappings {
		affectedInboundIds[mapping.InboundId] = true
	}
	
	needRestart := false

	tx := db.Begin()
	// Track if transaction was committed to avoid double rollback
	committed := false
	defer func() {
		// Only rollback if there was an error and transaction wasn't committed
		if err != nil && !committed {
			tx.Rollback()
		}
	}()

	// Delete inbound mappings
	err = tx.Where("client_id = ?", id).Delete(&model.ClientInboundMapping{}).Error
	if err != nil {
		return false, err
	}

	// Delete client
	err = tx.Where("id = ? AND user_id = ?", id, userId).Delete(&model.ClientEntity{}).Error
	if err != nil {
		return false, err
	}
	
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] DeleteClient: before Commit, clientId=%d, userId=%d", id, userId)
	// #endregion
	
	// Commit deletion transaction first to avoid nested transactions
	err = tx.Commit().Error
	committed = true
	
	// #region agent log
	logger.Debugf("[DEBUG-AGENT] DeleteClient: after Commit, clientId=%d, error=%v", id, err)
	// #endregion
	
	if err != nil {
		return false, err
	}
	
	// Instant config update + async restart approach (no API needed)
	// 1. Update config.json instantly (remove client from Settings)
	// 2. Update DB Settings
	// 3. Async restart to apply changes
	settingService := SettingService{}
	multiMode, _ := settingService.GetMultiNodeMode()
	inboundService := InboundService{}
	xrayService := XrayService{}
	
	// Single mode: instantly update config.json before restart
	if !multiMode {
		if xrayService.IsXrayRunning() {
			processConfig := xrayService.GetConfig()
			if processConfig != nil {
				// Instantly remove client from config.json for all affected inbounds
				for inboundId := range affectedInboundIds {
					inbound, err := inboundService.GetInbound(inboundId)
					if err != nil {
						logger.Warningf("DeleteClient: failed to get inbound %d: %v", inboundId, err)
						continue
					}
					// Instantly update config.json (remove client from Settings)
					if err := xray.UpdateConfigFileAfterUserRemoval(processConfig, inbound.Tag, existing.Email); err != nil {
						logger.Warningf("DeleteClient: failed to instantly update config.json for inbound %s: %v", inbound.Tag, err)
					} else {
						logger.Infof("DeleteClient: instantly removed client %s from config.json (inbound: %s)", existing.Email, inbound.Tag)
					}
				}
			}
		}
	}
	
	// Update Settings for affected inbounds (after deletion)
	// We do this AFTER committing the deletion transaction to avoid nested transactions and database locks
	for inboundId := range affectedInboundIds {
		inbound, err := inboundService.GetInbound(inboundId)
		if err != nil {
			logger.Warningf("Failed to get inbound %d for settings update: %v", inboundId, err)
			continue
		}
		
		// Get all remaining clients for this inbound (from ClientEntity)
		clientEntities, err := s.GetClientsForInbound(inboundId)
		if err != nil {
			logger.Warningf("Failed to get clients for inbound %d: %v", inboundId, err)
			continue
		}
		
		// Rebuild Settings from ClientEntity
		newSettings, err := inboundService.BuildSettingsFromClientEntities(inbound, clientEntities)
		if err != nil {
			logger.Warningf("Failed to build settings for inbound %d: %v", inboundId, err)
			continue
		}
		
		// Update inbound Settings in DB (to keep database in sync)
		// Use retry logic to handle database lock errors
		inbound.Settings = newSettings
		_, inboundNeedRestart, err := inboundService.updateInboundWithRetry(inbound)
		if err != nil {
			logger.Warningf("Failed to update inbound %d settings: %v", inboundId, err)
			// Continue with other inbounds
		} else if inboundNeedRestart {
			needRestart = true
		}
	}

	// Always restart in single mode (config.json already updated)
	// In multi-mode, nodes will be updated via their APIs
	if !multiMode {
		needRestart = true
	}

	// Restart Xray asynchronously in background to apply changes
	// This ensures config is fully synchronized without blocking the response
	// Fastest approach: instant config update + async restart (user gets instant response, restart happens in background)
	if needRestart {
		logger.Debugf("DeleteClient: scheduling async restart to apply changes")
		go func() {
			if err := xrayService.RestartXray(false); err != nil {
				logger.Warningf("DeleteClient: failed to restart Xray: %v", err)
			} else {
				logger.Debugf("DeleteClient: Xray restarted successfully (config synced)")
			}
		}()
		return false, nil // No need for synchronous restart
	}

	return false, nil
}

// AssignClientToInbounds assigns a client to multiple inbounds.
func (s *ClientService) AssignClientToInbounds(tx *gorm.DB, clientId int, inboundIds []int) error {
	for _, inboundId := range inboundIds {
		mapping := &model.ClientInboundMapping{
			ClientId:  clientId,
			InboundId: inboundId,
		}
		err := tx.Create(mapping).Error
		if err != nil {
			logger.Warningf("Failed to assign client %d to inbound %d: %v", clientId, inboundId, err)
			// Continue with other assignments
		}
	}
	return nil
}

// GetClientsForInbound retrieves all clients assigned to an inbound.
func (s *ClientService) GetClientsForInbound(inboundId int) ([]*model.ClientEntity, error) {
	db := database.GetDB()
	var mappings []model.ClientInboundMapping
	err := db.Where("inbound_id = ?", inboundId).Find(&mappings).Error
	if err != nil {
		return nil, err
	}

	if len(mappings) == 0 {
		return []*model.ClientEntity{}, nil
	}

	clientIds := make([]int, len(mappings))
	for i, mapping := range mappings {
		clientIds[i] = mapping.ClientId
	}

	var clients []*model.ClientEntity
	err = db.Where("id IN ?", clientIds).Find(&clients).Error
	if err != nil {
		return nil, err
	}

	return clients, nil
}

// ConvertClientEntityToClient converts ClientEntity to legacy Client struct for backward compatibility.
func (s *ClientService) ConvertClientEntityToClient(entity *model.ClientEntity) model.Client {
	return model.Client{
		ID:         entity.UUID,
		Security:   entity.Security,
		Password:   entity.Password,
		Flow:       entity.Flow,
		Email:      entity.Email,
		LimitIP:    entity.LimitIP,
		TotalGB:    int64(entity.TotalGB), // Convert float64 to int64 for legacy compatibility (rounds down)
		ExpiryTime: entity.ExpiryTime,
		Enable:     entity.Enable,
		TgID:       entity.TgID,
		SubID:      entity.SubID,
		Comment:    entity.Comment,
		Reset:      entity.Reset,
		CreatedAt:  entity.CreatedAt,
		UpdatedAt:  entity.UpdatedAt,
	}
}

// ConvertClientToEntity converts legacy Client struct to ClientEntity.
func (s *ClientService) ConvertClientToEntity(client *model.Client, userId int) *model.ClientEntity {
	status := "active"
	if !client.Enable {
		// If client is disabled, check if it's expired
		now := time.Now().Unix() * 1000
		totalUsed := int64(0) // We don't have traffic info here, assume 0
		trafficLimit := int64(client.TotalGB * 1024 * 1024 * 1024)
		trafficExceeded := client.TotalGB > 0 && totalUsed >= trafficLimit
		timeExpired := client.ExpiryTime > 0 && client.ExpiryTime <= now
		if trafficExceeded {
			status = "expired_traffic"
		} else if timeExpired {
			status = "expired_time"
		}
	}
	return &model.ClientEntity{
		UserId:     userId,
		Email:      strings.ToLower(client.Email),
		UUID:       client.ID,
		Security:   client.Security,
		Password:   client.Password,
		Flow:       client.Flow,
		LimitIP:    client.LimitIP,
		TotalGB:    float64(client.TotalGB), // Convert int64 to float64
		ExpiryTime: client.ExpiryTime,
		Enable:     client.Enable,
		Status:     status,
		TgID:       client.TgID,
		SubID:      client.SubID,
		Comment:    client.Comment,
		Reset:      client.Reset,
		CreatedAt:  client.CreatedAt,
		UpdatedAt:  client.UpdatedAt,
	}
}

// DisableClientsByEmail removes expired clients from Xray API and updates their status.
// This is called after AddClientTraffic marks clients as expired.
func (s *ClientService) DisableClientsByEmail(clientsToDisable map[string]string, inboundService *InboundService) (bool, error) {
	if len(clientsToDisable) == 0 {
		logger.Debugf("DisableClientsByEmail: no clients to disable")
		return false, nil
	}

	logger.Infof("DisableClientsByEmail: removing %d expired clients from Xray", len(clientsToDisable))

	db := database.GetDB()
	needRestart := false
	settingService := SettingService{}
	multiMode, _ := settingService.GetMultiNodeMode()
	nodeService := NodeService{}

	// Group clients by tag and inbound ID for better processing
	// Build map: email -> inbound info (tag, inboundId)
	emailToInbound := make(map[string]struct {
		tag       string
		inboundId int
	})
	
	for email, tag := range clientsToDisable {
		// Find inbound by tag to get inboundId
		var inbound model.Inbound
		if err := db.Where("tag = ?", tag).First(&inbound).Error; err == nil {
			emailToInbound[email] = struct {
				tag       string
				inboundId int
			}{tag: tag, inboundId: inbound.Id}
		} else {
			logger.Warningf("DisableClientsByEmail: failed to find inbound with tag %s for client %s: %v", tag, email, err)
			// Still try to remove with just tag
			emailToInbound[email] = struct {
				tag       string
				inboundId int
			}{tag: tag, inboundId: 0}
		}
	}

	// Remove from Xray API (both local and nodes)
	// Group by mode to optimize API calls
	if multiMode {
		// Multi-node mode: remove from all nodes
		for email, inboundInfo := range emailToInbound {
			if inboundInfo.inboundId > 0 {
				nodes, err := nodeService.GetNodesForInbound(inboundInfo.inboundId)
				if err == nil {
					for _, node := range nodes {
						go func(n *model.Node, tag string, email string) {
							if err := nodeService.RemoveUserFromNode(n, tag, email); err != nil {
								logger.Warningf("DisableClientsByEmail: failed to remove expired client %s from node %s via API: %v", email, n.Name, err)
							} else {
								logger.Infof("DisableClientsByEmail: removed expired client %s from node %s via API (instant)", email, n.Name)
							}
						}(node, inboundInfo.tag, email)
					}
				}
			}
		}
	} else {
		// Single mode: instantly update config.json and restart
		xrayService := XrayService{}
		if xrayService.IsXrayRunning() {
			processConfig := xrayService.GetConfig()
			if processConfig != nil {
				for email, inboundInfo := range emailToInbound {
					// Instantly remove client from config.json
					if err := xray.UpdateConfigFileAfterUserRemoval(processConfig, inboundInfo.tag, email); err != nil {
						logger.Warningf("DisableClientsByEmail: failed to instantly remove client %s from config.json: %v", email, err)
						needRestart = true
					} else {
						logger.Infof("DisableClientsByEmail: instantly removed client %s from config.json (inbound: %s)", email, inboundInfo.tag)
						needRestart = true
					}
				}
			}
		}
	}

	// Update client status in database (but keep Enable = true)
	emails := make([]string, 0, len(clientsToDisable))
	for email := range clientsToDisable {
		emails = append(emails, email)
	}

	// Get clients and update their status
	var clients []*model.ClientEntity
	if err := db.Where("LOWER(email) IN (?)", emails).Find(&clients).Error; err == nil {
		for _, client := range clients {
			// Status should already be set by AddClientTraffic, but ensure it's set
			if client.Status != "expired_traffic" && client.Status != "expired_time" {
				// Determine status based on limits
				now := time.Now().Unix() * 1000
				totalUsed := client.Up + client.Down
				trafficLimit := int64(client.TotalGB * 1024 * 1024 * 1024)
				trafficExceeded := client.TotalGB > 0 && totalUsed >= trafficLimit
				timeExpired := client.ExpiryTime > 0 && client.ExpiryTime <= now
				
				if trafficExceeded {
					client.Status = "expired_traffic"
				} else if timeExpired {
					client.Status = "expired_time"
				}
			}
		}
		db.Save(clients)
	}

	// Update inbound settings to remove expired clients
	// Get all affected inbounds
	allTags := make(map[string]bool)
	for _, tag := range clientsToDisable {
		allTags[tag] = true
	}

	for tag := range allTags {
		var inbound model.Inbound
		if err := db.Where("tag = ?", tag).First(&inbound).Error; err == nil {
			logger.Debugf("DisableClientsByEmail: updating inbound %d (tag: %s) to remove expired clients", inbound.Id, tag)
			// Rebuild settings without expired clients
			allClients, err := s.GetClientsForInbound(inbound.Id)
			if err == nil {
				// Count expired clients before filtering
				expiredCount := 0
				for _, client := range allClients {
					if client.Status == "expired_traffic" || client.Status == "expired_time" {
						expiredCount++
					}
				}
				logger.Debugf("DisableClientsByEmail: inbound %d has %d total clients, %d expired", inbound.Id, len(allClients), expiredCount)
				
				newSettings, err := inboundService.BuildSettingsFromClientEntities(&inbound, allClients)
				if err == nil {
					inbound.Settings = newSettings
					_, _, err = inboundService.updateInboundWithRetry(&inbound)
					if err != nil {
						logger.Warningf("DisableClientsByEmail: failed to update inbound %d: %v", inbound.Id, err)
						needRestart = true
					} else {
						logger.Infof("DisableClientsByEmail: successfully updated inbound %d (tag: %s) without expired clients", inbound.Id, tag)
					}
				} else {
					logger.Warningf("DisableClientsByEmail: failed to build settings for inbound %d: %v", inbound.Id, err)
				}
			} else {
				logger.Warningf("DisableClientsByEmail: failed to get clients for inbound %d: %v", inbound.Id, err)
			}
		} else {
			logger.Warningf("DisableClientsByEmail: failed to find inbound with tag %s: %v", tag, err)
		}
	}

	return needRestart, nil
}

// ResetAllClientTraffics resets traffic counters for all clients of a specific user.
// Returns whether Xray needs restart and any error.
func (s *ClientService) ResetAllClientTraffics(userId int) (bool, error) {
	db := database.GetDB()
	
	// Get all clients that were expired due to traffic before reset
	var expiredClients []model.ClientEntity
	err := db.Where("user_id = ? AND status = ?", userId, "expired_traffic").Find(&expiredClients).Error
	if err != nil {
		return false, err
	}
	
	// Reset traffic for all clients of this user in ClientEntity table
	result := db.Model(&model.ClientEntity{}).
		Where("user_id = ?", userId).
		Updates(map[string]interface{}{
			"up":       0,
			"down":     0,
			"all_time": 0,
		})
	
	if result.Error != nil {
		return false, result.Error
	}
	
	// Invalidate cache for this user's clients
	cache.InvalidateClients(userId)
	
	// Reset status to "active" for clients expired due to traffic
	// This will allow clients to be re-added to Xray if they were removed
	db.Model(&model.ClientEntity{}).
		Where("user_id = ? AND status = ?", userId, "expired_traffic").
		Update("status", "active")
	
	// Re-add expired clients to Xray if they were removed
	needRestart := false
	if len(expiredClients) > 0 {
		inboundService := InboundService{}
		settingService := SettingService{}
		multiMode, _ := settingService.GetMultiNodeMode()
		xrayService := XrayService{}
		
		// Re-add expired clients to Xray if they were removed
		{
			// Group clients by inbound
			inboundClients := make(map[int][]model.ClientEntity)
			for _, client := range expiredClients {
				if !client.Enable {
					continue
				}
				inboundIds, err := s.GetInboundIdsForClient(client.Id)
				if err == nil {
					for _, inboundId := range inboundIds {
						inboundClients[inboundId] = append(inboundClients[inboundId], client)
					}
				}
			}
			
			// Re-add clients to Xray for each inbound
			for inboundId, clients := range inboundClients {
				inbound, err := inboundService.GetInbound(inboundId)
				if err != nil {
					continue
				}
				
				// Get method for shadowsocks
				var method string
				if inbound.Protocol == model.Shadowsocks {
					var settings map[string]any
					json.Unmarshal([]byte(inbound.Settings), &settings)
					if m, ok := settings["method"].(string); ok {
						method = m
					}
				}
				
				for _, client := range clients {
					// Build client data for Xray API
					clientData := make(map[string]any)
					clientData["email"] = client.Email
					
					switch inbound.Protocol {
					case model.Trojan:
						clientData["password"] = client.Password
					case model.Shadowsocks:
						if method != "" {
							clientData["method"] = method
						}
						clientData["password"] = client.Password
					case model.VMESS, model.VLESS:
						clientData["id"] = client.UUID
						if inbound.Protocol == model.VMESS && client.Security != "" {
							clientData["security"] = client.Security
						}
						if inbound.Protocol == model.VLESS && client.Flow != "" {
							clientData["flow"] = client.Flow
						}
					}
					
					// Single mode: instantly update config.json and restart
					if !multiMode {
						if xrayService.IsXrayRunning() {
							processConfig := xrayService.GetConfig()
							if processConfig != nil {
								// Instantly add client to config.json
								if err := xray.UpdateConfigFileAfterUserAddition(processConfig, inbound.Tag, clientData); err != nil {
									logger.Warningf("ResetAllClientTraffics: failed to instantly add client %s to config.json: %v", client.Email, err)
									needRestart = true
								} else {
									logger.Infof("ResetAllClientTraffics: instantly added client %s to config.json (inbound: %s)", client.Email, inbound.Tag)
									needRestart = true
								}
							}
						}
					}
				}
				
				// Update inbound settings to include all clients
				allClients, err := s.GetClientsForInbound(inboundId)
				if err == nil {
					newSettings, err := inboundService.BuildSettingsFromClientEntities(inbound, allClients)
					if err == nil {
						inbound.Settings = newSettings
						_, inboundNeedRestart, err := inboundService.updateInboundWithRetry(inbound)
						if err != nil {
							logger.Warningf("Failed to update inbound %d settings: %v", inboundId, err)
						} else if inboundNeedRestart {
							needRestart = true
						}
					}
				}
			}
		}
	}
	
	return needRestart, nil
}

// ResetClientTraffic resets traffic counter for a specific client.
// Returns whether Xray needs restart and any error.
func (s *ClientService) ResetClientTraffic(userId int, clientId int) (bool, error) {
	db := database.GetDB()
	
	// Get client and verify ownership
	client, err := s.GetClient(clientId)
	if err != nil {
		return false, err
	}
	if client.UserId != userId {
		return false, common.NewError("Client not found or access denied")
	}
	
	// Check if client was expired due to traffic
	wasExpired := client.Status == "expired_traffic" || client.Status == "expired_time"
	
	// Reset traffic in ClientEntity
	result := db.Model(&model.ClientEntity{}).
		Where("id = ? AND user_id = ?", clientId, userId).
		Updates(map[string]interface{}{
			"up":       0,
			"down":     0,
			"all_time": 0,
		})
	
	if result.Error != nil {
		return false, result.Error
	}
	
	// Invalidate cache for this user's clients
	cache.InvalidateClients(userId)
	
	// Reset status to "active" if client was expired due to traffic
	if wasExpired {
		db.Model(&model.ClientEntity{}).
			Where("id = ? AND user_id = ?", clientId, userId).
			Update("status", "active")
	}
	
	// Re-add client to Xray if it was expired and is now active
	needRestart := false
	if wasExpired && client.Enable {
		inboundService := InboundService{}
		settingService := SettingService{}
		multiMode, _ := settingService.GetMultiNodeMode()
		nodeService := NodeService{}
		xrayService := XrayService{}
		
		// Check if we can use API (multi-node mode or local Xray running)
		canUseAPI := multiMode || xrayService.IsXrayRunning()
		
		if canUseAPI {
			// Get all inbounds for this client
			inboundIds, err := s.GetInboundIdsForClient(clientId)
			if err == nil {
				for _, inboundId := range inboundIds {
					inbound, err := inboundService.GetInbound(inboundId)
					if err != nil {
						continue
					}
					
					// Build client data for Xray API
					clientData := make(map[string]any)
					clientData["email"] = client.Email
					
					switch inbound.Protocol {
					case model.Trojan:
						clientData["password"] = client.Password
					case model.Shadowsocks:
						var settings map[string]any
						json.Unmarshal([]byte(inbound.Settings), &settings)
						if method, ok := settings["method"].(string); ok {
							clientData["method"] = method
						}
						clientData["password"] = client.Password
					case model.VMESS, model.VLESS:
						clientData["id"] = client.UUID
						if inbound.Protocol == model.VMESS && client.Security != "" {
							clientData["security"] = client.Security
						}
						if inbound.Protocol == model.VLESS && client.Flow != "" {
							clientData["flow"] = client.Flow
						}
					}
					
					if multiMode {
						// Multi-node mode: add to all nodes assigned to this inbound
						nodes, err := nodeService.GetNodesForInbound(inboundId)
						if err == nil && len(nodes) > 0 {
							for _, node := range nodes {
								go func(n *model.Node) {
									if err := nodeService.AddUserToNode(n, string(inbound.Protocol), inbound.Tag, clientData); err != nil {
										logger.Warningf("ResetClientTraffic: failed to re-add client %s to node %s via API: %v", client.Email, n.Name, err)
									} else {
										logger.Infof("ResetClientTraffic: re-added client %s to node %s via API after traffic reset", client.Email, n.Name)
									}
								}(node)
							}
						}
					} else {
						// Single mode: add to local Xray
						if p != nil && p.IsRunning() {
							apiPort := p.GetAPIPort()
							api, err := inboundService.getXrayAPI(apiPort)
							if err == nil {
								err1 := api.AddUser(string(inbound.Protocol), inbound.Tag, clientData)
								if err1 != nil {
									if strings.Contains(err1.Error(), "already exists") {
										logger.Debugf("ResetClientTraffic: client %s already exists in Xray (tag: %s)", client.Email, inbound.Tag)
									} else {
										logger.Warningf("ResetClientTraffic: failed to re-add client %s to Xray (tag: %s): %v", client.Email, inbound.Tag, err1)
										needRestart = true
									}
								} else {
									logger.Infof("ResetClientTraffic: re-added client %s to Xray (tag: %s) after traffic reset", client.Email, inbound.Tag)
								}
							} else {
								logger.Debugf("ResetClientTraffic: failed to get XrayAPI connection: %v", err)
								needRestart = true
							}
						}
					}
				}
				
				// Update inbound settings to include the client
				for _, inboundId := range inboundIds {
					inbound, err := inboundService.GetInbound(inboundId)
					if err != nil {
						continue
					}
					
					// Get all clients for this inbound
					clientEntities, err := s.GetClientsForInbound(inboundId)
					if err != nil {
						continue
					}
					
					// Rebuild Settings from ClientEntity
					newSettings, err := inboundService.BuildSettingsFromClientEntities(inbound, clientEntities)
					if err != nil {
						continue
					}
					
					// Update inbound Settings
					inbound.Settings = newSettings
					_, inboundNeedRestart, err := inboundService.updateInboundWithRetry(inbound)
					if err != nil {
						logger.Warningf("Failed to update inbound %d settings: %v", inboundId, err)
					} else if inboundNeedRestart {
						needRestart = true
					}
				}
			}
		}
	}
	
	return needRestart, nil
}

// DelDepletedClients deletes clients that have exhausted their traffic limits or expired.
// Returns the number of deleted clients, whether Xray needs restart, and any error.
func (s *ClientService) DelDepletedClients(userId int) (int, bool, error) {
	db := database.GetDB()
	now := time.Now().Unix() * 1000
	
	// Get all clients for this user
	var clients []model.ClientEntity
	err := db.Where("user_id = ?", userId).Find(&clients).Error
	if err != nil {
		return 0, false, err
	}
	
	if len(clients) == 0 {
		return 0, false, nil
	}
	
	emails := make([]string, len(clients))
	for i, client := range clients {
		emails[i] = strings.ToLower(client.Email)
	}
	
	// Find depleted client traffics
	var depletedTraffics []xray.ClientTraffic
	err = db.Model(&xray.ClientTraffic{}).
		Where("email IN (?) AND ((total > 0 AND up + down >= total) OR (expiry_time > 0 AND expiry_time <= ?))", emails, now).
		Find(&depletedTraffics).Error
	if err != nil {
		return 0, false, err
	}
	
	if len(depletedTraffics) == 0 {
		return 0, false, nil
	}
	
	// Get emails of depleted clients
	depletedEmails := make([]string, len(depletedTraffics))
	for i, traffic := range depletedTraffics {
		depletedEmails[i] = traffic.Email
	}
	
	// Get client IDs to delete
	var clientIdsToDelete []int
	err = db.Model(&model.ClientEntity{}).
		Where("user_id = ? AND LOWER(email) IN (?)", userId, depletedEmails).
		Pluck("id", &clientIdsToDelete).Error
	if err != nil {
		return 0, false, err
	}
	
	if len(clientIdsToDelete) == 0 {
		return 0, false, nil
	}
	
	// Delete clients and their mappings
	tx := db.Begin()
	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()
	
	// Delete client-inbound mappings
	err = tx.Where("client_id IN (?)", clientIdsToDelete).Delete(&model.ClientInboundMapping{}).Error
	if err != nil {
		return 0, false, err
	}
	
	// Delete client traffic records
	err = tx.Where("email IN (?)", depletedEmails).Delete(&xray.ClientTraffic{}).Error
	if err != nil {
		return 0, false, err
	}
	
	// Delete clients
	err = tx.Where("id IN (?) AND user_id = ?", clientIdsToDelete, userId).Delete(&model.ClientEntity{}).Error
	if err != nil {
		return 0, false, err
	}
	
	// Commit transaction before rebuilding inbounds (to avoid nested transactions)
	err = tx.Commit().Error
	if err != nil {
		return 0, false, err
	}
	
	// Rebuild Settings for all affected inbounds
	needRestart := false
	inboundService := InboundService{}
	
	// Get all unique inbound IDs that had these clients (from committed data)
	var affectedInboundIds []int
	err = db.Model(&model.ClientInboundMapping{}).
		Where("client_id IN (?)", clientIdsToDelete).
		Distinct("inbound_id").
		Pluck("inbound_id", &affectedInboundIds).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return 0, false, err
	}
	
	// Also check from client_traffics for backward compatibility (before deletion)
	// Note: This query runs after deletion, so we need to get inbound IDs from depleted traffics before deletion
	var trafficInboundIds []int
	for _, traffic := range depletedTraffics {
		if traffic.InboundId > 0 {
			// Check if already in list
			found := false
			for _, id := range trafficInboundIds {
				if id == traffic.InboundId {
					found = true
					break
				}
			}
			if !found {
				trafficInboundIds = append(trafficInboundIds, traffic.InboundId)
			}
		}
	}
	
	// Merge inbound IDs
	inboundIdSet := make(map[int]bool)
	for _, id := range affectedInboundIds {
		inboundIdSet[id] = true
	}
	for _, id := range trafficInboundIds {
		if !inboundIdSet[id] {
			affectedInboundIds = append(affectedInboundIds, id)
		}
	}
	
	// Rebuild Settings for each affected inbound
	for _, inboundId := range affectedInboundIds {
		var inbound model.Inbound
		err = db.First(&inbound, inboundId).Error
		if err != nil {
			continue
		}
		
		// Get all remaining clients for this inbound (from ClientEntity)
		clientEntities, err := s.GetClientsForInbound(inboundId)
		if err != nil {
			continue
		}
		
		// Rebuild Settings from ClientEntity
		newSettings, err := inboundService.BuildSettingsFromClientEntities(&inbound, clientEntities)
		if err != nil {
			logger.Warningf("Failed to build settings for inbound %d: %v", inboundId, err)
			continue
		}
		
		// Update inbound Settings
		inbound.Settings = newSettings
		_, inboundNeedRestart, err := inboundService.updateInboundWithRetry(&inbound)
		if err != nil {
			logger.Warningf("Failed to update inbound %d settings: %v", inboundId, err)
			continue
		} else if inboundNeedRestart {
			needRestart = true
		}
	}
	
	return len(clientIdsToDelete), needRestart, nil
}

// BulkResetTraffic resets traffic counters for multiple clients.
// Returns whether Xray needs restart and any error.
func (s *ClientService) BulkResetTraffic(userId int, clientIds []int) (bool, error) {
	if len(clientIds) == 0 {
		return false, nil
	}

	db := database.GetDB()

	// Verify all clients belong to user
	var count int64
	err := db.Model(&model.ClientEntity{}).
		Where("id IN ? AND user_id = ?", clientIds, userId).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	if int(count) != len(clientIds) {
		return false, common.NewError("Some clients not found or access denied")
	}

	// Get clients that were expired due to traffic before reset
	var expiredClients []model.ClientEntity
	err = db.Where("id IN ? AND user_id = ? AND status = ?", clientIds, userId, "expired_traffic").Find(&expiredClients).Error
	if err != nil {
		return false, err
	}

	// Reset traffic for selected clients
	result := db.Model(&model.ClientEntity{}).
		Where("id IN ? AND user_id = ?", clientIds, userId).
		Updates(map[string]interface{}{
			"up":       0,
			"down":     0,
			"all_time": 0,
		})

	if result.Error != nil {
		return false, result.Error
	}

	// Invalidate cache for this user's clients
	cache.InvalidateClients(userId)

	// Reset status to "active" for clients expired due to traffic
	if len(expiredClients) > 0 {
		db.Model(&model.ClientEntity{}).
			Where("id IN ? AND user_id = ? AND status = ?", clientIds, userId, "expired_traffic").
			Update("status", "active")
	}

	// Re-add expired clients to Xray if they were removed
	needRestart := false
	if len(expiredClients) > 0 {
		inboundService := InboundService{}
		settingService := SettingService{}
		multiMode, _ := settingService.GetMultiNodeMode()
		nodeService := NodeService{}
		xrayService := XrayService{}
		
		// Check if we can use API (multi-node mode or local Xray running)
		canUseAPI := multiMode || xrayService.IsXrayRunning()
		
		if canUseAPI {
			// Group clients by inbound
			inboundClients := make(map[int][]model.ClientEntity)
			for _, client := range expiredClients {
				if !client.Enable {
					continue
				}
				inboundIds, err := s.GetInboundIdsForClient(client.Id)
				if err == nil {
					for _, inboundId := range inboundIds {
						inboundClients[inboundId] = append(inboundClients[inboundId], client)
					}
				}
			}

			// Re-add clients to Xray for each inbound
			for inboundId, clients := range inboundClients {
				inbound, err := inboundService.GetInbound(inboundId)
				if err != nil {
					continue
				}

				for _, client := range clients {
					// Build client data for Xray API
					clientData := make(map[string]any)
					clientData["email"] = client.Email

					switch inbound.Protocol {
					case model.Trojan:
						clientData["password"] = client.Password
					case model.Shadowsocks:
						var settings map[string]any
						json.Unmarshal([]byte(inbound.Settings), &settings)
						if method, ok := settings["method"].(string); ok {
							clientData["method"] = method
						}
						clientData["password"] = client.Password
					case model.VMESS, model.VLESS:
						clientData["id"] = client.UUID
						if inbound.Protocol == model.VMESS && client.Security != "" {
							clientData["security"] = client.Security
						}
						if inbound.Protocol == model.VLESS && client.Flow != "" {
							clientData["flow"] = client.Flow
						}
					}

					if multiMode {
						// Multi-node mode: add to all nodes assigned to this inbound
						nodes, err := nodeService.GetNodesForInbound(inboundId)
						if err == nil && len(nodes) > 0 {
							for _, node := range nodes {
								go func(n *model.Node) {
									if err := nodeService.AddUserToNode(n, string(inbound.Protocol), inbound.Tag, clientData); err != nil {
										logger.Warningf("BulkResetTraffic: failed to re-add client %s to node %s via API: %v", client.Email, n.Name, err)
									} else {
										logger.Infof("BulkResetTraffic: re-added client %s to node %s via API after traffic reset", client.Email, n.Name)
									}
								}(node)
							}
						}
					} else {
						// Single mode: add to local Xray
						if p != nil && p.IsRunning() {
							apiPort := p.GetAPIPort()
							api, err := inboundService.getXrayAPI(apiPort)
							if err == nil {
								err1 := api.AddUser(string(inbound.Protocol), inbound.Tag, clientData)
								if err1 != nil {
									if strings.Contains(err1.Error(), "already exists") {
										logger.Debugf("BulkResetTraffic: client %s already exists in Xray (tag: %s)", client.Email, inbound.Tag)
									} else {
										logger.Warningf("BulkResetTraffic: failed to re-add client %s to Xray (tag: %s): %v", client.Email, inbound.Tag, err1)
										needRestart = true
									}
								} else {
									logger.Infof("BulkResetTraffic: re-added client %s to Xray (tag: %s) after traffic reset", client.Email, inbound.Tag)
								}
							} else {
								logger.Debugf("BulkResetTraffic: failed to get XrayAPI connection: %v", err)
								needRestart = true
							}
						}
					}
				}

				// Update inbound settings
				clientEntities, err := s.GetClientsForInbound(inboundId)
				if err == nil {
					newSettings, err := inboundService.BuildSettingsFromClientEntities(inbound, clientEntities)
					if err == nil {
						inbound.Settings = newSettings
						_, inboundNeedRestart, err := inboundService.updateInboundWithRetry(inbound)
						if err != nil {
							logger.Warningf("Failed to update inbound %d settings: %v", inboundId, err)
						} else if inboundNeedRestart {
							needRestart = true
						}
					}
				}
			}
		}
	}

	return needRestart, nil
}

// BulkClearHWIDs clears HWIDs for multiple clients.
func (s *ClientService) BulkClearHWIDs(userId int, clientIds []int) error {
	if len(clientIds) == 0 {
		return nil
	}

	// Verify all clients belong to user
	db := database.GetDB()
	var count int64
	err := db.Model(&model.ClientEntity{}).
		Where("id IN ? AND user_id = ?", clientIds, userId).
		Count(&count).Error
	if err != nil {
		return err
	}
	if int(count) != len(clientIds) {
		return common.NewError("Some clients not found or access denied")
	}

	// Clear HWIDs for selected clients
	hwidService := ClientHWIDService{}
	for _, clientId := range clientIds {
		err = hwidService.ClearHWIDsForClient(clientId)
		if err != nil {
			logger.Warningf("Failed to clear HWIDs for client %d: %v", clientId, err)
		}
	}

	// Invalidate cache for this user's clients
	cache.InvalidateClients(userId)

	return nil
}

// BulkDelete deletes multiple clients.
// Returns whether Xray needs restart and any error.
func (s *ClientService) BulkDelete(userId int, clientIds []int) (bool, error) {
	if len(clientIds) == 0 {
		return false, nil
	}

	// Verify all clients belong to user
	db := database.GetDB()
	var count int64
	err := db.Model(&model.ClientEntity{}).
		Where("id IN ? AND user_id = ?", clientIds, userId).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	if int(count) != len(clientIds) {
		return false, common.NewError("Some clients not found or access denied")
	}

	// Get inbound assignments before deleting
	var mappings []model.ClientInboundMapping
	err = db.Where("client_id IN ?", clientIds).Find(&mappings).Error
	if err != nil {
		return false, err
	}

	affectedInboundIds := make(map[int]bool)
	for _, mapping := range mappings {
		affectedInboundIds[mapping.InboundId] = true
	}

	needRestart := false

	tx := db.Begin()
	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()

	// Delete inbound mappings
	err = tx.Where("client_id IN ?", clientIds).Delete(&model.ClientInboundMapping{}).Error
	if err != nil {
		return false, err
	}

	// Delete clients
	err = tx.Where("id IN ? AND user_id = ?", clientIds, userId).Delete(&model.ClientEntity{}).Error
	if err != nil {
		return false, err
	}

	// Invalidate cache for this user's clients
	cache.InvalidateClients(userId)

	// Update Settings for affected inbounds
	inboundService := InboundService{}
	for inboundId := range affectedInboundIds {
		inbound, err := inboundService.GetInbound(inboundId)
		if err != nil {
			continue
		}

		clientEntities, err := s.GetClientsForInbound(inboundId)
		if err != nil {
			continue
		}

		newSettings, err := inboundService.BuildSettingsFromClientEntities(inbound, clientEntities)
		if err != nil {
			continue
		}

		inbound.Settings = newSettings
		_, inboundNeedRestart, err := inboundService.updateInboundWithRetry(inbound)
		if err != nil {
			logger.Warningf("Failed to update inbound %d settings: %v", inboundId, err)
		} else if inboundNeedRestart {
			needRestart = true
		}
	}

	return needRestart, nil
}

// BulkEnable enables or disables multiple clients.
// Returns whether Xray needs restart and any error.
func (s *ClientService) BulkEnable(userId int, clientIds []int, enable bool) (bool, error) {
	if len(clientIds) == 0 {
		return false, nil
	}

	// Verify all clients belong to user
	db := database.GetDB()
	var count int64
	err := db.Model(&model.ClientEntity{}).
		Where("id IN ? AND user_id = ?", clientIds, userId).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	if int(count) != len(clientIds) {
		return false, common.NewError("Some clients not found or access denied")
	}

	// Get inbound assignments
	var mappings []model.ClientInboundMapping
	err = db.Where("client_id IN ?", clientIds).Find(&mappings).Error
	if err != nil {
		return false, err
	}

	affectedInboundIds := make(map[int]bool)
	for _, mapping := range mappings {
		affectedInboundIds[mapping.InboundId] = true
	}

	// Update enable status
	err = db.Model(&model.ClientEntity{}).
		Where("id IN ? AND user_id = ?", clientIds, userId).
		Update("enable", enable).Error
	if err != nil {
		return false, err
	}

	// Invalidate cache for this user's clients
	cache.InvalidateClients(userId)

	// Use Xray API for instant user add/remove (both local and nodes)
	needRestart := false
	inboundService := InboundService{}
	settingService := SettingService{}
	multiMode, _ := settingService.GetMultiNodeMode()
	xrayService := XrayService{}
	// Get clients that are being enabled/disabled
	var clientsToUpdate []model.ClientEntity
	err = db.Where("id IN ? AND user_id = ?", clientIds, userId).Find(&clientsToUpdate).Error
	if err == nil {
		for _, client := range clientsToUpdate {
			// Get all inbound IDs for this client
			clientInboundIds, err := s.GetInboundIdsForClient(client.Id)
			if err == nil {
				for _, inboundId := range clientInboundIds {
					inbound, err := inboundService.GetInbound(inboundId)
					if err != nil {
						continue
					}

					if enable {
						// Enable: Add user via Xray API
						// Build client data for Xray API
						clientData := make(map[string]interface{})
						clientData["email"] = client.Email

						switch inbound.Protocol {
						case model.Trojan:
							clientData["password"] = client.Password
						case model.Shadowsocks:
							var settings map[string]interface{}
							json.Unmarshal([]byte(inbound.Settings), &settings)
							if method, ok := settings["method"].(string); ok {
								clientData["method"] = method
							}
							clientData["password"] = client.Password
						case model.VMESS, model.VLESS:
							clientData["id"] = client.UUID
							if inbound.Protocol == model.VMESS && client.Security != "" {
								clientData["security"] = client.Security
							}
							if inbound.Protocol == model.VLESS && client.Flow != "" {
								clientData["flow"] = client.Flow
							}
						}

						// Single mode: instantly update config.json
						if !multiMode {
							if xrayService.IsXrayRunning() {
								processConfig := xrayService.GetConfig()
								if processConfig != nil {
									if enable {
										// Instantly add client to config.json
										if err := xray.UpdateConfigFileAfterUserAddition(processConfig, inbound.Tag, clientData); err != nil {
											logger.Warningf("BulkEnable: failed to instantly add client %s to config.json: %v", client.Email, err)
											needRestart = true
										} else {
											logger.Infof("BulkEnable: instantly added client %s to config.json (inbound: %s)", client.Email, inbound.Tag)
											needRestart = true
										}
									} else {
										// Instantly remove client from config.json
										if err := xray.UpdateConfigFileAfterUserRemoval(processConfig, inbound.Tag, client.Email); err != nil {
											logger.Warningf("BulkEnable: failed to instantly remove client %s from config.json: %v", client.Email, err)
											needRestart = true
										} else {
											logger.Infof("BulkEnable: instantly removed client %s from config.json (inbound: %s)", client.Email, inbound.Tag)
											needRestart = true
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Update Settings for affected inbounds (needed to keep DB in sync)
	// But if API operations were successful, we don't need to send full config to nodes
	for inboundId := range affectedInboundIds {
		inbound, err := inboundService.GetInbound(inboundId)
		if err != nil {
			continue
		}

		clientEntities, err := s.GetClientsForInbound(inboundId)
		if err != nil {
			continue
		}

		newSettings, err := inboundService.BuildSettingsFromClientEntities(inbound, clientEntities)
		if err != nil {
			continue
		}

		// Update inbound Settings in DB (to keep database in sync)
		inbound.Settings = newSettings
		_, inboundNeedRestart, err := inboundService.updateInboundWithRetry(inbound)
		if err != nil {
			logger.Warningf("Failed to update inbound %d settings: %v", inboundId, err)
		} else if inboundNeedRestart {
			needRestart = true
		}
	}

	// Single mode: restart Xray asynchronously to apply changes
	if !multiMode && needRestart {
		logger.Debugf("BulkEnable: scheduling async restart to apply changes")
		xrayService := XrayService{}
		go func() {
			if err := xrayService.RestartXray(false); err != nil {
				logger.Warningf("BulkEnable: failed to restart Xray: %v", err)
			} else {
				logger.Debugf("BulkEnable: Xray restarted successfully (config synced)")
			}
		}()
		return false, nil // No need for synchronous restart
	}

	return needRestart, nil
}

// BulkSetHWIDLimit sets HWID limit for multiple clients.
func (s *ClientService) BulkSetHWIDLimit(userId int, clientIds []int, maxHwid int, enabled bool) error {
	if len(clientIds) == 0 {
		return nil
	}

	// Verify all clients belong to user
	db := database.GetDB()
	var count int64
	err := db.Model(&model.ClientEntity{}).
		Where("id IN ? AND user_id = ?", clientIds, userId).
		Count(&count).Error
	if err != nil {
		return err
	}
	if int(count) != len(clientIds) {
		return common.NewError("Some clients not found or access denied")
	}

	// Update HWID settings
	err = db.Model(&model.ClientEntity{}).
		Where("id IN ? AND user_id = ?", clientIds, userId).
		Updates(map[string]interface{}{
			"hwid_enabled": enabled,
			"max_hwid":     maxHwid,
		}).Error

	if err != nil {
		return err
	}

	// Invalidate cache for this user's clients
	cache.InvalidateClients(userId)

	return nil
}