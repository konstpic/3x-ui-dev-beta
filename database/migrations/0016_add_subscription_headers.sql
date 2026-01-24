-- Migration: Add subscription headers setting
-- This migration adds the subHeaders setting for storing subscription HTTP headers configuration
--
-- This migration is idempotent and safe to run multiple times.

-- Add subHeaders setting (default: empty JSON object "{}")
INSERT INTO settings (key, value)
SELECT 'subHeaders', '{}'
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'subHeaders'
);

-- Note: This setting is automatically initialized by the application's defaultValueMap
-- if it doesn't exist, but this migration ensures it is present in the database
-- with the correct default value.
