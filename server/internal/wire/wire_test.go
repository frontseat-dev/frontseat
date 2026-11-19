package wire

import (
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"testing"
)

func TestACoordinateCrossesThePluginContractAndBack(t *testing.T) {
	want := model.Coordinate{Ecosystem: "go", Name: "example.com/greet"}
	if got := ReadCoordinate(Coordinate(want)); got != want {
		t.Errorf("%v crossed as %v", want, got)
	}
}
