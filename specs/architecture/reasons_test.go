package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// What a server refuses by name it says in its contract: every reason a
// server gives in ErrorInfo is named in the contract of the domain it is
// given in (AGENTS.md, "APIs"), so that a client reads the reason from
// the contract and never from the server's code.

// refusing are the server's packages that refuse by name, each with the
// proto packages whose contracts name its reasons. What the daemon's
// source API refuses, the control plane says on in the workspace API's
// domain (controlplane.relayed): its reasons are named in both.
var refusing = map[string][]string{
	"server/internal/build":           {"frontseat.build.v1alpha1"},
	"server/internal/wire":            {"frontseat.build.v1alpha1"},
	"server/internal/engine/evaluate": {"frontseat.build.v1alpha1"},
	"server/internal/controlplane":    {"frontseat.workspace.v1alpha1"},
	"server/internal/backstage":       {"frontseat.workspace.v1alpha1"},
	"server/internal/source":          {"frontseat.source.v1alpha1", "frontseat.workspace.v1alpha1"},
	"server/sandbox":                  {"frontseat.sandbox.v1alpha1"},
	"server/internal/refusal":         nil,
}

// domainOf is the proto package of each domain refusal names, for a
// refusal that names its domain where it is made.
var domainOf = map[string]string{
	"Build":      "frontseat.build.v1alpha1",
	"Workspace":  "frontseat.workspace.v1alpha1",
	"Credential": "frontseat.credential.v1alpha1",
	"Sandbox":    "frontseat.sandbox.v1alpha1",
	"Source":     "frontseat.source.v1alpha1",
}

