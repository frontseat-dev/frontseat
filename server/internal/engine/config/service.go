package config

import (
	"bytes"
	"fmt"
	"google.golang.org/protobuf/types/known/durationpb"
	"sort"
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
	// Namespace and Name are the Resource's.
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

// Ref is the Resource's reference, as a catalog writes it.
func (l Service) Ref() string {
	kind := l.Kind
	if kind == "" {
		kind = "resource"
	}
	return kind + ":" + l.Namespace + "/" + l.Name
}

// Mistake is a mistake in what is said of the service, where it is
// written.
func (l Service) Mistake(err error) error {
	return &model.Mistake{File: l.File, Message: fmt.Sprintf("line %d: %v", l.Line, err)}
}

// serviceKeys are what service takes; anything else is refused by name,
// privileged among them.
var serviceKeys = map[string]bool{
	"as": true, "image": true, "args": true, "ports": true, "env": true,
	"ready": true, "timeout": true, "exports": true, "program": true, "apis": true,
	"endpoints": true, "user": true, "writable": true,
}

// serviceValues is what each key of a service takes, as a mistake says it.
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
	"user":      "text: the user its image runs as, by number, never 0",
	"writable":  "a list of the directories of its image it writes, each an absolute path",
}

// nullWithin returns a null written beneath a node, or nil: an entry of
// a list or a value of a map, at any depth. The node itself is not
// looked at: a key written with no value says nothing, which is said so.
func nullWithin(node *yaml.Node) *yaml.Node {
	for _, child := range node.Content {
		if child.Kind == yaml.ScalarNode && child.Tag == "!!null" {
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
	Port string `yaml:"port"`
	HTTP *struct {
		Port string `yaml:"port"`
		Path string `yaml:"path"`
		Body string `yaml:"body"`
	} `yaml:"http"`
	Command *[]string `yaml:"command"`
}

// readService reads spec.frontseat.service of one catalog document into
// the shape a service has, each key into the field that holds it. This
// is the one place a service is turned from what a file holds: a number
// the shape cannot hold is refused by what reads the file, a readiness
// check is of one kind, and what the shape can hold wrongly is refused
// by the one check of it (testservice.Shaped). A key written to say
// nothing is refused: what is written is kept by the plugin as it is,
// and nothing cannot be kept.
func readService(file, directory string, document, node *yaml.Node) (Service, error) {
	mistake := func(line int, format string, a ...any) (Service, error) {
		return Service{}, &model.Mistake{File: file, Message: fmt.Sprintf("line %d: ", line) + fmt.Sprintf(format, a...)}
	}
	kind := yamlnode.Child(document, "kind")
	if kind == nil || (kind.Value != "Resource" && kind.Value != "Component") {
		return mistake(node.Line, "only a Resource or Component is started as a service; this is a %s", value(kind))
	}
	if node.Kind != yaml.MappingNode {
		return mistake(node.Line, "service is a map: what it is, and what is written of it")
	}
	l := Service{File: file, Directory: directory, Line: node.Line, Kind: strings.ToLower(kind.Value), Namespace: "default", Said: &typev1.Service{}}
	if name := yamlnode.Child(yamlnode.Child(document, "metadata"), "name"); name != nil {
		l.Name = name.Value
	}
	if ns := yamlnode.Child(yamlnode.Child(document, "metadata"), "namespace"); ns != nil && ns.Value != "" {
		l.Namespace = strings.ToLower(ns.Value)
	}
	l.Name = strings.ToLower(l.Name)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i], node.Content[i+1]
		if !serviceKeys[key.Value] {
			return mistake(key.Line, "service takes no key %s; it takes %s", key.Value, keysOf(serviceKeys))
		}
		var (
			checks  []ready
			seconds int32
			target  any
			nothing func() bool
		)
		switch key.Value {
		case "as":
			target, nothing = &l.As, func() bool { return false }
		case "program":
			target, nothing = &l.Program, func() bool { return l.Program == "" }
		case "apis":
			target, nothing = &l.Said.Apis, func() bool { return len(l.Said.Apis) == 0 }
		case "image":
			target, nothing = &l.Said.Image, func() bool { return l.Said.Image == "" }
		case "args":
			target, nothing = &l.Said.Arguments, func() bool { return len(l.Said.Arguments) == 0 }
		case "env":
			target, nothing = &l.Said.Environment, func() bool { return len(l.Said.Environment) == 0 }
		case "exports":
			target, nothing = &l.Said.Exports, func() bool { return len(l.Said.Exports) == 0 }
		case "endpoints":
			target, nothing = &l.Said.Endpoints, func() bool { return len(l.Said.Endpoints) == 0 }
		case "user":
			target, nothing = &l.Said.User, func() bool { return l.Said.User == "" }
		case "writable":
			target, nothing = &l.Said.Writable, func() bool { return len(l.Said.Writable) == 0 }
		case "ports":
			// Read as the shape holds them: a number it cannot hold is
			// refused by what reads the file, never cut short.
			target, nothing = &l.Said.Ports, func() bool { return len(l.Said.Ports) == 0 }
		case "timeout":
			target, nothing = &seconds, func() bool { return seconds == 0 }
		case "ready":
			target, nothing = &checks, func() bool { return len(checks) == 0 }
		}
		// Nothing among what is written is null: a list that held one
		// would be read without it, and what is started would not be
		// what was written.
		if null := nullWithin(val); null != nil {
			return mistake(null.Line, "service.%s holds a null: say what it is, or leave it out", key.Value)
		}
		if err := decodeKnown(val, target); err != nil {
			return mistake(key.Line, "service.%s is not %s", key.Value, serviceValues[key.Value])
		}
		if nothing() {
			return mistake(key.Line, "service.%s is written and says nothing: leave it out, and it is its plugin's to say", key.Value)
		}
		if key.Value == "timeout" {
			l.Said.Timeout = durationpb.New(time.Duration(seconds) * time.Second)
		}
		for n, c := range checks {
			said := &typev1.ServiceReady{}
			switch kinds := []bool{c.Port != "", c.HTTP != nil, c.Command != nil}; {
			case kinds[0] && !kinds[1] && !kinds[2]:
				said.Check = &typev1.ServiceReady_Port{Port: c.Port}
			case kinds[1] && !kinds[0] && !kinds[2]:
				said.Check = &typev1.ServiceReady_Http{Http: &typev1.ServiceHttpReady{Port: c.HTTP.Port, Path: c.HTTP.Path, Body: c.HTTP.Body}}
			case kinds[2] && !kinds[0] && !kinds[1]:
				said.Check = &typev1.ServiceReady_Command{Command: &typev1.ServiceCommandReady{Arguments: *c.Command}}
			default:
				return mistake(key.Line, "service.ready[%d] is one of port, http or command", n)
			}
			l.Said.Ready = append(l.Said.Ready, said)
		}
	}
	// Who says how it is started is named: nothing is started by a form
	// of its own.
	if l.As == "" && l.Program == "" {
		return mistake(node.Line, "service does not say what it is: write as: container for an image written out here, or the name of what a plugin of the repository starts")
	}
	if l.As != "" && l.Program != "" {
		return mistake(node.Line, "service selects either as or program, never both")
	}
	if l.Kind == "component" && l.Program == "" {
		return mistake(node.Line, "only a Resource is started from an image; a Component selects a program")
	}
	if l.Kind == "component" && len(l.Said.Exports) > 0 {
		return mistake(node.Line, "a Component exports through each provided API in service.apis")
	}
	if l.Kind == "resource" && len(l.Said.Apis) > 0 {
		return mistake(node.Line, "a Resource provides no APIs")
	}
	if l.Program != "" {
		if _, err := model.ParseReference("", directory, l.Program); err != nil {
			return Service{}, l.Mistake(err)
		}
		if err := testservice.Program(l.Said); err != nil {
			return Service{}, l.Mistake(err)
		}
	}
	if err := testservice.Shaped(l.Said); err != nil {
		return Service{}, l.Mistake(err)
	}
	return l, nil
}

// decodeKnown decodes a node into target, refusing a key target has not.
func decodeKnown(node *yaml.Node, target any) error {
	raw, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	return decoder.Decode(target)
}

func value(node *yaml.Node) string {
	if node == nil {
		return "document of no kind"
	}
	return node.Value
}

func keysOf(m map[string]bool) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
