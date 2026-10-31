-- +goose Up
-- Trusted certificates (docs/adr/0030-settings-and-local-administrator.md):
-- the CA bundle every connection trusts besides the system's CAs, set in
-- Administration, and a certificate pinned for a vCenter added besides the
-- integration's.
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check
    check (kind in ('oidc', 'vcenter', 'console', 'awx', 'efficientip', 'nsx', 'git', 'vra', 'smtp', 'teams', 'slack',
                    'hosts', 'trust'));
alter table vcenters add column cert_sha256 text not null default '';

-- +goose Down
alter table vcenters drop column cert_sha256;
delete from integration_settings where kind = 'trust';
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check
    check (kind in ('oidc', 'vcenter', 'console', 'awx', 'efficientip', 'nsx', 'git', 'vra', 'smtp', 'teams', 'slack',
                    'hosts'));
