package config

import (
	"fmt"
	"google.golang.org/protobuf/types/known/durationpb"
	"maps"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"

	"github.com/frontseat-dev/frontseat/server/internal/engine/internal/yamlnode"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/testservice"
)

// Service is how an entity is started for the tests of the components
// that depend on it: spec.frontseat.service in its catalog file. A
// Resource says what it is, as a name a plugin of the repository claims,
// and that plugin says the whole of it: what is written beside the name
// is kept, and the rest is the plugin's. What is started is what the
// plugin answered, held to what a service is (server/internal/testservice).
// A Component selects one of its programs instead.
type Service struct {
	// File is the catalog file it is written in, relative to the
	// workspace; Directory the entity's directory in the repository; and
	// Line where in the file it is written.
	File, Directory string
	Line            int
	// Namespace and Name are the entity's.
	Namespace, Name string
	// Kind owns the runtime; Repository locates its declared program.
	Kind, Repository string
	// Program selects a program action (Action.program), exclusively of
	// As.
	Program string
	// As is what it is: "container" for an image written out here, or
	// what another plugin starts, such as "postgres".
	As string
	// Said is what is said of it, in the one shape a service has: what
	// was written beside As, as it is read here; and, once the plugin
	// that claims As has answered, the whole of it.
	Said *typev1.Service
}

// Ref is the entity's reference, as a catalog writes it.
func (service Service) Ref() string {
	kind := service.Kind
	if kind == "" {
		kind = "resource"
	}
	return kind + ":" + service.Namespace + "/" + service.Name
}

// Mistake is a mistake in what is said of the service, where it is
// written.
func (service Service) Mistake(err error) error {
	return &model.Mistake{File: service.File, Message: fmt.Sprintf("line %d: %v", service.Line, err)}
}

// serviceValues is what each key of a service takes, as a mistake says
// it; a key it does not hold is refused, with the keys it holds.
var serviceValues = map[string]string{
	"as":        "a name: what a plugin of the repository starts",
	"endpoints": "names, each with an address of {host} and one of its ports",
	"program":   "an action reference naming one program",
	"apis":      "provided API references, each with a port and exports",
	"image":     "text: an image, named with its registry and digest",
	"args":      "a list of text",
	"env":       "names, each with its value as text",
	"exports":   "names, each with its value as text",
	"ports":     "names, each with its port, a whole number",
	"timeout":   "a whole number of seconds",
	"ready":     "a list of checks, each one of port: <name>, http: {port, path, body} or command: [...]",
	"user":      "text: the user its image runs as, by number, never 0, written in quotes, as \"70\"",
	"writable":  "a list of the directories of its image it writes, each an absolute path",
}

// null reports whether a node is a null: a key written with no value.
func null(node *yaml.Node) bool { return node.Kind == yaml.ScalarNode && node.Tag == "!!null" }

// nullWithin returns a null written beneath a node, or nil: an entry of
// a list or a value of a map, at any depth. The node itself is not
// looked at: a key written with no value says nothing, which is said so.
func nullWithin(node *yaml.Node) *yaml.Node {
	for _, child := range node.Content {
		if null(child) {
			return child
		}
		if found := nullWithin(child); found != nil {
			return found
		}
	}
	return nil
}

// ready is one readiness check as a catalog file writes it: one of a
// port, an HTTP answer or a command.
type ready struct {
	port    string
	http    *typev1.ServiceHttpReady
	command *[]string
}

