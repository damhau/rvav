-- +goose Up
-- Pictures catalog administrators upload for image fields (ADR-0040), which
-- show them as upload:<id>. Forms are read by everyone who may request, so
-- these are too.
create table form_images (
    id           text primary key,
    name         text not null,
    content_type text not null,
    size         bigint not null check (size > 0),
    sha256       text not null,
    content      bytea not null,
    created_by   text references users on delete set null,
    created_at   timestamptz not null default now()
);

-- +goose Down
drop table form_images;
