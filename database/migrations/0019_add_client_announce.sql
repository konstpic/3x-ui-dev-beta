-- Migration: Add announce field to client_entities
-- This migration adds the announce field for per-client announcement customization
-- The announce field allows clients to have their own announcement text that overrides
-- the subscription header announce setting.
--
-- This migration is idempotent and safe to run multiple times.

-- Add announce column to client_entities table
ALTER TABLE client_entities
ADD COLUMN IF NOT EXISTS announce TEXT;

-- Note: The announce field supports up to 200 characters and base64 encoding,
-- as per the subscription header announce specification. Validation is handled
-- by the application layer.