// readReady reads service.ready: a list of checks, each a map of port,
// http and command, an HTTP answer a map of port, path and body. Which
// of them a check is, and that it is one, is for whoever reads them.
func readReady(node *yaml.Node) ([]ready, bool) {
	if node.Kind != yaml.SequenceNode {
		return nil, false
	}
	var out []ready
	for _, entry := range node.Content {
		if entry.Kind != yaml.MappingNode {
			return nil, false
		}
		var check ready
		for i := 0; i+1 < len(entry.Content); i += 2 {
			key, said := entry.Content[i], entry.Content[i+1]
			ok := false
			switch key.Value {
			case "port":
				check.port, ok = text(said)
			case "http":
				var asked map[string]string
				if asked, ok = mapOf(said, text); ok {
					check.http = &typev1.ServiceHttpReady{Port: asked["port"], Path: asked["path"], Body: asked["body"]}
					delete(asked, "port")
					delete(asked, "path")
					delete(asked, "body")
					ok = len(asked) == 0
				}
			case "command":
				command, wrong := textList(said)
				check.command, ok = &command, wrong == nil
			}
			if !ok {
				return nil, false
			}
		}
		out = append(out, check)
	}
	return out, true
}

// readAPI reads one entry of service.apis: the port an API is served
// on, and what a test that consumes it is told.
func readAPI(node *yaml.Node) (*typev1.ServiceApi, bool) {
	if node.Kind != yaml.MappingNode {
		return nil, false
	}
	api := &typev1.ServiceApi{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, said := node.Content[i], node.Content[i+1]
		ok := false
		switch key.Value {
		case "port":
			api.Port, ok = text(said)
		case "exports":
			api.Exports, ok = mapOf(said, text)
		}
		if !ok {
			return nil, false
		}
	}
	return api, true
}

