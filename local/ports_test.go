package local_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/local/ports"
)

// said is where each file that cannot read local/ports says one of its
// ports, to listen on it or to dial it: the text it is said in, with the
// port's number.
var said = []struct {
	file string
	text string
	port int
}{
	{"image/rootfs/etc/s6-overlay/s6-rc.d/controlplane/run", "--listen 0.0.0.0:%d ", ports.ControlPlane},
	{"image/rootfs/etc/s6-overlay/s6-rc.d/controlplane/run", "--public http://127.0.0.1:%d ", ports.ControlPlane},
	{"image/rootfs/etc/s6-overlay/s6-rc.d/controlplane/run", "--openshell https://127.0.0.1:%d ", ports.OpenShell},
	{"image/rootfs/etc/s6-overlay/s6-rc.d/controlplane/run", "--grid host.openshell.internal:%d\n", ports.Grid},
	{"image/rootfs/etc/s6-overlay/s6-rc.d/pool/run", "--gateway https://127.0.0.1:%d ", ports.OpenShell},
	{"image/rootfs/etc/s6-overlay/s6-rc.d/services/run", "--gateway https://127.0.0.1:%d ", ports.OpenShell},
	{"image/rootfs/etc/openshell/gateway.template.toml", `bind_address        = "0.0.0.0:%d"`, ports.OpenShell},
	{"image/rootfs/etc/frontseat/bb/frontend.jsonnet", "listenAddresses: [':%d']", ports.Grid},
	{"image/rootfs/etc/frontseat/bb/assets.jsonnet", "listenAddresses: [':%d']", ports.Assets},
	{"image/rootfs/etc/unbound/unbound.conf", "\tport: %d\n", ports.Resolver},
	{"box.Dockerfile", `"--listen", "0.0.0.0:%d"`, ports.Daemon},
	{"box.Dockerfile", `"--source", "0.0.0.0:%d"`, ports.Source},
	{"box.Dockerfile", `"--shell", "0.0.0.0:%d"`, ports.Shell},
}

// saidElsewhere returns the file of each port that is not said as
// local/ports says it, reading each file through read.
func saidElsewhere(read func(file string) ([]byte, error)) ([]string, error) {
	var out []string
	for _, each := range said {
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

// The stack's one container and the box listen and dial where
// local/ports says, each port said once there and held here in the
// files that cannot read it: the image's files, its services' flags and
// the box's entrypoint.
func TestTheStacksImageListensWhereThePortsAreSaid(t *testing.T) {
	wrong, err := saidElsewhere(os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(wrong) > 0 {
		t.Errorf("a port is not said as local/ports says it in: %s", strings.Join(wrong, ", "))
	}
	// The control, for each port held: written otherwise where it is
	// said, it is found, in that file and no other.
	for _, each := range said {
		moved, err := saidElsewhere(func(file string) ([]byte, error) {
			content, err := os.ReadFile(file)
			if file == each.file {
				content = []byte(strings.ReplaceAll(string(content), fmt.Sprintf(each.text, each.port), fmt.Sprintf(each.text, each.port+1)))
			}
			return content, err
		})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(moved, []string{each.file}) {
			t.Errorf("port %d moved in %s was found in %v", each.port, each.file, moved)
		}
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
