-- Remove bug_reports table and related indexes
-- Migration is idempotent and can be run multiple times safely

DROP INDEX IF EXISTS idx_bug_reports_user_id;
DROP INDEX IF EXISTS idx_bug_reports_status;
DROP INDEX IF EXISTS idx_bug_reports_taiga_task_id;
DROP INDEX IF EXISTS idx_bug_reports_created_at;
DROP TABLE IF EXISTS bug_reports;
