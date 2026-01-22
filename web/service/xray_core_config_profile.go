package service

import (
	"encoding/json"
	"fmt"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/singbox"
	"github.com/mhsanaei/3x-ui/v2/util/common"
	"github.com/mhsanaei/3x-ui/v2/xray"

	"gorm.io/gorm"
)

// XrayCoreConfigProfileService provides business logic for managing Xray core configuration profiles.
type XrayCoreConfigProfileService struct{}

// GetProfile retrieves a profile by ID.
func (s *XrayCoreConfigProfileService) GetProfile(id int) (*model.XrayCoreConfigProfile, error) {
	db := database.GetDB()
	var profile model.XrayCoreConfigProfile
	err := db.Where("id = ?", id).First(&profile).Error
	if err != nil {
		return nil, err
	}
	// Load node IDs
	nodeIds, err := s.GetNodesForProfile(profile.Id)
	if err == nil {
		profile.NodeIds = nodeIds
	}
	return &profile, nil
}

// GetNodesForProfile retrieves all node IDs assigned to a profile.
func (s *XrayCoreConfigProfileService) GetNodesForProfile(profileId int) ([]int, error) {
	db := database.GetDB()
	var mappings []model.ProfileNodeMapping
	err := db.Where("profile_id = ?", profileId).Find(&mappings).Error
	if err != nil {
		return nil, err
	}
	nodeIds := make([]int, len(mappings))
	for i, mapping := range mappings {
		nodeIds[i] = mapping.NodeId
	}
	return nodeIds, nil
}

// GetProfilesForNode retrieves all profiles assigned to a node.
func (s *XrayCoreConfigProfileService) GetProfilesForNode(nodeId int) ([]*model.XrayCoreConfigProfile, error) {
	db := database.GetDB()
	var mappings []model.ProfileNodeMapping
	err := db.Where("node_id = ?", nodeId).Find(&mappings).Error
	if err != nil {
		return nil, err
	}
	
	if len(mappings) == 0 {
		return []*model.XrayCoreConfigProfile{}, nil
	}
	
	profileIds := make([]int, len(mappings))
	for i, mapping := range mappings {
		profileIds[i] = mapping.ProfileId
	}
	
	var profiles []*model.XrayCoreConfigProfile
	err = db.Where("id IN ?", profileIds).Find(&profiles).Error
	if err != nil {
		return nil, err
	}
	
	return profiles, nil
}

// GetAllProfiles retrieves all profiles for a user.
func (s *XrayCoreConfigProfileService) GetAllProfiles(userId int) ([]*model.XrayCoreConfigProfile, error) {
	db := database.GetDB()
	var profiles []*model.XrayCoreConfigProfile
	err := db.Where("user_id = ?", userId).Order("is_default DESC, created_at ASC").Find(&profiles).Error
	if err != nil {
		return nil, err
	}
	// Load node IDs for each profile
	for _, profile := range profiles {
		nodeIds, err := s.GetNodesForProfile(profile.Id)
		if err == nil {
			profile.NodeIds = nodeIds
		}
	}
	return profiles, nil
}

// GetDefaultProfile retrieves the default profile for a user.
func (s *XrayCoreConfigProfileService) GetDefaultProfile(userId int) (*model.XrayCoreConfigProfile, error) {
	db := database.GetDB()
	var profile model.XrayCoreConfigProfile
	err := db.Where("user_id = ? AND is_default = ?", userId, true).First(&profile).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil // No default profile found
		}
		return nil, err
	}
	return &profile, nil
}

// AddProfile creates a new profile.
func (s *XrayCoreConfigProfileService) AddProfile(profile *model.XrayCoreConfigProfile) (*model.XrayCoreConfigProfile, error) {
	// Validate JSON config
	if err := s.validateConfigJson(profile.ConfigJson); err != nil {
		return nil, common.NewErrorf("invalid Xray config JSON: %v", err)
	}

	db := database.GetDB()

	// If this is set as default, unset other default profiles for this user
	if profile.IsDefault {
		err := db.Model(&model.XrayCoreConfigProfile{}).
			Where("user_id = ? AND is_default = ?", profile.UserId, true).
			Update("is_default", false).Error
		if err != nil {
			return nil, err
		}
	}

	err := db.Create(profile).Error
	if err != nil {
		return nil, err
	}

	logger.Infof("Xray core config profile %d created for user %d", profile.Id, profile.UserId)
	return profile, nil
}

