package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/store"
)

// Setup is the setup checklist (ADR-0030): the steps that set rvav up from
// its UI, each done or not, with what there is now.
func (s *Service) Setup(ctx context.Context, p *auth.Principal) (api.Setup, error) {
	if !p.IsAdmin() {
		return api.Setup{}, ErrForbidden
	}
	st, err := s.Store.SetupState(ctx, LocalAdminUsername)
	if err != nil {
		return api.Setup{}, err
	}
	its, err := s.Integrations(ctx, p)
	if err != nil {
		return api.Setup{}, err
	}
	byKind := map[string]api.Integration{}
	for _, it := range its {
		byKind[string(it.Kind)] = it
	}
	connection := func(key api.SetupStepKey, kind string) api.SetupStep {
		it := byKind[kind]
		switch {
		case config.Fake(kind):
			return api.SetupStep{Key: key, Done: true, Detail: "The development fake (RVAV_FAKE_" + strings.ToUpper(kind) + ")."}
		case it.Configured:
			return api.SetupStep{Key: key, Done: true, Detail: address(kind, it.Settings)}
		}
		return api.SetupStep{Key: key, Detail: "Not set."}
	}

	password := api.SetupStep{Key: api.SetupStepKeyPassword, Done: st.PasswordChanged,
		Detail: "The install's password is still the administrator's."}
	if st.PasswordChanged {
		password.Detail = "Changed from the install's."
	}
	provider := connection(api.SetupStepKeyProvider, "oidc")
	if !provider.Done {
		provider.Detail = "None: only the local administrator can sign in."
	}
	group := api.SetupStep{Key: api.SetupStepKeyAdminGroup, Done: len(st.AdminGroups) > 0,
		Detail: "No group is mapped to admin."}
	if group.Done {
		group.Detail = strings.Join(st.AdminGroups, ", ")
	}
	// A project with nowhere to place its VMs can't request any.
	projects := api.SetupStep{Key: api.SetupStepKeyProjects, Done: st.Environments > 0 && st.Projects > 0 && st.Placements > 0,
		Detail: plural(st.Environments, "environment") + ", " + plural(st.Projects, "project") + ", " +
			plural(st.Placements, "placement rule")}
	item := api.SetupStep{Key: api.SetupStepKeyCatalogItem, Done: st.PublishedItems > 0,
		Detail: fmt.Sprintf("%s, %d published", plural(st.Items, "item"), st.PublishedItems)}

	out := api.Setup{Hidden: st.Hidden, Steps: []api.SetupStep{password, provider, group,
		connection(api.SetupStepKeyVcenter, "vcenter"), projects,
		connection(api.SetupStepKeyIpam, "efficientip"), connection(api.SetupStepKeyAwx, "awx"), item}}
	out.Complete = true
	for _, step := range out.Steps {
		out.Complete = out.Complete && step.Done
	}
	out.Open = !out.Complete && !st.Hidden && !st.Seeded
	return out, nil
}

// SetSetup hides the checklist, or has it open at sign-in again.
func (s *Service) SetSetup(ctx context.Context, p *auth.Principal, w api.SetupWrite) (api.Setup, error) {
	if !p.IsAdmin() {
		return api.Setup{}, ErrForbidden
	}
	action := "setup.shown"
	if w.Hidden {
		action = "setup.hidden"
	}
	if err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.Store.SetSetupHidden(ctx, tx, w.Hidden, p.User.ID); err != nil {
			return err
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: action, TargetType: "setup",
			TargetID: "checklist", Detail: map[string]any{}})
	}); err != nil {
		return api.Setup{}, err
	}
	return s.Setup(ctx, p)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}
