// Package model defines the database models and data structures used by the 3x-ui panel.
package model

import (
	"fmt"

	"github.com/mhsanaei/3x-ui/v2/util/json_util"
	"github.com/mhsanaei/3x-ui/v2/xray"
)

// Protocol represents the protocol type for Xray inbounds.
type Protocol string

// Protocol constants for different Xray inbound protocols
const (
	VMESS       Protocol = "vmess"
	VLESS       Protocol = "vless"
	Tunnel      Protocol = "tunnel"
	HTTP        Protocol = "http"
	Trojan      Protocol = "trojan"
	Shadowsocks Protocol = "shadowsocks"
	Mixed       Protocol = "mixed"
	WireGuard   Protocol = "wireguard"
)

// User represents a user account in the 3x-ui panel.
type User struct {
	Id       int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// Inbound represents an Xray inbound configuration with traffic statistics and settings.
type Inbound struct {
	Id                   int                  `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`                                                    // Unique identifier
	UserId               int                  `json:"-"`                                                                                               // Associated user ID
	Up                   int64                `json:"up" form:"up"`                                                                                    // Upload traffic in bytes
	Down                 int64                `json:"down" form:"down"`                                                                                // Download traffic in bytes
	Total                int64                `json:"total" form:"total"`                                                                              // Total traffic limit in bytes
	AllTime              int64                `json:"allTime" form:"allTime" gorm:"default:0"`                                                         // All-time traffic usage
	Remark               string               `json:"remark" form:"remark"`                                                                            // Human-readable remark
	Enable               bool                 `json:"enable" form:"enable" gorm:"index:idx_enable_traffic_reset,priority:1"`                           // Whether the inbound is enabled
	ExpiryTime           int64                `json:"expiryTime" form:"expiryTime"`                                                                    // Expiration timestamp
	TrafficReset         string               `json:"trafficReset" form:"trafficReset" gorm:"default:never;index:idx_enable_traffic_reset,priority:2"` // Traffic reset schedule
	LastTrafficResetTime int64                `json:"lastTrafficResetTime" form:"lastTrafficResetTime" gorm:"default:0"`                               // Last traffic reset timestamp
	ClientStats          []xray.ClientTraffic `gorm:"foreignKey:InboundId;references:Id" json:"clientStats" form:"clientStats"`                        // Client traffic statistics

	// Xray configuration fields
	Listen         string   `json:"listen" form:"listen"`
	Port           int      `json:"port" form:"port"`
	Protocol       Protocol `json:"protocol" form:"protocol"`
	Settings       string   `json:"settings" form:"settings"`
	StreamSettings string   `json:"streamSettings" form:"streamSettings"`
	Tag            string   `json:"tag" form:"tag" gorm:"unique"`
	Sniffing       string   `json:"sniffing" form:"sniffing"`
	NodeId         *int     `json:"nodeId,omitempty" form:"-" gorm:"-"` // Node ID (not stored in Inbound table, from mapping) - DEPRECATED: kept only for backward compatibility with old clients, use NodeIds instead
	NodeIds        []int    `json:"nodeIds,omitempty" form:"-" gorm:"-"` // Node IDs array (not stored in Inbound table, from mapping) - use this for multi-node support
}

// OutboundTraffics tracks traffic statistics for Xray outbound connections.
type OutboundTraffics struct {
	Id    int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Tag   string `json:"tag" form:"tag" gorm:"unique"`
	Up    int64  `json:"up" form:"up" gorm:"default:0"`
	Down  int64  `json:"down" form:"down" gorm:"default:0"`
	Total int64  `json:"total" form:"total" gorm:"default:0"`
}

// InboundClientIps stores IP addresses associated with inbound clients for access control.
type InboundClientIps struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	ClientEmail string `json:"clientEmail" form:"clientEmail" gorm:"unique"`
	Ips         string `json:"ips" form:"ips"`
}

// HistoryOfSeeders tracks which database seeders have been executed to prevent re-running.
type HistoryOfSeeders struct {
	Id         int    `json:"id" gorm:"primaryKey;autoIncrement"`
	SeederName string `json:"seederName"`
}

// GenXrayInboundConfig generates an Xray inbound configuration from the Inbound model.
func (i *Inbound) GenXrayInboundConfig() *xray.InboundConfig {
	listen := i.Listen
	if listen != "" {
		listen = fmt.Sprintf("\"%v\"", listen)
	}
	return &xray.InboundConfig{
		Listen:         json_util.RawMessage(listen),
		Port:           i.Port,
		Protocol:       string(i.Protocol),
		Settings:       json_util.RawMessage(i.Settings),
		StreamSettings: json_util.RawMessage(i.StreamSettings),
		Tag:            i.Tag,
		Sniffing:       json_util.RawMessage(i.Sniffing),
	}
}

