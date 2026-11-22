package controlplane

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	credentialv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/credential/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/credential/v1alpha1/credentialv1alpha1connect"
	workspacev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/workspace/v1alpha1/workspacev1alpha1connect"
	"github.com/frontseat-dev/frontseat/server/internal/controlplane/identity"
)

// people knows a test's people, each by a token of their own.
type people map[string]string

func (p people) Authenticate(_ context.Context, token string) (identity.Caller, error) {
	if token == "" {
		return identity.Caller{}, identity.ErrMissing
	}
	if who, ok := p[token]; ok {
		return identity.Caller{Subject: who}, nil
	}
	return identity.Caller{}, identity.ErrInvalid
}

// a third person's token, beside owner's and another's.
const third = "746869726420706572736f6e2773206f776e20746f6b656e2c206e6f206f6e65"

// servedToPeople serves a control plane to Ada (owner's token), Bea
// (another's) and Cy (third's), and returns clients of each one's
// workspaces and credentials.
func servedToPeople(t *testing.T, s *sandboxes) (func(token string) workspacev1alpha1connect.WorkspaceServiceClient, func(token string) credentialv1alpha1connect.CredentialServiceClient) {
	t.Helper()
	_, address, _ := served(t, s, t.TempDir(), func(c *Config) { c.Callers = people{owner: "ada", another: "bea", third: "cy"} })
	return func(token string) workspacev1alpha1connect.WorkspaceServiceClient {
			return workspacev1alpha1connect.NewWorkspaceServiceClient(connect.NewClient(connecthttp.NewTransport(bearing(token), address)))
		}, func(token string) credentialv1alpha1connect.CredentialServiceClient {
			return credentialv1alpha1connect.NewCredentialServiceClient(connect.NewClient(connecthttp.NewTransport(bearing(token), address)))
		}
}

// listedFor are the hosts a person's credentials are listed for, in the
// order listed: each as Ada's, with no secret said.
func listedFor(t *testing.T, credentials credentialv1alpha1connect.CredentialServiceClient) ([]string, error) {
	t.Helper()
	listed, err := credentials.ListCredentials(context.Background(), &credentialv1.ListCredentialsRequest{})
	var hosts []string
	for _, each := range listed.GetCredentials() {
		hosts = append(hosts, each.GetHost())
		if each.GetSecret() != "" || each.GetUsername() != "ada" {
			t.Errorf("a credential is listed as %v", each)
		}
	}
	return hosts, err
}

