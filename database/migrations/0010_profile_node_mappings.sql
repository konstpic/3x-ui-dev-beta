-- Migration: Add profile_node_mappings table
-- This migration adds support for assigning Xray core config profiles to nodes

-- Profile node mappings table
CREATE TABLE IF NOT EXISTS profile_node_mappings (
    id SERIAL PRIMARY KEY,
    profile_id INTEGER NOT NULL,
    node_id INTEGER NOT NULL,
    UNIQUE(profile_id, node_id),
    FOREIGN KEY (profile_id) REFERENCES xray_core_config_profiles(id) ON DELETE CASCADE,
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

-- Create index for profile_node_mappings
CREATE INDEX IF NOT EXISTS idx_profile_node ON profile_node_mappings(profile_id, node_id);
CREATE INDEX IF NOT EXISTS idx_profile_node_node_id ON profile_node_mappings(node_id);
