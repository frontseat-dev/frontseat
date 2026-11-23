package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	connect "connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/local/ports"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/plain"
	"github.com/frontseat-dev/frontseat/server/internal/told"
	"github.com/frontseat-dev/frontseat/server/internal/vcs"
	sandboxv1 "github.com/frontseat-dev/frontseat/server/sandbox/api/frontseat/sandbox/v1alpha1"
)

// Named is the label a workspace's sandbox carries the workspace's name
// under, for whoever looks after sandboxes.
const Named = "frontseat.dev/workspace"

// Made is the label a workspace's sandbox carries the request it was
// made for under: what the control plane kept before asking for it.
const Made = "frontseat.dev/workspace-request"

// unmet is a workspace that cannot be given what it asks for: by whose
// fault says how it is answered, and with which reason.
type unmet struct {
	code   connect.Code
	reason string
	why    string
}

func (r *unmet) Error() string { return r.why }

func malformed(format string, a ...any) error {
	return &unmet{connect.CodeInvalidArgument, "SPEC_INVALID", fmt.Sprintf(format, a...)}
}

// unserved is what this control plane does not serve, refused by the
// reason that names it (AGENTS.md, "APIs": <WHAT>_NOT_SERVED).
func unserved(reason, format string, a ...any) error {
	return &unmet{connect.CodeUnimplemented, reason, fmt.Sprintf(format, a...)}
}

// errAddress is what is said of an address that is none a sandbox
// fetches a repository from. It says nothing of what was written.
var errAddress = errors.New("a repository is fetched over http or https from a host's name, by a path written plainly, in letters, digits and . _ - + /, with no user, query or fragment")

// fetched reads an address a repository is fetched from: the origin it
// is fetched from over HTTP, at a host's name (plain.OriginOf), and its
// path there (plain.Path). An address is that and nothing more: one
// with a user, a query or a fragment, or of an address in place of a
// name, is none.
func fetched(address string) (plain.Origin, string, error) {
	at, err := url.Parse(address)
	if err != nil || at.User != nil || at.RawQuery != "" || at.ForceQuery || at.Fragment != "" || at.Opaque != "" {
		return plain.Origin{}, "", errAddress
	}
	origin, ok := plain.OriginOf(at.Scheme, at.Hostname(), at.Port())
	where := strings.TrimSuffix(at.EscapedPath(), "/")
	if !ok || !plain.Path(where) {
		return plain.Origin{}, "", errAddress
	}
	return origin, where, nil
}

// cloned reads where a sandbox clones a repository from, as its remote
// is written: the address the version control system says the remote is
// cloned from, which it converts where the remote is written for
// another transport, read as an address is (fetched). The daemon in the
// sandbox reads the remote by the same system, so what is refused here
// is what it would refuse, and what a rule is written for is what it
// asks. Whether a remote names someone is the system's to say, by
// vcs.ErrCredentialed, since only it reads every way a remote is
// written. With no system, a remote is read as it is written, as an
// address.
func (s *service) cloned(remote string) (plain.Origin, string, error) {
	address := remote
	if s.Fetching != nil {
		var err error
		if address, err = s.Fetching.Cloned(remote); err != nil {
			return plain.Origin{}, "", err
		}
	}
	return fetched(address)
}

// unclonable is the refusal of a repository's address that is none a
// sandbox clones from, which repeats nothing of it. One that names
// someone is refused by its own reason: a remote is kept and said with
// the workspace as it is written, and a secret is given as a
// credential.
func unclonable(err error) error {
	if errors.Is(err, vcs.ErrCredentialed) {
		return &unmet{connect.CodeInvalidArgument, "REMOTE_CREDENTIALED", "a repository's address holds a user or a password, which is kept and said with the workspace: give the address alone, and the secret as a credential"}
	}
	return malformed("a repository's address is none a sandbox clones from: %v", err)
}

// addressed holds every repository a workspace is asked for to an
// address a sandbox clones it from, before anything of it is kept or
// said back: what is refused is not repeated.
func (s *service) addressed(spec *workspacev1.WorkspaceSpec) error {
	for _, repository := range spec.GetRepositories() {
		if _, _, err := s.cloned(repository.GetRemote()); err != nil {
			return unclonable(err)
		}
	}
	return nil
}