// aReason is how a reason is written: upper case, words joined by "_".
var aReason = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)*$`)

// givenReason is one reason a server gives, where, and in what contracts
// it is to be named.
type givenReason struct {
	reason, at string
	contracts  []string
}

// reasonsGiven reads every reason the sources give: a value passed where
// a function takes a parameter named reason, set as a field named reason
// of a struct literal, or assigned to a variable named reason; each
// written as a literal or a constant. A reason written otherwise cannot
// be read, and is said.
func reasonsGiven(sources map[string][]byte) ([]givenReason, []string) {
	type file struct {
		dir      string
		syntax   *ast.File
		imported map[string]string
	}
	set := token.NewFileSet()
	var files []file
	constants := map[string]string{} // dir.Name -> value
	takes := map[string]int{}        // dir.Func -> index of reason
	fields := map[string]int{}       // dir.Type -> index of reason
	for _, path := range slices.Sorted(func(yield func(string) bool) {
		for path := range sources {
			if !yield(path) {
				return
			}
		}
	}) {
		f, err := parser.ParseFile(set, path, sources[path], parser.SkipObjectResolution)
		if err != nil {
			return nil, []string{err.Error()}
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		imported := map[string]string{}
		for _, spec := range f.Imports {
			p, _ := strconv.Unquote(spec.Path.Value)
			local := filepath.Base(p)
			if spec.Name != nil {
				local = spec.Name.Name
			}
			imported[local] = strings.TrimPrefix(p, module+"/")
		}
		files = append(files, file{dir, f, imported})
		for _, decl := range f.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.ValueSpec:
						if decl.Tok != token.CONST {
							continue
						}
						for i, name := range spec.Names {
							if i < len(spec.Values) {
								if lit, ok := spec.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
									value, _ := strconv.Unquote(lit.Value)
									constants[dir+"."+name.Name] = value
								}
							}
						}
					case *ast.TypeSpec:
						if st, ok := spec.Type.(*ast.StructType); ok {
							if i := reasonAt(st.Fields); i >= 0 {
								fields[dir+"."+spec.Name.Name] = i
							}
						}
					}
				}
			case *ast.FuncDecl:
				if i := reasonAt(decl.Type.Params); i >= 0 && decl.Recv == nil {
					takes[dir+"."+decl.Name.Name] = i
				}
			}
		}
	}
	var given []givenReason
	var unread []string
	for _, f := range files {
		contracts, known := refusing[f.dir]
		// read is what an expression at a reason's place says, in a
		// domain where the place names one.
		read := func(e ast.Expr, domain string) {
			at := set.Position(e.Pos()).String()
			var value string
			switch e := e.(type) {
			case *ast.BasicLit:
				value, _ = strconv.Unquote(e.Value)
			case *ast.Ident:
				var isConstant bool
				if value, isConstant = constants[f.dir+"."+e.Name]; !isConstant {
					// A parameter or a variable, whose values are read
					// where they are given.
					return
				}
			case *ast.SelectorExpr:
				pkg, isPackage := e.X.(*ast.Ident)
				var isConstant bool
				if isPackage {
					value, isConstant = constants[f.imported[pkg.Name]+"."+e.Sel.Name]
				}
				if !isConstant {
					// A field, whose values are read where it is set.
					return
				}
			default:
				unread = append(unread, at+": a reason is written as a literal or a constant, where it is given")
				return
			}
			if value == "" {
				return
			}
			if !known {
				unread = append(unread, fmt.Sprintf("%s: %s is given in %s, which is not among the packages that refuse by name", at, value, f.dir))
				return
			}
			in := contracts
			if domain != "" {
				in = []string{domainOf[domain]}
			}
			given = append(given, givenReason{value, at, in})
		}
		// domainNamed is the domain an expression names as refusal's.
		domainNamed := func(e ast.Expr) string {
			if sel, ok := e.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && f.imported[pkg.Name] == "server/internal/refusal" {
					return sel.Sel.Name
				}
			}
			return ""
		}
		ast.Inspect(f.syntax, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				var callee string
				switch fn := n.Fun.(type) {
				case *ast.Ident:
					callee = f.dir + "." + fn.Name
				case *ast.SelectorExpr:
					if pkg, ok := fn.X.(*ast.Ident); ok {
						callee = f.imported[pkg.Name] + "." + fn.Sel.Name
					}
				}
				if i, ok := takes[callee]; ok && i < len(n.Args) {
					domain := ""
					if callee == "server/internal/refusal.New" {
						domain = domainNamed(n.Args[0])
					}
					read(n.Args[i], domain)
				}
			case *ast.CompositeLit:
				domain := ""
				for _, elt := range n.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Domain" {
							domain = domainNamed(kv.Value)
						}
					}
				}
				for i, elt := range n.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if key, ok := kv.Key.(*ast.Ident); ok && strings.EqualFold(key.Name, "reason") {
							read(kv.Value, domain)
						}
						continue
					}
					if name, ok := n.Type.(*ast.Ident); ok && fields[f.dir+"."+name.Name] == i {
						if _, has := fields[f.dir+"."+name.Name]; has {
							read(elt, "")
						}
					}
				}
			case *ast.AssignStmt:
				for i, lhs := range n.Lhs {
					if name, ok := lhs.(*ast.Ident); ok && name.Name == "reason" && len(n.Rhs) == len(n.Lhs) {
						read(n.Rhs[i], "")
					}
				}
			}
			return true
		})
	}
	return given, unread
}

// reasonAt is the index of a parameter or a field named reason, counting
// each name; -1 where there is none.
func reasonAt(list *ast.FieldList) int {
	if list == nil {
		return -1
	}
	i := 0
	for _, field := range list.List {
		if len(field.Names) == 0 {
			i++
			continue
		}
		for _, name := range field.Names {
			if strings.EqualFold(name.Name, "reason") {
				return i
			}
			i++
		}
	}
	return -1
}

// unnamed holds each reason given to the contracts it is given in:
// each names it, as a whole word.
func unnamed(given []givenReason, contracts map[string]string) []string {
	var out []string
	for _, g := range given {
		if !aReason.MatchString(g.reason) {
			out = append(out, fmt.Sprintf("%s: the reason %q is not written as a reason is, in capitals joined by _", g.at, g.reason))
			continue
		}
		named := regexp.MustCompile(`\b` + g.reason + `\b`)
		for _, contract := range g.contracts {
			if !named.MatchString(contracts[contract]) {
				out = append(out, fmt.Sprintf("%s: %s is given and %s does not name it", g.at, g.reason, contract))
			}
		}
	}
	return out
}

// contractsOf is the text of each proto package's files beneath a
// directory, by the package each says.
func contractsOf(t *testing.T, dir string) map[string]string {
	t.Helper()
	declared := regexp.MustCompile(`(?m)^package ([a-z0-9.]+);`)
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".proto") {
			return err
		}
		raw, err := os.ReadFile(path)
		if m := declared.FindSubmatch(raw); m != nil {
			out[string(m[1])] += string(raw)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// serverSources are the server's Go files that are not tests, test
// support or fixtures, by their path from the repository's root.
func serverSources(t *testing.T, root string) map[string][]byte {
	t.Helper()
	sources := map[string][]byte{}
	err := filepath.WalkDir(filepath.Join(root, "server"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || d.Name() == "testsupport" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".pb.go") || strings.HasSuffix(path, ".connect.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		rel, _ := filepath.Rel(root, path)
		sources[filepath.ToSlash(rel)] = raw
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return sources
}

func TestEveryReasonAServerGivesIsNamedInItsContract(t *testing.T) {
	root := repositoryRoot(t)
	given, unread := reasonsGiven(serverSources(t, root))
	if len(given) < 40 {
		t.Fatalf("found %d reasons given; the server's refusals were not read", len(given))
	}
	for _, broken := range append(unread, unnamed(given, contractsOf(t, filepath.Join(root, "api")))...) {
		t.Error(broken)
	}
}

// TestAReasonNoContractNamesIsFound is the control: a reason given by
// each way a server gives one, named in its contract, is allowed; the
// same reason, named in no contract or in another domain's, is refused;
// and one written so that it cannot be read is said.
func TestAReasonNoContractNamesIsFound(t *testing.T) {
	source := `package build
