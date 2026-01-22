package singbox

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mhsanaei/3x-ui/v2/util/json_util"
	"github.com/mhsanaei/3x-ui/v2/xray"
)

// ConvertXrayToSingBox converts an Xray configuration to sing-box format.
// This is a basic conversion that handles the most common cases.
func ConvertXrayToSingBox(xrayConfig *xray.Config) (*Config, error) {
	singboxConfig := &Config{}

	// Convert log settings
	// sing-box log format: { "level": "info|warn|error|debug", "timestamp": true, "output": "file_path" }
	// Xray log format: { "loglevel": "...", "access": "...", "error": "..." }
	// sing-box uses single "output" field for all logs, not separate access/error
	if len(xrayConfig.LogConfig) > 0 {
		var xrayLog map[string]interface{}
		if err := json.Unmarshal(xrayConfig.LogConfig, &xrayLog); err == nil {
			singboxLog := make(map[string]interface{})
			// Convert log level
			if level, ok := xrayLog["loglevel"].(string); ok {
				singboxLog["level"] = level
			} else {
				singboxLog["level"] = "warn" // Default
			}
			// sing-box uses single "output" field - prefer error log if available, otherwise access
			if errorLog, ok := xrayLog["error"].(string); ok && errorLog != "" {
				singboxLog["output"] = errorLog
			} else if access, ok := xrayLog["access"].(string); ok && access != "" {
				singboxLog["output"] = access
			}
			// Add timestamp (sing-box default)
			singboxLog["timestamp"] = true
			logJSON, _ := json.Marshal(singboxLog)
			singboxConfig.Log = json_util.RawMessage(logJSON)
		}
	} else {
		// Default sing-box log config if no Xray log config
		defaultLog := map[string]interface{}{
			"level":     "warn",
			"timestamp": true,
		}
		logJSON, _ := json.Marshal(defaultLog)
		singboxConfig.Log = json_util.RawMessage(logJSON)
	}

	// Convert DNS settings
	if len(xrayConfig.DNSConfig) > 0 {
		// sing-box DNS format is different, but we can preserve the structure
		singboxConfig.DNS = xrayConfig.DNSConfig
	}

	// Convert inbounds
	singboxConfig.Inbounds = make([]InboundConfig, 0, len(xrayConfig.InboundConfigs))
	for _, xrayInbound := range xrayConfig.InboundConfigs {
		sbInbound := convertXrayInboundToSingBox(&xrayInbound)
		if sbInbound == nil {
			// Skip unsupported protocols (like "tunnel")
			continue
		}
		singboxConfig.Inbounds = append(singboxConfig.Inbounds, *sbInbound)
	}

	// Convert outbounds
	// Xray uses "freedom" for direct, "blackhole" for block
	// sing-box uses "direct" and "block"
	// Also filter out unsupported outbound types
	if len(xrayConfig.OutboundConfigs) > 0 {
		var outboundArray []map[string]interface{}
		if err := json.Unmarshal(xrayConfig.OutboundConfigs, &outboundArray); err == nil {
			filteredOutbounds := make([]map[string]interface{}, 0, len(outboundArray))
			for _, outboundMap := range outboundArray {
				// Convert protocol names
				if protocol, ok := outboundMap["protocol"].(string); ok {
					switch protocol {
					case "freedom":
						outboundMap["protocol"] = "direct"
						// Remove Xray-specific fields that sing-box doesn't support
						if settings, ok := outboundMap["settings"].(map[string]interface{}); ok {
							delete(settings, "domainStrategy")
							delete(settings, "redirect")
							delete(settings, "noises")
						}
					case "blackhole":
						outboundMap["protocol"] = "block"
						// Remove Xray-specific settings - keep settings empty for block outbound
						outboundMap["settings"] = map[string]interface{}{}
					case "tun", "tunnel":
						// Skip unsupported outbound types
						continue
					}
				}
				// Rename "protocol" to "type" for sing-box
				if protocol, ok := outboundMap["protocol"].(string); ok {
					outboundMap["type"] = protocol
					delete(outboundMap, "protocol")
				}
				
				// Convert domainStrategy from settings to domain_strategy at outbound level
				// sing-box uses domain_strategy at outbound level, not in settings
				if settings, ok := outboundMap["settings"].(map[string]interface{}); ok {
					if domainStrategy, ok := settings["domainStrategy"].(string); ok {
						outboundMap["domain_strategy"] = domainStrategy
						delete(settings, "domainStrategy")
					}
				}
				
				filteredOutbounds = append(filteredOutbounds, outboundMap)
			}
			outboundJSON, _ := json.Marshal(filteredOutbounds)
			singboxConfig.Outbounds = json_util.RawMessage(outboundJSON)
		}
	}

	// Convert routing
	// sing-box doesn't support "domainStrategy" or "domain_strategy" in route
	// Also, sing-box doesn't support "type": "field" in route rules (Xray uses this)
	// Remove these fields if present
	if len(xrayConfig.RouterConfig) > 0 {
		var route map[string]interface{}
		if err := json.Unmarshal(xrayConfig.RouterConfig, &route); err == nil {
			// Remove domainStrategy/domain_strategy as sing-box doesn't support it in route
			if _, ok := route["domainStrategy"]; ok {
				delete(route, "domainStrategy")
			}
			if _, ok := route["domain_strategy"]; ok {
				delete(route, "domain_strategy")
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
						}
						// Convert Xray field names to sing-box field names
						// Xray uses "outboundTag", sing-box uses "outbound"
						if outboundTag, ok := ruleMap["outboundTag"].(string); ok {
							ruleMap["outbound"] = outboundTag
							delete(ruleMap, "outboundTag")
						}
						// Xray uses "inboundTag", sing-box uses "inbound"
						if inboundTag, ok := ruleMap["inboundTag"].([]interface{}); ok {
							ruleMap["inbound"] = inboundTag
							delete(ruleMap, "inboundTag")
						}
						// Xray uses "ip", sing-box uses "geoip" or "ip"
						// If it's "geoip:private", convert to geoip array
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
								} else {
									// geoip not configured, remove this rule (sing-box 1.12.0+ requires geoip database)
									// Skip this rule
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
								// Skip this rule
								continue
							}
						} else if geoipStr, ok := ruleMap["geoip"].(string); ok && geoipStr != "" {
							if !hasGeoipConfig {
								// geoip not configured, remove this rule
								// Skip this rule
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
			
			routeJSON, _ := json.Marshal(route)
			singboxConfig.Route = json_util.RawMessage(routeJSON)
		} else {
			// If unmarshal fails, use as-is (shouldn't happen, but fallback)
			singboxConfig.Route = xrayConfig.RouterConfig
		}
	}

	return singboxConfig, nil
}

