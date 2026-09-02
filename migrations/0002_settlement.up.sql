ALTER TABLE transfers
    ADD COLUMN settled_at     TIMESTAMPTZ,
    ADD COLUMN settlement_ref TEXT;

CREATE TABLE service_clients (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id   TEXT NOT NULL UNIQUE,
    secret_hash TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'SETTLEMENT' CHECK (role IN ('SETTLEMENT')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_transfers_status_created ON transfers(status, created_at);
