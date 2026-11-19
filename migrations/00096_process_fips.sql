-- +goose Up
-- The FIPS 140-3 mode each process started in, and the Go Cryptographic
-- Module it was built with, for Administration › System (ADR-0060).
alter table processes add column fips_mode text not null default 'off' check (fips_mode in ('off', 'on', 'only'));
alter table processes add column fips_module text not null default '';

-- +goose Down
alter table processes drop column fips_module;
alter table processes drop column fips_mode;
