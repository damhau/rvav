package notify

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/store"
)

// Data is what a template gets (ADR-0059): the variables its kind lists
// (Variables), the same for every channel and language.
type Data struct {
	Kind, Locale string
	// Portal is the portal's name (Administration › Branding).
	Portal string
	User   Person
	// Title, Body and Level are the notification's, as the app shows it
	// (English); Body carries what can't be translated, such as an error.
	Title, Body, Level string
	// Link opens what it is about in rvav; Preferences, the user's choices.
	Link, Preferences string
	Request           Request
	Project           Project
	Deployment        Deployment
	Lease             Lease
	Approval          Approval
	PreviousOwner     string
	// Ready: a request built what was asked for, rather than changed it.
	Ready bool

	approvalID string
}

// Person is who a notification is for.
type Person struct{ Name string }

// Request is the request a notification is about.
type Request struct{ Number, Title, Item string }

// Project is the project and environment it is in.
type Project struct{ Name, Environment string }

// Deployment is the deployment it is about.
type Deployment struct{ Name string }

// Lease is a deployment's lease, for lease reminders.
type Lease struct {
	ExpiresAt    *time.Time
	Days         int
	ExpiryAction string
	GraceDays    int
}

// Approval is the approval level that asks the user.
type Approval struct {
	Name    string
	Reasons []string
	// Why is "delegated" or "escalated" when it came to the user so.
	Why string
}

// extra is what the occasion added to a notification (store.Notification.Data).
type extra struct {
	Days         int    `json:"days"`
	ExpiryAction string `json:"expiry_action"`
	GraceDays    int    `json:"grace_days"`
	Why          string `json:"why"`
	From         string `json:"from"`
	Ready        bool   `json:"ready"`
}

// dataOf is a delivery's template data.
func dataOf(d store.Delivery, s store.NotificationSubject, publicURL, portal string) Data {
	var x extra
	_ = json.Unmarshal(d.Notification.Data, &x)
	n := d.Notification
	out := Data{Kind: n.Kind, Locale: Locale(d.Locale), Portal: portal, User: Person{d.UserName}, Title: n.Title, Body: n.Body,
		Level: n.Level, Link: publicURL + n.Link, Preferences: publicURL + "/notifications",
		Request:       Request{Number: s.RequestNumber, Title: s.RequestTitle, Item: s.ItemName},
		Project:       Project{Name: s.ProjectName, Environment: s.Environment},
		Deployment:    Deployment{Name: s.DeploymentName},
		Lease:         Lease{ExpiresAt: s.LeaseExpiresAt, Days: x.Days, ExpiryAction: x.ExpiryAction, GraceDays: x.GraceDays},
		Approval:      Approval{Name: s.ApprovalName, Reasons: s.ApprovalReasons, Why: x.Why},
		PreviousOwner: x.From, Ready: x.Ready}
	if n.ApprovalID != nil {
		out.approvalID = *n.ApprovalID
	}
	return out
}

// escaped is the data with each text an author or a requester wrote passed
// through esc, for a channel whose text is markup: what rvav writes around
// it may use the markup, what they wrote is shown as it is.
func (d Data) escaped(esc func(string) string) Data {
	d.Portal, d.User.Name, d.Title, d.Body = esc(d.Portal), esc(d.User.Name), esc(d.Title), esc(d.Body)
	d.Request = Request{Number: esc(d.Request.Number), Title: esc(d.Request.Title), Item: esc(d.Request.Item)}
	d.Project = Project{Name: esc(d.Project.Name), Environment: esc(d.Project.Environment)}
	d.Deployment.Name, d.PreviousOwner = esc(d.Deployment.Name), esc(d.PreviousOwner)
	reasons := make([]string, len(d.Approval.Reasons))
	for i, r := range d.Approval.Reasons {
		reasons[i] = esc(r)
	}
	d.Approval = Approval{Name: esc(d.Approval.Name), Reasons: reasons, Why: d.Approval.Why}
	return d
}

// Variable is one a template may use.
type Variable struct {
	// Name as a template writes it: .Request.Number.
	Name string `json:"name"`
	// Type: text, number, yes/no, date or list of text.
	Type        string `json:"type"`
	Description string `json:"description"`
}

