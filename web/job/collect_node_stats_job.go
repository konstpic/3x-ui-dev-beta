// Package job provides background job implementations for the 3x-ui panel.
package job

import (
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/web/service"
	"github.com/mhsanaei/3x-ui/v2/web/websocket"
)

// CollectNodeStatsJob collects traffic and online clients statistics from all nodes.
type CollectNodeStatsJob struct {
	nodeService service.NodeService
}

// NewCollectNodeStatsJob creates a new CollectNodeStatsJob instance.
func NewCollectNodeStatsJob() *CollectNodeStatsJob {
	return &CollectNodeStatsJob{
		nodeService: service.NodeService{},
	}
}

// Run executes the job to collect statistics from all nodes.
func (j *CollectNodeStatsJob) Run() {
	logger.Debug("Starting node stats collection job")
	
	if err := j.nodeService.CollectNodeStats(); err != nil {
		logger.Errorf("Failed to collect node stats: %v", err)
		return
	}
	
	// Broadcast updated nodes list via WebSocket for real-time updates
	// Enrich nodes with inbounds and profiles (same as in NodeController)
	nodes, err := j.nodeService.GetAllNodes()
	if err == nil && nodes != nil {
		// Enrich nodes with assigned inbounds and profiles information
		// Use the same structure as NodeController.broadcastNodesUpdate()
		type NodeWithInbounds struct {
			*model.Node
			Inbounds []*model.Inbound                    `json:"inbounds,omitempty"`
			Profiles []*model.XrayCoreConfigProfile       `json:"profiles,omitempty"`
		}
		
		profileService := service.XrayCoreConfigProfileService{}
		result := make([]NodeWithInbounds, 0, len(nodes))
		for _, node := range nodes {
			inbounds, _ := j.nodeService.GetInboundsForNode(node.Id)
			profiles, _ := profileService.GetProfilesForNode(node.Id)
			result = append(result, NodeWithInbounds{
				Node:     node,
				Inbounds: inbounds,
				Profiles: profiles,
			})
		}
		websocket.BroadcastNodes(result)
	} else if err != nil {
		logger.Warningf("Failed to get nodes for WebSocket broadcast: %v", err)
	}
	
	logger.Debug("Node stats collection job completed successfully")
}
