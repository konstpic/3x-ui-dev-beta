-- Migration: Add Grafana integration settings
-- This migration adds settings for Grafana integration:
-- - grafanaLokiUrl: URL for Loki API endpoint
-- - grafanaVictoriaMetricsUrl: URL for VictoriaMetrics API endpoint
-- - grafanaEnable: Enable/disable Grafana integration
--
-- This migration is idempotent and safe to run multiple times.

-- Add grafanaLokiUrl setting (default: empty string)
INSERT INTO settings (key, value)
SELECT 'grafanaLokiUrl', ''
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'grafanaLokiUrl'
);

-- Add grafanaVictoriaMetricsUrl setting (default: empty string)
INSERT INTO settings (key, value)
SELECT 'grafanaVictoriaMetricsUrl', ''
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'grafanaVictoriaMetricsUrl'
);

-- Add grafanaEnable setting (default: false)
INSERT INTO settings (key, value)
SELECT 'grafanaEnable', 'false'
WHERE NOT EXISTS (
    SELECT 1 FROM settings WHERE key = 'grafanaEnable'
);

-- Note: These settings are automatically initialized by the application's defaultValueMap
-- if they don't exist, but this migration ensures they are present in the database
-- with the correct default values for existing installations.
