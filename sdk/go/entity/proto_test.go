package entity

import (
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"testing"
)

func TestAReferenceCrossesTheContractAndBack(t *testing.T) {
	want := New("system", "edge", "storefront")
	if got := FromProto(Proto(want)); got != want {
		t.Errorf("%v crossed as %v", want, got)
	}
}

func TestWhatAMessageLeavesOutIsAComponentInTheDefaultNamespace(t *testing.T) {
	if got := FromProto(&typev1.EntityRef{Name: "gateway"}).String(); got != "component:default/gateway" {
		t.Errorf("read %q", got)
	}
}
