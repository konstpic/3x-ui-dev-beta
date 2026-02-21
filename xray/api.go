// Package xray provides integration with the Xray proxy core.
// It includes API client functionality, configuration management, traffic monitoring,
// and process control for Xray instances.
package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/konstpic/sharx/v2/logger"
	"github.com/konstpic/sharx/v2/util/common"

	"github.com/xtls/xray-core/app/proxyman/command"
	statsService "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vmess"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// XrayAPI is a gRPC client for managing Xray core configuration, inbounds, outbounds, and statistics.
type XrayAPI struct {
	HandlerServiceClient *command.HandlerServiceClient
	StatsServiceClient   *statsService.StatsServiceClient
	grpcClient           *grpc.ClientConn
	isConnected          bool
}

// Init connects to the Xray API server and initializes handler and stats service clients.
func (x *XrayAPI) Init(apiPort int) error {
	if apiPort <= 0 || apiPort > math.MaxUint16 {
		return fmt.Errorf("invalid Xray API port: %d", apiPort)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", apiPort)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to Xray API: %w", err)
	}

	x.grpcClient = conn
	x.isConnected = true

	hsClient := command.NewHandlerServiceClient(conn)
	ssClient := statsService.NewStatsServiceClient(conn)

	x.HandlerServiceClient = &hsClient
	x.StatsServiceClient = &ssClient

	return nil
}

// IsConnected checks if the gRPC connection is still active.
func (x *XrayAPI) IsConnected() bool {
	return x.isConnected && x.grpcClient != nil
}

// Close closes the gRPC connection and resets the XrayAPI client state.
func (x *XrayAPI) Close() {
	if x.grpcClient != nil {
		x.grpcClient.Close()
	}
	x.HandlerServiceClient = nil
	x.StatsServiceClient = nil
	x.isConnected = false
}

// AddInbound adds a new inbound configuration to the Xray core via gRPC.
func (x *XrayAPI) AddInbound(inbound []byte) error {
	client := *x.HandlerServiceClient

	conf := new(conf.InboundDetourConfig)
	err := json.Unmarshal(inbound, conf)
	if err != nil {
		logger.Debug("Failed to unmarshal inbound:", err)
		return err
	}
	config, err := conf.Build()
	if err != nil {
		logger.Debug("Failed to build inbound Detur:", err)
		return err
	}
	inboundConfig := command.AddInboundRequest{Inbound: config}

	_, err = client.AddInbound(context.Background(), &inboundConfig)

	return err
}

// DelInbound removes an inbound configuration from the Xray core by tag.
func (x *XrayAPI) DelInbound(tag string) error {
	client := *x.HandlerServiceClient
	_, err := client.RemoveInbound(context.Background(), &command.RemoveInboundRequest{
		Tag: tag,
	})
	return err
}

// AddUser adds a user to an inbound in the Xray core using the specified protocol and user data.
func (x *XrayAPI) AddUser(Protocol string, inboundTag string, user map[string]any) error {
	var account *serial.TypedMessage
	switch Protocol {
	case "vmess":
		account = serial.ToTypedMessage(&vmess.Account{
			Id: user["id"].(string),
		})
	case "vless":
		vlessAccount := &vless.Account{
			Id:   user["id"].(string),
			Flow: user["flow"].(string),
		}
		// Add testseed if provided
		if testseedVal, ok := user["testseed"]; ok {
			if testseedArr, ok := testseedVal.([]any); ok && len(testseedArr) >= 4 {
				testseed := make([]uint32, len(testseedArr))
				for i, v := range testseedArr {
					if num, ok := v.(float64); ok {
						testseed[i] = uint32(num)
					}
				}
				vlessAccount.Testseed = testseed
			} else if testseedArr, ok := testseedVal.([]uint32); ok && len(testseedArr) >= 4 {
				vlessAccount.Testseed = testseedArr
			}
		}
		// Add testpre if provided (for outbound, but can be in user for compatibility)
		if testpreVal, ok := user["testpre"]; ok {
			if testpre, ok := testpreVal.(float64); ok && testpre > 0 {
				vlessAccount.Testpre = uint32(testpre)
			} else if testpre, ok := testpreVal.(uint32); ok && testpre > 0 {
				vlessAccount.Testpre = testpre
			}
		}
		account = serial.ToTypedMessage(vlessAccount)
	case "trojan":
		account = serial.ToTypedMessage(&trojan.Account{
			Password: user["password"].(string),
		})
	case "shadowsocks":
		var ssCipherType shadowsocks.CipherType
		switch user["cipher"].(string) {
		case "aes-128-gcm":
			ssCipherType = shadowsocks.CipherType_AES_128_GCM
		case "aes-256-gcm":
			ssCipherType = shadowsocks.CipherType_AES_256_GCM
		case "chacha20-poly1305", "chacha20-ietf-poly1305":
			ssCipherType = shadowsocks.CipherType_CHACHA20_POLY1305
		case "xchacha20-poly1305", "xchacha20-ietf-poly1305":
			ssCipherType = shadowsocks.CipherType_XCHACHA20_POLY1305
		default:
			ssCipherType = shadowsocks.CipherType_NONE
		}

		if ssCipherType != shadowsocks.CipherType_NONE {
			account = serial.ToTypedMessage(&shadowsocks.Account{
				Password:   user["password"].(string),
				CipherType: ssCipherType,
			})
		} else {
			account = serial.ToTypedMessage(&shadowsocks_2022.ServerConfig{
				Key:   user["password"].(string),
				Email: user["email"].(string),
			})
		}
	default:
		return nil
	}

	client := *x.HandlerServiceClient

	_, err := client.AlterInbound(context.Background(), &command.AlterInboundRequest{
		Tag: inboundTag,
		Operation: serial.ToTypedMessage(&command.AddUserOperation{
			User: &protocol.User{
				Email:   user["email"].(string),
				Account: account,
			},
		}),
	})
	return err
}

