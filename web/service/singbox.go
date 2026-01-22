package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/singbox"
	"github.com/mhsanaei/3x-ui/v2/util/json_util"

	"go.uber.org/atomic"
)

var (
	singboxProcess        *singbox.Process
	singboxLock           sync.Mutex
	isNeedSingBoxRestart atomic.Bool // Indicates that restart was requested for sing-box
	isManuallyStoppedSB   atomic.Bool // Indicates that sing-box was stopped manually from the panel
	singboxResult         string
	// API connection pool: map[apiPort]*singbox.SingBoxAPI
	singboxAPIConnectionPool sync.Map
	singboxAPIPoolLock       sync.Mutex
)

// SingBoxService provides business logic for sing-box process management.
// It handles starting, stopping, restarting sing-box, and managing its configuration.
// In multi-node mode, it sends configurations to nodes instead of running sing-box locally.
type SingBoxService struct {
	inboundService InboundService
	settingService SettingService
	nodeService    NodeService
	singboxAPI     singbox.SingBoxAPI
}

// NewSingBoxService creates a new SingBoxService with default dependencies.
func NewSingBoxService() SingBoxService {
	return SingBoxService{
		inboundService: InboundService{},
		settingService: SettingService{},
		nodeService:    NodeService{},
	}
}

// IsSingBoxRunning checks if the sing-box process is currently running.
func (s *SingBoxService) IsSingBoxRunning() bool {
	return singboxProcess != nil && singboxProcess.IsRunning()
}

// GetOrCreateAPI gets or creates a cached SingBoxAPI connection for the given API port.
func (s *SingBoxService) GetOrCreateAPI(apiPort int) (*singbox.SingBoxAPI, func(), error) {
	if apiPort <= 0 {
		return nil, nil, fmt.Errorf("invalid API port: %d", apiPort)
	}

	// Try to get existing connection
	if conn, ok := singboxAPIConnectionPool.Load(apiPort); ok {
		api := conn.(*singbox.SingBoxAPI)
		if api.IsConnected() {
			return api, func() {}, nil
		}
		singboxAPIConnectionPool.Delete(apiPort)
	}

	// Create new connection
	singboxAPIPoolLock.Lock()
	defer singboxAPIPoolLock.Unlock()

	// Double-check after acquiring lock
	if conn, ok := singboxAPIConnectionPool.Load(apiPort); ok {
		api := conn.(*singbox.SingBoxAPI)
		if api.IsConnected() {
			return api, func() {}, nil
		}
	}

	// Create new API connection
	api := &singbox.SingBoxAPI{}
	if err := api.Init(apiPort); err != nil {
		return nil, nil, fmt.Errorf("failed to init SingBoxAPI: %w", err)
	}

	// Store in pool
	singboxAPIConnectionPool.Store(apiPort, api)

	return api, func() {}, nil
}

// CloseAPIConnections closes all cached API connections.
func (s *SingBoxService) CloseAPIConnections() {
	singboxAPIConnectionPool.Range(func(key, value interface{}) bool {
		api := value.(*singbox.SingBoxAPI)
		api.Close()
		singboxAPIConnectionPool.Delete(key)
		return true
	})
	logger.Debug("All sing-box API connections closed")
}

// GetSingBoxErr returns the error from the sing-box process, if any.
func (s *SingBoxService) GetSingBoxErr() error {
	if singboxProcess == nil {
		return nil
	}

	err := singboxProcess.GetErr()

	if runtime.GOOS == "windows" && err != nil && err.Error() == "exit status 1" {
		return nil
	}

	return err
}

// GetSingBoxResult returns the result string from the sing-box process.
func (s *SingBoxService) GetSingBoxResult() string {
	if singboxResult != "" {
		return singboxResult
	}
	if s.IsSingBoxRunning() {
		return ""
	}
	if singboxProcess == nil {
		return ""
	}

	singboxResult = singboxProcess.GetResult()

	if runtime.GOOS == "windows" && singboxResult == "exit status 1" {
		return ""
	}

	return singboxResult
}

