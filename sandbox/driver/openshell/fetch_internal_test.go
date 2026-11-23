package openshell

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	client "github.com/ai-openshell/sdk-go/openshell/v1"

	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/drivertest"
)

// A credential is kept under a name of its id: two ids are two
// credentials, whatever host each is for, and a name says nothing of the
// id. Where it is sent is read by plain.Ruled, whose test holds it.
func TestACredentialIsKeptUnderANameOfItsID(t *testing.T) {
	names := map[string]string{}
	for _, id := range []string{"ada-forge", "bea-forge", "x", "ada-forge-2"} {
		name, variable := kept(id)
		if other, twice := names[name]; twice {
			t.Errorf("%s and %s are kept under one name", id, other)
		}
		names[name] = id
		if len(name) != len("fs-cred-")+40 || variable != "FRONTSEAT_CRED_"+variable[len("FRONTSEAT_CRED_"):] || strings.Contains(name, id) {
			t.Errorf("kept(%s) = %s, %s", id, name, variable)
		}
	}
}

// The programs a rule is for are those named, each by its path, and any
// program where none is named.
func TestARuleIsForTheProgramsNamed(t *testing.T) {
	if got := programs(nil); len(got) != 1 || got[0].Path != "/**" {
		t.Errorf("with none named: %+v, want any program", got)
	}
	if got := programs([]string{"/opt/frontseat/bin/frontseatd", "/usr/bin/git"}); len(got) != 2 || got[0].Path != "/opt/frontseat/bin/frontseatd" || got[1].Path != "/usr/bin/git" {
		t.Errorf("with two named: %+v", got)
	}
}

// by is the programs a rule of a policy is for, on one line; "" where
// there is no such rule.
func by(policy *client.SandboxPolicy, host string, port uint32) string {
	for _, rule := range policy.NetworkPolicies {
		for _, endpoint := range rule.Endpoints {
			if endpoint.Host == host && endpoint.Port == port {
				var paths []string
				for _, program := range rule.Binaries {
					paths = append(paths, program.Path)
				}
				return strings.Join(paths, " ")
			}
		}
	}
	return ""
}