// RemoveUser removes a user from an inbound in the Xray core by email.
func (x *XrayAPI) RemoveUser(inboundTag, email string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	op := &command.RemoveUserOperation{Email: email}
	req := &command.AlterInboundRequest{
		Tag:       inboundTag,
		Operation: serial.ToTypedMessage(op),
	}

	_, err := (*x.HandlerServiceClient).AlterInbound(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to remove user: %w", err)
	}

	return nil
}

// UpdateConfigFileAfterUserRemoval updates the Xray config file after removing a user via API.
// This ensures the config file stays in sync with the running Xray instance.
// processConfig should be the current config from the Xray process (via process.GetConfig()).
func UpdateConfigFileAfterUserRemoval(processConfig *Config, inboundTag, email string) error {
	if processConfig == nil {
		return fmt.Errorf("process config is nil")
	}

	// Find the inbound by tag
	for i := range processConfig.InboundConfigs {
		if processConfig.InboundConfigs[i].Tag == inboundTag {
			// Parse settings JSON
			var settings map[string]interface{}
			if err := json.Unmarshal(processConfig.InboundConfigs[i].Settings, &settings); err != nil {
				return fmt.Errorf("failed to parse settings: %w", err)
			}

			// Get clients array
			clients, ok := settings["clients"].([]interface{})
			if !ok {
				// Try to handle case where clients might be a different type
				if clientsRaw, ok := settings["clients"]; ok {
					if clientsArray, ok := clientsRaw.([]interface{}); ok {
						clients = clientsArray
					} else {
						return fmt.Errorf("clients is not an array")
					}
				} else {
					return nil // No clients to remove
				}
			}

			// Remove user by email
			found := false
			newClients := make([]interface{}, 0, len(clients))
			for _, client := range clients {
				clientMap, ok := client.(map[string]interface{})
				if !ok {
					continue
				}
				clientEmail, ok := clientMap["email"].(string)
				if !ok {
					continue
				}
				if strings.EqualFold(clientEmail, email) {
					found = true
					continue // Skip this client
				}
				newClients = append(newClients, client)
			}

			if !found {
				logger.Debugf("User %s not found in config for inbound %s (may have been already removed)", email, inboundTag)
				return nil // User not in config, that's OK
			}

			// Update settings with new clients array
			settings["clients"] = newClients
			updatedSettings, err := json.Marshal(settings)
			if err != nil {
				return fmt.Errorf("failed to marshal updated settings: %w", err)
			}

			processConfig.InboundConfigs[i].Settings = updatedSettings

			// Save config to file
			if _, err := WriteConfigFile(processConfig); err != nil {
				return fmt.Errorf("failed to write config file: %w", err)
			}

			logger.Debugf("Config file updated after removing user %s from inbound %s", email, inboundTag)
			return nil
		}
	}

	logger.Debugf("Inbound %s not found in config", inboundTag)
	return nil // Inbound not in config, that's OK
}