import (
	"connectrpc.com/connect/v2"
	"github.com/frontseat-dev/frontseat/server/internal/refusal"
)
const busy = "THING_BUSY"
type refused struct {
	code   connect.Code
	reason string
}
func refuse(code connect.Code, reason, message string) error {
	return refusal.New(refusal.Build, code, reason, message)
}
func calls() {
	_ = refuse(connect.CodeNotFound, "THING_NOT_FOUND", "no thing")
	_ = refuse(connect.CodeAborted, busy, "busy")
	_ = &refused{connect.CodeInvalidArgument, "THING_INVALID"}
	_ = refusal.New(refusal.Credential, connect.CodeInvalidArgument, "KEY_INVALID", "no key")
	fallback, reason := 1, "THING_ENDING"
	_, _ = fallback, reason
}
`
	refusal := `package refusal
const PageTokenInvalid = "PAGE_TOKEN_INVALID"
func New(domain string, code int, reason, message string) error { return nil }
`
	sources := map[string][]byte{
		"server/internal/build/build.go":     []byte(source),
		"server/internal/refusal/refusal.go": []byte(refusal),
	}
	given, unread := reasonsGiven(sources)
	if len(unread) != 0 || len(given) != 5 {
		t.Fatalf("read %v, and could not read %v; want five reasons read", given, unread)
	}
	every := map[string]string{
		"frontseat.build.v1alpha1":      "// NOT_FOUND (THING_NOT_FOUND), ABORTED (THING_BUSY), THING_INVALID, THING_ENDING",
		"frontseat.credential.v1alpha1": "// INVALID_ARGUMENT (KEY_INVALID)",
	}
	if found := unnamed(given, every); len(found) != 0 {
		t.Errorf("reasons each contract names are refused: %v", found)
	}
	for name, contracts := range map[string]map[string]string{
		"one left out":            {"frontseat.build.v1alpha1": every["frontseat.build.v1alpha1"], "frontseat.credential.v1alpha1": "//"},
		"in another's domain":     {"frontseat.build.v1alpha1": every["frontseat.build.v1alpha1"] + " KEY_INVALID", "frontseat.credential.v1alpha1": "//"},
		"as a part of another's":  {"frontseat.build.v1alpha1": "// THING_NOT_FOUND THING_BUSY THING_INVALID THING_ENDINGS", "frontseat.credential.v1alpha1": every["frontseat.credential.v1alpha1"]},
		"a constant's left out":   {"frontseat.build.v1alpha1": "// THING_NOT_FOUND THING_INVALID THING_ENDING", "frontseat.credential.v1alpha1": every["frontseat.credential.v1alpha1"]},
		"a struct's field's left": {"frontseat.build.v1alpha1": "// THING_NOT_FOUND THING_BUSY THING_ENDING", "frontseat.credential.v1alpha1": every["frontseat.credential.v1alpha1"]},
	} {
		if found := unnamed(given, contracts); len(found) != 1 {
			t.Errorf("%s: found %v; want it alone", name, found)
		}
	}
	computed := strings.Replace(source, `"THING_NOT_FOUND"`, `strings.ToUpper("thing_not_found")`, 1)
	if _, unread := reasonsGiven(map[string][]byte{"server/internal/build/build.go": []byte(computed), "server/internal/refusal/refusal.go": []byte(refusal)}); len(unread) != 1 {
		t.Errorf("a reason that cannot be read is not said: %v", unread)
	}
	elsewhere := map[string][]byte{"server/internal/other/other.go": []byte(strings.Replace(source, "package build", "package other", 1)), "server/internal/refusal/refusal.go": []byte(refusal)}
	if _, unread := reasonsGiven(elsewhere); len(unread) == 0 {
		t.Error("a reason given by a package that is not known to refuse by name is not said")
	}
}

// clientContracts are the proto packages whose refusals a client of the Go SDK
// reads.
var clientContracts = []string{"frontseat.build.v1alpha1", "frontseat.workspace.v1alpha1", "frontseat.credential.v1alpha1"}

// sdkReasons are the reasons the Go SDK names (its constants Reason…),
// by their value, each with where it is named.
func sdkReasons(t *testing.T, dir string) map[string]string {
	t.Helper()
	set := token.NewFileSet()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(set, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for name, reason := range stringConstants(f) {
			if strings.HasPrefix(name, "Reason") {
				out[reason] = filepath.Base(path) + ": " + name
			}
		}
	}
	return out
}

// reasonsMissed compares the reasons servers give in the public
// contracts' domains with those the SDK names: each given is named, and
// each named is given.
func reasonsMissed(given []givenReason, named map[string]string) []string {
	clients := map[string]string{}
	for _, g := range given {
		for _, contract := range g.contracts {
			if slices.Contains(clientContracts, contract) {
				clients[g.reason] = g.at
			}
		}
	}
	var out []string
	for _, reason := range slices.Sorted(maps.Keys(clients)) {
		if _, ok := named[reason]; !ok {
			out = append(out, fmt.Sprintf("%s: %s is given to a client, and the Go SDK names no Reason constant for it", clients[reason], reason))
		}
	}
	for _, reason := range slices.Sorted(maps.Keys(named)) {
		if _, ok := clients[reason]; !ok {
			out = append(out, fmt.Sprintf("%s: the Go SDK names %s, which no server gives a client", named[reason], reason))
		}
	}
	return out
}

func TestEveryReasonAClientIsGivenIsOneTheGoSDKNames(t *testing.T) {
	root := repositoryRoot(t)
	given, _ := reasonsGiven(serverSources(t, root))
	named := sdkReasons(t, filepath.Join(root, "sdk", "go"))
	if len(named) < 20 {
		t.Fatalf("found %d reasons the Go SDK names; its constants were not read", len(named))
	}
	for _, missed := range reasonsMissed(given, named) {
		t.Error(missed)
	}
}

// TestAReasonTheGoSDKMissesOrMakesUpIsFound is the control: the SDK
// naming what is given passes; one left out, and one named that no
// server gives, are each found, and a reason given only in the sandbox
// service's domain is no client's.
func TestAReasonTheGoSDKMissesOrMakesUpIsFound(t *testing.T) {
	given := []givenReason{
		{"THING_NOT_FOUND", "build.go:1", []string{"frontseat.build.v1alpha1"}},
		{"KEY_INVALID", "credential.go:1", []string{"frontseat.credential.v1alpha1"}},
		{"SLOT_BUSY", "sandbox.go:1", []string{"frontseat.sandbox.v1alpha1"}},
	}
	if missed := reasonsMissed(given, map[string]string{"THING_NOT_FOUND": "a", "KEY_INVALID": "b"}); len(missed) != 0 {
		t.Errorf("the SDK naming every reason a client is given is refused: %v", missed)
	}
	for name, named := range map[string]map[string]string{
		"one left out":  {"THING_NOT_FOUND": "a"},
		"one made up":   {"THING_NOT_FOUND": "a", "KEY_INVALID": "b", "THING_LOST": "c"},
		"the sandbox's": {"THING_NOT_FOUND": "a", "KEY_INVALID": "b", "SLOT_BUSY": "c"},
	} {
		if missed := reasonsMissed(given, named); len(missed) != 1 {
			t.Errorf("%s: found %v; want it alone", name, missed)
		}
	}
}