// convertXrayInboundToSingBox converts a single Xray inbound to sing-box format.
// Returns nil if the protocol is not supported by sing-box.
func convertXrayInboundToSingBox(xrayInbound *xray.InboundConfig) *InboundConfig {
	// Check if protocol is supported by sing-box
	// According to https://sing-box.sagernet.org/configuration/, supported inbound types:
	// direct, mixed, socks, http, shadowsocks, vmess, trojan, naive, hysteria, shadowtls,
	// tuic, hysteria2, vless, anytls, tun, redirect, tproxy
	// Not supported: tunnel, wireguard (different format)
	supportedProtocols := map[string]bool{
		"direct":      true,
		"mixed":       true,
		"socks":       true,
		"http":        true,
		"shadowsocks": true,
		"vmess":       true,
		"trojan":      true,
		"naive":       true,
		"hysteria":    true,
		"shadowtls":   true,
		"tuic":        true,
		"hysteria2":   true,
		"vless":       true,
		"anytls":      true,
		"tun":         true,
		"redirect":    true,
		"tproxy":      true,
	}
	
	if !supportedProtocols[xrayInbound.Protocol] {
		// Return nil for unsupported protocols (like "tunnel")
		return nil
	}
	
	sbInbound := &InboundConfig{
		Type:       xrayInbound.Protocol,
		Tag:        xrayInbound.Tag,
		ListenPort: xrayInbound.Port,
	}

	// Convert listen address
	if len(xrayInbound.Listen) > 0 {
		sbInbound.Listen = xrayInbound.Listen
	} else {
		sbInbound.Listen = json_util.RawMessage("null")
	}

	// Convert settings based on protocol
	var settings map[string]interface{}
	if err := json.Unmarshal(xrayInbound.Settings, &settings); err == nil {
		switch xrayInbound.Protocol {
		case "vmess", "vless":
			if clients, ok := settings["clients"].([]interface{}); ok {
				users := make([]map[string]interface{}, 0)
				for _, client := range clients {
					if c, ok := client.(map[string]interface{}); ok {
						user := make(map[string]interface{})
						if uuid, ok := c["id"].(string); ok {
							user["uuid"] = uuid
						}
						// sing-box VLESS uses "name" field, not "email"
						// VMESS in sing-box doesn't use email/name field
						if email, ok := c["email"].(string); ok && email != "" {
							if xrayInbound.Protocol == "vless" {
								user["name"] = email // VLESS uses "name"
							}
							// VMESS doesn't use email/name in sing-box, skip it
						}
						if flow, ok := c["flow"].(string); ok && flow != "" {
							user["flow"] = flow
						}
						users = append(users, user)
					}
				}
				if len(users) > 0 {
					usersJSON, _ := json.Marshal(users)
					sbInbound.Users = json_util.RawMessage(usersJSON)
				}
			}
		case "trojan":
			if clients, ok := settings["clients"].([]interface{}); ok && len(clients) > 0 {
				if c, ok := clients[0].(map[string]interface{}); ok {
					if password, ok := c["password"].(string); ok {
						sbInbound.Password = password
					}
				}
			}
		case "shadowsocks":
			if method, ok := settings["method"].(string); ok {
				sbInbound.Method = method
			}
			if password, ok := settings["password"].(string); ok {
				sbInbound.Password = password
			}
		}
	}

	// Convert streamSettings to transport
	if len(xrayInbound.StreamSettings) > 0 {
		var stream map[string]interface{}
		if err := json.Unmarshal(xrayInbound.StreamSettings, &stream); err == nil {
			transport := make(map[string]interface{})
			
			if network, ok := stream["network"].(string); ok && network != "tcp" {
				transport["type"] = network
				switch network {
				case "ws":
					if wsSettings, ok := stream["wsSettings"].(map[string]interface{}); ok {
						transport["path"] = wsSettings["path"]
						if headers, ok := wsSettings["headers"].(map[string]interface{}); ok {
							transport["headers"] = headers
						}
					}
				case "grpc":
					if grpcSettings, ok := stream["grpcSettings"].(map[string]interface{}); ok {
						transport["service_name"] = grpcSettings["serviceName"]
					}
				}
			}

			// Convert TLS/Reality settings
			// In sing-box, TLS and Reality are in separate "tls" field at inbound level, NOT in transport
			// Transport is only for V2Ray transport types (ws, grpc, quic, http, httpupgrade)
			if security, ok := stream["security"].(string); ok {
				if security == "tls" {
					if tlsSettings, ok := stream["tlsSettings"].(map[string]interface{}); ok {
						tls := make(map[string]interface{})
						if certPath, ok := tlsSettings["certificateFile"].(string); ok {
							tls["certificate_path"] = certPath
						}
						if keyPath, ok := tlsSettings["keyFile"].(string); ok {
							tls["key_path"] = keyPath
						}
						if serverName, ok := tlsSettings["serverName"].(string); ok {
							tls["server_name"] = serverName
						}
						if alpn, ok := tlsSettings["alpn"].([]interface{}); ok {
							tls["alpn"] = alpn
						}
						tls["enabled"] = true
						tlsJSON, _ := json.Marshal(tls)
						sbInbound.TLS = json_util.RawMessage(tlsJSON)
					}
				} else if security == "reality" {
					if realitySettings, ok := stream["realitySettings"].(map[string]interface{}); ok {
						// sing-box Reality structure for inbound:
						// - server_name goes at tls level, NOT in tls.reality
						// - tls.reality contains: enabled, handshake, private_key, short_id (array), max_time_difference
						tls := map[string]interface{}{
							"enabled": true,
						}
						
						// server_name goes at tls level (not in reality)
						if dest, ok := realitySettings["dest"].(string); ok {
							tls["server_name"] = dest
						} else if serverNames, ok := realitySettings["serverNames"].([]interface{}); ok && len(serverNames) > 0 {
							// Use the first server name
							if firstServerName, ok := serverNames[0].(string); ok {
								tls["server_name"] = firstServerName
							}
						}
						
						// Reality-specific fields go in tls.reality
						reality := map[string]interface{}{
							"enabled": true,
						}
						
						// handshake is required for Reality inbound
						// Use dest as handshake server if available
						if dest, ok := realitySettings["dest"].(string); ok {
							reality["handshake"] = map[string]interface{}{
								"server": dest,
							}
						} else if serverNames, ok := realitySettings["serverNames"].([]interface{}); ok && len(serverNames) > 0 {
							if firstServerName, ok := serverNames[0].(string); ok {
								reality["handshake"] = map[string]interface{}{
									"server": firstServerName,
								}
							}
						}
						
						if privateKey, ok := realitySettings["privateKey"].(string); ok {
							reality["private_key"] = privateKey
						}
						
						// short_id must be an array for inbound Reality
						if shortIds, ok := realitySettings["shortIds"].([]interface{}); ok && len(shortIds) > 0 {
							// Convert to array of strings
							shortIdArray := make([]string, 0, len(shortIds))
							for _, sid := range shortIds {
								if shortId, ok := sid.(string); ok {
									shortIdArray = append(shortIdArray, shortId)
								}
							}
							if len(shortIdArray) > 0 {
								reality["short_id"] = shortIdArray
							}
						}
						
						// max_time_difference (optional)
						if maxTimeDiff, ok := realitySettings["maxTimeDiff"].(int64); ok && maxTimeDiff > 0 {
							reality["max_time_difference"] = fmt.Sprintf("%dms", maxTimeDiff)
						}
						
						tls["reality"] = reality
						tlsJSON, _ := json.Marshal(tls)
						sbInbound.TLS = json_util.RawMessage(tlsJSON)
					}
				}
			}

			// Only add transport if it has V2Ray transport type (ws, grpc, quic, http, httpupgrade)
			// Transport is NOT used for TLS/Reality - those go in inbound.tls
			_, hasType := transport["type"]
			if hasType {
				// Only add transport if it has a valid V2Ray transport type
				transportJSON, _ := json.Marshal(transport)
				sbInbound.Transport = json_util.RawMessage(transportJSON)
			}
			// If no network type, don't add transport (TCP default)
		}
	}

	// Convert sniffing
	if len(xrayInbound.Sniffing) > 0 {
		var sniff map[string]interface{}
		if err := json.Unmarshal(xrayInbound.Sniffing, &sniff); err == nil {
			if enabled, ok := sniff["enabled"].(bool); ok {
				sbInbound.Sniff = enabled
			}
			if destOverride, ok := sniff["destOverride"].([]interface{}); ok && len(destOverride) > 0 {
				sbInbound.SniffOverrideDestination = true
			}
		}
	}

	return sbInbound
}