// Setting stores key-value configuration settings for the 3x-ui panel.
type Setting struct {
	Id    int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Key   string `json:"key" form:"key"`
	Value string `json:"value" form:"value"`
}

// Client represents a client configuration for Xray inbounds with traffic limits and settings.
// This is a legacy struct used for JSON parsing from inbound Settings.
// For database operations, use ClientEntity instead.
type Client struct {
	ID          string `json:"id"`                           // Unique client identifier
	Security    string `json:"security"`                     // Security method (e.g., "auto", "aes-128-gcm")
	Password    string `json:"password"`                     // Client password
	Flow        string `json:"flow"`                         // Flow control (XTLS)
	Email       string `json:"email"`                        // Client email identifier
	TotalGB     int64  `json:"totalGB" form:"totalGB"`       // Total traffic limit in GB
	ExpiryTime  int64  `json:"expiryTime" form:"expiryTime"` // Expiration timestamp
	Enable      bool   `json:"enable" form:"enable"`         // Whether the client is enabled
	TgID        int64  `json:"tgId" form:"tgId"`             // Telegram user ID for notifications
	SubID       string `json:"subId" form:"subId"`           // Subscription identifier
	Comment     string `json:"comment" form:"comment"`       // Client comment
	Reset       int    `json:"reset" form:"reset"`           // Reset period in days
	HWIDEnabled bool   `json:"hwidEnabled,omitempty" form:"hwidEnabled"` // Whether HWID restriction is enabled
	MaxHWID     int    `json:"maxHwid,omitempty" form:"maxHwid"`           // Maximum number of allowed HWID devices (0 = unlimited)
	CreatedAt   int64  `json:"created_at,omitempty"`         // Creation timestamp
	UpdatedAt   int64  `json:"updated_at,omitempty"`         // Last update timestamp
}

// ClientEntity represents a client as a separate database entity.
// Clients can be assigned to multiple inbounds.
type ClientEntity struct {
	Id         int    `json:"id" gorm:"primaryKey;autoIncrement"` // Unique identifier
	UserId     int    `json:"userId" gorm:"index"`                // Associated user ID
	Email      string `json:"email" form:"email" gorm:"uniqueIndex:idx_user_email"` // Client email identifier (unique per user)
	UUID       string `json:"uuid" form:"uuid"`                    // UUID/ID for VMESS/VLESS
	Security   string `json:"security" form:"security"`          // Security method (e.g., "auto", "aes-128-gcm")
	Password   string `json:"password" form:"password"`           // Client password (for Trojan/Shadowsocks)
	Flow       string `json:"flow" form:"flow"`                  // Flow control (XTLS)
	TotalGB    float64 `json:"totalGB" form:"totalGB"`            // Total traffic limit in GB (supports decimal values like 0.01 for MB)
	ExpiryTime int64  `json:"expiryTime" form:"expiryTime"`     // Expiration timestamp
	Enable     bool   `json:"enable" form:"enable"`             // Whether the client is enabled
	Status     string `json:"status" form:"status" gorm:"default:active"` // Client status: active, expired_traffic, expired_time
	TgID       int64  `json:"tgId" form:"tgId"`                  // Telegram user ID for notifications
	SubID      string `json:"subId" form:"subId" gorm:"index"`   // Subscription identifier
	Comment    string `json:"comment" form:"comment"`            // Client comment
	Reset      int    `json:"reset" form:"reset"`                // Reset period in days
	CreatedAt  int64  `json:"createdAt" gorm:"autoCreateTime"` // Creation timestamp
	UpdatedAt  int64  `json:"updatedAt" gorm:"autoUpdateTime"`   // Last update timestamp
	
	// Relations (not stored in DB, loaded via joins)
	InboundIds []int `json:"inboundIds,omitempty" form:"-" gorm:"-"` // Inbound IDs this client is assigned to
	
	// Group assignment
	GroupId *int `json:"groupId,omitempty" form:"groupId" gorm:"column:group_id;index"` // Group ID (nullable, client can belong to one group)
	
	// Traffic statistics (stored directly in ClientEntity table)
	Up         int64 `json:"up,omitempty" form:"-" gorm:"default:0"`         // Upload traffic in bytes
	Down       int64 `json:"down,omitempty" form:"-" gorm:"default:0"`       // Download traffic in bytes
	AllTime    int64 `json:"allTime,omitempty" form:"-" gorm:"default:0"`    // All-time traffic usage
	
	// Speed statistics (calculated on backend, not stored in DB)
	UpSpeed    int64 `json:"upSpeed,omitempty" form:"-" gorm:"-"`            // Upload speed in bits per second (calculated)
	DownSpeed  int64 `json:"downSpeed,omitempty" form:"-" gorm:"-"`          // Download speed in bits per second (calculated)
	LastOnline int64 `json:"lastOnline,omitempty" form:"-" gorm:"default:0"` // Last online timestamp
	
	// HWID (Hardware ID) restrictions
	HWIDEnabled bool `json:"hwidEnabled" form:"hwidEnabled" gorm:"column:hwid_enabled;default:false"` // Whether HWID restriction is enabled for this client
	MaxHWID     int  `json:"maxHwid" form:"maxHwid" gorm:"column:max_hwid;default:1"`             // Maximum number of allowed HWID devices (0 = unlimited)
	HWIDs       []*ClientHWID `json:"hwids,omitempty" form:"-" gorm:"-"`          // Registered HWIDs for this client (loaded from client_hwids table, not stored in ClientEntity table)
	
	// Subscription customization
	Announce string `json:"announce,omitempty" form:"announce" gorm:"column:announce"` // Custom announcement text for this client (overrides subscription header, max 200 chars, supports base64)
}

