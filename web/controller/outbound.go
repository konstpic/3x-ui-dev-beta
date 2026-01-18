package controller

import (
	"strconv"

	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/web/service"
	"github.com/mhsanaei/3x-ui/v2/web/session"

	"github.com/gin-gonic/gin"
)

// OutboundController handles HTTP requests related to Xray outbound configurations management.
type OutboundController struct {
	outboundService service.OutboundService
	nodeService     service.NodeService
	xrayService     service.XrayService
}

// NewOutboundController creates a new OutboundController and sets up its routes.
func NewOutboundController(g *gin.RouterGroup) *OutboundController {
	a := &OutboundController{}
	a.initRouter(g)
	return a
}

// initRouter initializes the routes for outbound-related operations.
func (a *OutboundController) initRouter(g *gin.RouterGroup) {
	g.GET("/list", a.getOutbounds)
	g.GET("/get/:id", a.getOutbound)
	g.POST("/add", a.addOutbound)
	g.POST("/del/:id", a.delOutbound)
	g.POST("/update/:id", a.updateOutbound)
}

// getOutbounds retrieves the list of outbounds for the logged-in user.
func (a *OutboundController) getOutbounds(c *gin.Context) {
	user := session.GetLoginUser(c)
	outbounds, err := a.outboundService.GetOutbounds(user.Id)
	if err != nil {
		jsonMsg(c, "Failed to get outbounds", err)
		return
	}
	jsonObj(c, outbounds, nil)
}

// getOutbound retrieves a specific outbound by its ID.
func (a *OutboundController) getOutbound(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid outbound ID", err)
		return
	}
	outbound, err := a.outboundService.GetOutbound(id)
	if err != nil {
		jsonMsg(c, "Failed to get outbound", err)
		return
	}
	jsonObj(c, outbound, nil)
}

// addOutbound creates a new outbound configuration.
func (a *OutboundController) addOutbound(c *gin.Context) {
	user := session.GetLoginUser(c)

	outbound := &model.Outbound{}
	
	// Try to parse as JSON first (for API calls)
	contentType := c.GetHeader("Content-Type")
	if contentType == "application/json" {
		err := c.ShouldBindJSON(outbound)
		if err != nil {
			logger.Errorf("Failed to bind outbound data: %v", err)
			jsonMsg(c, "Invalid outbound data", err)
			return
		}
	} else {
		// Parse as form data (default for web UI)
		err := c.ShouldBind(outbound)
		if err != nil {
			logger.Errorf("Failed to bind outbound data: %v", err)
			jsonMsg(c, "Invalid outbound data", err)
			return
		}
	}

	outbound.UserId = user.Id

	outbound, err := a.outboundService.AddOutbound(outbound)
	if err != nil {
		logger.Errorf("Failed to add outbound: %v", err)
		jsonMsg(c, "Failed to add outbound: "+err.Error(), err)
		return
	}

	// Handle core config profile assignment
	if coreConfigProfileIdStr := c.PostForm("coreConfigProfileId"); coreConfigProfileIdStr != "" {
		if coreConfigProfileId, err := strconv.Atoi(coreConfigProfileIdStr); err == nil && coreConfigProfileId > 0 {
			outbound.CoreConfigProfileId = &coreConfigProfileId
		}
	} else if contentType == "application/json" {
		// Try to get from JSON body
		var jsonData map[string]interface{}
		if err := c.ShouldBindJSON(&jsonData); err == nil {
			if coreConfigProfileIdVal, ok := jsonData["coreConfigProfileId"]; ok {
				if coreConfigProfileIdFloat, ok := coreConfigProfileIdVal.(float64); ok {
					coreConfigProfileId := int(coreConfigProfileIdFloat)
					if coreConfigProfileId > 0 {
						outbound.CoreConfigProfileId = &coreConfigProfileId
					}
				}
			}
		}
	}

	// Handle node assignment in multi-node mode
	var nodeIds []int
	nodeIdsStr := c.PostFormArray("nodeIds")
	if len(nodeIdsStr) > 0 {
		// Handle as array from form
		for _, nodeIdStr := range nodeIdsStr {
			if nodeId, err := strconv.Atoi(nodeIdStr); err == nil && nodeId > 0 {
				nodeIds = append(nodeIds, nodeId)
			}
		}
	} else if contentType == "application/json" {
		// Try to get from JSON body
		var jsonData map[string]interface{}
		if err := c.ShouldBindJSON(&jsonData); err == nil {
			if nodeIdsVal, ok := jsonData["nodeIds"].([]interface{}); ok {
				for _, nodeIdVal := range nodeIdsVal {
					if nodeId, ok := nodeIdVal.(float64); ok {
						nodeIds = append(nodeIds, int(nodeId))
					}
				}
			}
		}
	}

	if len(nodeIds) > 0 {
		err = a.nodeService.AssignOutboundToNodes(outbound.Id, nodeIds)
		if err != nil {
			logger.Warningf("Failed to assign outbound to nodes: %v", err)
			jsonMsg(c, "Failed to assign outbound to nodes: "+err.Error(), err)
			return
		}
	} else {
		// Remove all assignments if no nodes provided
		err = a.nodeService.UnassignOutboundFromNode(outbound.Id)
		if err != nil {
			logger.Warningf("Failed to unassign outbound from nodes: %v", err)
		}
	}

	// Restart Xray if needed (RestartXray will check internally if restart is needed)
	err = a.xrayService.RestartXray(false)
	if err != nil {
		logger.Warningf("Failed to restart Xray after adding outbound: %v", err)
	}

	logger.Infof("Outbound %d added successfully", outbound.Id)
	jsonMsgObj(c, "Outbound added successfully", outbound, nil)
}

