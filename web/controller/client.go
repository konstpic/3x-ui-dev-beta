// Package controller provides HTTP handlers for client management.
package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/web/service"
	"github.com/mhsanaei/3x-ui/v2/web/session"

	"github.com/gin-gonic/gin"
)

// ClientController handles HTTP requests related to client management.
type ClientController struct {
	clientService service.ClientService
	xrayService   service.XrayService
}

// NewClientController creates a new ClientController and sets up its routes.
func NewClientController(g *gin.RouterGroup) *ClientController {
	a := &ClientController{
		clientService: service.ClientService{},
		xrayService:   service.XrayService{},
	}
	a.initRouter(g)
	return a
}

// initRouter initializes the routes for client-related operations.
func (a *ClientController) initRouter(g *gin.RouterGroup) {
	g.GET("/list", a.getClients)
	g.GET("/get/:id", a.getClient)
	g.POST("/add", a.addClient)
	g.POST("/update/:id", a.updateClient)
	g.POST("/del/:id", a.deleteClient)
	g.POST("/resetAllTraffics", a.resetAllClientTraffics)
	g.POST("/resetTraffic/:id", a.resetClientTraffic)
	g.POST("/delDepletedClients", a.delDepletedClients)
	// HWID operations
	g.POST("/clearHwid/:id", a.clearClientHWIDs)
	g.POST("/clearAllHwids", a.clearAllClientHWIDs)
	g.POST("/setHwidLimitAll", a.setHWIDLimitForAllClients)
	// Bulk operations
	g.POST("/bulk/resetTraffic", a.bulkResetTraffic)
	g.POST("/bulk/clearHwid", a.bulkClearHwid)
	g.POST("/bulk/delete", a.bulkDelete)
	g.POST("/bulk/enable", a.bulkEnable)
	g.POST("/bulk/setHwidLimit", a.bulkSetHwidLimit)
}

// getClients retrieves the list of all clients for the current user.
func (a *ClientController) getClients(c *gin.Context) {
	user := session.GetLoginUser(c)
	clients, err := a.clientService.GetClients(user.Id)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, clients, nil)
}

// getClient retrieves a specific client by its ID.
func (a *ClientController) getClient(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid client ID", err)
		return
	}
	user := session.GetLoginUser(c)
	client, err := a.clientService.GetClient(id)
	if err != nil {
		jsonMsg(c, "Failed to get client", err)
		return
	}
	if client.UserId != user.Id {
		jsonMsg(c, "Client not found or access denied", nil)
		return
	}
	jsonObj(c, client, nil)
}