// Node represents a worker node in multi-node architecture.
type Node struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"` // Unique identifier
	Name        string `json:"name" form:"name"`                   // Node name/identifier
	Address     string `json:"address" form:"address"`             // Node API address (e.g., "http://192.168.1.100:8080" or "https://...")
	ApiKey      string `json:"apiKey" form:"apiKey"`               // API key for authentication
	Status       string `json:"status" gorm:"default:unknown"`     // Status: online, offline, unknown
	LastCheck    int64  `json:"lastCheck" gorm:"default:0"`        // Last health check timestamp
	ResponseTime int64  `json:"responseTime" gorm:"default:0"`     // Response time in milliseconds (0 = not measured or error)
	UseTLS       bool   `json:"useTls" form:"useTls" gorm:"column:use_tls;default:false"` // Whether to use TLS/HTTPS for API calls
	CertPath    string `json:"certPath" form:"certPath" gorm:"column:cert_path"`       // Path to certificate file (optional, for custom CA)
	KeyPath     string `json:"keyPath" form:"keyPath" gorm:"column:key_path"`          // Path to private key file (optional, for custom CA)
	InsecureTLS bool   `json:"insecureTls" form:"insecureTls" gorm:"column:insecure_tls;default:false"` // Skip certificate verification (not recommended)
	CreatedAt    int64  `json:"createdAt" gorm:"autoCreateTime"`  // Creation timestamp
	UpdatedAt   int64  `json:"updatedAt" gorm:"autoUpdateTime"`   // Last update timestamp
	
	// Traffic statistics
	Up           int64   `json:"up" gorm:"default:0"`                    // Upload traffic in bytes
	Down         int64   `json:"down" gorm:"default:0"`                  // Download traffic in bytes
	AllTime      int64   `json:"allTime" gorm:"default:0"`              // All-time traffic usage in bytes
	TrafficLimitGB float64 `json:"trafficLimitGB" form:"trafficLimitGB" gorm:"column:traffic_limit_gb;default:0"` // Traffic limit in GB (0 = unlimited)
}

// InboundNodeMapping maps inbounds to nodes in multi-node mode.
type InboundNodeMapping struct {
	Id        int `json:"id" gorm:"primaryKey;autoIncrement"` // Unique identifier
	InboundId int `json:"inboundId" form:"inboundId" gorm:"uniqueIndex:idx_inbound_node"` // Inbound ID
	NodeId    int `json:"nodeId" form:"nodeId" gorm:"uniqueIndex:idx_inbound_node"`        // Node ID
}

