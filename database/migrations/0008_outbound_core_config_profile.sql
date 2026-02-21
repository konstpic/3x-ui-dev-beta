-- Migration: Add core_config_profile_id to outbounds table
-- This migration adds support for linking outbounds to Xray core configuration profiles

-- Add core_config_profile_id column to outbounds table
ALTER TABLE outbounds ADD COLUMN IF NOT EXISTS core_config_profile_id INTEGER;

-- Create index for core_config_profile_id
CREATE INDEX IF NOT EXISTS idx_outbounds_core_config_profile_id ON outbounds(core_config_profile_id);
