package drivertest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/core/poll"
	"github.com/frontseat-dev/frontseat/sandbox/driver"
)

// Conforms holds a driver to the port. The template is a sandbox the driver
// can make: an image that, written to on the first of its ports, answers,
// and prints
// something when it starts. Every claim makes a sandbox of its own, with
// the template's id and a suffix, and ends it. The template's id is the
// caller's own for this run (ID): a driver's provider is
// shared with whatever else runs the same suite, and a sandbox of the id
// that is there already is another's, which is refused, never ended.
func Conforms(t *testing.T, d driver.Driver, template driver.Sandbox) {
	ctx := context.Background()
	made := func(t *testing.T, suffix string) driver.Sandbox {
		t.Helper()
		return running(t, d, template, suffix)
	}

	t.Run("a sandbox is made and runs", func(t *testing.T) {
		record := made(t, "runs")
		got, err := d.Observe(ctx, record.ID)
		if err != nil || got.ID != record.ID || got.State != driver.Running {
			t.Errorf("Observe = %+v, %v; want %s running", got, err, record.ID)
		}
	})

	t.Run("applying a record twice changes nothing", func(t *testing.T) {
		record := made(t, "twice")
		again, err := d.Apply(ctx, record)
		if err != nil || again.State != driver.Running {
			t.Errorf("the second Apply = %+v, %v; want the sandbox as it was, running", again, err)
		}
		if got, err := d.Observe(ctx, record.ID); err != nil || got.ID != record.ID || got.State != driver.Running {
			t.Errorf("Observe after applying twice = %+v, %v; want %s running", got, err, record.ID)
		}
	})

	t.Run("a port inside a sandbox is reached", func(t *testing.T) {
		record := made(t, "dialled")
		deadline := time.Now().Add(30 * time.Second)
		err := poll.Until(context.Background(), 50*time.Millisecond, func() (bool, error) {
			// Reached is answered: a driver may hand back a connection it has
			// not made yet, which says nothing of the port.
			conn, err := d.Dial(ctx, record.ID, record.Ports[0])
			if err == nil {
				_, err = conn.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
				if err == nil {
					// Answered within a while: a port that takes a connection
					// and says nothing is not reached either.
					answered := make(chan error, 1)
					go func() {
						var first [1]byte
						_, err := conn.Read(first[:])
						answered <- err
					}()
					select {
					case err = <-answered:
					case <-time.After(10 * time.Second):
						err = errors.New("nothing was answered in ten seconds")
					}
				}
				_ = conn.Close()
				if err == nil {
					return true, nil
				}
			}
			if time.Now().After(deadline) {
				return false, fmt.Errorf("the port was not reached and answered: %v", err)
			}
			return false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a paused sandbox is kept and runs again", func(t *testing.T) {
		if !d.Capabilities().PauseKeepsDisk {
			t.Skip("the driver does not pause")
		}
		record := made(t, "paused")
		record.Paused = true
		if _, err := d.Apply(ctx, record); err != nil {
			t.Fatalf("pausing: %v", err)
		}
		Await(t, d, record.ID, driver.Paused)
		record.Paused = false
		if _, err := d.Apply(ctx, record); err != nil {
			t.Fatalf("resuming: %v", err)
		}
		Await(t, d, record.ID, driver.Running)
	})

	t.Run("a deleted sandbox is gone, and deleting it again succeeds", func(t *testing.T) {
		record := made(t, "deleted")
		if err := d.Delete(ctx, record.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := d.Observe(ctx, record.ID); !errors.Is(err, driver.ErrNotFound) {
			t.Errorf("Observe after Delete = %v, want ErrNotFound", err)
		}
		if err := d.Delete(ctx, record.ID); err != nil {
			t.Errorf("deleting it again = %v, want nil", err)
		}
	})

	t.Run("what the driver cannot enforce is refused", func(t *testing.T) {
		caps := d.Capabilities()
		// Each ask is the template, which the driver can make, but for the
		// one thing it cannot give: it is refused for that thing, by name.
		asks := map[string]func(*driver.Sandbox){}
		if !caps.EgressOpen {
			asks["a sandbox that reaches anything"] = func(s *driver.Sandbox) { s.Egress = driver.Egress{} }
		}
		if !caps.EgressBlock {
			asks["blocked egress"] = func(s *driver.Sandbox) { s.Egress = driver.Egress{Block: true} }
		}
		if !caps.EgressAllowList {
			asks["an egress allow list"] = func(s *driver.Sandbox) {
				s.Egress = driver.Egress{Listed: true, Rules: []driver.Rule{{Host: driver.Endpoint{Name: "example.com", Port: 443}}}}
			}
		}
		if !caps.EgressRules {
			asks["a host held to the programs and the requests a rule names"] = func(s *driver.Sandbox) {
				s.Egress = driver.Egress{Listed: true, Rules: []driver.Rule{{Host: driver.Endpoint{Name: "example.com", Port: 443}, Programs: []string{"/usr/bin/git"}}}}
			}
		}
		if !caps.Containers {
			asks["further containers"] = func(s *driver.Sandbox) {
				s.Containers = []driver.SandboxContainer{{Name: "service", Image: template.Image}}
			}
		}
		if !caps.VolumeSubpaths && slices.Contains(caps.Volumes, driver.Host) {
			asks["a volume's subpath"] = func(s *driver.Sandbox) {
				s.Volumes = []driver.Volume{{Name: "work", Kind: driver.Host, Source: t.TempDir(), Subpath: "mine", Path: "/work"}}
			}
		}
		for what, change := range asks {
			ask := template
			ask.ID, ask.Labels = template.ID+"-refused", nil
			change(&ask)
			var refused *driver.Unsupported
			if _, err := d.Apply(ctx, ask); !errors.As(err, &refused) || refused.Capability != what {
				_ = d.Delete(ctx, ask.ID)
				t.Errorf("asking for %s: Apply = %v, want it refused for that", what, err)
			}
		}
	})
}

// Lingering holds a driver to what it says of a sandbox whose ending
// takes longer than Delete waits for: it is there, Ending, for as long
// as the provider holds it, and is never said to be not found, which
// whoever asks takes for gone. The linger has the provider hold every
// sandbox ended from then on, until the function it returns is called;
// a driver whose provider cannot be had to is not held to this here.
func Lingering(t *testing.T, d driver.Driver, template driver.Sandbox, linger func() (gone func())) {
	ctx := context.Background()
	if len(template.Ports) == 0 {
		t.Fatal("the template opens no port: a sandbox that is ending is held to refusing a way to one")
	}
	record := running(t, d, template, "lingers")
	gone := linger()
	t.Cleanup(func() { gone(); _ = d.Delete(ctx, record.ID) })

	// Asked to end twice, it is as it is after once.
	for _, asked := range []string{"once", "again"} {
		if err := d.Delete(ctx, record.ID); err == nil {
			t.Fatalf("Delete, asked %s, of a sandbox the provider still holds = nil, want an error", asked)
		}
		got, err := d.Observe(ctx, record.ID)
		if err != nil || got.ID != record.ID || got.State != driver.Ending {
			t.Fatalf("Observe, Delete asked %s, = %+v, %v; want %s ending", asked, got, err, record.ID)
		}
	}
	if _, err := d.Apply(ctx, record); !errors.Is(err, driver.ErrEnding) {
		t.Errorf("Apply of one that is ending = %v, want ErrEnding", err)
	}
	if conn, err := d.Dial(ctx, record.ID, record.Ports[0]); !errors.Is(err, driver.ErrEnding) {
		if err == nil {
			_ = conn.Close()
		}
		t.Errorf("Dial of one that is ending = %v, want ErrEnding", err)
	}

	gone()
	if err := d.Delete(ctx, record.ID); err != nil {
		t.Errorf("Delete once the provider holds it no more = %v, want nil", err)
	}
	if _, err := d.Observe(ctx, record.ID); !errors.Is(err, driver.ErrNotFound) {
		t.Errorf("Observe once it is gone = %v, want ErrNotFound", err)
	}
}

// running has the driver make a sandbox of the template, of its id and
// a suffix and labelled with that id and with this run's label, waits
// for it to run, and ends it when the test ends (Running).
func running(t *testing.T, d driver.Driver, template driver.Sandbox, suffix string) driver.Sandbox {
	t.Helper()
	record := template
	record.ID = template.ID + "-" + suffix
	// The template's own labels, this run's, and one that is this
	// sandbox's alone, written in the copy Labelled makes so that the
	// template's are not; Running labels it again, which changes nothing.
	record = Labelled(record)
	record.Labels["conformance"] = record.ID
	Running(t, d, record)
	return record
}

// Await waits, for a minute at most, for a sandbox the driver holds by
// an id to reach a state, and fails the test where it fails first.
func Await(t testing.TB, d driver.Driver, id string, want driver.State) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	err := poll.Until(context.Background(), 50*time.Millisecond, func() (bool, error) {
		got, err := d.Observe(context.Background(), id)
		if err != nil {
			return false, fmt.Errorf("Observe: %v", err)
		}
		if got.State == want {
			return true, nil
		}
		if got.State == driver.Failed {
			return false, fmt.Errorf("the sandbox failed: %s", got.Message)
		}
		if time.Now().After(deadline) {
			return false, fmt.Errorf("the sandbox is in state %v after a minute, want %v", got.State, want)
		}
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Endpoint is a host as the driver port takes it, read as the sandbox
// API writes it: what a test names a rule's host or a credential's by.
func Endpoint(t testing.TB, written string) driver.Endpoint {
	t.Helper()
	at, err := driver.ParseEndpoint(written)
	if err != nil {
		t.Fatal(err)
	}
	return at
}