// UpdateProfile updates an existing profile.
func (s *XrayCoreConfigProfileService) UpdateProfile(profile *model.XrayCoreConfigProfile) (*model.XrayCoreConfigProfile, error) {
	// Validate JSON config if provided
	if profile.ConfigJson != "" {
		if err := s.validateConfigJson(profile.ConfigJson); err != nil {
			return nil, common.NewErrorf("invalid Xray config JSON: %v", err)
		}
	}

	db := database.GetDB()

	// If this is set as default, unset other default profiles for this user
	if profile.IsDefault {
		err := db.Model(&model.XrayCoreConfigProfile{}).
			Where("user_id = ? AND is_default = ? AND id != ?", profile.UserId, true, profile.Id).
			Update("is_default", false).Error
		if err != nil {
			return nil, err
		}
	}

	err := db.Model(&model.XrayCoreConfigProfile{}).Where("id = ?", profile.Id).Updates(profile).Error
	if err != nil {
		return nil, err
	}

	// Reload to get updated data
	return s.GetProfile(profile.Id)
}

// DeleteProfile deletes a profile by ID.
func (s *XrayCoreConfigProfileService) DeleteProfile(id int) error {
	db := database.GetDB()

	// Check if profile is used by any outbounds
	var count int64
	err := db.Model(&model.Outbound{}).Where("core_config_profile_id = ?", id).Count(&count).Error
	if err != nil {
		return err
	}
	if count > 0 {
		return common.NewErrorf("cannot delete profile: it is used by %d outbound(s)", count)
	}

	// Check if this is the default profile
	var profile model.XrayCoreConfigProfile
	err = db.Where("id = ?", id).First(&profile).Error
	if err != nil {
		return err
	}
	if profile.IsDefault {
		return common.NewError("cannot delete the default profile")
	}

	err = db.Delete(&model.XrayCoreConfigProfile{}, id).Error
	if err != nil {
		return err
	}

	logger.Infof("Xray core config profile %d deleted", id)
	return nil
}

// SetAsDefault sets a profile as the default for a user.
func (s *XrayCoreConfigProfileService) SetAsDefault(id int, userId int) error {
	db := database.GetDB()

	// Unset all other default profiles for this user
	err := db.Model(&model.XrayCoreConfigProfile{}).
		Where("user_id = ? AND is_default = ?", userId, true).
		Update("is_default", false).Error
	if err != nil {
		return err
	}

	// Set this profile as default
	err = db.Model(&model.XrayCoreConfigProfile{}).
		Where("id = ? AND user_id = ?", id, userId).
		Update("is_default", true).Error
	if err != nil {
		return err
	}

	logger.Infof("Xray core config profile %d set as default for user %d", id, userId)
	return nil
}

// CreateDefaultProfileFromTemplate creates a default profile from the current xrayTemplateConfig.
func (s *XrayCoreConfigProfileService) CreateDefaultProfileFromTemplate(userId int) (*model.XrayCoreConfigProfile, error) {
	settingService := SettingService{}
	templateConfig, err := settingService.GetXrayConfigTemplate()
	if err != nil {
		return nil, common.NewErrorf("failed to get Xray template config: %v", err)
	}

	// Validate the template config
	if err := s.validateConfigJson(templateConfig); err != nil {
		return nil, common.NewErrorf("invalid Xray template config: %v", err)
	}

	profile := &model.XrayCoreConfigProfile{
		UserId:      userId,
		Name:        "Default",
		Description: "Default Xray core configuration profile created from template",
		ConfigJson:  templateConfig,
		IsDefault:   true,
	}

	return s.AddProfile(profile)
}

// ResetToDefault resets a profile to the default template configuration.
func (s *XrayCoreConfigProfileService) ResetToDefault(id int) (*model.XrayCoreConfigProfile, error) {
	profile, err := s.GetProfile(id)
	if err != nil {
		return nil, err
	}

	settingService := SettingService{}
	templateConfig, err := settingService.GetXrayConfigTemplate()
	if err != nil {
		return nil, common.NewErrorf("failed to get Xray template config: %v", err)
	}

	// Validate the template config
	if err := s.validateConfigJson(templateConfig); err != nil {
		return nil, common.NewErrorf("invalid Xray template config: %v", err)
	}

	profile.ConfigJson = templateConfig
	return s.UpdateProfile(profile)
}

