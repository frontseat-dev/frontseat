// Package testforge gives a test something to clone: a git repository
// that holds two Go modules and a directory of documents, and the
// Backstage entities that say where each is, whose hello entity depends
// on its greet one, for a fake Backstage to serve (backstagetest).
package testforge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/testsupport/commits"
	"github.com/frontseat-dev/frontseat/testsupport/testfiles"
	"github.com/frontseat-dev/frontseat/testsupport/testpins"
)

// ToolLock is the go.sum of the module tool, of the repository lab: the
// checksums of the three modules it is built with, and of the go.mod
// files the go command read to choose them.
const ToolLock = `github.com/cpuguy83/go-md2man/v2 v2.0.4/go.mod h1:tgQtvFlXSQOSOSIRvRPT7W67SCa46tRHOmNcaadrF8o=
github.com/inconshreveable/mousetrap v1.1.0 h1:wN+x4NVGpMsO7ErUn/mUI3vEoE6Jt13X2s0bqwp9tc8=
github.com/inconshreveable/mousetrap v1.1.0/go.mod h1:vpF70FUmC8bwa3OWnCshd2FqLfsEA9PFc4w1p2J65bw=
github.com/russross/blackfriday/v2 v2.1.0/go.mod h1:+Rmxgy9KzJVeS9/2gXHxylqXiyQDYRxCVz55jmeOWTM=
github.com/spf13/cobra v1.8.1 h1:e5/vxKd/rZsfSJMUX1agtjeTDf+qv1/JdBF8gg5k9ZM=
github.com/spf13/cobra v1.8.1/go.mod h1:wHxEcudfqmLYa8iTfL+OuZPbBZkmvliBWKIezN3kD9Y=
github.com/spf13/pflag v1.0.5 h1:iy+VFUOCP1a+8yFto/drg2CJ5u0yRoB7fZw3DKv/JXA=
github.com/spf13/pflag v1.0.5/go.mod h1:McXfInJRrz4CZXVZOBLb0bTZqETkiAhM9Iw0y3An2Bg=
gopkg.in/check.v1 v0.0.0-20161208181325-20d25e280405/go.mod h1:Co6ibVJAznAaIkqp8huTwlJQCZ016jof/cbN4VW5Yz0=
gopkg.in/yaml.v3 v3.0.1/go.mod h1:K4uyk7z7BCEPqu6E+C64Yfv1cQ7kz7rIZviUmN+EgEM=
`

// Forge is repositories and the entities of a catalog, in one directory. The repository
// "services" holds the entities hello, which depends on greet, and greet;
// "platform" holds gateway, which depends on greet in the other
// repository, and log. Each also holds a directory no entity governs.
//
// Two more hold what misbehaves on purpose: "lab" holds broken, whose
// code the linter refuses, and slow, whose test takes two minutes; "bare"
// holds unpinned, in a repository that pins no tools.
type Forge struct {
	// Dir is the directory that holds both.
	Dir string
	// Remote is the URL of the repository "services".
	Remote string
	// base is the address the forge is found at by whoever clones from
	// it: its directory as a file URL, or where it is served.
	base string
}

// embeds is a program that embeds a file, a directory, a file whose name
// has a space in it and what a pattern matches, as the go command reads
// the directive; embedsTest is a test of it that embeds its fixture.
const embeds = `package main

import (
	"embed"
	"fmt"
)

//go:embed asset.txt
var asset string

//go:embed static "with space.txt"
//go:embed templates/*.tmpl
var files embed.FS

func main() {
	spaced, _ := files.ReadFile("with space.txt")
	deep, _ := files.ReadFile("static/deep/more.css")
	page, _ := files.ReadFile("templates/page.tmpl")
	fmt.Print(asset, string(spaced), string(deep), string(page))
}
`

const embedsTest = `package main

import (
	_ "embed"
	"testing"
)

//go:embed testdata/golden.txt
var golden string

func TestEmbedsItsFixture(t *testing.T) {
	if golden != "golden\n" {
		t.Fatalf("embedded %q", golden)
	}
}
`

