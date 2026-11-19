package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/frontseat-dev/frontseat/specs/internal/generated"
)

// compareGenerated holds the Go that Buf generates from the contracts to what it
// writes now: each template of the buf workspace (buf.gen.*.yaml) is run
// into a directory of the gate's own, laid out as the repository is, and
// each directory a template writes into is compared with the repository's,
// file by file. A file that differs, one that is not generated now and one
// that is generated and not committed each fail the gate, by name.
func compareGenerated(root string) int {
	templates, err := generated.Templates(root)
	if err != nil {
		return failed(err)
	}
	if len(templates) == 0 {
		return failed(fmt.Errorf("%s holds no template of what Buf generates", generated.Contracts))
	}
	scratch, err := os.MkdirTemp("", "gate-generated-")
	if err != nil {
		return failed(err)
	}
	defer os.RemoveAll(scratch)
	var differ []string
	for _, template := range templates {
		outs, err := generated.Outputs(template)
		if err != nil {
			return failed(err)
		}
		// Buf writes each output beneath what -o names, as it does beneath
		// the workspace: the workspace's place in the scratch directory
		// is its place in the repository.
		if code := run(filepath.Join(root, generated.Contracts), environment(os.Environ(), unsaid), "buf", "generate", "--template", filepath.Base(template), "-o", filepath.Join(scratch, generated.Contracts)); code != 0 {
			return code
		}
		for _, out := range outs {
			found, err := compared(filepath.Join(root, out), filepath.Join(scratch, out))
			if err != nil {
				return failed(err)
			}
			for _, each := range found {
				differ = append(differ, filepath.ToSlash(filepath.Join(out, each)))
			}
		}
	}
	if len(differ) == 0 {
		return 0
	}
	slices.Sort(differ)
	fmt.Fprintln(complained, strings.Join(slices.Compact(differ), "\n"))
	fmt.Fprintln(complained, "the committed Go above is not what Buf generates from the contracts now: run `mise run protos`")
	return 1
}

// compared returns the files, relative to each directory, that are not
// alike in the two: in one and not the other, or different in each.
func compared(committed, written string) ([]string, error) {
	have, err := files(committed)
	if err != nil {
		return nil, err
	}
	want, err := files(written)
	if err != nil {
		return nil, err
	}
	var differ []string
	for name, content := range want {
		if there, held := have[name]; !held || !bytes.Equal(there, content) {
			differ = append(differ, name)
		}
	}
	for name := range have {
		if _, held := want[name]; !held {
			differ = append(differ, name)
		}
	}
	return differ, nil
}

// files are the files beneath a directory, by their path from it, with
// what each holds; none where there is no directory.
func files(dir string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			if at == dir && os.IsNotExist(err) {
				return filepath.SkipAll
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, at)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(at)
		out[rel] = content
		return err
	})
	return out, err
}
