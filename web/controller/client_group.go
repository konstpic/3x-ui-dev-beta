// Package controller provides HTTP handlers for client group management.
package controller

import (
	"strconv"

	"github.com/konstpic/sharx/v2/database/model"
	"github.com/konstpic/sharx/v2/logger"
	"github.com/konstpic/sharx/v2/web/service"
	"github.com/konstpic/sharx/v2/web/session"

	"github.com/gin-gonic/gin"
)

// ClientGroupController handles HTTP requests related to client group management.
type ClientGroupController struct {
	groupService  service.ClientGroupService
	clientService service.ClientService
	xrayService   service.XrayService
}

// NewClientGroupController creates a new ClientGroupController and sets up its routes.
func NewClientGroupController(g *gin.RouterGroup) *ClientGroupController {
	a := &ClientGroupController{
		groupService:  service.ClientGroupService{},
		clientService: service.ClientService{},
		xrayService:   service.XrayService{},
	}
	a.initRouter(g)
	return a
}

// initRouter initializes the routes for group-related operations.
func (a *ClientGroupController) initRouter(g *gin.RouterGroup) {
	g.GET("/list", a.getGroups)
	g.GET("/get/:id", a.getGroup)
	g.POST("/add", a.addGroup)
	g.POST("/update/:id", a.updateGroup)
	g.POST("/del/:id", a.deleteGroup)
	g.GET("/:id/clients", a.getClientsInGroup)
	g.POST("/:id/assignClients", a.assignClientsToGroup)
	g.POST("/:id/removeClients", a.removeClientsFromGroup)
	// Bulk operations for groups
	g.POST("/:id/bulk/resetTraffic", a.bulkResetTraffic)
	g.POST("/:id/bulk/clearHwid", a.bulkClearHwid)
	g.POST("/:id/bulk/delete", a.bulkDelete)
	g.POST("/:id/bulk/enable", a.bulkEnable)
	g.POST("/:id/bulk/setHwidLimit", a.bulkSetHwidLimit)
	g.POST("/:id/bulk/assignInbounds", a.bulkAssignInbounds)
}

// getGroups retrieves all groups for the current user.
func (a *ClientGroupController) getGroups(c *gin.Context) {
	user := session.GetLoginUser(c)
	groups, err := a.groupService.GetGroups(user.Id)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, groups, nil)
}

// getGroup retrieves a specific group by its ID.
func (a *ClientGroupController) getGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid group ID", err)
		return
	}
	user := session.GetLoginUser(c)
	group, err := a.groupService.GetGroup(id, user.Id)
	if err != nil {
		jsonMsg(c, "Failed to get group", err)
		return
	}
	jsonObj(c, group, nil)
}

// addGroup creates a new group.
func (a *ClientGroupController) addGroup(c *gin.Context) {
	user := session.GetLoginUser(c)
	group := &model.ClientGroup{}
	err := c.ShouldBind(group)
	if err != nil {
		jsonMsg(c, "Invalid group data", err)
		return
	}
	// Limit group name to 30 characters
	if len(group.Name) > 30 {
		group.Name = group.Name[:30]
	}
	err = a.groupService.AddGroup(user.Id, group)
	if err != nil {
		logger.Errorf("Failed to add group: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsgObj(c, "Group created successfully", group, nil)
}

// updateGroup updates an existing group.
func (a *ClientGroupController) updateGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid group ID", err)
		return
	}
	user := session.GetLoginUser(c)
	group := &model.ClientGroup{}
	err = c.ShouldBind(group)
	if err != nil {
		jsonMsg(c, "Invalid group data", err)
		return
	}
	// Limit group name to 30 characters
	if len(group.Name) > 30 {
		group.Name = group.Name[:30]
	}
	err = a.groupService.UpdateGroup(user.Id, id, group)
	if err != nil {
		logger.Errorf("Failed to update group: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsgObj(c, "Group updated successfully", group, nil)
}

// deleteGroup deletes a group by ID.
func (a *ClientGroupController) deleteGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid group ID", err)
		return
	}
	user := session.GetLoginUser(c)
	err = a.groupService.DeleteGroup(user.Id, id)
	if err != nil {
		logger.Errorf("Failed to delete group: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "Group deleted successfully", nil)
}

// getClientsInGroup retrieves all clients in a specific group.
func (a *ClientGroupController) getClientsInGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid group ID", err)
		return
	}
	user := session.GetLoginUser(c)
	clients, err := a.groupService.GetClientsInGroup(id, user.Id)
	if err != nil {
		jsonMsg(c, "Failed to get clients in group", err)
		return
	}
	jsonObj(c, clients, nil)
}

