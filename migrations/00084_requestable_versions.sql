-- +goose Up
-- An item keeps several versions requestable (docs/adr/0046-deployment-names-and-versions.md):
-- every published one; catalog_items.published_version_id is the newest, the default.
drop index if exists catalog_item_versions_one_published;
create index catalog_item_versions_requestable on catalog_item_versions (item_id, created_at desc) where state = 'published';

-- +goose Down
drop index if exists catalog_item_versions_requestable;
update catalog_item_versions v set state = 'deprecated', deprecated_at = now()
from catalog_items i
where v.item_id = i.id and v.state = 'published' and v.id is distinct from i.published_version_id;
create unique index catalog_item_versions_one_published on catalog_item_versions (item_id) where state = 'published';
