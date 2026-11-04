ALTER TABLE update_tasks ADD COLUMN idempotency_key VARCHAR(255) UNIQUE;
