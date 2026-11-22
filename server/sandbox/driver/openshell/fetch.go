package openshell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	client "github.com/ai-openshell/sdk-go/openshell/v1"
	"github.com/ai-openshell/sdk-go/openshell/v1/types"

	"github.com/frontseat-dev/frontseat/server/internal/plain"
	"github.com/frontseat-dev/frontseat/server/sandbox/driver"
)

// kept is how the credential under an id is named at the gateway, and
// the variable a sandbox finds its stand-in under: both of the digest of
// the id, so that an id is one credential, a name says nothing of whose
// it is, and no other id comes to the same name.
func kept(id string) (name, variable string) {
	sum := sha256.Sum256([]byte("credential/" + id))
	digest := hex.EncodeToString(sum[:])
	return "fs-cred-" + digest[:40], "FRONTSEAT_CRED_" + strings.ToUpper(digest[:40])
}

// sent reads where a credential is sent, or a rule lets a sandbox
// reach: a host's name and a port, over https, or over http where it is
// written so (plain.Ruled). An address is refused in any spelling of
// one, so that a rule is always of a name and what the name resolves to
// is the gateway's to check: it refuses the loopback, link-local and
// unspecified addresses, and lets a name resolve to a private one.
func sent(host string) (plain.Origin, error) {
	at, ok := plain.Ruled(host)
	if !ok {
		return plain.Origin{}, errors.New("openshell: a host is named by its name and a port: a sandbox is let reach a name, never an address")
	}
	return at, nil
}

// Credential keeps a credential for a host at the gateway, under an id,
// in place of the one it held under the id for the same host. The
// gateway holds it outside every sandbox: a sandbox is given a stand-in,
// which the proxy of its supervisor, beside it and not in it, trades for
// the credential in a request it lets through, to the host and port it
// was kept for, encrypted or not (the package says what is not held). A
// credential is never moved to another host: a sandbox let through with
// it would send the new secret where the old one went.
func (d *Driver) Credential(ctx context.Context, id, host, user, secret string) error {
	if d.config.Identity == "" {
		return &driver.Unsupported{Driver: d.Name(), Capability: "a credential kept by a gateway that asks no one who they are"}
	}
	if !driver.CredentialID(id) {
		return fmt.Errorf("%w: openshell: a credential is asked for by what is no credential's id", driver.ErrInvalid)
	}
	at, err := sent(host)
	if err != nil {
		return fmt.Errorf("%w: %w", driver.ErrInvalid, err)
	}
	switch there, _, _, err := d.Kept(ctx, id); {
	case errors.Is(err, driver.ErrNotFound):
	case err != nil:
		return err
	case there != at.Rule():
		return fmt.Errorf("%w: openshell: the credential %s is kept for another host: let go of it first", driver.ErrInvalid, id)
	}
	if !plain.Secret(secret) {
		return fmt.Errorf("%w: openshell: the secret for %s is one line of no great length", driver.ErrInvalid, at.Rule())
	}
	if user == "" {
		user = "x-access-token"
	}
	if !plain.Username(user) {
		return fmt.Errorf("%w: openshell: the username for %s is letters, digits and . _ @ -", driver.ErrInvalid, at.Rule())
	}
	provider, variable := kept(id)
	// A profile of its own, which names no endpoint: where the credential
	// may go is said by each sandbox's policy, for the repositories that
	// sandbox fetches, and by nothing else.
	profiles := d.gateway.Providers().Profiles()
	if _, err := profiles.Get(ctx, d.config.Workspace, provider); client.IsNotFound(err) {
		imported, err := profiles.Import(ctx, d.config.Workspace, []client.ProfileImportItem{{Source: "frontseat", Profile: client.ProviderProfile{
			ID:          provider,
			DisplayName: "A host's credential",
			Description: "What a sandbox is let through with when it fetches a repository of one host",
			Category:    client.ProfileCategorySourceControl,
			Credentials: []client.ProfileCredential{{
				Name: "secret", Description: "The host's password or token", EnvVars: []string{variable},
				Required: true, AuthStyle: "basic", HeaderName: "authorization",
			}},
		}}})
		if err != nil {
			return fmt.Errorf("openshell: keeping a credential for %s: %w", at.Rule(), err)
		}
		// The gateway says what it made of a profile beside taking it or not.
		if !imported.Imported {
			var said []string
			for _, each := range imported.Diagnostics {
				said = append(said, each.Field+": "+each.Message)
			}
			return fmt.Errorf("openshell: keeping a credential for %s: the gateway did not take its profile: %s", at.Rule(), strings.Join(said, "; "))
		}
	} else if err != nil {
		return fmt.Errorf("openshell: keeping a credential for %s: %w", at.Rule(), err)
	}
	if _, err := d.gateway.Providers().Ensure(ctx, d.config.Workspace, &client.Provider{
		Name: provider, Type: provider,
		Spec: client.ProviderSpec{
			Credentials: map[string]string{variable: secret},
			// Where it is sent is kept with it, and read again before it is
			// bound: a credential is bound where it was kept for.
			Config: map[string]string{"username": user, "sent": at.URL(), "id": id},
			// The profile is the workspace's own, and is looked for there.
			ProfileWorkspace: d.config.Workspace,
		},
	}); err != nil {
		return fmt.Errorf("openshell: keeping a credential for %s: %w", at.Rule(), err)
	}
	return nil
}

