package evaluate

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/frontseat-dev/frontseat/build/engine/engine/plan"
	"google.golang.org/protobuf/proto"
)

// A prepared program can carry the same nested staging payloads as a finite
// action. Fill each branch, including unknown wire fields, and test the full
// and narrow outward ownership boundaries. This is a copy probe, not a claim
// that a worker executes this synthetic fixture.
func TestRetainedProgramAllNestedPayloadsAreOwned(t *testing.T) {
	mutations := map[string]func(*plan.Action){
		"command environment":      func(a *plan.Action) { a.Declared.Command.Environment["A"] = "mutated" },
		"declared file":            func(a *plan.Action) { a.Declared.GetSharedFetch().Files[0].Content[0] ^= 1 },
		"declared unknown":         func(a *plan.Action) { a.Declared.ProtoReflect().GetUnknown()[2] ^= 1 },
		"context unknown":          func(a *plan.Action) { a.Context.ProtoReflect().GetUnknown()[2] ^= 1 },
		"context nested option":    func(a *plan.Action) { a.Context.Options["tags"].GetStringListValue().Values[0] = "mutated" },
		"inputs":                   func(a *plan.Action) { a.Sources[0].Patterns[0] = "mutated" },
		"references":               func(a *plan.Action) { a.References[0].Rest = "mutated" },
		"reads":                    func(a *plan.Action) { a.Reads[0].Output = "mutated" },
		"generated input":          func(a *plan.Action) { a.Sources[0].Generated[0][1][0] = "mutated" },
		"needs":                    func(a *plan.Action) { a.Needs[0].Name = "mutated" },
		"artifact only":            func(a *plan.Action) { a.ArtifactOnly[0].Name = "mutated" },
		"artifact authority":       func(a *plan.Action) { a.Artifacts[0].Source.Patterns[0] = "mutated" },
		"artifact selection":       func(a *plan.Action) { a.Artifacts[0].Generated[0][0][0] = "mutated" },
		"artifact patterns":        func(a *plan.Action) { a.Artifacts[0].Patterns[0] = "mutated" },
		"generated authority":      func(a *plan.Action) { a.GeneratedSources[0].Patterns[0] = "mutated" },
		"workspace files":          func(a *plan.Action) { a.WorkspaceFiles[0] = "mutated" },
		"handed patterns":          func(a *plan.Action) { a.Handed[0].Patterns[0] = "mutated" },
		"handed generated":         func(a *plan.Action) { a.Handed[0].Generated[0][1][0] = "mutated" },
		"handed artifact optional": func(a *plan.Action) { a.Handed[0].Artifacts[0].Optional = false },
		"arranged bytes":           func(a *plan.Action) { a.Arranged[0].Content[0] ^= 1 },
	}
	for _, projection := range []string{"full", "needed", "asked"} {
		for name, mutate := range mutations {
			t.Run(projection+"/"+name, func(t *testing.T) {
				original := ownedFixture(t)
				program := original.Actions[0]
				program.Beside, program.InStep = nil, nil
				program.Declared.ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 0x01})
				program.Context.ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 0x02})
				original.Actions[0].Beside.Programs = map[string]plan.Action{"one": program, "two": program}
				copyActions := func() []plan.Action {
					if projection == "full" {
						return original.Clone().Actions
					}
					out := ownSelection(original.Actions, original.Actions, original.Entities)
					if projection == "asked" {
						return out.Asked
					}
					return out.Needed
				}
				before := programFingerprint(t, original.Actions)
				owned := copyActions()
				if !bytes.Equal(before, programFingerprint(t, owned)) {
					t.Fatal("copy changed the prepared programs before mutation")
				}
				changed := owned[0].Beside.Programs["one"]
				mutate(&changed)
				owned[0].Beside.Programs["one"] = changed
				if bytes.Equal(before, programFingerprint(t, owned)) {
					t.Fatal("mutation control was not observable")
				}
				if !bytes.Equal(before, programFingerprint(t, original.Actions)) || !bytes.Equal(before, programFingerprint(t, copyActions())) {
					t.Fatal("returned program mutation reached original or subsequent return")
				}
			})
		}
	}
}

func programFingerprint(t *testing.T, actions []plan.Action) []byte {
	t.Helper()
	var wires [][]byte
	for _, name := range []string{"one", "two"} {
		a := actions[0].Beside.Programs[name]
		for _, message := range []proto.Message{a.Declared, a.Context} {
			wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			wires = append(wires, wire)
		}
	}
	raw, err := json.Marshal(struct {
		Actions []plan.Action
		Wire    [][]byte
	}{actions, wires})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
