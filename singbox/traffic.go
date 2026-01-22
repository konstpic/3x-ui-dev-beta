package singbox

// Traffic represents network traffic statistics for sing-box connections.
// It tracks upload and download bytes for inbound or outbound traffic.
type Traffic struct {
	IsInbound  bool
	IsOutbound bool
	Tag        string
	Up         int64
	Down       int64
}

// ClientTraffic represents traffic statistics for a specific client (user).
type ClientTraffic struct {
	Email string
	Up    int64
	Down  int64
}