// addClient creates a new client.
func (a *ClientController) addClient(c *gin.Context) {
	user := session.GetLoginUser(c)
	
	// Extract inboundIds and groupId from JSON or form data
	var inboundIdsFromJSON []int
	var hasInboundIdsInJSON bool
	var groupIdFromJSON *int
	var hasGroupIdInJSON bool
	
	if c.ContentType() == "application/json" {
		// Read raw body to extract inboundIds and groupId
		bodyBytes, err := c.GetRawData()
		if err == nil && len(bodyBytes) > 0 {
			// Parse JSON to extract inboundIds and groupId
			var jsonData map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &jsonData); err == nil {
				// Check for inboundIds array
				if inboundIdsVal, ok := jsonData["inboundIds"]; ok {
					hasInboundIdsInJSON = true
					if inboundIdsArray, ok := inboundIdsVal.([]interface{}); ok {
						for _, val := range inboundIdsArray {
							if num, ok := val.(float64); ok {
								inboundIdsFromJSON = append(inboundIdsFromJSON, int(num))
							} else if num, ok := val.(int); ok {
								inboundIdsFromJSON = append(inboundIdsFromJSON, num)
							}
						}
					} else if num, ok := inboundIdsVal.(float64); ok {
						// Single number instead of array
						inboundIdsFromJSON = append(inboundIdsFromJSON, int(num))
					} else if num, ok := inboundIdsVal.(int); ok {
						inboundIdsFromJSON = append(inboundIdsFromJSON, num)
					}
				}
				// Check for groupId
				if groupIdVal, ok := jsonData["groupId"]; ok {
					hasGroupIdInJSON = true
					if groupIdVal == nil {
						// Explicitly null - no group
						groupIdFromJSON = nil
					} else if num, ok := groupIdVal.(float64); ok && num > 0 {
						groupIdInt := int(num)
						groupIdFromJSON = &groupIdInt
					} else if num, ok := groupIdVal.(int); ok && num > 0 {
						groupIdFromJSON = &num
					}
				}
			}
			// Restore body for ShouldBind
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}
	}
	
	client := &model.ClientEntity{}
	err := c.ShouldBind(client)
	if err != nil {
		jsonMsg(c, "Invalid client data", err)
		return
	}
	
	// Set inboundIds from JSON if available
	if hasInboundIdsInJSON {
		client.InboundIds = inboundIdsFromJSON
	} else {
		// Try to get from form data
		inboundIdsStr := c.PostFormArray("inboundIds")
		if len(inboundIdsStr) > 0 {
			var inboundIds []int
			for _, idStr := range inboundIdsStr {
				if idStr != "" {
					if id, err := strconv.Atoi(idStr); err == nil && id > 0 {
						inboundIds = append(inboundIds, id)
					}
				}
			}
			client.InboundIds = inboundIds
		}
	}

	// Handle groupId - from JSON or form data
	if hasGroupIdInJSON {
		// Use groupId from JSON (can be nil)
		client.GroupId = groupIdFromJSON
	} else {
		// Try to get from form data
		if groupIdStr := c.PostForm("groupId"); groupIdStr != "" {
			if groupId, err := strconv.Atoi(groupIdStr); err == nil && groupId > 0 {
				client.GroupId = &groupId
			} else {
				// Invalid value, set to nil
				client.GroupId = nil
			}
		} else {
			// No groupId provided - explicitly set to nil (no group)
			client.GroupId = nil
		}
	}

	needRestart, err := a.clientService.AddClient(user.Id, client)
	if err != nil {
		logger.Errorf("Failed to add client: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}

	jsonMsgObj(c, I18nWeb(c, "pages.clients.toasts.clientCreateSuccess"), client, nil)
	if needRestart {
		// In multi-node mode, this will send config to nodes immediately
		// In single mode, this will restart local Xray
		if err := a.xrayService.RestartXray(false); err != nil {
			logger.Warningf("Failed to restart Xray after client creation: %v", err)
		}
	}
}

// updateClient updates an existing client.
func (a *ClientController) updateClient(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid client ID", err)
		return
	}

	user := session.GetLoginUser(c)
	
	// Get existing client first to preserve fields not being updated
	existing, err := a.clientService.GetClient(id)
	if err != nil {
		jsonMsg(c, "Client not found", err)
		return
	}
	if existing.UserId != user.Id {
		jsonMsg(c, "Client not found or access denied", nil)
		return
	}
	
	// Extract inboundIds from JSON or form data
	var inboundIdsFromJSON []int
	var hasInboundIdsInJSON bool
	
	if c.ContentType() == "application/json" {
		// Read raw body to extract inboundIds
		bodyBytes, err := c.GetRawData()
		if err == nil && len(bodyBytes) > 0 {
			// Parse JSON to extract inboundIds
			var jsonData map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &jsonData); err == nil {
				// Check for inboundIds array
				if inboundIdsVal, ok := jsonData["inboundIds"]; ok {
					hasInboundIdsInJSON = true
					if inboundIdsArray, ok := inboundIdsVal.([]interface{}); ok {
						for _, val := range inboundIdsArray {
							if num, ok := val.(float64); ok {
								inboundIdsFromJSON = append(inboundIdsFromJSON, int(num))
							} else if num, ok := val.(int); ok {
								inboundIdsFromJSON = append(inboundIdsFromJSON, num)
							}
						}
					} else if num, ok := inboundIdsVal.(float64); ok {
						// Single number instead of array
						inboundIdsFromJSON = append(inboundIdsFromJSON, int(num))
					} else if num, ok := inboundIdsVal.(int); ok {
						inboundIdsFromJSON = append(inboundIdsFromJSON, num)
					}
				}
			}
			// Restore body for ShouldBind
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}
	}
	
	// Use existing client as base and update only provided fields
	client := existing
	
	// Try to bind only provided fields - use ShouldBindJSON for JSON requests
	if c.ContentType() == "application/json" {
		var updateData map[string]interface{}
		if err := c.ShouldBindJSON(&updateData); err == nil {
			// Update only fields that are present in the request
			if email, ok := updateData["email"].(string); ok && email != "" {
				client.Email = email
			}
			if uuid, ok := updateData["uuid"].(string); ok && uuid != "" {
				client.UUID = uuid
			}
			if security, ok := updateData["security"].(string); ok && security != "" {
				client.Security = security
			}
			if password, ok := updateData["password"].(string); ok && password != "" {
				client.Password = password
			}
			if flow, ok := updateData["flow"].(string); ok && flow != "" {
				client.Flow = flow
			}
			// Handle limitIp - can be 0 (unlimited), so check if key exists
			if limitIPVal, exists := updateData["limitIp"]; exists {
				if limitIP, ok := limitIPVal.(float64); ok {
					client.LimitIP = int(limitIP)
				} else if limitIP, ok := limitIPVal.(int); ok {
					client.LimitIP = limitIP
				}
			}
			// Handle totalGB - can be 0 (unlimited), so check if key exists
			if totalGBVal, exists := updateData["totalGB"]; exists {
				if totalGB, ok := totalGBVal.(float64); ok {
					client.TotalGB = totalGB
				} else if totalGB, ok := totalGBVal.(int); ok {
					client.TotalGB = float64(totalGB)
				} else if totalGB, ok := totalGBVal.(int64); ok {
					client.TotalGB = float64(totalGB)
				}
			}
			// Handle expiryTime - can be 0 (never expires), so check if key exists
			if expiryTimeVal, exists := updateData["expiryTime"]; exists {
				if expiryTime, ok := expiryTimeVal.(float64); ok {
					client.ExpiryTime = int64(expiryTime)
				} else if expiryTime, ok := expiryTimeVal.(int64); ok {
					client.ExpiryTime = expiryTime
				} else if expiryTime, ok := expiryTimeVal.(int); ok {
					client.ExpiryTime = int64(expiryTime)
				}
			}
			if enable, ok := updateData["enable"].(bool); ok {
				client.Enable = enable
			}
			if tgID, ok := updateData["tgId"].(float64); ok {
				client.TgID = int64(tgID)
			} else if tgID, ok := updateData["tgId"].(int64); ok {
				client.TgID = tgID
			}
			// Handle subId - check if key exists (can be empty to clear)
			if subIDVal, exists := updateData["subId"]; exists {
				if subID, ok := subIDVal.(string); ok {
					client.SubID = subID
				}
			}
			// Handle comment - check if key exists (can be empty to clear)
			if commentVal, exists := updateData["comment"]; exists {
				if comment, ok := commentVal.(string); ok {
					client.Comment = comment
				}
			}
			// Handle reset - can be 0 (disabled), so check if key exists
			if resetVal, exists := updateData["reset"]; exists {
				if reset, ok := resetVal.(float64); ok {
					client.Reset = int(reset)
				} else if reset, ok := resetVal.(int); ok {
					client.Reset = reset
				}
			}
			if hwidEnabled, ok := updateData["hwidEnabled"].(bool); ok {
				client.HWIDEnabled = hwidEnabled
			}
			// Handle maxHwid - can be 0 (unlimited), so check if key exists, not just value
			if maxHwidVal, exists := updateData["maxHwid"]; exists {
				if maxHwid, ok := maxHwidVal.(float64); ok {
					client.MaxHWID = int(maxHwid)
				} else if maxHwid, ok := maxHwidVal.(int); ok {
					client.MaxHWID = maxHwid
				} else if maxHwid, ok := maxHwidVal.(int64); ok {
					client.MaxHWID = int(maxHwid)
				}
			}
			// Handle groupId - can be null (no group), so check if key exists
			if groupIdVal, exists := updateData["groupId"]; exists {
				if groupIdVal == nil {
					client.GroupId = nil
				} else if groupId, ok := groupIdVal.(float64); ok {
					groupIdInt := int(groupId)
					client.GroupId = &groupIdInt
				} else if groupId, ok := groupIdVal.(int); ok {
					client.GroupId = &groupId
				}
			}
		}
	} else {
		// For form data, use ShouldBind
		updateClient := &model.ClientEntity{}
		if err := c.ShouldBind(updateClient); err == nil {
			// Update only non-empty fields
			if updateClient.Email != "" {
				client.Email = updateClient.Email
			}
			if updateClient.UUID != "" {
				client.UUID = updateClient.UUID
			}
			if updateClient.Security != "" {
				client.Security = updateClient.Security
			}
			if updateClient.Password != "" {
				client.Password = updateClient.Password
			}
		if updateClient.Flow != "" {
			client.Flow = updateClient.Flow
		}
		// Handle limitIp - can be 0 (unlimited)
		limitIpStr := c.PostForm("limitIp")
		if limitIpStr != "" {
			if limitIp, err := strconv.Atoi(limitIpStr); err == nil {
				client.LimitIP = limitIp
			}
		}
		// Handle totalGB - can be 0 (unlimited)
		totalGBStr := c.PostForm("totalGB")
		if totalGBStr != "" {
			if totalGB, err := strconv.ParseFloat(totalGBStr, 64); err == nil {
				client.TotalGB = totalGB
			}
		}
		// Handle expiryTime - can be 0 (never expires)
		expiryTimeStr := c.PostForm("expiryTime")
		if expiryTimeStr != "" {
			if expiryTime, err := strconv.ParseInt(expiryTimeStr, 10, 64); err == nil {
				client.ExpiryTime = expiryTime
			}
		}
		// Always update enable if it's in the request (even if false)
		enableStr := c.PostForm("enable")
		if enableStr != "" {
			client.Enable = enableStr == "true" || enableStr == "1"
		}
		// Handle tgId - can be 0
		tgIdStr := c.PostForm("tgId")
		if tgIdStr != "" {
			if tgId, err := strconv.ParseInt(tgIdStr, 10, 64); err == nil {
				client.TgID = tgId
			}
		}
		// Handle subId - can be empty to clear
		if subIdVal, exists := c.GetPostForm("subId"); exists {
			client.SubID = subIdVal
		}
		// Handle comment - can be empty to clear
		if commentVal, exists := c.GetPostForm("comment"); exists {
			client.Comment = commentVal
		}
		// Handle reset - can be 0 (disabled)
		resetStr := c.PostForm("reset")
		if resetStr != "" {
			if reset, err := strconv.Atoi(resetStr); err == nil {
				client.Reset = reset
			}
		}
			// Always update hwidEnabled if it's in the request (even if false)
			hwidEnabledStr := c.PostForm("hwidEnabled")
			if hwidEnabledStr != "" {
				client.HWIDEnabled = hwidEnabledStr == "true" || hwidEnabledStr == "1"
			}
			// Always update maxHwid if it's in the request (including 0 for unlimited)
			maxHwidStr := c.PostForm("maxHwid")
			if maxHwidStr != "" {
				if maxHwid, err := strconv.Atoi(maxHwidStr); err == nil {
					client.MaxHWID = maxHwid
				}
			} else if updateClient.MaxHWID >= 0 {
				// If maxHwid is explicitly set in the form (including 0), use it
				client.MaxHWID = updateClient.MaxHWID
			}
			// Handle groupId - can be empty (no group)
			if groupIdStr := c.PostForm("groupId"); groupIdStr != "" {
				if groupId, err := strconv.Atoi(groupIdStr); err == nil && groupId > 0 {
					client.GroupId = &groupId
				}
			} else if groupIdStr, exists := c.GetPostForm("groupId"); exists && groupIdStr == "" {
				// Explicitly set to null (remove from group)
				client.GroupId = nil
			}
		}
	}
	
	// Set inboundIds from JSON if available
	if hasInboundIdsInJSON {
		client.InboundIds = inboundIdsFromJSON
		logger.Debugf("UpdateClient: extracted inboundIds from JSON: %v", inboundIdsFromJSON)
	} else {
		// Try to get from form data
		inboundIdsStr := c.PostFormArray("inboundIds")
		if len(inboundIdsStr) > 0 {
			var inboundIds []int
			for _, idStr := range inboundIdsStr {
				if idStr != "" {
					if id, err := strconv.Atoi(idStr); err == nil && id > 0 {
						inboundIds = append(inboundIds, id)
					}
				}
			}
			client.InboundIds = inboundIds
			logger.Debugf("UpdateClient: extracted inboundIds from form: %v", inboundIds)
		} else {
			logger.Debugf("UpdateClient: inboundIds not provided, keeping existing assignments")
		}
	}

	client.Id = id
	logger.Debugf("UpdateClient: client.InboundIds = %v", client.InboundIds)
	needRestart, err := a.clientService.UpdateClient(user.Id, client)
	if err != nil {
		logger.Errorf("Failed to update client: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}

	jsonMsgObj(c, I18nWeb(c, "pages.clients.toasts.clientUpdateSuccess"), client, nil)
	if needRestart {
		// In multi-node mode, this will send config to nodes immediately
		// In single mode, this will restart local Xray
		if err := a.xrayService.RestartXray(false); err != nil {
			logger.Warningf("Failed to restart Xray after client update: %v", err)
		}
	}
}

// deleteClient deletes a client by ID.
func (a *ClientController) deleteClient(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid client ID", err)
		return
	}

	user := session.GetLoginUser(c)
	needRestart, err := a.clientService.DeleteClient(user.Id, id)
	if err != nil {
		logger.Errorf("Failed to delete client: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}

	jsonMsg(c, I18nWeb(c, "pages.clients.toasts.clientDeleteSuccess"), nil)
	if needRestart {
		// In multi-node mode, this will send config to nodes immediately
		// In single mode, this will restart local Xray
		if err := a.xrayService.RestartXray(false); err != nil {
			logger.Warningf("Failed to restart Xray after client deletion: %v", err)
		}
	}
}

// resetAllClientTraffics resets traffic counters for all clients of the current user.
func (a *ClientController) resetAllClientTraffics(c *gin.Context) {
	user := session.GetLoginUser(c)
	needRestart, err := a.clientService.ResetAllClientTraffics(user.Id)
	if err != nil {
		logger.Errorf("Failed to reset all client traffics: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.resetAllClientTrafficSuccess"), nil)
	if needRestart {
		// In multi-node mode, this will send config to nodes immediately
		// In single mode, this will restart local Xray
		if err := a.xrayService.RestartXray(false); err != nil {
			logger.Warningf("Failed to restart Xray after resetting all client traffics: %v", err)
		}
	}
}

// resetClientTraffic resets traffic counter for a specific client.
func (a *ClientController) resetClientTraffic(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid client ID", err)
		return
	}

	user := session.GetLoginUser(c)
	needRestart, err := a.clientService.ResetClientTraffic(user.Id, id)
	if err != nil {
		logger.Errorf("Failed to reset client traffic: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}

	jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.resetInboundClientTrafficSuccess"), nil)
	if needRestart {
		// In multi-node mode, this will send config to nodes immediately
		// In single mode, this will restart local Xray
		if err := a.xrayService.RestartXray(false); err != nil {
			logger.Warningf("Failed to restart Xray after client traffic reset: %v", err)
		}
	}
}

// delDepletedClients deletes clients that have exhausted their traffic limits or expired.
func (a *ClientController) delDepletedClients(c *gin.Context) {
	user := session.GetLoginUser(c)
	count, needRestart, err := a.clientService.DelDepletedClients(user.Id)
	if err != nil {
		logger.Errorf("Failed to delete depleted clients: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	
	if count > 0 {
		jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.delDepletedClientsSuccess"), nil)
		if needRestart {
			// In multi-node mode, this will send config to nodes immediately
			// In single mode, this will restart local Xray
			if err := a.xrayService.RestartXray(false); err != nil {
				logger.Warningf("Failed to restart Xray after deleting depleted clients: %v", err)
			}
		}
	} else {
		jsonMsg(c, "No depleted clients found", nil)
	}
}

// clearClientHWIDs clears all HWIDs for a specific client.
func (a *ClientController) clearClientHWIDs(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid client ID", err)
		return
	}

	user := session.GetLoginUser(c)
	// Verify client belongs to user
	client, err := a.clientService.GetClient(id)
	if err != nil || client == nil || client.UserId != user.Id {
		jsonMsg(c, "Client not found or access denied", nil)
		return
	}

	hwidService := service.ClientHWIDService{}
	err = hwidService.ClearHWIDsForClient(id)
	if err != nil {
		logger.Errorf("Failed to clear HWIDs for client %d: %v", id, err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}

	jsonMsg(c, "HWIDs cleared successfully", nil)
}

// clearAllClientHWIDs clears all HWIDs for all clients of the current user.
func (a *ClientController) clearAllClientHWIDs(c *gin.Context) {
	user := session.GetLoginUser(c)
	
	hwidService := service.ClientHWIDService{}
	count, err := hwidService.ClearAllHWIDs(user.Id)
	if err != nil {
		logger.Errorf("Failed to clear all HWIDs: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}

	jsonMsg(c, fmt.Sprintf("Cleared %d HWIDs successfully", count), nil)
}

// setHWIDLimitForAllClients sets HWID limit for all clients of the current user.
func (a *ClientController) setHWIDLimitForAllClients(c *gin.Context) {
	var req struct {
		MaxHwid int  `json:"maxHwid" form:"maxHwid"`
		Enabled bool `json:"enabled" form:"enabled"`
	}
	
	if err := c.ShouldBind(&req); err != nil {
		jsonMsg(c, "Invalid request", err)
		return
	}

	user := session.GetLoginUser(c)
	
	hwidService := service.ClientHWIDService{}
	count, err := hwidService.SetHWIDLimitForAllClients(user.Id, req.MaxHwid, req.Enabled)
	if err != nil {
		logger.Errorf("Failed to set HWID limit for all clients: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}

	jsonMsg(c, fmt.Sprintf("Updated HWID limit for %d clients", count), nil)
}

// bulkResetTraffic resets traffic for selected clients.
func (a *ClientController) bulkResetTraffic(c *gin.Context) {
	user := session.GetLoginUser(c)
	var req struct {
		ClientIds []int `json:"clientIds" form:"clientIds"`
	}
	err := c.ShouldBind(&req)
	if err != nil {
		jsonMsg(c, "Invalid request data", err)
		return
	}
	needRestart, err := a.clientService.BulkResetTraffic(user.Id, req.ClientIds)
	if err != nil {
		logger.Errorf("Failed to reset traffic for clients: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "Traffic reset successfully", nil)
	if needRestart {
		if err := a.xrayService.RestartXray(false); err != nil {
			logger.Warningf("Failed to restart Xray after bulk traffic reset: %v", err)
		}
	}
}

// bulkClearHwid clears HWIDs for selected clients.
func (a *ClientController) bulkClearHwid(c *gin.Context) {
	user := session.GetLoginUser(c)
	var req struct {
		ClientIds []int `json:"clientIds" form:"clientIds"`
	}
	err := c.ShouldBind(&req)
	if err != nil {
		jsonMsg(c, "Invalid request data", err)
		return
	}
	err = a.clientService.BulkClearHWIDs(user.Id, req.ClientIds)
	if err != nil {
		logger.Errorf("Failed to clear HWIDs for clients: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "HWIDs cleared successfully", nil)
}

// bulkDelete deletes selected clients.
func (a *ClientController) bulkDelete(c *gin.Context) {
	user := session.GetLoginUser(c)
	var req struct {
		ClientIds []int `json:"clientIds" form:"clientIds"`
	}
	err := c.ShouldBind(&req)
	if err != nil {
		jsonMsg(c, "Invalid request data", err)
		return
	}
	needRestart, err := a.clientService.BulkDelete(user.Id, req.ClientIds)
	if err != nil {
		logger.Errorf("Failed to delete clients: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "Clients deleted successfully", nil)
	if needRestart {
		if err := a.xrayService.RestartXray(false); err != nil {
			logger.Warningf("Failed to restart Xray after bulk deletion: %v", err)
		}
	}
}

// bulkEnable enables or disables selected clients.
func (a *ClientController) bulkEnable(c *gin.Context) {
	user := session.GetLoginUser(c)
	var req struct {
		ClientIds []int `json:"clientIds" form:"clientIds"`
		Enable    bool  `json:"enable" form:"enable"`
	}
	err := c.ShouldBind(&req)
	if err != nil {
		jsonMsg(c, "Invalid request data", err)
		return
	}
	needRestart, err := a.clientService.BulkEnable(user.Id, req.ClientIds, req.Enable)
	if err != nil {
		logger.Errorf("Failed to enable/disable clients: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "Clients updated successfully", nil)
	if needRestart {
		if err := a.xrayService.RestartXray(false); err != nil {
			logger.Warningf("Failed to restart Xray after bulk enable/disable: %v", err)
		}
	}
}

// bulkSetHwidLimit sets HWID limit for selected clients.
func (a *ClientController) bulkSetHwidLimit(c *gin.Context) {
	user := session.GetLoginUser(c)
	var req struct {
		ClientIds []int `json:"clientIds" form:"clientIds"`
		MaxHwid   int   `json:"maxHwid" form:"maxHwid"`
		Enabled   bool  `json:"enabled" form:"enabled"`
	}
	err := c.ShouldBind(&req)
	if err != nil {
		jsonMsg(c, "Invalid request data", err)
		return
	}
	err = a.clientService.BulkSetHWIDLimit(user.Id, req.ClientIds, req.MaxHwid, req.Enabled)
	if err != nil {
		logger.Errorf("Failed to set HWID limit for clients: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "HWID limit set successfully", nil)
}
