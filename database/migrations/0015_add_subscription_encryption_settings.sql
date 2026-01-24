-- Migration: Add subscription encryption and filter settings
-- This migration adds new subscription settings for Happ/V2RayTun encryption and filtering
--
-- This migration is idempotent and safe to run multiple times.

-- Add subEncryptHappV2RayTun setting (default: false)
INSERT INTO settings (key, value)
SELECT 'subEncryptHappV2RayTun', 'false'
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'subEncryptHappV2RayTun'
);

-- Add subOnlyHappV2RayTun setting (default: false)
INSERT INTO settings (key, value)
SELECT 'subOnlyHappV2RayTun', 'false'
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'subOnlyHappV2RayTun'
);

-- Add subHideConfigLinks setting (default: false)
INSERT INTO settings (key, value)
SELECT 'subHideConfigLinks', 'false'
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'subHideConfigLinks'
);

-- Note: These settings are automatically initialized by the application's defaultValueMap
-- if they don't exist, but this migration ensures they are present in the database
-- with the correct default values.
