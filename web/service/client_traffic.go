// Package service provides Client traffic management service.
package service

import (
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/xray"

	"gorm.io/gorm"
)

// clientTrafficState stores previous traffic values for speed calculation
type clientTrafficState struct {
	prevUp      int64
	prevDown    int64
	prevTime    int64
	mu          sync.RWMutex
}

var (
	// trafficStateMap stores previous traffic states for speed calculation
	// map[clientId]clientTrafficState
	trafficStateMap = make(map[int]*clientTrafficState)
	trafficStateMu  sync.RWMutex
)

// AddClientTraffic updates client traffic statistics and returns clients that need to be disabled.
// This method handles traffic tracking for clients in the new architecture (ClientEntity).
// After updating client traffic, it synchronizes inbound traffic as the sum of all its clients' traffic.
func (s *ClientService) AddClientTraffic(tx *gorm.DB, traffics []*xray.ClientTraffic, inboundService *InboundService) (map[string]string, map[int]bool, error) {
	clientsToDisable := make(map[string]string) // map[email]tag
	affectedInboundIds := make(map[int]bool)    // Track affected inbounds for traffic sync

	if len(traffics) == 0 {
		// Empty onlineUsers
		if p != nil {
			p.SetOnlineClients(make([]string, 0))
		}
		return clientsToDisable, affectedInboundIds, nil
	}

	onlineClients := make([]string, 0)

	// Group traffic by email (aggregate traffic from all inbounds for each client)
	emailTrafficMap := make(map[string]struct {
		Up        int64
		Down      int64
		InboundIds []int
	})
	
	for _, traffic := range traffics {
		email := strings.ToLower(traffic.Email)
		existing := emailTrafficMap[email]
		existing.Up += traffic.Up
		existing.Down += traffic.Down
		// Track all inbound IDs for this email
		if traffic.InboundId > 0 {
			found := false
			for _, id := range existing.InboundIds {
				if id == traffic.InboundId {
					found = true
					break
				}
			}
			if !found {
				existing.InboundIds = append(existing.InboundIds, traffic.InboundId)
				affectedInboundIds[traffic.InboundId] = true
			}
		}
		emailTrafficMap[email] = existing
	}

	// Get all unique emails
	emails := make([]string, 0, len(emailTrafficMap))
	for email := range emailTrafficMap {
		emails = append(emails, email)
	}
	
	if len(emails) == 0 {
		return clientsToDisable, affectedInboundIds, nil
	}

	// Load ClientEntity records for these emails
	var clientEntities []*model.ClientEntity
	err := tx.Model(&model.ClientEntity{}).Where("LOWER(email) IN (?)", emails).Find(&clientEntities).Error
	if err != nil {
		return nil, nil, err
	}

	// Get inbound tags for clients that need to be disabled
	inboundIdMap := make(map[int]string) // map[inboundId]tag
	if len(affectedInboundIds) > 0 {
		inboundIdList := make([]int, 0, len(affectedInboundIds))
		for id := range affectedInboundIds {
			inboundIdList = append(inboundIdList, id)
		}
		var inbounds []*model.Inbound
		err = tx.Model(model.Inbound{}).Where("id IN (?)", inboundIdList).Find(&inbounds).Error
		if err == nil {
			for _, inbound := range inbounds {
				inboundIdMap[inbound.Id] = inbound.Tag
			}
		}
	}

	now := time.Now().Unix() * 1000
	
	// Update traffic for each client
	for _, client := range clientEntities {
		email := strings.ToLower(client.Email)
		trafficData, ok := emailTrafficMap[email]
		if !ok {
			continue
		}

		// Check limits BEFORE adding traffic
		currentUsed := client.Up + client.Down
		newUp := trafficData.Up
		newDown := trafficData.Down
		newTotal := newUp + newDown

		// Check if time is already expired
		timeExpired := client.ExpiryTime > 0 && client.ExpiryTime <= now

		// Check if adding this traffic would exceed the limit
		trafficLimit := int64(client.TotalGB * 1024 * 1024 * 1024)
		if client.TotalGB > 0 && trafficLimit > 0 {
			remaining := trafficLimit - currentUsed
			if remaining <= 0 {
				// Already exceeded, don't add any traffic
				newUp = 0
				newDown = 0
				newTotal = 0
			} else if newTotal > remaining {
				// Would exceed, add only up to the limit
				allowedTraffic := remaining
				// Proportionally distribute allowed traffic between up and down
				if newTotal > 0 {
					ratio := float64(allowedTraffic) / float64(newTotal)
					newUp = int64(float64(newUp) * ratio)
					newDown = int64(float64(newDown) * ratio)
					newTotal = allowedTraffic
				} else {
					newUp = 0
					newDown = 0
					newTotal = 0
				}
			}
		}

		// Add traffic (may be reduced if limit would be exceeded)
		// Note: ClientTraffic.Up = uplink (server→client) = Download for client
		//       ClientTraffic.Down = downlink (client→server) = Upload for client
		// So we swap them when saving to ClientEntity to match client perspective
		// IMPORTANT: All traffic values are in BYTES (not KB, MB, GB)
		// Xray API returns traffic in bytes, and we store it directly in bytes
		if newTotal > 0 {
			// Log traffic values to debug unit issues
			// If these values seem too small (e.g., 1024 bytes for 1MB transfer), 
			// it means traffic is being divided somewhere
			// Check if traffic values are suspiciously small (might be in KB instead of bytes)
			if trafficData.Up > 0 || trafficData.Down > 0 {
				logger.Debugf("AddClientTraffic: client %s - incoming Up: %d, Down: %d, Total: %d | "+
					"adding Up: %d, Down: %d | current total: %d bytes (Up: %.2f MB, Down: %.2f MB)", 
					client.Email, trafficData.Up, trafficData.Down, newTotal, newDown, newUp, client.Up+client.Down,
					float64(client.Up)/(1024*1024), float64(client.Down)/(1024*1024))
			}
		}
		// Store previous values for speed calculation (BEFORE updating)
		prevUp := client.Up
		prevDown := client.Down
		
		// Calculate speed BEFORE updating traffic values
		// This ensures we use the correct difference between old and new values
		currentTime := time.Now().Unix()
		
		// Get or create traffic state for this client
		trafficStateMu.Lock()
		state, exists := trafficStateMap[client.Id]
		if !exists {
			state = &clientTrafficState{
				prevUp:   prevUp,
				prevDown: prevDown,
				prevTime: currentTime,
			}
			trafficStateMap[client.Id] = state
		}
		trafficStateMu.Unlock()
		
		// Calculate speed if we have previous values
		state.mu.Lock()
		timeDiff := currentTime - state.prevTime
		if timeDiff > 0 && timeDiff <= 5 { // Only calculate if time diff is reasonable (1-5 seconds)
			// Calculate differences BEFORE updating client.Up/Down
			// NOTE: In ClientEntity (after swap in AddClientTraffic):
			// - client.Up = upload traffic (client sends to server) = prevUp + newDown
			// - client.Down = download traffic (client receives from server) = prevDown + newUp
			// But based on user feedback, they appear swapped, so we swap them back:
			// Calculate what the NEW values will be after adding traffic (with swap)
			futureUp := prevUp + newDown   // Upload will be: old + newDown (from trafficData)
			futureDown := prevDown + newUp // Download will be: old + newUp (from trafficData)
			
			// Calculate differences: new - old (using state.prevUp/Down from previous call)
			// Note: state.prevUp/Down are from the LAST time we updated, so they represent the "old" values
			// futureUp = upload traffic (client sends), futureDown = download traffic (client receives)
			upDiff := futureUp - state.prevUp      // Upload difference (client sends to server)
			downDiff := futureDown - state.prevDown // Download difference (client receives from server)
			
			// Calculate speed in BITS per second (not bytes)
			// Speed = bytes / seconds * 8 = bits per second
			// This matches standard internet speed measurement (Mbps, Gbps)
			// IMPORTANT: Both directions use the same calculation formula for consistency
			// Formula: (diff / timeDiff) * 8 = bits per second
			// Xray API returns traffic in BYTES, so we multiply by 8 to get bits
			// For 300 Mbps: 300 * 1024 * 1024 / 8 = 37.5 MB/s = 37,500,000 bytes/s
			// So if upDiff = 37,500,000 bytes and timeDiff = 1 sec, speed = 37,500,000 * 8 = 300,000,000 bps = 300 Mbps
			bytesPerSecUp := float64(upDiff) / float64(timeDiff)
			bytesPerSecDown := float64(downDiff) / float64(timeDiff)
			
			// Calculate speed in bits per second: bytes/sec * 8 = bits/sec
			// upDiff = upload difference (client sends), downDiff = download difference (client receives)
			// Assign correctly: UpSpeed = upload speed, DownSpeed = download speed
			client.UpSpeed = int64(bytesPerSecUp * 8)     // Upload speed (client sends)
			client.DownSpeed = int64(bytesPerSecDown * 8) // Download speed (client receives)
			
			// Log speed calculation for debugging (only for significant speeds > 10Kbps)
			// Log detailed info to understand units and verify calculation
			if (client.UpSpeed > 10*1024 || client.DownSpeed > 10*1024) && newTotal > 0 {
				logger.Debugf("AddClientTraffic: client %s speed calculation - "+
					"UpDiff: %d, DownDiff: %d, TimeDiff: %d sec, "+
					"BytesPerSecUp: %.2f, BytesPerSecDown: %.2f, "+
					"UpSpeed: %d bps (%.2f Mbps, %.2f Gbps), DownSpeed: %d bps (%.2f Mbps, %.2f Gbps)",
					client.Email, upDiff, downDiff, timeDiff,
					bytesPerSecUp, bytesPerSecDown,
					client.UpSpeed, float64(client.UpSpeed)/(1024*1024), float64(client.UpSpeed)/(1024*1024*1024),
					client.DownSpeed, float64(client.DownSpeed)/(1024*1024), float64(client.DownSpeed)/(1024*1024*1024))
			}
			
			// Ensure non-negative speeds
			if client.UpSpeed < 0 {
				client.UpSpeed = 0
			}
			if client.DownSpeed < 0 {
				client.DownSpeed = 0
			}
		} else {
			// Time diff too large or invalid, reset speed
			client.UpSpeed = 0
			client.DownSpeed = 0
		}
		
		// NOW update traffic values AFTER calculating speed
		client.Up += newDown   // Upload (client→server) goes to Up
		client.Down += newUp   // Download (server→client) goes to Down
		client.AllTime += newTotal
		
		// Update state for next calculation (use updated values)
		state.prevUp = client.Up
		state.prevDown = client.Down
		state.prevTime = currentTime
		state.mu.Unlock()

		// Check final state after adding traffic
		finalUsed := client.Up + client.Down
		finalTrafficExceeded := client.TotalGB > 0 && finalUsed >= trafficLimit

		// Mark client with expired status if limit exceeded or time expired
		if (finalTrafficExceeded || timeExpired) && client.Enable {
			// Update status if not already set or if reason changed
			shouldUpdateStatus := false
			if finalTrafficExceeded && client.Status != "expired_traffic" {
				client.Status = "expired_traffic"
				shouldUpdateStatus = true
			} else if timeExpired && client.Status != "expired_time" {
				client.Status = "expired_time"
				shouldUpdateStatus = true
			}
			
			// Only add to disable list if status was just set (not already expired)
			// This prevents repeated attempts to remove already-removed clients
			if shouldUpdateStatus {
				// Mark for removal from Xray API - get all inbound IDs for this client
				clientInboundIds, err := s.GetInboundIdsForClient(client.Id)
				if err == nil && len(clientInboundIds) > 0 {
					// Try to find tag from inboundIdMap first (from traffic data)
					found := false
					for _, inboundId := range clientInboundIds {
						if tag, ok := inboundIdMap[inboundId]; ok {
							clientsToDisable[client.Email] = tag
							found = true
							break
						}
					}
					// If not found in map, query database for tag
					if !found {
						var inbound model.Inbound
						if err := tx.Model(&model.Inbound{}).Where("id = ?", clientInboundIds[0]).First(&inbound).Error; err == nil {
							clientsToDisable[client.Email] = inbound.Tag
						}
					}
				}
				
				logger.Infof("Client %s marked with status %s: trafficExceeded=%v, timeExpired=%v, currentUsed=%d, newTraffic=%d, finalUsed=%d, total=%d",
					client.Email, client.Status, finalTrafficExceeded, timeExpired, currentUsed, newTotal, finalUsed, trafficLimit)
			}
		}

		// Add user in onlineUsers array on traffic (only if not disabled)
		if newTotal > 0 && client.Enable {
			onlineClients = append(onlineClients, client.Email)
			// Check if this is the first connection (LastOnline was 0 before update)
			wasFirstConnection := client.LastOnline == 0
			client.LastOnline = time.Now().UnixMilli()
			
			// Send notification about first connection
			if wasFirstConnection {
				go func(c *model.ClientEntity) {
					tgbotService := Tgbot{}
					if tgbotService.IsRunning() {
						tgbotService.NotifyClientFirstConnection(c)
					}
				}(client)
			}
		}
	}

	// Set onlineUsers
	if p != nil {
		p.SetOnlineClients(onlineClients)
	}

	// Save client entities with retry logic for database lock errors
	maxRetries := 3
	baseDelay := 10 * time.Millisecond
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseDelay * time.Duration(1<<uint(attempt-1))
			logger.Debugf("Retrying Save client entities (attempt %d/%d) after %v", attempt+1, maxRetries, delay)
			time.Sleep(delay)
		}
		
		err = tx.Save(clientEntities).Error
		if err == nil {
			break
		}
		
		// Check if error is "database is locked"
		errStr := err.Error()
		if strings.Contains(errStr, "database is locked") || strings.Contains(errStr, "locked") {
			if attempt < maxRetries-1 {
				logger.Debugf("Database locked when saving client entities, will retry: %v", err)
				continue
			}
			// Last attempt failed
			logger.Warningf("Failed to save client entities after %d retries: %v", maxRetries, err)
			return nil, nil, err
		}
		
		// For other errors, don't retry
		logger.Warning("AddClientTraffic update data ", err)
		return nil, nil, err
	}

	// Synchronize inbound traffic as sum of all its clients' traffic
	// IMPORTANT: Sync ALL inbounds, not just affected ones, to ensure accurate totals
	if inboundService != nil {
		// Get all inbounds to sync their traffic
		allInbounds, err := inboundService.GetAllInbounds()
		if err == nil {
			allInboundIds := make(map[int]bool)
			for _, inbound := range allInbounds {
				allInboundIds[inbound.Id] = true
			}
			err = s.syncInboundTrafficFromClients(tx, allInboundIds, inboundService)
			if err != nil {
				logger.Warningf("Failed to sync inbound traffic from clients: %v", err)
				// Don't fail the whole operation, but log the warning
			}
		} else {
			logger.Warningf("Failed to get all inbounds for traffic sync: %v", err)
			// Fallback: sync only affected inbounds
			err = s.syncInboundTrafficFromClients(tx, affectedInboundIds, inboundService)
			if err != nil {
				logger.Warningf("Failed to sync affected inbound traffic: %v", err)
			}
		}
	}

	return clientsToDisable, affectedInboundIds, nil
}

