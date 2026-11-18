-- +goose Up
-- Images per vCenter (ADR-0037): a catalogue template has one source per
-- vCenter, a VM template's name or a content library's item. vcenter is
-- '' for the vCenter integration's, else one added in vCenters.
create table vm_template_sources (
    template_key    text not null references vm_templates (key) on delete cascade,
    vcenter         text not null,
    template        text not null,
    content_library text not null default '',
    primary key (template_key, vcenter)
);

-- Each template where vCenter has it now, else on the vCenter integration's.
insert into vm_template_sources (template_key, vcenter, template, content_library)
select distinct t.key, case when exists (select 1 from vcenters v where v.name = i.vcenter) then i.vcenter else '' end,
    t.vcenter_template, t.content_library
from vm_templates t
join inv_templates i on i.name = t.vcenter_template and i.library = t.content_library
on conflict do nothing;

insert into vm_template_sources (template_key, vcenter, template, content_library)
select t.key, '', t.vcenter_template, t.content_library
from vm_templates t
where not exists (select 1 from vm_template_sources s where s.template_key = t.key);

alter table vm_templates drop column vcenter_template, drop column content_library;

-- Its own cloud-init user data and constraint tags, merged with the
-- blueprint's, and whether its VMs are linked clones.
alter table vm_templates add column user_data text not null default '',
    add column constraints jsonb not null default '{}',
    add column linked_clone boolean not null default false;

-- +goose Down
alter table vm_templates drop column user_data, drop column constraints, drop column linked_clone;
alter table vm_templates add column vcenter_template text not null default '',
    add column content_library text not null default '';

update vm_templates t set vcenter_template = s.template, content_library = s.content_library
from (select distinct on (template_key) template_key, template, content_library
      from vm_template_sources order by template_key, vcenter) s
where s.template_key = t.key;

drop table vm_template_sources;