// Outbound represents an Xray outbound configuration.
// Outbounds can be assigned to specific nodes in multi-node mode.
type Outbound struct {
	Id          int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement"` // Unique identifier
	UserId      int    `json:"userId" gorm:"index"`                           // Associated user ID
	Remark      string `json:"remark" form:"remark"`                          // Human-readable remark
	Enable      bool   `json:"enable" form:"enable" gorm:"default:true"`     // Whether the outbound is enabled
	Protocol    string `json:"protocol" form:"protocol"`                      // Outbound protocol (freedom, blackhole, socks, http, vmess, vless, trojan, shadowsocks, wireguard, etc.)
	Settings    string `json:"settings" form:"settings"`                      // Protocol-specific settings (JSON)
	StreamSettings string `json:"streamSettings" form:"streamSettings"`       // Stream settings (JSON, optional)
	Tag         string `json:"tag" form:"tag" gorm:"unique"`                 // Outbound tag (must be unique)
	ProxySettings string `json:"proxySettings" form:"proxySettings"`           // Proxy settings for chaining (JSON, optional)
	SendThrough  string `json:"sendThrough" form:"sendThrough"`              // Send through address (optional)
	Mux          string `json:"mux" form:"mux"`                               // Mux settings (JSON, optional)
	CreatedAt   int64  `json:"createdAt" gorm:"autoCreateTime"`               // Creation timestamp
	UpdatedAt   int64  `json:"updatedAt" gorm:"autoUpdateTime"`                // Last update timestamp
	
	// Relations (not stored in DB, loaded via queries)
	NodeIds []int `json:"nodeIds,omitempty" form:"-" gorm:"-"` // Node IDs array (not stored in Outbound table, from mapping) - use this for multi-node support
	
	// Core config profile relation
	CoreConfigProfileId *int `json:"coreConfigProfileId,omitempty" form:"coreConfigProfileId" gorm:"index"` // Xray core config profile ID (optional)
}

// OutboundNodeMapping maps outbounds to nodes in multi-node mode.
type OutboundNodeMapping struct {
	Id         int `json:"id" gorm:"primaryKey;autoIncrement"` // Unique identifier
	OutboundId int `json:"outboundId" form:"outboundId" gorm:"uniqueIndex:idx_outbound_node"` // Outbound ID
	NodeId     int `json:"nodeId" form:"nodeId" gorm:"uniqueIndex:idx_outbound_node"`         // Node ID
}

