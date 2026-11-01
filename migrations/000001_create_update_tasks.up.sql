CREATE TABLE update_tasks (
    id UUID PRIMARY KEY,

    base_currency VARCHAR(3) NOT NULL,
    quote_currency VARCHAR(3) NOT NULL,

    status VARCHAR(20) NOT NULL DEFAULT 'PENDING',

    rate DOUBLE PRECISION,
    updated_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,

    error_message TEXT,

    CONSTRAINT valid_status CHECK (status IN ('PENDING', 'IN_PROGRESS', 'COMPLETED', 'FAILED')),
    CONSTRAINT valid_pair CHECK (base_currency != quote_currency),
    CONSTRAINT valid_rate CHECK (
        rate IS NULL OR (
            rate > 0 AND rate < 'Infinity'::DOUBLE PRECISION
        )
    ),

    CONSTRAINT completed_task CHECK (
        status <> 'COMPLETED' OR (
            rate IS NOT NULL
            AND updated_at IS NOT NULL
            AND completed_at IS NOT NULL
        )
    )
);

CREATE INDEX idx_update_tasks_latest
ON update_tasks (base_currency, quote_currency, updated_at DESC)
WHERE status = 'COMPLETED';
