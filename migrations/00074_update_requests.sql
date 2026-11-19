-- +goose Up
-- Updates of a deployment to its item's version and new inputs (ADR-0042).
alter table requests drop constraint requests_kind_check;
alter table requests add constraint requests_kind_check check (kind in ('provision', 'day2', 'update'));

-- +goose Down
-- An update made a day-2 request would have no action: refuse instead.
-- +goose StatementBegin
do $$
begin
  if exists (select 1 from requests where kind = 'update') then
    raise exception 'requests of kind update exist: rvav before updates (00073) cannot read them';
  end if;
end
$$;
-- +goose StatementEnd
alter table requests drop constraint requests_kind_check;
alter table requests add constraint requests_kind_check check (kind in ('provision', 'day2'));
