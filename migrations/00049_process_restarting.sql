-- +goose Up
-- A process that exited to apply newer settings is kept, "restarting", until
-- its supervisor has started it again (its successor, of the same role and
-- host, records itself) or for 10 minutes: until then a change is not
-- applied, and the other APIs don't drain (ADR-0030).
alter table processes drop constraint processes_state_check;
alter table processes add constraint processes_state_check check (state in ('running', 'draining', 'restarting'));

-- +goose Down
delete from processes where state = 'restarting';
alter table processes drop constraint processes_state_check;
alter table processes add constraint processes_state_check check (state in ('running', 'draining'));
