-- +goose Up
-- Subscriptions that change the request (docs/adr/0054-subscriptions-that-change-the-request.md):
-- a VM's compute.allocation.pre and network.configure may block, a blocking
-- subscription may name a recovery target, and a run keeps what its answer
-- changed on the VM.
alter table subscriptions drop constraint subscriptions_check;
alter table subscriptions add constraint subscriptions_check
    check (not blocking or topic like '%.pre' or topic = 'network.configure');
-- Same shape as target; told once when the blocking subscription fails.
alter table subscriptions add column recovery jsonb;
alter table subscriptions add constraint subscriptions_recovery_check check (recovery is null or blocking);

alter table subscription_runs add column recovery boolean not null default false;
alter table subscription_runs add column changes jsonb;
alter table subscription_runs drop constraint subscription_runs_subscription_id_event_id_key;
alter table subscription_runs add constraint subscription_runs_subscription_id_event_id_key
    unique (subscription_id, event_id, recovery);

-- +goose Down
alter table subscription_runs drop constraint subscription_runs_subscription_id_event_id_key;
delete from subscription_runs where recovery;
alter table subscription_runs add constraint subscription_runs_subscription_id_event_id_key
    unique (subscription_id, event_id);
alter table subscription_runs drop column changes;
alter table subscription_runs drop column recovery;
alter table subscriptions drop constraint subscriptions_recovery_check;
alter table subscriptions drop column recovery;
update subscriptions set blocking = false where topic not like '%.pre';
alter table subscriptions drop constraint subscriptions_check;
alter table subscriptions add constraint subscriptions_check check (not blocking or topic like '%.pre');
