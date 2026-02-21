-- Migration: Remove color column from client_groups table
-- This migration removes the color column as it's no longer needed

-- Remove color column from client_groups table
-- Using IF EXISTS to make it idempotent
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'client_groups' 
        AND column_name = 'color'
    ) THEN
        ALTER TABLE client_groups DROP COLUMN color;
    END IF;
END $$;