// GetSingBoxVersion returns the version of the running sing-box process.
func (s *SingBoxService) GetSingBoxVersion() string {
	if singboxProcess == nil {
		return "Unknown"
	}
	return singboxProcess.GetVersion()
}

// GetSingBoxConfig retrieves and builds the sing-box configuration from settings and inbounds.
func (s *SingBoxService) GetSingBoxConfig() (*singbox.Config, error) {
	// Ensure singboxTemplateConfig is valid before using it
	if err := s.settingService.EnsureSingBoxTemplateConfigValid(); err != nil {
		logger.Debugf("[DEBUG-AGENT] GetSingBoxConfig: failed EnsureSingBoxTemplateConfigValid: %v", err)
	}

	templateConfig, err := s.settingService.GetSingBoxConfigTemplate()
	if err != nil {
		logger.Debugf("[DEBUG-AGENT] GetSingBoxConfig: GetSingBoxConfigTemplate error: %v", err)
		return nil, err
	}

	singboxConfig := &singbox.Config{}
	err = json.Unmarshal([]byte(templateConfig), singboxConfig)
	if err != nil {
		logger.Debugf("[DEBUG-AGENT] GetSingBoxConfig: failed to unmarshal template JSON: %v", err)
		return nil, err
	}

	// Filter out any unsupported inbounds from template (like tunnel API inbound)
	// This can happen if the template was converted from Xray config
	// Also clean up invalid transport objects (empty or without valid type)
	filteredInbounds := make([]singbox.InboundConfig, 0, len(singboxConfig.Inbounds))
	for i, ib := range singboxConfig.Inbounds {
		if ib.Type == "tunnel" || ib.Type == "wireguard" {
			logger.Warningf("GetSingBoxConfig: filtering out unsupported inbound from template: type=%s, tag=%s (index %d)", ib.Type, ib.Tag, i)
			continue
		}
		
		// Clean up invalid transport objects
		if len(ib.Transport) > 0 {
			var transport map[string]interface{}
			if err := json.Unmarshal(ib.Transport, &transport); err == nil {
				// Check if transport has valid type
				transportType, hasType := transport["type"].(string)
				validTransportTypes := map[string]bool{
					"ws":         true,
					"grpc":       true,
					"quic":       true,
					"http":       true,
					"httpupgrade": true,
				}
				
				// Remove reality/tls from transport (should be in tls field)
				if _, hasReality := transport["reality"]; hasReality {
					logger.Warningf("GetSingBoxConfig: removing reality from transport in template inbound %s (tag: %s, index %d)", ib.Type, ib.Tag, i)
					delete(transport, "reality")
				}
				if _, hasTLS := transport["tls"]; hasTLS {
					logger.Warningf("GetSingBoxConfig: removing tls from transport in template inbound %s (tag: %s, index %d)", ib.Type, ib.Tag, i)
					delete(transport, "tls")
				}
				
				// If transport is empty or has invalid type, remove it
				if !hasType || transportType == "" || !validTransportTypes[transportType] {
					logger.Warningf("GetSingBoxConfig: removing invalid transport from template inbound %s (tag: %s, index %d)", ib.Type, ib.Tag, i)
					ib.Transport = nil
				} else {
					// Update transport if it was modified
					transportJSON, _ := json.Marshal(transport)
					ib.Transport = json_util.RawMessage(transportJSON)
				}
			}
		}
		
		// Clean up users: VLESS uses "name" instead of "email" in sing-box
		if len(ib.Users) > 0 {
			var users []map[string]interface{}
			if err := json.Unmarshal(ib.Users, &users); err == nil {
				needsUpdate := false
				for _, user := range users {
					// Convert "email" to "name" for VLESS
					if ib.Type == "vless" {
						if email, ok := user["email"].(string); ok && email != "" {
							user["name"] = email
							delete(user, "email")
							needsUpdate = true
						}
					}
					// Remove "email" for VMESS (not used in sing-box)
					if ib.Type == "vmess" {
						if _, ok := user["email"]; ok {
							delete(user, "email")
							needsUpdate = true
						}
					}
				}
				if needsUpdate {
					usersJSON, _ := json.Marshal(users)
					ib.Users = json_util.RawMessage(usersJSON)
					logger.Warningf("GetSingBoxConfig: converted email to name in users for template inbound %s (tag: %s, index %d)", ib.Type, ib.Tag, i)
				}
			}
		}
		
		// Clean up Reality fields: server_name goes at tls level, short_id must be array
		if len(ib.TLS) > 0 {
			var tls map[string]interface{}
			if err := json.Unmarshal(ib.TLS, &tls); err == nil {
				if reality, ok := tls["reality"].(map[string]interface{}); ok {
					needsUpdate := false
					// server_name should be at tls level, not in reality
					if serverName, ok := reality["server_name"].(string); ok {
						// Move server_name to tls level
						tls["server_name"] = serverName
						delete(reality, "server_name")
						needsUpdate = true
						logger.Warningf("GetSingBoxConfig: moved server_name from reality to tls level in template inbound %s (tag: %s, index %d)", ib.Type, ib.Tag, i)
					}
					// Remove server_names from reality (should be at tls level)
					if serverNames, ok := reality["server_names"].([]interface{}); ok && len(serverNames) > 0 {
						// Move first server name to tls level
						if firstServerName, ok := serverNames[0].(string); ok {
							tls["server_name"] = firstServerName
						}
						delete(reality, "server_names")
						needsUpdate = true
						logger.Warningf("GetSingBoxConfig: moved server_names to server_name at tls level in template inbound %s (tag: %s, index %d)", ib.Type, ib.Tag, i)
					}
					// Ensure short_id is an array (required for inbound Reality)
					if shortId, ok := reality["short_id"].(string); ok {
						// Convert string to array
						reality["short_id"] = []string{shortId}
						needsUpdate = true
						logger.Warningf("GetSingBoxConfig: converted short_id from string to array in template inbound %s (tag: %s, index %d)", ib.Type, ib.Tag, i)
					}
					// Ensure handshake exists (required for Reality inbound)
					if _, hasHandshake := reality["handshake"]; !hasHandshake {
						// Add default handshake if server_name is available
						if serverName, ok := tls["server_name"].(string); ok && serverName != "" {
							reality["handshake"] = map[string]interface{}{
								"server": serverName,
							}
							needsUpdate = true
							logger.Warningf("GetSingBoxConfig: added handshake to reality in template inbound %s (tag: %s, index %d)", ib.Type, ib.Tag, i)
						}
					}
					if needsUpdate {
						tlsJSON, _ := json.Marshal(tls)
						ib.TLS = json_util.RawMessage(tlsJSON)
					}
				}
			}
		}
		
		filteredInbounds = append(filteredInbounds, ib)
	}
	
	// Get all inbounds from database first to collect their tags
	s.inboundService.AddTraffic(nil, nil)
	inbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return nil, err
	}
	
	// Collect all tags from database inbounds to check for duplicates
	dbInboundTags := make(map[string]bool)
	for _, inbound := range inbounds {
		if inbound.Enable && inbound.Tag != "" {
			dbInboundTags[inbound.Tag] = true
		}
	}
	
	// Filter template inbounds: remove those that conflict with database inbounds
	finalFilteredInbounds := make([]singbox.InboundConfig, 0, len(filteredInbounds))
	for _, ib := range filteredInbounds {
		if ib.Tag != "" {
			if dbInboundTags[ib.Tag] {
				logger.Warningf("GetSingBoxConfig: skipping template inbound with tag '%s' (conflicts with database inbound)", ib.Tag)
				continue
			}
			// Mark this tag as used
			dbInboundTags[ib.Tag] = true
		}
		finalFilteredInbounds = append(finalFilteredInbounds, ib)
	}
	singboxConfig.Inbounds = finalFilteredInbounds
	logger.Debugf("GetSingBoxConfig: template had %d inbounds, after filtering: %d (removed %d conflicting)", len(filteredInbounds), len(finalFilteredInbounds), len(filteredInbounds)-len(finalFilteredInbounds))

	// Filter and convert outbounds from template
	// Xray uses "freedom" (direct), "blackhole" (block), and may have "tun"/"tunnel"
	// sing-box uses "direct", "block", and doesn't support "tun"/"tunnel" outbounds
	if len(singboxConfig.Outbounds) > 0 {
		var outboundArray []map[string]interface{}
		if err := json.Unmarshal(singboxConfig.Outbounds, &outboundArray); err == nil {
			filteredOutbounds := make([]map[string]interface{}, 0, len(outboundArray))
			for i, ob := range outboundArray {
				obType, _ := ob["type"].(string)
				obProtocol, _ := ob["protocol"].(string) // Xray uses "protocol"
				obTag, _ := ob["tag"].(string)
				
				// Check both "type" (sing-box) and "protocol" (Xray) fields
				actualType := obType
				if actualType == "" {
					actualType = obProtocol
				}
				
				if actualType == "tun" || actualType == "tunnel" {
					logger.Warningf("GetSingBoxConfig: filtering out unsupported outbound from template: type=%s, tag=%s (index %d)", actualType, obTag, i)
					continue
				}
				
				// Convert Xray protocol names to sing-box type names
				if obProtocol != "" && obType == "" {
					switch obProtocol {
					case "freedom":
						ob["type"] = "direct"
						delete(ob, "protocol")
						// Clean up Xray-specific settings
						if settings, ok := ob["settings"].(map[string]interface{}); ok {
							delete(settings, "domainStrategy")
							delete(settings, "redirect")
							delete(settings, "noises")
						}
					case "blackhole":
						ob["type"] = "block"
						delete(ob, "protocol")
						// Clean up settings for block
						ob["settings"] = map[string]interface{}{}
					}
				}
				
				// Convert domainStrategy from settings to domain_strategy at outbound level
				// sing-box uses domain_strategy at outbound level, not in settings
				if settings, ok := ob["settings"].(map[string]interface{}); ok {
					if domainStrategy, ok := settings["domainStrategy"].(string); ok {
						ob["domain_strategy"] = domainStrategy
						delete(settings, "domainStrategy")
						logger.Warningf("GetSingBoxConfig: moved domainStrategy from settings to domain_strategy at outbound level for outbound %s (tag: %s, index %d)", actualType, obTag, i)
					}
				}
				
				filteredOutbounds = append(filteredOutbounds, ob)
			}
			outboundJSON, _ := json.Marshal(filteredOutbounds)
			singboxConfig.Outbounds = json_util.RawMessage(outboundJSON)
		}
	}

	// Clean up route: remove domainStrategy/domain_strategy and clean up rules
	if len(singboxConfig.Route) > 0 {
		var route map[string]interface{}
		if err := json.Unmarshal(singboxConfig.Route, &route); err == nil {
			needsUpdate := false
			if _, ok := route["domainStrategy"]; ok {
				delete(route, "domainStrategy")
				needsUpdate = true
				logger.Warningf("GetSingBoxConfig: removed domainStrategy from route (not supported by sing-box)")
			}
			if _, ok := route["domain_strategy"]; ok {
				delete(route, "domain_strategy")
				needsUpdate = true
				logger.Warningf("GetSingBoxConfig: removed domain_strategy from route (not supported by sing-box)")
			}
			
			// Check if geoip is configured in route (required for geoip rules in sing-box 1.12.0+)
			hasGeoipConfig := false
			if _, ok := route["geoip"].(map[string]interface{}); ok {
				hasGeoipConfig = true
			}
			
			// Clean up route rules: remove "type": "field" and convert Xray-specific fields
			if rules, ok := route["rules"].([]interface{}); ok {
				filteredRules := make([]interface{}, 0, len(rules))
				for _, rule := range rules {
					if ruleMap, ok := rule.(map[string]interface{}); ok {
						// Remove "type": "field" (Xray-specific, not supported by sing-box)
						if _, ok := ruleMap["type"]; ok {
							delete(ruleMap, "type")
							needsUpdate = true
							logger.Warningf("GetSingBoxConfig: removed type field from route rule (not supported by sing-box)")
						}
						// Convert Xray field names to sing-box field names
						// Xray uses "outboundTag", sing-box uses "outbound"
						if outboundTag, ok := ruleMap["outboundTag"].(string); ok {
							ruleMap["outbound"] = outboundTag
							delete(ruleMap, "outboundTag")
							needsUpdate = true
						}
						// Xray uses "inboundTag", sing-box uses "inbound"
						if inboundTag, ok := ruleMap["inboundTag"].([]interface{}); ok {
							ruleMap["inbound"] = inboundTag
							delete(ruleMap, "inboundTag")
							needsUpdate = true
						}
						// Xray uses "ip" with "geoip:private", sing-box uses "geoip" array
						// But in sing-box 1.12.0+, geoip requires geoip database to be configured
						if ip, ok := ruleMap["ip"].([]interface{}); ok {
							geoipList := make([]string, 0)
							regularIPs := make([]interface{}, 0)
							for _, ipItem := range ip {
								if ipStr, ok := ipItem.(string); ok {
									if strings.HasPrefix(ipStr, "geoip:") {
										// Extract geoip value (e.g., "geoip:private" -> "private")
										geoipValue := strings.TrimPrefix(ipStr, "geoip:")
										geoipList = append(geoipList, geoipValue)
									} else {
										// Regular IP, keep as is
										regularIPs = append(regularIPs, ipStr)
									}
								}
							}
							if len(geoipList) > 0 {
								if hasGeoipConfig {
									// geoip is configured, can use geoip rules
									ruleMap["geoip"] = geoipList
									delete(ruleMap, "ip")
									needsUpdate = true
								} else {
									// geoip not configured, remove this rule (sing-box 1.12.0+ requires geoip database)
									logger.Warningf("GetSingBoxConfig: removing route rule with geoip (geoip database not configured, required in sing-box 1.12.0+)")
									needsUpdate = true
									continue
								}
							}
							if len(regularIPs) > 0 {
								ruleMap["ip"] = regularIPs
							} else if len(geoipList) == 0 {
								delete(ruleMap, "ip")
							}
						}
						// Check if rule has geoip field directly (from template or already converted)
						if geoip, ok := ruleMap["geoip"].([]interface{}); ok && len(geoip) > 0 {
							if !hasGeoipConfig {
								// geoip not configured, remove this rule
								logger.Warningf("GetSingBoxConfig: removing route rule with geoip (geoip database not configured, required in sing-box 1.12.0+)")
								needsUpdate = true
								continue
							}
						} else if geoipStr, ok := ruleMap["geoip"].(string); ok && geoipStr != "" {
							if !hasGeoipConfig {
								// geoip not configured, remove this rule
								logger.Warningf("GetSingBoxConfig: removing route rule with geoip (geoip database not configured, required in sing-box 1.12.0+)")
								needsUpdate = true
								continue
							}
						}
						filteredRules = append(filteredRules, ruleMap)
					} else {
						filteredRules = append(filteredRules, rule)
					}
				}
				if len(filteredRules) != len(rules) {
					route["rules"] = filteredRules
				}
			}
			
			if needsUpdate {
				routeJSON, _ := json.Marshal(route)
				singboxConfig.Route = json_util.RawMessage(routeJSON)
			}
		}
	}

	for _, inbound := range inbounds {
		if !inbound.Enable {
			continue
		}

		// Process clients (same logic as GetXrayConfig)
		settings := map[string]any{}
		json.Unmarshal([]byte(inbound.Settings), &settings)
		clients, ok := settings["clients"].([]any)
		if ok {
			// check users active or not
			clientStats := inbound.ClientStats
			for _, clientTraffic := range clientStats {
				indexDecrease := 0
				for index, client := range clients {
					c := client.(map[string]any)
					if c["email"] == clientTraffic.Email {
						if !clientTraffic.Enable {
							clients = RemoveIndex(clients, index-indexDecrease)
							indexDecrease++
							logger.Infof("Remove Inbound User %s due to expiration or traffic limit", c["email"])
						}
					}
				}
			}

			// clear client config for additional parameters
			var final_clients []any
			for _, client := range clients {
				c := client.(map[string]any)
				if c["enable"] != nil {
					if enable, ok := c["enable"].(bool); ok && !enable {
						continue
					}
				}
				for key := range c {
					if key != "email" && key != "id" && key != "password" && key != "flow" && key != "method" {
						delete(c, key)
					}
					if c["flow"] == "xtls-rprx-vision-udp443" {
						c["flow"] = "xtls-rprx-vision"
					}
				}
				final_clients = append(final_clients, any(c))
			}

			settings["clients"] = final_clients
			modifiedSettings, err := json.MarshalIndent(settings, "", "  ")
			if err != nil {
				return nil, err
			}

			inbound.Settings = string(modifiedSettings)
		}

		if len(inbound.StreamSettings) > 0 {
			// Unmarshal stream JSON
			var stream map[string]any
			json.Unmarshal([]byte(inbound.StreamSettings), &stream)

			// Remove the "settings" field under "tlsSettings" and "realitySettings"
			tlsSettings, ok1 := stream["tlsSettings"].(map[string]any)
			realitySettings, ok2 := stream["realitySettings"].(map[string]any)
			if ok1 || ok2 {
				if ok1 {
					delete(tlsSettings, "settings")
				} else if ok2 {
					delete(realitySettings, "settings")
				}
			}

			delete(stream, "externalProxy")

			newStream, err := json.MarshalIndent(stream, "", "  ")
			if err != nil {
				return nil, err
			}
			inbound.StreamSettings = string(newStream)
		}

		// Log before generating config
		logger.Debugf("GetSingBoxConfig: processing inbound tag=%s, protocol=%s, remark=%s", inbound.Tag, inbound.Protocol, inbound.Remark)
		
		inboundConfig := inbound.GenSingBoxInboundConfig()
		if inboundConfig == nil {
			// Skip unsupported protocols (like "tunnel")
			logger.Warningf("Skipping inbound %s (tag: %s) - protocol '%s' is not supported by sing-box", inbound.Remark, inbound.Tag, inbound.Protocol)
			continue
		}
		
		// Log after generating config
		logger.Debugf("GetSingBoxConfig: adding inbound type=%s, tag=%s", inboundConfig.Type, inboundConfig.Tag)
		
		// Check for duplicate tags before adding
		if inboundConfig.Tag != "" {
			// Check if tag already exists in config
			tagExists := false
			for _, existingInbound := range singboxConfig.Inbounds {
				if existingInbound.Tag == inboundConfig.Tag {
					tagExists = true
					logger.Warningf("GetSingBoxConfig: skipping inbound %s (tag: %s) - duplicate tag detected", inbound.Remark, inboundConfig.Tag)
					break
				}
			}
			if !tagExists {
				singboxConfig.Inbounds = append(singboxConfig.Inbounds, *inboundConfig)
			}
		} else {
			// If no tag, add anyway (shouldn't happen, but handle it)
			singboxConfig.Inbounds = append(singboxConfig.Inbounds, *inboundConfig)
		}
	}
	return singboxConfig, nil
}

