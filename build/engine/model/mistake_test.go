package model

import (
	"errors"
	"fmt"
	"testing"
)

func TestAMistakeNamesItsFileWhereItHasOne(t *testing.T) {
	for want, mistake := range map[string]*Mistake{
		"platform/frontseat.yaml:3: it takes no key plugin":                  {Repository: "platform", File: "frontseat.yaml", Line: 3, Message: "it takes no key plugin"},
		"frontseat.yaml:3: it takes no key plugin":                           {File: "frontseat.yaml", Line: 3, Message: "it takes no key plugin"},
		"platform: it generates gen; say so in its catalog file":             {Repository: "platform", Message: "it generates gen", Help: "say so in its catalog file"},
		"line 3: it takes no key plugin":                                     {Line: 3, Message: "it takes no key plugin"},
		"no action is named tset":                                            {Message: "no action is named tset"},
		"platform/a/catalog-info.yaml: it is a map; write it as one":         {Repository: "platform", File: "a/catalog-info.yaml", Message: "it is a map", Help: "write it as one"},
		"platform/a/catalog-info.yaml:9: it is a map; write it as one, here": {Repository: "platform", File: "a/catalog-info.yaml", Line: 9, Rule: "catalog/map", Message: "it is a map", Help: "write it as one, here"},
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
