package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/store"
)

// The most characters of the name a requester gives their deployment and
// of what they say it is for (ADR-0019).
const (
	maxDeploymentName        = 80
	maxDeploymentDescription = 2000
)

// MakesDeployments says a version's blueprint makes deployments, which a
// request may name: a VM item, or a composite with a VM step.
func (s *Service) MakesDeployments(ctx context.Context, raw json.RawMessage) (bool, error) {
	b, err := blueprint.Parse(raw)
	if err != nil {
		return false, nil
	}
	switch b.Kind {
	case blueprint.KindVM:
		return true, nil
	case blueprint.KindComposite:
		steps, _ := flattenSteps(b.Steps)
		for _, st := range steps {
			if st.Item == "" {
				continue
			}
			item, err := s.Store.GetItem(ctx, st.Item, "")
			if errors.Is(err, store.ErrNotFound) || err == nil && item.PublishedVersionID == nil {
				continue
			} else if err != nil {
				return false, err
			}
			v, err := s.Store.GetVersion(ctx, *item.PublishedVersionID)
			if err != nil {
				return false, err
			}
			if sb, err := blueprint.Parse(v.Blueprint); err == nil && sb.Kind == blueprint.KindVM {
				return true, nil
			}
		}
	}
	return false, nil
}

// deploymentNameErr says what is wrong with a name given to a deployment,
// at field: too long, or not on one line.
func deploymentNameErr(field, name string) *api.FieldError {
	switch {
	case name == "":
		e := fieldErr(field, "required", "Give the deployment a name.")
		return &e
	case utf8.RuneCountInString(name) > maxDeploymentName:
		e := fieldErr(field, "too_long", fmt.Sprintf("%d characters at most.", maxDeploymentName))
		return &e
	case strings.ContainsFunc(name, unicode.IsControl):
		e := fieldErr(field, "invalid", "One line, without tabs.")
		return &e
	}
	return nil
}

// evaluateDeployments checks the name and description a request gives its
// deployments: a name not too long, not a VM name the request's naming
// gives, and not one the project's live deployments hold.
func (s *Service) evaluateDeployments(ctx context.Context, ev *Evaluation, d api.RequestDraft) error {
	ev.DeploymentName = strings.TrimSpace(deref(d.DeploymentName))
	ev.Description = strings.TrimSpace(deref(d.DeploymentDescription))
	if ev.DeploymentName == "" && ev.Description == "" {
		return nil
	}
	add := func(field, code, msg string) { ev.Errors = append(ev.Errors, fieldErr(field, code, msg)) }
	makes, err := s.MakesDeployments(ctx, ev.Version.Blueprint)
	if err != nil {
		return err
	}
	if !makes {
		for field, given := range map[string]bool{"/deployment_name": ev.DeploymentName != "", "/deployment_description": ev.Description != ""} {
			if given {
				add(field, "no_deployment", ev.Item.Name+" makes no deployment.")
			}
		}
		ev.DeploymentName, ev.Description = "", ""
		return nil
	}
	if utf8.RuneCountInString(ev.Description) > maxDeploymentDescription {
		add("/deployment_description", "too_long", fmt.Sprintf("%d characters at most.", maxDeploymentDescription))
	}
	if ev.DeploymentName == "" {
		return nil
	}
	if e := deploymentNameErr("/deployment_name", ev.DeploymentName); e != nil {
		ev.Errors = append(ev.Errors, *e)
		return nil
	}
	ev.DeploymentNames = numbered(ev.DeploymentName, ev.deploymentCount())
	if e := ev.vmNamed(ev.DeploymentNames); e != "" {
		add("/deployment_name", "naming", e)
		return nil
	}
	taken, err := s.Store.TakenDeploymentNames(ctx, s.Store.Pool, ev.Project.ID, "", ev.DeploymentNames)
	if err != nil {
		return err
	}
	if len(taken) > 0 {
		add("/deployment_name", "taken", takenMsg(ev.Project.Name, taken[0]))
	}
	return nil
}

func takenMsg(project, name string) string {
	return fmt.Sprintf("%s already has a deployment named %s.", project, name)
}

// numbered are the names of n deployments a request names name: name
// itself for one, else name followed by 1, 2 and so on.
func numbered(name string, n int) []string {
	if n <= 1 {
		return []string{name}
	}
	out := make([]string, n)
	for i := range out {
		out[i] = name + " " + strconv.Itoa(i+1)
	}
	return out
}

