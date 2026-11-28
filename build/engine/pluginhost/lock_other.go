//go:build !unix

package pluginhost

// lockShared and lockExclusive lock nothing where there is no flock: a
// directory of another version is then never removed (removeOtherVersions).
func lockShared(string) (func(), error) { return func() {}, nil }

func lockExclusive(string) (func(), bool, error) { return func() {}, true, nil }