// vandal tries to change a file it was given, a fixture of its own, as a
// tool that formats in
// place or a test that tidies up would, and passes only if the file is
// then as it was. How it was stopped is the worker's business: one
// refuses, another lets it say it did and changes nothing.
const vandal = `package vandal

import (
	"bytes"
	"os"
	"testing"
)

func TestCannotChangeWhatItWasGiven(t *testing.T) {
	given, err := os.ReadFile("testdata/given.txt")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod("testdata/given.txt", 0o666)
	if file, err := os.OpenFile("testdata/given.txt", os.O_WRONLY|os.O_APPEND, 0); err == nil {
		_, _ = file.WriteString("// changed\n")
		file.Close()
	}
	_ = os.WriteFile("testdata/given.txt", []byte("package changed\n"), 0o666)

	now, err := os.ReadFile("testdata/given.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(now, given) {
		t.Errorf("changed a file it was given, to %q", now)
	}
	if info, err := os.Stat("testdata/given.txt"); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Errorf("changed who may write a file it was given: %v, %v", info.Mode(), err)
	}
}
`

// Make makes a forge that is cloned from its directory, by a daemon of
// this machine.
func Make(t testing.TB) Forge {
	t.Helper()
	dir := t.TempDir()
	return MakeAt(t, dir, "file://"+dir)
}

// Served makes a forge that is cloned over HTTP, as a sandbox clones:
// a sandbox is given no directory of the machine.
func Served(t testing.TB) Forge {
	t.Helper()
	dir := t.TempDir()
	return MakeAt(t, dir, Serve(t, dir))
}

