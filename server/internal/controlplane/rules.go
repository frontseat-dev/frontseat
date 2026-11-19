package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	connect "connectrpc.com/connect/v2"

	"github.com/frontseat-dev/frontseat/local/ports"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/plain"
	"github.com/frontseat-dev/frontseat/server/internal/source/told"
	"github.com/frontseat-dev/frontseat/server/internal/source/vcs"
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

// origin is where a repository is fetched from over HTTP: its scheme, its
// host as a name and a port, and its path there.
type origin struct {
	scheme, host, path string
}

// rule is the host as a sandbox's rule names it.
func (o origin) rule() string {
	if o.scheme == "http" {
		return "http://" + o.host
	}
	return o.host
}

// fetched reads where a repository is fetched from, and whether it is
// fetched over HTTP from a host's name at all: one that is not is the
// daemon's to refuse when it brings it.
func fetched(remote string) (origin, bool) {
	at, err := url.Parse(remote)
	if err != nil || (at.Scheme != "http" && at.Scheme != "https") || at.Hostname() == "" || net.ParseIP(at.Hostname()) != nil {
		return origin{}, false
	}
	port, ok := portOf(at.Port(), map[string]string{"http": "80", "https": "443"}[at.Scheme])
	if !ok {
		return origin{}, false
	}
	where := strings.TrimSuffix(at.EscapedPath(), "/")
	if where == "" {
		return origin{}, false
	}
	return origin{scheme: at.Scheme, host: net.JoinHostPort(strings.TrimSuffix(strings.ToLower(at.Hostname()), "."), port), path: where}, true
}

// portOf is a port as a rule names it: its number, written plainly, or
// the one given where none is written; and whether it is a port at all.
func portOf(written, byDefault string) (string, bool) {
	if written == "" {
		written = byDefault
	}
	number, err := strconv.Atoi(written)
	if err != nil || number < 1 || number > 65535 {
		return "", false
	}
	return strconv.Itoa(number), true
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
			if strings.ContainsAny(value, "\r\n\x00") || told == "" || strings.HasPrefix(told, "FRONTSEAT_") || strings.HasPrefix(told, "LD_") {
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
	origins := map[string]origin{}
	var order []string
	fetchable := 0
	// A push is to a repository the workspace was asked for by its
	// address, and never to one a catalog named.
	pushed := map[string]bool{}
	for _, remote := range remotesOf(spec, resolved) {
		at, is := fetched(remote)
		if !is {
			continue
		}
		if slices.ContainsFunc(spec.GetRepositories(), func(r *workspacev1.RepositorySource) bool { return r.GetRemote() == remote }) {
			pushed[at.host+at.path] = true
		}
		if origins[at.host].scheme != "" && origins[at.host].scheme != at.scheme {
			return nil, malformed("%s is fetched from over both http and https", at.host)
		}
		if _, seen := origins[at.host]; !seen {
			order = append(order, at.host)
		}
		origins[at.host] = at
		if !slices.Contains(paths[at.host], at.path) {
			paths[at.host] = append(paths[at.host], at.path)
		}
	}
	for _, repository := range spec.GetRepositories() {
		if _, is := fetched(repository.GetRemote()); is {
			fetchable++
		}
	}
	if spec.GetPush() && (fetchable == 0 || fetchable != len(spec.GetRepositories())) {
		return nil, unserved("PUSH_NOT_SERVED", "a workspace pushes to repositories it is given by their address over http or https, and each of these is not one")
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
		rule := &sandboxv1.EgressRule{Host: at.rule(), Programs: programs}
		for _, repository := range paths[host] {
			// A repository is asked for as it is spelled, and not by another
			// spelling, which a forge may hold as another repository.
			if !plain.Text(repository) || strings.Contains(repository, "..") || strings.Contains(repository, "//") {
				return nil, malformed("%q names no repository: its path is one repository's, written plainly, in letters, digits and . _ - + /", repository)
			}
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
		// asked after: a workspace of no one's is let through with none.
		var kept *sandboxv1.Credential
		var err error
		if owner != "" {
			var said *sandboxv1.DescribeCredentialResponse
			said, err = s.Sandboxes.DescribeCredential(ctx, &sandboxv1.DescribeCredentialRequest{Name: credentialID(owner, at.rule())})
			kept = said.GetCredential()
		}
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
			if !variable.MatchString(kept.GetVariable()) || !plain.Username(kept.GetUsername()) || kept.GetHost() != at.rule() {
				return nil, fmt.Errorf("the sandbox service says the credential for %s is of %q under %q, for %q, which is no username and variable of a credential for that host", at.host, kept.GetUsername(), kept.GetVariable(), kept.GetHost())
			}
			rule.Credential = kept.GetName()
			presenting, err := s.Fetching.Presenting(environment, at.scheme+"://"+at.host, kept.GetUsername(), kept.GetVariable())
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
		if (asked.Method != "GET" && asked.Method != "POST") || !is || !plain.Text(beneath) || strings.Contains(beneath, "..") || strings.Contains(beneath, "//") {
			return fmt.Errorf("the version control system says %s of %s asks %s %q, which is no request beneath that repository", what, repository, asked.Method, asked.Path)
		}
		for parameter, value := range asked.Query {
			if !plain.Text(parameter) || !plain.Text(value) || strings.Contains(parameter+value, "/") {
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
		if !strings.HasPrefix(program, "/") || path.Clean(program) != program || program == "/" || !plain.Text(program) {
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
	for _, others := range []string{"FRONTSEAT_", "OPENSHELL_", "LD_", "DYLD_", "MISE_", "NONO_", "XDG_"} {
		if strings.HasPrefix(own, others) || strings.HasPrefix(others, own) {
			named = false
		}
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

// variable is a variable's name, as one is written in an environment.
var variable = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// ruleOf is the rule a sandbox spec has for a host; nil where it has none.
func ruleOf(spec *sandboxv1.SandboxSpec, host string) *sandboxv1.EgressRule {
	for _, rule := range spec.GetEgress().GetRules() {
		if rule.GetHost() == host || rule.GetHost() == "http://"+host {
			return rule
		}
	}
	return nil
}

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
			return "the repository " + strconv.Quote(repository.GetRemote()) + " was not named when the workspace's sandbox was made, and what a sandbox may reach is said then"
		}
	}
	for _, c := range resolved.Checkouts {
		if !had[c.Remote] {
			return "an entity added resolved to the repository " + c.Name + ", which the workspace's sandbox was not made to reach: what a sandbox may reach is said when it is made"
		}
	}
	return ""
}
