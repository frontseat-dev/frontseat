package drivertest

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/frontseat-dev/frontseat/sandbox/driver"
)

// Memory is a driver whose sandboxes are held in memory: each is a
// listener on this machine's loopback for every port it asks for, which
// answers a connection with one line. It makes nothing real. It is what
// the sandbox API is tested over, and it is held to the same conformance
// claims as every driver (TestTheMemoryDriverConforms).
type Memory struct {
	mu        sync.Mutex
	sandboxes map[string]*memorySandbox
	// lingers is whether a sandbox that is ended stays, ending.
	lingers bool
	// ruled is whether it takes a sandbox that is held to rules.
	ruled bool
}

type memorySandbox struct {
	record    driver.Sandbox
	listeners map[int]net.Listener
	// ending is whether it was ended and is not gone yet.
	ending bool
}

// Linger has every sandbox ended from now on stay with the driver,
// ending, until the function returned is called, as a provider's does
// whose deletion takes longer than a driver waits for it: Delete says it
// was not gone, and the sandbox is Ending.
func (d *Memory) Linger() (gone func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lingers = true
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.lingers = false
		for id, held := range d.sandboxes {
			if held.ending {
				delete(d.sandboxes, id)
			}
		}
	}
}

// Holds is whether the driver still holds a sandbox, one that is ending
// among them.
func (d *Memory) Holds(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.sandboxes[id]
	return ok
}

var _ driver.Driver = (*Memory)(nil)

// NewMemory returns a driver that holds nothing.
func NewMemory() *Memory { return &Memory{sandboxes: map[string]*memorySandbox{}} }

// RuledMemory returns a driver that holds nothing and takes a sandbox that is
// held to the hosts, programs and requests its rules name, as a
// workspace's is: it holds a sandbox to none of them, making nothing
// real.
func RuledMemory() *Memory { return &Memory{sandboxes: map[string]*memorySandbox{}, ruled: true} }

// Name implements driver.Driver.
func (*Memory) Name() string { return "memory" }

// Capabilities implements driver.Driver.
func (d *Memory) Capabilities() driver.Capabilities {
	return driver.Capabilities{
		PauseKeepsDisk:  true,
		EgressOpen:      true,
		EgressAllowList: d.ruled,
		EgressRules:     d.ruled,
		Volumes:         []driver.VolumeKind{driver.Ephemeral},
		// It keeps no disk, and so takes any size asked for.
	}
}

// Apply implements driver.Driver.
func (d *Memory) Apply(_ context.Context, record driver.Sandbox) (driver.Sandbox, error) {
	if err := driver.Admit(d, record); err != nil {
		return driver.Sandbox{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	held, ok := d.sandboxes[record.ID]
	if ok && held.ending {
		return driver.Sandbox{}, fmt.Errorf("memory: %s: %w", record.ID, driver.ErrEnding)
	}
	if !ok {
		held = &memorySandbox{record: record, listeners: map[int]net.Listener{}}
		for _, port := range record.Ports {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return driver.Sandbox{}, err
			}
			held.listeners[port] = l
			go greet(l, record.ID)
		}
		d.sandboxes[record.ID] = held
	}
	held.record.Paused = record.Paused
	return observed(held), nil
}

// greet answers every connection with the sandbox's id, and closes it.
func greet(l net.Listener, id string) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		fmt.Fprintln(conn, id)
		_ = conn.Close()
	}
}

func observed(s *memorySandbox) driver.Sandbox {
	out := s.record
	out.State = driver.Running
	if out.Paused {
		out.State = driver.Paused
	}
	if s.ending {
		out.State = driver.Ending
	}
	return out
}

// Observe implements driver.Driver.
func (d *Memory) Observe(_ context.Context, id string) (driver.Sandbox, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	held, ok := d.sandboxes[id]
	if !ok {
		return driver.Sandbox{}, driver.ErrNotFound
	}
	return observed(held), nil
}

// List returns the sandboxes that carry labels, those that are ending
// among them: what a test asks of what the driver holds.
func (d *Memory) List(_ context.Context, labels map[string]string) ([]driver.Sandbox, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []driver.Sandbox
	for _, held := range d.sandboxes {
		if carries(held.record.Labels, labels) {
			out = append(out, observed(held))
		}
	}
	return out, nil
}

func carries(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

// Delete implements driver.Driver.
func (d *Memory) Delete(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if held, ok := d.sandboxes[id]; ok {
		for _, l := range held.listeners {
			_ = l.Close()
		}
		if d.lingers {
			held.ending = true
			return fmt.Errorf("memory: %s was not gone when it was ended", id)
		}
		delete(d.sandboxes, id)
	}
	return nil
}

// Dial implements driver.Driver.
func (d *Memory) Dial(ctx context.Context, id string, port int) (net.Conn, error) {
	d.mu.Lock()
	held, ok := d.sandboxes[id]
	var address string
	if ok && held.ending {
		d.mu.Unlock()
		return nil, fmt.Errorf("memory: %s: %w", id, driver.ErrEnding)
	}
	if ok {
		if l, listening := held.listeners[port]; listening {
			address = l.Addr().String()
		}
	}
	d.mu.Unlock()
	if !ok {
		return nil, driver.ErrNotFound
	}
	if address == "" {
		return nil, fmt.Errorf("memory: %s does not open the port %d", id, port)
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", address)
}
