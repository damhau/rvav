-- +goose Up
-- Edit deployment (`edit`, C2.1) joins the day-2 policies that let only
-- the owner and the project admins delete a deployment or hand it over,
-- as the seed's do: without it, a member a later "*" policy allows edits
-- the deployments of others (#160).
update day2_policies
set actions = array['edit'] || actions,
    reason = case
        when reason = 'Only the owner and the project admins can delete this deployment or hand it over.'
        then 'Only the owner and the project admins can edit this deployment, delete it or hand it over.'
        else reason end
where condition = 'true'
  and (select array_agg(a order by a) from unnest(actions) a) = array['change_owner', 'change_project', 'delete']
  and ((effect = 'allow' and (select array_agg(r order by r) from unnest(roles) r) = array['owner', 'project-admin'])
       or (effect = 'deny' and roles = '{}'));

-- +goose Down
-- `edit` stays: taking it out would let members edit the deployments of
-- others again.
select 1;
