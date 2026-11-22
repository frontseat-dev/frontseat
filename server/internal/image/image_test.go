package image_test

import (
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/image"
)

const digest = "sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec"

func TestAnImageIsNamedByItsRegistryAndDigest(t *testing.T) {
	got, err := image.Read("docker.io/library/busybox:1.37@" + digest)
	if err != nil || got.Registry != "docker.io" || got.Path != "library/busybox" || got.Digest != digest {
		t.Fatalf("the control: %+v %v", got, err)
	}
	for written, says := range map[string]string{
		"":                                    "names no image",
		"//services/web:image":                "not started yet",
		"--label=a.b/x@" + digest:             "not an image's reference",
		"busybox@" + digest:                   "does not name its registry",
		"library/busybox@" + digest:           "does not name its registry",
		"docker.io/library/busybox:1.37":      "not named by its digest",
		"Docker.io/Library/busybox@" + digest: "not an image's reference",
		"registry.example/Token@" + digest:    "not an image's reference",
	} {
		_, err := image.Read(written)
		if err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%q: %v, want it refused as %q", written, err, says)
		}
		// A refusal does not repeat the name: one may hold a credential.
		if err != nil && written != "" && (strings.Contains(err.Error(), written) || strings.Contains(err.Error(), "Token")) {
			t.Errorf("%q is repeated by its refusal: %v", written, err)
		}
	}
}
