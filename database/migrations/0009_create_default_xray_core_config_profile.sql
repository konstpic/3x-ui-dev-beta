-- Migration: Create default Xray core config profile
-- This migration creates a default profile from the current xrayTemplateConfig for all users
-- Note: This migration should be run after the xray_core_config_profiles table is created
-- The actual profile creation will be handled by the application logic on first access

-- This migration is a placeholder - the actual default profile creation
-- will be handled by XrayCoreConfigProfileService.CreateDefaultProfileFromTemplate()
-- when a user first accesses the profiles page in multi-node mode
