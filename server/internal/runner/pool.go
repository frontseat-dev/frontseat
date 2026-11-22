package runner

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frontseat-dev/frontseat/server/internal/poll"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver/openshell"
)

// sandboxes is what the pool asks of a driver: an OpenShell gateway's.
type sandboxes interface {
	Apply(ctx context.Context, record driver.Sandbox) (driver.Sandbox, error)
	Observe(ctx context.Context, id string) (driver.Sandbox, error)
	Delete(ctx context.Context, id string) error
}

// Pool keeps the grid's runner sandboxes: one for each slot of the
// worker, each running Frontseat's runner (Confined) over the slot's
// build directory, one
// action at a time, and holding nothing else. Of its Count slots, the
// last Egress reach the hosts Hosts names, and their slots of the worker
// advertise it (an action that fetches is sent there); the others reach
// nothing. The worker mounts each slot's build directory
// anew each time it starts, and a sandbox holds the mount it was made
// with: so a slot whose mount is not the one its sandbox holds has its
// sandbox made anew, and so has one whose sandbox failed or is gone
// (its runner ended). What goes wrong with one slot is said, and the
// slot is tried again; the others are kept meanwhile.
type Pool struct {
	// Sandboxes is the driver that makes them.
	Sandboxes sandboxes
	// Image is the runner image each is made of, by its digest.
	Image string
	// BB is the directory of the gateway's host holding the runner and the
	// confiners, bound into each at /bb.
	BB string
	// Slots is the worker's directory of the gateway's host holding each
	// slot's, by its number from one, bound into its sandbox at /slot: the
	// image's working directory is OpenShell's own to keep.
	Slots string
	// Count is how many slots there are, numbered from one, and Egress
	// how many of the last of them reach Hosts.
	Count, Egress int
	// Hosts are what an egress slot reaches, each a name and a port
	// (EgressHosts).
	Hosts []driver.Endpoint
	// Mounted says what is mounted at a slot's build directory, by an id
	// the kernel never gives another mount, or that nothing is.
	Mounted func(path string) (id string, is bool, err error)
	// Hand gives the worker a slot's runner: the socket its runner made at
	// listen, given to root alone (owned, 0600) and linked at endpoint, in
	// a directory of root's that no sandbox holds (Slots/runners), where
	// the worker dials it. Nothing in a sandbox reaches it, nor replaces
	// it.
	Hand func(listen, endpoint string) error
	// Continue continues each runner an action stopped.
	Continue func()
	// Every is how often the slots are read; Wait how long a sandbox
	// deleted is waited on before the slot is tried again.
	Every, Wait time.Duration
}

// slotLabel marks a sandbox the pool made, with its slot: what is the
// pool's to delete.
const slotLabel = "frontseat.dev/runner-slot"

// idOf is a slot's sandbox's id.
func idOf(slot int) string { return "runner-" + strconv.Itoa(slot) }

// Record is the sandbox a slot is given.
func (p Pool) Record(slot int) driver.Sandbox {
	record := driver.Sandbox{
		ID:        idOf(slot),
		Image:     p.Image,
		Arguments: []string{"/bb/frontseat-runner", "serve", "--listen", "/slot/listen", "--build", "/slot/build"},
		Labels:    map[string]string{slotLabel: strconv.Itoa(slot)},
		Volumes: []driver.Volume{
			{Name: "bb", Kind: driver.Host, Source: p.BB, Path: "/bb", ReadOnly: true},
			{Name: "slot", Kind: driver.Host, Source: filepath.Join(p.Slots, strconv.Itoa(slot)), Path: "/slot"},
			// Where each action's temporary directory is made
			// (confine.Temporaries): on the stack's disk, which keeps a
			// file's mode as the worker's mount does not, and in a
			// directory of root's, so that the sandbox, which writes /slot,
			// leads root's making it nowhere else.
			{Name: "temporary", Kind: driver.Host, Source: p.temporary(slot), Path: "/tmp"},
		},
		Egress: driver.Egress{Block: true},
	}
	if slot > p.Count-p.Egress {
		record.Egress = driver.Egress{Listed: true}
		for _, host := range p.Hosts {
			record.Egress.Rules = append(record.Egress.Rules, driver.Rule{Host: host})
		}
	}
	return record
}

