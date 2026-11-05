BEGIN;
CREATE TABLE provider_rate_limit (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    blocked_until timestamptz NOT NULL DEFAULT 'epoch'
);
INSERT INTO provider_rate_limit(singleton) VALUES (true);
CREATE TABLE provider_attempts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    attempted_at timestamptz NOT NULL
);
CREATE INDEX idx_provider_attempts_time ON provider_attempts (attempted_at);
COMMIT;
