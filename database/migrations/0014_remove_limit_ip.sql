-- Migration: Remove limit_ip field from client_entities
-- This migration removes the limit_ip column from client_entities table
-- as IP limiting is being replaced with HWID-based device limiting
--
-- This migration is idempotent and safe to run multiple times.

-- Remove limit_ip column from client_entities table
-- Use IF EXISTS to make it safe to run multiple times
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 
        FROM information_schema.columns 
        WHERE table_name = 'client_entities' 
        AND column_name = 'limit_ip'
    ) THEN
        ALTER TABLE client_entities DROP COLUMN limit_ip;
    END IF;
END $$;

-- Remove ldapDefaultLimitIP setting from settings table (no longer used)
DELETE FROM settings WHERE key = 'ldapDefaultLimitIP';

-- Note: The limit_ip field in the old Client structure (stored in inbounds.settings JSON)
-- will be automatically ignored when parsing, so no migration needed for that.
-- The field will simply not be used in new client creations/updates.