// EgressHosts reads what an egress slot reaches, as the stack's file
// lists it: each a name and a port, apart by white space.
func EgressHosts(listed string) ([]driver.Endpoint, error) {
	var out []driver.Endpoint
	for _, written := range strings.Fields(listed) {
		host, err := driver.ParseEndpoint(written)
		if err != nil {
			return nil, fmt.Errorf("an egress slot's host: %w", err)
		}
		out = append(out, host)
	}
	return out, nil
}

// temporary is a slot's temporary directory on the gateway's host.
func (p Pool) temporary(slot int) string {
	return filepath.Join(p.Slots, "temporaries", strconv.Itoa(slot))
}

// KeepPool keeps a pool of sandboxes an OpenShell gateway makes, with the
// Podman driver, each private (openshell.Config.Private): it binds the
// directories each is given, and lets one write nothing else. The slots
// are read every two seconds, their mounts by the kernel's unique mount id
// (mountOf).
func KeepPool(ctx context.Context, gateway, identity string, p Pool) error {
	d, err := openshell.New(openshell.Config{Address: gateway, Identity: identity, Compute: "podman", Private: true})
	if err != nil {
		return err
	}
	defer d.Close()
	p.Sandboxes, p.Mounted, p.Every, p.Wait = d, mountOf, 2*time.Second, time.Minute
	p.Hand, p.Continue = handOver, func() { continueStopped("/proc") }
	return p.Run(ctx)
}

