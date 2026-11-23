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

	"google.golang.org/protobuf/reflect/protoreflect"
)

// What a server refuses by name it says in its contract: every reason a
// server gives in ErrorInfo is named in the contract of the domain it is
// given in, and, where the source says which call gives it, in what the
// contract says of that call (AGENTS.md, "APIs"), so that a client reads
// the reason from the contract and never from the server's code. A
// reason lost on the way from one service to another is not seen here:
// only what is given is read.

// refusing are the server's packages that refuse by name, each with the
// proto packages whose contracts name its reasons. What the daemon's
// source API refuses, the control plane says on in the workspace API's
// domain (controlplane.relayed): its reasons are named in both.
var refusing = map[string][]string{
	"build/daemon/build":            {"frontseat.build.v1alpha1"},
	"build/engine/wire":             {"frontseat.build.v1alpha1"},
	"build/engine/engine/evaluate":  {"frontseat.build.v1alpha1"},
	"workspace/server/controlplane": {"frontseat.workspace.v1alpha1"},
	"workspace/server/backstage":    {"frontseat.workspace.v1alpha1"},
	"workspace/box/source":          {"frontseat.source.v1alpha1", "frontseat.workspace.v1alpha1"},
	"sandbox":                       {"frontseat.sandbox.v1alpha1"},
	"core/refusal":                  nil,
}

// domainOf is the proto packages of each domain refusal names, for a
// refusal that names its domain where it is made: the control plane's
// own is said by every contract it serves.
var domainOf = map[string][]string{
	"ControlPlane": {"frontseat.workspace.v1alpha1", "frontseat.credential.v1alpha1"},
	"Build":        {"frontseat.build.v1alpha1"},
	"Workspace":    {"frontseat.workspace.v1alpha1"},
	"Credential":   {"frontseat.credential.v1alpha1"},
	"Sandbox":      {"frontseat.sandbox.v1alpha1"},
	"Source":       {"frontseat.source.v1alpha1"},
}

