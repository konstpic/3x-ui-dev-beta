package service

import (
	"fmt"

	"github.com/konstpic/sharx-code/v2/database"
	"github.com/konstpic/sharx-code/v2/database/model"
	"github.com/konstpic/sharx-code/v2/logger"
	"github.com/konstpic/sharx-code/v2/util/common"
	"github.com/konstpic/sharx-code/v2/xray"

	"gorm.io/gorm"
)

// OutboundService provides business logic for managing Xray outbound configurations.
// It handles outbound traffic monitoring, statistics, and CRUD operations for outbound configs.
type OutboundService struct{}

func (s *OutboundService) AddTraffic(traffics []*xray.Traffic, clientTraffics []*xray.ClientTraffic) (error, bool) {
	db := database.GetDB()
	
	err := db.Transaction(func(tx *gorm.DB) error {
		return s.addOutboundTraffic(tx, traffics)
	})
	
	if err != nil {
		return err, false
	}

	return nil, false
}

func (s *OutboundService) addOutboundTraffic(tx *gorm.DB, traffics []*xray.Traffic) error {
	if len(traffics) == 0 {
		return nil
	}

	var err error

	for _, traffic := range traffics {
		if traffic.IsOutbound {

			var outbound model.OutboundTraffics

			err = tx.Model(&model.OutboundTraffics{}).Where("tag = ?", traffic.Tag).
				FirstOrCreate(&outbound).Error
			if err != nil {
				return err
			}

			outbound.Tag = traffic.Tag
			outbound.Up = outbound.Up + traffic.Up
			outbound.Down = outbound.Down + traffic.Down
			outbound.Total = outbound.Up + outbound.Down

			err = tx.Save(&outbound).Error
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *OutboundService) GetOutboundsTraffic() ([]*model.OutboundTraffics, error) {
	db := database.GetDB()
	var traffics []*model.OutboundTraffics

	err := db.Model(model.OutboundTraffics{}).Find(&traffics).Error
	if err != nil {
		logger.Warning("Error retrieving OutboundTraffics: ", err)
		return nil, err
	}

	return traffics, nil
}

func (s *OutboundService) ResetOutboundTraffic(tag string) error {
	db := database.GetDB()

	whereText := "tag "
	if tag == "-alltags-" {
		whereText += " <> ?"
	} else {
		whereText += " = ?"
	}

	result := db.Model(model.OutboundTraffics{}).
		Where(whereText, tag).
		Updates(map[string]any{"up": 0, "down": 0, "total": 0})

	err := result.Error
	if err != nil {
		return err
	}

	return nil
}

// GetAllOutbounds retrieves all outbounds from the database.
func (s *OutboundService) GetAllOutbounds() ([]*model.Outbound, error) {
	db := database.GetDB()
	var outbounds []*model.Outbound
	err := db.Model(model.Outbound{}).Find(&outbounds).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	// Enrich with node assignments
	nodeService := NodeService{}
	for _, outbound := range outbounds {
		nodes, err := nodeService.GetNodesForOutbound(outbound.Id)
		if err == nil && len(nodes) > 0 {
			nodeIds := make([]int, len(nodes))
			for i, node := range nodes {
				nodeIds[i] = node.Id
			}
			outbound.NodeIds = nodeIds
		} else {
			outbound.NodeIds = []int{}
		}
	}

	return outbounds, nil
}

// GetOutbounds retrieves all outbounds for a specific user.
func (s *OutboundService) GetOutbounds(userId int) ([]*model.Outbound, error) {
	db := database.GetDB()
	var outbounds []*model.Outbound
	err := db.Model(model.Outbound{}).Where("user_id = ?", userId).Find(&outbounds).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	// Enrich with node assignments
	nodeService := NodeService{}
	for _, outbound := range outbounds {
		nodes, err := nodeService.GetNodesForOutbound(outbound.Id)
		if err == nil && len(nodes) > 0 {
			nodeIds := make([]int, len(nodes))
			for i, node := range nodes {
				nodeIds[i] = node.Id
			}
			outbound.NodeIds = nodeIds
		} else {
			outbound.NodeIds = []int{}
		}
	}

	return outbounds, nil
}

// GetOutbound retrieves a specific outbound by ID.
func (s *OutboundService) GetOutbound(id int) (*model.Outbound, error) {
	db := database.GetDB()
	var outbound model.Outbound
	err := db.Model(model.Outbound{}).Where("id = ?", id).First(&outbound).Error
	if err != nil {
		return nil, err
	}

	// Enrich with node assignments
	nodeService := NodeService{}
	nodes, err := nodeService.GetNodesForOutbound(outbound.Id)
	if err == nil && len(nodes) > 0 {
		nodeIds := make([]int, len(nodes))
		for i, node := range nodes {
			nodeIds[i] = node.Id
		}
		outbound.NodeIds = nodeIds
	} else {
		outbound.NodeIds = []int{}
	}

	return &outbound, nil
}

// checkTagExist checks if an outbound tag already exists.
func (s *OutboundService) checkTagExist(tag string, ignoreId int) (bool, error) {
	db := database.GetDB()
	query := db.Model(model.Outbound{}).Where("tag = ?", tag)
	if ignoreId > 0 {
		query = query.Where("id != ?", ignoreId)
	}
	var count int64
	err := query.Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// checkNodeAssignedToOtherOutbound checks if any of the given nodeIds are already assigned to another outbound.
func (s *OutboundService) checkNodeAssignedToOtherOutbound(nodeIds []int, excludeOutboundId int) ([]int, error) {
	if len(nodeIds) == 0 {
		return nil, nil
	}

	db := database.GetDB()
	var conflictingNodes []int

	// Get all outbound-node mappings for the given nodes, excluding the current outbound
	var mappings []model.OutboundNodeMapping
	query := db.Model(&model.OutboundNodeMapping{}).
		Where("node_id IN ?", nodeIds)
	
	if excludeOutboundId > 0 {
		query = query.Where("outbound_id != ?", excludeOutboundId)
	}

	err := query.Find(&mappings).Error
	if err != nil {
		return nil, err
	}

	// Collect unique node IDs that are already assigned
	nodeMap := make(map[int]bool)
	for _, mapping := range mappings {
		if !nodeMap[mapping.NodeId] {
			conflictingNodes = append(conflictingNodes, mapping.NodeId)
			nodeMap[mapping.NodeId] = true
		}
	}

	return conflictingNodes, nil
}

// AddOutbound creates a new outbound configuration.
func (s *OutboundService) AddOutbound(outbound *model.Outbound) (*model.Outbound, error) {
	// Validate tag uniqueness
	exist, err := s.checkTagExist(outbound.Tag, 0)
	if err != nil {
		return nil, err
	}
	if exist {
		return nil, common.NewError("Outbound tag already exists:", outbound.Tag)
	}

	// Validate required fields
	if outbound.Tag == "" {
		return nil, common.NewError("Outbound tag is required")
	}
	if outbound.Protocol == "" {
		return nil, common.NewError("Outbound protocol is required")
	}

	db := database.GetDB()
	err = db.Create(outbound).Error
	if err != nil {
		return nil, err
	}

	return outbound, nil
}

// UpdateOutbound updates an existing outbound configuration.
func (s *OutboundService) UpdateOutbound(outbound *model.Outbound) (*model.Outbound, error) {
	// Validate tag uniqueness (if tag changed)
	if outbound.Tag != "" {
		exist, err := s.checkTagExist(outbound.Tag, outbound.Id)
		if err != nil {
			return nil, err
		}
		if exist {
			return nil, common.NewError("Outbound tag already exists:", outbound.Tag)
		}
	}

	db := database.GetDB()
	err := db.Model(model.Outbound{}).Where("id = ?", outbound.Id).Updates(outbound).Error
	if err != nil {
		return nil, err
	}

	// Reload from database to get updated values
	return s.GetOutbound(outbound.Id)
}

// DeleteOutbound deletes an outbound configuration by ID.
func (s *OutboundService) DeleteOutbound(id int) error {
	db := database.GetDB()

	// First, remove all node mappings for this outbound
	err := db.Where("outbound_id = ?", id).Delete(&model.OutboundNodeMapping{}).Error
	if err != nil {
		return fmt.Errorf("failed to remove outbound node mappings: %w", err)
	}

	// Then delete the outbound
	err = db.Delete(&model.Outbound{}, id).Error
	if err != nil {
		return err
	}

	return nil
}

// GetOutboundsForNode retrieves all outbounds assigned to a specific node.
func (s *OutboundService) GetOutboundsForNode(nodeId int) ([]*model.Outbound, error) {
	db := database.GetDB()
	var outbounds []*model.Outbound

	err := db.Model(model.Outbound{}).
		Joins("INNER JOIN outbound_node_mappings ON outbounds.id = outbound_node_mappings.outbound_id").
		Where("outbound_node_mappings.node_id = ? AND outbounds.enable = ?", nodeId, true).
		Find(&outbounds).Error

	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	return outbounds, nil
}