// Kind is a notification kind whose templates administrators edit.
type Kind struct {
	Kind, Name, When string
	Variables        []Variable
}

var (
	common = []Variable{
		{".Portal", "text", "The portal's name, set in Administration › Branding."},
		{".User.Name", "text", "The name of the person it is sent to."},
		{".Link", "text", "The address of what it is about in the portal."},
		{".Preferences", "text", "The address of the person's notification preferences."},
		{".Title", "text", "The notification's title as the portal's notification centre shows it, in English."},
		{".Locale", "text", "The template's language: en or fr."},
	}
	requestVars = []Variable{
		{".Request.Number", "text", "The request's number, such as REQ-10077."},
		{".Request.Title", "text", "The request's title."},
		{".Request.Item", "text", "The name of the catalog item requested."},
		{".Project.Name", "text", "The request's project."},
		{".Project.Environment", "text", "The request's environment, such as prd."},
	}
	deploymentVars = []Variable{
		{".Deployment.Name", "text", "The deployment's name."},
		{".Project.Name", "text", "The deployment's project."},
		{".Project.Environment", "text", "The deployment's environment, such as prd."},
	}
	bodyVar = func(what string) Variable { return Variable{".Body", "text", what} }
)

func kind(k, name, when string, vars ...[]Variable) Kind {
	all := slices.Clone(common)
	for _, v := range vars {
		all = append(all, v...)
	}
	return Kind{Kind: k, Name: name, When: when, Variables: all}
}

// Kinds are the kinds whose templates administrators edit, in the order
// the notification centre lists them.
var Kinds = []Kind{
	kind("request_submitted", "Request submitted", "To the requester, and the owner when on behalf, once a request is submitted.",
		requestVars),
	kind("approval_needed", "Approval needed", "To each approver of a level when it opens, is delegated or escalated to them, or a custom workflow asks them.",
		requestVars, []Variable{
			{".Approval.Name", "text", "The approval level's name."},
			{".Approval.Reasons", "list of text", "Why the request needs approval: one reason per policy."},
			{".Approval.Why", "text", "delegated or escalated when the approval came to the person so; empty otherwise."},
			bodyVar("Why it needs approval, or the custom workflow's message."),
		}),
	kind("request_approved", "Request approved", "To the requester, once the last level approved.",
		requestVars, []Variable{bodyVar("The approvers' comments, if any.")}),
	kind("request_rejected", "Request rejected", "To the requester, once a level rejected.",
		requestVars, []Variable{bodyVar("The approver's comment: why it was rejected.")}),
	kind("request_succeeded", "Request done", "To the owner, and the requester when on behalf, once a request is done.",
		requestVars, []Variable{{".Ready", "yes/no", "Yes when the request built what was asked for, rather than changed it."}}),
	kind("request_failed", "Request failed", "To the owner, and the requester when on behalf, once a request failed.",
		requestVars, []Variable{bodyVar("What went wrong.")}),
	kind("request_rolled_back", "Request rolled back", "To the owner, once what a failed request built was deleted.",
		requestVars),
	kind("lease_expiring", "Lease ending", "To the deployment's owner, 7 days and 1 day before its lease ends.",
		deploymentVars, []Variable{
			{".Lease.ExpiresAt", "date", "When the lease ends: write {{date .Lease.ExpiresAt}}."},
			{".Lease.Days", "number", "Days left: 7 or 1."},
			{".Lease.ExpiryAction", "text", "What happens then: power_off_then_delete, delete or notify_only."},
			bodyVar("What happens then, in English."),
		}),
	kind("lease_expired", "Lease ended", "To the deployment's owner, once its lease ended.",
		deploymentVars, []Variable{
			{".Lease.GraceDays", "number", "Days before the deployment is deleted; 0 when its policy keeps it running."},
			bodyVar("What happens now, in English."),
		}),
	kind("owner_changed", "Deployment handed over", "To the new owner of a deployment.",
		deploymentVars, []Variable{{".PreviousOwner", "text", "Who handed it over."}, bodyVar("Who handed it over, as a sentence in English.")}),
	kind("step_message", "Message from a composite", "To those a composite's message step names, when it runs.",
		requestVars, []Variable{bodyVar("The message the step wrote.")}),
}

