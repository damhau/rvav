-- +goose Up
-- Audit forwarding (docs/adr/0058-audit-forwarding-and-sign-ins.md). An
-- event a destination refuses for good, such as a webhook's 413, waits
-- aside with why, and the events after it go; saving the destination
-- tries it again.
alter table audit_deliveries add column set_aside_at timestamptz,
    add column set_aside_reason text;

-- A webhook's authentication header's value, sealed as integrations'
-- passwords are (ADR-0019).
alter table audit_destinations add column auth_key_id text not null default '',
    add column auth_nonce bytea,
    add column auth_ciphertext bytea,
    add check ((auth_ciphertext is null) = (auth_nonce is null));

-- +goose Down
alter table audit_destinations drop column auth_ciphertext, drop column auth_nonce, drop column auth_key_id;
alter table audit_deliveries drop column set_aside_reason, drop column set_aside_at;
