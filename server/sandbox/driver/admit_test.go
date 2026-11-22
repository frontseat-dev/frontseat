package driver

import (
	"context"
	"errors"
	"net"
	"testing"
)

type fake struct{ caps Capabilities }

func (fake) Name() string                 { return "fake" }
func (f fake) Capabilities() Capabilities { return f.caps }
func (fake) Apply(context.Context, Sandbox) (Sandbox, error) {
	return Sandbox{}, nil
}
func (fake) Observe(context.Context, string) (Sandbox, error)    { return Sandbox{}, ErrNotFound }
func (fake) Delete(context.Context, string) error                { return nil }
func (fake) Dial(context.Context, string, int) (net.Conn, error) { return nil, nil }

func TestASandboxAskingForMoreThanTheDriverEnforcesIsRefused(t *testing.T) {
	containers := fake{caps: Capabilities{EgressOpen: true, Volumes: []VolumeKind{Ephemeral}}}
	cases := []struct {
		name string
		ask  Sandbox
		want string
	}{
		{"blocked egress", Sandbox{Egress: Egress{Block: true}}, "blocked egress"},
		{"a pause", Sandbox{Paused: true}, "pause"},
		{"a host volume", Sandbox{Volumes: []Volume{{Name: "work", Kind: Host}}}, "a volume of a kind it does not give"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var u *Unsupported
			if err := Admit(containers, c.ask); !errors.As(err, &u) || u.Capability != c.want {
				t.Errorf("Admit = %v, want it refused for %s", err, c.want)
			}
		})
	}
}

func TestASandboxWithinWhatTheDriverEnforcesIsAdmitted(t *testing.T) {
	d := fake{caps: Capabilities{EgressBlock: true, Volumes: []VolumeKind{Ephemeral, Host}}}
	ask := Sandbox{Egress: Egress{Block: true}, Volumes: []Volume{{Name: "work", Kind: Host}}}
	if err := Admit(d, ask); err != nil {
		t.Errorf("Admit = %v, want it admitted", err)
	}
}

// A driver that lets nothing be reached unnamed refuses a sandbox that
// names neither a block nor the hosts it may reach, and takes one that
// names them.
func TestASandboxThatReachesAnythingIsRefusedByADriverThatLetsNothingBeReachedUnnamed(t *testing.T) {
	closed := fake{caps: Capabilities{EgressBlock: true, EgressAllowList: true}}
	var u *Unsupported
	if err := Admit(closed, Sandbox{}); !errors.As(err, &u) || u.Capability != "a sandbox that reaches anything" {
		t.Errorf("Admit = %v, want it refused for a sandbox that reaches anything", err)
	}
	for name, ask := range map[string]Sandbox{
		"one that names its hosts": {Egress: Egress{Listed: true, Rules: []Rule{{Host: Endpoint{Name: "example.com", Port: 443}}}}},
		"one that reaches nothing": {Egress: Egress{Block: true}},
	} {
		if err := Admit(closed, ask); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
	open := fake{caps: Capabilities{EgressOpen: true}}
	if err := Admit(open, Sandbox{}); err != nil {
		t.Errorf("a driver whose sandboxes may reach anything refused one: %v", err)
	}
}