// MakeAt fills a caller-owned directory with fixture repositories found at
// base. The caller owns the directory and any server serving it. No test
// handle is retained after construction.
func MakeAt(t testing.TB, dir, base string) Forge {
	t.Helper()
	services := filepath.Join(dir, "services")
	// greet is a library. hello, beside it, and gateway, in another
	// repository, call it: each go.mod requires it on its fifth line.
	const calls = "package main\n\nimport \"example.com/greet\"\n\nfunc main() { println(greet.Hello()) }\n"
	write(t, services, "hello/go.mod", "module example.com/hello\n\ngo 1.27\n\nrequire example.com/greet v0.0.0\n")
	write(t, services, "hello/main.go", calls)
	write(t, services, "greet/go.mod", "module example.com/greet\n\ngo 1.27\n")
	write(t, services, "greet/greet.go", "package greet\n\n// Hello is a greeting.\nfunc Hello() string { return \"hello\" }\n")
	write(t, services, "docs/guide.md", "# guide\n")
	commit(t, services, true)
	platform := filepath.Join(dir, "platform")
	write(t, platform, "gateway/go.mod", "module example.com/gateway\n\ngo 1.27\n\nrequire example.com/greet v0.0.0\n")
	write(t, platform, "gateway/main.go", calls)
	write(t, platform, "log/go.mod", "module example.com/log\n\ngo 1.27\n")
	write(t, platform, "log/log.go", "package log\n")
	write(t, platform, "notes/readme.md", "# notes\n")
	commit(t, platform, true)
	lab := filepath.Join(dir, "lab")
	write(t, lab, "broken/go.mod", "module example.com/broken\n\ngo 1.27\n")
	// Code after a return: the linter refuses it, and nothing else does.
	write(t, lab, "broken/main.go", "package main\n\nfunc main() {\n\treturn\n\tprintln(\"never\")\n}\n")
	write(t, lab, "failing/go.mod", "module example.com/failing\n\ngo 1.27\n")
	write(t, lab, "failing/failing.go", "package failing\n")
	write(t, lab, "failing/failing_test.go", "package failing\n\nimport \"testing\"\n\nfunc TestFails(t *testing.T) { t.Fatal(\"this test fails on purpose\") }\n")
	// Two that pass only if they are stopped. What they prove is what
	// a worker does to them, so each is different every time and is
	// run every time: a result kept from an earlier run would say what
	// a worker did then. A test is run again where its program differs,
	// and a remark changes no program: the moment is in a test's words.
	once := "\nfunc TestOfThisRun(t *testing.T) { t.Log(\"" + time.Now().Format(time.RFC3339Nano) + "\") }\n"
	write(t, lab, "caller/go.mod", "module example.com/caller\n\ngo 1.27\n")
	write(t, lab, "caller/caller.go", "package caller\n")
	write(t, lab, "caller/caller_test.go", "package caller\n\nimport (\n\t\"net\"\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestCannotOpenAConnection(t *testing.T) {\n\tfor _, to := range []string{\"github.com:443\", \"1.1.1.1:443\"} {\n\t\tif conn, err := net.DialTimeout(\"tcp\", to, 3*time.Second); err == nil {\n\t\t\tconn.Close()\n\t\t\tt.Errorf(\"opened a connection to %s\", to)\n\t\t}\n\t}\n}\n"+once)
	write(t, lab, "snoop/go.mod", "module example.com/snoop\n\ngo 1.27\n")
	write(t, lab, "snoop/snoop.go", "package snoop\n")
	write(t, lab, "snoop/snoop_test.go", "package snoop\n\nimport (\n\t\"net\"\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestCannotReachTheRunnerNorWriteTheSlot(t *testing.T) {\n\tif c, err := net.Dial(\"unix\", \"/slot/runner\"); err == nil {\n\t\tc.Close()\n\t\tt.Error(\"reached the runner\")\n\t}\n\tif err := os.WriteFile(\"/slot/planted\", nil, 0o644); err == nil {\n\t\tt.Error(\"wrote the slot\")\n\t}\n}\n"+once)
	// vandal's test passes only if the file it was given is, after all it
	// tried, as it was given.
	// What a worker gives an action it keeps for every action after, so
	// a file one changed would be changed for all of them.
	write(t, lab, "vandal/go.mod", "module example.com/vandal\n\ngo 1.27\n")
	write(t, lab, "vandal/vandal.go", "package vandal\n")
	write(t, lab, "vandal/testdata/given.txt", "as it was given\n")
	write(t, lab, "vandal/vandal_test.go", vandal+once)
	// What the go command reads of a module besides its Go files: what
	// a file embeds, a test's own fixtures, and assembly with the header
	// it includes.
	write(t, lab, "embeds/go.mod", "module example.com/embeds\n\ngo 1.27\n")
	write(t, lab, "embeds/main.go", embeds)
	write(t, lab, "embeds/main_test.go", embedsTest)
	write(t, lab, "embeds/asset.txt", "an asset\n")
	write(t, lab, "embeds/with space.txt", "a name with a space\n")
	write(t, lab, "embeds/static/site.css", "body {}\n")
	write(t, lab, "embeds/static/deep/more.css", "p {}\n")
	write(t, lab, "embeds/templates/page.tmpl", "{{.}}\n")
	write(t, lab, "embeds/testdata/golden.txt", "golden\n")
	write(t, lab, "fixtures/go.mod", "module example.com/fixtures\n\ngo 1.27\n")
	write(t, lab, "fixtures/fixtures.go", "package fixtures\n")
	write(t, lab, "fixtures/fixtures_test.go", "package fixtures\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestReadsItsFixture(t *testing.T) {\n\tif got, err := os.ReadFile(\"testdata/golden.txt\"); err != nil || string(got) != \"golden\\n\" {\n\t\tt.Fatalf(\"read %q: %v\", got, err)\n\t}\n}\n")
	write(t, lab, "fixtures/testdata/golden.txt", "golden\n")
	write(t, lab, "assembly/go.mod", "module example.com/assembly\n\ngo 1.27\n")
	write(t, lab, "assembly/add.go", "package assembly\n\n// Add is written in assembly.\nfunc Add(a, b int64) int64\n")
	write(t, lab, "assembly/frame.h", "#define FRAME $0-24\n")
	write(t, lab, "assembly/add_arm64.s", "#include \"textflag.h\"\n#include \"frame.h\"\n\nTEXT ·Add(SB), NOSPLIT, FRAME\n\tMOVD a+0(FP), R0\n\tMOVD b+8(FP), R1\n\tADD R1, R0\n\tMOVD R0, ret+16(FP)\n\tRET\n")
	write(t, lab, "assembly/add_amd64.s", "#include \"textflag.h\"\n#include \"frame.h\"\n\nTEXT ·Add(SB), NOSPLIT, FRAME\n\tMOVQ a+0(FP), AX\n\tADDQ b+8(FP), AX\n\tMOVQ AX, ret+16(FP)\n\tRET\n")
	write(t, lab, "assembly/add_test.go", "package assembly\n\nimport \"testing\"\n\nfunc TestAdds(t *testing.T) {\n\tif got := Add(2, 3); got != 5 {\n\t\tt.Fatalf(\"Add(2, 3) = %d\", got)\n\t}\n}\n")
	write(t, lab, "slow/go.mod", "module example.com/slow\n\ngo 1.27\n")
	write(t, lab, "slow/slow.go", "package slow\n")
	write(t, lab, "slow/slow_test.go", "package slow\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSlow(t *testing.T) { time.Sleep(2 * time.Minute) }\n")
	// Entities with actions of their own, which read what other actions
	// left. Each is a Go module with two programs: write, which writes
	// made/word.txt, and read, which prints it and fails where it is not
	// there.
	action := func(name, command, rest string) string {
		return "      " + name + ":\n        command: " + command + "\n        tools: [go]\n" + rest
	}
	inputs := func(each ...string) string {
		return "        inputs: [" + strings.Join(each, ", ") + "]\n"
	}
	writes := inputs("go.mod", "\"write/**\"") + "        outputs: [made/]\n"
	reads := func(more ...string) string {
		return inputs(append([]string{"go.mod", "\"read/**\""}, more...)...)
	}
	// What an entity's own catalog file says it depends on: what its
	// actions read of another entity.
	depends := map[string]string{"borrower": "  dependsOn: [component:chain]\n", "unclaimed": "  dependsOn: [component:chain]\n"}
	own := func(entity, word string, actions ...string) {
		write(t, lab, entity+"/go.mod", "module example.com/"+entity+"\n\ngo 1.27\n")
		write(t, lab, entity+"/write/main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tif err := os.MkdirAll(\"made\", 0o755); err != nil {\n\t\tpanic(err)\n\t}\n\tif err := os.WriteFile(\"made/word.txt\", []byte(\"hello\\n\"), 0o644); err != nil {\n\t\tpanic(err)\n\t}\n}\n")
		write(t, lab, entity+"/read/main.go", "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tword, err := os.ReadFile(\""+word+"\")\n\tif err != nil {\n\t\tfmt.Println(\"nothing was made:\", err)\n\t\tos.Exit(1)\n\t}\n\tfmt.Print(string(word))\n}\n")
		write(t, lab, entity+"/fail/main.go", "package main\n\nimport \"os\"\n\nfunc main() { os.Exit(1) }\n")
		write(t, lab, entity+"/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: "+entity+"\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n"+depends[entity]+"  frontseat:\n    actions:\n"+strings.Join(actions, ""))
	}
	// chain: read reads made/, which write leaves; report reads all the
	// Go plugin's compile left.
	own("chain", "made/word.txt",
		action("write", "[go, run, ./write]", writes),
		action("read", "[go, run, ./read]", reads("\":made/\"")),
		action("report", "[go, version]", inputs("\":go.compile\"")))
	// apart: read does not read made/.
	own("apart", "made/word.txt",
		action("write", "[go, run, ./write]", writes),
		action("read", "[go, run, ./read]", reads()))
	// globbed: read reads made/*, a pattern, which names no output.
	own("globbed", "made/word.txt",
		action("write", "[go, run, ./write]", writes),
		action("read", "[go, run, ./read]", reads("\"made/*\"")))
	// doubly: read reads write, and made/ which write leaves: one need.
	own("doubly", "made/word.txt",
		action("write", "[go, run, ./write]", writes),
		action("read", "[go, run, ./read]", reads("\":custom.write\"", "\":made/\"")))
	// blocked: what read reads fails to be written.
	own("blocked", "made/word.txt",
		action("write", "[go, run, ./fail]", inputs("go.mod", "\"fail/**\"")+"        outputs: [made/]\n"),
		action("read", "[go, run, ./read]", reads("\":made/\"")))
	// hollow: write says it writes made/, and writes nothing.
	own("hollow", "made/word.txt",
		action("write", "[go, version]", "        outputs: [made/]\n"))
	// circle: two actions that read each other.
	own("circle", "made/word.txt",
		action("first", "[go, version]", inputs("\":custom.second\"")+"        outputs: [first/]\n"),
		action("second", "[go, version]", inputs("\":custom.first\"")+"        outputs: [second/]\n"))
	// stray: read reads an action of an entity nobody has.
	own("stray", "made/word.txt",
		action("read", "[go, run, ./read]", reads("\"//nowhere:custom.write\"")))
	// borrower: read reads made/ of chain, another entity.
	own("borrower", "../chain/made/word.txt",
		action("read", "[go, run, ./read]", reads("\"//chain:made/\"")))
	// unclaimed: read reads spare/ of chain, which no action leaves.
	own("unclaimed", "../chain/made/word.txt",
		action("read", "[go, run, ./read]", reads("\"//chain:spare/\"")))
	// twofold: write leaves a file named as the action is printed, so
	// the label names both.
	own("twofold", "made/word.txt",
		action("write", "[go, version]", "        outputs: [custom.write]\n"),
		action("read", "[go, run, ./read]", reads("\":custom.write\"")))
	// rivals: write and copy both leave made/, which read names.
	own("rivals", "made/word.txt",
		action("write", "[go, run, ./write]", writes),
		action("copy", "[go, run, ./write]", writes),
		action("read", "[go, run, ./read]", reads("\":made/\"")))
	// idle: read reads the Go plugin's lint, which leaves nothing.
	own("idle", "made/word.txt",
		action("read", "[go, run, ./read]", reads("\":go.lint\"")))
	// shadow: the repository holds made/word.txt, and read reads both
	// that and what write leaves at made/.
	own("shadow", "made/word.txt",
		action("write", "[go, run, ./write]", writes),
		action("read", "[go, run, ./read]", reads("\"made/**\"", "\":made/\"")))
	write(t, lab, "shadow/made/word.txt", "committed\n")
	// picky: both writes made/ and spare/; made reads made/ and spare
	// reads spare/, and each prints spare/word.txt.
	own("picky", "spare/word.txt",
		action("both", "[go, run, ./both]", inputs("go.mod", "\"both/**\"")+"        outputs: [made/, spare/]\n"),
		action("made", "[go, run, ./read]", reads("\":made/\"")),
		action("spare", "[go, run, ./read]", reads("\":spare/\"")))
	write(t, lab, "picky/both/main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tfor _, dir := range []string{\"made\", \"spare\"} {\n\t\tif err := os.MkdirAll(dir, 0o755); err != nil {\n\t\t\tpanic(err)\n\t\t}\n\t\tif err := os.WriteFile(dir+\"/word.txt\", []byte(dir+\"\\n\"), 0o644); err != nil {\n\t\t\tpanic(err)\n\t\t}\n\t}\n}\n")
	// Modules with programs: a package named main, which the go command
	// makes a file to run of, named after its directory.
	//
	// programs has a library and three programs. One of them, runner,
	// runs the program hello that the compile left in the action's home,
	// and says which programs are there.
	program := func(says string) string {
		return "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"" + says + "\") }\n"
	}
	write(t, lab, "programs/go.mod", "module example.com/programs\n\ngo 1.27\n")
	write(t, lab, "programs/lib/word.go", "package lib\n\n// Word is what hello says.\nfunc Word() string { return \"hello from a program\" }\n")
	write(t, lab, "programs/cmd/hello/main.go", "package main\n\nimport (\n\t\"fmt\"\n\n\t\"example.com/programs/lib\"\n)\n\nfunc main() { fmt.Println(lib.Word()) }\n")
	write(t, lab, "programs/cmd/bye/main.go", program("bye"))
	write(t, lab, "programs/runner/main.go", "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\t\"os/exec\"\n\t\"path/filepath\"\n\t\"strings\"\n)\n\nfunc main() {\n\thome, err := os.UserHomeDir()\n\tif err != nil {\n\t\tpanic(err)\n\t}\n\tbin := filepath.Join(home, \"go\", \"bin\")\n\tsaid, err := exec.Command(filepath.Join(bin, \"hello\")).CombinedOutput()\n\tif err != nil {\n\t\tfmt.Println(\"no program was left:\", err)\n\t\tos.Exit(1)\n\t}\n\tfmt.Print(string(said))\n\tleft, _ := os.ReadDir(bin)\n\tvar names []string\n\tfor _, each := range left {\n\t\tnames = append(names, each.Name())\n\t}\n\tfmt.Println(\"left:\", strings.Join(names, \" \"))\n}\n")
	write(t, lab, "programs/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: programs\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n"+
		action("run", "[go, run, ./runner]", inputs("go.mod", "\"runner/**\"", "\":go.compile\"")))
	// list says what programs an action was handed.
	const list = "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\t\"path/filepath\"\n\t\"strings\"\n)\n\nfunc main() {\n\thome, err := os.UserHomeDir()\n\tif err != nil {\n\t\tpanic(err)\n\t}\n\tleft, _ := os.ReadDir(filepath.Join(home, \"go\", \"bin\"))\n\tvar names []string\n\tfor _, each := range left {\n\t\tnames = append(names, each.Name())\n\t}\n\tfmt.Println(\"handed:\", strings.Join(names, \" \"))\n\tif err := os.WriteFile(\"handed.txt\", []byte(strings.Join(names, \" \")+\"\\n\"), 0o644); err != nil {\n\t\tpanic(err)\n\t}\n}\n"
	lists := func(reads ...string) string {
		return inputs(append([]string{"go.mod", "\"list/**\""}, reads...)...) + "        outputs: [handed.txt]\n"
	}
	// more has the program extra, and same a program of the name hello
	// that says something else than the hello of programs. gather's own
	// action list reads the compile of programs and of more, and
	// collide's that of programs and of same. collide has two more:
	// after, which reads what list leaves, and beside, which reads
	// nothing of it.
	write(t, lab, "more/go.mod", "module example.com/more\n\ngo 1.27\n")
	write(t, lab, "more/cmd/extra/main.go", program("extra"))
	write(t, lab, "same/go.mod", "module example.com/same\n\ngo 1.27\n")
	write(t, lab, "same/cmd/hello/main.go", program("another hello"))
	for entity, other := range map[string]string{"gather": "more", "collide": "same"} {
		write(t, lab, entity+"/go.mod", "module example.com/"+entity+"\n\ngo 1.27\n")
		write(t, lab, entity+"/list/main.go", list)
		actions := action("list", "[go, run, ./list]", lists("\"//programs:go.compile\"", "\"//"+other+":go.compile\""))
		if entity == "collide" {
			actions += action("after", "[go, version]", inputs("\":handed.txt\"")) + action("beside", "[go, version]", "")
		}
		write(t, lab, entity+"/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: "+entity+"\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  dependsOn: [component:"+other+", component:programs]\n  frontseat:\n    actions:\n"+actions)
	}
	// clash has two programs in directories of one name: the go command
	// writes both to one file.
	write(t, lab, "clash/go.mod", "module example.com/clash\n\ngo 1.27\n")
	write(t, lab, "clash/a/tool/main.go", program("a"))
	write(t, lab, "clash/b/tool/main.go", program("b"))
	write(t, lab, "clash/list/main.go", list)
	write(t, lab, "clash/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: clash\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n"+action("list", "[go, run, ./list]", lists("\":go.compile\"")))
	// generator has a program gen, and a library gen that holds a file
	// the go command leaves out of every build: it is no program.
	write(t, lab, "generator/go.mod", "module example.com/generator\n\ngo 1.27\n")
	write(t, lab, "generator/cmd/gen/main.go", program("gen"))
	write(t, lab, "generator/gen/gen.go", "package gen\n")
	write(t, lab, "generator/gen/make.go", "//go:build ignore\n\npackage main\n\nfunc main() {}\n")
	write(t, lab, "generator/list/main.go", list)
	write(t, lab, "generator/catalog-info.yaml", "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: generator\nspec:\n  type: service\n  lifecycle: production\n  owner: team\n  frontseat:\n    actions:\n"+action("list", "[go, run, ./list]", lists("\":go.compile\"")))
	// forarm and foramd each have one program, for one architecture
	// alone: on any worker, one of the two leaves no program.
	for name, architecture := range map[string]string{"forarm": "arm64", "foramd": "amd64"} {
		write(t, lab, name+"/go.mod", "module example.com/"+name+"\n\ngo 1.27\n")
		write(t, lab, name+"/lib/lib.go", "package lib\n")
		write(t, lab, name+"/cmd/only/main_"+architecture+".go", program("only on "+architecture))
	}
	// outsider requires a module nothing in the company holds. twins is
	// two modules that go by one name.
	write(t, lab, "outsider/go.mod", "module example.com/outsider\n\ngo 1.27\n\nrequire example.com/elsewhere v1.0.0\n")
	write(t, lab, "outsider/outsider.go", "package outsider\n")
	// tool requires a module from outside the company, which requires two
	// more. Its go.sum is as go mod tidy wrote it.
	write(t, lab, "tool/go.mod", "module example.com/tool\n\ngo 1.27\n\nrequire github.com/spf13/cobra v1.8.1\n\nrequire (\n\tgithub.com/inconshreveable/mousetrap v1.1.0 // indirect\n\tgithub.com/spf13/pflag v1.0.5 // indirect\n)\n")
	write(t, lab, "tool/go.sum", ToolLock)
	write(t, lab, "tool/main.go", "package main\n\nimport \"github.com/spf13/cobra\"\n\nfunc main() { _ = (&cobra.Command{Use: \"tool\"}).Execute() }\n")
	write(t, lab, "tool/main_test.go", "package main\n\nimport \"testing\"\n\nfunc TestNothing(t *testing.T) {}\n")
	write(t, lab, "twins/one/go.mod", "module example.com/twin\n\ngo 1.27\n")
	write(t, lab, "twins/one/twin.go", "package twin\n")
	write(t, lab, "twins/two/go.mod", "module example.com/twin\n\ngo 1.27\n")
	write(t, lab, "twins/two/twin.go", "package twin\n")
	commit(t, lab, true)
	bare := filepath.Join(dir, "bare")
	write(t, bare, "unpinned/go.mod", "module example.com/unpinned\n\ngo 1.27\n")
	write(t, bare, "unpinned/unpinned.go", "package unpinned\n")
	write(t, bare, "frontseat.yaml", "plugins:\n  go: {}\n")
	commit(t, bare, false)
	remote := base + "/services"
	other := base + "/platform"
	write(t, dir, "catalog-info.yaml", `{apiVersion: backstage.io/v1alpha1, kind: System, metadata: {name: storefront}}
---
{apiVersion: backstage.io/v1alpha1, kind: System, metadata: {name: warehouse}}
---
{apiVersion: backstage.io/v1alpha1, kind: Group, metadata: {name: team-edge}}
---
{apiVersion: backstage.io/v1alpha1, kind: Group, metadata: {name: team-web}}
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: hello
  annotations:
    backstage.io/source-location: url:`+remote+`/tree/main/hello/
spec:
  type: service
  lifecycle: production
  system: storefront
  owner: team-web
  dependsOn: [component:greet]
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: greet
  annotations:
    backstage.io/source-location: url:`+remote+`/tree/main/greet/
spec:
  owner: team-web
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: gateway
  annotations:
    backstage.io/source-location: url:`+other+`/tree/main/gateway/
spec:
  system: storefront
  owner: team-edge
  dependsOn: [component:greet]
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: log
  annotations:
    backstage.io/source-location: url:`+other+`/tree/main/log/
spec:
  owner: team-edge
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: unplaced
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: broken
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/broken/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: failing
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/failing/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: caller
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/caller/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: snoop
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/snoop/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: embeds
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/embeds/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: fixtures
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/fixtures/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: assembly
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/assembly/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: slow
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/slow/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: chain
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/chain/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: apart
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/apart/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: globbed
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/globbed/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: rivals
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/rivals/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: doubly
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/doubly/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: twofold
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/twofold/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: idle
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/idle/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: shadow
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/shadow/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: picky
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/picky/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: unclaimed
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/unclaimed/
spec:
  dependsOn: [component:chain]
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: blocked
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/blocked/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: hollow
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/hollow/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: circle
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/circle/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: stray
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/stray/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: programs
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/programs/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: more
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/more/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: same
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/same/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: gather
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/gather/
spec:
  dependsOn: [component:more, component:programs]
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: collide
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/collide/
spec:
  dependsOn: [component:same, component:programs]
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: clash
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/clash/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: generator
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/generator/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: forarm
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/forarm/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: foramd
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/foramd/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: borrower
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/borrower/
spec:
  dependsOn: [component:chain]
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: outsider
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/outsider/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: vandal
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/vandal/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: tool
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/tool/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: twins
  annotations:
    backstage.io/source-location: url:`+base+`/lab/tree/main/twins/
---
apiVersion: backstage.io/v1alpha1
kind: Component
metadata:
  name: unpinned
  annotations:
    backstage.io/source-location: url:`+base+`/bare/tree/main/unpinned/
`)
	return Forge{Dir: dir, Remote: remote, base: base}
}

