package runner

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
)

// held is a driver that holds sandboxes by id and says what it was asked.
type held struct {
	mu    sync.Mutex
	by    map[string]driver.Sandbox
	asked []string
}

func (h *held) Apply(_ context.Context, record driver.Sandbox) (driver.Sandbox, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.by == nil {
		h.by = map[string]driver.Sandbox{}
	}
	if _, there := h.by[record.ID]; there {
		h.asked = append(h.asked, "apply-again "+record.ID)
	}
	h.by[record.ID] = record
	h.asked = append(h.asked, "apply "+record.ID)
	return record, nil
}

func (h *held) Observe(_ context.Context, id string) (driver.Sandbox, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if record, there := h.by[id]; there {
		return record, nil
	}
	return driver.Sandbox{}, driver.ErrNotFound
}

func (h *held) Delete(_ context.Context, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.asked = append(h.asked, "delete "+id)
	delete(h.by, id)
	return nil
}

// mounted says each slot's build directory is mounted at the id given, as
// the kernel would; a slot not given is not mounted.
func mounted(slots string, ids map[int]string) func(string) (string, bool, error) {
	return func(path string) (string, bool, error) {
		for slot, id := range ids {
			if path == filepath.Join(slots, strconv.Itoa(slot), "build") {
				return id, true, nil
			}
		}
		return "", false, nil
	}
}

// alike says whether the driver was asked what is wanted: of each
// sandbox, in order, the slots being kept at once.
func alike(got, want []string) bool {
	bySandbox := func(asked []string) map[string][]string {
		out := map[string][]string{}
		for _, each := range asked {
			verb, id, _ := strings.Cut(each, " ")
			out[id] = append(out[id], verb)
		}
		return out
	}
	g, w := bySandbox(got), bySandbox(want)
	if len(g) != len(w) {
		return false
	}
	for id, verbs := range w {
		if !slices.Equal(g[id], verbs) {
			return false
		}
	}
	return true
}

// keptOver keeps a pool over slots mounted at the ids given, with what it
// held before, and requires it asked the driver what is wanted, in order
// for each sandbox.
func keptOver(t *testing.T, d sandboxes, asked *[]string, ids map[int]string, kept map[int]string, want ...string) {
	t.Helper()
	slots := "/var/lib/frontseat/worker"
	*asked = nil
	pool(d, mounted(slots, ids)).keep(t.Context(), kept)
	if !alike(*asked, want) {
		t.Errorf("asked %q, want %q", *asked, want)
	}
}

func pool(sandboxes sandboxes, mounted func(string) (string, bool, error)) Pool {
	return Pool{Sandboxes: sandboxes, Image: "runner@sha256:" + strings.Repeat("a", 64), BB: "/s/bb", Slots: "/var/lib/frontseat/worker", Count: 3, Egress: 1,
		Hosts: []driver.Endpoint{{Name: "github.com", Port: 443}}, Mounted: mounted, Every: time.Hour, Wait: time.Second}
}

// Each slot whose build directory the worker has mounted has a sandbox
// that holds it; a slot not mounted yet has none until it is.
func TestEachMountedSlotHasARunnerSandbox(t *testing.T) {
	d := &held{}
	p := pool(d, mounted("/var/lib/frontseat/worker", map[int]string{1: "101", 3: "103"}))
	kept := map[int]string{}
	p.keep(t.Context(), kept)
	if want := []string{"delete runner-1", "apply runner-1", "delete runner-3", "apply runner-3"}; !alike(d.asked, want) {
		t.Errorf("asked %q, want %q", d.asked, want)
	}
	// Read again with nothing changed, nothing is asked.
	d.asked = nil
	p.keep(t.Context(), kept)
	if len(d.asked) != 0 {
		t.Errorf("an unchanged pool asked %q", d.asked)
	}
}

// The worker started again mounts each build directory anew, and a
// sandbox holds the mount it was made with: that slot's sandbox is
// deleted, waited on, and made again; the others are left as they are.
func TestASlotMountedAnewHasItsSandboxMadeAgain(t *testing.T) {
	d, kept := &held{}, map[int]string{}
	keptOver(t, d, &d.asked, map[int]string{1: "101", 2: "102"}, kept, "delete runner-1", "apply runner-1", "delete runner-2", "apply runner-2")
	keptOver(t, d, &d.asked, map[int]string{1: "201", 2: "102"}, kept, "delete runner-1", "apply runner-1")
}

