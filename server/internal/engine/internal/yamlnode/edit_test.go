package yamlnode

import (
	"go.yaml.in/yaml/v4"
	"strings"
	"testing"
)

func TestMaterializationPreservesScalarSpellingAndTags(t *testing.T) {
	for _, scalar := range []string{"0123", "2026-11-07", "18446744073709551615", "1.2300", "'0123'", "true", "null"} {
		var document yaml.Node
		if err := yaml.Unmarshal([]byte("value: &value "+scalar+"\ncopy: *value\n"), &document); err != nil {
			t.Fatal(err)
		}
		root := document.Content[0]
		original := *Child(root, "value")
		if err := Materialize(&document); err != nil {
			t.Fatal(err)
		}
		copied := Child(document.Content[0], "copy")
		if copied.Value != original.Value || copied.Tag != original.Tag || copied.Style != original.Style {
			t.Errorf("%s changed: (%s,%q,%v) -> (%s,%q,%v)", scalar, original.Tag, original.Value, original.Style, copied.Tag, copied.Value, copied.Style)
		}
	}
}

func TestMaterializationBoundsRepeatedScalarBytesWithoutChangingInput(t *testing.T) {
	for _, size := range []int{20, 2 * 1024 * 1024} {
		var document yaml.Node
		if err := yaml.Unmarshal([]byte("value: &value "+strings.Repeat("x", size)+"\ncopies: [*value, *value]\n"), &document); err != nil {
			t.Fatal(err)
		}
		before := document.Content[0]
		err := Materialize(&document)
		if size == 20 && err != nil {
			t.Fatalf("valid repeated alias refused: %v", err)
		}
		if size > 20 && (err == nil || document.Content[0] != before) {
			t.Fatalf("expanded scalar bytes not refused atomically: %v", err)
		}
	}
}

func TestMaterializationBudgetCoversTheWholeFile(t *testing.T) {
	var first, second yaml.Node
	raw := []byte("value: &value " + strings.Repeat("x", 1024*1024) + "\ncopy: *value\n")
	for _, document := range []*yaml.Node{&first, &second} {
		if err := yaml.Unmarshal(raw, document); err != nil {
			t.Fatal(err)
		}
	}
	a, b := first.Content[0], second.Content[0]
	if err := Materialize(&first, &second); err == nil {
		t.Fatal("each document reset the scalar byte budget")
	}
	if first.Content[0] != a || second.Content[0] != b {
		t.Fatal("failed multi-document expansion changed source")
	}
}

func TestMaterializationRefusesCyclesAndDeepExpansion(t *testing.T) {
	for _, raw := range []string{"value: &value {copy: *value}\n", "value: &value [*value]\n"} {
		var document yaml.Node
		if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
			t.Fatal(err)
		}
		if err := Materialize(&document); err == nil {
			t.Fatal("accepted cyclic YAML alias")
		}
	}
	root := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "value"}
	for range 65 {
		root = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{root}}
	}
	if err := Materialize(root); err == nil {
		t.Fatal("accepted excessive nesting")
	}
}
