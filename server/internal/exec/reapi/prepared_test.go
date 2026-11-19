package reapi_test

import (
	"context"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/exec"
)

func TestPreparedRuntimeRootsAreIndependentInputs(t *testing.T) {
	grid := dial(t)
	program := exec.Action{Root: t.TempDir(), Written: map[string]exec.Written{"config": {Content: []byte("one")}}, Directories: []string{"home", "tmp"}}
	action := shell(root(t, map[string]string{"config": "consumer"}), "cat config runtime/config; test -d runtime/home; test -d runtime/tmp")
	action.Prepared = map[string]exec.Action{"runtime": program}
	key, err := grid.Key(context.Background(), action)
	if err != nil {
		t.Fatal(err)
	}
	result, err := grid.Execute(context.Background(), action)
	if err != nil || result.ExitCode != 0 || string(result.Stdout) != "consumerone" {
		t.Fatalf("independent prepared inputs: %v %v", result, err)
	}
	program.Written["config"] = exec.Written{Content: []byte("two")}
	changed, err := grid.Key(context.Background(), action)
	if err != nil || changed == key {
		t.Fatalf("runtime configuration did not change the key: %q %v", changed, err)
	}
	for _, at := range []string{"", ".", "../elsewhere", "/outside", "runtime/../other", "config"} {
		action.Prepared = map[string]exec.Action{at: program}
		if _, err := grid.Key(context.Background(), action); err == nil {
			t.Errorf("prepared root %q replaced or escaped an input", at)
		}
	}
	action.Prepared = map[string]exec.Action{"runtime": program}
	action.Copies = map[string]exec.Copy{"runtime": {From: program.Root, Files: []string{"."}}}
	if _, err := grid.Key(context.Background(), action); err == nil {
		t.Fatal("prepared root replaced a copied tree")
	}
	delete(action.Copies, "runtime")
	program.Prepared = map[string]exec.Action{}
	program.Prepared["recursive"] = program
	action.Prepared["runtime"] = program
	if _, err := grid.Key(context.Background(), action); err == nil {
		t.Fatal("unbounded prepared-root recursion was accepted")
	}
}
