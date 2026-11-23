package openshell

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestAHostVolumeIsBoundAsItSays(t *testing.T) {
	got, err := mounts("podman", []driver.Volume{
		{Name: "bb", Kind: driver.Host, Source: "/var/lib/frontseat/bb", Path: "/bb", ReadOnly: true},
		{Name: "slot", Kind: driver.Host, Source: "/var/lib/frontseat/worker/3", Path: "/worker"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"podman": map[string]any{"mounts": []any{
		map[string]any{"type": "bind", "source": "/var/lib/frontseat/bb", "target": "/bb", "read_only": true},
		map[string]any{"type": "bind", "source": "/var/lib/frontseat/worker/3", "target": "/worker", "read_only": false},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bound %v, want %v", got, want)
	}
	// It is what the gateway is sent: a protobuf Struct holds it.
	if _, err := structpb.NewStruct(got); err != nil {
		t.Errorf("the driver's configuration is no Struct: %v", err)
	}
}

func TestNoVolumeIsNoDriverConfiguration(t *testing.T) {
	if got, err := mounts("podman", nil); got != nil || err != nil {
		t.Errorf("no volume gave %v, %v", got, err)
	}
}

func TestAVolumeTheGatewayCannotBindIsRefusedByName(t *testing.T) {
	plain := driver.Volume{Name: "v", Kind: driver.Host, Source: "/a", Path: "/b"}
	for name, c := range map[string]struct {
		compute string
		volume  driver.Volume
	}{
		"a compute driver that binds nothing": {"kubernetes", plain},
		"no compute driver named":             {"", plain},
		"an ephemeral volume":                 {"podman", driver.Volume{Name: "v", Kind: driver.Ephemeral, Path: "/b"}},
		"a subpath":                           {"podman", driver.Volume{Name: "v", Kind: driver.Host, Source: "/a", Path: "/b", Subpath: "c"}},
		"a relative source":                   {"podman", driver.Volume{Name: "v", Kind: driver.Host, Source: "a", Path: "/b"}},
		"a source that climbs":                {"podman", driver.Volume{Name: "v", Kind: driver.Host, Source: "/a/../etc", Path: "/b"}},
		"a target spelled two ways":           {"podman", driver.Volume{Name: "v", Kind: driver.Host, Source: "/a", Path: "/b/"}},
	} {
		if _, err := mounts(c.compute, []driver.Volume{c.volume}); err == nil || !strings.Contains(err.Error(), "openshell:") {
			t.Errorf("%s: bound, want refused (%v)", name, err)
		}
	}
	// The control: the same volume, plain, on a binding driver, is bound.
	if _, err := mounts("docker", []driver.Volume{plain}); err != nil {
		t.Errorf("a plain host volume on docker was refused: %v", err)
	}
}

func TestAGatewayThatBindsSaysItTakesHostVolumes(t *testing.T) {
	if got := volumesOf("podman"); !reflect.DeepEqual(got, []driver.VolumeKind{driver.Host}) {
		t.Errorf("podman takes %v, want host volumes", got)
	}
	if got := volumesOf("kubernetes"); got != nil {
		t.Errorf("kubernetes takes %v, want none", got)
	}
}

func TestWhatIsBoundInIsReadOrWrittenAsItsVolumeSays(t *testing.T) {
	d := &Driver{}
	policy, err := d.policy(driver.Sandbox{Egress: driver.Egress{Block: true}, Volumes: []driver.Volume{
		{Name: "bb", Kind: driver.Host, Source: "/s/bb", Path: "/bb", ReadOnly: true},
		{Name: "slot", Kind: driver.Host, Source: "/s/w", Path: "/worker"},
	}}, fetched{})
	if err != nil {
		t.Fatal(err)
	}
	has := func(list []string, path string) bool {
		for _, each := range list {
			if each == path {
				return true
			}
		}
		return false
	}
	if !has(policy.Filesystem.ReadOnly, "/bb") || has(policy.Filesystem.ReadWrite, "/bb") {
		t.Errorf("/bb is read-only: read %v, written %v", policy.Filesystem.ReadOnly, policy.Filesystem.ReadWrite)
	}
	if !has(policy.Filesystem.ReadWrite, "/worker") {
		t.Errorf("/worker is written: written %v", policy.Filesystem.ReadWrite)
	}
}

// A sandbox that runs one action after another may write what is bound
// into it and nothing else: no working directory, no temporary directory
// one action could leave something in for the next. The control is a
// sandbox that is not so, which keeps both.
func TestAPrivateSandboxWritesWhatIsBoundInAlone(t *testing.T) {
	record := driver.Sandbox{Egress: driver.Egress{Block: true}, Volumes: []driver.Volume{{Name: "slot", Kind: driver.Host, Source: "/s/w/1", Path: "/worker"}}}
	private, err := (&Driver{config: Config{Private: true}}).policy(record, fetched{})
	if err != nil {
		t.Fatal(err)
	}
	if private.Filesystem.IncludeWorkdir {
		t.Error("a private sandbox may write its working directory")
	}
	for _, path := range private.Filesystem.ReadWrite {
		if path != "/worker" && !strings.HasPrefix(path, "/dev/") {
			t.Errorf("a private sandbox may write %s", path)
		}
	}
	// A directory of its image it asks to write is refused, never
	// dropped; the control is the same sandbox where it is not private.
	writes := record
	writes.Writable = []string{"/var/lib/postgresql"}
	if _, err := (&Driver{config: Config{Private: true}}).policy(writes, fetched{}); err == nil {
		t.Error("a private sandbox that asks to write a directory of its image was made")
	}
	if _, err := (&Driver{}).policy(writes, fetched{}); err != nil {
		t.Errorf("the control, not private: %v", err)
	}
	shared, err := (&Driver{}).policy(record, fetched{})
	if err != nil {
		t.Fatal(err)
	}
	if !shared.Filesystem.IncludeWorkdir || !slices.Contains(shared.Filesystem.ReadWrite, "/tmp") {
		t.Errorf("a sandbox that is not private lost its working directory or /tmp: %+v", shared.Filesystem)
	}
}

// A host volume's subpath is bound as the directory it names beneath the
// source, reached through directories alone: one that climbs out, one
// reached through a link, or one that is no directory, is refused, since
// the gateway would bind whatever the link leads to. The control is a
// directory beneath the source, which is bound.
func TestASubpathIsBoundAsTheDirectoryBeneathItsVolume(t *testing.T) {
	source := t.TempDir()
	for _, dir := range []string{"a/b", "elsewhere"} {
		if err := os.MkdirAll(filepath.Join(source, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(source, "elsewhere"), filepath.Join(source, "a", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "a", "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	volume := func(subpath string) []driver.Volume {
		return []driver.Volume{{Name: "v", Kind: driver.Host, Source: source, Path: "/b", Subpath: subpath}}
	}
	bound, err := mounts("podman", volume("a/b"))
	if err != nil {
		t.Fatalf("a directory beneath the volume was refused: %v", err)
	}
	if got := bound["podman"].(map[string]any)["mounts"].([]any)[0].(map[string]any)["source"]; got != filepath.Join(source, "a", "b") {
		t.Errorf("the subpath is bound from %v, want %s", got, filepath.Join(source, "a", "b"))
	}
	for name, subpath := range map[string]string{
		"one that climbs":            "../x",
		"one spelled two ways":       "a//b",
		"one reached through a link": "a/link",
		"one beneath a link":         "a/link/x",
		"one that is no directory":   "a/file",
		"one that is not there":      "a/none",
	} {
		if _, err := mounts("podman", volume(subpath)); err == nil {
			t.Errorf("%s (%s) was bound", name, subpath)
		}
	}
}

// A service runs as the user it names and writes the directories of its
// image it names, and a gateway's sandbox that may read all its image
// holds reads /. The control is a record that names neither, whose
// sandbox runs as the gateway's own user and writes nothing more.
func TestASandboxRunsAsItsUserAndWritesWhatItNames(t *testing.T) {
	service := driver.Sandbox{User: "70", Writable: []string{"/var/lib/postgresql", "/var/run/postgresql"}}
	held, err := (&Driver{config: Config{ReadAll: true}}).policy(service, fetched{})
	if err != nil {
		t.Fatal(err)
	}
	if held.Process == nil || held.Process.RunAsUser != "70" || held.Process.RunAsGroup != "70" {
		t.Errorf("the service runs as %+v, want user and group 70", held.Process)
	}
	for _, dir := range service.Writable {
		if !slices.Contains(held.Filesystem.ReadWrite, dir) {
			t.Errorf("the service may not write %s: %v", dir, held.Filesystem.ReadWrite)
		}
	}
	if !slices.Equal(held.Filesystem.ReadOnly, []string{"/"}) {
		t.Errorf("a sandbox that may read all its image reads %v", held.Filesystem.ReadOnly)
	}
	plain, err := (&Driver{}).policy(driver.Sandbox{}, fetched{})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Process != nil || slices.Contains(plain.Filesystem.ReadWrite, "/var/lib/postgresql") || slices.Contains(plain.Filesystem.ReadOnly, "/") {
		t.Errorf("a sandbox that names nothing is held to %+v %+v", plain.Process, plain.Filesystem)
	}
}
