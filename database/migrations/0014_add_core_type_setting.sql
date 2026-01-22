-- Migration: Add core type setting
-- This migration adds the coreType setting to allow switching between xray and sing-box cores
-- Default value is "xray" to maintain backward compatibility

-- Insert coreType setting if it doesn't exist
INSERT INTO settings (key, value)
SELECT 'coreType', 'xray'
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'coreType'
);
