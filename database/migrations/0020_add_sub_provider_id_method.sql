-- Migration: Add subProviderIDMethod setting
-- This migration adds the subProviderIDMethod setting for choosing how to deliver Provider ID
-- to clients: "url" (query parameter), "header" (HTTP header), or "none" (disabled)
--
-- This migration is idempotent and safe to run multiple times.

-- Add subProviderIDMethod setting (default: "url" for backward compatibility)
INSERT INTO settings (key, value)
SELECT 'subProviderIDMethod', 'url'
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'subProviderIDMethod'
);

-- Note: This setting is automatically initialized by the application's defaultValueMap
-- if it doesn't exist, but this migration ensures it is present in the database
-- with the correct default value for existing installations.
