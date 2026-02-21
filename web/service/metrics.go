// Package service provides Prometheus metrics export functionality for 3x-ui panel.
package service

import (
	"fmt"
	"strings"
	"sync"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
)

var (
	metricsMu        sync.RWMutex
	userAgentCounts = make(map[string]int64)
	userAgentMu     sync.RWMutex
)

// InitMetricsExporter initializes the metrics exporter
// Metrics are exposed via /panel/metrics endpoint for Prometheus/Grafana scraping
func InitMetricsExporter() {
	// Metrics are collected on-demand when /panel/metrics endpoint is accessed
	// No initialization needed - just ensure the endpoint is available
}

// RecordUserAgent records a User-Agent request from subscription server
func RecordUserAgent(userAgent string) {
	if userAgent == "" {
		userAgent = "unknown"
	}
	// Normalize User-Agent: take first part before space (e.g., "Happ/1.0.0" -> "Happ")
	parts := strings.Fields(userAgent)
	if len(parts) > 0 {
		// Remove version numbers and keep only the main identifier
		normalized := strings.Split(parts[0], "/")[0]
		if normalized == "" {
			normalized = "unknown"
		}
		userAgent = normalized
	}
	
	userAgentMu.Lock()
	defer userAgentMu.Unlock()
	userAgentCounts[userAgent]++
}