// deploymentCount is how many deployments the request makes: one per VM
// of a single-VM item, one for a multi-VM item, those of a composite's VM
// steps.
func (ev *Evaluation) deploymentCount() int {
	switch ev.Blueprint.Kind {
	case blueprint.KindVM:
		if ev.Blueprint.Multi() {
			return 1
		}
		n := 0
		for _, p := range ev.Parts {
			n += p.count
		}
		return max(n, 1)
	case blueprint.KindComposite:
		n := 0
		for _, part := range ev.Composite {
			if part.sub != nil {
				n += part.sub.deploymentCount()
			}
		}
		return n
	}
	return 0
}

// vmNamed says which of names the request's VM naming gives (its prefix
// and a number): a single VM's deployment has its VM's name, and rvav
// never gives a VM name twice (ADR-0019).
func (ev *Evaluation) vmNamed(names []string) string {
	parts := slices.Clone(ev.Parts)
	for _, c := range ev.Composite {
		if c.sub != nil {
			parts = append(parts, c.sub.Parts...)
		}
	}
	for _, p := range parts {
		if p.naming == nil || p.prefix == "" {
			continue
		}
		for _, n := range names {
			if seq, ok := strings.CutPrefix(strings.ToLower(n), p.prefix); ok && seq != "" && strings.Trim(seq, "0123456789") == "" {
				return fmt.Sprintf("%s is a VM name: the naming policy gives %s and a number. Choose another.", n, p.prefix)
			}
		}
	}
	return ""
}

// claimDeploymentNames holds the names of a request's deployments in its
// project until it is stored, and gives them the name and description
// the request asked for. A name it asked for that another deployment took
// since is refused. One rvav gave that is taken (a blueprint's
// deployment_name) gets the request number, and one given twice in the
// request (two steps of an item) a number.
func (s *Service) claimDeploymentNames(ctx context.Context, tx pgx.Tx, ev *Evaluation, deps []store.Deployment, number string) error {
	if ev.DeploymentName != "" {
		for i, n := range numbered(ev.DeploymentName, len(deps)) {
			deps[i].Name = n
		}
	}
	names := make([]string, len(deps))
	for i := range deps {
		deps[i].Description = ev.Description
		names[i] = deps[i].Name
	}
	if err := s.Store.LockDeploymentNames(ctx, tx, ev.Project.ID, names); err != nil {
		return err
	}
	taken, err := s.Store.TakenDeploymentNames(ctx, tx, ev.Project.ID, "", names)
	if err != nil {
		return err
	}
	if len(taken) > 0 && ev.DeploymentName != "" {
		return &ValidationError{Errors: []api.FieldError{fieldErr("/deployment_name", "taken", takenMsg(ev.Project.Name, taken[0]))}}
	}
	held := map[string]bool{}
	for _, n := range taken {
		held[strings.ToLower(n)] = true
	}
	used := map[string]bool{}
	for i := range deps {
		switch k := strings.ToLower(deps[i].Name); {
		case held[k]:
			deps[i].Name, err = s.freeDeploymentName(ctx, tx, ev.Project.ID, deps[i].Name+" "+number, 1, used)
		case used[k]:
			deps[i].Name, err = s.freeDeploymentName(ctx, tx, ev.Project.ID, deps[i].Name, 2, used)
		}
		if err != nil {
			return err
		}
		used[strings.ToLower(deps[i].Name)] = true
	}
	return nil
}

// freeDeploymentName is name followed by the first number from from that
// makes it free in the project and not used, held until the transaction
// ends; name alone stands for 1.
func (s *Service) freeDeploymentName(ctx context.Context, tx pgx.Tx, projectID, name string, from int, used map[string]bool) (string, error) {
	for i := from; ; i++ {
		candidate := name
		if i > 1 {
			candidate = name + " " + strconv.Itoa(i)
		}
		if used[strings.ToLower(candidate)] {
			continue
		}
		if err := s.Store.LockDeploymentNames(ctx, tx, projectID, []string{candidate}); err != nil {
			return "", err
		}
		taken, err := s.Store.TakenDeploymentNames(ctx, tx, projectID, "", []string{candidate})
		if err != nil || len(taken) == 0 {
			return candidate, err
		}
	}
}