// updateOutbound updates an existing outbound configuration.
func (a *OutboundController) updateOutbound(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid outbound ID", err)
		return
	}

	// Create outbound with only provided fields
	outbound := &model.Outbound{Id: id}

	// Try to parse as JSON first (for API calls)
	contentType := c.GetHeader("Content-Type")
	if contentType == "application/json" {
		var jsonData map[string]interface{}
		if err := c.ShouldBindJSON(&jsonData); err == nil {
			// Only set fields that are provided in JSON
			if remarkVal, ok := jsonData["remark"].(string); ok {
				outbound.Remark = remarkVal
			}
			if enableVal, ok := jsonData["enable"].(bool); ok {
				outbound.Enable = enableVal
			}
			if protocolVal, ok := jsonData["protocol"].(string); ok {
				outbound.Protocol = protocolVal
			}
			if settingsVal, ok := jsonData["settings"].(string); ok {
				outbound.Settings = settingsVal
			}
			if streamSettingsVal, ok := jsonData["streamSettings"].(string); ok {
				outbound.StreamSettings = streamSettingsVal
			}
			if tagVal, ok := jsonData["tag"].(string); ok {
				outbound.Tag = tagVal
			}
			if proxySettingsVal, ok := jsonData["proxySettings"].(string); ok {
				outbound.ProxySettings = proxySettingsVal
			}
			if sendThroughVal, ok := jsonData["sendThrough"].(string); ok {
				outbound.SendThrough = sendThroughVal
			}
			if muxVal, ok := jsonData["mux"].(string); ok {
				outbound.Mux = muxVal
			}
		}
	} else {
		// Parse as form data (default for web UI)
		if remark := c.PostForm("remark"); remark != "" {
			outbound.Remark = remark
		}
		outbound.Enable = c.PostForm("enable") == "true" || c.PostForm("enable") == "on"
		if protocol := c.PostForm("protocol"); protocol != "" {
			outbound.Protocol = protocol
		}
		if settings := c.PostForm("settings"); settings != "" {
			outbound.Settings = settings
		}
		if streamSettings := c.PostForm("streamSettings"); streamSettings != "" {
			outbound.StreamSettings = streamSettings
		}
		if tag := c.PostForm("tag"); tag != "" {
			outbound.Tag = tag
		}
		if proxySettings := c.PostForm("proxySettings"); proxySettings != "" {
			outbound.ProxySettings = proxySettings
		}
		if sendThrough := c.PostForm("sendThrough"); sendThrough != "" {
			outbound.SendThrough = sendThrough
		}
		if mux := c.PostForm("mux"); mux != "" {
			outbound.Mux = mux
		}
	}

	// Handle core config profile assignment
	if coreConfigProfileIdStr := c.PostForm("coreConfigProfileId"); coreConfigProfileIdStr != "" {
		if coreConfigProfileId, err := strconv.Atoi(coreConfigProfileIdStr); err == nil && coreConfigProfileId > 0 {
			outbound.CoreConfigProfileId = &coreConfigProfileId
		} else if coreConfigProfileIdStr == "" || coreConfigProfileIdStr == "0" {
			// Explicitly unset profile
			outbound.CoreConfigProfileId = nil
		}
	} else {
		// Try to get from JSON body
		var jsonData map[string]interface{}
		if err := c.ShouldBindJSON(&jsonData); err == nil {
			if coreConfigProfileIdVal, ok := jsonData["coreConfigProfileId"]; ok {
				if coreConfigProfileIdFloat, ok := coreConfigProfileIdVal.(float64); ok {
					coreConfigProfileId := int(coreConfigProfileIdFloat)
					if coreConfigProfileId > 0 {
						outbound.CoreConfigProfileId = &coreConfigProfileId
					} else {
						outbound.CoreConfigProfileId = nil
					}
				} else if coreConfigProfileIdVal == nil {
					outbound.CoreConfigProfileId = nil
				}
			}
		}
	}

	// Handle node assignment
	var nodeIds []int
	if _, exists := c.GetPostForm("nodeIds"); exists {
		// Handle as array from form
		nodeIdsStr := c.PostFormArray("nodeIds")
		for _, nodeIdStr := range nodeIdsStr {
			if nodeId, err := strconv.Atoi(nodeIdStr); err == nil && nodeId > 0 {
				nodeIds = append(nodeIds, nodeId)
			}
		}
	} else {
		// Try to get from JSON body
		var jsonData map[string]interface{}
		if err := c.ShouldBindJSON(&jsonData); err == nil {
			if nodeIdsVal, ok := jsonData["nodeIds"].([]interface{}); ok {
				for _, nodeIdVal := range nodeIdsVal {
					if nodeId, ok := nodeIdVal.(float64); ok {
						nodeIds = append(nodeIds, int(nodeId))
					}
				}
			}
		}
	}

	err = a.nodeService.AssignOutboundToNodes(id, nodeIds)
	if err != nil {
		logger.Warningf("Failed to assign outbound to nodes: %v", err)
		jsonMsg(c, "Failed to assign outbound to nodes: "+err.Error(), err)
		return
	}

	// Update outbound
	outbound, err = a.outboundService.UpdateOutbound(outbound)
	if err != nil {
		jsonMsg(c, "Failed to update outbound: "+err.Error(), err)
		return
	}

	// Restart Xray if needed (RestartXray will check internally if restart is needed)
	err = a.xrayService.RestartXray(false)
	if err != nil {
		logger.Warningf("Failed to restart Xray after updating outbound: %v", err)
	}

	jsonMsgObj(c, "Outbound updated successfully", outbound, nil)
}

// delOutbound deletes an outbound configuration by ID.
func (a *OutboundController) delOutbound(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid outbound ID", err)
		return
	}

	err = a.outboundService.DeleteOutbound(id)
	if err != nil {
		jsonMsg(c, "Failed to delete outbound: "+err.Error(), err)
		return
	}

	// Restart Xray if needed (RestartXray will check internally if restart is needed)
	err = a.xrayService.RestartXray(false)
	if err != nil {
		logger.Warningf("Failed to restart Xray after deleting outbound: %v", err)
	}

	jsonMsg(c, "Outbound deleted successfully", nil)
}