// GetSingBoxTraffic fetches the current traffic statistics from the running sing-box process.
func (s *SingBoxService) GetSingBoxTraffic() ([]*singbox.Traffic, []*singbox.ClientTraffic, error) {
	if !s.IsSingBoxRunning() {
		err := errors.New("sing-box is not running")
		logger.Debug("Attempted to fetch sing-box traffic, but sing-box is not running:", err)
		return nil, nil, err
	}
	apiPort := singboxProcess.GetAPIPort()
	api, cleanup, err := s.GetOrCreateAPI(apiPort)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()

	traffic, clientTraffic, err := api.GetTraffic(true)
	if err != nil {
		logger.Debug("Failed to fetch sing-box traffic:", err)
		return nil, nil, err
	}
	return traffic, clientTraffic, nil
}

// RestartSingBox restarts the sing-box process, optionally forcing a restart even if config unchanged.
// In multi-node mode, it sends configurations to nodes instead of restarting local sing-box.
func (s *SingBoxService) RestartSingBox(isForce bool) error {
	singboxLock.Lock()
	defer singboxLock.Unlock()
	logger.Debug("restart sing-box, force:", isForce)
	isManuallyStoppedSB.Store(false)

	// Check if multi-node mode is enabled
	multiMode, err := s.settingService.GetMultiNodeMode()
	if err != nil {
		multiMode = false
	}

	if multiMode {
		return s.restartSingBoxMultiMode(isForce)
	}

	// Single mode: use local sing-box
	singboxConfig, err := s.GetSingBoxConfig()
	if err != nil {
		return err
	}

	if s.IsSingBoxRunning() {
		if !isForce && singboxProcess.GetConfig().Equals(singboxConfig) && !isNeedSingBoxRestart.Load() {
			logger.Debug("It does not need to restart sing-box")
			return nil
		}
		// Close API connections before stopping sing-box
		s.CloseAPIConnections()
		singboxProcess.Stop()
	}

	singboxProcess = singbox.NewProcess(singboxConfig)
	singboxResult = ""
	err = singboxProcess.Start()
	if err != nil {
		return err
	}

	return nil
}

