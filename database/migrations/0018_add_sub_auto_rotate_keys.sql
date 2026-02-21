-- Migration: Add subscription auto-rotate keys setting
-- This migration adds the subAutoRotateKeys setting for automatically rotating client keys before subscription update interval
--
-- This migration is idempotent and safe to run multiple times.

-- Add subAutoRotateKeys setting (default: false)
INSERT INTO settings (key, value)
SELECT 'subAutoRotateKeys', 'false'
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'subAutoRotateKeys'
);

-- Note: This setting is automatically initialized by the application's defaultValueMap
-- if it doesn't exist, but this migration ensures it is present in the database
-- with the correct default value for existing installations.
