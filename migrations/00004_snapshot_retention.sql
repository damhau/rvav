-- +goose Up
-- Snapshot retention per environment: how many a VM may have, and after how
-- many days rvav deletes them (null: never).
alter table environments add column snapshot_max_count int not null default 3 check (snapshot_max_count >= 0);
alter table environments add column snapshot_max_age_days int default 7 check (snapshot_max_age_days > 0);

-- rvav itself, as the requester of what it starts on its own (expired
-- snapshots now, expired leases later). Nobody signs in as it.
insert into users (id, subject, name, email, title, global_roles)
values ('usr_rvav', 'system:rvav', 'rvav', '', 'Retention and lease policies', '{}')
on conflict (id) do nothing;

-- +goose Down
delete from users where id = 'usr_rvav';
alter table environments drop column if exists snapshot_max_age_days, drop column if exists snapshot_max_count;