// ConvertSingBoxToXray converts a sing-box configuration to Xray format.
// This is a basic conversion that handles the most common cases.
func ConvertSingBoxToXray(singboxConfig *Config) (*xray.Config, error) {
	xrayConfig := &xray.Config{}

	// Convert log settings
	if len(singboxConfig.Log) > 0 {
		var sbLog map[string]interface{}
		if err := json.Unmarshal(singboxConfig.Log, &sbLog); err == nil {
			xrayLog := make(map[string]interface{})
			if level, ok := sbLog["level"].(string); ok {
				xrayLog["loglevel"] = level
			}
			if output, ok := sbLog["output"].(string); ok {
				xrayLog["access"] = output
			}
			if errorLog, ok := sbLog["error"].(string); ok {
				xrayLog["error"] = errorLog
			}
			logJSON, _ := json.Marshal(xrayLog)
			xrayConfig.LogConfig = logJSON
		}
	}

	// Convert DNS (preserve as-is)
	xrayConfig.DNSConfig = singboxConfig.DNS

	// Convert inbounds
	xrayConfig.InboundConfigs = make([]xray.InboundConfig, 0, len(singboxConfig.Inbounds))
	for _, sbInbound := range singboxConfig.Inbounds {
		xrayInbound := convertSingBoxInboundToXray(&sbInbound)
		if xrayInbound != nil {
			xrayConfig.InboundConfigs = append(xrayConfig.InboundConfigs, *xrayInbound)
		}
	}

	// Convert outbounds
	// sing-box uses "domain_strategy" at outbound level, Xray uses "domainStrategy" in settings
	if len(singboxConfig.Outbounds) > 0 {
		var outboundArray []map[string]interface{}
		if err := json.Unmarshal(singboxConfig.Outbounds, &outboundArray); err == nil {
			for _, outboundMap := range outboundArray {
				// Convert domain_strategy from outbound level to domainStrategy in settings
				if domainStrategy, ok := outboundMap["domain_strategy"].(string); ok {
					// Ensure settings exists
					settings, ok := outboundMap["settings"].(map[string]interface{})
					if !ok {
						settings = make(map[string]interface{})
						outboundMap["settings"] = settings
					}
					settings["domainStrategy"] = domainStrategy
					delete(outboundMap, "domain_strategy")
				}
				// Rename "type" back to "protocol" for Xray
				if obType, ok := outboundMap["type"].(string); ok {
					outboundMap["protocol"] = obType
					delete(outboundMap, "type")
				}
			}
			outboundJSON, _ := json.Marshal(outboundArray)
			xrayConfig.OutboundConfigs = json_util.RawMessage(outboundJSON)
		} else {
			// If unmarshal fails, use as-is (shouldn't happen, but fallback)
			xrayConfig.OutboundConfigs = singboxConfig.Outbounds
		}
	}

	// Convert routing
	// sing-box doesn't support "domainStrategy" or "domain_strategy" in route
	// Xray uses "domainStrategy", so we can add it back if needed (but it's optional)
	if len(singboxConfig.Route) > 0 {
		// Just copy route as-is, domainStrategy is optional in Xray
		xrayConfig.RouterConfig = singboxConfig.Route
	}

	return xrayConfig, nil
}

