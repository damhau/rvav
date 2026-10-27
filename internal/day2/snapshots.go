package day2

import (
	"fmt"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
)

// Snapshots, kept within the environment's retention: at most a number per
// VM, deleted by rvav once older than a number of days (see core.ExpireSnapshots).

func init() {
	register(&Action{
		Key: "snapshot_create", Name: "Take a snapshot", Group: GroupSnapshots, Types: []string{vmType},
		Unavailable: func(t Target, c Choices) string {
			if n := len(snapshots(t)); n >= c.SnapshotMaxCount {
				return fmt.Sprintf("The VM has %d snapshots, the most %s allows. Delete one first.", n, c.Environment)
			}
			return ""
		},
		Form: func(t Target, c Choices) *api.ActionForm {
			name := text("name", "Name", 80, "Before change "+time.Now().UTC().Format("2006-01-02 15:04"))
			if c.SnapshotMaxAgeDays > 0 {
				help := fmt.Sprintf("rvav deletes snapshots after %d days in %s.", c.SnapshotMaxAgeDays, c.Environment)
				name.ui.Help = &help
			}
			fields := []field{name, textarea("description", "Description", 500)}
			if t.VM.PowerState == api.On {
				fields = append(fields, toggle("memory", "Include the memory",
					"Reverting then resumes the VM where it was. Takes longer and needs space for the memory."))
			}
			return form("Snapshot", fields...)
		},
		Validate: func(t Target, in map[string]any) []api.FieldError {
			name := strings.TrimSpace(strInput(in, "name"))
			for _, s := range snapshots(t) {
				if s.Name == name {
					return invalid("name", "taken", "The VM already has a snapshot named %q.", name)
				}
			}
			return nil
		},
		Ops: func(t Target, in map[string]any) []Op {
			name := strings.TrimSpace(strInput(in, "name"))
			op := vcenter("vsphere.snapshot_create", "Take snapshot "+name, "create_snapshot",
				map[string]any{"name": name, "description": strInput(in, "description"), "memory": boolInput(in, "memory")})
			if boolInput(in, "memory") {
				op.Detail = "With the memory"
			}
			return []Op{op}
		},
	})
	register(&Action{
		Key: "snapshot_revert", Name: "Revert to a snapshot", Group: GroupSnapshots, Types: []string{vmType}, Destructive: true,
		Confirm:     "Everything that changed in the VM since the snapshot is lost.",
		Unavailable: hasSnapshots,
		Form: func(t Target, _ Choices) *api.ActionForm {
			return form("Snapshot", snapshotChoice(t))
		},
		Ops: func(_ Target, in map[string]any) []Op {
			name := strInput(in, "snapshot")
			return []Op{vcenter("vsphere.snapshot_revert", "Revert to snapshot "+name, "revert_snapshot", map[string]any{"name": name})}
		},
	})
	register(&Action{
		Key: "snapshot_delete", Name: "Delete a snapshot", Group: GroupSnapshots, Types: []string{vmType},
		Unavailable: hasSnapshots,
		Form: func(t Target, _ Choices) *api.ActionForm {
			return form("Snapshot", snapshotChoice(t))
		},
		Ops: func(_ Target, in map[string]any) []Op {
			name := strInput(in, "snapshot")
			op := vcenter("vsphere.snapshot_delete", "Delete snapshot "+name, "delete_snapshot", map[string]any{"name": name})
			op.Detail = "Its changes are merged into the VM's disks"
			return []Op{op}
		},
	})
}

func hasSnapshots(t Target, _ Choices) string {
	if len(snapshots(t)) == 0 {
		return "The VM has no snapshot."
	}
	return ""
}

func snapshotChoice(t Target) field {
	snaps := snapshots(t)
	opts := make([]api.Option, len(snaps))
	for i, s := range snaps {
		meta := s.CreatedAt.UTC().Format("2006-01-02 15:04 UTC")
		opts[i] = api.Option{Value: s.Name, Label: s.Name, Meta: &meta}
	}
	value := ""
	if len(snaps) > 0 {
		value = snaps[len(snaps)-1].Name
	}
	return choice("snapshot", "Snapshot", opts, value)
}

type vmSnapshot = struct {
	CreatedAt   time.Time `json:"created_at"`
	Description *string   `json:"description,omitempty"`
	Name        string    `json:"name"`
}

func snapshots(t Target) []vmSnapshot {
	if t.VM.Snapshots == nil {
		return nil
	}
	return *t.VM.Snapshots
}