// An offline slot reaches nothing; an egress slot reaches the hosts it is
// given and nothing else; each holds /bb to read and its own slot's
// directory, and no other's.
func TestASlotsSandboxReachesWhatItsKindMayAndHoldsItsOwnDirectory(t *testing.T) {
	p := pool(&held{}, mounted("/var/lib/frontseat/worker", nil))
	offline, egress := p.Record(2), p.Record(3)
	if !offline.Egress.Block || offline.Egress.Listed {
		t.Errorf("an offline slot's egress is %+v, want blocked", offline.Egress)
	}
	if egress.Egress.Block || !egress.Egress.Listed || len(egress.Egress.Rules) != 1 || egress.Egress.Rules[0].Host.String() != "github.com:443" {
		t.Errorf("an egress slot's egress is %+v, want github.com:443 alone", egress.Egress)
	}
	for _, record := range []driver.Sandbox{offline, egress} {
		var bound []string
		for _, v := range record.Volumes {
			bound = append(bound, v.Source+">"+v.Path)
			if v.Path == "/bb" && !v.ReadOnly {
				t.Errorf("%s may write /bb", record.ID)
			}
		}
		n := strings.TrimPrefix(record.ID, "runner-")
		want := []string{"/s/bb>/bb", "/var/lib/frontseat/worker/" + n + ">/slot", "/var/lib/frontseat/worker/temporaries/" + n + ">/tmp"}
		if !slices.Equal(bound, want) {
			t.Errorf("%s binds %q, want %q", record.ID, bound, want)
		}
	}
}

// Slots whose sandboxes are slow to end hold up no other: each is kept
// apart, at once.
func TestASlowSlotHoldsUpNoOther(t *testing.T) {
	d := &held{by: map[string]driver.Sandbox{"runner-1": {ID: "runner-1"}, "runner-2": {ID: "runner-2"}}}
	p := pool(stuck{d}, mounted("/var/lib/frontseat/worker", map[int]string{1: "101", 2: "102", 3: "103"}))
	p.Wait = 2 * time.Second
	started := time.Now()
	kept := map[int]string{}
	p.keep(t.Context(), kept)
	// Two slow slots one after another take twice as long as one.
	if took := time.Since(started); took > 3*time.Second {
		t.Errorf("a round took %s: the slots were kept one after another", took)
	}
	if kept[3] != "103" {
		t.Errorf("the slot beside the slow ones holds %v", kept)
	}
}

// The pool deletes, as it ends, what it made, and nothing else.
func TestThePoolDeletesWhatItMadeAsItEnds(t *testing.T) {
	d := &held{by: map[string]driver.Sandbox{"someone-else": {ID: "someone-else"}}}
	slots := t.TempDir()
	p := pool(d, mounted(slots, map[int]string{1: "101"}))
	p.Slots = slots
	p.Every = 10 * time.Millisecond
	ranUntilSlotOneIsMade(t, p, d)
	if _, err := d.Observe(t.Context(), "runner-1"); err == nil {
		t.Error("the pool's sandbox was left as it ended")
	}
	if _, err := d.Observe(t.Context(), "someone-else"); err != nil {
		t.Error("the pool deleted a sandbox it did not make")
	}
}

// A sandbox an earlier pool left under a slot's id, the stack having
// stopped with it, holds a mount gone: the slot's is made anew, never
// taken for the one it would have been.
func TestASandboxAnEarlierPoolLeftIsMadeAnew(t *testing.T) {
	d := &held{by: map[string]driver.Sandbox{"runner-1": {ID: "runner-1", Image: "stale"}}}
	keptOver(t, d, &d.asked, map[int]string{1: "101"}, map[int]string{}, "delete runner-1", "apply runner-1")
	if got, _ := d.Observe(t.Context(), "runner-1"); got.Image == "stale" {
		t.Error("the stale sandbox was kept")
	}
}

// A slot whose sandbox failed, or is gone, its runner having ended, has
// its sandbox made anew; one that runs is left as it is (the control).
func TestASlotWhoseRunnerEndedHasItsSandboxMadeAgain(t *testing.T) {
	d, kept := &held{}, map[int]string{}
	ids := map[int]string{1: "101", 2: "102", 3: "103"}
	keptOver(t, d, &d.asked, ids, kept, "delete runner-1", "apply runner-1", "delete runner-2", "apply runner-2", "delete runner-3", "apply runner-3")
	d.mu.Lock()
	failed := d.by["runner-1"]
	failed.State = driver.Failed
	d.by["runner-1"] = failed
	delete(d.by, "runner-2")
	d.mu.Unlock()
	keptOver(t, d, &d.asked, ids, kept, "delete runner-1", "apply runner-1", "delete runner-2", "apply runner-2")
}