// validateConfigJson validates that the JSON string is a valid configuration.
// It tries to validate as both xray and sing-box format to support both cores.
func (s *XrayCoreConfigProfileService) validateConfigJson(configJson string) error {
	if configJson == "" {
		return common.NewError("config JSON cannot be empty")
	}

	// Try to validate as xray config first
	xrayConfig := &xray.Config{}
	if err := json.Unmarshal([]byte(configJson), xrayConfig); err == nil {
		// Valid xray config
		return nil
	}

	// Try to validate as sing-box config
	singboxConfig := &singbox.Config{}
	if err := json.Unmarshal([]byte(configJson), singboxConfig); err == nil {
		// Valid sing-box config
		return nil
	}

	// Neither format is valid
	return fmt.Errorf("invalid config JSON: not a valid xray or sing-box configuration")
}

// EnsureDefaultProfile ensures that a default profile exists for a user.
// If no default profile exists, creates one from the template.
func (s *XrayCoreConfigProfileService) EnsureDefaultProfile(userId int) (*model.XrayCoreConfigProfile, error) {
	defaultProfile, err := s.GetDefaultProfile(userId)
	if err != nil {
		return nil, err
	}

	if defaultProfile != nil {
		return defaultProfile, nil
	}

	// No default profile exists, create one
	return s.CreateDefaultProfileFromTemplate(userId)
}

// AssignProfileToNodes assigns a profile to multiple nodes.
// Validates that nodes are not already assigned to other profiles.
func (s *XrayCoreConfigProfileService) AssignProfileToNodes(profileId int, nodeIds []int) error {
	if len(nodeIds) == 0 {
		// Remove all assignments if no nodes provided
		db := database.GetDB()
		return db.Where("profile_id = ?", profileId).Delete(&model.ProfileNodeMapping{}).Error
	}

	// Check if any nodes are already assigned to other profiles
	conflictingNodes, err := s.checkNodeAssignedToOtherProfile(nodeIds, profileId)
	if err != nil {
		return fmt.Errorf("failed to check node assignments: %w", err)
	}

	if len(conflictingNodes) > 0 {
		// Get node names for error message
		db := database.GetDB()
		var nodes []model.Node
		db.Where("id IN ?", conflictingNodes).Find(&nodes)
		nodeNames := make([]string, len(nodes))
		for i, node := range nodes {
			nodeNames[i] = node.Name
		}
		return fmt.Errorf("nodes already assigned to other profile: %v", nodeNames)
	}

	db := database.GetDB()
	// First, remove all existing assignments for this profile
	if err := db.Where("profile_id = ?", profileId).Delete(&model.ProfileNodeMapping{}).Error; err != nil {
		return err
	}
	// Then, add new assignments
	mappings := make([]model.ProfileNodeMapping, len(nodeIds))
	for i, nodeId := range nodeIds {
		mappings[i] = model.ProfileNodeMapping{
			ProfileId: profileId,
			NodeId:    nodeId,
		}
	}
	if err := db.Create(&mappings).Error; err != nil {
		return err
	}
	return nil
}

// checkNodeAssignedToOtherProfile checks if any of the given nodes are already assigned to a different profile.
func (s *XrayCoreConfigProfileService) checkNodeAssignedToOtherProfile(nodeIds []int, excludeProfileId int) ([]int, error) {
	if len(nodeIds) == 0 {
		return nil, nil
	}

	db := database.GetDB()
	var conflictingMappings []model.ProfileNodeMapping
	err := db.Where("node_id IN ? AND profile_id != ?", nodeIds, excludeProfileId).Find(&conflictingMappings).Error
	if err != nil {
		return nil, err
	}

	conflictingNodeIds := make(map[int]bool)
	for _, mapping := range conflictingMappings {
		conflictingNodeIds[mapping.NodeId] = true
	}

	result := make([]int, 0, len(conflictingNodeIds))
	for nodeId := range conflictingNodeIds {
		result = append(result, nodeId)
	}

	return result, nil
}
