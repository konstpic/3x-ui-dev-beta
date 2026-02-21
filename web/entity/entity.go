// Package entity defines data structures and entities used by the web layer of the SharX panel.
package entity

import (
	"crypto/tls"
	"math"
	"net"
	"strings"
	"time"

	"github.com/konstpic/sharx-code/v2/util/common"
)

// Msg represents a standard API response message with success status, message text, and optional data object.
type Msg struct {
	Success bool   `json:"success"` // Indicates if the operation was successful
	Msg     string `json:"msg"`     // Response message text
	Obj     any    `json:"obj"`     // Optional data object
}

// AllSetting contains all configuration settings for the SharX panel including web server, Telegram bot, and subscription settings.
type AllSetting struct {
	// Web server settings
	WebListen     string `json:"webListen" form:"webListen"`         // Web server listen IP address
	WebDomain     string `json:"webDomain" form:"webDomain"`         // Web server domain for domain validation
	WebPort       int    `json:"webPort" form:"webPort"`             // Web server port number
	WebCertFile   string `json:"webCertFile" form:"webCertFile"`     // Path to SSL certificate file for web server
	WebKeyFile    string `json:"webKeyFile" form:"webKeyFile"`       // Path to SSL private key file for web server
	WebBasePath   string `json:"webBasePath" form:"webBasePath"`     // Base path for web panel URLs
	SessionMaxAge int    `json:"sessionMaxAge" form:"sessionMaxAge"` // Session maximum age in minutes

	// UI settings
	PageSize    int    `json:"pageSize" form:"pageSize"`       // Number of items per page in lists
	ExpireDiff  int    `json:"expireDiff" form:"expireDiff"`   // Expiration warning threshold in days
	TrafficDiff int    `json:"trafficDiff" form:"trafficDiff"` // Traffic warning threshold percentage
	RemarkModel string `json:"remarkModel" form:"remarkModel"` // Remark model pattern for inbounds
	Datepicker  string `json:"datepicker" form:"datepicker"`   // Date picker format

	// Telegram bot settings
	TgBotEnable      bool   `json:"tgBotEnable" form:"tgBotEnable"`           // Enable Telegram bot notifications
	TgBotToken       string `json:"tgBotToken" form:"tgBotToken"`             // Telegram bot token
	TgBotProxy       string `json:"tgBotProxy" form:"tgBotProxy"`             // Proxy URL for Telegram bot
	TgBotAPIServer   string `json:"tgBotAPIServer" form:"tgBotAPIServer"`     // Custom API server for Telegram bot
	TgBotChatId      string `json:"tgBotChatId" form:"tgBotChatId"`           // Telegram chat ID for notifications
	TgRunTime        string `json:"tgRunTime" form:"tgRunTime"`               // Cron schedule for Telegram notifications
	TgBotBackup      bool   `json:"tgBotBackup" form:"tgBotBackup"`           // Enable database backup via Telegram
	TgBotLoginNotify bool   `json:"tgBotLoginNotify" form:"tgBotLoginNotify"` // Send login notifications
	TgCpu            int    `json:"tgCpu" form:"tgCpu"`                       // CPU usage threshold for alerts
	TgLang           string `json:"tgLang" form:"tgLang"`                     // Telegram bot language

	// Security settings
	TimeLocation    string `json:"timeLocation" form:"timeLocation"`       // Time zone location
	TwoFactorEnable bool   `json:"twoFactorEnable" form:"twoFactorEnable"` // Enable two-factor authentication
	TwoFactorToken  string `json:"twoFactorToken" form:"twoFactorToken"`   // Two-factor authentication token

	// Subscription server settings
	SubEnable                   bool   `json:"subEnable" form:"subEnable"`                                     // Enable subscription server
	SubJsonEnable               bool   `json:"subJsonEnable" form:"subJsonEnable"`                             // Enable JSON subscription endpoint
	SubTitle                    string `json:"subTitle" form:"subTitle"`                                       // Subscription title
	SubListen                   string `json:"subListen" form:"subListen"`                                     // Subscription server listen IP
	SubPort                     int    `json:"subPort" form:"subPort"`                                         // Subscription server port
	SubPath                     string `json:"subPath" form:"subPath"`                                         // Base path for subscription URLs
	SubDomain                   string `json:"subDomain" form:"subDomain"`                                     // Domain for subscription server validation
	SubCertFile                 string `json:"subCertFile" form:"subCertFile"`                                 // SSL certificate file for subscription server
	SubKeyFile                  string `json:"subKeyFile" form:"subKeyFile"`                                   // SSL private key file for subscription server
	SubUpdates                  int    `json:"subUpdates" form:"subUpdates"`                                   // Subscription update interval in minutes
	ExternalTrafficInformEnable bool   `json:"externalTrafficInformEnable" form:"externalTrafficInformEnable"` // Enable external traffic reporting
	ExternalTrafficInformURI    string `json:"externalTrafficInformURI" form:"externalTrafficInformURI"`       // URI for external traffic reporting
	SubEncrypt                  bool   `json:"subEncrypt" form:"subEncrypt"`                                   // Encrypt subscription responses
	SubShowInfo                 bool   `json:"subShowInfo" form:"subShowInfo"`                                 // Show client information in subscriptions
	SubURI                      string `json:"subURI" form:"subURI"`                                           // Subscription server URI
	SubJsonPath                 string `json:"subJsonPath" form:"subJsonPath"`                                 // Path for JSON subscription endpoint
	SubJsonURI                  string `json:"subJsonURI" form:"subJsonURI"`                                   // JSON subscription server URI
	SubJsonFragment             string `json:"subJsonFragment" form:"subJsonFragment"`                         // JSON subscription fragment configuration
	SubJsonNoises               string `json:"subJsonNoises" form:"subJsonNoises"`                             // JSON subscription noise configuration
	SubJsonMux                  string `json:"subJsonMux" form:"subJsonMux"`                                   // JSON subscription mux configuration
	SubJsonRules                string `json:"subJsonRules" form:"subJsonRules"`                               // JSON subscription rules configuration
	SubEncryptHappV2RayTun      bool   `json:"subEncryptHappV2RayTun" form:"subEncryptHappV2RayTun"`           // Encrypt subscription for Happ and V2RayTun
	SubOnlyHappV2RayTun         bool   `json:"subOnlyHappV2RayTun" form:"subOnlyHappV2RayTun"`                // Only include Happ and V2RayTun compatible links
	SubHideConfigLinks          bool   `json:"subHideConfigLinks" form:"subHideConfigLinks"`                  // Hide configuration links on subscription page
	SubShowOnlyHappV2RayTun     bool   `json:"subShowOnlyHappV2RayTun" form:"subShowOnlyHappV2RayTun"`        // Show only Happ and V2RayTun applications on subscription page
	SubAutoRotateKeys           bool   `json:"subAutoRotateKeys" form:"subAutoRotateKeys"`                     // Automatically rotate client keys before subscription update interval
	SubHeaders                  string `json:"subHeaders" form:"subHeaders"`                                    // JSON string containing subscription headers configuration
	SubProviderID               string `json:"subProviderID" form:"subProviderID"`                             // Provider ID for Happ extended headers (required for new-url, new-domain, etc.)
	SubProviderIDMethod         string `json:"subProviderIDMethod" form:"subProviderIDMethod"`                 // Method to send Provider ID: "url" (query parameter), "header" (HTTP header), "none" (disabled)
	SubPageTheme                string `json:"subPageTheme" form:"subPageTheme"`                                 // Subscription page theme: "rainbow", "coffee", "banana", "sunset"
	SubPageLogoUrl              string `json:"subPageLogoUrl" form:"subPageLogoUrl"`                           // Logo URL for subscription page (32x32 or 64x64)
	SubPageBrandText            string `json:"subPageBrandText" form:"subPageBrandText"`                         // Brand text for subscription page
	SubPageBackgroundUrl        string `json:"subPageBackgroundUrl" form:"subPageBackgroundUrl"`                 // Background image URL for subscription card (overrides theme gradient)

	// LDAP settings
	LdapEnable     bool   `json:"ldapEnable" form:"ldapEnable"`
	LdapHost       string `json:"ldapHost" form:"ldapHost"`
	LdapPort       int    `json:"ldapPort" form:"ldapPort"`
	LdapUseTLS     bool   `json:"ldapUseTLS" form:"ldapUseTLS"`
	LdapBindDN     string `json:"ldapBindDN" form:"ldapBindDN"`
	LdapPassword   string `json:"ldapPassword" form:"ldapPassword"`
	LdapBaseDN     string `json:"ldapBaseDN" form:"ldapBaseDN"`
	LdapUserFilter string `json:"ldapUserFilter" form:"ldapUserFilter"`
	LdapUserAttr   string `json:"ldapUserAttr" form:"ldapUserAttr"` // e.g., mail or uid
	LdapVlessField string `json:"ldapVlessField" form:"ldapVlessField"`
	LdapSyncCron   string `json:"ldapSyncCron" form:"ldapSyncCron"`
	// Generic flag configuration
	LdapFlagField         string `json:"ldapFlagField" form:"ldapFlagField"`
	LdapTruthyValues      string `json:"ldapTruthyValues" form:"ldapTruthyValues"`
	LdapInvertFlag        bool   `json:"ldapInvertFlag" form:"ldapInvertFlag"`
	LdapInboundTags       string `json:"ldapInboundTags" form:"ldapInboundTags"`
	LdapAutoCreate        bool   `json:"ldapAutoCreate" form:"ldapAutoCreate"`
	LdapAutoDelete        bool   `json:"ldapAutoDelete" form:"ldapAutoDelete"`
	LdapDefaultTotalGB    int    `json:"ldapDefaultTotalGB" form:"ldapDefaultTotalGB"`
	LdapDefaultExpiryDays int    `json:"ldapDefaultExpiryDays" form:"ldapDefaultExpiryDays"`
	LdapDefaultLimitIP    int    `json:"ldapDefaultLimitIP" form:"ldapDefaultLimitIP"`
	
	// Multi-node mode setting
	MultiNodeMode bool `json:"multiNodeMode" form:"multiNodeMode"` // Enable multi-node architecture mode
	
	// HWID tracking mode
	// "off" = HWID tracking disabled
	// "client_header" = HWID provided by client via x-hwid header (default, recommended)
	// "legacy_fingerprint" = deprecated fingerprint-based HWID generation (deprecated, for backward compatibility only)
	HwidMode string `json:"hwidMode" form:"hwidMode"` // HWID tracking mode
	
	// Grafana integration settings
	GrafanaLokiUrl            string `json:"grafanaLokiUrl" form:"grafanaLokiUrl"`                         // Loki API URL (e.g., http://localhost:3100/loki/api/v1/push)
	GrafanaVictoriaMetricsUrl string `json:"grafanaVictoriaMetricsUrl" form:"grafanaVictoriaMetricsUrl"` // VictoriaMetrics API URL (e.g., http://localhost:8428/api/v1/import/prometheus)
	GrafanaEnable             bool   `json:"grafanaEnable" form:"grafanaEnable"`                           // Enable Grafana integration (Loki logging and VictoriaMetrics metrics)
	
	// Panel log level setting (overrides XUI_LOG_LEVEL env var)
	// Valid values: "debug", "info", "notice", "warning", "error"
	PanelLogLevel string `json:"panelLogLevel" form:"panelLogLevel"` // Panel log level (default: "info")
	// JSON subscription routing rules
}