// Forget lets go of the credential under an id.
func (d *Driver) Forget(ctx context.Context, id string) error {
	if !driver.CredentialID(id) {
		return fmt.Errorf("%w: openshell: a credential is asked for by what is no credential's id", driver.ErrInvalid)
	}
	provider, _ := kept(id)
	if _, err := d.gateway.Providers().Delete(ctx, d.config.Workspace, provider); err != nil && !client.IsNotFound(err) {
		// The gateway keeps a credential a sandbox is attached to.
		if client.IsConflict(err) {
			return fmt.Errorf("openshell: the credential %s: %w", id, driver.ErrInUse)
		}
		return fmt.Errorf("openshell: letting go of the credential %s: %w", id, err)
	}
	if _, err := d.gateway.Providers().Profiles().Delete(ctx, d.config.Workspace, provider); err != nil && !client.IsNotFound(err) {
		return fmt.Errorf("openshell: letting go of the credential %s: %w", id, err)
	}
	return nil
}

// fetched is what a sandbox's rules come to: the hosts it reaches, the
// gateway's rule for each, and the credentials attached.
type fetched struct {
	// hosts are the hosts reached, each a name and a port.
	hosts map[string]bool
	// rules are the network rules, by name.
	rules map[string]client.NetworkPolicyRule
	// providers are the credentials attached.
	providers []string
}

// Kept implements driver.Credentialed: the host the credential under an
// id is for, who it is of, and the variable a sandbox let through with it
// finds its stand-in under. What the gateway holds is read as anything
// from outside is: a credential is said to be kept only where this
// server kept it under the id, for a host it can say.
func (d *Driver) Kept(ctx context.Context, id string) (string, string, string, error) {
	if !driver.CredentialID(id) {
		return "", "", "", fmt.Errorf("%w: openshell: a credential is asked for by what is no credential's id", driver.ErrInvalid)
	}
	provider, variable := kept(id)
	held, err := d.gateway.Providers().Get(ctx, d.config.Workspace, provider)
	if client.IsNotFound(err) {
		return "", "", "", driver.ErrNotFound
	}
	if err != nil {
		return "", "", "", fmt.Errorf("openshell: asking after the credential %s: %w", id, err)
	}
	sentTo, err := url.Parse(held.Spec.Config["sent"])
	var at plain.Origin
	ok := err == nil
	if ok {
		at, ok = plain.OriginOf(sentTo.Scheme, sentTo.Hostname(), sentTo.Port())
	}
	if !ok || at.URL() != held.Spec.Config["sent"] || held.Spec.Config["id"] != id || !plain.Username(held.Spec.Config["username"]) {
		return "", "", "", fmt.Errorf("openshell: what the gateway keeps as the credential %s was not kept by this server under it", id)
	}
	return at.Rule(), held.Spec.Config["username"], variable, nil
}

