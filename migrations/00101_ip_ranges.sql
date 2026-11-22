-- +goose Up
-- Addresses rvav hands out itself, for networks EfficientIP doesn't manage
-- (ADR-0063). A network of a vCenter with at least one range takes its VMs'
-- addresses from its ranges; the others keep EfficientIP.
create table ip_ranges (
    id                 text primary key,
    name               text not null,
    description        text not null default '',
    vcenter            text not null,
    network            text not null,
    cidr               text not null,
    start_address      text not null,
    end_address        text not null,
    gateway            text not null,
    dns_servers        text[] not null default '{}',
    dns_search_domains text[] not null default '{}',
    dns_domain         text not null default '',
    excluded           text[] not null default '{}',
    created_at         timestamptz not null default now(),
    updated_at         timestamptz not null default now()
);
create index ip_ranges_network on ip_ranges (vcenter, network);

-- One row per address given: the unique keys are what keep two requests
-- from getting one address, and one adapter from getting two.
create table ip_allocations (
    id            text primary key,
    range_id      text not null references ip_ranges on delete restrict,
    address       text not null,
    request_id    text not null,
    deployment_id text not null,
    hostname      text not null,
    nic           text not null default '',
    fqdn          text not null default '',
    created_at    timestamptz not null default now(),
    unique (range_id, address),
    unique (request_id, hostname, nic)
);
create index ip_allocations_deployment on ip_allocations (deployment_id);

-- +goose Down
drop table ip_allocations;
drop table ip_ranges;
