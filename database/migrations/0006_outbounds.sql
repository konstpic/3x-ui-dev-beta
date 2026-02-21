-- Migration: Add outbounds and outbound_node_mappings tables
-- This migration adds support for managing outbound configurations and assigning them to nodes

-- Outbounds table
CREATE TABLE IF NOT EXISTS outbounds (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL DEFAULT 0,
    remark VARCHAR(255),
    enable BOOLEAN NOT NULL DEFAULT true,
    protocol VARCHAR(50) NOT NULL,
    settings TEXT,
    stream_settings TEXT,
    tag VARCHAR(255) UNIQUE NOT NULL,
    proxy_settings TEXT,
    send_through VARCHAR(255),
    mux TEXT,
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);

-- Create index for outbounds
CREATE INDEX IF NOT EXISTS idx_outbounds_user_id ON outbounds(user_id);
CREATE INDEX IF NOT EXISTS idx_outbounds_enable ON outbounds(enable);

-- Outbound node mappings table
CREATE TABLE IF NOT EXISTS outbound_node_mappings (
    id SERIAL PRIMARY KEY,
    outbound_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE(outbound_id, node_id)
);

-- Create index for outbound_node_mappings
CREATE INDEX IF NOT EXISTS idx_outbound_node ON outbound_node_mappings(outbound_id, node_id);
