package driver

import (
	"errors"
	"strings"
	"testing"
)

// An endpoint is read as the sandbox API writes one: a name, with a port
// or the scheme's own, over https, or over http where it is written so;
// and said back as it is read, port and all. What is no name and port
// is refused as not well formed, and the refusal does not repeat it.
func TestAnEndpointIsANameAndAPortReachedOverHTTPSOrHTTP(t *testing.T) {
	for written, want := range map[string]Endpoint{
		"forge.example":                {Name: "forge.example", Port: 443},
		"https://Forge.Example:8443":   {Name: "forge.example", Port: 8443},
		"http://forge.example":         {Name: "forge.example", Port: 80, Clear: true},
		"http://forge.example:8080":    {Name: "forge.example", Port: 8080, Clear: true},
		"host.openshell.internal:8980": {Name: "host.openshell.internal", Port: 8980},
	} {
		got, err := ParseEndpoint(written)
		if err != nil || got != want {
			t.Errorf("%s is read as %+v, %v; want %+v", written, got, err, want)
			continue
		}
		if again, err := ParseEndpoint(got.String()); err != nil || again != got {
			t.Errorf("%s, said back as %s, is read as %+v, %v", written, got.String(), again, err)
		}
	}
	for name, written := range map[string]string{
		"an address":     "192.0.2.7",
		"no port":        "forge.example:",
		"port zero":      "forge.example:0",
		"a user":         "a-secret-of-its-own@forge.example",
		"a path":         "forge.example/a-secret-of-its-own",
		"another kind":   "ssh://forge.example",
		"nothing at all": "",
	} {
		_, err := ParseEndpoint(written)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
		if written != "" && err != nil && strings.Contains(err.Error(), written) {
			t.Errorf("%s: the refusal repeats what was written: %v", name, err)
		}
	}
}
