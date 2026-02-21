-- Initial schema migration for SharX panel
-- This migration creates all base tables required by the application
-- Migration is idempotent and can be run multiple times safely

-- Users table
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(255) NOT NULL,
    password VARCHAR(255) NOT NULL
);

-- Settings table
CREATE TABLE IF NOT EXISTS settings (
    id SERIAL PRIMARY KEY,
    key VARCHAR(255) NOT NULL,
    value TEXT
);

-- Inbounds table
CREATE TABLE IF NOT EXISTS inbounds (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL DEFAULT 0,
    up BIGINT NOT NULL DEFAULT 0,
    down BIGINT NOT NULL DEFAULT 0,
    total BIGINT NOT NULL DEFAULT 0,
    all_time BIGINT NOT NULL DEFAULT 0,
    remark VARCHAR(255),
    enable BOOLEAN NOT NULL DEFAULT true,
    expiry_time BIGINT NOT NULL DEFAULT 0,
    traffic_reset VARCHAR(255) NOT NULL DEFAULT 'never',
    last_traffic_reset_time BIGINT NOT NULL DEFAULT 0,
    listen VARCHAR(255),
    port INTEGER NOT NULL,
    protocol VARCHAR(50) NOT NULL,
    settings TEXT,
    stream_settings TEXT,
    tag VARCHAR(255) UNIQUE,
    sniffing TEXT
);

-- Create index for inbounds enable and traffic_reset
CREATE INDEX IF NOT EXISTS idx_enable_traffic_reset ON inbounds(enable, traffic_reset);

-- Outbound traffics table
CREATE TABLE IF NOT EXISTS outbound_traffics (
    id SERIAL PRIMARY KEY,
    tag VARCHAR(255) UNIQUE NOT NULL,
    up BIGINT NOT NULL DEFAULT 0,
    down BIGINT NOT NULL DEFAULT 0,
    total BIGINT NOT NULL DEFAULT 0
);

-- Inbound client IPs table
CREATE TABLE IF NOT EXISTS inbound_client_ips (
    id SERIAL PRIMARY KEY,
    client_email VARCHAR(255) UNIQUE NOT NULL,
    ips TEXT
);

-- History of seeders table
CREATE TABLE IF NOT EXISTS history_of_seeders (
    id SERIAL PRIMARY KEY,
    seeder_name VARCHAR(255) NOT NULL
);

-- Client traffics table (from xray package)
CREATE TABLE IF NOT EXISTS client_traffics (
    id SERIAL PRIMARY KEY,
    inbound_id INTEGER NOT NULL,
    enable BOOLEAN NOT NULL DEFAULT true,
    email VARCHAR(255) UNIQUE NOT NULL,
    up BIGINT NOT NULL DEFAULT 0,
    down BIGINT NOT NULL DEFAULT 0,
    all_time BIGINT NOT NULL DEFAULT 0,
    expiry_time BIGINT NOT NULL DEFAULT 0,
    total BIGINT NOT NULL DEFAULT 0,
    reset INTEGER NOT NULL DEFAULT 0,
    last_online BIGINT NOT NULL DEFAULT 0
);

-- Nodes table
CREATE TABLE IF NOT EXISTS nodes (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    address VARCHAR(255) NOT NULL,
    api_key VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'unknown',
    last_check BIGINT NOT NULL DEFAULT 0,
    response_time BIGINT NOT NULL DEFAULT 0,
    use_tls BOOLEAN NOT NULL DEFAULT false,
    cert_path VARCHAR(255),
    key_path VARCHAR(255),
    insecure_tls BOOLEAN NOT NULL DEFAULT false,
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);

-- Inbound node mappings table
CREATE TABLE IF NOT EXISTS inbound_node_mappings (
    id SERIAL PRIMARY KEY,
    inbound_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE(inbound_id, node_id)
);

-- Create index for inbound_node_mappings
CREATE INDEX IF NOT EXISTS idx_inbound_node ON inbound_node_mappings(inbound_id, node_id);

-- Client entities table
CREATE TABLE IF NOT EXISTS client_entities (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL,
    email VARCHAR(255) NOT NULL,
    uuid VARCHAR(255),
    security VARCHAR(50),
    password VARCHAR(255),
    flow VARCHAR(50),
    limit_ip INTEGER NOT NULL DEFAULT 0,
    total_gb DOUBLE PRECISION NOT NULL DEFAULT 0,
    expiry_time BIGINT NOT NULL DEFAULT 0,
    enable BOOLEAN NOT NULL DEFAULT true,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    tg_id BIGINT NOT NULL DEFAULT 0,
    sub_id VARCHAR(255),
    comment TEXT,
    reset INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0,
    up BIGINT NOT NULL DEFAULT 0,
    down BIGINT NOT NULL DEFAULT 0,
    all_time BIGINT NOT NULL DEFAULT 0,
    last_online BIGINT NOT NULL DEFAULT 0,
    hwid_enabled BOOLEAN NOT NULL DEFAULT false,
    max_hwid INTEGER NOT NULL DEFAULT 1
);

-- Create index for client_entities
CREATE INDEX IF NOT EXISTS idx_user_email ON client_entities(user_id, email);
CREATE INDEX IF NOT EXISTS idx_sub_id ON client_entities(sub_id);

-- Client inbound mappings table
CREATE TABLE IF NOT EXISTS client_inbound_mappings (
    id SERIAL PRIMARY KEY,
    client_id INTEGER NOT NULL,
    inbound_id INTEGER NOT NULL,
    UNIQUE(client_id, inbound_id)
);

-- Create index for client_inbound_mappings
CREATE INDEX IF NOT EXISTS idx_client_inbound ON client_inbound_mappings(client_id, inbound_id);

-- Hosts table
CREATE TABLE IF NOT EXISTS hosts (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    address VARCHAR(255) NOT NULL,
    port INTEGER NOT NULL DEFAULT 0,
    protocol VARCHAR(50),
    remark TEXT,
    enable BOOLEAN NOT NULL DEFAULT true,
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);

-- Create index for hosts
CREATE INDEX IF NOT EXISTS idx_hosts_user_id ON hosts(user_id);

-- Host inbound mappings table
CREATE TABLE IF NOT EXISTS host_inbound_mappings (
    id SERIAL PRIMARY KEY,
    host_id INTEGER NOT NULL,
    inbound_id INTEGER NOT NULL,
    UNIQUE(host_id, inbound_id)
);

-- Create index for host_inbound_mappings
CREATE INDEX IF NOT EXISTS idx_host_inbound ON host_inbound_mappings(host_id, inbound_id);

-- Client HWIDs table (note: table name is client_hw_ids, not client_hwids)
CREATE TABLE IF NOT EXISTS client_hw_ids (
    id SERIAL PRIMARY KEY,
    client_id INTEGER NOT NULL,
    hwid VARCHAR(255) NOT NULL,
    device_name VARCHAR(255),
    device_os VARCHAR(255),
    device_model VARCHAR(255),
    os_version VARCHAR(255),
    first_seen_at BIGINT NOT NULL DEFAULT 0,
    last_seen_at BIGINT NOT NULL DEFAULT 0,
    first_seen_ip VARCHAR(255),
    is_active BOOLEAN NOT NULL DEFAULT true,
    ip_address VARCHAR(255),
    user_agent TEXT,
    blocked_at BIGINT,
    block_reason TEXT,
    UNIQUE(client_id, hwid)
);

-- Create index for client_hw_ids
CREATE INDEX IF NOT EXISTS idx_client_hwid ON client_hw_ids(client_id, hwid);
