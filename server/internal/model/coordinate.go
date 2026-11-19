package model

// Coordinate names a package in an ecosystem: what a project can be
// required by, and what a project requires. It is written
// "go:example.com/greet".
type Coordinate struct {
	// Ecosystem is the ecosystem: "go", "maven", "npm".
	Ecosystem string
	// Name is the package's name there.
	Name string
}

// String writes the coordinate: its ecosystem, a colon and its name.
func (c Coordinate) String() string { return c.Ecosystem + ":" + c.Name }
