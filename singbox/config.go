package singbox

import (
	"bytes"

	"github.com/mhsanaei/3x-ui/v2/util/json_util"
)

// Config represents the complete sing-box configuration structure.
// Based on sing-box configuration format: https://sing-box.sagernet.org/configuration/
type Config struct {
	Log          json_util.RawMessage `json:"log,omitempty"`
	DNS          json_util.RawMessage  `json:"dns,omitempty"`
	NTP          json_util.RawMessage  `json:"ntp,omitempty"`
	Inbounds     []InboundConfig      `json:"inbounds,omitempty"`
	Outbounds    json_util.RawMessage  `json:"outbounds,omitempty"`
	Route        json_util.RawMessage  `json:"route,omitempty"`
	Experimental json_util.RawMessage  `json:"experimental,omitempty"`
}

// Equals compares two Config instances for deep equality.
func (c *Config) Equals(other *Config) bool {
	if len(c.Inbounds) != len(other.Inbounds) {
		return false
	}
	for i, inbound := range c.Inbounds {
		if !inbound.Equals(&other.Inbounds[i]) {
			return false
		}
	}
	if !bytes.Equal(c.Log, other.Log) {
		return false
	}
	if !bytes.Equal(c.DNS, other.DNS) {
		return false
	}
	if !bytes.Equal(c.NTP, other.NTP) {
		return false
	}
	if !bytes.Equal(c.Outbounds, other.Outbounds) {
		return false
	}
	if !bytes.Equal(c.Route, other.Route) {
		return false
	}
	if !bytes.Equal(c.Experimental, other.Experimental) {
		return false
	}
	return true
}
