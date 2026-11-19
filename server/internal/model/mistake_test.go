package model

import (
	"errors"
	"fmt"
	"testing"
)

func TestAMistakeNamesItsFileWhereItHasOne(t *testing.T) {
	for want, mistake := range map[string]*Mistake{
		"platform/frontseat.yaml: line 3: it takes no key plugin": {File: "platform/frontseat.yaml", Message: "line 3: it takes no key plugin"},
		"no action is named tset":                                 {Message: "no action is named tset"},
	} {
		if got := mistake.Error(); got != want {
			t.Errorf("said %q, want %q", got, want)
		}
	}
}

func TestAMistakeIsFoundBeneathWhatWrapsIt(t *testing.T) {
	var found *Mistake
	if err := fmt.Errorf("evaluating: %w", &Mistake{Message: "a circle"}); !errors.As(err, &found) || found.Message != "a circle" {
		t.Errorf("found %v", found)
	}
}
