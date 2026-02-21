package service

import (
	"github.com/google/uuid"
	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/random"
)

// RotateAllClientKeys rotates keys (UUID/password) for all active clients across all inbounds.
// For VMESS/VLESS: generates new UUID
// For Trojan/Shadowsocks: generates new password
// Returns the number of clients updated and any error.
func (s *ClientService) RotateAllClientKeys() (int, error) {
	db := database.GetDB()
	
	// Get all active clients
	var clients []model.ClientEntity
	err := db.Where("enable = ? AND status = ?", true, "active").Find(&clients).Error
	if err != nil {
		return 0, err
	}
	
	if len(clients) == 0 {
		logger.Info("RotateAllClientKeys: No active clients found")
		return 0, nil
	}
	
	logger.Infof("RotateAllClientKeys: Found %d active clients to rotate keys", len(clients))
	
	updatedCount := 0
	
	for _, client := range clients {
		// Get all inbound IDs for this client
		inboundIds, err := s.GetInboundIdsForClient(client.Id)
		if err != nil {
			logger.Warningf("RotateAllClientKeys: Failed to get inbounds for client %d (%s): %v", 
				client.Id, client.Email, err)
			continue
		}
		
		if len(inboundIds) == 0 {
			logger.Debugf("RotateAllClientKeys: Client %d (%s) has no inbounds, skipping", 
				client.Id, client.Email)
			continue
		}
		
		// Determine protocol from client's existing UUID/password
		// If client has UUID, it's VMESS/VLESS; if has password, it's Trojan/Shadowsocks
		// Create updated client with new key
		updatedClient := client
		needsUpdate := false
		
		if client.UUID != "" {
			// VMESS/VLESS client - rotate UUID
			newUUID, err := uuid.NewRandom()
			if err != nil {
				logger.Warningf("RotateAllClientKeys: Failed to generate UUID for client %d (%s): %v", 
					client.Id, client.Email, err)
				continue
			}
			updatedClient.UUID = newUUID.String()
			needsUpdate = true
			logger.Infof("RotateAllClientKeys: Rotating UUID for client %d (%s): %s -> %s", 
				client.Id, client.Email, client.UUID, updatedClient.UUID)
		} else if client.Password != "" {
			// Trojan/Shadowsocks client - rotate password
			updatedClient.Password = random.Seq(32)
			needsUpdate = true
			logger.Infof("RotateAllClientKeys: Rotating password for client %d (%s)", 
				client.Id, client.Email)
		} else {
			logger.Warningf("RotateAllClientKeys: Client %d (%s) has neither UUID nor password, skipping", 
				client.Id, client.Email)
			continue
		}
		
		if !needsUpdate {
			continue
		}
		
		// Update client (this will automatically update all related inbounds)
		_, err = s.UpdateClient(client.UserId, &updatedClient)
		if err != nil {
			logger.Warningf("RotateAllClientKeys: Failed to update client %d (%s): %v", 
				client.Id, client.Email, err)
			continue
		}
		
		updatedCount++
		logger.Infof("RotateAllClientKeys: Successfully rotated keys for client %d (%s)", 
			client.Id, client.Email)
	}
	
	logger.Infof("RotateAllClientKeys: Completed. Rotated keys for %d out of %d clients", 
		updatedCount, len(clients))
	
	return updatedCount, nil
}
