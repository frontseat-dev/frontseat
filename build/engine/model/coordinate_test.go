package model

import "testing"

func TestACoordinateIsWrittenAsItsEcosystemAndItsName(t *testing.T) {
	if got := (Coordinate{Ecosystem: "go", Name: "example.com/greet"}).String(); got != "go:example.com/greet" {
		t.Errorf("written %q", got)
	}
}

func TestTwoCoordinatesOfOneNameInTwoEcosystemsDiffer(t *testing.T) {
	if (Coordinate{Ecosystem: "go", Name: "greet"}) == (Coordinate{Ecosystem: "npm", Name: "greet"}) {
		t.Error("a Go module and an npm package of one name are one coordinate")
	}
}