// sandbox is the sandbox a workspace lives in, written in the sandbox
// layer's own terms: a box that serves the daemon's build API, its source
// API and the shell, is told
// the workspace's name, who commits and whose key gets in, and reaches
// what its rules say and no more. The rules are this layer's to write,
// since the sandbox layer knows no repository:
//
//   - a host a repository is fetched from over HTTP, by the version
//     control system's programs, for the requests it says a fetch of each
//     repository named makes, and a push where the workspace pushes, with
//     its owner's credential for the host where they keep one, and with
//     no one else's;
//   - the grid and its asset service, by the daemon's program.
//
// The repositories are those the workspace is asked for and those its
// entities resolved to, alike: each is fetched by its host's rule, with
// its owner's credential, and nothing else is reached. The catalog its
// entities were resolved through is not: the box never asks it.
func (s *service) sandbox(ctx context.Context, name, owner string, spec *workspacev1.WorkspaceSpec, resolved told.Resolved) (*sandboxv1.SandboxSpec, error) {
	image := spec.GetImage()
	if image == "" {
		if image = s.Box; image == "" {
			return nil, unserved("IMAGE_NOT_SERVED", "the control plane names no box image of its own: name one")
		}
	}
	environment := map[string]string{}
	for told, value := range s.Environment {
		environment[told] = value
	}
	// The daemon is told by its flags, after those the box starts it
	// with: the grid it builds on, and which workspace it manages, whose
	// name is for what it shows and records and makes it the sandbox's
	// one daemon. Each is one argument, written with its "=", so that no
	// value is read as a flag.
	arguments := slices.Clone(s.Grid.Arguments)
	arguments = append(arguments, "--name="+name)
	if s.Fetching != nil {
		authoring := s.Fetching.Authoring(spec.GetAuthor().GetName(), spec.GetAuthor().GetEmail())
		for told, value := range authoring {
			if strings.ContainsAny(value, "\r\n\x00") || told == "" || others(told) {
				return nil, fmt.Errorf("the version control system would tell its program %q of who commits, which is not its to say", told)
			}
			environment[told] = value
		}
	}
	if key := spec.GetAuthorizedKey(); key != "" {
		if strings.ContainsAny(key, "\r\n\x00") || strings.TrimSpace(key) == "" {
			return nil, malformed("a key let into a workspace's shell is one line")
		}
		arguments = append(arguments, "--authorized-key="+strings.TrimSpace(key))
	}
	out := &sandboxv1.SandboxSpec{
		Image:     image,
		Arguments: arguments,
		Ports:     []int32{ports.Daemon, ports.Source, ports.Shell},
		Labels:    map[string]string{Named: name},
		Egress:    &sandboxv1.Egress{Mode: sandboxv1.Egress_MODE_ALLOW_LISTED},
	}
	ruled := map[string]bool{}

	// The hosts repositories are fetched from, each with the paths of its
	// repositories, in the order named.
	paths := map[string][]string{}
	origins := map[string]plain.Origin{}
	var order []string
	// A push is to a repository the workspace was asked for by its
	// address, and never to one a catalog named.
	pushed := map[string]bool{}
	for _, remote := range remotesOf(spec, resolved) {
		at, where, err := s.cloned(remote)
		if err != nil {
			return nil, unclonable(err)
		}
		host := at.Host()
		if slices.ContainsFunc(spec.GetRepositories(), func(r *workspacev1.RepositorySource) bool { return r.GetRemote() == remote }) {
			pushed[host+where] = true
		}
		if origins[host].Scheme != "" && origins[host].Scheme != at.Scheme {
			return nil, malformed("%s is fetched from over both http and https", host)
		}
		if _, seen := origins[host]; !seen {
			order = append(order, host)
		}
		origins[host] = at
		if !slices.Contains(paths[host], where) {
			paths[host] = append(paths[host], where)
		}
	}
	if spec.GetPush() && len(spec.GetRepositories()) == 0 {
		return nil, unserved("PUSH_NOT_SERVED", "a workspace pushes to repositories it is given by their address, and this one is given none so")
	}
	if len(order) > 0 && s.Fetching == nil {
		return nil, unserved("FETCH_NOT_SERVED", "the control plane was told of no version control system, and so lets no workspace fetch a repository")
	}
	sort.Strings(order)
	for _, host := range order {
		at := origins[host]
		programs := s.Fetching.Programs()
		if err := fetchedBy(programs); err != nil {
			return nil, err
		}
		rule := &sandboxv1.EgressRule{Host: at.Rule(), Programs: programs}
		for _, repository := range paths[host] {
			// A repository is asked for as it is spelled, and not by another
			// spelling, which a forge may hold as another repository: its
			// path is one written plainly (fetched).
			asked := s.Fetching.Requests(repository)
			if err := asking("a fetch", repository, asked); err != nil {
				return nil, err
			}
			if spec.GetPush() && pushed[host+repository] {
				pushing := s.Fetching.Pushes(repository)
				if err := asking("a push", repository, pushing); err != nil {
					return nil, err
				}
				asked = append(slices.Clone(asked), pushing...)
			}
			for _, each := range asked {
				rule.Requests = append(rule.Requests, &sandboxv1.EgressRequest{Method: each.Method, Path: each.Path, Query: each.Query})
			}
		}
		// Where its owner keeps a credential for the host, the sandbox is
		// let through with it, and the system's program is told to present
		// the stand-in as its password. Another's credential is never
		// asked after.
		said, err := s.Sandboxes.DescribeCredential(ctx, &sandboxv1.DescribeCredentialRequest{Name: credentialID(owner, at.Rule())})
		kept := said.GetCredential()
		switch connect.CodeOf(err) {
		case connect.CodeNotFound, connect.CodeUnimplemented:
			// None is kept, or none can be: its repositories are fetched
			// as anyone fetches them.
		default:
			if err != nil {
				return nil, err
			}
			if kept == nil {
				break
			}
			// What another service says is held before it reaches a
			// program's configuration: a variable's name and a username are
			// each one word, and the credential is the one for this host.
			if !variable.MatchString(kept.GetVariable()) || !plain.Username(kept.GetUsername()) || kept.GetHost() != at.Rule() {
				return nil, fmt.Errorf("the sandbox service says of the credential for %s what is no username and variable of a credential for that host", host)
			}
			rule.Credential = kept.GetName()
			presenting, err := s.Fetching.Presenting(environment, at.URL(), kept.GetUsername(), kept.GetVariable())
			if err != nil {
				return nil, malformed("%v", err)
			}
			if err := telling(presenting, s.Fetching.Tells()); err != nil {
				return nil, err
			}
			for told, value := range presenting {
				environment[told] = value
			}
		}
		out.Egress.Rules = append(out.Egress.Rules, rule)
		ruled[host] = true
	}
	// The grid and its asset service, for the daemon and what it starts.
	for _, address := range s.Grid.Hosts {
		if address == "" || ruled[address] {
			continue
		}
		rule := &sandboxv1.EgressRule{Host: address}
		if s.Daemon != "" {
			rule.Programs = []string{s.Daemon}
		}
		out.Egress.Rules = append(out.Egress.Rules, rule)
		ruled[address] = true
	}
	out.Environment = environment
	return out, nil
}

