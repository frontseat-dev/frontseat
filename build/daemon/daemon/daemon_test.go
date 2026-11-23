package daemon

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/build/daemon/build"
	"github.com/frontseat-dev/frontseat/testsupport/testauthority"
)

func TestTheWorkersPlatformIsWhatTheDaemonIsTold(t *testing.T) {
	image := "docker://example.com/runner@sha256:0000"
	got, err := platformOf([]string{"OSFamily=linux", "container-image=" + image})
	if want := map[string]string{"OSFamily": "linux", "container-image": image}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("platform = %v, %v; want %v", got, err, want)
	}
	// The control: a daemon told nothing asks for Linux on this machine's
	// architecture.
	if got, err := platformOf(nil); err != nil || got["OSFamily"] != "linux" || got["ISA"] == "" {
		t.Errorf("told nothing: %v, %v; want Linux on this machine's architecture", got, err)
	}
}

func TestAPlatformThatIsNotPropertiesIsRefused(t *testing.T) {
	for name, flags := range map[string][]string{
		"a flag with no value":   {"OSFamily"},
		"a flag with no name":    {"=linux"},
		"a property given twice": {"ISA=aarch64", "ISA=x86-64"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := platformOf(flags); err == nil {
				t.Errorf("platform = %v; want it refused", got)
			}
		})
	}
}