// aReason is how a reason is written: upper case, words joined by "_".
var aReason = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)*$`)

// givenReason is one reason a server gives, where, and in what contracts
// it is to be named.
type givenReason struct {
	reason, at string
	contracts  []string
	// code is the canonical code it is given with, as connect names it
	// ("CodeUnimplemented"), where the call or literal names one.
	code string
	// file and line are where it is given, as at says them.
	file string
	line int
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
		read := func(e ast.Expr, domain, code string) {
			where := set.Position(e.Pos())
			at := where.String()
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
				in = domainOf[domain]
			}
			given = append(given, givenReason{value, at, in, code, where.Filename, where.Line})
		}
		// codeNamed is the canonical code among some expressions, as
		// connect names it, or "" where none names one.
		codeNamed := func(among []ast.Expr) string {
			for _, e := range among {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					e = kv.Value
				}
				if sel, ok := e.(*ast.SelectorExpr); ok {
					if pkg, ok := sel.X.(*ast.Ident); ok && f.imported[pkg.Name] == "connectrpc.com/connect/v2" && strings.HasPrefix(sel.Sel.Name, "Code") {
						return sel.Sel.Name
					}
				}
			}
			return ""
		}
		// domainNamed is the domain an expression names as refusal's.
		domainNamed := func(e ast.Expr) string {
			if sel, ok := e.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && f.imported[pkg.Name] == "core/refusal" {
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
					if callee == "core/refusal.New" {
						domain = domainNamed(n.Args[0])
					}
					read(n.Args[i], domain, codeNamed(n.Args))
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
							read(kv.Value, domain, codeNamed(n.Elts))
						}
						continue
					}
					if name, ok := n.Type.(*ast.Ident); ok && fields[f.dir+"."+name.Name] == i {
						if _, has := fields[f.dir+"."+name.Name]; has {
							read(elt, "", codeNamed(n.Elts))
						}
					}
				}
			case *ast.AssignStmt:
				for i, lhs := range n.Lhs {
					if name, ok := lhs.(*ast.Ident); ok && name.Name == "reason" && len(n.Rhs) == len(n.Lhs) {
						read(n.Rhs[i], "", "")
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

// notServedByName holds each reason given with UNIMPLEMENTED to the one
// form AGENTS.md gives what a server does not serve: <WHAT>_NOT_SERVED,
// and no other suffix.
func notServedByName(given []givenReason) []string {
	var out []string
	for _, g := range given {
		if g.code == "CodeUnimplemented" && !strings.HasSuffix(g.reason, "_NOT_SERVED") {
			out = append(out, fmt.Sprintf("%s: %s is given with UNIMPLEMENTED, which refuses what is not served by <WHAT>_NOT_SERVED alone", g.at, g.reason))
		}
	}
	return out
}

// stretch is some lines of a file of the server's.
type stretch struct {
	file     string
	from, to int
}

// handlerStretches are, for each call a method of the sources handles
// (handles), the lines its handler runs: the handler's own, and those
// of what it calls in its own package, and of what those call. What is
// called is read from the source alone, without types: a function of
// the package by its name, a method of the caller's own receiver by its
// name, and any other method only where one type of the package has a
// method of that name. A call through a value, or of a method several
// types have, is not followed: what is given there is held to its
// domain's contract and to no call's.
func handlerStretches(sources map[string][]byte) (map[string][]stretch, error) {
	type declared struct {
		dir, receiver, name string
		fn                  *ast.FuncDecl
		at                  stretch
		handles             string
	}
	set := token.NewFileSet()
	var all []*declared
	functions := map[string]*declared{} // dir.name
	methods := map[string]*declared{}   // dir.type.name
	named := map[string][]*declared{}   // dir.name, of any type
	for path, source := range sources {
		f, err := parser.ParseFile(set, path, source, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		dir := filepath.ToSlash(filepath.Dir(path))
		imported := importsOf(f)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			d := &declared{dir: dir, name: fn.Name.Name, fn: fn, handles: handles(fn, imported),
				at: stretch{path, set.Position(fn.Pos()).Line, set.Position(fn.End()).Line}}
			all = append(all, d)
			if fn.Recv == nil {
				functions[dir+"."+d.name] = d
				continue
			}
			receiver := fn.Recv.List[0].Type
			if star, ok := receiver.(*ast.StarExpr); ok {
				receiver = star.X
			}
			if generic, ok := receiver.(*ast.IndexExpr); ok {
				receiver = generic.X
			}
			if of, ok := receiver.(*ast.Ident); ok {
				d.receiver = of.Name
				methods[dir+"."+of.Name+"."+d.name] = d
			}
			named[dir+"."+d.name] = append(named[dir+"."+d.name], d)
		}
	}
	// calls are what a declaration calls in its package, as far as read.
	calls := func(d *declared) []*declared {
		own := ""
		if d.fn.Recv != nil && len(d.fn.Recv.List[0].Names) == 1 {
			own = d.fn.Recv.List[0].Names[0].Name
		}
		var out []*declared
		ast.Inspect(d.fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if called := functions[d.dir+"."+fn.Name]; called != nil {
					out = append(out, called)
				}
			case *ast.SelectorExpr:
				if on, ok := fn.X.(*ast.Ident); ok && own != "" && on.Name == own {
					if called := methods[d.dir+"."+d.receiver+"."+fn.Sel.Name]; called != nil {
						out = append(out, called)
					}
					return true
				}
				if of := named[d.dir+"."+fn.Sel.Name]; len(of) == 1 {
					out = append(out, of[0])
				}
			}
			return true
		})
		return out
	}
	out := map[string][]stretch{}
	for _, handler := range all {
		if handler.handles == "" {
			continue
		}
		seen := map[*declared]bool{}
		var run func(*declared)
		run = func(d *declared) {
			if seen[d] {
				return
			}
			seen[d] = true
			out[handler.handles] = append(out[handler.handles], d.at)
			for _, called := range calls(d) {
				run(called)
			}
		}
		run(handler)
	}
	return out, nil
}

// asForACall is how a comment says a call refuses as another does: "as
// for PlanRelease". A call named for any other purpose lends it nothing.
var asForACall = regexp.MustCompile(`\bas for ((?:` + strings.Join(verbs, "|") + `)[A-Z][A-Za-z]*)\b`)

// namedBy is what the contract says of a call's refusals: the comment of
// the call, that of its service, the header of its file, which names
// what every call of the file refuses by, and the Errors of the calls of
// its package that it says it refuses as, by "as for PlanRelease", and
// those that theirs say so of in turn.
func namedBy(c contract, m protoreflect.MethodDescriptor) string {
	comment := func(d protoreflect.Descriptor) string {
		return d.ParentFile().SourceLocations().ByDescriptor(d).LeadingComments
	}
	// The header is the comment above a file's syntax statement.
	header := func(f protoreflect.FileDescriptor) string {
		return f.SourceLocations().ByPath(protoreflect.SourcePath{12}).LeadingComments
	}
	// errorsOf is the Errors block of a call's comment, where it has one.
	errorsOf := func(said string) string {
		if at := strings.Index(said, "### Errors"); at >= 0 {
			return said[at:]
		}
		return ""
	}
	seen := map[protoreflect.FullName]bool{m.FullName(): true}
	var said strings.Builder
	var follow func(string)
	follow = func(own string) {
		for _, as := range asForACall.FindAllStringSubmatch(own, -1) {
			for _, other := range c.methods() {
				if string(other.Name()) != as[1] || other.ParentFile().Package() != m.ParentFile().Package() || seen[other.FullName()] {
					continue
				}
				seen[other.FullName()] = true
				theirs := errorsOf(comment(other))
				said.WriteString(theirs)
				follow(theirs)
			}
		}
	}
	own := comment(m)
	said.WriteString(own + comment(m.Parent()) + header(m.ParentFile()))
	follow(own)
	return said.String()
}

// neverGiven are the reasons a call's handler reaches in the source and
// never gives, each with why. The source says what a handler calls, and
// not what it calls it with: what is called refuses for some of its
// callers alone.
var neverGiven = map[protoreflect.FullName]map[string]string{
	"frontseat.build.v1alpha1.GraphService.ListEntities": {
		"ENTITY_NOT_FOUND":   "releaseFailure, which says how planning a release failed, says here how reading the entities' release settings did: that names no entity",
		"RELEASE_INVALID":    "as ENTITY_NOT_FOUND: reading the settings takes no request to refuse",
		"RELEASE_NOT_SERVED": "as ENTITY_NOT_FOUND: what is not served is refused when it is released, not when it is listed",
	},
	"frontseat.credential.v1alpha1.CredentialService.ListCredentials": {
		"CREDENTIAL_HOST_INVALID": "holding checks the host a call names, and a list names none",
	},
	"frontseat.sandbox.v1alpha1.SandboxService.ListSandboxes": {
		"SANDBOX_NOT_FOUND": "a sandbox that is gone when it is observed is left out of the list, and the list is not refused",
	},
}

// unnamedByCall holds each reason a call's handler gives, as far as the
// source says which call gives it (handlerStretches), to the contract of
// that call: what the contract says of the call's refusals names it, as
// a whole word (namedBy), unless the call never gives it (never). What
// is said never to be given and is not reached, or is named after all,
// is said too: the list holds only what it must.
func unnamedByCall(c contract, sources map[string][]byte, given []givenReason, never map[protoreflect.FullName]map[string]string) []string {
	stretches, err := handlerStretches(sources)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	for _, m := range c.methods() {
		said := namedBy(c, m)
		unnamed := map[string]bool{}
		for _, at := range stretches[generatedPackage(m.ParentFile().Package())+"."+string(m.Name())] {
			for _, g := range given {
				if g.file != at.file || g.line < at.from || g.line > at.to || unnamed[g.reason] {
					continue
				}
				if regexp.MustCompile(`\b` + regexp.QuoteMeta(g.reason) + `\b`).MatchString(said) {
					continue
				}
				unnamed[g.reason] = true
				if _, excused := never[m.FullName()][g.reason]; !excused {
					out = append(out, fmt.Sprintf("%s: %s is given by %s, and what the contract says of that call does not name it", g.at, g.reason, m.FullName()))
				}
			}
		}
		for reason := range never[m.FullName()] {
			if !unnamed[reason] {
				out = append(out, fmt.Sprintf("%s is said never to give %s, and its handler does not reach it, or its contract names it: take it off the list", m.FullName(), reason))
			}
		}
	}
	slices.Sort(out)
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

// serverParts are the directories that hold what the server programs
// are made of.
var serverParts = []string{"core", "common", "sandbox", "build", "grid", "workspace", "cmd"}

// serverSources are the server's Go files that are not tests, test
// support or fixtures, by their path from the repository's root.
func serverSources(t *testing.T, root string) map[string][]byte {
	t.Helper()
	sources := map[string][]byte{}
	walk := func(path string, d os.DirEntry, err error) error {
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
	}
	// The server's parts: every directory of the repository's that holds
	// what a server program is made of.
	var err error
	for _, part := range serverParts {
		if err = filepath.WalkDir(filepath.Join(root, part), walk); err != nil {
			break
		}
	}
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

func TestWhatIsNotServedIsRefusedAsNotServed(t *testing.T) {
	given, _ := reasonsGiven(serverSources(t, repositoryRoot(t)))
	unimplemented := 0
	for _, g := range given {
		if g.code == "CodeUnimplemented" {
			unimplemented++
		}
	}
	if unimplemented < 5 {
		t.Fatalf("found %d reasons given with UNIMPLEMENTED; the codes were not read", unimplemented)
	}
	for _, broken := range notServedByName(given) {
		t.Error(broken)
	}
}

// TestAReasonOfWhatIsNotServedNamedOtherwiseIsFound is the control: a
// reason given with UNIMPLEMENTED that ends _NOT_SERVED is allowed, by a
// call and by a struct literal, as is one of another code named
// otherwise; one given with UNIMPLEMENTED named otherwise is refused, by
// either.
func TestAReasonOfWhatIsNotServedNamedOtherwiseIsFound(t *testing.T) {
	source := func(call, literal string) map[string][]byte {
		return map[string][]byte{"build/daemon/build/build.go": []byte(`package build