// refusing is a driver that refuses to make one sandbox, and holds the
// others as held does.
type refusing struct {
	*held
	id string
}

func (r refusing) Apply(ctx context.Context, record driver.Sandbox) (driver.Sandbox, error) {
	if record.ID == r.id {
		return driver.Sandbox{}, errors.New("refused")
	}
	return r.held.Apply(ctx, record)
}

// A slot whose sandbox cannot be made is tried again in the next round,
// and the others are kept meanwhile; so is a slot whose mount cannot be
// read.
func TestASlotThatFailsLeavesTheOthersKept(t *testing.T) {
	d := &held{}
	kept := map[int]string{}
	keptOver(t, refusing{d, "runner-2"}, &d.asked, map[int]string{1: "101", 2: "102", 3: "103"}, kept,
		"delete runner-1", "apply runner-1", "delete runner-2", "delete runner-3", "apply runner-3")
	if _, is := kept[2]; is {
		t.Error("a slot whose sandbox was refused is held as made")
	}
	keptOver(t, d, &d.asked, map[int]string{1: "101", 2: "102", 3: "103"}, kept, "delete runner-2", "apply runner-2")

	unreadable := pool(d, func(path string) (string, bool, error) {
		if strings.HasSuffix(path, "/1/build") {
			return "", false, errors.New("unreadable")
		}
		return mounted("/var/lib/frontseat/worker", map[int]string{2: "202"})(path)
	})
	d.asked = nil
	unreadable.keep(t.Context(), kept)
	if want := []string{"delete runner-2", "apply runner-2"}; !alike(d.asked, want) {
		t.Errorf("asked %q, want %q", d.asked, want)
	}
}

// stuck is a driver that deletes nothing: a sandbox deleted is held still.
type stuck struct{ *held }

func (stuck) Delete(context.Context, string) error { return nil }

// A sandbox deleted that is still held is waited on no longer than the
// pool says, and the slot is not made over it.
func TestADeletionIsWaitedOnNoLongerThanThePoolSays(t *testing.T) {
	d := &held{by: map[string]driver.Sandbox{"runner-1": {ID: "runner-1"}}}
	p := pool(stuck{d}, mounted("/var/lib/frontseat/worker", map[int]string{1: "101"}))
	p.Wait = 50 * time.Millisecond
	started := time.Now()
	_, err := p.keepSlot(t.Context(), 1, "", false)
	if err == nil || time.Since(started) > 5*time.Second {
		t.Errorf("waited %s and said %v; want it to give up after %s", time.Since(started), err, p.Wait)
	}
	if slices.Contains(d.asked, "apply runner-1") {
		t.Error("the slot was made over a sandbox still held")
	}
}

// unanswering is a driver that deletes nothing and cannot say what it
// holds.
type unanswering struct{ stuck }

var errUnanswered = errors.New("the gateway answers nothing")

func (unanswering) Observe(context.Context, string) (driver.Sandbox, error) {
	return driver.Sandbox{}, errUnanswered
}

// A driver that cannot say whether a deleted sandbox is gone is not
// taken for one that still holds it: what it failed with is said. The
// control is the driver that holds it still, which is said to.
func TestADriverThatCannotSayASandboxIsGoneIsSaidSo(t *testing.T) {
	d := &held{by: map[string]driver.Sandbox{"runner-1": {ID: "runner-1"}}}
	for name, sandboxes := range map[string]sandboxes{"unanswering": unanswering{stuck{d}}, "holding": stuck{d}} {
		p := pool(sandboxes, mounted("/var/lib/frontseat/worker", map[int]string{1: "101"}))
		p.Wait = 50 * time.Millisecond
		_, err := p.keepSlot(t.Context(), 1, "", false)
		if said := errors.Is(err, errUnanswered); err == nil || said != (name == "unanswering") {
			t.Errorf("a driver %s: %v", name, err)
		}
	}
}

