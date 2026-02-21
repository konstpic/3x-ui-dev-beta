-- Migration: Fix group_id to allow NULL values
-- This migration ensures that group_id column can be NULL and the foreign key constraint allows NULL values
-- This fixes the issue where creating a client without a group would fail with a foreign key constraint violation

-- Ensure group_id column allows NULL (should already be the case, but making it explicit)
DO $$
BEGIN
    -- Check if column exists and if it has NOT NULL constraint
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'client_entities' AND column_name = 'group_id'
    ) THEN
        -- Remove NOT NULL constraint if it exists (shouldn't exist, but just in case)
        ALTER TABLE client_entities ALTER COLUMN group_id DROP NOT NULL;
    END IF;
END $$;

-- Recreate foreign key constraint to ensure it properly handles NULL values
-- First, drop the existing constraint if it exists
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint 
        WHERE conname = 'fk_client_entities_group_id'
    ) THEN
        ALTER TABLE client_entities DROP CONSTRAINT fk_client_entities_group_id;
    END IF;
END $$;

-- Clean up invalid group_id references (clients pointing to non-existent groups)
-- This must be done BEFORE recreating the foreign key constraint
UPDATE client_entities
SET group_id = NULL
WHERE group_id IS NOT NULL
  AND group_id NOT IN (SELECT id FROM client_groups);

-- Recreate the foreign key constraint with explicit NULL handling
-- PostgreSQL foreign keys allow NULL by default, but we're being explicit
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint 
        WHERE conname = 'fk_client_entities_group_id'
    ) THEN
        ALTER TABLE client_entities 
        ADD CONSTRAINT fk_client_entities_group_id 
        FOREIGN KEY (group_id) REFERENCES client_groups(id) ON DELETE SET NULL;
    END IF;
END $$;
