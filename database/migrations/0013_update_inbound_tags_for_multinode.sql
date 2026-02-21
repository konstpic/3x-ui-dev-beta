-- Migration: Update inbound tags for multi-node mode compatibility
-- This migration updates existing inbound tags to use ID-based format in multi-node mode
-- to allow multiple inbounds with the same port (with different SNI).
-- 
-- In single-node mode: tags remain as inbound-{port} or inbound-{listen}:{port}
-- In multi-node mode: tags are updated to inbound-{id} for uniqueness
--
-- This migration is idempotent and safe to run multiple times.
-- It only updates tags that don't already match the ID-based format.

-- Update tags for inbounds that don't already have ID-based tags
-- Only update if multi-node mode is enabled AND tag doesn't match the pattern 'inbound-{id}' where id equals the inbound id
UPDATE inbounds
SET tag = 'inbound-' || id::text
WHERE EXISTS (
    SELECT 1 FROM settings 
    WHERE key = 'multiNodeMode' 
    AND value = 'true'
)
AND tag != 'inbound-' || id::text
AND (
    -- Match old format: inbound-{port} (but not inbound-{id} where id matches)
    (tag ~ '^inbound-[0-9]+$' AND tag != 'inbound-' || id::text)
    OR
    -- Match old format: inbound-{listen}:{port}
    tag ~ '^inbound-.*:[0-9]+$'
);

-- Note: This migration only updates tags if multi-node mode is enabled.
-- If multi-node mode is disabled, tags remain unchanged and will use port-based format.
-- When multi-node mode is enabled later, tags will be updated on next inbound modification.