// One person's credential for a host is theirs: a workspace of another
// that fetches from the host is not let through with it. The controls
// are the owner's own workspace, which is, and the other's, once they
// keep a credential of their own, which is let through with theirs.
func TestOnePersonsCredentialIsNotUsedByAnothersWorkspace(t *testing.T) {
	s := over(t)
	workspaces, credentials := servedToPeople(t, s)
	ctx := context.Background()
	// Ada keeps a credential for the forge.
	if _, err := credentials(owner).SetCredential(ctx, &credentialv1.SetCredentialRequest{Credential: &credentialv1.Credential{Host: "forge.example", Username: "ada", Secret: "adas-secret"}}); err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"adas": owner, "beas": another} {
		if _, err := workspaces(token).CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: name, Spec: of("https://forge.example/acme/code.git")}); err != nil {
			t.Fatal(err)
		}
	}
	adas, beas := waited(t, workspaces(owner), "adas"), waited(t, workspaces(another), "beas")
	if rule := s.sandbox(s.of(adas.GetName())).GetEgress().GetRules()[0]; rule.GetCredential() != credentialID("ada", "forge.example:443") {
		t.Errorf("Ada's workspace is let through the forge with %v, want her credential", rule)
	}
	if rule := s.sandbox(s.of(beas.GetName())).GetEgress().GetRules()[0]; rule.GetCredential() != "" {
		t.Errorf("Bea's workspace is let through the forge with %v, want no credential: Ada's is hers", rule)
	}
	// Bea keeps one of her own, and her next workspace is let through
	// with it, and with none of Ada's.
	if _, err := credentials(another).SetCredential(ctx, &credentialv1.SetCredentialRequest{Credential: &credentialv1.Credential{Host: "forge.example:443", Secret: "beas-secret"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaces(another).CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "beas-next", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Fatal(err)
	}
	next := waited(t, workspaces(another), "beas-next")
	if rule := s.sandbox(s.of(next.GetName())).GetEgress().GetRules()[0]; rule.GetCredential() != credentialID("bea", "forge.example:443") {
		t.Errorf("Bea's next workspace is let through the forge with %v, want her own credential", rule)
	}
	if credentialID("ada", "forge.example:443") == credentialID("bea", "forge.example:443") {
		t.Error("two people's credentials for one host are kept under one id")
	}
}

// A person's credentials are said back to them alone, each by its host
// however it was written, and never with its secret; what is no host,
// no username or no secret is refused before anything is kept; one a
// workspace is still let through with is not let go of, by name; and
// none of it is asked of anyone the control plane does not know.
func TestAPersonsCredentialsAreTheirsAlone(t *testing.T) {
	s := over(t)
	_, credentials := servedToPeople(t, s)
	ctx := context.Background()
	for _, written := range []string{"Forge.Example.", "http://plain.example", "https://other.example:8443"} {
		set, err := credentials(owner).SetCredential(ctx, &credentialv1.SetCredentialRequest{Credential: &credentialv1.Credential{Host: written, Username: "ada", Secret: "adas-secret"}})
		if err != nil {
			t.Fatalf("%s: %v", written, err)
		}
		if strings.Contains(set.String(), "adas-secret") {
			t.Errorf("the secret was said back: %v", set)
		}
	}
	hosts, err := listedFor(t, credentials(owner))
	if want := []string{"forge.example:443", "http://plain.example:80", "other.example:8443"}; err != nil || !slices.Equal(hosts, want) {
		t.Errorf("Ada's credentials are listed as %v (%v), want %v", hosts, err, want)
	}
	// Another lists none of hers.
	if listed, err := credentials(another).ListCredentials(ctx, &credentialv1.ListCredentialsRequest{}); err != nil || len(listed.GetCredentials()) != 0 {
		t.Errorf("Bea is listed %v (%v), want none of Ada's", listed, err)
	}
	// Another's deleting the host lets go of nothing of Ada's.
	if _, err := credentials(another).DeleteCredential(ctx, &credentialv1.DeleteCredentialRequest{Host: "forge.example"}); err != nil {
		t.Errorf("Bea letting go of what she does not keep: %v, want no error", err)
	}
	if again, err := credentials(owner).ListCredentials(ctx, &credentialv1.ListCredentialsRequest{}); err != nil || len(again.GetCredentials()) != len(hosts) {
		t.Errorf("Ada's credentials after Bea let go of the host: %v (%v), want them kept", again, err)
	}
	for what, refused := range map[string]*credentialv1.Credential{
		"no host":            {Secret: "s"},
		"an address":         {Host: "192.0.2.7", Secret: "s"},
		"a path":             {Host: "forge.example/acme", Secret: "s"},
		"a user":             {Host: "ada@forge.example", Secret: "s"},
		"another scheme":     {Host: "ssh://forge.example", Secret: "s"},
		"no secret":          {Host: "forge.example"},
		"a secret of lines":  {Host: "forge.example", Secret: "a\nb"},
		"a username of ways": {Host: "forge.example", Username: "a b", Secret: "s"},
	} {
		if _, err := credentials(third).SetCredential(ctx, &credentialv1.SetCredentialRequest{Credential: refused}); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("a credential of %s: %v, want it refused", what, err)
		} else if strings.Contains(err.Error(), refused.GetSecret()+"\n") {
			t.Errorf("the refusal says the secret: %v", err)
		}
	}
	if listed, _ := credentials(third).ListCredentials(ctx, &credentialv1.ListCredentialsRequest{}); len(listed.GetCredentials()) != 0 {
		t.Errorf("what was refused was kept: %v", listed)
	}
	// A host written with a user before it is refused without it said
	// back: the refusal says what a host is, not what was written. The
	// control is the hosts above, written without one and kept.
	for _, written := range []string{"ada:v4lue-of-adas@forge.example", "https://ada:v4lue-of-adas@forge.example", "http://v4lue-of-adas@forge.example"} {
		_, err := credentials(third).SetCredential(ctx, &credentialv1.SetCredentialRequest{Credential: &credentialv1.Credential{Host: written, Secret: "s"}})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("a host written with a user: %v, want it refused", err)
		} else if strings.Contains(err.Error(), "v4lue-of-adas") {
			t.Errorf("the refusal of a host repeats what was written in it: %v", err)
		}
	}
	// One in use is kept, by name; the control is the same let go of once
	// it is not.
	s.mu.Lock()
	s.inUse[credentialID("ada", "forge.example:443")] = true
	s.mu.Unlock()
	if _, err := credentials(owner).DeleteCredential(ctx, &credentialv1.DeleteCredentialRequest{Host: "forge.example"}); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "still let through") {
		t.Errorf("letting go of a credential in use: %v, want it kept, by name", err)
	}
	s.mu.Lock()
	s.inUse[credentialID("ada", "forge.example:443")] = false
	s.mu.Unlock()
	if _, err := credentials(owner).DeleteCredential(ctx, &credentialv1.DeleteCredentialRequest{Host: "forge.example"}); err != nil {
		t.Fatal(err)
	}
	if left, err := credentials(owner).ListCredentials(ctx, &credentialv1.ListCredentialsRequest{}); err != nil || slices.ContainsFunc(left.GetCredentials(), func(c *credentialv1.Credential) bool { return c.GetHost() == "forge.example:443" }) {
		t.Errorf("a credential let go of is listed: %v (%v)", left, err)
	}
	s.mu.Lock()
	_, still := s.kept[credentialID("ada", "forge.example:443")]
	s.mu.Unlock()
	if still {
		t.Error("the sandbox service still keeps the credential let go of")
	}
	// No one the control plane does not know asks anything of it.
	if _, err := credentials("").ListCredentials(ctx, &credentialv1.ListCredentialsRequest{}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("listing credentials with no token: %v, want it refused", err)
	}
}