// readService reads spec.frontseat.service of one catalog document into
// the shape a service has, each key into the field that holds it. This
// is the one place a service is turned from what a file holds: text is
// what was written as text, a number the shape cannot hold is refused by
// what reads the file, a readiness check is of one kind, and what the
// shape can hold wrongly is refused by the one check of it
// (testservice.Shaped). A key written to say nothing is refused: what is
// written is kept by the plugin as it is, and nothing cannot be kept.
func readService(file, directory string, document, node *yaml.Node) (Service, error) {
	mistake := func(line int, format string, a ...any) (Service, error) {
		return Service{}, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: ", line) + fmt.Sprintf(format, a...)}
	}
	kind := yamlnode.Child(document, "kind")
	if kind == nil || (kind.Value != "Resource" && kind.Value != "Component") {
		return mistake(node.Line, "only a Resource or Component is started as a service")
	}
	if node.Kind != yaml.MappingNode {
		return mistake(node.Line, "service is a map: what it is, and what is written of it")
	}
	service := Service{File: file, Directory: directory, Line: node.Line, Kind: strings.ToLower(kind.Value), Namespace: "default", Said: &typev1.Service{}}
	if name := yamlnode.Child(yamlnode.Child(document, "metadata"), "name"); name != nil {
		service.Name = name.Value
	}
	if ns := yamlnode.Child(yamlnode.Child(document, "metadata"), "namespace"); ns != nil && ns.Value != "" {
		service.Namespace = strings.ToLower(ns.Value)
	}
	service.Name = strings.ToLower(service.Name)
	said := service.Said
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i], node.Content[i+1]
		if _, taken := serviceValues[key.Value]; !taken {
			return mistake(key.Line, "service takes no key %s; it takes %s", key.Value, strings.Join(slices.Sorted(maps.Keys(serviceValues)), ", "))
		}
		// Nothing among what is written is null: a list that held one
		// would be read without it, and what is started would not be
		// what was written.
		if within := nullWithin(val); within != nil {
			return mistake(within.Line, "service.%s holds a null: say what it is, or leave it out", key.Value)
		}
		var (
			checks  []ready
			seconds int32
			wrong   *yaml.Node
			// read is whether the value is what its key takes, and
			// nothing whether it then says nothing.
			read, nothing = true, false
		)
		switch written := !null(val); key.Value {
		case "as":
			if written {
				service.As, read = text(val)
			}
		case "program":
			if written {
				service.Program, read = text(val)
			}
			nothing = service.Program == ""
		case "apis":
			if written {
				said.Apis, read = mapOf(val, readAPI)
			}
			nothing = len(said.Apis) == 0
		case "image":
			if written {
				said.Image, read = text(val)
			}
			nothing = said.Image == ""
		case "args":
			if written {
				said.Arguments, wrong = textList(val)
			}
			read, nothing = wrong == nil, len(said.Arguments) == 0
		case "env":
			if written {
				said.Environment, read = mapOf(val, text)
			}
			nothing = len(said.Environment) == 0
		case "exports":
			if written {
				said.Exports, read = mapOf(val, text)
			}
			nothing = len(said.Exports) == 0
		case "endpoints":
			if written {
				said.Endpoints, read = mapOf(val, text)
			}
			nothing = len(said.Endpoints) == 0
		case "user":
			if written {
				said.User, read = text(val)
			}
			nothing = said.User == ""
		case "writable":
			if written {
				said.Writable, wrong = textList(val)
			}
			read, nothing = wrong == nil, len(said.Writable) == 0
		case "ports":
			// Read as the shape holds them: a number it cannot hold is
			// refused by what reads the file, never cut short.
			if written {
				said.Ports, read = mapOf(val, whole[int32])
			}
			nothing = len(said.Ports) == 0
		case "timeout":
			if written {
				seconds, read = whole[int32](val)
			}
			nothing = seconds == 0
		case "ready":
			if written {
				checks, read = readReady(val)
			}
			nothing = len(checks) == 0
		}
		if !read {
			return mistake(key.Line, "service.%s is not %s", key.Value, serviceValues[key.Value])
		}
		if nothing {
			return mistake(key.Line, "service.%s is written and says nothing: leave it out, and it is its plugin's to say", key.Value)
		}
		// A name is held where it is written, so that its refusal says
		// its own line: what a service's shape is held to again
		// (testservice.Shaped) knows no line.
		for i := 0; val.Kind == yaml.MappingNode && i+1 < len(val.Content); i += 2 {
			name, held := val.Content[i], error(nil)
			switch key.Value {
			case "exports", "env":
				held = testservice.Variable(key.Value, name.Value)
			case "ports":
				held = testservice.Port(name.Value, said.Ports[name.Value])
			}
			if held != nil {
				return mistake(name.Line, "%v", held)
			}
		}
		if key.Value == "timeout" {
			said.Timeout = durationpb.New(time.Duration(seconds) * time.Second)
		}
		for n, c := range checks {
			check := &typev1.ServiceReady{}
			switch kinds := []bool{c.port != "", c.http != nil, c.command != nil}; {
			case kinds[0] && !kinds[1] && !kinds[2]:
				check.Check = &typev1.ServiceReady_Port{Port: c.port}
			case kinds[1] && !kinds[0] && !kinds[2]:
				check.Check = &typev1.ServiceReady_Http{Http: c.http}
			case kinds[2] && !kinds[0] && !kinds[1]:
				check.Check = &typev1.ServiceReady_Command{Command: &typev1.ServiceCommandReady{Arguments: *c.command}}
			default:
				return mistake(key.Line, "service.ready[%d] is one of port, http or command", n)
			}
			said.Ready = append(said.Ready, check)
		}
	}
	// Who says how it is started is named: nothing is started by a form
	// of its own.
	if service.As == "" && service.Program == "" {
		return mistake(node.Line, "service does not say what it is: write as: container for an image written out here, or the name of what a plugin of the repository starts")
	}
	if service.As != "" && service.Program != "" {
		return mistake(node.Line, "service selects either as or program, never both")
	}
	if service.Kind == "component" && service.Program == "" {
		return mistake(node.Line, "only a Resource is started from an image; a Component selects a program")
	}
	if service.Kind == "component" && len(said.Exports) > 0 {
		return mistake(node.Line, "a Component exports through each provided API in service.apis")
	}
	if service.Kind == "resource" && len(said.Apis) > 0 {
		return mistake(node.Line, "a Resource provides no APIs")
	}
	if service.Program != "" {
		if _, err := model.ParseReference("", directory, service.Program); err != nil {
			return Service{}, service.Mistake(err)
		}
		if err := testservice.Program(said); err != nil {
			return Service{}, service.Mistake(err)
		}
	}
	if err := testservice.Shaped(said); err != nil {
		return Service{}, service.Mistake(err)
	}
	return service, nil
}
