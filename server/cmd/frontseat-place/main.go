// Command frontseat-place is what a fetch's command runs, confined as
// every action's is: it unpacks what the asset service brought into the
// fetch's root, or cuts the parts a lock holds out of it and checks
// them. All it does is server/internal/place's (Run), whose form names
// it. It is built statically and installed beside the confiner, at
// /bb/frontseat-place-<form>.
package main

import "github.com/frontseat-dev/frontseat/server/internal/place"

func main() { place.Run() }
