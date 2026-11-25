package sandbox

import "testing"

// A host volume is said to a gateway under its compute driver's name,
// which is the runtime's where no other is said: a server on another
// machine than its gateway asks a runtime of its own about images, and
// says the gateway's driver apart. A gateway whose sandboxes are given
// no host volume is said none.
func TestAHostVolumeIsSaidUnderTheGatewaysComputeDriver(t *testing.T) {
	for what, c := range map[string]struct {
		gateway Gateway
		want    string
	}{
		"the gateway's driver, said apart":   {Gateway{Runtime: "docker", Compute: "podman", HostVolumes: true}, "podman"},
		"the control: the runtime's, unsaid": {Gateway{Runtime: "docker", HostVolumes: true}, "docker"},
		"no host volume":                     {Gateway{Runtime: "docker", Compute: "podman"}, ""},
	} {
		if got := computeOf(c.gateway); got != c.want {
			t.Errorf("%s: a host volume is said under %q, want %q", what, got, c.want)
		}
	}
}
