DROP INDEX IF EXISTS idx_transfers_status_created;
DROP TABLE IF EXISTS service_clients;
ALTER TABLE transfers
    DROP COLUMN IF EXISTS settlement_ref,
    DROP COLUMN IF EXISTS settled_at;
