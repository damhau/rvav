-- +goose Up
-- The cutover (ADR-0027, P2.6): the vRA projects an administrator marked
-- moved to rvav, and when. New requests go to rvav from then on; vRA keeps
-- what isn't adopted yet.
create table vra_cutover (
    vra_project_id  text primary key,
    moved_at        timestamptz not null default now(),
    moved_by        text references users,
    note            text not null default ''
);

-- +goose Down
drop table vra_cutover;
