-- Migration: Cleanup invalid group_id references
-- This migration cleans up any client_entities that reference non-existent groups
-- This fixes the issue where clients have group_id pointing to deleted groups

-- Clean up invalid group_id references (clients pointing to non-existent groups)
-- This is safe to run multiple times (idempotent)
UPDATE client_entities
SET group_id = NULL
WHERE group_id IS NOT NULL
  AND group_id NOT IN (SELECT id FROM client_groups);
