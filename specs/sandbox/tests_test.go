package sandbox_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/server/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/server/testsupport/testpins"

	. "github.com/onsi/gomega"
)

// A module's tests are an action for each package that has tests, and a
// package's test runs where the go command would run it: in the
// package's directory, with the fixtures under it. Here that is asked of
// a daemon in a workspace's sandbox, which cloned the repository: the
// plugin is the sandbox image's own, and the grid runs what it said.
// A package's tests, in a workspace's sandbox / A package beneath a module is tested in its own directory, with its fixture.
func TestAPackageBeneathAModuleIsTestedInItsOwnDirectoryWithItsFixture(t *testing.T) {
	spec := newScenario(t)

	// What the fixture holds, no build before this one read.
	word := fmt.Sprintf("word-%d", time.Now().UnixNano())
	checkout := spec.aRepository()
	testfiles.Tree(spec.TB, checkout, map[string]string{
		"hello/go.mod":                   "module example.com/hello\n\ngo 1.27\n",
		"hello/main.go":                  "package main\n\nfunc main() {}\n",
		"hello/ledger/ledger.go":         "package ledger\n",
		"hello/ledger/testdata/word.txt": word,
		// It opens its fixture by a path from where it stands, and
		// finds no go.mod there: run in the module's directory, it
		// would find the one and miss the other. And it finds its own
		// package's files, as the go command runs a test among them,
		// and nothing above them: not the module's go.mod, nor another
		// package's source.
		"hello/ledger/ledger_test.go": fmt.Sprintf(`package ledger

import (
	"os"
	"testing"
)

func TestItRunsInItsPackagesDirectory(t *testing.T) {
	if got, err := os.ReadFile("testdata/word.txt"); err != nil || string(got) != %q {
		t.Fatalf("read %%q: %%v", got, err)
	}
	if _, err := os.Stat("go.mod"); err == nil {
		t.Fatal("the module's go.mod is here: this is the module's directory")
	}
	// It is given its package's directory, and nothing above it.
	for _, own := range []string{"ledger.go", "ledger_test.go"} {
		if _, err := os.Stat(own); err != nil {
			t.Fatalf("%%s, of its own package, is not here: %%v", own, err)
		}
	}
	for _, above := range []string{"../go.mod", "../main.go"} {
		if _, err := os.Stat(above); err == nil {
			t.Fatalf("%%s is here, above its package", above)
		}
	}
}
`, word),
	})
	testpins.Go(spec.TB, checkout)

	build := spec.inASandboxOfItsOwn(spec.cloning(checkout)).build()

	spec.Expect(build.outcome()).To(Equal("SUCCESS"))
	// Its lint, format and compile, the program of its one tested
	// package, and that package's test.
	spec.Expect(build.passed()).To(Equal(5))
}
