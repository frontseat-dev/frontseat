package sandbox

import (
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	sandboxv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/internal/records"
)

// kept is where the service's records outlive it: a directory of the
// server's own, with one record for each sandbox, named by its id
// (server/internal/records). A record is written before its sandbox is
// made and removed once the sandbox is known to be gone, so that at no
// moment is there a sandbox the service made and holds no record of.
type kept struct{ records.Dir }

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

// open returns the records kept in a directory, making it where it is
// not there.
func open(dir string) (kept, error) {
	d, err := records.Open(dir)
	if err != nil {
		return kept{}, fmt.Errorf("the sandboxes' records: %w", err)
	}
	return kept{d}, nil
}

// keep writes a sandbox's record, in place of the one there was.
func (k kept) keep(id string, r *record) error {
	spec, err := protojson.Marshal(r.spec)
	if err != nil {
		return err
	}
	if err := k.Keep(id, stored{Request: r.request, Spec: spec, Created: r.created}); err != nil {
		return fmt.Errorf("keeping the record of the sandbox %s: %w", id, err)
	}
	return nil
}

// drop removes a sandbox's record. Dropping one that is gone succeeds.
func (k kept) drop(id string) error {
	if err := k.Drop(id); err != nil {
		return fmt.Errorf("dropping the record of the sandbox %s: %w", id, err)
	}
	return nil
}

// all returns every record kept, by sandbox. A record is read strictly:
// one written by a server that kept something this one does not know is
// not read in part.
func (k kept) all() (map[string]*record, error) {
	ids, err := k.Names()
	if err != nil {
		return nil, err
	}
	out := map[string]*record{}
	for _, id := range ids {
		var held stored
		_, err := k.Read(id, &held)
		spec := &sandboxv1.SandboxSpec{}
		if err == nil {
			err = protojson.Unmarshal(held.Spec, spec)
		}
		if err != nil {
			return nil, unread(k.File(id), id, err)
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
