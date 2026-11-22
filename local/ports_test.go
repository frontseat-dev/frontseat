package local_test

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/local/ports"
)

// listens is where each file that cannot read local/ports, of the stack's
// image and the box's Dockerfile, says a port it holds, to listen on it
// or to dial it: the text it is said in, with the port's number.
var listens = []struct {
	file string
	text string
	port int
}{
	{"image/rootfs/etc/s6-overlay/s6-rc.d/controlplane/run", "--listen 0.0.0.0:%d ", ports.ControlPlane},
	{"image/rootfs/etc/openshell/gateway.template.toml", `bind_address        = "0.0.0.0:%d"`, ports.OpenShell},
	{"image/rootfs/etc/frontseat/bb/frontend.jsonnet", "listenAddresses: [':%d']", ports.Grid},
	{"image/rootfs/etc/frontseat/bb/assets.jsonnet", "listenAddresses: [':%d']", ports.Assets},
	{"image/rootfs/etc/unbound/unbound.conf", "\tport: %d\n", ports.Resolver},
	{"image/rootfs/etc/s6-overlay/s6-rc.d/controlplane/run", "--public http://127.0.0.1:%d ", ports.ControlPlane},
	{"image/rootfs/etc/s6-overlay/s6-rc.d/controlplane/run", "--openshell https://127.0.0.1:%d ", ports.OpenShell},
	{"image/rootfs/etc/s6-overlay/s6-rc.d/controlplane/run", "--grid host.openshell.internal:%d\n", ports.Grid},
	{"image/rootfs/etc/s6-overlay/s6-rc.d/pool/run", "--gateway https://127.0.0.1:%d ", ports.OpenShell},
	{"image/rootfs/etc/s6-overlay/s6-rc.d/services/run", "--gateway https://127.0.0.1:%d ", ports.OpenShell},
	{"box.Dockerfile", `"--listen", "0.0.0.0:%d"`, ports.Daemon},
	{"box.Dockerfile", `"--source", "0.0.0.0:%d"`, ports.Source},
	{"box.Dockerfile", `"--shell", "0.0.0.0:%d"`, ports.Shell},
}

// listeningElsewhere returns each file of the image that does not say its port as
// local/ports does, reading each through read.
func listeningElsewhere(read func(file string) ([]byte, error)) ([]string, error) {
	var out []string
	for _, each := range listens {
		content, err := read(each.file)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(content), fmt.Sprintf(each.text, each.port)) {
			out = append(out, each.file)
		}
	}
	return out, nil
}

// The stack's one container and a box listen, and are dialled, where
// local/ports says, each port said once there and held here in the
// files that cannot read it.
func TestTheStacksImageListensWhereThePortsAreSaid(t *testing.T) {
	wrong, err := listeningElsewhere(os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(wrong) > 0 {
		t.Errorf("not listening where local/ports says: %s", strings.Join(wrong, ", "))
	}
	// The control: a port written otherwise is found.
	moved, err := listeningElsewhere(func(file string) ([]byte, error) {
		content, err := os.ReadFile(file)
		if file == listens[2].file {
			content = []byte(strings.ReplaceAll(string(content), strconv.Itoa(ports.Grid), "9999"))
		}
		return content, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(moved, []string{listens[2].file}) {
		t.Errorf("a moved grid port was found in %v, want %s", moved, listens[2].file)
	}
	// And a box's: its daemon told to serve its source API elsewhere.
	moved, err = listeningElsewhere(func(file string) ([]byte, error) {
		content, err := os.ReadFile(file)
		if file == "box.Dockerfile" {
			content = []byte(strings.ReplaceAll(string(content), strconv.Itoa(ports.Source), "9999"))
		}
		return content, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(moved, []string{"box.Dockerfile"}) {
		t.Errorf("a moved source port of a box was found in %v, want box.Dockerfile", moved)
	}
}

// The container's own resolver listens at the address local/ports says
// it is asked at, which init writes into the container's resolv.conf.
func TestTheContainersResolverListensWhereItIsSaidToBeAsked(t *testing.T) {
	content, err := os.ReadFile("image/rootfs/etc/unbound/unbound.conf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "interface: "+ports.ResolverAddress+"\n") {
		t.Errorf("image/rootfs/etc/unbound/unbound.conf does not listen at %s", ports.ResolverAddress)
	}
}
