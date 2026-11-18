# ADR-0038: Name counters, property groups, secrets in templates, properties

- Status: accepted
- Date: 2026-11-18
- Extends the naming policies of [ADR-0020](0020-administration.md), the
  secrets of [ADR-0019](0019-catalog-administration.md), the blueprints of
  [ADR-0024](0024-multi-vm-blueprints.md) and the importer of
  [ADR-0027](0027-vra-importer.md). T1.5 (#138).

## Context
vRA's custom naming has a counter that starts and moves as the
administrator says; rvav's always started at 1 and moved by 1 (parity C7).

## Decision

### Name counters
- A naming policy has `start` (0 or more, 1 by default) and `increment`
  (1 to 1000, 1 by default). Migration 00058 adds `counter_start` and
  `counter_step` to `naming_policies`; existing policies keep 1 and 1. A
  blueprint's own template, without a policy, counts from 1 by 1.
- Counters stay one row per prefix or per policy in `name_counters`
  (`last_value`, the last number given). The next number is
  `greatest(last_value + increment, start)`: a new counter starts at
  `start`, one in use goes on from its last number, and jumps to `start`
  when an administrator raises it past that number. Lowering the start
  never goes back to numbers given before. The upsert is the same single
  statement as before, so names stay unique under concurrent requests;
  the names passed over (taken in vCenter, DNS or rvav) are skipped by
  `increment` too.
- The policy's preview and a request's look-ahead (the DNS prefetch) count
  the same way.
- A number wider than `{seq:NN}`'s padding is printed whole (`web1000`).
