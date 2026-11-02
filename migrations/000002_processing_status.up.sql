-- Correct the original constraint and retain any legacy unfinished work.
BEGIN;
ALTER TABLE update_tasks DROP CONSTRAINT valid_status;
UPDATE update_tasks SET status = 'PROCESSING' WHERE status = 'IN_PROGRESS';
ALTER TABLE update_tasks ADD CONSTRAINT valid_status
    CHECK (status IN ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED'));
CREATE INDEX idx_update_tasks_pending ON update_tasks (created_at, id)
    WHERE status = 'PENDING';
COMMIT;
