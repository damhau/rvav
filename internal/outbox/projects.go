package outbox

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/store"
)

// ForProject writes the topic's event about a project (ADR-0049): the
// project as it is now, with its admins, who changed it (`actor`, null for
// rvav) and the settings it changed (`changes`, empty on project.created).
func ForProject(ctx context.Context, db store.DB, topic, projectID, actorID string, changes []string) error {
	var project []byte
	err := db.QueryRow(ctx, `select jsonb_build_object('id', p.id, 'code', p.code, 'name', p.name, 'description', p.description,
			'cost_center', p.cost_center, 'environments', p.environments, 'properties', p.properties,
			'archived', p.archived_at is not null, 'deployment_sharing', p.deployment_sharing,
			'admins', coalesce((select jsonb_agg(jsonb_build_object('id', u.id, 'name', u.name, 'email', u.email) order by u.name, u.id)
				from project_members m join users u on u.id = m.user_id where m.project_id = p.id and m.role = 'project-admin'), '[]'))
		from projects p where p.id = $1`, projectID).Scan(&project)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	} else if err != nil {
		return err
	}
	var actor any
	if actorID != "" {
		by, err := people(ctx, db, []string{actorID})
		if err != nil {
			return err
		}
		if len(by) > 0 {
			actor = by[0]
		}
	}
	data := projectData(raw(project), actor, nonNil(changes))
	return insert(ctx, db, topic, nil, nil, topic+":"+projectID+":"+ids.New(ids.Event), data, false)
}

// projectData is a project event's data: no request or deployment.
func projectData(project map[string]any, actor any, changes []string) map[string]any {
	return map[string]any{"request": nil, "item": nil, "project": project, "environment": "", "production": false,
		"requester": nil, "owner": nil, "inputs": map[string]any{}, "vm": nil,
		"deployment": nil, "deployments": []any{}, "resources": []any{}, "actor": actor, "changes": changes}
}

func sampleProject(topic string) map[string]any {
	project := map[string]any{"id": "prj_sample", "code": "web", "name": "Web Platform", "description": "The web shop's servers.",
		"cost_center": "CC-0000", "environments": []any{"dev", "prd"}, "properties": map[string]any{"cmdb_ci": "CI-0042"},
		"archived": false, "deployment_sharing": "all_members",
		"admins": []any{map[string]any{"id": "usr_admin", "name": "Sample Admin", "email": "admin@example.com"}}}
	changes := []string{}
	if topic == ProjectUpdated {
		changes = []string{"cost_center"}
	}
	return projectData(project, map[string]any{"id": "usr_admin", "name": "Sample Admin", "email": "admin@example.com"}, changes)
}