// UpdateConfigFileAfterUserAddition updates the Xray config file after adding a user via API.
// This ensures the config file stays in sync with the running Xray instance.
// processConfig should be the current config from the Xray process (via process.GetConfig()).
func UpdateConfigFileAfterUserAddition(processConfig *Config, inboundTag string, user map[string]interface{}) error {
	if processConfig == nil {
		return fmt.Errorf("process config is nil")
	}

	userEmail, ok := user["email"].(string)
	if !ok {
		return fmt.Errorf("user email not found")
	}

	// Find the inbound by tag
	for i := range processConfig.InboundConfigs {
		if processConfig.InboundConfigs[i].Tag == inboundTag {
			// Parse settings JSON
			var settings map[string]interface{}
			if err := json.Unmarshal(processConfig.InboundConfigs[i].Settings, &settings); err != nil {
				return fmt.Errorf("failed to parse settings: %w", err)
			}

			// Get clients array
			clients, ok := settings["clients"].([]interface{})
			if !ok {
				// Initialize clients array if it doesn't exist
				clients = make([]interface{}, 0)
			}

			// Check if user already exists
			for _, client := range clients {
				clientMap, ok := client.(map[string]interface{})
				if !ok {
					continue
				}
				clientEmail, ok := clientMap["email"].(string)
				if !ok {
					continue
				}
				if strings.EqualFold(clientEmail, userEmail) {
					logger.Debugf("User %s already exists in config for inbound %s", userEmail, inboundTag)
					return nil // User already in config, that's OK
				}
			}

			// Add user to clients array
			clients = append(clients, user)

			// Update settings with new clients array
			settings["clients"] = clients
			updatedSettings, err := json.Marshal(settings)
			if err != nil {
				return fmt.Errorf("failed to marshal updated settings: %w", err)
			}

			processConfig.InboundConfigs[i].Settings = updatedSettings

			// Save config to file
			if _, err := WriteConfigFile(processConfig); err != nil {
				return fmt.Errorf("failed to write config file: %w", err)
			}

			logger.Debugf("Config file updated after adding user %s to inbound %s", userEmail, inboundTag)
			return nil
		}
	}

	logger.Debugf("Inbound %s not found in config", inboundTag)
	return nil // Inbound not in config, that's OK
}

// GetTraffic queries traffic statistics from the Xray core, optionally resetting counters.
func (x *XrayAPI) GetTraffic(reset bool) ([]*Traffic, []*ClientTraffic, error) {
	if x.grpcClient == nil {
		return nil, nil, common.NewError("xray api is not initialized")
	}

	trafficRegex := regexp.MustCompile(`(inbound|outbound)>>>([^>]+)>>>traffic>>>(downlink|uplink)`)
	clientTrafficRegex := regexp.MustCompile(`user>>>([^>]+)>>>traffic>>>(downlink|uplink)`)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	if x.StatsServiceClient == nil {
		return nil, nil, common.NewError("xray StatusServiceClient is not initialized")
	}

	resp, err := (*x.StatsServiceClient).QueryStats(ctx, &statsService.QueryStatsRequest{Reset_: reset})
	if err != nil {
		logger.Debug("Failed to query Xray stats:", err)
		return nil, nil, err
	}

	tagTrafficMap := make(map[string]*Traffic)
	emailTrafficMap := make(map[string]*ClientTraffic)

	for _, stat := range resp.GetStat() {
		if matches := trafficRegex.FindStringSubmatch(stat.Name); len(matches) == 4 {
			processTraffic(matches, stat.Value, tagTrafficMap)
		} else if matches := clientTrafficRegex.FindStringSubmatch(stat.Name); len(matches) == 3 {
			processClientTraffic(matches, stat.Value, emailTrafficMap)
		}
	}
	return mapToSlice(tagTrafficMap), mapToSlice(emailTrafficMap), nil
}

// processTraffic aggregates a traffic stat into trafficMap using regex matches and value.
// Note: In Xray API terminology:
// - "downlink" = traffic from client to server → maps to Traffic.Down (from server perspective)
// - "uplink" = traffic from server to client → maps to Traffic.Up (from server perspective)
// For inbounds: downlink is what clients send (server receives), uplink is what server sends (clients receive)
func processTraffic(matches []string, value int64, trafficMap map[string]*Traffic) {
	isInbound := matches[1] == "inbound"
	tag := matches[2]
	isDown := matches[3] == "downlink"

	if tag == "api" {
		return
	}

	traffic, ok := trafficMap[tag]
	if !ok {
		traffic = &Traffic{
			IsInbound:  isInbound,
			IsOutbound: !isInbound,
			Tag:        tag,
		}
		trafficMap[tag] = traffic
	}

	// Direct mapping: downlink → Down, uplink → Up
	if isDown {
		traffic.Down = value   // downlink = traffic from clients to server
	} else {
		traffic.Up = value      // uplink = traffic from server to clients
	}
}

// processClientTraffic updates clientTrafficMap with upload/download values for a client email.
// Note: In Xray API terminology:
// - "downlink" = traffic from client to server → maps to ClientTraffic.Down
// - "uplink" = traffic from server to client → maps to ClientTraffic.Up
// This matches the server perspective and is consistent with processTraffic for inbounds.
func processClientTraffic(matches []string, value int64, clientTrafficMap map[string]*ClientTraffic) {
	email := matches[1]
	isDown := matches[2] == "downlink"

	traffic, ok := clientTrafficMap[email]
	if !ok {
		traffic = &ClientTraffic{Email: email}
		clientTrafficMap[email] = traffic
	}

	// Direct mapping: downlink → Down, uplink → Up (consistent with processTraffic)
	if isDown {
		traffic.Down = value  // downlink = traffic from client to server
	} else {
		traffic.Up = value     // uplink = traffic from server to client
	}
}

// mapToSlice converts a map of pointers to a slice of pointers.
func mapToSlice[T any](m map[string]*T) []*T {
	result := make([]*T, 0, len(m))
	for _, v := range m {
		result = append(result, v)
	}
	return result
}