// CheckValid validates all settings in the AllSetting struct, checking IP addresses, ports, SSL certificates, and other configuration values.
func (s *AllSetting) CheckValid() error {
	// WebListen is now env-only setting, only validate if set
	if s.WebListen != "" {
		ip := net.ParseIP(s.WebListen)
		if ip == nil {
			return common.NewError("web listen is not valid ip:", s.WebListen)
		}
	}

	if s.SubListen != "" {
		ip := net.ParseIP(s.SubListen)
		if ip == nil {
			return common.NewError("Sub listen is not valid ip:", s.SubListen)
		}
	}

	// WebPort, WebCertFile, WebKeyFile are now env-only settings, skip validation if not set
	if s.WebPort > 0 {
		if s.WebPort > math.MaxUint16 {
			return common.NewError("web port is not a valid port:", s.WebPort)
		}
	}

	// SubPort, SubCertFile, SubKeyFile are now env-only settings, skip validation if not set
	if s.SubPort > 0 {
		if s.SubPort > math.MaxUint16 {
			return common.NewError("Sub port is not a valid port:", s.SubPort)
		}
	}

	// Only validate port conflict if both ports are set
	if s.SubPort > 0 && s.WebPort > 0 {
		if (s.SubPort == s.WebPort) && (s.WebListen == s.SubListen) {
			return common.NewError("Sub and Web could not use same ip:port, ", s.SubListen, ":", s.SubPort, " & ", s.WebListen, ":", s.WebPort)
		}
	}

	// WebCertFile and WebKeyFile are now env-only settings, only validate if both are set
	if s.WebCertFile != "" && s.WebKeyFile != "" {
		_, err := tls.LoadX509KeyPair(s.WebCertFile, s.WebKeyFile)
		if err != nil {
			return common.NewErrorf("cert file <%v> or key file <%v> invalid: %v", s.WebCertFile, s.WebKeyFile, err)
		}
	}

	// SubCertFile and SubKeyFile are now env-only settings, only validate if both are set
	if s.SubCertFile != "" && s.SubKeyFile != "" {
		_, err := tls.LoadX509KeyPair(s.SubCertFile, s.SubKeyFile)
		if err != nil {
			return common.NewErrorf("cert file <%v> or key file <%v> invalid: %v", s.SubCertFile, s.SubKeyFile, err)
		}
	}

	// WebBasePath is now env-only setting, only validate if set
	if s.WebBasePath != "" {
		if !strings.HasPrefix(s.WebBasePath, "/") {
			s.WebBasePath = "/" + s.WebBasePath
		}
		if !strings.HasSuffix(s.WebBasePath, "/") {
			s.WebBasePath += "/"
		}
	}
	if !strings.HasPrefix(s.SubPath, "/") {
		s.SubPath = "/" + s.SubPath
	}
	if !strings.HasSuffix(s.SubPath, "/") {
		s.SubPath += "/"
	}

	if !strings.HasPrefix(s.SubJsonPath, "/") {
		s.SubJsonPath = "/" + s.SubJsonPath
	}
	if !strings.HasSuffix(s.SubJsonPath, "/") {
		s.SubJsonPath += "/"
	}

	_, err := time.LoadLocation(s.TimeLocation)
	if err != nil {
		return common.NewError("time location not exist:", s.TimeLocation)
	}

	// Validate HWID mode
	validHwidModes := map[string]bool{
		"off":                true,
		"client_header":     true,
		"legacy_fingerprint": true,
	}
	if s.HwidMode != "" && !validHwidModes[s.HwidMode] {
		return common.NewErrorf("invalid hwidMode: %s (must be one of: off, client_header, legacy_fingerprint)", s.HwidMode)
	}

	return nil
}