// A workspace is its owner's: another is refused describing it, waiting
// for it, changing it, ending it, asking for it again, and reaching its
// build API and its shell, PERMISSION_DENIED with NOT_OWNER, and is not
// listed it; so that what runs in it with its owner's credential is
// asked of it by its owner alone. The control is its owner, who does
// each.
func TestAWorkspaceIsItsOwners(t *testing.T) {
	s := over(t)
	workspaces, _ := servedToPeople(t, s)
	ctx := context.Background()
	if _, err := workspaces(owner).CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "adas", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Fatal(err)
	}
	ready := waited(t, workspaces(owner), "adas")
	address := strings.TrimSuffix(ready.GetStatus().GetBuildUri(), "/workspaces/adas/build")
	notOwner := func(what string, err error) {
		t.Helper()
		if why, in := reason(t, err); connect.CodeOf(err) != connect.CodePermissionDenied || why != "NOT_OWNER" || in != "workspace.frontseat.dev" {
			t.Errorf("Bea %s Ada's workspace: %v (%s, %s), want it refused as another's", what, err, why, in)
		}
	}
	_, err := workspaces(another).DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "adas"})
	notOwner("describing", err)
	_, err = workspaces(another).UpdateWorkspace(ctx, &workspacev1.UpdateWorkspaceRequest{Name: "adas", Spec: of("https://forge.example/acme/code.git")})
	notOwner("changing", err)
	_, err = workspaces(another).CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "adas", Spec: of("https://forge.example/acme/code.git")})
	notOwner("asking again for", err)
	_, err = workspaces(another).WaitWorkspace(ctx, &workspacev1.WaitWorkspaceRequest{Name: "adas"})
	notOwner("waiting for", err)
	_, err = workspaces(another).DeleteWorkspace(ctx, &workspacev1.DeleteWorkspaceRequest{Name: "adas"})
	notOwner("ending", err)
	for _, path := range []string{"/workspaces/adas/build/frontseat.build.v1alpha1.BuildService/ListBuilds", "/workspaces/adas/shell"} {
		if status, said := asked(t, "POST", address+path, another, "{}"); status != 403 {
			t.Errorf("Bea reaching %s: %d %s, want it refused", path, status, said)
		}
	}
	if listed, err := workspaces(another).ListWorkspaces(ctx, &workspacev1.ListWorkspacesRequest{}); err != nil || len(listed.GetWorkspaces()) != 0 {
		t.Errorf("Bea is listed %v (%v), want none of Ada's", listed.GetWorkspaces(), err)
	}
	s.mu.Lock()
	led := len(s.led)
	s.mu.Unlock()
	if led != 0 {
		t.Errorf("Bea was led into Ada's workspace %d times", led)
	}
	// The control: Ada does each.
	if listed, err := workspaces(owner).ListWorkspaces(ctx, &workspacev1.ListWorkspacesRequest{}); err != nil || len(listed.GetWorkspaces()) != 1 {
		t.Errorf("Ada is listed %v (%v), want her workspace", listed.GetWorkspaces(), err)
	}
	if status, said := asked(t, "POST", address+"/workspaces/adas/build/frontseat.build.v1alpha1.BuildService/ListBuilds", owner, "{}"); status != 200 {
		t.Errorf("Ada reaching her build API: %d %s", status, said)
	}
	if _, err := workspaces(owner).DeleteWorkspace(ctx, &workspacev1.DeleteWorkspaceRequest{Name: "adas"}); err != nil {
		t.Errorf("Ada ending her workspace: %v", err)
	}
}

