package driver

import (
	"fmt"
	"slices"
)

// Unsupported is a sandbox that asks for what a driver cannot enforce.
type Unsupported struct {
	// Driver is the driver asked.
	Driver string
	// Capability names what it lacks.
	Capability string
}

// Error implements error.
func (u *Unsupported) Error() string {
	return fmt.Sprintf("the %s driver cannot give %s", u.Driver, u.Capability)
}

// Admit checks a sandbox against what a driver can enforce. A sandbox
// that asks for more is refused: it is never run weaker. A refusal names
// what was asked for by its kind and never by what the caller wrote, a
// volume's name among it, which may hold a token.
func Admit(d Driver, s Sandbox) error {
	c := d.Capabilities()
	refuse := func(capability string) error {
		return &Unsupported{Driver: d.Name(), Capability: capability}
	}
	if !s.Egress.Block && !s.Egress.Listed && !c.EgressOpen {
		return refuse("a sandbox that reaches anything")
	}
	if s.Egress.Block && !c.EgressBlock {
		return refuse("blocked egress")
	}
	// What a rule holds is refused by its own name, before the list it is
	// in: a credential is kept or not whatever else a sandbox reaches.
	for _, rule := range s.Egress.Rules {
		if rule.CredentialID != "" && !c.Credentials {
			return refuse("a credential kept outside its sandboxes")
		}
		if (len(rule.Programs) > 0 || len(rule.Requests) > 0) && !c.EgressRules {
			return refuse("a host held to the programs and the requests a rule names")
		}
	}
	if s.Egress.Listed && !c.EgressAllowList {
		return refuse("an egress allow list")
	}
	if s.Paused && !c.PauseKeepsDisk {
		return refuse("pause")
	}
	if s.BeforeContainers != nil && !c.StartupGate {
		return refuse("a startup gate before further containers")
	}
	if len(s.Containers) > 0 && !c.Containers {
		return refuse("further containers")
	}
	volumes := append([]Volume{}, s.Volumes...)
	for _, container := range s.Containers {
		volumes = append(volumes, container.Volumes...)
	}
	for _, v := range volumes {
		if !slices.Contains(c.Volumes, v.Kind) {
			return refuse("a volume of a kind it does not give")
		}
		if v.Subpath != "" && !c.VolumeSubpaths {
			return refuse("a volume's subpath")
		}
	}
	if s.DiskBytes != 0 && !c.DiskSize {
		return refuse("a disk size")
	}
	return nil
}
