-- Migration: Add xray_core_config_profiles table
-- This migration adds support for managing Xray core configuration profiles in multi-node mode

-- Xray core config profiles table
CREATE TABLE IF NOT EXISTS xray_core_config_profiles (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL DEFAULT 0,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    config_json TEXT NOT NULL,  -- Full Xray JSON config (routing, dns, log, policy, stats, inbounds, outbounds)
    is_default BOOLEAN NOT NULL DEFAULT false,
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);

-- Create indexes for xray_core_config_profiles
CREATE INDEX IF NOT EXISTS idx_xray_core_config_profiles_user_id ON xray_core_config_profiles(user_id);
CREATE INDEX IF NOT EXISTS idx_xray_core_config_profiles_is_default ON xray_core_config_profiles(is_default);