// syncInboundTrafficFromClients synchronizes inbound traffic as the sum of all its clients' traffic.
// This ensures that inbound traffic always equals the sum of all its clients' traffic.
// Traffic is now stored in ClientEntity, so we sum traffic from all enabled clients assigned to each inbound.
func (s *ClientService) syncInboundTrafficFromClients(tx *gorm.DB, inboundIds map[int]bool, inboundService *InboundService) error {
	if len(inboundIds) == 0 {
		return nil
	}

	inboundIdList := make([]int, 0, len(inboundIds))
	for id := range inboundIds {
		inboundIdList = append(inboundIdList, id)
	}

	// For each inbound, get all its clients and sum their traffic
	for _, inboundId := range inboundIdList {
		// Get all clients assigned to this inbound
		clientEntities, err := s.GetClientsForInbound(inboundId)
		if err != nil {
			logger.Warningf("Failed to get clients for inbound %d: %v", inboundId, err)
			continue
		}

		// Sum traffic from ALL clients (both enabled and disabled) for inbound statistics
		// This ensures inbound traffic reflects total usage, not just active clients
		var totalUp int64
		var totalDown int64
		var totalAllTime int64
		enabledClientCount := 0
		totalClientCount := len(clientEntities)

		for _, client := range clientEntities {
			// Sum traffic from all clients (enabled and disabled) for statistics
			totalUp += client.Up
			totalDown += client.Down
			totalAllTime += client.AllTime
			if client.Enable {
				enabledClientCount++
			}
		}

		// Update inbound traffic
		err = tx.Model(&model.Inbound{}).Where("id = ?", inboundId).
			Updates(map[string]any{
				"up":       totalUp,
				"down":     totalDown,
				"all_time": totalAllTime,
			}).Error
		if err != nil {
			logger.Warningf("Failed to sync inbound %d traffic: %v", inboundId, err)
			continue
		}
		logger.Debugf("Synced inbound %d traffic: up=%d, down=%d, all_time=%d (from %d total clients, %d enabled)",
			inboundId, totalUp, totalDown, totalAllTime, totalClientCount, enabledClientCount)
	}

	return nil
}