// KindOf is an editable kind; false for others ("test").
func KindOf(k string) (Kind, bool) {
	i := slices.IndexFunc(Kinds, func(x Kind) bool { return x.Kind == k })
	if i < 0 {
		return Kind{}, false
	}
	return Kinds[i], true
}

// variables are the kind's variable names; the test message's are the
// common ones.
func variables(k string) map[string]bool {
	vars := common
	if kd, ok := KindOf(k); ok {
		vars = kd.Variables
	}
	out := map[string]bool{}
	for _, v := range vars {
		out[v.Name] = true
		// The objects on the way are variables too: {{with .Request}}.
		for p := v.Name; strings.Count(p, ".") > 1; {
			p = p[:strings.LastIndex(p, ".")]
			out[p] = true
		}
	}
	return out
}

// Sample is what a kind's preview shows, and what a template is tried on
// before it is saved.
func Sample(kind, locale, publicURL, portal string) Data {
	ends := time.Date(2026, 11, 26, 9, 30, 0, 0, time.UTC)
	d := Data{Kind: kind, Locale: locale, Portal: portal, User: Person{"Thomas Nguyen"}, Level: "info",
		Preferences: publicURL + "/notifications"}
	request := func(link string) {
		d.Request = Request{Number: "REQ-10077", Title: "RHEL 9 server web-prd-front01", Item: "RHEL 9 server"}
		d.Project = Project{Name: "Web Platform", Environment: "prd"}
		d.Link = publicURL + link
	}
	deployment := func() {
		d.Deployment = Deployment{Name: "web-prd-front01"}
		d.Project = Project{Name: "Web Platform", Environment: "prd"}
		d.Link = publicURL + "/deployments/dep_sample"
	}
	switch kind {
	case "request_submitted":
		request("/requests/REQ-10077")
		d.Title = "Submitted: RHEL 9 server web-prd-front01"
	case "approval_needed":
		request("/approvals/apr_sample")
		d.Approval = Approval{Name: "Infrastructure leads", Reasons: []string{"Production VMs need the infrastructure leads' approval",
			"8 vCPU is above the project's size for prd"}}
		d.Title, d.Body, d.approvalID = "Approval needed: RHEL 9 server web-prd-front01", strings.Join(d.Approval.Reasons, "; "), "apr_sample"
		d.Level = "warning"
	case "request_approved":
		request("/requests/REQ-10077")
		d.Title, d.Body, d.Level = "Approved: RHEL 9 server web-prd-front01", "Fine for Black Friday.", "success"
	case "request_rejected":
		request("/requests/REQ-10077")
		d.Title, d.Body, d.Level = "Rejected: RHEL 9 server web-prd-front01", "Use the shared front-end pool instead.", "error"
	case "request_succeeded":
		request("/requests/REQ-10077")
		d.Title, d.Ready, d.Level = "Ready: RHEL 9 server web-prd-front01", true, "success"
	case "request_failed":
		request("/requests/REQ-10077")
		d.Title, d.Body, d.Level = "Failed: RHEL 9 server web-prd-front01", "No datastore of GVA-PRD-CL02 has 200 GB free.", "error"
	case "request_rolled_back":
		request("/requests/REQ-10077")
		d.Title = "Rolled back: RHEL 9 server web-prd-front01"
	case "lease_expiring":
		deployment()
		d.Lease = Lease{ExpiresAt: &ends, Days: 7, ExpiryAction: "power_off_then_delete"}
		d.Title, d.Body, d.Level = "The lease of web-prd-front01 ends in 7 days", "Its VMs are then powered off.", "warning"
	case "lease_expired":
		deployment()
		d.Lease = Lease{GraceDays: 7}
		d.Title, d.Body, d.Level = "The lease of web-prd-front01 ended", "Its VMs are powered off.", "warning"
	case "owner_changed":
		deployment()
		d.PreviousOwner, d.Title = "Léa Martin", "You now own web-prd-front01"
	case "step_message":
		request("/requests/REQ-10077")
		d.Title, d.Body = "A message about RHEL 9 server web-prd-front01", "The load balancer pool is ready.\nAdd the DNS alias by Friday."
	default:
		d.Link, d.Title = publicURL+"/notifications", "Test"
	}
	return d
}
