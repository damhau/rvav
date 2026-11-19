-- +goose Up
-- Notification templates administrators edit, and the portal's branding
-- (docs/adr/0059-notification-templates-and-branding.md).
create table notification_templates (
    kind       text not null,
    channel    text not null check (channel in ('email', 'teams', 'slack')),
    locale     text not null,
    subject    text not null,
    text_body  text not null,
    -- Emails only.
    html_body  text not null default '',
    updated_at timestamptz not null default now(),
    updated_by text references users on delete set null,
    primary key (kind, channel, locale)
);

-- One row: the portal's name, accent and logos.
create table branding (
    id                boolean primary key default true check (id),
    name              text not null default '',
    accent            text not null default '',
    logo_light        bytea,
    logo_light_type   text,
    logo_dark         bytea,
    logo_dark_type    text,
    updated_at        timestamptz not null default now(),
    updated_by        text references users on delete set null
);

-- +goose Down
drop table branding;
drop table notification_templates;
