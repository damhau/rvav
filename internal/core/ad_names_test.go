package core

import (
	"errors"
	"testing"

	"github.com/damhau/rvav/internal/provision"
)

// TestComputerNamesForGood: a name given for good, once the evaluation's
// were taken by another request, is held to 15 characters where its VM
// gets a computer object.
func TestComputerNamesForGood(t *testing.T) {
	withAD := vmPart{ad: &provision.ADUnit{OU: "OU=Web,DC=corp,DC=example"}}
	if err := checkComputerName(withAD, "web-dev-abcde99", false); err != nil {
		t.Fatalf("15 characters = %v", err)
	}
	if err := checkComputerName(vmPart{}, "web-dev-abcde100", false); err != nil {
		t.Fatalf("no computer object = %v", err)
	}
	var ve *ValidationError
	err := checkComputerName(withAD, "web-dev-abcde100", false)
	if !errors.As(err, &ve) || len(ve.Errors) != 1 || ve.Errors[0].Field != "/inputs/role" || deref(ve.Errors[0].Code) != "invalid_name" {
		t.Fatalf("16 characters = %v", err)
	}
	if err := checkComputerName(withAD, "web-dev-abcde100", true); !errors.As(err, &ve) || ve.Errors[0].Field != "" {
		t.Fatalf("16 characters in a component = %v", err)
	}
}
