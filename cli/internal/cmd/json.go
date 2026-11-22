package cmd

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// asJSON is whether what a command prints is written as JSON, for a
// program to read, and not as lines for a person.
var asJSON bool

// emitted writes a resource as the API's own message, as one line of
// JSON: proto field names, enums by name, as protojson writes them.
func emitted(message proto.Message) error {
	raw, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(message)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s\n", raw)
	return err
}

// shown writes a resource as JSON where --json says so, and else as a
// person reads it.
func shown[T proto.Message](message T, plainly func(T)) error {
	if asJSON {
		return emitted(message)
	}
	plainly(message)
	return nil
}

// jsonHelp is what frontseat --help says of --json.
const jsonHelp = `Output: plain lines are for people, and may change between releases. A
program reads --json: each resource a command prints is the API's own
message, one to a line, as protojson writes it (proto field names, enums
by name). An error is one line of JSON on standard error.`