import connect "connectrpc.com/connect/v2"
type refused struct {
	code   connect.Code
	reason string
}
func refuse(code connect.Code, reason, message string) error { return nil }
func calls() {
	_ = refuse(connect.CodeUnimplemented, "` + call + `", "not here")
	_ = &refused{code: connect.CodeUnimplemented, reason: "` + literal + `"}
	_ = refuse(connect.CodeNotFound, "THING_NOT_FOUND", "no thing")
}
`)}
	}
	for name, c := range map[string]struct {
		call, literal string
		want          int
	}{
		"both not served":       {"THING_NOT_SERVED", "OTHER_NOT_SERVED", 0},
		"a call's otherwise":    {"CALL_NOT_LED", "OTHER_NOT_SERVED", 1},
		"a literal's otherwise": {"THING_NOT_SERVED", "OTHER_UNSUPPORTED", 1},
	} {
		given, unread := reasonsGiven(source(c.call, c.literal))
		if len(unread) != 0 || len(given) != 3 {
			t.Fatalf("%s: read %v, and could not read %v; want three reasons read", name, given, unread)
		}
		if found := notServedByName(given); len(found) != c.want {
			t.Errorf("%s: found %v; want %d", name, found, c.want)
		}
	}
}

func TestEveryReasonACallGivesIsNamedInThatCallsContract(t *testing.T) {
	root := repositoryRoot(t)
	sources := serverSources(t, root)
	given, _ := reasonsGiven(sources)
	c := contract{files: built(t, filepath.Join(root, "api")), served: served}
	for _, broken := range unnamedByCall(c, sources, given, neverGiven) {
		t.Error(broken)
	}
}

// TestAReasonItsCallsContractDoesNotNameIsFound is the control: a reason
// a handler gives, itself or through what it calls, is allowed where the
// call's own comment names it, where the Errors of a call it says it
// refuses as ("as for WaitThing") name it, and where the file's header
// does; it is refused where only another call's comment names it, the
// call naming that one for another purpose or the reason being outside
// that one's Errors. A method of the same name on
// another type is not the handler's, and what it gives is not held to
// the call. A reason on the list of those a call never gives is allowed,
// and one listed that the call does not reach is said.
func TestAReasonItsCallsContractDoesNotNameIsFound(t *testing.T) {
	for name, c := range map[string]struct {
		header, describe, wait string
		never                  map[string]string
		want                   int
	}{
		"each in its own call's comment":                   {"", "// - NOT_FOUND: no thing (THING_NOT_FOUND).", "// - ABORTED: ending (THING_ENDING).", nil, 0},
		"one in another call's alone":                      {"", "// DescribeThing returns a thing.", "// - ABORTED: ending (THING_ENDING), or no thing (THING_NOT_FOUND).", nil, 1},
		"one in the call it is said as":                    {"", "// - NOT_FOUND: as for WaitThing.", "// ### Errors\n  // - ABORTED: ending (THING_ENDING), or no thing (THING_NOT_FOUND).", nil, 0},
		"one in a call it merely names":                    {"", "// It is gone when WaitThing says so.", "// ### Errors\n  // - ABORTED: ending (THING_ENDING), or no thing (THING_NOT_FOUND).", nil, 1},
		"one outside the Errors of the call it is said as": {"", "// - NOT_FOUND: as for WaitThing.", "// WaitThing ends with no thing (THING_NOT_FOUND).\n  // ### Errors\n  // - ABORTED: ending (THING_ENDING).", nil, 1},
		"one in the file's header":                         {"// Every call refuses a thing that is gone by THING_NOT_FOUND.\n", "// DescribeThing returns a thing.", "// - ABORTED: ending (THING_ENDING).", nil, 0},
		"one the call never gives":                         {"", "// DescribeThing returns a thing.", "// - ABORTED: ending (THING_ENDING).", map[string]string{"THING_NOT_FOUND": "the fixture says so"}, 0},
		"one listed and not reached":                       {"", "// - NOT_FOUND: no thing (THING_NOT_FOUND).", "// - ABORTED: ending (THING_ENDING).", map[string]string{"THING_BUSY": "the fixture says so"}, 1},
		"one listed and named after all":                   {"", "// - NOT_FOUND: no thing (THING_NOT_FOUND).", "// - ABORTED: ending (THING_ENDING).", map[string]string{"THING_NOT_FOUND": "the fixture says so"}, 1},
	} {
		c2 := protoFixture(t, map[string]string{"fixture/called/v1/x.proto": c.header + `syntax = "proto3";
