BEGIN;
CREATE TABLE task_queue_guard (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton)
);
INSERT INTO task_queue_guard(singleton) VALUES (true);
CREATE INDEX idx_update_tasks_unfinished ON update_tasks (status)
WHERE status IN ('PENDING', 'PROCESSING');
COMMIT;