// Entities are the forge's entities, as its catalog file holds them:
// what a fake Backstage serves (backstagetest.Serve) for a control plane
// to resolve entities through.
func (f Forge) Entities(t testing.TB) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.Dir, "catalog-info.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Repository returns the URL of one of the forge's repositories.
func (f Forge) Repository(name string) string { return f.base + "/" + name }

// Write puts a file in one of the forge's repositories, as a commit on
// main.
func (f Forge) Write(t testing.TB, repository, file, content string) {
	t.Helper()
	write(t, filepath.Join(f.Dir, repository), file, content)
	f.record(t, repository)
}

// Remove takes a file out of one of the forge's repositories, as a
// commit on main.
func (f Forge) Remove(t testing.TB, repository, file string) {
	t.Helper()
	if err := os.Remove(filepath.Join(f.Dir, repository, filepath.FromSlash(file))); err != nil {
		t.Fatal(err)
	}
	f.record(t, repository)
}

func (f Forge) record(t testing.TB, repository string) {
	t.Helper()
	directory := filepath.Join(f.Dir, repository)
	commits.Git(t, directory, "add", "--all")
	commits.Git(t, directory, "commit", "--quiet", "-m", "change")
}

// commit makes a directory a git repository with one commit on main,
// pinning its tools first where it is to pin any.
func commit(t testing.TB, repository string, pinned bool) {
	t.Helper()
	if pinned {
		testpins.Go(t, repository)
	}
	// What holds secrets is ignored, as in most repositories.
	write(t, repository, ".gitignore", ".env\n")
	commits.First(t, repository)
}

func write(t testing.TB, root, name, content string) {
	t.Helper()
	testfiles.Write(t, filepath.Join(root, filepath.FromSlash(name)), content)
}
