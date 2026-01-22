package singbox

import (
	"bytes"

	"github.com/mhsanaei/3x-ui/v2/util/json_util"
)

// InboundConfig represents a sing-box inbound configuration.
// It defines how sing-box accepts incoming connections including protocol, port, and settings.
type InboundConfig struct {
	Type         string               `json:"type"`                   // Protocol type: vmess, vless, trojan, shadowsocks, etc.
	Tag          string                `json:"tag,omitempty"`          // Inbound tag for routing
	Listen       json_util.RawMessage  `json:"listen,omitempty"`      // Listen address (can be string or null)
	ListenPort   int                   `json:"listen_port,omitempty"` // Listen port
	Users        json_util.RawMessage  `json:"users,omitempty"`       // Users array (for protocols with users)
	Method       string                `json:"method,omitempty"`      // Encryption method (for shadowsocks)
	Password     string                `json:"password,omitempty"`    // Password (for trojan, shadowsocks)
	Network      json_util.RawMessage  `json:"network,omitempty"`    // Network type: tcp, udp, etc.
	Transport    json_util.RawMessage `json:"transport,omitempty"`  // V2Ray Transport (ws, grpc, quic, http, httpupgrade) - NOT for TLS/Reality
	TLS          json_util.RawMessage  `json:"tls,omitempty"`        // TLS/Reality configuration (separate from transport)
	Sniff        bool                  `json:"sniff,omitempty"`      // Enable protocol sniffing
	SniffOverrideDestination bool      `json:"sniff_override_destination,omitempty"` // Override destination
	Settings     json_util.RawMessage  `json:"settings,omitempty"`    // Protocol-specific settings
}

// Equals compares two InboundConfig instances for deep equality.
func (c *InboundConfig) Equals(other *InboundConfig) bool {
	if c.Type != other.Type {
		return false
	}
	if c.Tag != other.Tag {
		return false
	}
	if c.ListenPort != other.ListenPort {
		return false
	}
	if c.Method != other.Method {
		return false
	}
	if c.Password != other.Password {
		return false
	}
	if c.Sniff != other.Sniff {
		return false
	}
	if c.SniffOverrideDestination != other.SniffOverrideDestination {
		return false
	}
	if !bytes.Equal(c.Listen, other.Listen) {
		return false
	}
	if !bytes.Equal(c.Users, other.Users) {
		return false
	}
	if !bytes.Equal(c.Network, other.Network) {
		return false
	}
	if !bytes.Equal(c.Transport, other.Transport) {
		return false
	}
	if !bytes.Equal(c.TLS, other.TLS) {
		return false
	}
	if !bytes.Equal(c.Settings, other.Settings) {
		return false
	}
	return true
}
