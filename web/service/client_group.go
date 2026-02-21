// Package service provides ClientGroup management service.
package service

import (
	"strings"
	"time"

	"github.com/konstpic/sharx-code/v2/database"
	"github.com/konstpic/sharx-code/v2/database/model"
	"github.com/konstpic/sharx-code/v2/util/common"
	"github.com/konstpic/sharx-code/v2/web/cache"
)

// ClientGroupService provides business logic for managing client groups.
type ClientGroupService struct{}

// GetGroups retrieves all groups for a specific user.
func (s *ClientGroupService) GetGroups(userId int) ([]*model.ClientGroup, error) {
	db := database.GetDB()
	var groups []*model.ClientGroup
	err := db.Where("user_id = ?", userId).Order("created_at DESC").Find(&groups).Error
	if err != nil {
		return nil, err
	}

	// Load client count for each group
	for _, group := range groups {
		var count int64
		db.Model(&model.ClientEntity{}).Where("group_id = ? AND user_id = ?", group.Id, userId).Count(&count)
		group.ClientCount = int(count)
	}

	return groups, nil
}

// GetGroup retrieves a group by ID.
func (s *ClientGroupService) GetGroup(id int, userId int) (*model.ClientGroup, error) {
	db := database.GetDB()
	var group model.ClientGroup
	err := db.Where("id = ? AND user_id = ?", id, userId).First(&group).Error
	if err != nil {
		return nil, err
	}

	// Load client count
	var count int64
	db.Model(&model.ClientEntity{}).Where("group_id = ? AND user_id = ?", group.Id, userId).Count(&count)
	group.ClientCount = int(count)

	return &group, nil
}

// AddGroup creates a new group.
func (s *ClientGroupService) AddGroup(userId int, group *model.ClientGroup) error {
	// Validate group name length (spaces count as characters)
	if len(group.Name) > 30 {
		return common.NewError("Group name exceeds maximum length of 30 characters (spaces count as characters)")
	}
	
	// Validate description length (spaces count as characters)
	if len(group.Description) > 100 {
		return common.NewError("Group description exceeds maximum length of 100 characters (spaces count as characters)")
	}
	
	// Trim whitespace from name and description
	group.Name = strings.TrimSpace(group.Name)
	group.Description = strings.TrimSpace(group.Description)
	
	group.UserId = userId

	// Set timestamps
	now := time.Now().Unix()
	if group.CreatedAt == 0 {
		group.CreatedAt = now
	}
	group.UpdatedAt = now

	db := database.GetDB()
	err := db.Create(group).Error
	if err != nil {
		return err
	}

	// Invalidate cache for this user's clients
	cache.InvalidateClients(userId)

	return nil
}

// UpdateGroup updates an existing group.
func (s *ClientGroupService) UpdateGroup(userId int, id int, group *model.ClientGroup) error {
	// Check if group exists and belongs to user
	_, err := s.GetGroup(id, userId)
	if err != nil {
		return err
	}

	// Validate group name length (spaces count as characters)
	if len(group.Name) > 30 {
		return common.NewError("Group name exceeds maximum length of 30 characters (spaces count as characters)")
	}
	
	// Validate description length (spaces count as characters)
	if len(group.Description) > 100 {
		return common.NewError("Group description exceeds maximum length of 100 characters (spaces count as characters)")
	}
	
	// Trim whitespace from name and description
	group.Name = strings.TrimSpace(group.Name)
	group.Description = strings.TrimSpace(group.Description)

	// Update timestamp
	group.UpdatedAt = time.Now().Unix()
	group.Id = id
	group.UserId = userId

	db := database.GetDB()
	err = db.Model(&model.ClientGroup{}).
		Where("id = ? AND user_id = ?", id, userId).
		Updates(map[string]interface{}{
			"name":        group.Name,
			"description": group.Description,
			"updated_at":  group.UpdatedAt,
		}).Error

	if err != nil {
		return err
	}

	// Invalidate cache for this user's clients
	cache.InvalidateClients(userId)

	return nil
}

// DeleteGroup deletes a group by ID.
// Clients in the group will have their group_id set to NULL.
func (s *ClientGroupService) DeleteGroup(userId int, id int) error {
	// Check if group exists and belongs to user
	_, err := s.GetGroup(id, userId)
	if err != nil {
		return err
	}

	db := database.GetDB()
	tx := db.Begin()
	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()

	// Remove group_id from all clients in this group
	err = tx.Model(&model.ClientEntity{}).
		Where("group_id = ? AND user_id = ?", id, userId).
		Update("group_id", nil).Error
	if err != nil {
		return err
	}

	// Delete group
	err = tx.Where("id = ? AND user_id = ?", id, userId).Delete(&model.ClientGroup{}).Error
	if err != nil {
		return err
	}

	// Invalidate cache for this user's clients
	cache.InvalidateClients(userId)

	return nil
}

// GetClientsInGroup retrieves all clients in a specific group.
func (s *ClientGroupService) GetClientsInGroup(groupId int, userId int) ([]*model.ClientEntity, error) {
	// Verify group belongs to user
	_, err := s.GetGroup(groupId, userId)
	if err != nil {
		return nil, err
	}

	db := database.GetDB()
	var clients []*model.ClientEntity
	err = db.Where("group_id = ? AND user_id = ?", groupId, userId).Find(&clients).Error
	if err != nil {
		return nil, err
	}

	// Load inbound assignments for each client
	clientService := ClientService{}
	for _, client := range clients {
		inboundIds, err := clientService.GetInboundIdsForClient(client.Id)
		if err == nil {
			client.InboundIds = inboundIds
		}
	}

	return clients, nil
}

// AssignClientsToGroup assigns clients to a group.
// If a client is already in another group, it will be moved to the new group.
func (s *ClientGroupService) AssignClientsToGroup(groupId int, clientIds []int, userId int) error {
	// Verify group belongs to user
	_, err := s.GetGroup(groupId, userId)
	if err != nil {
		return err
	}

	// Verify all clients belong to user
	db := database.GetDB()
	var count int64
	err = db.Model(&model.ClientEntity{}).
		Where("id IN ? AND user_id = ?", clientIds, userId).
		Count(&count).Error
	if err != nil {
		return err
	}
	if int(count) != len(clientIds) {
		return common.NewError("Some clients not found or access denied")
	}

	// Assign clients to group
	err = db.Model(&model.ClientEntity{}).
		Where("id IN ? AND user_id = ?", clientIds, userId).
		Update("group_id", groupId).Error
	if err != nil {
		return err
	}

	// Invalidate cache for this user's clients
	cache.InvalidateClients(userId)

	return nil
}

// RemoveClientsFromGroup removes clients from their group (sets group_id to NULL).
func (s *ClientGroupService) RemoveClientsFromGroup(clientIds []int, userId int) error {
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

	// Remove clients from group
	err = db.Model(&model.ClientEntity{}).
		Where("id IN ? AND user_id = ?", clientIds, userId).
		Update("group_id", nil).Error
	if err != nil {
		return err
	}

	// Invalidate cache for this user's clients
	cache.InvalidateClients(userId)

	return nil
}