// assignClientsToGroup assigns clients to a group.
func (a *ClientGroupController) assignClientsToGroup(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid group ID", err)
		return
	}
	user := session.GetLoginUser(c)
	var req struct {
		ClientIds []int `json:"clientIds" form:"clientIds"`
	}
	err = c.ShouldBind(&req)
	if err != nil {
		jsonMsg(c, "Invalid request data", err)
		return
	}
	err = a.groupService.AssignClientsToGroup(id, req.ClientIds, user.Id)
	if err != nil {
		logger.Errorf("Failed to assign clients to group: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "Clients assigned to group successfully", nil)
}

// removeClientsFromGroup removes clients from their group.
func (a *ClientGroupController) removeClientsFromGroup(c *gin.Context) {
	_, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid group ID", err)
		return
	}
	user := session.GetLoginUser(c)
	var req struct {
		ClientIds []int `json:"clientIds" form:"clientIds"`
	}
	err = c.ShouldBind(&req)
	if err != nil {
		jsonMsg(c, "Invalid request data", err)
		return
	}
	err = a.groupService.RemoveClientsFromGroup(req.ClientIds, user.Id)
	if err != nil {
		logger.Errorf("Failed to remove clients from group: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "Clients removed from group successfully", nil)
}

// bulkResetTraffic resets traffic for all clients in a group.
func (a *ClientGroupController) bulkResetTraffic(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid group ID", err)
		return
	}
	user := session.GetLoginUser(c)
	// Get all clients in group
	clients, err := a.groupService.GetClientsInGroup(id, user.Id)
	if err != nil {
		jsonMsg(c, "Failed to get clients in group", err)
		return
	}
	if len(clients) == 0 {
		jsonMsg(c, "No clients in group", nil)
		return
	}
	clientIds := make([]int, len(clients))
	for i, client := range clients {
		clientIds[i] = client.Id
	}
	needRestart, err := a.clientService.BulkResetTraffic(user.Id, clientIds)
	if err != nil {
		logger.Errorf("Failed to reset traffic for group: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "Traffic reset successfully", nil)
	if needRestart {
		// Restart asynchronously to avoid blocking the response
		a.xrayService.RestartXrayAsync(false)
	}
}

// bulkClearHwid clears HWIDs for all clients in a group.
func (a *ClientGroupController) bulkClearHwid(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid group ID", err)
		return
	}
	user := session.GetLoginUser(c)
	// Get all clients in group
	clients, err := a.groupService.GetClientsInGroup(id, user.Id)
	if err != nil {
		jsonMsg(c, "Failed to get clients in group", err)
		return
	}
	if len(clients) == 0 {
		jsonMsg(c, "No clients in group", nil)
		return
	}
	clientIds := make([]int, len(clients))
	for i, client := range clients {
		clientIds[i] = client.Id
	}
	err = a.clientService.BulkClearHWIDs(user.Id, clientIds)
	if err != nil {
		logger.Errorf("Failed to clear HWIDs for group: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "HWIDs cleared successfully", nil)
}

// bulkDelete deletes all clients in a group.
func (a *ClientGroupController) bulkDelete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid group ID", err)
		return
	}
	user := session.GetLoginUser(c)
	// Get all clients in group
	clients, err := a.groupService.GetClientsInGroup(id, user.Id)
	if err != nil {
		jsonMsg(c, "Failed to get clients in group", err)
		return
	}
	if len(clients) == 0 {
		jsonMsg(c, "No clients in group", nil)
		return
	}
	clientIds := make([]int, len(clients))
	for i, client := range clients {
		clientIds[i] = client.Id
	}
	needRestart, err := a.clientService.BulkDelete(user.Id, clientIds)
	if err != nil {
		logger.Errorf("Failed to delete clients in group: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "Clients deleted successfully", nil)
	if needRestart {
		// Restart asynchronously to avoid blocking the response
		a.xrayService.RestartXrayAsync(false)
	}
}

// bulkEnable enables or disables all clients in a group.
func (a *ClientGroupController) bulkEnable(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid group ID", err)
		return
	}
	user := session.GetLoginUser(c)
	var req struct {
		Enable bool `json:"enable" form:"enable"`
	}
	err = c.ShouldBind(&req)
	if err != nil {
		jsonMsg(c, "Invalid request data", err)
		return
	}
	// Get group info first
	group, err := a.groupService.GetGroup(id, user.Id)
	if err != nil {
		jsonMsg(c, "Failed to get group", err)
		return
	}
	
	// Get all clients in group
	clients, err := a.groupService.GetClientsInGroup(id, user.Id)
	if err != nil {
		jsonMsg(c, "Failed to get clients in group", err)
		return
	}
	if len(clients) == 0 {
		jsonMsg(c, "No clients in group", nil)
		return
	}
	clientIds := make([]int, len(clients))
	for i, client := range clients {
		clientIds[i] = client.Id
	}
	needRestart, err := a.clientService.BulkEnable(user.Id, clientIds, req.Enable)
	if err != nil {
		logger.Errorf("Failed to enable/disable clients in group: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	
	// Send group-level notification instead of per-client notifications
	tgbotService := service.Tgbot{}
	if tgbotService.IsRunning() {
		// Reload clients to get updated enable status
		updatedClients, _ := a.groupService.GetClientsInGroup(id, user.Id)
		tgbotService.NotifyGroupChanged(group.Name, req.Enable, updatedClients)
	}
	
	jsonMsg(c, "Clients updated successfully", nil)
	if needRestart {
		// Restart asynchronously to avoid blocking the response
		a.xrayService.RestartXrayAsync(false)
	}
}

// bulkSetHwidLimit sets HWID limit for all clients in a group.
func (a *ClientGroupController) bulkSetHwidLimit(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid group ID", err)
		return
	}
	user := session.GetLoginUser(c)
	var req struct {
		MaxHwid int  `json:"maxHwid" form:"maxHwid"`
		Enabled bool `json:"enabled" form:"enabled"`
	}
	err = c.ShouldBind(&req)
	if err != nil {
		jsonMsg(c, "Invalid request data", err)
		return
	}
	// Get all clients in group
	clients, err := a.groupService.GetClientsInGroup(id, user.Id)
	if err != nil {
		jsonMsg(c, "Failed to get clients in group", err)
		return
	}
	if len(clients) == 0 {
		jsonMsg(c, "No clients in group", nil)
		return
	}
	clientIds := make([]int, len(clients))
	for i, client := range clients {
		clientIds[i] = client.Id
	}
	err = a.clientService.BulkSetHWIDLimit(user.Id, clientIds, req.MaxHwid, req.Enabled)
	if err != nil {
		logger.Errorf("Failed to set HWID limit for group: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "HWID limit set successfully", nil)
}

// bulkAssignInbounds assigns inbounds to all clients in a group.
func (a *ClientGroupController) bulkAssignInbounds(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "Invalid group ID", err)
		return
	}
	user := session.GetLoginUser(c)
	var req struct {
		InboundIds []int `json:"inboundIds" form:"inboundIds"`
	}
	err = c.ShouldBind(&req)
	if err != nil {
		jsonMsg(c, "Invalid request data", err)
		return
	}
	if len(req.InboundIds) == 0 {
		jsonMsg(c, "No inbounds selected", nil)
		return
	}
	// Get all clients in group
	clients, err := a.groupService.GetClientsInGroup(id, user.Id)
	if err != nil {
		jsonMsg(c, "Failed to get clients in group", err)
		return
	}
	if len(clients) == 0 {
		jsonMsg(c, "No clients in group", nil)
		return
	}
	clientIds := make([]int, len(clients))
	for i, client := range clients {
		clientIds[i] = client.Id
	}
	needRestart, err := a.clientService.BulkAssignInbounds(user.Id, clientIds, req.InboundIds)
	if err != nil {
		logger.Errorf("Failed to assign inbounds for group: %v", err)
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "Inbounds assigned successfully", nil)
	if needRestart {
		// Restart asynchronously to avoid blocking the response
		a.xrayService.RestartXrayAsync(false)
	}
}