// Run keeps the pool until ctx ends, then deletes what it made.
func (p Pool) Run(ctx context.Context) error {
	if p.Count < 1 || p.Egress < 0 || p.Egress > p.Count {
		return errors.New("a pool has one slot or more, and no more egress slots than slots")
	}
	// Where the worker dials each slot's runner: this program's, root's,
	// to write, and bound into no sandbox.
	runners := filepath.Join(p.Slots, "runners")
	if err := os.MkdirAll(runners, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(runners, 0o700); err != nil {
		return err
	}
	// Each slot's temporary directory, in a directory of this program's
	// that no sandbox holds: open to every user of its sandbox, as /tmp is,
	// each user's own files kept to it.
	if err := os.MkdirAll(filepath.Join(p.Slots, "temporaries"), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(p.Slots, "temporaries"), 0o755); err != nil {
		return err
	}
	for slot := 1; slot <= p.Count; slot++ {
		if err := os.MkdirAll(p.temporary(slot), 0o755); err != nil {
			return err
		}
		if err := os.Chmod(p.temporary(slot), 0o777|os.ModeSticky); err != nil {
			return err
		}
	}
	held := map[int]string{}
	defer func() {
		ending := context.WithoutCancel(ctx)
		// Each sandbox is the pool's own: made by it under its slot's id.
		for slot := range held {
			if err := p.gone(ending, slot); err != nil {
				log.Printf("frontseat-runner pool: deleting slot %d: %v", slot, err)
			}
		}
	}()
	tick := time.NewTicker(p.Every)
	defer tick.Stop()
	for {
		p.keep(ctx, held)
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// keep gives each slot whose build directory is mounted a sandbox that
// holds that mount and runs. A slot that cannot be given one is said and
// left for the next round; the others are kept.
func (p Pool) keep(ctx context.Context, held map[int]string) {
	if p.Continue != nil {
		p.Continue()
	}
	// Each slot is kept apart, at once: one whose sandbox is slow to end
	// holds up no other.
	var (
		mu    sync.Mutex
		slots sync.WaitGroup
	)
	for slot := 1; slot <= p.Count; slot++ {
		mu.Lock()
		was, is := held[slot]
		mu.Unlock()
		slots.Go(func() {
			now, err := p.keepSlot(ctx, slot, was, is)
			mu.Lock()
			if now == "" {
				delete(held, slot)
			} else {
				held[slot] = now
			}
			mu.Unlock()
			if err != nil && ctx.Err() == nil {
				log.Printf("frontseat-runner pool: slot %d: %v", slot, err)
			}
		})
	}
	slots.Wait()
}

// keepSlot gives a slot whose build directory is mounted a sandbox that
// holds that mount and runs. It is told the mount the slot's sandbox was
// made over, where the pool made one, and returns the mount its sandbox
// holds now, empty where it made none.
func (p Pool) keepSlot(ctx context.Context, slot int, was string, made bool) (string, error) {
	mount, is, err := p.Mounted(filepath.Join(p.Slots, strconv.Itoa(slot), "build"))
	if err != nil {
		return keptOn(was, made), fmt.Errorf("reading its mount: %w", err)
	}
	if !is {
		return keptOn(was, made), nil
	}
	if made && was == mount {
		sandbox, err := p.Sandboxes.Observe(ctx, idOf(slot))
		switch {
		case errors.Is(err, driver.ErrNotFound):
		case err != nil:
			return was, fmt.Errorf("observing its sandbox: %w", err)
		case sandbox.State == driver.Failed || sandbox.State == driver.Ending:
			log.Printf("frontseat-runner pool: slot %d's sandbox ended (%s): made anew", slot, sandbox.Message)
		default:
			return was, p.hand(slot)
		}
	}
	// A sandbox of the slot's id is the pool's own: one this pool made
	// over the worker's mount before it started again, one whose runner
	// ended, or one an earlier pool left, the stack having stopped with
	// it. Each is deleted before the slot's is made.
	if err := p.gone(ctx, slot); err != nil {
		return "", err
	}
	// What an earlier runner listened at is the pool's to remove: it names
	// a runner gone, and the worker is to find none until the new one's
	// is root's.
	for _, at := range []string{p.endpoint(slot), filepath.Join(p.Slots, strconv.Itoa(slot), "listen")} {
		if err := os.Remove(at); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if _, err := p.Sandboxes.Apply(ctx, p.Record(slot)); err != nil {
		return "", fmt.Errorf("making its sandbox: %w", err)
	}
	return mount, p.hand(slot)
}

// keptOn is what a slot's sandbox holds where nothing was done of it:
// what it held.
func keptOn(was string, made bool) string {
	if !made {
		return ""
	}
	return was
}

// hand gives the worker the slot's runner, once it listens, and once
// for each sandbox of the slot: what is at its endpoint is the sandbox's
// runner until the sandbox is made anew, which removes it first.
func (p Pool) hand(slot int) error {
	endpoint := p.endpoint(slot)
	if _, err := os.Lstat(endpoint); err == nil {
		return nil
	}
	listening := filepath.Join(p.Slots, strconv.Itoa(slot), "listen")
	if _, err := os.Lstat(listening); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := p.Hand(listening, endpoint); err != nil {
		return fmt.Errorf("giving the worker its runner: %w", err)
	}
	return nil
}

// endpoint is where the worker dials a slot's runner.
func (p Pool) endpoint(slot int) string {
	return filepath.Join(p.Slots, "runners", strconv.Itoa(slot))
}

// gone deletes a slot's sandbox and returns once the driver holds it no
// more, so that one of its id may be made again, or once Wait has passed.
func (p Pool) gone(ctx context.Context, slot int) error {
	ctx, cancel := context.WithTimeout(ctx, p.Wait)
	defer cancel()
	id := idOf(slot)
	if err := p.Sandboxes.Delete(ctx, id); err != nil && !errors.Is(err, driver.ErrNotFound) {
		return err
	}
	// What the driver last failed to answer with, where the waiting was
	// not what ended the asking: it is said, and not taken for a sandbox
	// still held.
	var failed error
	// It ends with an error only where ctx ended while it waited.
	if poll.Until(ctx, 200*time.Millisecond, func() (bool, error) {
		_, err := p.Sandboxes.Observe(ctx, id)
		if errors.Is(err, driver.ErrNotFound) {
			return true, nil
		}
		if ctx.Err() == nil {
			failed = err
		}
		return false, nil
	}) == nil {
		return nil
	}
	if failed != nil {
		return fmt.Errorf("its sandbox deleted could not be asked after for %s: %w", p.Wait, failed)
	}
	return fmt.Errorf("its sandbox deleted was still held after %s", p.Wait)
}
