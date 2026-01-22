package service

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/singbox"
	"github.com/mhsanaei/3x-ui/v2/util/common"
	"github.com/mhsanaei/3x-ui/v2/util/json_util"
	"github.com/mhsanaei/3x-ui/v2/util/random"
	"github.com/mhsanaei/3x-ui/v2/util/reflect_util"
	"github.com/mhsanaei/3x-ui/v2/web/cache"
	"github.com/mhsanaei/3x-ui/v2/web/entity"
	"github.com/mhsanaei/3x-ui/v2/xray"
)

//go:embed config.json
var defaultXrayTemplateConfig string

//go:embed singbox_config.json
var defaultSingBoxTemplateConfig string

var defaultValueMap = map[string]string{
	// Default Xray template configuration. At runtime, the real source of truth
	// is always the "xrayTemplateConfig" record in the settings table; this
	// value is only used as an initial/default template when there is no valid
	// value in the database.
	"xrayTemplateConfig":          defaultXrayTemplateConfig,
	"singboxTemplateConfig":       defaultSingBoxTemplateConfig,
	"webListen":                   "",
	"webDomain":                   "",
	"webPort":                     "2053",
	"webCertFile":                 "",
	"webKeyFile":                  "",
	"secret":                      random.Seq(32),
	"webBasePath":                 "/",
	"sessionMaxAge":               "360",
	"pageSize":                    "25",
	"expireDiff":                  "0",
	"trafficDiff":                 "0",
	"remarkModel":                 "-ieo",
	"timeLocation":                "Local",
	"tgBotEnable":                 "false",
	"tgBotToken":                  "",
	"tgBotProxy":                  "",
	"tgBotAPIServer":              "",
	"tgBotChatId":                 "",
	"tgRunTime":                   "@daily",
	"tgBotBackup":                 "false",
	"tgBotLoginNotify":            "true",
	"tgCpu":                       "80",
	"tgLang":                      "en-US",
	"twoFactorEnable":             "false",
	"twoFactorToken":              "",
	"subEnable":                   "true",
	"subJsonEnable":               "false",
	"subTitle":                    "",
	"subListen":                   "",
	"subPort":                     "2096",
	"subPath":                     "/sub/",
	"subDomain":                   "",
	"subCertFile":                 "",
	"subKeyFile":                  "",
	"subUpdates":                  "12",
	"subEncrypt":                  "true",
	"subShowInfo":                 "true",
	"subURI":                      "",
	"subJsonPath":                 "/json/",
	"subJsonURI":                  "",
	"subJsonFragment":             "",
	"subJsonNoises":               "",
	"subJsonMux":                  "",
	"subJsonRules":                "",
	"datepicker":                  "gregorian",
	"warp":                        "",
	"externalTrafficInformEnable": "false",
	"externalTrafficInformURI":    "",
	// LDAP defaults
	"ldapEnable":            "false",
	"ldapHost":              "",
	"ldapPort":              "389",
	"ldapUseTLS":            "false",
	"ldapBindDN":            "",
	"ldapPassword":          "",
	"ldapBaseDN":            "",
	"ldapUserFilter":        "(objectClass=person)",
	"ldapUserAttr":          "mail",
	"ldapVlessField":        "vless_enabled",
	"ldapSyncCron":          "@every 1m",
	"ldapFlagField":         "",
	"ldapTruthyValues":      "true,1,yes,on",
	"ldapInvertFlag":        "false",
	"ldapInboundTags":       "",
	"ldapAutoCreate":        "false",
	"ldapAutoDelete":        "false",
	"ldapDefaultTotalGB":    "0",
	"ldapDefaultExpiryDays": "0",
	"ldapDefaultLimitIP":    "0",
	// Multi-node mode
	"multiNodeMode": "false", // "true" for multi-mode, "false" for single-mode
	// HWID tracking mode
	"hwidMode": "client_header", // "off" = disabled, "client_header" = use x-hwid header (default), "legacy_fingerprint" = deprecated fingerprint-based (deprecated)
	// Core type selection
	"coreType": "xray", // "xray" or "sing-box"
}

// SettingService provides business logic for application settings management.
// It handles configuration storage, retrieval, and validation for all system settings.
type SettingService struct{}

// EnsureXrayTemplateConfigValid ensures that xrayTemplateConfig in the database is valid.
// If it's missing or invalid, it updates it from the default template.
// This is critical when updating only the panel image without updating the database,
// as the old config structure might be incompatible with the new code.
// All configuration is now stored in database, not in embedded files.
func (s *SettingService) EnsureXrayTemplateConfigValid() error {
	db := database.GetDB()

	current := &model.Setting{}
	err := db.Model(&model.Setting{}).Where("key = ?", "xrayTemplateConfig").First(current).Error
	if database.IsNotFound(err) {
		// No record: initialize from default template
		logger.Infof("xrayTemplateConfig not found in DB, initializing with default template")
		return s.saveSetting("xrayTemplateConfig", defaultXrayTemplateConfig)
	}
	if err != nil {
		return err
	}

	value := strings.TrimSpace(current.Value)
	if value == "" || value == "{}" {
		logger.Warning("xrayTemplateConfig in DB is empty or placeholder, resetting to default template")
		return s.saveSetting("xrayTemplateConfig", defaultXrayTemplateConfig)
	}

	// Validate JSON by unmarshalling into xray.Config; if invalid, reset to default
	cfg := &xray.Config{}
	if err := json.Unmarshal([]byte(value), cfg); err != nil {
		logger.Warningf("Invalid xrayTemplateConfig in DB, resetting to default template: %v", err)
		return s.saveSetting("xrayTemplateConfig", defaultXrayTemplateConfig)
	}

	return nil
}