// A slot's runner is given to the worker once, by what Hand does, at an
// endpoint beside the slots' directories and in none of them: until then
// the worker finds no runner, and what was at the endpoint before is
// removed when the slot's sandbox is made anew. The control is the
// runner's socket, handed over once it listens, and once alone.
func TestARunnersSocketIsHandedOverOnceOutsideTheSlot(t *testing.T) {
	// Short: a socket's path is held to about a hundred bytes. Run as an
	// action, which writes /tmp not at all, its own temporary directory is
	// short enough.
	slots, err := os.MkdirTemp("/tmp", "pool")
	if err != nil {
		if slots, err = os.MkdirTemp("", "pool"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { os.RemoveAll(slots) })
	for _, dir := range []string{filepath.Join(slots, "1"), filepath.Join(slots, "runners")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	endpoint := filepath.Join(slots, "runners", "1")
	if err := os.WriteFile(endpoint, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var handed [][2]string
	p := pool(&held{}, mounted(slots, map[int]string{1: "101"}))
	p.Slots, p.Count, p.Egress = slots, 1, 0
	p.Hand = func(listen, at string) error {
		handed = append(handed, [2]string{listen, at})
		return os.Link(listen, at)
	}
	kept := map[int]string{}
	p.keep(t.Context(), kept)
	if _, err := os.Lstat(endpoint); err == nil {
		t.Error("what was at the endpoint before the slot's sandbox was made was left for the worker to find")
	}
	listener, err := net.Listen("unix", filepath.Join(slots, "1", "listen"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	p.keep(t.Context(), kept)
	p.keep(t.Context(), kept)
	if want := [][2]string{{filepath.Join(slots, "1", "listen"), endpoint}}; !slices.Equal(handed, want) {
		t.Errorf("handed over %q, want the runner's socket at the endpoint, once", handed)
	}
	if info, err := os.Lstat(endpoint); err != nil || info.Mode().Type() != os.ModeSocket {
		t.Errorf("the worker finds no runner at its endpoint: %v", err)
	}
}

// A runner the table of processes says is stopped is found, and one
// running, or of another name, is not.
func TestAStoppedRunnerIsFound(t *testing.T) {
	proc := t.TempDir()
	for pid, stat := range map[string]string{
		"10": "10 (frontseat-runne) T 1 1 1 0",
		"11": "11 (frontseat-runne) S 1 1 1 0",
		"12": "12 (sleep) T 1 1 1 0",
		"13": "13 (frontseat-runne) t 1 1 1 0",
	} {
		if err := os.MkdirAll(filepath.Join(proc, pid), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proc, pid, "stat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := stoppedRunners(proc)
	slices.Sort(got)
	if !slices.Equal(got, []int{10, 13}) {
		t.Errorf("found %v stopped, want 10 and 13", got)
	}
}

// Each slot's temporary directory, bound at /tmp in its sandbox, is open
// to every user as /tmp is, in a directory of the pool's that no sandbox
// holds; one laid out otherwise before, as an earlier pool or anyone
// could have, is set so again.
func TestEachSlotsTemporaryDirectoryIsOpenAsTmpIsInADirectoryOfThePools(t *testing.T) {
	d := &held{}
	slots := t.TempDir()
	if err := os.MkdirAll(filepath.Join(slots, "temporaries", "1"), 0o700); err != nil {
		t.Fatal(err)
	}
	p := pool(d, mounted(slots, map[int]string{1: "101"}))
	p.Slots, p.Every = slots, 10*time.Millisecond
	ranUntilSlotOneIsMade(t, p, d)
	if info, err := os.Stat(filepath.Join(slots, "temporaries")); err != nil || info.Mode().Perm() != 0o755 || !info.IsDir() {
		t.Errorf("the slots' temporary directories are in %v (%v), want a directory of the pool's, 0755", info.Mode(), err)
	}
	for slot := 1; slot <= p.Count; slot++ {
		info, err := os.Stat(p.temporary(slot))
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o777 || info.Mode()&os.ModeSticky == 0 {
			t.Errorf("slot %d's temporary directory is %v (%v), want one open as /tmp is", slot, info.Mode(), err)
		}
	}
}

// ranUntilSlotOneIsMade runs a pool until it has made slot 1's sandbox,
// then ends it.
func ranUntilSlotOneIsMade(t *testing.T, p Pool, d *held) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := d.Observe(t.Context(), "runner-1"); err == nil {
			break
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
