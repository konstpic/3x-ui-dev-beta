-- Migration: Initialize default xrayTemplateConfig in database
-- This migration ensures that xrayTemplateConfig exists in the database
-- so that Xray configuration is fully managed from the settings table.
-- It is idempotent and safe to run multiple times.

-- Ensure unique index on settings.key to prevent duplicate keys
CREATE UNIQUE INDEX IF NOT EXISTS idx_settings_key ON settings(key);

-- Ensure that a row for xrayTemplateConfig exists.
-- The actual default JSON will be populated and validated at application startup
-- by SettingService.EnsureXrayTemplateConfigValid(), which will replace this
-- placeholder value with a proper default template if needed.
INSERT INTO settings (key, value)
SELECT 'xrayTemplateConfig', '{}'
WHERE NOT EXISTS (SELECT 1 FROM settings WHERE key = 'xrayTemplateConfig');

