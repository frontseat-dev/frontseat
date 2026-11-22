package frontseat

import (
	"context"

	credentialv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/credential/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/credential/v1alpha1/credentialv1alpha1connect"
)

// Credentials are the caller's credentials for the hosts their
// workspaces fetch from and push to, which the control plane keeps
// outside every sandbox.
type Credentials struct {
	api credentialv1alpha1connect.CredentialServiceClient
}

// Credentials are the caller's credentials, as the control plane keeps
// them.
func (c *Client) Credentials() *Credentials {
	return &Credentials{api: credentialv1alpha1connect.NewCredentialServiceClient(c.api)}
}

// Credential is a credential for a host.
type Credential struct {
	// Host is the host it is for: a name, or name:port, sent over https;
	// written with http:// before it, it is sent over http.
	Host string
	// Username is who it is of, as the host asks.
	Username string
	// Secret is the password or token, on one line. No call returns it.
	Secret string
}

// Set keeps the caller's credential for its host, in place of the one
// there was.
func (cs *Credentials) Set(ctx context.Context, credential Credential) error {
	_, err := cs.api.SetCredential(ctx, &credentialv1.SetCredentialRequest{Credential: &credentialv1.Credential{Host: credential.Host, Username: credential.Username, Secret: credential.Secret}})
	return err
}

// Delete lets go of the caller's credential for a host, once no
// workspace of theirs is let through with it.
func (cs *Credentials) Delete(ctx context.Context, host string) error {
	_, err := cs.api.DeleteCredential(ctx, &credentialv1.DeleteCredentialRequest{Host: host})
	return err
}

// List lists the caller's credentials by host, without their secrets:
// each one's Secret is empty.
func (cs *Credentials) List(ctx context.Context) ([]Credential, error) {
	var out []Credential
	for each, err := range paged(func(token string) ([]*credentialv1.Credential, string, error) {
		page, err := cs.api.ListCredentials(ctx, &credentialv1.ListCredentialsRequest{PageToken: token})
		return page.GetCredentials(), page.GetNextPageToken(), err
	}) {
		if err != nil {
			return nil, err
		}
		out = append(out, Credential{Host: each.GetHost(), Username: each.GetUsername()})
	}
	return out, nil
}