// What a version control system says of a fetch becomes a way out of a
// sandbox, so it is held to what a fetch can be before any of it is
// kept, as anything is that this layer did not make: an answer that is
// more is a mistake of the system's, and nothing is let through on it.

// asking holds the requests of a repository's fetch, or of a push to it:
// some, each a read or a post, of something beneath the repository's own
// path and written plainly, held to parameters written plainly. Whether
// a request of a fetch is in truth a push is not for this to say, which
// knows no system: that is the system's own to hold.
func asking(what, repository string, requests []vcs.Request) error {
	if len(requests) == 0 {
		return fmt.Errorf("the version control system says %s of %s asks nothing", what, repository)
	}
	for _, asked := range requests {
		beneath, is := strings.CutPrefix(asked.Path, repository+"/")
		if (asked.Method != "GET" && asked.Method != "POST") || !is || !plain.Path("/"+beneath) {
			return fmt.Errorf("the version control system says %s of %s asks %s %q, which is no request beneath that repository", what, repository, asked.Method, asked.Path)
		}
		for parameter, value := range asked.Query {
			if !plain.Parameter(parameter, value) {
				return fmt.Errorf("the version control system says %s of %s asks with %q=%q, which is no parameter", what, repository, parameter, value)
			}
		}
	}
	return nil
}