// EnsureSingBoxTemplateConfigValid ensures that singboxTemplateConfig in the database is valid.
// If it's missing or invalid, it updates it from the default template.
// This is critical when updating only the panel image without updating the database,
// as the old config structure might be incompatible with the new code.
func (s *SettingService) EnsureSingBoxTemplateConfigValid() error {
	db := database.GetDB()

	current := &model.Setting{}
	err := db.Model(&model.Setting{}).Where("key = ?", "singboxTemplateConfig").First(current).Error
	if database.IsNotFound(err) {
		// No record: initialize from default template
		logger.Infof("singboxTemplateConfig not found in DB, initializing with default template")
		return s.saveSetting("singboxTemplateConfig", defaultSingBoxTemplateConfig)
	}
	if err != nil {
		return err
	}

	value := strings.TrimSpace(current.Value)
	if value == "" || value == "{}" {
		logger.Warning("singboxTemplateConfig in DB is empty or placeholder, resetting to default template")
		return s.saveSetting("singboxTemplateConfig", defaultSingBoxTemplateConfig)
	}

	// Validate JSON by unmarshalling into singbox.Config; if invalid, reset to default
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(value), &cfg); err != nil {
		logger.Warningf("Invalid singboxTemplateConfig in DB, resetting to default template: %v", err)
		return s.saveSetting("singboxTemplateConfig", defaultSingBoxTemplateConfig)
	}

	// Clean up invalid fields in log section (sing-box doesn't support "error" field)
	needsUpdate := false
	if logSection, ok := cfg["log"].(map[string]interface{}); ok {
		// Remove "error" field if present (sing-box only uses "output")
		if _, hasError := logSection["error"]; hasError {
			delete(logSection, "error")
			needsUpdate = true
		}
		// Ensure "timestamp" is set
		if _, hasTimestamp := logSection["timestamp"]; !hasTimestamp {
			logSection["timestamp"] = true
			needsUpdate = true
		}
		// Ensure "level" is set
		if _, hasLevel := logSection["level"]; !hasLevel {
			logSection["level"] = "warn"
			needsUpdate = true
		}
		// If we removed "error" but don't have "output", set a default
		if _, hasOutput := logSection["output"]; !hasOutput && needsUpdate {
			logSection["output"] = "sing-box.log"
			needsUpdate = true
		}
	}

	// Clean up unsupported inbounds from template (like tunnel, wireguard)
	// This can happen if the template was converted from Xray config
	// Also clean up invalid transport objects in inbounds
	if inbounds, ok := cfg["inbounds"].([]interface{}); ok {
		filteredInbounds := make([]interface{}, 0, len(inbounds))
		for i, ib := range inbounds {
			if ibMap, ok := ib.(map[string]interface{}); ok {
				ibType, _ := ibMap["type"].(string)
				ibTag, _ := ibMap["tag"].(string)
				if ibType == "tunnel" || ibType == "wireguard" {
					logger.Warningf("EnsureSingBoxTemplateConfigValid: removing unsupported inbound from template: type=%s, tag=%s (index %d)", ibType, ibTag, i)
					needsUpdate = true
					continue
				}
				
				// Clean up invalid transport objects in inbound
				if transport, ok := ibMap["transport"].(map[string]interface{}); ok {
					// Remove reality/tls from transport (should be in tls field)
					if _, hasReality := transport["reality"]; hasReality {
						logger.Warningf("EnsureSingBoxTemplateConfigValid: removing reality from transport in template inbound %s (tag: %s, index %d)", ibType, ibTag, i)
						delete(transport, "reality")
						needsUpdate = true
					}
					if _, hasTLS := transport["tls"]; hasTLS {
						logger.Warningf("EnsureSingBoxTemplateConfigValid: removing tls from transport in template inbound %s (tag: %s, index %d)", ibType, ibTag, i)
						delete(transport, "tls")
						needsUpdate = true
					}
					
					// Check if transport has valid type
					transportType, hasType := transport["type"].(string)
					validTransportTypes := map[string]bool{
						"ws":         true,
						"grpc":       true,
						"quic":       true,
						"http":       true,
						"httpupgrade": true,
					}
					
					// If transport is empty or has invalid type, remove it
					if !hasType || transportType == "" || !validTransportTypes[transportType] {
						if len(transport) == 0 || (!hasType && len(transport) == 0) {
							logger.Warningf("EnsureSingBoxTemplateConfigValid: removing empty/invalid transport from template inbound %s (tag: %s, index %d)", ibType, ibTag, i)
							delete(ibMap, "transport")
							needsUpdate = true
						} else if hasType && !validTransportTypes[transportType] {
							logger.Warningf("EnsureSingBoxTemplateConfigValid: removing invalid transport type '%s' from template inbound %s (tag: %s, index %d)", transportType, ibType, ibTag, i)
							delete(ibMap, "transport")
							needsUpdate = true
						}
					}
				}
				
				// Clean up users: VLESS uses "name" instead of "email" in sing-box
				if users, ok := ibMap["users"].([]interface{}); ok {
					for _, user := range users {
						if userMap, ok := user.(map[string]interface{}); ok {
							// Convert "email" to "name" for VLESS
							if ibType == "vless" {
								if email, ok := userMap["email"].(string); ok && email != "" {
									userMap["name"] = email
									delete(userMap, "email")
									needsUpdate = true
								}
							}
							// Remove "email" for VMESS (not used in sing-box)
							if ibType == "vmess" {
								if _, ok := userMap["email"]; ok {
									delete(userMap, "email")
									needsUpdate = true
								}
							}
						}
					}
				}
				
				// Clean up Reality fields: server_name goes at tls level, short_id must be array
				if tls, ok := ibMap["tls"].(map[string]interface{}); ok {
					if reality, ok := tls["reality"].(map[string]interface{}); ok {
						// server_name should be at tls level, not in reality
						if serverName, ok := reality["server_name"].(string); ok {
							// Move server_name to tls level
							tls["server_name"] = serverName
							delete(reality, "server_name")
							needsUpdate = true
							logger.Warningf("EnsureSingBoxTemplateConfigValid: moved server_name from reality to tls level in template inbound %s (tag: %s, index %d)", ibType, ibTag, i)
						}
						// Remove server_names from reality (should be at tls level)
						if serverNames, ok := reality["server_names"].([]interface{}); ok && len(serverNames) > 0 {
							// Move first server name to tls level
							if firstServerName, ok := serverNames[0].(string); ok {
								tls["server_name"] = firstServerName
							}
							delete(reality, "server_names")
							needsUpdate = true
							logger.Warningf("EnsureSingBoxTemplateConfigValid: moved server_names to server_name at tls level in template inbound %s (tag: %s, index %d)", ibType, ibTag, i)
						}
						// Ensure short_id is an array (required for inbound Reality)
						if shortId, ok := reality["short_id"].(string); ok {
							// Convert string to array
							reality["short_id"] = []string{shortId}
							needsUpdate = true
							logger.Warningf("EnsureSingBoxTemplateConfigValid: converted short_id from string to array in template inbound %s (tag: %s, index %d)", ibType, ibTag, i)
						}
						// Ensure handshake exists (required for Reality inbound)
						if _, hasHandshake := reality["handshake"]; !hasHandshake {
							// Add default handshake if server_name is available
							if serverName, ok := tls["server_name"].(string); ok && serverName != "" {
								reality["handshake"] = map[string]interface{}{
									"server": serverName,
								}
								needsUpdate = true
								logger.Warningf("EnsureSingBoxTemplateConfigValid: added handshake to reality in template inbound %s (tag: %s, index %d)", ibType, ibTag, i)
							}
						}
					}
				}
				
				filteredInbounds = append(filteredInbounds, ib)
			} else {
				filteredInbounds = append(filteredInbounds, ib)
			}
		}
		if needsUpdate {
			cfg["inbounds"] = filteredInbounds
		}
	}

	// Clean up unsupported outbounds from template
	// Xray uses "freedom" (direct), "blackhole" (block), and may have "tun"/"tunnel"
	// sing-box uses "direct", "block", and doesn't support "tun"/"tunnel" outbounds
	if outbounds, ok := cfg["outbounds"].([]interface{}); ok {
		filteredOutbounds := make([]interface{}, 0, len(outbounds))
		for i, ob := range outbounds {
			if obMap, ok := ob.(map[string]interface{}); ok {
				obType, _ := obMap["type"].(string)
				obProtocol, _ := obMap["protocol"].(string) // Xray uses "protocol"
				obTag, _ := obMap["tag"].(string)
				
				// Check both "type" (sing-box) and "protocol" (Xray) fields
				actualType := obType
				if actualType == "" {
					actualType = obProtocol
				}
				
				if actualType == "tun" || actualType == "tunnel" {
					logger.Warningf("EnsureSingBoxTemplateConfigValid: removing unsupported outbound from template: type=%s, tag=%s (index %d)", actualType, obTag, i)
					needsUpdate = true
					continue
				}
				
				// Convert Xray protocol names to sing-box type names
				if obProtocol != "" && obType == "" {
					switch obProtocol {
					case "freedom":
						obMap["type"] = "direct"
						delete(obMap, "protocol")
						// Clean up Xray-specific settings
						if settings, ok := obMap["settings"].(map[string]interface{}); ok {
							delete(settings, "domainStrategy")
							delete(settings, "redirect")
							delete(settings, "noises")
						}
						needsUpdate = true
					case "blackhole":
						obMap["type"] = "block"
						delete(obMap, "protocol")
						// Clean up settings for block
						obMap["settings"] = map[string]interface{}{}
						needsUpdate = true
					}
				}
				
				// Convert domainStrategy from settings to domain_strategy at outbound level
				// sing-box uses domain_strategy at outbound level, not in settings
				if settings, ok := obMap["settings"].(map[string]interface{}); ok {
					if domainStrategy, ok := settings["domainStrategy"].(string); ok {
						obMap["domain_strategy"] = domainStrategy
						delete(settings, "domainStrategy")
						needsUpdate = true
						logger.Warningf("EnsureSingBoxTemplateConfigValid: moved domainStrategy from settings to domain_strategy at outbound level for outbound %s (tag: %s, index %d)", actualType, obTag, i)
					}
				}
				
				filteredOutbounds = append(filteredOutbounds, ob)
			} else {
				filteredOutbounds = append(filteredOutbounds, ob)
			}
		}
		if needsUpdate {
			cfg["outbounds"] = filteredOutbounds
		}
	}

	// Clean up route: remove domainStrategy/domain_strategy and clean up rules
	if route, ok := cfg["route"].(map[string]interface{}); ok {
		if _, ok := route["domainStrategy"]; ok {
			delete(route, "domainStrategy")
			needsUpdate = true
			logger.Warningf("EnsureSingBoxTemplateConfigValid: removed domainStrategy from route (not supported by sing-box)")
		}
		if _, ok := route["domain_strategy"]; ok {
			delete(route, "domain_strategy")
			needsUpdate = true
			logger.Warningf("EnsureSingBoxTemplateConfigValid: removed domain_strategy from route (not supported by sing-box)")
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
						logger.Warningf("EnsureSingBoxTemplateConfigValid: removed type field from route rule (not supported by sing-box)")
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
								logger.Warningf("EnsureSingBoxTemplateConfigValid: removing route rule with geoip (geoip database not configured, required in sing-box 1.12.0+)")
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
							logger.Warningf("EnsureSingBoxTemplateConfigValid: removing route rule with geoip (geoip database not configured, required in sing-box 1.12.0+)")
							needsUpdate = true
							continue
						}
					} else if geoipStr, ok := ruleMap["geoip"].(string); ok && geoipStr != "" {
						if !hasGeoipConfig {
							// geoip not configured, remove this rule
							logger.Warningf("EnsureSingBoxTemplateConfigValid: removing route rule with geoip (geoip database not configured, required in sing-box 1.12.0+)")
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
	}

	if needsUpdate {
		cleanedJSON, err := json.MarshalIndent(cfg, "", "  ")
		if err == nil {
			logger.Infof("Cleaned singboxTemplateConfig: removed invalid fields and unsupported inbounds")
			return s.saveSetting("singboxTemplateConfig", string(cleanedJSON))
		}
	}

	return nil
}

