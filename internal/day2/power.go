package day2

import "github.com/damhau/rvav/internal/api"

// Power actions. Shut down and restart ask the guest OS through VMware
// Tools; power off and reset act on the VM like its power button.

func init() {
	register(&Action{
		Key: "power_on", Name: "Power on", Group: GroupPower, Types: []string{vmType},
		Unavailable: func(t Target, _ Choices) string {
			if t.VM.PowerState == api.On {
				return "The VM is already powered on."
			}
			return ""
		},
		Ops: func(t Target, _ map[string]any) []Op {
			return []Op{vcenter("vsphere.power_on", "Power on "+t.ResourceName, "power_on", nil)}
		},
	})
	register(&Action{
		Key: "shutdown_guest", Name: "Shut down guest", Group: GroupPower, Types: []string{vmType},
		Unavailable: needsTools("shut down"),
		Ops: func(t Target, _ map[string]any) []Op {
			return []Op{vcenter("vsphere.shutdown_guest", "Shut down the guest OS of "+t.ResourceName, "shutdown_guest", nil)}
		},
	})
	register(&Action{
		Key: "restart_guest", Name: "Restart guest", Group: GroupPower, Types: []string{vmType},
		Unavailable: needsTools("restart"),
		Ops: func(t Target, _ map[string]any) []Op {
			return []Op{vcenter("vsphere.restart_guest", "Restart the guest OS of "+t.ResourceName, "restart_guest", nil)}
		},
	})
	register(&Action{
		Key: "power_off", Name: "Power off", Group: GroupPower, Types: []string{vmType}, Destructive: true,
		Confirm:     "Powering off is like pulling the plug: unsaved work in the guest is lost. Shut down guest stops it cleanly.",
		Unavailable: poweredOn,
		Ops: func(t Target, _ map[string]any) []Op {
			return []Op{vcenter("vsphere.power_off", "Power off "+t.ResourceName, "power_off", nil)}
		},
	})
	register(&Action{
		Key: "reset", Name: "Reset", Group: GroupPower, Types: []string{vmType}, Destructive: true,
		Confirm:     "Reset restarts the VM without asking the guest OS: unsaved work is lost. Restart guest restarts it cleanly.",
		Unavailable: poweredOn,
		Ops: func(t Target, _ map[string]any) []Op {
			return []Op{vcenter("vsphere.reset", "Reset "+t.ResourceName, "reset", nil)}
		},
	})
}

func poweredOn(t Target, _ Choices) string {
	if t.VM.PowerState != api.On {
		return "The VM is powered off."
	}
	return ""
}

func needsTools(verb string) func(Target, Choices) string {
	return func(t Target, c Choices) string {
		if reason := poweredOn(t, c); reason != "" {
			return reason
		}
		if t.VM.GuestState != api.VmPropertiesGuestStateRunning {
			return "VMware Tools is not running, so the guest OS can't be asked to " + verb + "."
		}
		return ""
	}
}