// fetchedBy holds the programs a fetch's rule is for: some, each one
// program by its whole path, written plainly, and no pattern of many.
func fetchedBy(programs []string) error {
	if len(programs) == 0 {
		return errors.New("the version control system names no program that fetches")
	}
	for _, program := range programs {
		if !plain.Program(program) {
			return fmt.Errorf("the version control system names %q as a program that fetches, which is no one program's path", program)
		}
	}
	return nil
}

// telling holds what a system's program is told through a sandbox's
// environment: variables that begin as the system says its own do, each
// on one line, and no other. What the system says its own begin with is
// held too: capitals and digits ending in an underscore, and nothing
// that is this server's, the gateway's or the loader's to say.
func telling(told map[string]string, own string) error {
	named := len(own) > 1 && strings.HasSuffix(own, "_") && own[0] >= 'A' && own[0] <= 'Z'
	for _, r := range own {
		if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' {
			named = false
		}
	}
	if others(own) || slices.ContainsFunc(othersBegin, func(other string) bool { return strings.HasPrefix(other, own) }) {
		named = false
	}
	if !named {
		return fmt.Errorf("the version control system says its variables begin with %q, which is no beginning of its own", own)
	}
	for name, value := range told {
		rest, is := strings.CutPrefix(name, own)
		for _, r := range rest {
			if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' {
				is = false
			}
		}
		if !is || rest == "" || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("the version control system would tell its program %q, which is not its to say: its own begin with %s", name, own)
		}
	}
	return nil
}

// othersBegin are what the variables of this server, the gateway, the
// loader and the tools a box runs begin with: none is a version control
// system's to say, whether of who commits or of how a fetch is let
// through, and nothing it says begins so.
var othersBegin = []string{"FRONTSEAT_", "OPENSHELL_", "LD_", "DYLD_", "MISE_", "NONO_", "XDG_"}

// others is whether a variable's name begins as another's do
// (othersBegin), or is PATH, which a program is found by.
func others(name string) bool {
	return name == "PATH" || slices.ContainsFunc(othersBegin, func(other string) bool { return strings.HasPrefix(name, other) })
}

// variable is a variable's name, as one is written in an environment.
var variable = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// remotesOf are the remotes of the repositories a workspace holds: those
// it is asked for, and those its entities resolved to.
func remotesOf(spec *workspacev1.WorkspaceSpec, resolved told.Resolved) []string {
	var out []string
	for _, repository := range spec.GetRepositories() {
		out = append(out, repository.GetRemote())
	}
	for _, c := range resolved.Checkouts {
		out = append(out, c.Remote)
	}
	return out
}

// reaches says what a workspace's sandbox does not reach of a spec it is
// to come to hold, and of what its entities are to resolve to: a
// repository that is added, by its name or by an entity's, is fetched by
// its host's rule, which was written for the repositories the sandbox
// was made with. Those it resolved to since were held to the same, and
// so are among them. It is "" where nothing is beyond reach.
func reaches(made *workspacev1.WorkspaceSpec, was told.Resolved, wanted *workspacev1.WorkspaceSpec, resolved told.Resolved) string {
	had := map[string]bool{}
	for _, remote := range remotesOf(made, was) {
		had[remote] = true
	}
	for _, repository := range wanted.GetRepositories() {
		if !had[repository.GetRemote()] {
			return "a repository asked for was not named when the workspace's sandbox was made, and what a sandbox may reach is said then"
		}
	}
	for _, c := range resolved.Checkouts {
		if !had[c.Remote] {
			return "an entity added resolved to the repository " + c.Name + ", which the workspace's sandbox was not made to reach: what a sandbox may reach is said when it is made"
		}
	}
	return ""
}
