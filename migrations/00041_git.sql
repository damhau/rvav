-- +goose Up
-- The catalog's Git repository is an integration (docs/adr/0029-catalog-in-git.md).
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check
    check (kind in ('vcenter', 'awx', 'efficientip', 'nsx', 'git', 'smtp', 'teams', 'slack'));

-- +goose Down
delete from integration_settings where kind = 'git';
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check
    check (kind in ('vcenter', 'awx', 'efficientip', 'nsx', 'smtp', 'teams', 'slack'));
