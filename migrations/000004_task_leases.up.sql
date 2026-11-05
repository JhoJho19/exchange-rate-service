BEGIN;
ALTER TABLE update_tasks ADD COLUMN leased_until TIMESTAMPTZ,
    ADD COLUMN lease_token UUID;
-- Legacy workers must be stopped before applying this migration.
UPDATE update_tasks SET status = 'PENDING' WHERE status = 'PROCESSING';
ALTER TABLE update_tasks ADD CONSTRAINT valid_task_lease CHECK (
    (status = 'PROCESSING' AND leased_until IS NOT NULL AND lease_token IS NOT NULL)
    OR (status <> 'PROCESSING' AND leased_until IS NULL AND lease_token IS NULL)
);
-- The existing idx_update_tasks_pending covers unclaimed tasks.
CREATE INDEX idx_update_tasks_expired_lease ON update_tasks (leased_until, created_at, id)
    WHERE status = 'PROCESSING';
COMMIT;