// Which proxy stands in for a download's site is read strictly: a flag
// that is not a pattern, a space and an address is refused at the
// start, not found out at the first download. They are kept in the order
// given, which is the order they are tried in.
func TestTheDownloadRewritesAreReadStrictly(t *testing.T) {
	rewrites, err := rewritesOf([]string{`^https://b c/(.*)$ http://other/$1`, `^https://a\.example/(.*)$ http://proxy/$1`})
	if want := []build.Rewrite{{From: `^https://b c/(.*)$`, To: "http://other/$1"}, {From: `^https://a\.example/(.*)$`, To: "http://proxy/$1"}}; err != nil || !slices.Equal(rewrites, want) {
		t.Fatalf("sound rewrites: %v, %v", rewrites, err)
	}
	if rewrites, err := rewritesOf(nil); err != nil || rewrites != nil {
		t.Errorf("nothing told: %v, %v; want no rewrites and no error", rewrites, err)
	}
	for name, given := range map[string][]string{
		"no address":             {`^https://a\.example/(.*)$`},
		"an address and nothing": {` http://proxy/`},
		"a space and nothing":    {`^a$ `},
		"no regular expression":  {`^(a$ http://proxy/`},
		"a pattern given twice":  {`^a$ http://one/`, `^a$ http://two/`},
	} {
		if _, err := rewritesOf(given); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A sandbox's daemon told nothing of its workers' platform is refused
// by name; the control is the same daemon told it by its flags.
func TestASandboxDaemonToldNoPlatformIsRefused(t *testing.T) {
	if err := toldPlatform(nil); err == nil || !strings.Contains(err.Error(), "--platform") {
		t.Errorf("told nothing: %v, want it refused naming --platform", err)
	}
	if err := toldPlatform([]string{"OSFamily=linux"}); err != nil {
		t.Errorf("told by its flags: %v, want it allowed", err)
	}
}

// A grid that asks who a daemon is, is asked as the flags say, and a
// daemon told to ask in a way it cannot is refused before it serves: a
// token is never sent in the clear, and a file named is one it can read.
func TestAGridThatAsksWhoTheDaemonIsIsAskedAsTheFlagsSay(t *testing.T) {
	dir := t.TempDir()
	token, nothing := filepath.Join(dir, "token"), filepath.Join(dir, "nothing.pem")
	for name, content := range map[string]string{token: "aaa.bbb.ccc\n", nothing: "no certificate\n"} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	missing := filepath.Join(dir, "missing")
	known := testauthority.New(t).File
	asked, err := gridOf("grid:8980", "frontseat/p", "assets:8983", nil, true, known, token)
	if err != nil || !asked.TLS || asked.Authorities != known || asked.Token != token || asked.Instance != "frontseat/p" || asked.Address != "grid:8980" || asked.Assets != "assets:8983" {
		t.Errorf("over TLS with a token: %+v, %v", asked, err)
	}
	// As the local stack's grid is asked: in the clear, saying nothing.
	if plain, err := gridOf("localhost:8980", "", "", nil, false, "", ""); err != nil || plain.TLS || plain.Authorities != "" || plain.Token != "" {
		t.Errorf("given nothing: %+v, %v", plain, err)
	}
	for name, c := range map[string]struct {
		tls       bool
		ca, token string
	}{
		"a token in the clear":                  {token: token},
		"a token, and the system's authorities": {tls: true, token: token},
		"a token file that is not there":        {ca: known, token: missing},
		"authorities that are not there":        {ca: missing},
		"authorities that hold none":            {ca: nothing},
		"a token where authorities fail":        {ca: nothing, token: token},
	} {
		if got, err := gridOf("grid:8980", "", "", nil, c.tls, c.ca, c.token); err == nil {
			t.Errorf("%s: %+v; want it refused", name, got)
		} else if strings.Contains(err.Error(), "aaa.bbb.ccc") {
			t.Errorf("%s: the refusal repeats the token: %v", name, err)
		}
	}
}

// A flag that takes no value ends the flags where a value follows it:
// "--grid-tls true --grid-ca FILE --grid-token FILE" was read as TLS by the
// system's authorities with no token, and nothing said so.
func TestWhatIsNoFlagIsRefused(t *testing.T) {
	if err := strays([]string{"true", "--grid-ca", "ca.pem"}); err == nil || !strings.Contains(err.Error(), `"true"`) {
		t.Errorf("an argument after the flags: %v", err)
	}
	if err := strays(nil); err != nil {
		t.Errorf("flags alone: %v", err)
	}
}

// The daemon's flags are its own: what the grid's client library
// registers with the process's flags, as its logging's -v and
// -logtostderr, is there and is no flag of the daemon's, refused before
// the daemon does anything. The control is a flag of the daemon's, taken.
func TestAFlagALibraryRegisteredIsNoneOfTheDaemons(t *testing.T) {
	for _, name := range []string{"v", "logtostderr", "log_dir"} {
		if flag.CommandLine.Lookup(name) == nil {
			t.Fatalf("the process's flags hold no -%s: the library's flags are not what this test takes them for", name)
		}
		said, err := asked(t, "-"+name+"=2")
		if !errors.Is(err, ErrUsage) || !strings.Contains(said, "flag provided but not defined: -"+name) {
			t.Errorf("-%s: %v, saying %q; want it refused as no flag", name, err, said)
		}
	}
	if _, err := asked(t, "--version"); err != nil {
		t.Errorf("a flag of the daemon's: %v", err)
	}
}

// A daemon asked wrongly, by a flag that is none of its own or by what
// follows its flags, says so with its flags on standard error, as the
// flag package does for every program here, and Run returns ErrUsage,
// which its main ends with status 2 for. Asked for its flags, it is told
// them and does nothing.
func TestADaemonAskedWronglySaysItsFlags(t *testing.T) {
	for name, args := range map[string][]string{
		"no flag of its own":     {"--no-such-flag"},
		"what follows the flags": {"--grid-tls", "true"},
	} {
		said, err := asked(t, args...)
		if !errors.Is(err, ErrUsage) || !strings.Contains(said, "-workspace") {
			t.Errorf("%s: %v, saying %q; want ErrUsage and the daemon's flags", name, err, said)
		}
	}
	said, err := asked(t, "--help")
	if err != nil || !strings.Contains(said, "-workspace") {
		t.Errorf("--help: %v, saying %q; want the daemon's flags and nothing done", err, said)
	}
}

// asked runs the daemon with args and returns what it said on standard
// error.
func asked(t *testing.T, args ...string) (string, error) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stderr := os.Stderr
	os.Stderr = file
	err = Run(t.Context(), args, Release{Version: "1.4.0"})
	os.Stderr = stderr
	said, readErr := os.ReadFile(file.Name())
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(said), err
}

// The daemon says which release it is, as the release that built it
// wrote it in; one no release built says its version alone.
func TestTheDaemonSaysWhichReleaseItIs(t *testing.T) {
	if said := (Release{Version: "0.0.0-dev"}).String(); said != "frontseatd 0.0.0-dev" {
		t.Errorf("a daemon no release built said %q", said)
	}
	if said := (Release{Version: "1.4.0", Commit: "0123abcd"}).String(); said != "frontseatd 1.4.0 (0123abcd)" {
		t.Errorf("a released daemon said %q", said)
	}
}

// A daemon given a checkout is named, or the command line beside it
// finds no daemon: one given a checkout and no name is refused. The
// controls are one given both, and a daemon given no checkout.
func TestADaemonGivenACheckoutIsNamed(t *testing.T) {
	if err := handed(true, ""); err == nil || !strings.Contains(err.Error(), "--checkout was given with no --name") {
		t.Errorf("a daemon given a checkout and no name: %v", err)
	}
	for _, control := range []struct {
		checkout bool
		name     string
	}{{true, "acme/code"}, {false, "payments"}, {false, ""}} {
		if err := handed(control.checkout, control.name); err != nil {
			t.Errorf("checkout %v, workspace %q: refused, %v", control.checkout, control.name, err)
		}
	}
}