// A control plane that keeps a credential starts again over its state,
// whatever its workspaces are named: the record of the credentials is no
// workspace's, and a workspace named for it keeps its own. The control
// is the credential and the workspace, each there after.
func TestAControlPlaneThatKeepsACredentialStartsAgain(t *testing.T) {
	s, state := over(t), t.TempDir()
	people := func(c *Config) { c.Callers = people{owner: "ada"} }
	first, address, plane := served(t, s, state, people)
	ctx := context.Background()
	credentials := credentialv1alpha1connect.NewCredentialServiceClient(connect.NewClient(connecthttp.NewTransport(bearing(owner), address)))
	if _, err := first.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{Name: "credentials", Spec: of("https://forge.example/acme/code.git")}); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.SetCredential(ctx, &credentialv1.SetCredentialRequest{Credential: &credentialv1.Credential{Host: "forge.example:0443", Secret: "adas-secret"}}); err != nil {
		t.Fatal(err)
	}
	waited(t, first, "credentials")
	plane.Close()
	again, address, _ := served(t, s, state, people)
	if _, err := again.DescribeWorkspace(ctx, &workspacev1.DescribeWorkspaceRequest{Name: "credentials"}); err != nil {
		t.Errorf("the workspace named credentials, after the control plane started again: %v", err)
	}
	listed, err := credentialv1alpha1connect.NewCredentialServiceClient(connect.NewClient(connecthttp.NewTransport(bearing(owner), address))).ListCredentials(ctx, &credentialv1.ListCredentialsRequest{})
	// A port is one however it is written.
	if err != nil || len(listed.GetCredentials()) != 1 || listed.GetCredentials()[0].GetHost() != "forge.example:443" {
		t.Errorf("the credentials after the control plane started again: %v, %v", listed, err)
	}
}

// What a box answers through the way to its build API is said on, but a
// redirect: one would lead the caller, and the token it presents, where
// the box chose. The control is the same call answered plainly.
func TestARedirectFromABoxIsNotSaidOn(t *testing.T) {
	s, client := made(t, "code", of("https://forge.example/acme/code.git"))
	ready := waited(t, client, "code")
	s.mu.Lock()
	s.redirect = "https://elsewhere.example/collect"
	s.mu.Unlock()
	req, _ := http.NewRequest("POST", ready.GetStatus().GetBuildUri()+"/frontseat.build.v1alpha1.BuildService/ListBuilds", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+owner)
	got, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got.Body.Close()
	if got.StatusCode != http.StatusBadGateway || got.Header.Get("Location") != "" {
		t.Errorf("a box's redirect was said on: %d, to %q", got.StatusCode, got.Header.Get("Location"))
	}
	s.mu.Lock()
	s.redirect = ""
	s.mu.Unlock()
	if status, said := asked(t, "POST", ready.GetStatus().GetBuildUri()+"/frontseat.build.v1alpha1.BuildService/ListBuilds", owner, "{}"); status != http.StatusOK {
		t.Errorf("the plain answer: %d %s", status, said)
	}
}
