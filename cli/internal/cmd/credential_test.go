package cmd

import (
	"context"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"

	credentialv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/credential/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/credential/v1alpha1/credentialv1alpha1connect"
)

// keeper is a control plane, as far as credentials go: it keeps what it
// is given.
type keeper struct {
	credentialv1alpha1connect.UnimplementedCredentialServiceHandler
	given  []*credentialv1.Credential
	forgot []string
}

func (k *keeper) SetCredential(_ context.Context, req *credentialv1.SetCredentialRequest) (*credentialv1.SetCredentialResponse, error) {
	k.given = append(k.given, req.GetCredential())
	return &credentialv1.SetCredentialResponse{}, nil
}

func (k *keeper) DeleteCredential(_ context.Context, req *credentialv1.DeleteCredentialRequest) (*credentialv1.DeleteCredentialResponse, error) {
	k.forgot = append(k.forgot, req.GetHost())
	return &credentialv1.DeleteCredentialResponse{}, nil
}

// A credential is read from standard input, never from an argument, and
// handed to the control plane as it was given; nothing of it is printed.
func TestACredentialIsReadFromStandardInputAndHandedToTheServer(t *testing.T) {
	k := &keeper{}
	serverAt(t, serving(t, func(api *connect.Server) { credentialv1alpha1connect.RegisterCredentialServiceHandler(api, k) }))
	typed := func(secret string) {
		secretFrom = strings.NewReader(secret)
		t.Cleanup(func() { secretFrom = nil })
	}

	typed("a-made-up-secret\n")
	printed, code, err := runAt(t, context.Background(), "", "workspace", "credential", "forge.example", "--username", "someone")
	if err != nil || code != 0 {
		t.Fatalf("giving a credential ended %d, %v", code, err)
	}
	if len(k.given) != 1 || k.given[0].GetHost() != "forge.example" || k.given[0].GetUsername() != "someone" || k.given[0].GetSecret() != "a-made-up-secret" {
		t.Errorf("the server was given %v, want the host, the username and the secret without its line's end", k.given)
	}
	if strings.Contains(printed, "a-made-up-secret") {
		t.Errorf("the secret was printed: %q", printed)
	}

	// No secret, or one of several lines, is not handed over.
	for name, secret := range map[string]string{"nothing": "", "two lines": "one\ntwo\n"} {
		typed(secret)
		if _, _, err := runAt(t, context.Background(), "", "workspace", "credential", "forge.example"); err == nil || !strings.Contains(err.Error(), "one line") {
			t.Errorf("%s on standard input: %v, want it refused", name, err)
		}
	}
	if len(k.given) != 1 {
		t.Errorf("the server was given %d credentials, want the one", len(k.given))
	}

	// A secret is no argument.
	if _, _, err := runAt(t, context.Background(), "", "workspace", "credential", "forge.example", "a-made-up-secret"); err == nil {
		t.Error("a secret given as an argument was taken")
	}

	// With --forget the server lets go of it, and nothing is read.
	typed("")
	if _, code, err := runAt(t, context.Background(), "", "workspace", "credential", "forge.example", "--forget"); err != nil || code != 0 || len(k.forgot) != 1 || k.forgot[0] != "forge.example" {
		t.Errorf("letting go of a credential ended %d, %v, and the server forgot %v", code, err, k.forgot)
	}
}
