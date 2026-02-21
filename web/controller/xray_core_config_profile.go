package controller

import (
	"strconv"

	"github.com/konstpic/sharx/v2/database/model"
	"github.com/konstpic/sharx/v2/logger"
	"github.com/konstpic/sharx/v2/web/service"
	"github.com/konstpic/sharx/v2/web/session"

	"github.com/gin-gonic/gin"
)

// XrayCoreConfigProfileController handles HTTP requests related to Xray core configuration profile management.
type XrayCoreConfigProfileController struct {
	profileService service.XrayCoreConfigProfileService
	xrayService    service.XrayService
}

// NewXrayCoreConfigProfileController creates a new XrayCoreConfigProfileController and sets up its routes.
func NewXrayCoreConfigProfileController(g *gin.RouterGroup) *XrayCoreConfigProfileController {
	a := &XrayCoreConfigProfileController{}
	a.initRouter(g)
	return a
}

// initRouter initializes the routes for profile-related operations.
func (a *XrayCoreConfigProfileController) initRouter(g *gin.RouterGroup) {
	g.GET("/list", a.getProfiles)
	g.GET("/get/:id", a.getProfile)
	g.POST("/add", a.addProfile)
	g.POST("/update/:id", a.updateProfile)
	g.POST("/del/:id", a.deleteProfile)
	g.POST("/set-default/:id", a.setAsDefault)
	g.POST("/reset-to-default/:id", a.resetToDefault)
	g.POST("/assign-nodes/:id", a.assignNodes)
}

// getProfiles retrieves the list of profiles for the logged-in user.
// Automatically creates a default profile if none exists.
func (a *XrayCoreConfigProfileController) getProfiles(c *gin.Context) {
	user := session.GetLoginUser(c)
	
	// Ensure default profile exists
	_, err := a.profileService.EnsureDefaultProfile(user.Id)
	if err != nil {
		logger.Warningf("Failed to ensure default profile: %v", err)
		// Continue anyway - try to get existing profiles
	}
	
	profiles, err := a.profileService.GetAllProfiles(user.Id)
	if err != nil {
		jsonMsg(c, "Failed to get profiles", err)
		return
	}
	jsonObj(c, profiles, nil)
}

// getProfile retrieves a specific profile by its ID.
func (a *XrayCoreConfigProfileController) getProfile(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid profile ID", err)
		return
	}
	profile, err := a.profileService.GetProfile(id)
	if err != nil {
		jsonMsg(c, "Failed to get profile", err)
		return
	}
	jsonObj(c, profile, nil)
}

// addProfile creates a new profile.
func (a *XrayCoreConfigProfileController) addProfile(c *gin.Context) {
	user := session.GetLoginUser(c)

	profile := &model.XrayCoreConfigProfile{}

	// Try to parse as JSON first (for API calls)
	contentType := c.GetHeader("Content-Type")
	if contentType == "application/json" {
		err := c.ShouldBindJSON(profile)
		if err != nil {
			logger.Errorf("Failed to bind profile data: %v", err)
			jsonMsg(c, "Invalid profile data", err)
			return
		}
	} else {
		// Parse as form data (default for web UI)
		err := c.ShouldBind(profile)
		if err != nil {
			logger.Errorf("Failed to bind profile data: %v", err)
			jsonMsg(c, "Invalid profile data", err)
			return
		}
	}

	profile.UserId = user.Id

	profile, err := a.profileService.AddProfile(profile)
	if err != nil {
		logger.Errorf("Failed to add profile: %v", err)
		jsonMsg(c, "Failed to add profile: "+err.Error(), err)
		return
	}

	logger.Infof("Profile %d added successfully", profile.Id)
	jsonMsgObj(c, "Profile added successfully", profile, nil)
}

// updateProfile updates an existing profile.
func (a *XrayCoreConfigProfileController) updateProfile(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid profile ID", err)
		return
	}

	user := session.GetLoginUser(c)

	// Create profile with only provided fields
	profile := &model.XrayCoreConfigProfile{Id: id, UserId: user.Id}

	// Try to parse as JSON first (for API calls)
	contentType := c.GetHeader("Content-Type")
	if contentType == "application/json" {
		var jsonData map[string]interface{}
		if err := c.ShouldBindJSON(&jsonData); err == nil {
			// Only set fields that are provided in JSON
			if nameVal, ok := jsonData["name"].(string); ok {
				profile.Name = nameVal
			}
			if descVal, ok := jsonData["description"].(string); ok {
				profile.Description = descVal
			}
			if configVal, ok := jsonData["configJson"].(string); ok {
				profile.ConfigJson = configVal
			}
			if isDefaultVal, ok := jsonData["isDefault"].(bool); ok {
				profile.IsDefault = isDefaultVal
			}
		}
	} else {
		// Parse as form data (default for web UI)
		if name := c.PostForm("name"); name != "" {
			profile.Name = name
		}
		if description := c.PostForm("description"); description != "" {
			profile.Description = description
		}
		if configJson := c.PostForm("configJson"); configJson != "" {
			profile.ConfigJson = configJson
		}
		profile.IsDefault = c.PostForm("isDefault") == "true" || c.PostForm("isDefault") == "on"
	}

	// Update profile
	profile, err = a.profileService.UpdateProfile(profile)
	if err != nil {
		jsonMsg(c, "Failed to update profile: "+err.Error(), err)
		return
	}

	jsonMsgObj(c, "Profile updated successfully", profile, nil)
}

// deleteProfile deletes a profile by ID.
func (a *XrayCoreConfigProfileController) deleteProfile(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid profile ID", err)
		return
	}

	err = a.profileService.DeleteProfile(id)
	if err != nil {
		jsonMsg(c, "Failed to delete profile: "+err.Error(), err)
		return
	}

	jsonMsg(c, "Profile deleted successfully", nil)
}

// assignNodes assigns nodes to a profile.
func (a *XrayCoreConfigProfileController) assignNodes(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid profile ID", err)
		return
	}

	// Get node IDs from request
	var nodeIds []int
	if nodeIdsStr := c.PostForm("nodeIds"); nodeIdsStr != "" {
		// Handle as array from form
		nodeIdsArray := c.PostFormArray("nodeIds")
		for _, nodeIdStr := range nodeIdsArray {
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

	err = a.profileService.AssignProfileToNodes(id, nodeIds)
	if err != nil {
		jsonMsg(c, "Failed to assign nodes to profile", err)
		return
	}

	// Apply updated config to affected nodes
	err = a.xrayService.RestartXray(false)
	if err != nil {
		logger.Warningf("Failed to apply config to nodes after profile assignment: %v", err)
		// Don't fail the request, just log the warning
	}

	jsonMsg(c, "Nodes assigned successfully", nil)
}

// setAsDefault sets a profile as the default for the logged-in user.
func (a *XrayCoreConfigProfileController) setAsDefault(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid profile ID", err)
		return
	}

	user := session.GetLoginUser(c)
	err = a.profileService.SetAsDefault(id, user.Id)
	if err != nil {
		jsonMsg(c, "Failed to set profile as default: "+err.Error(), err)
		return
	}

	jsonMsg(c, "Profile set as default successfully", nil)
}

// resetToDefault resets a profile to the default template configuration.
func (a *XrayCoreConfigProfileController) resetToDefault(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid profile ID", err)
		return
	}

	profile, err := a.profileService.ResetToDefault(id)
	if err != nil {
		jsonMsg(c, "Failed to reset profile: "+err.Error(), err)
		return
	}

	jsonMsgObj(c, "Profile reset to default successfully", profile, nil)
}
