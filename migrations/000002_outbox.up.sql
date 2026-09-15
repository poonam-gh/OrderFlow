CREATE TABLE outbox_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ
);

-- Partial index: the dispatcher only ever queries pending events, so only
-- those rows need to be indexed for that lookup.
CREATE INDEX idx_outbox_events_pending ON outbox_events (created_at)
    WHERE status = 'pending';