// RestartSingBoxAsync restarts sing-box asynchronously in a goroutine.
func (s *SingBoxService) RestartSingBoxAsync(isForce bool) {
	go func() {
		if err := s.RestartSingBox(isForce); err != nil {
			logger.Warningf("Failed to restart sing-box asynchronously: %v", err)
		} else {
			logger.Debug("sing-box restarted asynchronously")
		}
	}()
}

// restartSingBoxMultiMode handles sing-box restart in multi-node mode by sending configs to nodes.
func (s *SingBoxService) restartSingBoxMultiMode(isForce bool) error {
	// Initialize nodeService if not already initialized
	if s.nodeService == (NodeService{}) {
		s.nodeService = NodeService{}
	}

	// Get all nodes
	nodes, err := s.nodeService.GetAllNodes()
	if err != nil {
		return fmt.Errorf("failed to get nodes: %w", err)
	}

	// Group inbounds by node
	nodeInbounds := make(map[int][]*model.Inbound)
	allInbounds, err := s.inboundService.GetAllInbounds()
	if err != nil {
		return fmt.Errorf("failed to get inbounds: %w", err)
	}

	// Get template config
	if err := s.settingService.EnsureSingBoxTemplateConfigValid(); err != nil {
		logger.Warningf("Failed to ensure singboxTemplateConfig is valid in restartSingBoxMultiMode: %v", err)
	}

	templateConfig, err := s.settingService.GetSingBoxConfigTemplate()
	if err != nil {
		return err
	}

	baseConfig := &singbox.Config{}
	if err := json.Unmarshal([]byte(templateConfig), baseConfig); err != nil {
		return err
	}

	// Group inbounds by their assigned nodes
	for _, inbound := range allInbounds {
		if !inbound.Enable {
			continue
		}

		// Get all nodes assigned to this inbound
		nodes, err := s.nodeService.GetNodesForInbound(inbound.Id)
		if err != nil || len(nodes) == 0 {
			continue
		}

		// Add inbound to all assigned nodes
		for _, node := range nodes {
			nodeInbounds[node.Id] = append(nodeInbounds[node.Id], inbound)
		}
	}

	// Send config to each node in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []error

	// Helper function to build config for a node
	buildNodeConfig := func(node *model.Node, inbounds []*model.Inbound) ([]byte, error) {
		nodeConfig := *baseConfig
		nodeConfig.Inbounds = []singbox.InboundConfig{}

		for _, inbound := range inbounds {
		inboundConfig := inbound.GenSingBoxInboundConfig()
		if inboundConfig == nil {
			// Skip unsupported protocols (like "tunnel")
			logger.Warningf("Skipping inbound %s (tag: %s) - protocol '%s' is not supported by sing-box", inbound.Remark, inbound.Tag, inbound.Protocol)
			continue
		}
		nodeConfig.Inbounds = append(nodeConfig.Inbounds, *inboundConfig)
		}

		// Marshal config to JSON
		return json.MarshalIndent(&nodeConfig, "", "  ")
	}

	// Send configs to all nodes in parallel
	for _, node := range nodes {
		inbounds, ok := nodeInbounds[node.Id]
		if !ok {
			continue
		}

		wg.Add(1)
		go func(n *model.Node, ibs []*model.Inbound) {
			defer wg.Done()

			configJSON, err := buildNodeConfig(n, ibs)
			if err != nil {
				logger.Errorf("[Node: %s] Failed to marshal config: %v", n.Name, err)
				mu.Lock()
				errors = append(errors, fmt.Errorf("node %s: failed to marshal config: %w", n.Name, err))
				mu.Unlock()
				return
			}

			if err := s.nodeService.ApplyConfigToNode(n, configJSON); err != nil {
				logger.Errorf("[Node: %s] Failed to apply config: %v", n.Name, err)
				mu.Lock()
				errors = append(errors, fmt.Errorf("node %s: %w", n.Name, err))
				mu.Unlock()
			} else {
				logger.Infof("[Node: %s] Successfully applied config", n.Name)
			}
		}(node, inbounds)
	}

	wg.Wait()

	if len(errors) > 0 {
		logger.Warningf("Failed to apply config to some nodes: %d error(s)", len(errors))
		for _, err := range errors {
			logger.Warningf("  - %v", err)
		}
		if len(errors) == len(nodes) {
			return fmt.Errorf("failed to apply config to all nodes: %d errors", len(errors))
		}
	} else {
		logger.Infof("Successfully applied config to all %d node(s)", len(nodes))
	}

	return nil
}

