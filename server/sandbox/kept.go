package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/renameio/v2"
	"google.golang.org/protobuf/encoding/protojson"

	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
)

// kept is where the service's records outlive it: a directory of the
// server's own, with one file for each sandbox, named by its id. A
// record is written before its sandbox is made and removed once the
// sandbox is known to be gone, so that at no moment is there a sandbox
// the service made and holds no record of.
type kept struct{ dir string }

// stored is a record as a file holds it.
type stored struct {
	// Request is the id of the request that made the sandbox, where it
	// had one.
	Request string `json:"request,omitempty"`
	// Spec is the spec it was asked for with, as JSON.
	Spec json.RawMessage `json:"spec"`
	// Created is when it was made.
	Created time.Time `json:"created"`
}

const suffix = ".json"

// open returns the records kept in a directory, making it where it is
// not there.
func open(dir string) (kept, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return kept{}, fmt.Errorf("the sandboxes' records: %w", err)
	}
	return kept{dir: dir}, nil
}

// keep writes a sandbox's record, in place of the one there was. The
// file is whole or it is the earlier one: it is written beside and
// moved into place (renameio).
func (k kept) keep(id string, r *record) error {
	spec, err := protojson.Marshal(r.spec)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(stored{Request: r.request, Spec: spec, Created: r.created})
	if err != nil {
		return err
	}
	if err := renameio.WriteFile(filepath.Join(k.dir, id+suffix), raw, 0o600); err != nil {
		return fmt.Errorf("keeping the record of the sandbox %s: %w", id, err)
	}
	return nil
}

// drop removes a sandbox's record. Dropping one that is gone succeeds.
func (k kept) drop(id string) error {
	if err := os.Remove(filepath.Join(k.dir, id+suffix)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("dropping the record of the sandbox %s: %w", id, err)
	}
	return nil
}

// all returns every record kept, by sandbox.
func (k kept) all() (map[string]*record, error) {
	entries, err := os.ReadDir(k.dir)
	if err != nil {
		return nil, err
	}
	out := map[string]*record{}
	for _, entry := range entries {
		id, is := strings.CutSuffix(entry.Name(), suffix)
		if !is || strings.HasPrefix(id, ".") {
			// What was being written when the server stopped.
			continue
		}
		file := filepath.Join(k.dir, entry.Name())
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		// A record is read strictly: one written by a server that kept
		// something this one does not know is not read in part.
		var held stored
		err = json.Unmarshal(raw, &held)
		spec := &sandboxv1.SandboxSpec{}
		if err == nil {
			err = protojson.Unmarshal(held.Spec, spec)
		}
		if err != nil {
			return nil, unread(file, id, err)
		}
		out[id] = &record{spec: spec, request: held.Request, created: held.Created, turn: newTurn()}
	}
	return out, nil
}

// unread says that a record cannot be read, which file it is, and what
// is done about it: the file may be removed, and the server then holds
// no record of the sandbox, which is ended through its provider where it
// is still there.
func unread(file, id string, err error) error {
	return fmt.Errorf("the record of the sandbox %s, %s, is not one this server reads (%w): remove the file, and end the sandbox through its provider if it is still there", id, file, err)
}
