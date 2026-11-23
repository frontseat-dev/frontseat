package confine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAServiceProgramComesFromItsPreparedInputs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".frontseat/mise/installs/probe/1/bin")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "probe"), []byte("#!/bin/sh\nprintf prepared"), 0755); err != nil {
		t.Fatal(err)
	}
	spec := valid()
	spec.Environment = []Variable{{Name: "PATH", Parts: []string{"", "/.frontseat/mise/installs/probe/1/bin:/usr/bin:/bin"}}}
	raw := strings.TrimSuffix(encoded(t, spec), "}") + `,"program":true}`
	for _, item := range []struct {
		word, want string
		allowed    bool
	}{{"probe", "probe", true}, {"env", "prepared tool", false}, {"/usr/bin/env", "prepared input", false}} {
		t.Run(item.word, func(t *testing.T) {
			t.Chdir(root)
			a, err := prepare([]string{raw, ".", item.word})
			if item.allowed {
				if err != nil || filepath.Base(a.program) != item.want {
					t.Fatalf("prepared control: %q %v", a.program, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("ambient program was not refused: %q %v", a.program, err)
			}
		})
	}
}
