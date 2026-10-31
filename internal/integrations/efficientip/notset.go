package efficientip

import (
	"context"
	"errors"
)

// ErrNotSet is NotSet's error when it is given none.
var ErrNotSet = errors.New("EfficientIP is not set: Administration › Integrations › EfficientIP")

// NotSet is the client of an install whose EfficientIP isn't set yet, or
// whose settings make no client (ADR-0030): every call returns why, so the
// process starts and Administration can set it.
type NotSet struct{ Err error }

var _ Client = NotSet{}

func (n NotSet) err() error {
	if n.Err != nil {
		return n.Err
	}
	return ErrNotSet
}

func (n NotSet) NextFree(context.Context, Subnet) (string, error) { return "", n.err() }
func (n NotSet) Reserve(context.Context, Subnet, string, string) (Address, error) {
	return Address{}, n.err()
}
func (n NotSet) Release(context.Context, Address) error               { return n.err() }
func (n NotSet) RenameAddress(context.Context, Address, string) error { return n.err() }
func (n NotSet) CreateRecord(context.Context, Record) (Record, error) { return Record{}, n.err() }
func (n NotSet) UpdateRecord(context.Context, Record, string) (Record, error) {
	return Record{}, n.err()
}
func (n NotSet) DeleteRecord(context.Context, Record) error           { return n.err() }
func (n NotSet) NameExists(context.Context, string) (bool, error)     { return false, n.err() }
func (n NotSet) Subnets(context.Context) ([]SubnetInfo, error)        { return nil, n.err() }
func (n NotSet) Zones(context.Context) ([]string, error)              { return nil, n.err() }
func (n NotSet) Managed(context.Context) ([]Address, []Record, error) { return nil, nil, n.err() }
func (n NotSet) Existing(context.Context, []string, []string) (map[string]bool, map[string]bool, error) {
	return nil, nil, n.err()
}
func (n NotSet) AllocateSubnet(context.Context, string, int, string, map[string]string) (SubnetInfo, error) {
	return SubnetInfo{}, n.err()
}
func (n NotSet) DeleteSubnet(context.Context, string) error { return n.err() }
func (n NotSet) Find(context.Context, []string, []string) ([]Address, []Record, error) {
	return nil, nil, n.err()
}