// convertSingBoxInboundToXray converts a single sing-box inbound to Xray format.
func convertSingBoxInboundToXray(sbInbound *InboundConfig) *xray.InboundConfig {
	xrayInbound := &xray.InboundConfig{
		Protocol: sbInbound.Type,
		Tag:      sbInbound.Tag,
		Port:     sbInbound.ListenPort,
	}

	// Convert listen address
	if len(sbInbound.Listen) > 0 {
		xrayInbound.Listen = sbInbound.Listen
	}

	// Convert settings based on protocol
	settings := make(map[string]interface{})
	switch sbInbound.Type {
	case "vmess", "vless":
		if len(sbInbound.Users) > 0 {
			var users []map[string]interface{}
			if err := json.Unmarshal(sbInbound.Users, &users); err == nil {
				clients := make([]map[string]interface{}, 0)
				for _, user := range users {
					client := make(map[string]interface{})
					if uuid, ok := user["uuid"].(string); ok {
						client["id"] = uuid
					}
					// sing-box VLESS uses "name", convert back to "email" for Xray
					if name, ok := user["name"].(string); ok && name != "" {
						client["email"] = name // Convert "name" back to "email" for Xray
					} else if email, ok := user["email"].(string); ok && email != "" {
						// VMESS might use "email" (if supported)
						client["email"] = email
					}
					if flow, ok := user["flow"].(string); ok {
						client["flow"] = flow
					}
					clients = append(clients, client)
				}
				settings["clients"] = clients
			}
		}
	case "trojan":
		if sbInbound.Password != "" {
			settings["clients"] = []map[string]interface{}{
				{"password": sbInbound.Password},
			}
		}
	case "shadowsocks":
		if sbInbound.Method != "" {
			settings["method"] = sbInbound.Method
		}
		if sbInbound.Password != "" {
			settings["password"] = sbInbound.Password
		}
	}
	settingsJSON, _ := json.Marshal(settings)
	xrayInbound.Settings = json_util.RawMessage(settingsJSON)

	// Convert transport and TLS to streamSettings
	// In sing-box, TLS/Reality are in separate "tls" field, transport is only for V2Ray transport types
	stream := make(map[string]interface{})
	
	// Convert transport (V2Ray transport types: ws, grpc, quic, http, httpupgrade)
	if len(sbInbound.Transport) > 0 {
		var transport map[string]interface{}
		if err := json.Unmarshal(sbInbound.Transport, &transport); err == nil {
			if networkType, ok := transport["type"].(string); ok {
				stream["network"] = networkType
				switch networkType {
				case "ws":
					wsSettings := make(map[string]interface{})
					if path, ok := transport["path"].(string); ok {
						wsSettings["path"] = path
					}
					if headers, ok := transport["headers"].(map[string]interface{}); ok {
						wsSettings["headers"] = headers
					}
					stream["wsSettings"] = wsSettings
				case "grpc":
					grpcSettings := make(map[string]interface{})
					if serviceName, ok := transport["service_name"].(string); ok {
						grpcSettings["serviceName"] = serviceName
					}
					stream["grpcSettings"] = grpcSettings
				}
			}
		}
	}

	// Convert TLS/Reality from separate "tls" field
	if len(sbInbound.TLS) > 0 {
		var tlsConfig map[string]interface{}
		if err := json.Unmarshal(sbInbound.TLS, &tlsConfig); err == nil {
			if reality, ok := tlsConfig["reality"].(map[string]interface{}); ok {
				// Reality configuration
				stream["security"] = "reality"
				realitySettings := make(map[string]interface{})
				
				// server_name is at tls level, not in reality
				if serverName, ok := tlsConfig["server_name"].(string); ok {
					realitySettings["dest"] = serverName
					realitySettings["serverNames"] = []string{serverName}
				}
				
				if privateKey, ok := reality["private_key"].(string); ok {
					realitySettings["privateKey"] = privateKey
				}
				
				// short_id is an array for inbound Reality
				if shortIdArray, ok := reality["short_id"].([]interface{}); ok && len(shortIdArray) > 0 {
					// Convert array to Xray format (array of strings)
					shortIds := make([]string, 0, len(shortIdArray))
					for _, sid := range shortIdArray {
						if shortId, ok := sid.(string); ok {
							shortIds = append(shortIds, shortId)
						}
					}
					if len(shortIds) > 0 {
						realitySettings["shortIds"] = shortIds
					}
				} else if shortId, ok := reality["short_id"].(string); ok {
					// Fallback: if it's a string (shouldn't happen, but handle it)
					realitySettings["shortIds"] = []string{shortId}
				}
				
				stream["realitySettings"] = realitySettings
			} else {
				// TLS configuration (without reality)
				stream["security"] = "tls"
				tlsSettings := make(map[string]interface{})
				if certPath, ok := tlsConfig["certificate_path"].(string); ok {
					tlsSettings["certificateFile"] = certPath
				}
				if keyPath, ok := tlsConfig["key_path"].(string); ok {
					tlsSettings["keyFile"] = keyPath
				}
				if serverName, ok := tlsConfig["server_name"].(string); ok {
					tlsSettings["serverName"] = serverName
				}
				if alpn, ok := tlsConfig["alpn"].([]interface{}); ok {
					tlsSettings["alpn"] = alpn
				}
				stream["tlsSettings"] = tlsSettings
			}
		}
	}

	// Only add streamSettings if there's content
	if len(stream) > 0 {
		streamJSON, _ := json.Marshal(stream)
		xrayInbound.StreamSettings = json_util.RawMessage(streamJSON)
	}

	// Convert sniffing
	if sbInbound.Sniff {
		sniffing := make(map[string]interface{})
		sniffing["enabled"] = true
		if sbInbound.SniffOverrideDestination {
			sniffing["destOverride"] = []string{"http", "tls"}
		}
		sniffingJSON, _ := json.Marshal(sniffing)
		xrayInbound.Sniffing = json_util.RawMessage(sniffingJSON)
	}

	return xrayInbound
}