// ruled reads a sandbox's rules into what its fetches came to: for each
// host, the one rule that lets it be reached as far as the record says,
// by the programs named, for the requests named, with the host's
// credential where the rule has it attached. A host is named by one
// rule, and by a rule or a fetch and not both.
//
// What a rule says is the asker's, and is held as anything from outside
// is before any of it reaches the gateway.
func (d *Driver) ruled(ctx context.Context, record driver.Sandbox) (fetched, error) {
	out := fetched{hosts: map[string]bool{}, rules: map[string]client.NetworkPolicyRule{}}
	for i, rule := range record.Egress.Rules {
		reached, err := sent(rule.Host)
		if err != nil {
			return fetched{}, fmt.Errorf("%w: %w", driver.ErrInvalid, err)
		}
		at := reached.Host()
		if out.hosts[at] {
			return fetched{}, fmt.Errorf("%w: openshell: %s is named by two rules, and a host is named by one", driver.ErrInvalid, at)
		}
		out.hosts[at] = true
		for _, program := range rule.Programs {
			if !plain.Program(program) {
				return fetched{}, fmt.Errorf("%w: openshell: the rule for %s names a program by what is no one program's path", driver.ErrInvalid, at)
			}
		}
		endpoint := client.PolicyNetworkEndpoint{Host: reached.Name, Port: uint32(reached.Port), Protocol: "tcp"}
		if len(rule.Requests) > 0 {
			endpoint.Protocol, endpoint.Enforcement = "rest", client.NetworkEnforcementModeEnforce
		}
		for _, asked := range rule.Requests {
			if !slices.Contains([]string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}, asked.Method) || !plain.Asked(asked.Path) {
				return fetched{}, fmt.Errorf("%w: openshell: the rule for %s lets through what is no request: one is a method and a path written plainly, a part of which after its first may be any one part, written *", driver.ErrInvalid, at)
			}
			allow := &client.L7Allow{Method: asked.Method, Path: asked.Path}
			for parameter, value := range asked.Query {
				if !plain.Parameter(parameter, value) {
					return fetched{}, fmt.Errorf("%w: openshell: the rule for %s holds a request to what is no parameter", driver.ErrInvalid, at)
				}
				if allow.Query == nil {
					allow.Query = map[string]client.L7QueryMatcher{}
				}
				allow.Query[parameter] = client.L7QueryMatcher{Any: []string{value}}
			}
			endpoint.Rules = append(endpoint.Rules, client.L7Rule{Allow: allow})
		}
		if id := rule.CredentialID; id != "" {
			// A credential goes where the requests it is attached to are
			// named: attached to anything asked of a host, it would be sent
			// with whatever a program there cared to ask.
			if len(rule.Requests) == 0 {
				return fetched{}, fmt.Errorf("%w: openshell: the rule for %s attaches a credential to no request it names", driver.ErrInvalid, at)
			}
			// And only to the host it was kept for, the way it was kept for
			// it: a credential kept for one host is never attached to
			// another's rule.
			host, _, _, err := d.Kept(ctx, id)
			if errors.Is(err, driver.ErrNotFound) {
				return fetched{}, fmt.Errorf("%w: openshell: no credential is kept as %s, and the rule for %s attaches it", driver.ErrInvalid, id, at)
			} else if err != nil {
				return fetched{}, err
			}
			if host != reached.Rule() {
				return fetched{}, fmt.Errorf("%w: openshell: the credential %s is kept for another host than %s, whose rule attaches it", driver.ErrInvalid, id, at)
			}
			provider, _ := kept(id)
			endpoint.CredentialBinding = &types.NetworkCredentialBinding{Provider: provider}
			out.providers = append(out.providers, provider)
		}
		named := "rule-" + strconv.Itoa(i+1)
		out.rules[named] = client.NetworkPolicyRule{Name: named, Endpoints: []client.PolicyNetworkEndpoint{endpoint}, Binaries: programs(rule.Programs)}
	}
	return out, nil
}

var _ driver.Credentialed = (*Driver)(nil)