package fixture.called.v1;
message DescribeThingRequest { string thing_id = 1; }
message DescribeThingResponse {}
message WaitThingRequest { string thing_id = 1; }
message WaitThingResponse {}
service ThingService {
  ` + c.describe + `
  rpc DescribeThing(DescribeThingRequest) returns (DescribeThingResponse);
  ` + c.wait + `
  rpc WaitThing(WaitThingRequest) returns (WaitThingResponse);
}
`})
		sources := map[string][]byte{"build/daemon/build/things.go": []byte(`package build
import (
	"context"
	thingv1 "` + sdkPackage("fixture.called.v1") + `"
)
func refuse(code int, reason, message string) error { return nil }
type things struct{}
func (s *things) DescribeThing(ctx context.Context, req *thingv1.DescribeThingRequest) (*thingv1.DescribeThingResponse, error) {
	return nil, s.find()
}
func (s *things) WaitThing(ctx context.Context, req *thingv1.WaitThingRequest) (*thingv1.WaitThingResponse, error) {
	return nil, refuse(10, "THING_ENDING", "it is ending")
}
func (s *things) find() error { return refuse(5, "THING_NOT_FOUND", "no thing") }
type others struct{}
func (o *others) find() error { return refuse(5, "OTHER_NOT_FOUND", "no other") }
`)}
		given, unread := reasonsGiven(sources)
		if len(unread) != 0 || len(given) != 3 {
			t.Fatalf("%s: read %v, and could not read %v; want three reasons read", name, given, unread)
		}
		found := unnamedByCall(c2, sources, given, map[protoreflect.FullName]map[string]string{"fixture.called.v1.ThingService.DescribeThing": c.never})
		if len(found) != c.want {
			t.Errorf("%s: found %v; want %d", name, found, c.want)
		}
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
	"github.com/frontseat-dev/frontseat/core/refusal"
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
		"build/daemon/build/build.go": []byte(source),
		"core/refusal/refusal.go":     []byte(refusal),
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
	if _, unread := reasonsGiven(map[string][]byte{"build/daemon/build/build.go": []byte(computed), "core/refusal/refusal.go": []byte(refusal)}); len(unread) != 1 {
		t.Errorf("a reason that cannot be read is not said: %v", unread)
	}
	elsewhere := map[string][]byte{"server/internal/other/other.go": []byte(strings.Replace(source, "package build", "package other", 1)), "core/refusal/refusal.go": []byte(refusal)}
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
		{reason: "THING_NOT_FOUND", at: "build.go:1", contracts: []string{"frontseat.build.v1alpha1"}},
		{reason: "KEY_INVALID", at: "credential.go:1", contracts: []string{"frontseat.credential.v1alpha1"}},
		{reason: "SLOT_BUSY", at: "sandbox.go:1", contracts: []string{"frontseat.sandbox.v1alpha1"}},
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
