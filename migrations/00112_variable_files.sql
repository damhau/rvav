-- +goose Up
-- A shared variable may hold a text file (E2.1, ADR-0057): composite steps
-- read it as vars.<group>.<name>.text, cloud-init as {{ file "<group>.<name>" }}.
-- Its value is then the file's record, which steps read without the content.
alter table variables
    add column file_name    text,
    add column file_type    text,
    add column file_size    bigint,
    add column file_sha256  text,
    add column file_content bytea,
    add constraint variables_file_whole check (
        (file_name is null) = (file_content is null)
        and (file_name is null) = (file_type is null)
        and (file_name is null) = (file_size is null)
        and (file_name is null) = (file_sha256 is null));

-- +goose Down
alter table variables
    drop constraint variables_file_whole,
    drop column file_content,
    drop column file_sha256,
    drop column file_size,
    drop column file_type,
    drop column file_name;