// EnsureSingBoxConfigFile generates and saves the sing-box configuration file from database.
func (s *SingBoxService) EnsureSingBoxConfigFile() error {
	if err := s.settingService.EnsureSingBoxTemplateConfigValid(); err != nil {
		logger.Warningf("Failed to ensure singboxTemplateConfig is valid in EnsureSingBoxConfigFile: %v", err)
	}

	cfg, err := s.GetSingBoxConfig()
	if err != nil {
		return err
	}

	// Log how many inbounds are in the config for debugging
	logger.Infof("EnsureSingBoxConfigFile: generated config with %d inbounds", len(cfg.Inbounds))
	for i, inbound := range cfg.Inbounds {
		logger.Infof("EnsureSingBoxConfigFile: inbound[%d]: type=%s, tag=%s", i, inbound.Type, inbound.Tag)
		if inbound.Type == "tunnel" {
			logger.Errorf("ERROR: Found tunnel inbound in config at index %d! This should have been filtered out!", i)
		}
	}

	if _, err := singbox.WriteConfigFile(cfg); err != nil {
		return err
	}

	logger.Info("sing-box configuration file pre-generated from database")
	return nil
}

// StopSingBox stops the running sing-box process.
func (s *SingBoxService) StopSingBox() error {
	singboxLock.Lock()
	defer singboxLock.Unlock()
	isManuallyStoppedSB.Store(true)
	logger.Debug("Attempting to stop sing-box...")
	if s.IsSingBoxRunning() {
		s.CloseAPIConnections()
		return singboxProcess.Stop()
	}
	logger.Debug("sing-box is not running, nothing to stop")
	return nil
}
