package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	connect "connectrpc.com/connect/v2"
	credentialv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/credential/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/credential/v1alpha1/credentialv1alpha1connect"
	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/controlplane/identity"
	"github.com/frontseat-dev/frontseat/server/internal/page"
	"github.com/frontseat-dev/frontseat/server/internal/plain"
	"github.com/frontseat-dev/frontseat/server/internal/records"
	"github.com/frontseat-dev/frontseat/server/internal/refusal"
)

// credentials serves each person's credentials (frontseat.credential.
// v1alpha1). A credential is kept by the sandbox service, outside every
// sandbox, under an id of the control plane's own for its owner and its
// host (credentialID): a workspace's rule for a host names its owner's,
// and no other. The control plane keeps no secret: what it keeps is who
// keeps a credential for which host, and who it is of, to say back.
type credentials struct {
	s *service

	// mu is held while the record is read and written.
	mu sync.Mutex
}

// Every call of the contract is served: one added to it is not built
// until it is.
var _ credentialv1alpha1connect.CredentialServiceHandler = (*credentials)(nil)

// credentialID is the id a person's credential for a host is kept under
// by the sandbox service: of the digest of both, so that one person's
// credential for a host is one credential, no other person's or host's
// comes to it, and it says nothing of whose it is.
func credentialID(owner, host string) string {
	sum := sha256.Sum256([]byte(owner + "\x00" + host))
	return "cred-" + hex.EncodeToString(sum[:])[:40]
}

// besideID is the id a person's credential for a forge's host is kept
// under for another host that forge is asked at: of the digest of all
// three, so that it is no credential the person gave for that other
// host themselves, which stays theirs and as they gave it.
func besideID(owner, host, beside string) string {
	sum := sha256.Sum256([]byte(owner + "\x00" + host + "\x00beside\x00" + beside))
	return "cred-" + hex.EncodeToString(sum[:])[:40]
}

// credentialHost is a host as a credential is kept for it, and as a
// sandbox's rule names it (plain.Ruled): name:port, over https, or
// http://name:port, over http. A host is a name, never an address, and
// nothing more. What was written is not said back: a host written with
// a user before it may hold a token.
func credentialHost(written string) (string, error) {
	at, ok := plain.Ruled(written)
	if !ok {
		return "", errors.New("the host is no host's name, as a credential is kept for one: write it as forge.example, forge.example:8443 or http://forge.example:8080, with no user, path or address")
	}
	return at.Rule(), nil
}

// beside are the hosts a credential for a host is kept for beside it:
// those its forge is asked at for a release, each as a credential is
// kept for it. None, where the control plane was told of no forge, or
// the host is none of one's.
func (c *credentials) beside(host string) ([]string, error) {
	if c.s.Releasing == nil {
		return nil, nil
	}
	at, ok := plain.Ruled(host)
	if !ok {
		return nil, errors.New("a credential is kept for what is no host")
	}
	var out []string
	for _, each := range c.s.Releasing.Beside(at.Address()) {
		kept, err := credentialHost(each)
		if err != nil {
			return nil, fmt.Errorf("the forge says a credential for %s serves a host that is none: %w", host, err)
		}
		out = append(out, kept)
	}
	return out, nil
}

// credentialRecord is the record of the credentials kept: by owner, by
// host, who each is of.
type credentialRecord map[string]map[string]string

// recorded is the one record of the credentials kept, named so in a
// directory of its own beside the workspaces' records, which no
// workspace's name is.
const recorded = "record"

// records are where the record is kept.
func (c *credentials) records() (records.Dir, error) {
	return records.Open(filepath.Join(c.s.State, "credentials"))
}

// read is the record, under mu.
func (c *credentials) read() (credentialRecord, error) {
	dir, err := c.records()
	if err != nil {
		return nil, err
	}
	out := credentialRecord{}
	if _, err := dir.Read(recorded, &out); err != nil {
		return nil, fmt.Errorf("the record of the credentials kept is not one: %w", err)
	}
	return out, nil
}

// write keeps the record, whole or as it was, under mu.
func (c *credentials) write(record credentialRecord) error {
	dir, err := c.records()
	if err != nil {
		return err
	}
	return dir.Keep(recorded, record)
}

// caller is who asks, as the endpoint found them.
func caller(ctx context.Context) (string, error) {
	who, ok := identity.Of(ctx)
	if !ok || who.Subject == "" {
		return "", refusedFor(connect.CodeUnauthenticated, "IDENTITY_MISSING", "the call is from no one known")
	}
	return who.Subject, nil
}

