-- +goose Up
-- NSX (ADR-0028): its settings are saved from Administration ›
-- Integrations. The kinds were never widened for the notification channels
-- (S11): saving email, Teams or Slack settings failed. All of them now.
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check
    check (kind in ('vcenter', 'awx', 'efficientip', 'nsx', 'smtp', 'teams', 'slack'));

-- +goose Down
delete from integration_settings where kind not in ('vcenter', 'awx', 'efficientip');
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check check (kind in ('vcenter', 'awx', 'efficientip'));
