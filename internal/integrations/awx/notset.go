package awx

import (
	"context"
	"errors"
)

// ErrNotSet is NotSet's error when it is given none.
var ErrNotSet = errors.New("AWX is not set: Administration › Integrations › AWX")

// NotSet is the client of an install whose AWX isn't set yet, or whose
// settings make no client (ADR-0030): every call returns why, so the
// process starts and Administration can set it.
type NotSet struct{ Err error }

var _ Client = NotSet{}

// IsSet says whether c is a client of an AWX set: not nil, nor NotSet.
func IsSet(c Client) bool {
	_, notSet := c.(NotSet)
	return c != nil && !notSet
}

func (n NotSet) err() error {
	if n.Err != nil {
		return n.Err
	}
	return ErrNotSet
}

func (n NotSet) LookupJobTemplate(context.Context, string, bool) (int, error) { return 0, n.err() }
func (n NotSet) Templates(context.Context, TemplateFilter) ([]Template, error) {
	return nil, n.err()
}
func (n NotSet) Survey(context.Context, int, bool) ([]SurveyQuestion, error) { return nil, n.err() }
func (n NotSet) WorkflowNodes(context.Context, int) ([]WorkflowNode, error)  { return nil, n.err() }
func (n NotSet) Launch(context.Context, int, bool, LaunchRequest) (Job, error) {
	return Job{}, n.err()
}
func (n NotSet) GetJob(context.Context, int, bool) (Job, error)          { return Job{}, n.err() }
func (n NotSet) Events(context.Context, int, int) ([]Event, error)       { return nil, n.err() }
func (n NotSet) Cancel(context.Context, int, bool) error                 { return n.err() }
func (n NotSet) UpdateInventorySource(context.Context, int) (int, error) { return 0, n.err() }
func (n NotSet) InventoryOfSource(context.Context, int) (int, error)     { return 0, n.err() }
func (n NotSet) Inventories(context.Context, string) ([]Inventory, error) {
	return nil, n.err()
}
func (n NotSet) Credentials(context.Context, string, string) ([]Credential, error) {
	return nil, n.err()
}
func (n NotSet) GetInventoryUpdate(context.Context, int) (Job, error) { return Job{}, n.err() }
func (n NotSet) SetRvavCredential(context.Context, int, string, string, string) (RvavCredentialSet, error) {
	return RvavCredentialSet{}, n.err()
}
