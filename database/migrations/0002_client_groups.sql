-- Migration: Add client groups support
-- This migration adds client_groups table and group_id column to client_entities

-- Client groups table
CREATE TABLE IF NOT EXISTS client_groups (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    color VARCHAR(50),
    created_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL DEFAULT 0
);

-- Create index for client_groups
CREATE INDEX IF NOT EXISTS idx_client_groups_user_id ON client_groups(user_id);

-- Add group_id column to client_entities
ALTER TABLE client_entities ADD COLUMN IF NOT EXISTS group_id INTEGER;

-- Create index for group_id
CREATE INDEX IF NOT EXISTS idx_client_entities_group_id ON client_entities(group_id);

-- Add foreign key constraint (optional, for referential integrity)
-- Note: We use IF NOT EXISTS pattern, but PostgreSQL doesn't support IF NOT EXISTS for constraints
-- So we'll check if constraint exists first
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
