-- +goose Up
-- A naming policy's counter starts at counter_start and moves by
-- counter_step (ADR-0038); existing policies keep 1 and 1.
alter table naming_policies
    add column counter_start int not null default 1 check (counter_start >= 0),
    add column counter_step  int not null default 1 check (counter_step between 1 and 1000);

-- +goose Down
alter table naming_policies drop column counter_start, drop column counter_step;