// ResetXrayTemplateConfigToDefault resets the xrayTemplateConfig setting to the
// built-in default template. Intended to be called from admin UI / API.
func (s *SettingService) ResetXrayTemplateConfigToDefault() error {
	logger.Info("Resetting xrayTemplateConfig to default template")
	return s.saveSetting("xrayTemplateConfig", defaultXrayTemplateConfig)
}

func (s *SettingService) GetDefaultJsonConfig() (any, error) {
	var jsonData any
	err := json.Unmarshal([]byte(defaultXrayTemplateConfig), &jsonData)
	if err != nil {
		return nil, err
	}
	return jsonData, nil
}

func (s *SettingService) GetAllSetting() (*entity.AllSetting, error) {
	var allSetting *entity.AllSetting
	
	err := cache.GetOrSet(cache.KeySettingsAll, &allSetting, cache.TTLSettings, func() (interface{}, error) {
		// Cache miss - fetch from database
		db := database.GetDB()
		settings := make([]*model.Setting, 0)
		err := db.Model(model.Setting{}).Not("key = ?", "xrayTemplateConfig").Find(&settings).Error
		if err != nil {
			return nil, err
		}
		result := &entity.AllSetting{}
		t := reflect.TypeOf(result).Elem()
		v := reflect.ValueOf(result).Elem()
		fields := reflect_util.GetFields(t)

		setSetting := func(key, value string) (err error) {
			defer func() {
				panicErr := recover()
				if panicErr != nil {
					err = errors.New(fmt.Sprint(panicErr))
				}
			}()

			var found bool
			var field reflect.StructField
			for _, f := range fields {
				if f.Tag.Get("json") == key {
					field = f
					found = true
					break
				}
			}

			if !found {
				// Some settings are automatically generated, no need to return to the front end to modify the user
				return nil
			}

			fieldV := v.FieldByName(field.Name)
			switch t := fieldV.Interface().(type) {
			case int:
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return err
				}
				fieldV.SetInt(n)
			case string:
				fieldV.SetString(value)
			case bool:
				fieldV.SetBool(value == "true")
			default:
				return common.NewErrorf("unknown field %v type %v", key, t)
			}
			return
		}

		keyMap := map[string]bool{}
		for _, setting := range settings {
			err := setSetting(setting.Key, setting.Value)
			if err != nil {
				return nil, err
			}
			keyMap[setting.Key] = true
		}

		for key, value := range defaultValueMap {
			if keyMap[key] {
				continue
			}
			err := setSetting(key, value)
			if err != nil {
				return nil, err
			}
		}

		return result, nil
	})
	
	return allSetting, err
}

