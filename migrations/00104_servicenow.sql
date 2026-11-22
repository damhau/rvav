-- +goose Up
-- ServiceNow (docs/adr/0065-servicenow.md): the instance rvav keeps CIs in
-- and takes requests from, an integration of its own.
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check
    check (kind in ('oidc', 'vcenter', 'console', 'awx', 'efficientip', 'nsx', 'git', 'vra', 'ad', 'servicenow', 'smtp',
                    'teams', 'slack', 'hosts', 'trust'));

-- +goose Down
delete from integration_settings where kind = 'servicenow';
alter table integration_settings drop constraint integration_settings_kind_check;
alter table integration_settings add constraint integration_settings_kind_check
    check (kind in ('oidc', 'vcenter', 'console', 'awx', 'efficientip', 'nsx', 'git', 'vra', 'ad', 'smtp', 'teams',
                    'slack', 'hosts', 'trust'));