// SubscriptionHeaders represents subscription HTTP headers configuration
// This structure is used to store and manage custom headers for subscription responses
type SubscriptionHeaders struct {
	// Standard headers (supported by both Happ and V2RayTun)
	ProfileTitle          string `json:"profileTitle,omitempty"`          // Subscription name (max 25 chars for Happ)
	SubscriptionUserinfo  string `json:"subscriptionUserinfo,omitempty"`  // Traffic info: upload=X; download=Y; total=Z; expire=T
	ProfileUpdateInterval string `json:"profileUpdateInterval,omitempty"` // Update interval in hours
	SupportUrl            string `json:"supportUrl,omitempty"`            // Support button URL
	ProfileWebPageUrl     string `json:"profileWebPageUrl,omitempty"`     // Subscription website URL
	Announce              string `json:"announce,omitempty"`               // Announcement text (max 200 chars)
	AnnounceUrl           string `json:"announceUrl,omitempty"`           // Announcement click URL (V2RayTun)
	Routing               string `json:"routing,omitempty"`               // Base64 encoded routing config
	RoutingEnable         string `json:"routingEnable,omitempty"`          // Enable/disable routing (0/1)
	CustomTunnelConfig    string `json:"customTunnelConfig,omitempty"`    // Custom tunnel config JSON (Happ)

	// Extended Happ headers (require Provider ID)
	NewUrl                string `json:"newUrl,omitempty"`                // New subscription URL
	NewDomain             string `json:"newDomain,omitempty"`              // New domain for subscription
	ServerDescription     string `json:"serverDescription,omitempty"`      // Server description (max 30 chars, base64)
	SubExpire             string `json:"subExpire,omitempty"`             // Enable expire notifications (true/1)
	SubExpireButtonLink   string `json:"subExpireButtonLink,omitempty"`   // Expire notification button link
	SubInfoColor          string `json:"subInfoColor,omitempty"`          // Info block color (red/blue/green)
	SubInfoText           string `json:"subInfoText,omitempty"`            // Info block text (max 200 chars)
	SubInfoButtonText     string `json:"subInfoButtonText,omitempty"`     // Info block button text (max 25 chars)
	SubInfoButtonLink     string `json:"subInfoButtonLink,omitempty"`     // Info block button link
	SubscriptionAlwaysHwidEnable string `json:"subscriptionAlwaysHwidEnable,omitempty"` // Force HWID enable (true/1)
	NotificationSubsExpire        string `json:"notificationSubsExpire,omitempty"`        // Enable expire notifications (true/1)
	HideSettings                  string `json:"hideSettings,omitempty"`                 // Hide settings in app (true/1)
	ServerAddressResolveEnable    string `json:"serverAddressResolveEnable,omitempty"`  // Enable DNS resolve (true/1)
	ServerAddressResolveDnsDomain string `json:"serverAddressResolveDnsDomain,omitempty"` // DoH server URL
	ServerAddressResolveDnsIP     string `json:"serverAddressResolveDnsIP,omitempty"`     // DoH server IP
	SubscriptionAutoconnect       string `json:"subscriptionAutoconnect,omitempty"`       // Auto-connect on start (true/1)
	SubscriptionAutoconnectType   string `json:"subscriptionAutoconnectType,omitempty"`   // Auto-connect type (lastused/lowestdelay)
	SubscriptionPingOnopenEnabled  string `json:"subscriptionPingOnopenEnabled,omitempty"` // Ping on open (true/1)
	SubscriptionAutoUpdateEnable  string `json:"subscriptionAutoUpdateEnable,omitempty"`  // Auto-update enable (true/1)
	FragmentationEnable           string `json:"fragmentationEnable,omitempty"`           // Enable fragmentation (true/1)
	FragmentationPackets          string `json:"fragmentationPackets,omitempty"`           // Fragmentation packets
	FragmentationLength            string `json:"fragmentationLength,omitempty"`           // Fragmentation length
	FragmentationInterval          string `json:"fragmentationInterval,omitempty"`         // Fragmentation interval
	FragmentationMaxsplit          string `json:"fragmentationMaxsplit,omitempty"`         // Fragmentation max split
	NoisesEnable                  string `json:"noisesEnable,omitempty"`                   // Enable noises (true/1)
	NoisesType                    string `json:"noisesType,omitempty"`                     // Noises type (rand/str/base64)
	NoisesPacket                  string `json:"noisesPacket,omitempty"`                   // Noises packet
	NoisesDelay                   string `json:"noisesDelay,omitempty"`                    // Noises delay
	NoisesApplyto                 string `json:"noisesApplyto,omitempty"`                  // Noises apply to (ip/ipv4/ipv6)
	PingType                      string `json:"pingType,omitempty"`                       // Ping type (proxy/proxy-head/tcp/icmp)
	CheckUrlViaProxy              string `json:"checkUrlViaProxy,omitempty"`               // Check URL via proxy
	ChangeUserAgent               string `json:"changeUserAgent,omitempty"`                // Custom User-Agent
	AppAutoStart                  string `json:"appAutoStart,omitempty"`                   // Auto-start app (true/1)
	SubscriptionAutoUpdateOpenEnable string `json:"subscriptionAutoUpdateOpenEnable,omitempty"` // Auto-update on open (true/1)
	PerAppProxyMode               string `json:"perAppProxyMode,omitempty"`                // Per-app proxy mode (off/on/bypass)
	PerAppProxyList               string `json:"perAppProxyList,omitempty"`                // Per-app proxy list (comma-separated)
	SniffingEnable                string `json:"sniffingEnable,omitempty"`                // Enable sniffing (true/1)
	SubscriptionsCollapse         string `json:"subscriptionsCollapse,omitempty"`        // Collapse subscriptions (false/0)
	PingResult                    string `json:"pingResult,omitempty"`                     // Ping result display (time/icon)
	MuxEnable                     string `json:"muxEnable,omitempty"`                      // Enable Mux (true/1)
	MuxTcpConnections              string `json:"muxTcpConnections,omitempty"`              // Mux TCP connections
	MuxXudpConnections            string `json:"muxXudpConnections,omitempty"`              // Mux XUDP connections
	MuxQuic                       string `json:"muxQuic,omitempty"`                        // Mux QUIC setting
	ProxyEnable                   string `json:"proxyEnable,omitempty"`                    // Enable proxy mode (true/1)
	TunEnable                     string `json:"tunEnable,omitempty"`                      // Enable TUN mode (true/1)
	TunMode                       string `json:"tunMode,omitempty"`                        // TUN mode (system/gvisor)
	TunType                       string `json:"tunType,omitempty"`                       // TUN type (singbox/tun2proxy)
	ExcludeRoutes                 string `json:"excludeRoutes,omitempty"`                 // Exclude routes (space/comma-separated)
	ColorProfile                  string `json:"colorProfile,omitempty"`                   // Color theme profile (JSON or base64)

	// V2RayTun specific headers
	UpdateAlways string `json:"updateAlways,omitempty"` // Force update on every app open (true)
}