// XrayCoreConfigProfile represents an Xray core configuration profile for multi-node mode.
// Each profile contains a complete Xray configuration (routing, dns, log, policy, stats, inbounds, outbounds)
// that can be assigned to nodes.
type XrayCoreConfigProfile struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`                    // Unique identifier
	UserId      int    `json:"userId" form:"userId" gorm:"index"`                    // Associated user ID
	Name        string `json:"name" form:"name"`                                     // Profile name
	Description string `json:"description" form:"description"`                       // Profile description
	ConfigJson  string `json:"configJson" form:"configJson" gorm:"type:text"`        // Full Xray JSON config
	IsDefault   bool   `json:"isDefault" form:"isDefault" gorm:"default:false"`     // Whether this is the default profile
	CreatedAt   int64  `json:"createdAt" gorm:"autoCreateTime"`                      // Creation timestamp
	UpdatedAt   int64  `json:"updatedAt" gorm:"autoUpdateTime"`                      // Last update timestamp
	
	// Relations (not stored in DB, loaded via queries)
	NodeIds []int `json:"nodeIds,omitempty" form:"-" gorm:"-"` // Node IDs array (not stored in Profile table, from mapping) - use this for multi-node support
}

// ProfileNodeMapping maps profiles to nodes in multi-node mode.
type ProfileNodeMapping struct {
	Id        int `json:"id" gorm:"primaryKey;autoIncrement"` // Unique identifier
	ProfileId int `json:"profileId" form:"profileId" gorm:"uniqueIndex:idx_profile_node"` // Profile ID
	NodeId    int `json:"nodeId" form:"nodeId" gorm:"uniqueIndex:idx_profile_node"`     // Node ID
}

// ClientInboundMapping maps clients to inbounds (many-to-many relationship).
type ClientInboundMapping struct {
	Id        int `json:"id" gorm:"primaryKey;autoIncrement"` // Unique identifier
	ClientId  int `json:"clientId" form:"clientId" gorm:"uniqueIndex:idx_client_inbound"` // Client ID
	InboundId int `json:"inboundId" form:"inboundId" gorm:"uniqueIndex:idx_client_inbound"` // Inbound ID
}

// Host represents a proxy/balancer host configuration for multi-node mode.
// Hosts can override the node address when generating subscription links.
type Host struct {
	Id         int    `json:"id" gorm:"primaryKey;autoIncrement"` // Unique identifier
	UserId     int    `json:"userId" gorm:"index"`                  // Associated user ID
	Name       string `json:"name" form:"name"`                     // Host name/identifier
	Address    string `json:"address" form:"address"`              // Host address (IP or domain)
	Port       int    `json:"port" form:"port"`                     // Host port (0 means use inbound port)
	Protocol   string `json:"protocol" form:"protocol"`             // Protocol override (optional)
	Remark     string `json:"remark" form:"remark"`                 // Host remark/description
	Enable     bool   `json:"enable" form:"enable"`                 // Whether the host is enabled
	CreatedAt  int64  `json:"createdAt" gorm:"autoCreateTime"`      // Creation timestamp
	UpdatedAt  int64  `json:"updatedAt" gorm:"autoUpdateTime"`       // Last update timestamp
	
	// Relations (not stored in DB, loaded via joins)
	InboundIds []int `json:"inboundIds,omitempty" form:"-" gorm:"-"` // Inbound IDs this host applies to
}

// HostInboundMapping maps hosts to inbounds (many-to-many relationship).
type HostInboundMapping struct {
	Id        int `json:"id" gorm:"primaryKey;autoIncrement"` // Unique identifier
	HostId    int `json:"hostId" form:"hostId" gorm:"uniqueIndex:idx_host_inbound"` // Host ID
	InboundId int `json:"inboundId" form:"inboundId" gorm:"uniqueIndex:idx_host_inbound"` // Inbound ID
}

// ClientHWID represents a hardware ID (HWID) associated with a client.
// HWID is provided explicitly by client applications via HTTP headers (x-hwid).
// Server MUST NOT generate or derive HWID from IP, User-Agent, or access logs.
type ClientHWID struct {
	// TableName specifies the table name for GORM
	// GORM by default would use "client_hwids" but the actual table is "client_hw_ids"
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`                    // Unique identifier
	ClientId    int    `json:"clientId" form:"clientId" gorm:"column:client_id;index:idx_client_hwid"` // Client ID
	HWID        string `json:"hwid" form:"hwid" gorm:"column:hwid;index:idx_client_hwid"`          // Hardware ID (unique per client, provided by client via x-hwid header)
	DeviceName  string `json:"deviceName" form:"deviceName" gorm:"column:device_name"`                          // Optional device name/description (deprecated, use DeviceModel instead)
	DeviceOS    string `json:"deviceOs" form:"deviceOs" gorm:"column:device_os"`                             // Device operating system (from x-device-os header)
	DeviceModel string `json:"deviceModel" form:"deviceModel" gorm:"column:device_model"`                       // Device model (from x-device-model header)
	OSVersion   string `json:"osVersion" form:"osVersion" gorm:"column:os_version"`                           // OS version (from x-ver-os header)
	FirstSeenAt int64  `json:"firstSeenAt" gorm:"column:first_seen_at"`                    // First time this HWID was seen (timestamp)
	LastSeenAt  int64  `json:"lastSeenAt" gorm:"column:last_seen_at"`                     // Last time this HWID was used (timestamp)
	FirstSeenIP string `json:"firstSeenIp" form:"firstSeenIp" gorm:"column:first_seen_ip"`                       // IP address when first seen
	IsActive    bool   `json:"isActive" form:"isActive" gorm:"column:is_active;default:true"`          // Whether this HWID is currently active
	IPAddress   string `json:"ipAddress" form:"ipAddress" gorm:"column:ip_address"`                             // Last known IP address for this HWID
	UserAgent   string `json:"userAgent" form:"userAgent" gorm:"column:user_agent"`                            // User agent or client identifier (if available)
	BlockedAt   *int64 `json:"blockedAt,omitempty" form:"blockedAt" gorm:"column:blocked_at"`                  // Timestamp when HWID was blocked (null if not blocked)
	BlockReason string `json:"blockReason,omitempty" form:"blockReason" gorm:"column:block_reason"`              // Reason for blocking (e.g., "HWID limit exceeded")
	
	// Legacy fields (deprecated, kept for backward compatibility)
	FirstSeen   int64  `json:"firstSeen,omitempty" gorm:"-"`                          // Deprecated: use FirstSeenAt
	LastSeen    int64  `json:"lastSeen,omitempty" gorm:"-"`                           // Deprecated: use LastSeenAt
}

// TableName specifies the table name for ClientHWID.
// GORM by default would use "client_hwids" but the actual table is "client_hw_ids"
func (ClientHWID) TableName() string {
	return "client_hw_ids"
}

// ClientGroup represents a group of clients for organization and bulk operations.
type ClientGroup struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"` // Unique identifier
	UserId      int    `json:"userId" gorm:"index"`                // Associated user ID
	Name        string `json:"name" form:"name"`                    // Group name
	Description string `json:"description" form:"description"`     // Group description
	CreatedAt   int64  `json:"createdAt" gorm:"autoCreateTime"`    // Creation timestamp
	UpdatedAt   int64  `json:"updatedAt" gorm:"autoUpdateTime"`    // Last update timestamp
	
	// Relations (not stored in DB, loaded via queries)
	ClientCount int `json:"clientCount,omitempty" form:"-" gorm:"-"` // Number of clients in this group (computed)
}