// CollectMetrics collects all metrics and returns them in Prometheus format
func CollectMetrics() string {
	var builder strings.Builder

	// Get server status
	serverService := &ServerService{}
	status := serverService.GetStatus(nil)
	if status != nil {
		// System metrics
		builder.WriteString(fmt.Sprintf("# HELP xui_cpu_usage CPU usage percentage\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_cpu_usage gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_cpu_usage %.2f\n", status.Cpu))

		builder.WriteString(fmt.Sprintf("# HELP xui_cpu_cores Number of CPU cores\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_cpu_cores gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_cpu_cores %d\n", status.CpuCores))

		builder.WriteString(fmt.Sprintf("# HELP xui_memory_bytes Memory usage in bytes\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_memory_bytes gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_memory_bytes{type=\"used\"} %d\n", status.Mem.Current))
		builder.WriteString(fmt.Sprintf("xui_memory_bytes{type=\"total\"} %d\n", status.Mem.Total))

		builder.WriteString(fmt.Sprintf("# HELP xui_swap_bytes Swap usage in bytes\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_swap_bytes gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_swap_bytes{type=\"used\"} %d\n", status.Swap.Current))
		builder.WriteString(fmt.Sprintf("xui_swap_bytes{type=\"total\"} %d\n", status.Swap.Total))

		builder.WriteString(fmt.Sprintf("# HELP xui_disk_bytes Disk usage in bytes\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_disk_bytes gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_disk_bytes{type=\"used\"} %d\n", status.Disk.Current))
		builder.WriteString(fmt.Sprintf("xui_disk_bytes{type=\"total\"} %d\n", status.Disk.Total))

		builder.WriteString(fmt.Sprintf("# HELP xui_load_average System load average\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_load_average gauge\n"))
		if len(status.Loads) >= 1 {
			builder.WriteString(fmt.Sprintf("xui_load_average{period=\"1m\"} %.2f\n", status.Loads[0]))
		}
		if len(status.Loads) >= 2 {
			builder.WriteString(fmt.Sprintf("xui_load_average{period=\"5m\"} %.2f\n", status.Loads[1]))
		}
		if len(status.Loads) >= 3 {
			builder.WriteString(fmt.Sprintf("xui_load_average{period=\"15m\"} %.2f\n", status.Loads[2]))
		}

		builder.WriteString(fmt.Sprintf("# HELP xui_network_io_bytes Network I/O in bytes per second\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_network_io_bytes gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_network_io_bytes{direction=\"up\"} %d\n", status.NetIO.Up))
		builder.WriteString(fmt.Sprintf("xui_network_io_bytes{direction=\"down\"} %d\n", status.NetIO.Down))

		builder.WriteString(fmt.Sprintf("# HELP xui_network_traffic_bytes Total network traffic in bytes\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_network_traffic_bytes counter\n"))
		builder.WriteString(fmt.Sprintf("xui_network_traffic_bytes{direction=\"sent\"} %d\n", status.NetTraffic.Sent))
		builder.WriteString(fmt.Sprintf("xui_network_traffic_bytes{direction=\"recv\"} %d\n", status.NetTraffic.Recv))

		builder.WriteString(fmt.Sprintf("# HELP xui_connections_count Number of connections\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_connections_count gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_connections_count{type=\"tcp\"} %d\n", status.TcpCount))
		builder.WriteString(fmt.Sprintf("xui_connections_count{type=\"udp\"} %d\n", status.UdpCount))

		// Xray metrics
		xrayState := 0
		if status.Xray.State == "running" {
			xrayState = 1
		}
		builder.WriteString(fmt.Sprintf("# HELP xui_xray_state Xray process state (1=running, 0=stopped)\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_xray_state gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_xray_state %d\n", xrayState))

		builder.WriteString(fmt.Sprintf("# HELP xui_xray_version_info Xray version information\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_xray_version_info gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_xray_version_info{version=\"%s\"} 1\n", escapeLabelValue(status.Xray.Version)))

		// Application metrics
		builder.WriteString(fmt.Sprintf("# HELP xui_app_uptime_seconds Application uptime in seconds\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_app_uptime_seconds gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_app_uptime_seconds %d\n", status.AppStats.Uptime))

		builder.WriteString(fmt.Sprintf("# HELP xui_app_threads Number of application threads\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_app_threads gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_app_threads %d\n", status.AppStats.Threads))

		builder.WriteString(fmt.Sprintf("# HELP xui_app_memory_bytes Application memory usage in bytes\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_app_memory_bytes gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_app_memory_bytes %d\n", status.AppStats.Mem))

		// Database metrics
		builder.WriteString(fmt.Sprintf("# HELP xui_database_size_bytes Database size in bytes\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_database_size_bytes gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_database_size_bytes %d\n", status.Database.Size))

		builder.WriteString(fmt.Sprintf("# HELP xui_database_tables Number of database tables\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_database_tables gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_database_tables %d\n", status.Database.Tables))

		builder.WriteString(fmt.Sprintf("# HELP xui_database_rows_total Total number of rows in database\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_database_rows_total gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_database_rows_total %d\n", status.Database.TotalRows))

		builder.WriteString(fmt.Sprintf("# HELP xui_database_connections Database connection pool statistics\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_database_connections gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_database_connections{state=\"open\"} %d\n", status.Database.OpenConns))
		builder.WriteString(fmt.Sprintf("xui_database_connections{state=\"idle\"} %d\n", status.Database.IdleConns))
		builder.WriteString(fmt.Sprintf("xui_database_connections{state=\"max_open\"} %d\n", status.Database.MaxOpenConns))
		builder.WriteString(fmt.Sprintf("xui_database_connections{state=\"max_idle\"} %d\n", status.Database.MaxIdleConns))

		// Node metrics (multi-node mode)
		builder.WriteString(fmt.Sprintf("# HELP xui_nodes_total Total number of nodes\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_nodes_total gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_nodes_total %d\n", status.Nodes.Total))

		builder.WriteString(fmt.Sprintf("# HELP xui_nodes_online Number of online nodes\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_nodes_online gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_nodes_online %d\n", status.Nodes.Online))
	}

	// Inbound metrics
	inboundService := &InboundService{}
	inbounds, err := inboundService.GetAllInbounds()
	if err == nil {
		builder.WriteString(fmt.Sprintf("# HELP xui_inbounds_total Total number of inbounds\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_inbounds_total gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_inbounds_total %d\n", len(inbounds)))

		enabledCount := 0
		// Define metrics once outside the loop
		builder.WriteString(fmt.Sprintf("# HELP xui_inbound_traffic_bytes Traffic per inbound in bytes\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_inbound_traffic_bytes counter\n"))
		builder.WriteString(fmt.Sprintf("# HELP xui_inbound_state Inbound state (1=enabled, 0=disabled)\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_inbound_state gauge\n"))
		
		for _, inbound := range inbounds {
			if inbound.Enable {
				enabledCount++
			}

			// Traffic per inbound
			builder.WriteString(fmt.Sprintf("xui_inbound_traffic_bytes{inbound=\"%s\",direction=\"up\"} %d\n",
				escapeLabelValue(inbound.Tag), inbound.Up))
			builder.WriteString(fmt.Sprintf("xui_inbound_traffic_bytes{inbound=\"%s\",direction=\"down\"} %d\n",
				escapeLabelValue(inbound.Tag), inbound.Down))
			builder.WriteString(fmt.Sprintf("xui_inbound_traffic_bytes{inbound=\"%s\",direction=\"total\"} %d\n",
				escapeLabelValue(inbound.Tag), inbound.Total))

			// Inbound state
			inboundState := 0
			if inbound.Enable {
				inboundState = 1
			}
			builder.WriteString(fmt.Sprintf("xui_inbound_state{inbound=\"%s\"} %d\n",
				escapeLabelValue(inbound.Tag), inboundState))
		}

		builder.WriteString(fmt.Sprintf("# HELP xui_inbounds_enabled Number of enabled inbounds\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_inbounds_enabled gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_inbounds_enabled %d\n", enabledCount))
	}

	// Client metrics
	db := database.GetDB()
	var clients []*model.ClientEntity
	err = db.Model(model.ClientEntity{}).Find(&clients).Error
	if err == nil {
		builder.WriteString(fmt.Sprintf("# HELP xui_clients_total Total number of clients\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_clients_total gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_clients_total %d\n", len(clients)))

		enabledClientCount := 0
		// Define metrics once outside the loop
		builder.WriteString(fmt.Sprintf("# HELP xui_client_traffic_bytes Traffic per client in bytes\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_client_traffic_bytes counter\n"))
		builder.WriteString(fmt.Sprintf("# HELP xui_client_state Client state (1=enabled, 0=disabled)\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_client_state gauge\n"))
		
		for _, client := range clients {
			if client.Enable {
				enabledClientCount++
			}

			// Traffic per client
			builder.WriteString(fmt.Sprintf("xui_client_traffic_bytes{client=\"%s\",direction=\"up\"} %d\n",
				escapeLabelValue(client.Email), client.Up))
			builder.WriteString(fmt.Sprintf("xui_client_traffic_bytes{client=\"%s\",direction=\"down\"} %d\n",
				escapeLabelValue(client.Email), client.Down))
			builder.WriteString(fmt.Sprintf("xui_client_traffic_bytes{client=\"%s\",direction=\"total\"} %d\n",
				escapeLabelValue(client.Email), client.AllTime))

			// Client state
			clientState := 0
			if client.Enable {
				clientState = 1
			}
			builder.WriteString(fmt.Sprintf("xui_client_state{client=\"%s\"} %d\n",
				escapeLabelValue(client.Email), clientState))
		}

		builder.WriteString(fmt.Sprintf("# HELP xui_clients_enabled Number of enabled clients\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_clients_enabled gauge\n"))
		builder.WriteString(fmt.Sprintf("xui_clients_enabled %d\n", enabledClientCount))
	}

	// Node traffic metrics (multi-node mode)
	nodeService := &NodeService{}
	nodes, err := nodeService.GetAllNodes()
	if err == nil && len(nodes) > 0 {
		// Define metrics once outside the loop
		builder.WriteString(fmt.Sprintf("# HELP xui_node_traffic_bytes Traffic per node in bytes\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_node_traffic_bytes counter\n"))
		builder.WriteString(fmt.Sprintf("# HELP xui_node_state Node state (1=online, 0=offline)\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_node_state gauge\n"))
		builder.WriteString(fmt.Sprintf("# HELP xui_node_response_time_ms Node response time in milliseconds\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_node_response_time_ms gauge\n"))
		
		for _, node := range nodes {
			builder.WriteString(fmt.Sprintf("xui_node_traffic_bytes{node=\"%s\",node_id=\"%d\",direction=\"up\"} %d\n",
				escapeLabelValue(node.Name), node.Id, node.Up))
			builder.WriteString(fmt.Sprintf("xui_node_traffic_bytes{node=\"%s\",node_id=\"%d\",direction=\"down\"} %d\n",
				escapeLabelValue(node.Name), node.Id, node.Down))
			builder.WriteString(fmt.Sprintf("xui_node_traffic_bytes{node=\"%s\",node_id=\"%d\",direction=\"total\"} %d\n",
				escapeLabelValue(node.Name), node.Id, node.AllTime))

			// Node state
			nodeState := 0
			if node.Status == "online" {
				nodeState = 1
			}
			builder.WriteString(fmt.Sprintf("xui_node_state{node=\"%s\",node_id=\"%d\"} %d\n",
				escapeLabelValue(node.Name), node.Id, nodeState))

			builder.WriteString(fmt.Sprintf("xui_node_response_time_ms{node=\"%s\",node_id=\"%d\"} %d\n",
				escapeLabelValue(node.Name), node.Id, node.ResponseTime))
		}
	}

	// User Agent metrics (subscription server)
	userAgentMu.RLock()
	if len(userAgentCounts) > 0 {
		builder.WriteString(fmt.Sprintf("# HELP xui_subscription_user_agent_total Total subscription requests by User-Agent\n"))
		builder.WriteString(fmt.Sprintf("# TYPE xui_subscription_user_agent_total counter\n"))
		for ua, count := range userAgentCounts {
			builder.WriteString(fmt.Sprintf("xui_subscription_user_agent_total{user_agent=\"%s\"} %d\n",
				escapeLabelValue(ua), count))
		}
	}
	userAgentMu.RUnlock()

	return builder.String()
}

// escapeLabelValue escapes special characters in Prometheus label values
func escapeLabelValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return value
}
