-- Migration: Add traffic tracking fields to nodes table
-- This migration adds support for tracking traffic statistics and limits on nodes

-- Add traffic tracking fields to nodes table
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS up BIGINT NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS down BIGINT NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS all_time BIGINT NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS traffic_limit_gb FLOAT NOT NULL DEFAULT 0;
