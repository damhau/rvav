package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/config"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/servicenow"
	"github.com/damhau/rvav/internal/store"
)

// ServiceNow's token (ADR-0065): rvav makes it for a user of its own, with
// no role, whose one permission, auth.ServiceNowRequest, only a token of
// the scope servicenow holds.
const (
	serviceNowUserSubject = "system:servicenow"
	serviceNowUserName    = "ServiceNow (rvav)"
	serviceNowTokenName   = "ServiceNow"
	serviceNowTokenDays   = 365
	// readsPerSubmission: each process takes this many reads for each
	// submission the limit allows, for the catalog form's lookups.
	readsPerSubmission = 10
	// serviceNowNameMax is the longest email or user name ServiceNow
	// names a person by.
	serviceNowNameMax = 254
)

// ServiceNow's sys_ids and requested items' numbers, as the API documents
// them: a sys_id is stored, and named in paths rvav and ServiceNow build.
var (
	serviceNowSysID  = regexp.MustCompile(`^[0-9a-f]{32}$`)
	serviceNowNumber = regexp.MustCompile(`^[A-Z]{2,8}[0-9]{4,12}$`)
)

// checkServiceNowDraft refuses a submission the API's schema doesn't take.
func checkServiceNowDraft(d api.ServiceNowRequestDraft) error {
	var errs []api.FieldError
	if !serviceNowSysID.MatchString(d.RitmSysId) {
		errs = append(errs, fieldErr("/ritm_sys_id", "pattern", "A sys_id is 32 lower-case hexadecimal digits."))
	}
	if !serviceNowNumber.MatchString(d.RitmNumber) {
		errs = append(errs, fieldErr("/ritm_number", "pattern",
			"A requested item's number is 2 to 8 capital letters, then 4 to 12 digits, for example RITM0010045."))
	}
	for _, f := range []struct{ ptr, v string }{{"/user", d.User}, {"/ordered_by", d.OrderedBy}} {
		switch {
		case strings.TrimSpace(f.v) == "":
			errs = append(errs, fieldErr(f.ptr, "required", "An email or a user name."))
		case len(f.v) > serviceNowNameMax:
			errs = append(errs, fieldErr(f.ptr, "too_long", fmt.Sprintf("At most %d characters.", serviceNowNameMax)))
		}
	}
	if d.Justification != nil && len([]rune(*d.Justification)) > 2000 {
		errs = append(errs, fieldErr("/justification", "too_long", "At most 2000 characters."))
	}
	if len(errs) > 0 {
		return &ValidationError{Errors: errs}
	}
	return nil
}

// RateLimitedError: more calls this minute than the limit; RetryAfter is
// the wait until the next minute.
type RateLimitedError struct {
	RetryAfter time.Duration
	Msg        string
}

func (e *RateLimitedError) Error() string { return e.Msg }

// minuteCount counts calls in the current minute of the clock.
type minuteCount struct {
	mu     sync.Mutex
	minute time.Time
	n      int
}

func (c *minuteCount) take(now time.Time, limit int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m := now.Truncate(time.Minute); !m.Equal(c.minute) {
		c.minute, c.n = m, 0
	}
	if c.n >= limit {
		return false
	}
	c.n++
	return true
}

// ServiceNowSourceAllowed says whether the caller's address is one
// ServiceNow's calls may come from: any when none is set.
func (s *Service) ServiceNowSourceAllowed(ctx context.Context) bool {
	allowed := s.liveConfig(ctx).ServiceNow.AllowedSources
	if len(allowed) == 0 {
		return true
	}
	prefixes, err := config.ParsePrefixes(allowed)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(auth.ClientFrom(ctx).IP)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(prefixes, func(p netip.Prefix) bool { return p.Contains(ip.Unmap()) })
}

