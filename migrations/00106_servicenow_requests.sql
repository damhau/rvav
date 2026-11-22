-- +goose Up
-- Requests from ServiceNow (docs/adr/0065-servicenow.md): its token's
-- scope, the requested item each request was made for, the work notes
-- rvav owes those items, and the submissions counted a minute.
alter table api_tokens drop constraint api_tokens_scope_check;
alter table api_tokens add constraint api_tokens_scope_check check (scope in ('', 'inventory', 'servicenow'));

create table servicenow_requests (
    request_id   text primary key references requests (id) on delete cascade,
    -- One request per requested item: ServiceNow submitting again gets it.
    ritm_sys_id  text not null unique,
    ritm_number  text not null,
    -- The email or user name ServiceNow named the user by.
    user_given   text not null,
    token_id     text,
    created_at   timestamptz not null default now()
);

-- Written in the transaction that marks the request's events dispatched,
-- sent in order per requested item; the note is made from the event when
-- sent.
create table servicenow_ritm_notes (
    id           bigserial primary key,
    event_id     text not null unique,
    request_id   text not null,
    ritm_sys_id  text not null,
    due_at       timestamptz not null default now(),
    attempts     int not null default 0,
    last_error   text,
    sent_at      timestamptz
);
create index servicenow_ritm_notes_unsent_idx on servicenow_ritm_notes (ritm_sys_id, id) where sent_at is null;

create table servicenow_rate (
    minute  timestamptz primary key,
    n       int not null
);

-- +goose Down
drop table servicenow_rate;
drop table servicenow_ritm_notes;
drop table servicenow_requests;
delete from api_tokens where scope = 'servicenow';
alter table api_tokens drop constraint api_tokens_scope_check;
alter table api_tokens add constraint api_tokens_scope_check check (scope in ('', 'inventory'));
