-- Migration: Add subscription page customization settings
-- This migration adds new subscription page customization settings for themes, branding, and background images
--
-- This migration is idempotent and safe to run multiple times.

-- Add subPageTheme setting (default: empty string - no theme)
INSERT INTO settings (key, value)
SELECT 'subPageTheme', ''
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'subPageTheme'
);

-- Add subPageLogoUrl setting (default: empty string - no logo)
INSERT INTO settings (key, value)
SELECT 'subPageLogoUrl', ''
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'subPageLogoUrl'
);

-- Add subPageBrandText setting (default: empty string - no brand text)
INSERT INTO settings (key, value)
SELECT 'subPageBrandText', ''
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'subPageBrandText'
);

-- Add subPageBackgroundUrl setting (default: empty string - no custom background)
INSERT INTO settings (key, value)
SELECT 'subPageBackgroundUrl', ''
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'subPageBackgroundUrl'
);

-- Note: These settings are automatically initialized by the application's defaultValueMap
-- if they don't exist, but this migration ensures they are present in the database
-- with the correct default values for existing installations.