func (s *Service) serviceNowLimit(ctx context.Context, p *auth.Principal, submission bool) error {
	limit := s.liveConfig(ctx).ServiceNow.RequestsPerMinute
	if limit <= 0 {
		limit = 30
	}
	now := time.Now()
	if submission {
		n, err := s.Store.CountServiceNowSubmission(ctx, now)
		if err != nil || n <= limit {
			return err
		}
	} else if s.snowReads.take(now, limit*readsPerSubmission) {
		return nil
	}
	// Once a minute in each process at most, so that a flood doesn't fill
	// the audit log.
	if s.snowLimited.take(now, 1) {
		s.audit(ctx, p, "servicenow.request_limited", "integration", "servicenow", nil,
			map[string]any{"submission": submission, "limit": limit})
	}
	return &RateLimitedError{RetryAfter: now.Truncate(time.Minute).Add(time.Minute).Sub(now),
		Msg: fmt.Sprintf("ServiceNow may submit %d requests a minute: try again in a moment.", limit)}
}

func serviceNowCaller(p *auth.Principal) error {
	if !p.Holds(auth.ServiceNowRequest) {
		return fmt.Errorf("%w: only ServiceNow's token is taken here", ErrForbidden)
	}
	return nil
}

// ServiceNowCatalog is every item ServiceNow's catalog may offer: each
// published and shared in a project, with the inputs a ServiceNow variable
// can hold.
func (s *Service) ServiceNowCatalog(ctx context.Context, p *auth.Principal) (api.ServiceNowCatalog, error) {
	if err := serviceNowCaller(p); err != nil {
		return api.ServiceNowCatalog{}, err
	}
	if err := s.serviceNowLimit(ctx, p, false); err != nil {
		return api.ServiceNowCatalog{}, err
	}
	items, err := s.Store.ServiceNowCatalogItems(ctx)
	if err != nil {
		return api.ServiceNowCatalog{}, err
	}
	out := api.ServiceNowCatalog{Items: make([]api.ServiceNowCatalogItem, 0, len(items))}
	for _, it := range items {
		v, err := s.Store.GetVersion(ctx, it.VersionID)
		if err != nil {
			return out, err
		}
		item := api.ServiceNowCatalogItem{Id: it.ID, Slug: it.Slug, Name: it.Name, Description: nonEmpty(it.Summary),
			Category: nonEmpty(it.Category), Type: it.Type, VersionId: it.VersionID, Version: it.VersionNumber,
			Inputs: ServiceNowInputs(v.FormSchema, v.UISchema, v.InitialValues)}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// Widgets whose values rvav finds at request time, from vCenter, AWX, the
// IPAM or its own records.
var dynamicWidgets = []api.UiWidget{api.UiWidgetAwxCredential, api.UiWidgetAwxInventory, api.UiWidgetDeployment,
	api.UiWidgetDnsZone, api.UiWidgetIpamAddress, api.UiWidgetIpamSubnet, api.UiWidgetProject, api.UiWidgetSearch,
	api.UiWidgetUser, api.UiWidgetVsphereCluster, api.UiWidgetVsphereDatastore, api.UiWidgetVsphereFolder,
	api.UiWidgetVsphereNetwork, api.UiWidgetVsphereTemplate, api.UiWidgetVsphereVm}

// Widgets with no value to give: shown, computed or uploaded.
var skippedWidgets = []api.UiWidget{api.UiWidgetComputed, api.UiWidgetFile, api.UiWidgetImage, api.UiWidgetInfo,
	api.UiWidgetLink, api.UiWidgetSecret}

// ServiceNowInputs are the inputs of a form a ServiceNow variable can
// hold, in the form's order: strings, numbers, booleans and choices.
// Secrets are left out: ServiceNow would keep them in the clear.
func ServiceNowInputs(formSchema, uiSchema, initialValues json.RawMessage) []api.ServiceNowInput {
	var schema struct {
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	_ = json.Unmarshal(formSchema, &schema)
	var ui api.UiSchema
	_ = json.Unmarshal(uiSchema, &ui)
	start := startOf(formSchema, initialValues)

	var order []string
	for _, sec := range ui.Layout.Sections {
		for _, f := range sec.Fields {
			if _, ok := schema.Properties[f]; ok && !slices.Contains(order, f) {
				order = append(order, f)
			}
		}
	}
	var rest []string
	for name := range schema.Properties {
		if !slices.Contains(order, name) {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)

	out := []api.ServiceNowInput{}
	for _, name := range append(order, rest...) {
		prop, field := schema.Properties[name], ui.Fields[name]
		kind, _ := prop["type"].(string)
		typ := api.ServiceNowInputType(kind)
		if !typ.Valid() || slices.Contains(skippedWidgets, field.Widget) || (field.Secret != nil && *field.Secret) {
			continue
		}
		in := api.ServiceNowInput{Name: name, Label: field.Label, Type: typ, Required: slices.Contains(schema.Required, name),
			Help: field.Help, Dynamic: field.ValueSource != nil || slices.Contains(dynamicWidgets, field.Widget)}
		if in.Label == "" {
			in.Label, _ = prop["title"].(string)
		}
		if in.Label == "" {
			in.Label = name
		}
		if v, ok := start[name]; ok {
			in.Default = v
		}
		var opts []api.ServiceNowChoice
		switch {
		case in.Dynamic:
		case field.Options != nil:
			for _, o := range *field.Options {
				opts = append(opts, api.ServiceNowChoice{Value: fmt.Sprint(o.Value), Label: o.Label})
			}
		default:
			if enum, ok := prop["enum"].([]any); ok {
				for _, e := range enum {
					opts = append(opts, api.ServiceNowChoice{Value: fmt.Sprint(e), Label: fmt.Sprint(e)})
				}
			}
		}
		if len(opts) > 0 {
			in.Options = &opts
		}
		out = append(out, in)
	}
	return out
}

// serviceNowUser finds the user ServiceNow names at field, by email or by
// user name, as a principal of their own, with the groups of their last
// sign-in: refused when that sign-in is older than
// RVAV_SCHEDULE_OWNER_IDLE_DAYS, as a schedule's run is, since the
// groups may no longer be theirs.
func (s *Service) serviceNowUser(ctx context.Context, given, field string) (*auth.Principal, error) {
	given = strings.TrimSpace(given)
	users, err := s.Store.ServiceNowUsersMatching(ctx, given)
	if err != nil {
		return nil, err
	}
	switch {
	case len(users) == 0:
		return nil, &unknownUserError{given: given}
	case len(users) > 1:
		return nil, &ValidationError{Errors: []api.FieldError{fieldErr(field, "ambiguous",
			fmt.Sprintf("More than one rvav user has the user name %q: ServiceNow must send the email.", given))}}
	}
	u := users[0]
	if idle := s.Config.ScheduleOwnerIdleDays; idle > 0 && time.Since(u.SignedInAt) > time.Duration(idle)*24*time.Hour {
		return nil, &ValidationError{Errors: []api.FieldError{fieldErr(field, "idle", fmt.Sprintf(
			"%s hasn't signed in to rvav for %d days: rvav doesn't request with the groups of a sign-in that old. "+
				"They sign in to rvav once, then order again.", u.Name, idle))}}
	}
	return auth.ForUser(ctx, s.Store, u.ID)
}

// unknownUserError: no user ServiceNow may request for has this email or
// user name.
type unknownUserError struct{ given string }

func (e *unknownUserError) Error() string {
	return fmt.Sprintf("no rvav user has the email or user name %q", e.given)
}

// serviceNowOrderer is who placed the order: the user it's for when
// ServiceNow names the same person, else someone else, who must be
// known to rvav.
func (s *Service) serviceNowOrderer(ctx context.Context, up *auth.Principal, d api.ServiceNowRequestDraft) (*auth.Principal, error) {
	given := strings.TrimSpace(d.OrderedBy)
	switch {
	case given == "":
		return nil, &ValidationError{Errors: []api.FieldError{fieldErr("/ordered_by", "required",
			"ServiceNow must say who placed the order: the requested item's opened_by.")}}
	case strings.EqualFold(given, strings.TrimSpace(d.User)):
		return up, nil
	}
	op, err := s.serviceNowUser(ctx, given, "/ordered_by")
	var unknown *unknownUserError
	if errors.As(err, &unknown) {
		return nil, &ValidationError{Errors: []api.FieldError{fieldErr("/ordered_by", "unknown_user", fmt.Sprintf(
			"No rvav user has the email or user name %q: to order for someone else, they sign in to rvav once.", unknown.given))}}
	}
	if err != nil {
		return nil, err
	}
	if op.User.ID == up.User.ID {
		return up, nil
	}
	return op, nil
}

// orderKey holds in a context how a request being submitted was made, for
// its approval policies (ADR-0065).
type orderKey struct{}

// order is how a request was made: via rvav, a schedule or ServiceNow,
// and who ordered it, when not its requester.
type order struct {
	via string
	by  *auth.Principal
}

func withOrder(ctx context.Context, o order) context.Context {
	return context.WithValue(ctx, orderKey{}, o)
}

func orderOf(ctx context.Context) order {
	o, _ := ctx.Value(orderKey{}).(order)
	if o.via == "" {
		o.via = "rvav"
	}
	return o
}

func presentServiceNowUser(u store.User) api.ServiceNowUser {
	return api.ServiceNowUser{Id: u.ID, Name: u.Name, Email: openapi_types.Email(u.Email)}
}

// ServiceNowEntitlements are where a user may request and what: what
// ServiceNow's catalog form offers them.
func (s *Service) ServiceNowEntitlements(ctx context.Context, p *auth.Principal, user string) (api.ServiceNowEntitlements, error) {
	if err := serviceNowCaller(p); err != nil {
		return api.ServiceNowEntitlements{}, err
	}
	if err := s.serviceNowLimit(ctx, p, false); err != nil {
		return api.ServiceNowEntitlements{}, err
	}
	up, err := s.serviceNowUser(ctx, user, "/user")
	var unknown *unknownUserError
	if errors.As(err, &unknown) {
		return api.ServiceNowEntitlements{}, ErrNotFound
	}
	if err != nil {
		return api.ServiceNowEntitlements{}, err
	}
	out := api.ServiceNowEntitlements{User: presentServiceNowUser(up.User), Projects: []api.ServiceNowEntitlement{}}
	envs, err := s.Store.ListEnvironments(ctx)
	if err != nil {
		return out, err
	}
	names := map[string]string{}
	for _, e := range envs {
		names[e.Key] = e.Name
	}
	page, err := s.Store.ListProjects(ctx, up.Projects(auth.RequestSubmit), 500, nil)
	if err != nil {
		return out, err
	}
	for _, pr := range page.Items {
		if pr.ArchivedAt != nil || !up.In(auth.RequestSubmit, pr.ID) {
			continue
		}
		items, err := s.itemsFor(ctx, up, pr.ID)
		if err != nil {
			return out, err
		}
		if len(items) == 0 {
			continue
		}
		e := api.ServiceNowEntitlement{Project: api.ProjectRef{Id: pr.ID, Code: pr.Code, Name: pr.Name}, Items: items,
			Environments: []api.ServiceNowChoice{}}
		for _, k := range pr.Environments {
			name := names[k]
			if name == "" {
				name = k
			}
			e.Environments = append(e.Environments, api.ServiceNowChoice{Value: k, Label: name})
		}
		out.Projects = append(out.Projects, e)
	}
	return out, nil
}

// itemsFor are the IDs of the items a principal finds in a project's
// catalog.
func (s *Service) itemsFor(ctx context.Context, p *auth.Principal, projectID string) ([]string, error) {
	who := p.Requester(projectID)
	f := store.ItemFilter{ProjectID: projectID, UserID: who.UserID, Groups: who.Groups, Roles: who.Roles, Limit: 200}
	out := []string{}
	for {
		page, err := s.Store.ListItems(ctx, f)
		if err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			out = append(out, it.ID)
		}
		if page.Next == nil {
			return out, nil
		}
		f.After = page.Next
	}
}

// SubmitServiceNowRequest submits a requested item's request on its user's
// behalf, as that user: with their entitlements, quotas, approval policies
// and lease rules, never more. made is false when a request was made for
// this requested item already: that one is answered.
func (s *Service) SubmitServiceNowRequest(ctx context.Context, p *auth.Principal, d api.ServiceNowRequestDraft) (api.ServiceNowRequest,
	bool, error) {
	if err := serviceNowCaller(p); err != nil {
		return api.ServiceNowRequest{}, false, err
	}
	if err := checkServiceNowDraft(d); err != nil {
		return api.ServiceNowRequest{}, false, err
	}
	// A submission made already is answered as a read: ServiceNow's retry
	// job asks again, and shouldn't use up the room for new orders.
	if made, err := s.Store.ServiceNowRequestForRITM(ctx, d.RitmSysId); err == nil {
		if err := s.serviceNowLimit(ctx, p, false); err != nil {
			return api.ServiceNowRequest{}, false, err
		}
		out, err := s.presentServiceNowRequest(ctx, made)
		return out, false, err
	} else if !errors.Is(err, store.ErrNotFound) {
		return api.ServiceNowRequest{}, false, err
	}
	if err := s.serviceNowLimit(ctx, p, true); err != nil {
		return api.ServiceNowRequest{}, false, err
	}
	client := auth.ClientFrom(ctx)
	detail := map[string]any{"via": "servicenow", "ritm_number": d.RitmNumber, "ritm_sys_id": d.RitmSysId, "user": d.User,
		"ordered_by_given": d.OrderedBy, "catalog_item_id": d.CatalogItemId, "project_id": d.ProjectId, "environment": d.Environment, "token_id": p.TokenID,
		"ip": client.IP, "user_agent": client.UserAgent}
	refuse := func(err error) (api.ServiceNowRequest, bool, error) {
		detail["reason"] = err.Error()
		var ve *ValidationError
		if errors.As(err, &ve) {
			detail["errors"] = ve.Errors
		}
		s.audit(ctx, p, "servicenow.request_refused", "servicenow_request", d.RitmSysId, nil, detail)
		return api.ServiceNowRequest{}, false, err
	}
	if !s.liveConfig(ctx).ServiceNow.Configured() {
		return refuse(&ConflictError{Msg: "ServiceNow's connection isn't set in rvav: it couldn't tell the requested item how the request goes.",
			Retry: true})
	}
	up, err := s.serviceNowUser(ctx, d.User, "/user")
	var unknown *unknownUserError
	if errors.As(err, &unknown) {
		err = &ValidationError{Errors: []api.FieldError{fieldErr("/user", "unknown_user",
			fmt.Sprintf("No rvav user has the email or user name %q.", unknown.given))}}
	}
	if err != nil {
		return refuse(err)
	}
	detail["on_behalf_of"] = map[string]any{"id": up.User.ID, "name": up.User.Name, "email": up.User.Email}
	op, err := s.serviceNowOrderer(ctx, up, d)
	if err != nil {
		return refuse(err)
	}
	detail["ordered_by"] = map[string]any{"id": op.User.ID, "name": op.User.Name, "email": op.User.Email}
	draft, err := s.serviceNowDraft(ctx, up, d)
	if err != nil {
		return refuse(err)
	}
	// As in rvav, ordering for someone else takes request.on_behalf there.
	if op != up && !op.In(auth.RequestOnBehalf, draft.ProjectId) {
		return refuse(&ValidationError{Errors: []api.FieldError{fieldErr("/ordered_by", "not_allowed", fmt.Sprintf(
			"%s ordered it for %s, but may not request for someone else in this project: that takes request.on_behalf there.",
			op.User.Name, up.User.Name))}})
	}
	var tokenID *string
	if p.TokenID != "" {
		tokenID = &p.TokenID
	}
	snow := &store.ServiceNowRequest{RITMSysID: d.RitmSysId, RITMNumber: d.RitmNumber, UserGiven: strings.TrimSpace(d.User),
		OrderedByGiven: strings.TrimSpace(d.OrderedBy), TokenID: tokenID}
	o := order{via: "servicenow"}
	if op != up {
		o.by, snow.OrderedByID = op, &op.User.ID
	}
	row, err := s.submit(withOrder(ctx, o), up, draft, origin{snow: snow})
	if errors.Is(err, store.ErrRITMTaken) {
		// Made by a submission at the same moment.
		made, err := s.Store.ServiceNowRequestForRITM(ctx, d.RitmSysId)
		if err != nil {
			return api.ServiceNowRequest{}, false, err
		}
		out, err := s.presentServiceNowRequest(ctx, made)
		return out, false, err
	}
	if err != nil {
		return refuse(err)
	}
	detail["request_number"] = row.Number
	s.audit(ctx, p, "servicenow.request", "request", row.ID, &row.ProjectID, detail)
	made, err := s.Store.ServiceNowRequestOf(ctx, row.ID)
	if err != nil {
		return api.ServiceNowRequest{}, true, err
	}
	out, err := s.presentServiceNowRequest(ctx, made)
	return out, true, err
}

// serviceNowDraft is the request the user would make in rvav's form for
// the requested item: an item not shared with them in the project is not
// found.
func (s *Service) serviceNowDraft(ctx context.Context, up *auth.Principal, d api.ServiceNowRequestDraft) (api.RequestDraft, error) {
	proj, err := s.visibleProject(ctx, up, d.ProjectId)
	if err != nil {
		return api.RequestDraft{}, err
	}
	if !up.In(auth.RequestSubmit, proj.ID) {
		return api.RequestDraft{}, fmt.Errorf("%w: %s can't request in %s", ErrForbidden, up.User.Name, proj.Name)
	}
	it, err := s.Store.GetItem(ctx, d.CatalogItemId, up.User.ID)
	if errors.Is(err, store.ErrNotFound) {
		return api.RequestDraft{}, ErrNotFound
	}
	if err != nil {
		return api.RequestDraft{}, err
	}
	shared, err := s.SharedWithRequester(ctx, up, it.ID, proj.ID)
	if err != nil {
		return api.RequestDraft{}, err
	}
	if !shared || it.PublishedVersionID == nil {
		return api.RequestDraft{}, ErrNotFound
	}
	v, err := s.Store.GetVersion(ctx, *it.PublishedVersionID)
	if err != nil {
		return api.RequestDraft{}, err
	}
	draft := api.RequestDraft{CatalogItemId: it.ID, VersionId: v.ID, ProjectId: proj.ID,
		Environment: d.Environment, Inputs: map[string]any{}, DeploymentName: d.DeploymentName, AcceptWarnings: d.AcceptWarnings}
	if d.Inputs != nil {
		draft.Inputs = ServiceNowValues(v.FormSchema, *d.Inputs)
	}
	just := fmt.Sprintf("Requested in ServiceNow, %s.", d.RitmNumber)
	if j := strings.TrimSpace(deref(d.Justification)); j != "" {
		just = j + "\n\n" + just
	}
	draft.Justification = &just
	return draft, nil
}

// ServiceNowValues takes ServiceNow's variables, which are strings, as the
// form's types: "4" for an integer, "true" or "false" for a boolean. An
// empty string is left out, so the form's initial value applies; a value
// that isn't one of the type's is kept as it came, for the form to refuse.
func ServiceNowValues(formSchema json.RawMessage, given map[string]any) map[string]any {
	var schema struct {
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(formSchema, &schema)
	out := make(map[string]any, len(given))
	for k, v := range given {
		str, ok := v.(string)
		if !ok {
			out[k] = v
			continue
		}
		if str = strings.TrimSpace(str); str == "" {
			continue
		}
		out[k] = str
		switch t, _ := schema.Properties[k].Type.(string); t {
		case "integer":
			if n, err := strconv.ParseInt(str, 10, 64); err == nil {
				out[k] = n
			}
		case "number":
			if n, err := strconv.ParseFloat(str, 64); err == nil {
				out[k] = n
			}
		case "boolean":
			if b, err := strconv.ParseBool(str); err == nil {
				out[k] = b
			}
		case "string":
			out[k] = v
		}
	}
	return out
}

// ServiceNowRequest is the request made for a requested item.
func (s *Service) ServiceNowRequest(ctx context.Context, p *auth.Principal, sysID string) (api.ServiceNowRequest, error) {
	if err := serviceNowCaller(p); err != nil {
		return api.ServiceNowRequest{}, err
	}
	if !serviceNowSysID.MatchString(sysID) {
		return api.ServiceNowRequest{}, &BadRequestError{Msg: "A sys_id is 32 lower-case hexadecimal digits."}
	}
	if err := s.serviceNowLimit(ctx, p, false); err != nil {
		return api.ServiceNowRequest{}, err
	}
	made, err := s.Store.ServiceNowRequestForRITM(ctx, sysID)
	if err != nil {
		return api.ServiceNowRequest{}, err
	}
	return s.presentServiceNowRequest(ctx, made)
}

func (s *Service) presentServiceNowRequest(ctx context.Context, made store.ServiceNowRequest) (api.ServiceNowRequest, error) {
	req, err := s.Store.GetRequest(ctx, made.RequestID)
	if err != nil {
		return api.ServiceNowRequest{}, err
	}
	u, err := s.Store.GetUser(ctx, req.RequesterID)
	if err != nil {
		return api.ServiceNowRequest{}, err
	}
	deps, err := s.Store.DeploymentsForRequest(ctx, req.ID)
	if err != nil {
		return api.ServiceNowRequest{}, err
	}
	out := api.ServiceNowRequest{RitmSysId: made.RITMSysID, RitmNumber: made.RITMNumber, RequestId: req.ID,
		RequestNumber: req.Number, Status: api.RequestStatus(req.Status), User: presentServiceNowUser(u),
		Url: strings.TrimSuffix(s.Config.PublicURL, "/") + "/requests/" + req.Number, DeploymentIds: []string{}}
	if made.OrderedByID != nil {
		by, err := s.Store.GetUser(ctx, *made.OrderedByID)
		if err != nil {
			return api.ServiceNowRequest{}, err
		}
		ordered := presentServiceNowUser(by)
		out.OrderedBy = &ordered
	}
	for _, dep := range deps {
		out.DeploymentIds = append(out.DeploymentIds, dep.ID)
	}
	return out, nil
}

func (s *Service) serviceNowRef(ctx context.Context, made store.ServiceNowRequest, err error) (*api.ServiceNowRef, error) {
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ref := &api.ServiceNowRef{SysId: made.RITMSysID, Number: made.RITMNumber, User: made.UserGiven,
		Url: servicenow.RecordURL(s.liveConfig(ctx).ServiceNow.URL, servicenow.TableRequestedItem, made.RITMSysID)}
	if made.OrderedByID != nil {
		by, err := s.Store.GetUser(ctx, *made.OrderedByID)
		if err != nil {
			return nil, err
		}
		email := openapi_types.Email(by.Email)
		ref.OrderedBy = &api.UserRef{Id: by.ID, Name: by.Name, Email: &email}
	}
	return ref, nil
}

// ServiceNowRefOfRequest is the requested item a request was made for;
// nil for one made in rvav.
func (s *Service) ServiceNowRefOfRequest(ctx context.Context, requestID string) (*api.ServiceNowRef, error) {
	made, err := s.Store.ServiceNowRequestOf(ctx, requestID)
	return s.serviceNowRef(ctx, made, err)
}

// ServiceNowRefOfDeployment is the requested item the request that made a
// deployment was made for; nil for one made otherwise.
func (s *Service) ServiceNowRefOfDeployment(ctx context.Context, deploymentID string) (*api.ServiceNowRef, error) {
	made, err := s.Store.ServiceNowRequestOfDeployment(ctx, deploymentID)
	return s.serviceNowRef(ctx, made, err)
}

func (s *Service) serviceNowTokenSettings(ctx context.Context) api.ServiceNowToken {
	c := s.liveConfig(ctx).ServiceNow
	out := api.ServiceNowToken{AllowedSources: c.AllowedSources, RequestsPerMinute: c.RequestsPerMinute}
	if out.AllowedSources == nil {
		out.AllowedSources = []string{}
	}
	return out
}

// ServiceNowToken says which token rvav made for ServiceNow, while it is
// valid.
func (s *Service) ServiceNowToken(ctx context.Context, p *auth.Principal) (api.ServiceNowToken, error) {
	if !p.Everywhere(auth.IntegrationsManage) {
		return api.ServiceNowToken{}, ErrForbidden
	}
	out := s.serviceNowTokenSettings(ctx)
	list, err := s.Store.APITokens(ctx, auth.ServiceNowUserID)
	if err != nil {
		return out, err
	}
	now := time.Now()
	for _, t := range list { // newest first
		if t.Name == serviceNowTokenName && (t.ExpiresAt == nil || t.ExpiresAt.After(now)) {
			pt := presentToken(t)
			out.Token = &pt
			break
		}
	}
	return out, nil
}

// MakeServiceNowToken makes ServiceNow's token, shown once, and revokes the
// earlier ones.
func (s *Service) MakeServiceNowToken(ctx context.Context, p *auth.Principal) (api.ServiceNowTokenMade, error) {
	if !p.Everywhere(auth.IntegrationsManage) {
		return api.ServiceNowTokenMade{}, ErrForbidden
	}
	if err := s.Store.EnsureSystemUser(ctx, auth.ServiceNowUserID, serviceNowUserSubject, serviceNowUserName, []string{}); err != nil {
		return api.ServiceNowTokenMade{}, err
	}
	token, id := auth.NewToken(), ids.New(ids.Token)
	expires := time.Now().Add(serviceNowTokenDays * 24 * time.Hour)
	if err := s.Store.CreateAPIToken(ctx, id, auth.ServiceNowUserID, serviceNowTokenName, auth.HashToken(token),
		auth.ScopeServiceNow, &expires); err != nil {
		return api.ServiceNowTokenMade{}, err
	}
	revoked, err := s.revokeServiceNowTokens(ctx, id)
	if err != nil {
		return api.ServiceNowTokenMade{}, err
	}
	s.audit(ctx, p, "servicenow.token", "api_token", id, nil, map[string]any{"revoked": revoked, "expires_at": expires})
	t, err := s.Store.GetAPIToken(ctx, id)
	if err != nil {
		return api.ServiceNowTokenMade{}, err
	}
	return api.ServiceNowTokenMade{Token: presentToken(t), Secret: token, Revoked: revoked}, nil
}

// RevokeServiceNowToken revokes ServiceNow's tokens: ServiceNow can't
// call rvav until another is made.
func (s *Service) RevokeServiceNowToken(ctx context.Context, p *auth.Principal) error {
	if !p.Everywhere(auth.IntegrationsManage) {
		return ErrForbidden
	}
	revoked, err := s.revokeServiceNowTokens(ctx, "")
	if err != nil {
		return err
	}
	if revoked > 0 {
		s.audit(ctx, p, "servicenow.token_revoked", "integration", "servicenow", nil, map[string]any{"revoked": revoked})
	}
	return nil
}

// revokeServiceNowTokens revokes ServiceNow's tokens but keep.
func (s *Service) revokeServiceNowTokens(ctx context.Context, keep string) (int, error) {
	list, err := s.Store.APITokens(ctx, auth.ServiceNowUserID)
	if err != nil {
		return 0, err
	}
	revoked := 0
	for _, t := range list {
		if t.ID == keep {
			continue
		}
		if err := s.Store.RevokeAPIToken(ctx, t.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return revoked, err
		}
		revoked++
	}
	return revoked, nil
}
