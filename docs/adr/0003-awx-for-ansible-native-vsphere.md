# ADR-0003: Native vSphere operations, AWX for Ansible

- Status: accepted
- Date: 2026-10-26

## Context
We need VM provisioning, day-2 operations, and Ansible automation. Options were:
do everything with Ansible (through AWX or embedded), or split the work.

## Decision
- VM provisioning and day-2 operations are implemented **natively** in the app
  with govmomi.
- All Ansible automation runs through **AWX** job templates and workflow job
  templates, launched through the AWX API.
- AWX gets hosts from rvav through a **dynamic inventory plugin** that calls
  the rvav API.

## Consequences
- Fast, precise and observable vSphere operations with good error handling and
  timelines; no playbook needed for a power-off.
- AWX keeps doing what it's good at: credentials, execution environments,
  scaling, and the Ansible content people already have.
- Job templates must allow prompts on launch for limit and variables.
- Before a job targets a new VM, the AWX inventory must be refreshed; the
  workflow handles this explicitly.
