-- +goose Up
-- Updates of a deployment to its item's version and new inputs (ADR-0042).
alter table requests drop constraint requests_kind_check;
alter table requests add constraint requests_kind_check check (kind in ('provision', 'day2', 'update'));

-- +goose Down
update requests set kind = 'day2' where kind = 'update';
alter table requests drop constraint requests_kind_check;
alter table requests add constraint requests_kind_check check (kind in ('provision', 'day2'));