// holding does what a call does with the record of the credentials kept,
// under mu, as the caller, and for the host the call names where it
// names one: a host that is none is refused before anything is read.
func (c *credentials) holding(ctx context.Context, written *string, do func(owner, host string, record credentialRecord) error) error {
	owner, err := caller(ctx)
	if err != nil {
		return err
	}
	var host string
	if written != nil {
		if host, err = credentialHost(*written); err != nil {
			return refusal.New(refusal.Credential, connect.CodeInvalidArgument, "CREDENTIAL_HOST_INVALID", err.Error())
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	record, err := c.read()
	if err != nil {
		return connect.NewError(connect.CodeInternal, err.Error())
	}
	return do(owner, host, record)
}

func (c *credentials) SetCredential(ctx context.Context, req *credentialv1.SetCredentialRequest) (*credentialv1.SetCredentialResponse, error) {
	given := req.GetCredential()
	if given.GetUsername() != "" && !plain.Username(given.GetUsername()) {
		return nil, refusal.New(refusal.Credential, connect.CodeInvalidArgument, "CREDENTIAL_USERNAME_INVALID", "a username is letters, digits and . _ @ -")
	}
	if !plain.Secret(given.GetSecret()) {
		return nil, refusal.New(refusal.Credential, connect.CodeInvalidArgument, "CREDENTIAL_SECRET_INVALID", "a credential's secret is given, on one line of no great length")
	}
	out := &credentialv1.SetCredentialResponse{}
	written := given.GetHost()
	err := c.holding(ctx, &written, func(owner, host string, record credentialRecord) error {
		// A forge is asked for its releases at hosts beside the one its
		// repositories are fetched from: a credential given for that one
		// is kept for those too, so that it is given once.
		beside, err := c.beside(host)
		if err != nil {
			return connect.NewError(connect.CodeInternal, err.Error())
		}
		if _, err := c.s.Sandboxes.SetCredential(ctx, &sandboxv1.SetCredentialRequest{Credential: &sandboxv1.Credential{
			Name: credentialID(owner, host), Host: host, Username: given.GetUsername(), Secret: given.GetSecret(),
		}}); err != nil {
			return relayed(err)
		}
		// Each is kept under a name of its own, and so is no credential
		// the person gave for that host themselves. One that could not
		// be kept fails the call, the host's own already kept anew: it
		// is given again.
		for _, each := range beside {
			if _, err := c.s.Sandboxes.SetCredential(ctx, &sandboxv1.SetCredentialRequest{Credential: &sandboxv1.Credential{
				Name: besideID(owner, host, each), Host: each, Username: given.GetUsername(), Secret: given.GetSecret(),
			}}); err != nil {
				return relayed(err)
			}
		}
		if record[owner] == nil {
			record[owner] = map[string]string{}
		}
		record[owner][host] = given.GetUsername()
		if err := c.write(record); err != nil {
			return connect.NewError(connect.CodeInternal, err.Error())
		}
		out.Credential = &credentialv1.Credential{Host: host, Username: given.GetUsername()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *credentials) ListCredentials(ctx context.Context, req *credentialv1.ListCredentialsRequest) (*credentialv1.ListCredentialsResponse, error) {
	out := &credentialv1.ListCredentialsResponse{}
	err := c.holding(ctx, nil, func(owner, _ string, record credentialRecord) error {
		for host, username := range record[owner] {
			out.Credentials = append(out.Credentials, &credentialv1.Credential{Host: host, Username: username})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out.Credentials, func(i, j int) bool { return out.Credentials[i].GetHost() < out.Credentials[j].GetHost() })
	if out.Credentials, out.NextPageToken, err = page.Of(out.Credentials, req.GetPageSize(), req.GetPageToken()); err != nil {
		return nil, refusal.New(refusal.Credential, connect.CodeInvalidArgument, refusal.PageTokenInvalid, err.Error())
	}
	return out, nil
}

func (c *credentials) DeleteCredential(ctx context.Context, req *credentialv1.DeleteCredentialRequest) (*credentialv1.DeleteCredentialResponse, error) {
	written := req.GetHost()
	err := c.holding(ctx, &written, func(owner, host string, record credentialRecord) error {
		// It is let go of where it was kept: for the host first, which
		// every workspace let through with any of it is let through
		// with, so that one still in use keeps the whole of it; then for
		// the hosts its forge is asked at beside it.
		beside, err := c.beside(host)
		if err != nil {
			return connect.NewError(connect.CodeInternal, err.Error())
		}
		names := []string{credentialID(owner, host)}
		for _, each := range beside {
			names = append(names, besideID(owner, host, each))
		}
		for i, name := range names {
			if _, err := c.s.Sandboxes.DeleteCredential(ctx, &sandboxv1.DeleteCredentialRequest{Name: name}); err != nil {
				if connect.CodeOf(err) == connect.CodeFailedPrecondition {
					return refusal.New(refusal.Credential, connect.CodeFailedPrecondition, "CREDENTIAL_IN_USE", fmt.Sprintf("a workspace of yours is still let through with your credential for %s: it is let go of once those workspaces have ended", host))
				}
				if i > 0 && connect.CodeOf(err) == connect.CodeNotFound {
					continue
				}
				return relayed(err)
			}
		}
		if _, ok := record[owner][host]; !ok {
			return nil
		}
		delete(record[owner], host)
		if len(record[owner]) == 0 {
			delete(record, owner)
		}
		if err := c.write(record); err != nil {
			return connect.NewError(connect.CodeInternal, err.Error())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &credentialv1.DeleteCredentialResponse{}, nil
}
