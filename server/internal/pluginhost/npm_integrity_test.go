package pluginhost_test

import (
	"errors"
	"strings"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/server/internal/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost/conformance"
	"github.com/frontseat-dev/frontseat/server/testsupport/testmodule"
)

func TestNpmRequiresAVerifiableNativeLockIntegrity(t *testing.T) {
	module, err := testmodule.Plugin("npm")
	if err != nil {
		t.Fatal(err)
	}
	plugin := conformance.Load(t, module)
	const valid = "sha512-41Cifkg6e8TylSpdtTpeLVMqvSBEVzTttHvERD741+pnZ8ANv0004MRL43QKPDlK9cGvNp6NZWZUBlbGXYxxng=="
	invoke := func(integrity string) error {
		source := files{"app/package.json": npmPackage, "app/package-lock.json": strings.Replace(npmLock, npmIntegrity, integrity, 1)}
		_, err := plugin.Call(t.Context(), source, "Analyze", &pluginv1.AnalyzeRequest{Roots: []string{"app"}, Context: npmContext()}, new(pluginv1.AnalyzeResponse))
		return err
	}
	if err := invoke(valid); err != nil {
		t.Fatalf("valid SHA512 refused: %v", err)
	}
	for name, sri := range map[string]string{
		"empty":                 "",
		"unparsed token":        "not-an-integrity",
		"whitespace":            " ",
		"unsupported algorithm": "sha999-AAAA",
		"malformed base64":      "sha512-@",
		"short SHA512":          "sha512-AAAA",
	} {
		t.Run(name, func(t *testing.T) {
			var failure *pluginhost.Error
			if err := invoke(sri); !errors.As(err, &failure) || failure.Reason != "INVALID_MANIFEST" {
				t.Fatalf("unverifiable lock integrity accepted: %v", err)
			}
		})
	}
	if err := invoke(valid); err != nil {
		t.Fatalf("valid SHA512 after refusals: %v", err)
	}
}