func (s *SettingService) ResetSettings() error {
	db := database.GetDB()
	err := db.Where("1 = 1").Delete(model.Setting{}).Error
	if err != nil {
		return err
	}
	return db.Model(model.User{}).
		Where("1 = 1").Error
}

func (s *SettingService) getSetting(key string) (*model.Setting, error) {
	cacheKey := cache.KeySettingPrefix + key
	var setting *model.Setting
	
	err := cache.GetOrSet(cacheKey, &setting, cache.TTLSetting, func() (interface{}, error) {
		// Cache miss - fetch from database
		db := database.GetDB()
		result := &model.Setting{}
		err := db.Model(model.Setting{}).Where("key = ?", key).First(result).Error
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	
	return setting, err
}

func (s *SettingService) saveSetting(key string, value string) error {
	setting, err := s.getSetting(key)
	db := database.GetDB()
	if database.IsNotFound(err) {
		err = db.Create(&model.Setting{
			Key:   key,
			Value: value,
		}).Error
	} else if err != nil {
		return err
	} else {
		setting.Key = key
		setting.Value = value
		err = db.Save(setting).Error
	}
	
	if err == nil {
		// Invalidate cache for this specific setting
		cache.InvalidateSetting(key)
		// Invalidate all settings cache only when a setting is actually changed
		// This ensures consistency while avoiding unnecessary cache misses
		cache.Delete(cache.KeySettingsAll)
		// Also invalidate default settings cache (they depend on individual settings)
		cache.DeletePattern("defaultSettings:*")
		// Invalidate computed settings that depend on this setting
		if key == "multiNodeMode" {
			cache.Delete("computed:ipLimitEnable")
			
			// Check if switching from multi-node to single-node mode
			// Need to clean up duplicate inbounds with same port
			// Get old value before it's updated
			oldSetting, oldErr := s.getSetting("multiNodeMode")
			oldValue := false
			if oldErr == nil && oldSetting != nil {
				oldValue, _ = strconv.ParseBool(oldSetting.Value)
			}
			
			// Get new value
			newValue, err := strconv.ParseBool(value)
			if err == nil && oldValue && !newValue {
				// We're switching from multi-node (true) to single-node (false)
				logger.Infof("Switching from multi-node to single-node mode - cleaning up duplicate inbounds")
				if err := s.cleanupDuplicateInbounds(); err != nil {
					logger.Warningf("Failed to cleanup duplicate inbounds: %v", err)
				}
			}
		}
	}
	
	return err
}

func (s *SettingService) getString(key string) (string, error) {
	setting, err := s.getSetting(key)
	if database.IsNotFound(err) {
		value, ok := defaultValueMap[key]
		if !ok {
			return "", common.NewErrorf("key <%v> not in defaultValueMap", key)
		}
		return value, nil
	} else if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (s *SettingService) setString(key string, value string) error {
	return s.saveSetting(key, value)
}

func (s *SettingService) getBool(key string) (bool, error) {
	str, err := s.getString(key)
	if err != nil {
		return false, err
	}
	// If the string is empty, treat it as missing and use default value
	if str == "" {
		defaultValue, ok := defaultValueMap[key]
		if !ok {
			return false, common.NewErrorf("key <%v> not in defaultValueMap", key)
		}
		return strconv.ParseBool(defaultValue)
	}
	return strconv.ParseBool(str)
}

func (s *SettingService) setBool(key string, value bool) error {
	return s.setString(key, strconv.FormatBool(value))
}

func (s *SettingService) getInt(key string) (int, error) {
	str, err := s.getString(key)
	if err != nil {
		return 0, err
	}
	// If the string is empty, treat it as missing and use default value
	if str == "" {
		defaultValue, ok := defaultValueMap[key]
		if !ok {
			return 0, common.NewErrorf("key <%v> not in defaultValueMap", key)
		}
		return strconv.Atoi(defaultValue)
	}
	return strconv.Atoi(str)
}

func (s *SettingService) setInt(key string, value int) error {
	return s.setString(key, strconv.Itoa(value))
}

func (s *SettingService) GetXrayConfigTemplate() (string, error) {
	return s.getString("xrayTemplateConfig")
}

// GetSingBoxConfigTemplate returns the sing-box template configuration from the database.
func (s *SettingService) GetSingBoxConfigTemplate() (string, error) {
	return s.getString("singboxTemplateConfig")
}

func (s *SettingService) GetListen() (string, error) {
	return s.getString("webListen")
}

func (s *SettingService) SetListen(ip string) error {
	return s.setString("webListen", ip)
}

func (s *SettingService) GetWebDomain() (string, error) {
	// Check environment variable first
	if envValue := os.Getenv("XUI_WEB_DOMAIN"); envValue != "" {
		return envValue, nil
	}
	return s.getString("webDomain")
}

func (s *SettingService) GetTgBotToken() (string, error) {
	return s.getString("tgBotToken")
}

func (s *SettingService) SetTgBotToken(token string) error {
	return s.setString("tgBotToken", token)
}

func (s *SettingService) GetTgBotProxy() (string, error) {
	return s.getString("tgBotProxy")
}

func (s *SettingService) SetTgBotProxy(token string) error {
	return s.setString("tgBotProxy", token)
}

func (s *SettingService) GetTgBotAPIServer() (string, error) {
	return s.getString("tgBotAPIServer")
}

func (s *SettingService) SetTgBotAPIServer(token string) error {
	return s.setString("tgBotAPIServer", token)
}

func (s *SettingService) GetTgBotChatId() (string, error) {
	return s.getString("tgBotChatId")
}

func (s *SettingService) SetTgBotChatId(chatIds string) error {
	return s.setString("tgBotChatId", chatIds)
}

func (s *SettingService) GetTgbotEnabled() (bool, error) {
	return s.getBool("tgBotEnable")
}

func (s *SettingService) SetTgbotEnabled(value bool) error {
	return s.setBool("tgBotEnable", value)
}

func (s *SettingService) GetTgbotRuntime() (string, error) {
	return s.getString("tgRunTime")
}

func (s *SettingService) SetTgbotRuntime(time string) error {
	return s.setString("tgRunTime", time)
}

func (s *SettingService) GetTgBotBackup() (bool, error) {
	return s.getBool("tgBotBackup")
}

func (s *SettingService) GetTgBotLoginNotify() (bool, error) {
	return s.getBool("tgBotLoginNotify")
}

func (s *SettingService) GetTgCpu() (int, error) {
	return s.getInt("tgCpu")
}

func (s *SettingService) GetTgLang() (string, error) {
	return s.getString("tgLang")
}

func (s *SettingService) GetTwoFactorEnable() (bool, error) {
	return s.getBool("twoFactorEnable")
}

func (s *SettingService) SetTwoFactorEnable(value bool) error {
	return s.setBool("twoFactorEnable", value)
}

func (s *SettingService) GetTwoFactorToken() (string, error) {
	return s.getString("twoFactorToken")
}

func (s *SettingService) SetTwoFactorToken(value string) error {
	return s.setString("twoFactorToken", value)
}

func (s *SettingService) GetPort() (int, error) {
	// Check environment variable first
	if envValue := os.Getenv("XUI_WEB_PORT"); envValue != "" {
		port, err := strconv.Atoi(envValue)
		if err != nil {
			return 0, common.NewErrorf("invalid XUI_WEB_PORT value: %v", envValue)
		}
		return port, nil
	}
	return s.getInt("webPort")
}

func (s *SettingService) SetPort(port int) error {
	return s.setInt("webPort", port)
}

func (s *SettingService) SetCertFile(webCertFile string) error {
	return s.setString("webCertFile", webCertFile)
}

func (s *SettingService) GetCertFile() (string, error) {
	// Check environment variable first
	if envValue := os.Getenv("XUI_WEB_CERT_FILE"); envValue != "" {
		return envValue, nil
	}
	return s.getString("webCertFile")
}

func (s *SettingService) SetKeyFile(webKeyFile string) error {
	return s.setString("webKeyFile", webKeyFile)
}

func (s *SettingService) GetKeyFile() (string, error) {
	// Check environment variable first
	if envValue := os.Getenv("XUI_WEB_KEY_FILE"); envValue != "" {
		return envValue, nil
	}
	return s.getString("webKeyFile")
}

func (s *SettingService) GetExpireDiff() (int, error) {
	return s.getInt("expireDiff")
}

func (s *SettingService) GetTrafficDiff() (int, error) {
	return s.getInt("trafficDiff")
}

func (s *SettingService) GetSessionMaxAge() (int, error) {
	return s.getInt("sessionMaxAge")
}

func (s *SettingService) GetRemarkModel() (string, error) {
	return s.getString("remarkModel")
}

func (s *SettingService) GetSecret() ([]byte, error) {
	secret, err := s.getString("secret")
	if secret == defaultValueMap["secret"] {
		err := s.saveSetting("secret", secret)
		if err != nil {
			logger.Warning("save secret failed:", err)
		}
	}
	return []byte(secret), err
}

func (s *SettingService) SetBasePath(basePath string) error {
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	if !strings.HasSuffix(basePath, "/") {
		basePath += "/"
	}
	return s.setString("webBasePath", basePath)
}

func (s *SettingService) GetBasePath() (string, error) {
	basePath, err := s.getString("webBasePath")
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	if !strings.HasSuffix(basePath, "/") {
		basePath += "/"
	}
	return basePath, nil
}

func (s *SettingService) GetTimeLocation() (*time.Location, error) {
	l, err := s.getString("timeLocation")
	if err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(l)
	if err != nil {
		defaultLocation := defaultValueMap["timeLocation"]
		logger.Errorf("location <%v> not exist, using default location: %v", l, defaultLocation)
		return time.LoadLocation(defaultLocation)
	}
	return location, nil
}

func (s *SettingService) GetSubEnable() (bool, error) {
	return s.getBool("subEnable")
}

func (s *SettingService) GetSubJsonEnable() (bool, error) {
	return s.getBool("subJsonEnable")
}

func (s *SettingService) GetSubTitle() (string, error) {
	return s.getString("subTitle")
}

func (s *SettingService) GetSubListen() (string, error) {
	return s.getString("subListen")
}

func (s *SettingService) GetSubPort() (int, error) {
	return s.getInt("subPort")
}

func (s *SettingService) GetSubPath() (string, error) {
	return s.getString("subPath")
}

func (s *SettingService) GetSubJsonPath() (string, error) {
	return s.getString("subJsonPath")
}

func (s *SettingService) GetSubDomain() (string, error) {
	return s.getString("subDomain")
}

func (s *SettingService) SetSubCertFile(subCertFile string) error {
	return s.setString("subCertFile", subCertFile)
}

func (s *SettingService) GetSubCertFile() (string, error) {
	return s.getString("subCertFile")
}

func (s *SettingService) SetSubKeyFile(subKeyFile string) error {
	return s.setString("subKeyFile", subKeyFile)
}

func (s *SettingService) GetSubKeyFile() (string, error) {
	return s.getString("subKeyFile")
}

func (s *SettingService) GetSubUpdates() (string, error) {
	return s.getString("subUpdates")
}

func (s *SettingService) GetSubEncrypt() (bool, error) {
	return s.getBool("subEncrypt")
}

func (s *SettingService) GetSubShowInfo() (bool, error) {
	return s.getBool("subShowInfo")
}

func (s *SettingService) GetPageSize() (int, error) {
	return s.getInt("pageSize")
}

func (s *SettingService) GetSubURI() (string, error) {
	return s.getString("subURI")
}

func (s *SettingService) GetSubJsonURI() (string, error) {
	return s.getString("subJsonURI")
}

func (s *SettingService) GetSubJsonFragment() (string, error) {
	return s.getString("subJsonFragment")
}

func (s *SettingService) GetSubJsonNoises() (string, error) {
	return s.getString("subJsonNoises")
}

func (s *SettingService) GetSubJsonMux() (string, error) {
	return s.getString("subJsonMux")
}

func (s *SettingService) GetSubJsonRules() (string, error) {
	return s.getString("subJsonRules")
}

func (s *SettingService) GetDatepicker() (string, error) {
	return s.getString("datepicker")
}

func (s *SettingService) GetWarp() (string, error) {
	return s.getString("warp")
}

func (s *SettingService) SetWarp(data string) error {
	return s.setString("warp", data)
}

func (s *SettingService) GetExternalTrafficInformEnable() (bool, error) {
	return s.getBool("externalTrafficInformEnable")
}

func (s *SettingService) SetExternalTrafficInformEnable(value bool) error {
	return s.setBool("externalTrafficInformEnable", value)
}

func (s *SettingService) GetExternalTrafficInformURI() (string, error) {
	return s.getString("externalTrafficInformURI")
}

func (s *SettingService) SetExternalTrafficInformURI(InformURI string) error {
	return s.setString("externalTrafficInformURI", InformURI)
}

func (s *SettingService) GetIpLimitEnable() (bool, error) {
	// Cache key for this computed setting
	cacheKey := "computed:ipLimitEnable"
	var result bool
	
	err := cache.GetOrSet(cacheKey, &result, cache.TTLSetting, func() (interface{}, error) {
		// Check if multi-node mode is enabled
		multiMode, err := s.GetMultiNodeMode()
		if err == nil && multiMode {
			// In multi-node mode, IP limiting is handled by nodes
			return false, nil
		}
		
		// Check core type - IP limiting works differently for sing-box
		coreType, err := s.GetCoreType()
		if err != nil {
			coreType = "xray" // Default to xray
		}
		
		if coreType == "sing-box" {
			// For sing-box, check if access log is enabled in config
			// For now, return false as sing-box doesn't use the same log format
			// TODO: Implement sing-box access log path detection if needed
			return false, nil
		}
		
		// For Xray, check access log path from config file
		accessLogPath, err := xray.GetAccessLogPath()
		if err != nil {
			// If config file doesn't exist (e.g., using sing-box), return false
			return false, nil
		}
		return (accessLogPath != "none" && accessLogPath != ""), nil
	})
	
	return result, err
}

// LDAP exported getters
func (s *SettingService) GetLdapEnable() (bool, error) {
	return s.getBool("ldapEnable")
}

func (s *SettingService) GetLdapHost() (string, error) {
	return s.getString("ldapHost")
}

func (s *SettingService) GetLdapPort() (int, error) {
	return s.getInt("ldapPort")
}

func (s *SettingService) GetLdapUseTLS() (bool, error) {
	return s.getBool("ldapUseTLS")
}

func (s *SettingService) GetLdapBindDN() (string, error) {
	return s.getString("ldapBindDN")
}

func (s *SettingService) GetLdapPassword() (string, error) {
	return s.getString("ldapPassword")
}

func (s *SettingService) GetLdapBaseDN() (string, error) {
	return s.getString("ldapBaseDN")
}

func (s *SettingService) GetLdapUserFilter() (string, error) {
	return s.getString("ldapUserFilter")
}

func (s *SettingService) GetLdapUserAttr() (string, error) {
	return s.getString("ldapUserAttr")
}

func (s *SettingService) GetLdapVlessField() (string, error) {
	return s.getString("ldapVlessField")
}

func (s *SettingService) GetLdapSyncCron() (string, error) {
	return s.getString("ldapSyncCron")
}

func (s *SettingService) GetLdapFlagField() (string, error) {
	return s.getString("ldapFlagField")
}

func (s *SettingService) GetLdapTruthyValues() (string, error) {
	return s.getString("ldapTruthyValues")
}

func (s *SettingService) GetLdapInvertFlag() (bool, error) {
	return s.getBool("ldapInvertFlag")
}

func (s *SettingService) GetLdapInboundTags() (string, error) {
	return s.getString("ldapInboundTags")
}

func (s *SettingService) GetLdapAutoCreate() (bool, error) {
	return s.getBool("ldapAutoCreate")
}

func (s *SettingService) GetLdapAutoDelete() (bool, error) {
	return s.getBool("ldapAutoDelete")
}

func (s *SettingService) GetLdapDefaultTotalGB() (int, error) {
	return s.getInt("ldapDefaultTotalGB")
}

func (s *SettingService) GetLdapDefaultExpiryDays() (int, error) {
	return s.getInt("ldapDefaultExpiryDays")
}

func (s *SettingService) GetLdapDefaultLimitIP() (int, error) {
	return s.getInt("ldapDefaultLimitIP")
}

// GetMultiNodeMode returns whether multi-node mode is enabled.
func (s *SettingService) GetMultiNodeMode() (bool, error) {
	return s.getBool("multiNodeMode")
}

// SetMultiNodeMode sets the multi-node mode setting.
func (s *SettingService) SetMultiNodeMode(enabled bool) error {
	return s.setBool("multiNodeMode", enabled)
}

// GetHwidMode returns the HWID tracking mode.
// Returns: "off", "client_header", or "legacy_fingerprint"
func (s *SettingService) GetHwidMode() (string, error) {
	mode, err := s.getString("hwidMode")
	if err != nil {
		return "client_header", err // Default to client_header on error
	}
	// Validate mode
	validModes := map[string]bool{
		"off":                true,
		"client_header":     true,
		"legacy_fingerprint": true,
	}
	if !validModes[mode] {
		// Invalid mode, return default
		return "client_header", nil
	}
	return mode, nil
}

// SetHwidMode sets the HWID tracking mode.
// Valid values: "off", "client_header", "legacy_fingerprint"
func (s *SettingService) SetHwidMode(mode string) error {
	validModes := map[string]bool{
		"off":                true,
		"client_header":     true,
		"legacy_fingerprint": true,
	}
	if !validModes[mode] {
		return common.NewErrorf("invalid hwidMode: %s (must be one of: off, client_header, legacy_fingerprint)", mode)
	}
	return s.setString("hwidMode", mode)
}

func (s *SettingService) UpdateAllSetting(allSetting *entity.AllSetting) error {
	if err := allSetting.CheckValid(); err != nil {
		return err
	}

	// Check if coreType is being changed
	oldCoreType, _ := s.GetCoreType()
	newCoreType := allSetting.CoreType
	coreTypeChanged := oldCoreType != newCoreType

	v := reflect.ValueOf(allSetting).Elem()
	t := reflect.TypeOf(allSetting).Elem()
	fields := reflect_util.GetFields(t)
	errs := make([]error, 0)
	for _, field := range fields {
		key := field.Tag.Get("json")
		fieldV := v.FieldByName(field.Name)
		value := fmt.Sprint(fieldV.Interface())
		err := s.saveSetting(key, value)
		if err != nil {
			errs = append(errs, err)
		}
	}
	
	// If coreType changed, switch cores
	if coreTypeChanged && newCoreType != "" {
		logger.Infof("Core type changed from %s to %s, switching cores...", oldCoreType, newCoreType)
		if err := s.SwitchCoreWithConversion(newCoreType); err != nil {
			logger.Errorf("Failed to switch core after settings update: %v", err)
			errs = append(errs, fmt.Errorf("failed to switch core: %w", err))
		}
	}
	
	return common.Combine(errs...)
}

func (s *SettingService) GetDefaultXrayConfig() (any, error) {
	var jsonData any
	err := json.Unmarshal([]byte(defaultXrayTemplateConfig), &jsonData)
	if err != nil {
		return nil, err
	}
	return jsonData, nil
}

func (s *SettingService) GetDefaultSettings(host string) (any, error) {
	// Cache key includes host to support multi-domain setups
	cacheKey := fmt.Sprintf("defaultSettings:%s", host)
	var result map[string]any
	
	err := cache.GetOrSet(cacheKey, &result, cache.TTLSettings, func() (interface{}, error) {
		// Cache miss - compute default settings
		type settingFunc func() (any, error)
		settings := map[string]settingFunc{
			"expireDiff":    func() (any, error) { return s.GetExpireDiff() },
			"trafficDiff":   func() (any, error) { return s.GetTrafficDiff() },
			"pageSize":      func() (any, error) { return s.GetPageSize() },
			"defaultCert":   func() (any, error) { return s.GetCertFile() },
			"defaultKey":    func() (any, error) { return s.GetKeyFile() },
			"tgBotEnable":   func() (any, error) { return s.GetTgbotEnabled() },
			"subEnable":     func() (any, error) { return s.GetSubEnable() },
			"subJsonEnable": func() (any, error) { return s.GetSubJsonEnable() },
			"subTitle":      func() (any, error) { return s.GetSubTitle() },
			"subURI":        func() (any, error) { return s.GetSubURI() },
			"subJsonURI":    func() (any, error) { return s.GetSubJsonURI() },
			"remarkModel":   func() (any, error) { return s.GetRemarkModel() },
			"datepicker":    func() (any, error) { return s.GetDatepicker() },
			"ipLimitEnable": func() (any, error) { return s.GetIpLimitEnable() },
		}

		res := make(map[string]any)

		for key, fn := range settings {
			value, err := fn()
			if err != nil {
				return nil, err
			}
			res[key] = value
		}
		return res, nil
	})
	
	if err != nil {
		return nil, err
	}

	subEnable := result["subEnable"].(bool)
	subJsonEnable := false
	if v, ok := result["subJsonEnable"]; ok {
		if b, ok2 := v.(bool); ok2 {
			subJsonEnable = b
		}
	}
	if (subEnable && result["subURI"].(string) == "") || (subJsonEnable && result["subJsonURI"].(string) == "") {
		subURI := ""
		subTitle, _ := s.GetSubTitle()
		subPort, _ := s.GetSubPort()
		subPath, _ := s.GetSubPath()
		subJsonPath, _ := s.GetSubJsonPath()
		subDomain, _ := s.GetSubDomain()
		subKeyFile, _ := s.GetSubKeyFile()
		subCertFile, _ := s.GetSubCertFile()
		subTLS := false
		if subKeyFile != "" && subCertFile != "" {
			subTLS = true
		}
		if subDomain == "" {
			subDomain = strings.Split(host, ":")[0]
		}
		if subTLS {
			subURI = "https://"
		} else {
			subURI = "http://"
		}
		if (subPort == 443 && subTLS) || (subPort == 80 && !subTLS) {
			subURI += subDomain
		} else {
			subURI += fmt.Sprintf("%s:%d", subDomain, subPort)
		}
		if subEnable && result["subURI"].(string) == "" {
			result["subURI"] = subURI + subPath
		}
		if result["subTitle"].(string) == "" {
			result["subTitle"] = subTitle
		}
		if subJsonEnable && result["subJsonURI"].(string) == "" {
			result["subJsonURI"] = subURI + subJsonPath
		}
	}

	return result, nil
}

// GetCoreType returns the current core type setting (xray or sing-box).
func (s *SettingService) GetCoreType() (string, error) {
	coreType, err := s.getString("coreType")
	if err != nil {
		return "xray", err // Default to xray if not set
	}
	if coreType != "xray" && coreType != "sing-box" {
		// Invalid value, reset to default
		return "xray", s.SetCoreType("xray")
	}
	return coreType, nil
}

// SetCoreType sets the core type setting.
func (s *SettingService) SetCoreType(coreType string) error {
	if coreType != "xray" && coreType != "sing-box" {
		return fmt.Errorf("invalid core type: %s (must be 'xray' or 'sing-box')", coreType)
	}
	return s.setString("coreType", coreType)
}

// SwitchCoreType switches the core type and returns the previous type.
// This method is intended to be used when switching cores, and the actual
// conversion logic should be handled by the caller (e.g., XrayService or SingBoxService).
func (s *SettingService) SwitchCoreType(newType string) (string, error) {
	oldType, err := s.GetCoreType()
	if err != nil {
		return "", err
	}
	if oldType == newType {
		return oldType, nil // No change needed
	}
	if err := s.SetCoreType(newType); err != nil {
		return oldType, err
	}
	return oldType, nil
}

// SwitchCoreWithConversion switches the core type and performs automatic configuration conversion.
// It stops the current core, converts the configuration, and starts the new core.
func (s *SettingService) SwitchCoreWithConversion(newType string) error {
	oldType, err := s.GetCoreType()
	if err != nil {
		return err
	}
	if oldType == newType {
		return nil // No change needed
	}

	// Get current configuration directly from services (works even if core is not running)
	var currentConfig interface{}
	if oldType == "xray" {
		xrayService := NewXrayService()
		currentConfig, err = xrayService.GetXrayConfig()
		if err != nil {
			return fmt.Errorf("failed to get xray config: %w", err)
		}
	} else if oldType == "sing-box" {
		singboxService := NewSingBoxService()
		currentConfig, err = singboxService.GetSingBoxConfig()
		if err != nil {
			return fmt.Errorf("failed to get sing-box config: %w", err)
		}
	} else {
		return fmt.Errorf("unknown old core type: %s", oldType)
	}

	// Stop both cores to ensure no port conflicts
	// Stop old core first
	oldCore, err := GetCoreService()
	if err == nil && oldCore.IsRunning() {
		logger.Infof("Stopping %s core before switching to %s", oldType, newType)
		if err := oldCore.Stop(); err != nil {
			logger.Warningf("Failed to stop %s core: %v", oldType, err)
			// Continue anyway
		}
	}
	
	// Also stop the new core if it's running (might be leftover from previous switch)
	xrayService := NewXrayService()
	singboxService := NewSingBoxService()
	if newType == "sing-box" {
		// If switching to sing-box, make sure Xray is stopped
		if xrayService.IsXrayRunning() {
			logger.Infof("Stopping Xray core (switching to sing-box)")
			if err := xrayService.StopXray(); err != nil {
				logger.Warningf("Failed to stop Xray core: %v", err)
			}
		}
	} else if newType == "xray" {
		// If switching to Xray, make sure sing-box is stopped
		if singboxService.IsSingBoxRunning() {
			logger.Infof("Stopping sing-box core (switching to Xray)")
			if err := singboxService.StopSingBox(); err != nil {
				logger.Warningf("Failed to stop sing-box core: %v", err)
			}
		}
	}
	
	// Wait for processes to fully stop and ports to be released
	// Check that both cores are stopped before proceeding
	maxWait := 3 * time.Second
	waitInterval := 100 * time.Millisecond
	waited := 0 * time.Millisecond
	for waited < maxWait {
		xrayRunning := xrayService.IsXrayRunning()
		singboxRunning := singboxService.IsSingBoxRunning()
		if !xrayRunning && !singboxRunning {
			logger.Debugf("Both cores stopped after %v", waited)
			break
		}
		time.Sleep(waitInterval)
		waited += waitInterval
	}
	if waited >= maxWait {
		logger.Warningf("Some cores may still be running after %v wait time", maxWait)
	}

	// Convert configuration
	var newConfig interface{}
	if oldType == "xray" && newType == "sing-box" {
		xrayConfig := currentConfig.(*xray.Config)
		newConfig, err = singbox.ConvertXrayToSingBox(xrayConfig)
		if err != nil {
			return fmt.Errorf("failed to convert xray config to sing-box: %w", err)
		}
	} else if oldType == "sing-box" && newType == "xray" {
		singboxConfig := currentConfig.(*singbox.Config)
		newConfig, err = singbox.ConvertSingBoxToXray(singboxConfig)
		if err != nil {
			return fmt.Errorf("failed to convert sing-box config to xray: %w", err)
		}
	} else {
		return fmt.Errorf("unsupported core type conversion: %s -> %s", oldType, newType)
	}

	// Update template config in database
	if newType == "xray" {
		xrayConfig := newConfig.(*xray.Config)
		configJSON, err := json.MarshalIndent(xrayConfig, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal xray config: %w", err)
		}
		if err := s.saveSetting("xrayTemplateConfig", string(configJSON)); err != nil {
			return fmt.Errorf("failed to save xray template config: %w", err)
		}
	} else if newType == "sing-box" {
		singboxConfig := newConfig.(*singbox.Config)
		// Clean up log section to remove any invalid fields before saving
		if len(singboxConfig.Log) > 0 {
			var logSection map[string]interface{}
			if err := json.Unmarshal(singboxConfig.Log, &logSection); err == nil {
				// Remove "error" field if present (sing-box doesn't support it)
				if _, hasError := logSection["error"]; hasError {
					delete(logSection, "error")
					// If we removed "error" but don't have "output", ensure we have one
					if _, hasOutput := logSection["output"]; !hasOutput {
						// Try to use access log path, or set default
						logSection["output"] = "sing-box.log"
					}
					cleanedLogJSON, _ := json.Marshal(logSection)
					singboxConfig.Log = json_util.RawMessage(cleanedLogJSON)
				}
			}
		}
		configJSON, err := json.MarshalIndent(singboxConfig, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal sing-box config: %w", err)
		}
		if err := s.saveSetting("singboxTemplateConfig", string(configJSON)); err != nil {
			return fmt.Errorf("failed to save sing-box template config: %w", err)
		}
	}

	// Switch core type
	if err := s.SetCoreType(newType); err != nil {
		return fmt.Errorf("failed to set core type: %w", err)
	}

	// Get new core service and restart
	newCore, err := GetCoreService()
	if err != nil {
		return fmt.Errorf("failed to get new core service: %w", err)
	}

	// Restart with new core
	if err := newCore.Restart(true); err != nil {
		return fmt.Errorf("failed to restart with new core: %w", err)
	}

	logger.Infof("Successfully switched from %s to %s core", oldType, newType)
	return nil
}

// cleanupDuplicateInbounds removes duplicate inbounds with the same port when switching from multi-node to single-node mode.
// In multi-node mode, multiple inbounds can have the same port (with different SNI), but in single-node mode, ports must be unique.
// This function keeps the first inbound (by ID) for each port and deletes the rest.
func (s *SettingService) cleanupDuplicateInbounds() error {
	inboundService := InboundService{}
	allInbounds, err := inboundService.GetAllInbounds()
	if err != nil {
		return fmt.Errorf("failed to get all inbounds: %w", err)
	}
	
	// Group inbounds by port (and listen address if specified)
	portGroups := make(map[string][]*model.Inbound)
	for _, inbound := range allInbounds {
		// Normalize listen address for grouping
		listen := inbound.Listen
		if listen == "" || listen == "0.0.0.0" || listen == "::" || listen == "::0" {
			listen = ""
		}
		key := fmt.Sprintf("%s:%d", listen, inbound.Port)
		portGroups[key] = append(portGroups[key], inbound)
	}
	
	// Find and delete duplicates (keep first by ID, delete rest)
	var toDelete []int
	for key, inbounds := range portGroups {
		if len(inbounds) > 1 {
			// Sort by ID to keep the first one
			sort.Slice(inbounds, func(i, j int) bool {
				return inbounds[i].Id < inbounds[j].Id
			})
			
			// Keep first, mark rest for deletion
			keepInbound := inbounds[0]
			logger.Infof("Found %d inbounds with same port %s, keeping inbound-%d (ID: %d), will delete %d duplicates", 
				len(inbounds), key, keepInbound.Id, keepInbound.Id, len(inbounds)-1)
			
			for i := 1; i < len(inbounds); i++ {
				toDelete = append(toDelete, inbounds[i].Id)
				logger.Infof("Marking inbound-%d (ID: %d, port: %d) for deletion (duplicate)", 
					inbounds[i].Id, inbounds[i].Id, inbounds[i].Port)
			}
		}
	}
	
	// Delete duplicate inbounds
	if len(toDelete) > 0 {
		logger.Infof("Deleting %d duplicate inbounds", len(toDelete))
		for _, id := range toDelete {
			needRestart, err := inboundService.DelInbound(id)
			if err != nil {
				logger.Warningf("Failed to delete duplicate inbound %d: %v", id, err)
				continue
			}
			if needRestart {
				// Note: We don't restart here, as the caller should handle restart after mode switch
				logger.Debugf("Inbound %d deletion requires restart", id)
			}
		}
		logger.Infof("Successfully deleted %d duplicate inbounds", len(toDelete))
	} else {
		logger.Debugf("No duplicate inbounds found")
	}
	
	// Update tags for remaining inbounds (from ID-based to port-based)
	// This is done automatically when inbounds are accessed, but we can trigger it here
	allInbounds, err = inboundService.GetAllInbounds()
	if err == nil {
		multiMode, _ := s.GetMultiNodeMode()
		for _, inbound := range allInbounds {
			// Regenerate tag based on current mode - use InboundService method
			// We need to access the private method, so we'll update tags directly
			var newTag string
			if multiMode {
				newTag = fmt.Sprintf("inbound-%d", inbound.Id)
			} else {
				if inbound.Listen == "" || inbound.Listen == "0.0.0.0" || inbound.Listen == "::" || inbound.Listen == "::0" {
					newTag = fmt.Sprintf("inbound-%d", inbound.Port)
				} else {
					newTag = fmt.Sprintf("inbound-%s:%d", inbound.Listen, inbound.Port)
				}
			}
			if inbound.Tag != newTag {
				db := database.GetDB()
				if err := db.Model(inbound).Update("tag", newTag).Error; err != nil {
					logger.Warningf("Failed to update tag for inbound %d: %v", inbound.Id, err)
				} else {
					logger.Debugf("Updated tag for inbound %d from %s to %s", inbound.Id, inbound.Tag, newTag)
				}
			}
		}
	}
	
	return nil
}