// What a sandbox reaches is what its rules say, each host by the
// programs its rule names, or any program where it names none, and only
// for the requests it names where it names some. A sandbox that blocks
// reaches nothing, whatever it names.
func TestEachHostIsReachedAsItsRuleSays(t *testing.T) {
	d := &Driver{config: Config{Workspace: "default"}}
	record := driver.Sandbox{Egress: driver.Egress{Listed: true, Rules: []driver.Rule{
		{Host: drivertest.Endpoint(t, "grid.example:8980"), Programs: []string{"/opt/a/daemon"}},
		{Host: drivertest.Endpoint(t, "listed.example")},
		{Host: drivertest.Endpoint(t, "http://forge.example"), Programs: []string{"/usr/bin/a-fetcher"}, Requests: []driver.Request{
			{Method: "GET", Path: "/acme/code/refs", Query: map[string]string{"service": "a-fetch"}},
			{Method: "POST", Path: "/acme/code/a-fetch"},
		}},
	}}}
	ruled, err := d.ruled(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := d.policy(record, ruled)
	if err != nil {
		t.Fatal(err)
	}
	for at, want := range map[string]string{
		"grid.example:8980":  "/opt/a/daemon",
		"listed.example:443": "/**",
		"forge.example:80":   "/usr/bin/a-fetcher",
	} {
		host, port, _ := strings.Cut(at, ":")
		number, _ := strconv.Atoi(port)
		if got := by(policy, host, uint32(number)); got != want {
			t.Errorf("%s is reached by %q, want %q", at, got, want)
		}
	}
	if len(policy.NetworkPolicies) != 3 {
		t.Errorf("the policy holds %d rules, want one for each of three hosts: %+v", len(policy.NetworkPolicies), policy.NetworkPolicies)
	}
	for _, rule := range policy.NetworkPolicies {
		endpoint := rule.Endpoints[0]
		switch endpoint.Host {
		case "forge.example":
			if endpoint.Protocol != "rest" || endpoint.Enforcement != client.NetworkEnforcementModeEnforce || len(endpoint.Rules) != 2 || endpoint.Rules[0].Allow.Query["service"].Any[0] != "a-fetch" {
				t.Errorf("the forge is reached by %+v, want the two requests named, enforced", endpoint)
			}
		default:
			if endpoint.Protocol != "tcp" || len(endpoint.Rules) != 0 {
				t.Errorf("%s is reached by %+v, want anything asked of it", endpoint.Host, endpoint)
			}
		}
	}
	blocked := record
	blocked.Egress = driver.Egress{Block: true, Rules: record.Egress.Rules}
	if held, err := d.policy(blocked, ruled); err != nil || len(held.NetworkPolicies) != 0 {
		t.Errorf("a sandbox that blocks reaches %+v (%v), want nothing", held.NetworkPolicies, err)
	}
}

// What a rule says is the asker's, and is held before any of it reaches
// the gateway: a host is a name and once, a program is one whole path, a
// request is a method and a path written plainly, a parameter is plain,
// and a credential is attached only to requests named. The control is
// the test above, whose rules are taken.
func TestWhatARuleSaysIsHeld(t *testing.T) {
	// The driver has no gateway: reaching for one would not return.
	d := &Driver{config: Config{Workspace: "default"}}
	get := []driver.Request{{Method: "GET", Path: "/acme/code/refs"}}
	for name, rules := range map[string][]driver.Rule{
		"an address":                      {{Host: driver.Endpoint{Name: "192.0.2.7", Port: 443}}},
		"no port that is one":             {{Host: driver.Endpoint{Name: "forge.example"}}},
		"a host named twice":              {{Host: drivertest.Endpoint(t, "forge.example")}, {Host: drivertest.Endpoint(t, "forge.example:443")}},
		"any program":                     {{Host: drivertest.Endpoint(t, "forge.example"), Programs: []string{"/**"}}},
		"a program by its name":           {{Host: drivertest.Endpoint(t, "forge.example"), Programs: []string{"git"}}},
		"a program by a path that climbs": {{Host: drivertest.Endpoint(t, "forge.example"), Programs: []string{"/usr/bin/../bin/git"}}},
		"another method":                  {{Host: drivertest.Endpoint(t, "forge.example"), Requests: []driver.Request{{Method: "CONNECT", Path: "/a"}}}},
		"a path that climbs":              {{Host: drivertest.Endpoint(t, "forge.example"), Requests: []driver.Request{{Method: "GET", Path: "/acme/../other"}}}},
		"a path that climbs unseen":       {{Host: drivertest.Endpoint(t, "forge.example"), Requests: []driver.Request{{Method: "GET", Path: "/acme/%2e%2e/other"}}}},
		"a pattern of many parts":         {{Host: drivertest.Endpoint(t, "forge.example"), Requests: []driver.Request{{Method: "GET", Path: "/acme/**"}}}},
		"a pattern within a part":         {{Host: drivertest.Endpoint(t, "forge.example"), Requests: []driver.Request{{Method: "GET", Path: "/acme/rel*"}}}},
		"a pattern of what a part is":     {{Host: drivertest.Endpoint(t, "forge.example"), Requests: []driver.Request{{Method: "GET", Path: "/acme/?"}}}},
		"any first part":                  {{Host: drivertest.Endpoint(t, "forge.example"), Requests: []driver.Request{{Method: "GET", Path: "/*/code"}}}},
		"any part and nothing else":       {{Host: drivertest.Endpoint(t, "forge.example"), Requests: []driver.Request{{Method: "GET", Path: "/*"}}}},
		"a path that is not one":          {{Host: drivertest.Endpoint(t, "forge.example"), Requests: []driver.Request{{Method: "GET", Path: "acme/code"}}}},
		"a parameter of any value":        {{Host: drivertest.Endpoint(t, "forge.example"), Requests: []driver.Request{{Method: "GET", Path: "/a", Query: map[string]string{"service": "*"}}}}},
		"a credential to anything asked":  {{Host: drivertest.Endpoint(t, "forge.example"), CredentialID: "ada-forge"}},
	} {
		if _, err := d.ruled(context.Background(), driver.Sandbox{Egress: driver.Egress{Listed: true, Rules: rules}}); !errors.Is(err, driver.ErrInvalid) {
			t.Errorf("%s: %v, want it refused as not well formed", name, err)
		}
	}
	// What is refused is not said back: a host, a program, a path and a
	// parameter are the asker's to write, and any may hold a token.
	const refusedValue = "v4lue of adas"
	for name, rule := range map[string]driver.Rule{
		"a host":      {Host: driver.Endpoint{Name: refusedValue + "@forge.example", Port: 443}},
		"a program":   {Host: drivertest.Endpoint(t, "forge.example"), Programs: []string{refusedValue}},
		"a path":      {Host: drivertest.Endpoint(t, "forge.example"), Requests: []driver.Request{{Method: "GET", Path: "/" + refusedValue}}},
		"a method":    {Host: drivertest.Endpoint(t, "forge.example"), Requests: []driver.Request{{Method: refusedValue, Path: "/a"}}},
		"a parameter": {Host: drivertest.Endpoint(t, "forge.example"), Requests: []driver.Request{{Method: "GET", Path: "/a", Query: map[string]string{"token": refusedValue}}}},
	} {
		_, err := d.ruled(context.Background(), driver.Sandbox{Egress: driver.Egress{Listed: true, Rules: []driver.Rule{rule}}})
		if !errors.Is(err, driver.ErrInvalid) {
			t.Errorf("%s that is none: %v, want it refused as not well formed", name, err)
		} else if strings.Contains(err.Error(), refusedValue) {
			t.Errorf("the refusal of %s repeats what it refused: %v", name, err)
		}
	}
	if _, err := d.ruled(context.Background(), driver.Sandbox{Egress: driver.Egress{Listed: true, Rules: []driver.Rule{{Host: drivertest.Endpoint(t, "forge.example"), Requests: get}}}}); err != nil {
		t.Errorf("a rule of one request was refused: %v", err)
	}
	// A part that is any one part is let through, after a part that is
	// said: what a server names as it makes it, as a release's number,
	// is not known when the rule is written. It is asked of the gateway
	// as it is written, which reads it as one part and no more.
	anyPart := []driver.Request{{Method: "PATCH", Path: "/repos/acme/code/releases/*"}, {Method: "POST", Path: "/repos/acme/code/releases/*/assets"}}
	ruled, err := d.ruled(context.Background(), driver.Sandbox{Egress: driver.Egress{Listed: true, Rules: []driver.Rule{{Host: drivertest.Endpoint(t, "forge.example"), Requests: anyPart}}}})
	if err != nil {
		t.Fatalf("a request of any one part was refused: %v", err)
	}
	policy, err := d.policy(driver.Sandbox{Egress: driver.Egress{Listed: true, Rules: []driver.Rule{{Host: drivertest.Endpoint(t, "forge.example"), Requests: anyPart}}}}, ruled)
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	for _, rule := range policy.NetworkPolicies {
		for _, allowed := range rule.Endpoints[0].Rules {
			asked = append(asked, allowed.Allow.Method+" "+allowed.Allow.Path)
		}
	}
	if got := strings.Join(asked, ", "); got != "PATCH /repos/acme/code/releases/*, POST /repos/acme/code/releases/*/assets" {
		t.Errorf("the gateway is asked for %s", got)
	}
}
