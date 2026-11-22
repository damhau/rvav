-- +goose Up
-- An event audited while its destination is removed: the foreign key's
-- check waited on the removal's lock, then failed the audited action.
-- Deliveries to a destination gone are swept by the forwarder instead
-- (docs/adr/0058-audit-forwarding-and-sign-ins.md).
alter table audit_deliveries drop constraint audit_deliveries_destination_id_fkey;

-- +goose Down
delete from audit_deliveries q where not exists (select 1 from audit_destinations d where d.id = q.destination_id);
alter table audit_deliveries add constraint audit_deliveries_destination_id_fkey
    foreign key (destination_id) references audit_destinations on delete cascade;
