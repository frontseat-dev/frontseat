// Package pins reads the tools a repository pins. The pins are mise's:
// mise.toml names each tool and its version, and mise.lock names, for
// each platform, where the tool is fetched from and the checksum of what
// is fetched. Frontseat adds no file of its own, and fetches nothing
// itself: on its worker, mise installs an action's tools and says what
// they put in its environment, given both files as they are for the
// tools it names (For).
package pins

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Files are the files that pin a repository's tools, relative to its
// root.
var Files = []string{"mise.toml", "mise.lock"}

// of returns whether a pin, as a mise.toml or a lock writes it, is of one
// of some tools a plugin named: by its own name, or by that name with
// what provides it, "http:rust" for rust.
func of(tools []string) func(written string) bool {
	wanted := map[string]bool{}
	for _, tool := range tools {
		wanted[tool] = true
	}
	return func(written string) bool { return wanted[written] || wanted[plain(written)] }
}

// plain is a tool's own name: what a pin is written by, without what
// provides it. "http:rust" is rust, and "go" is go.
func plain(written string) string {
	if _, name, provided := strings.Cut(written, ":"); provided {
		return name
	}
	return written
}

// Named returns what a lock pins a tool as: the tool's own name, or that
// name with what provides it before it, as mise writes "http:rust". A
// plugin names a tool; the repository says where it comes from. A tool
// the lock does not pin, or pins twice, is an error.
func Named(lock []byte, tool string) (string, error) {
	var file struct {
		Tools map[string]any `toml:"tools"`
	}
	if err := toml.Unmarshal(lock, &file); err != nil {
		return "", fmt.Errorf("mise.lock: %w", err)
	}
	var as []string
	for written := range file.Tools {
		if written == tool || plain(written) == tool {
			as = append(as, written)
		}
	}
	sort.Strings(as)
	switch len(as) {
	case 0:
		return "", fmt.Errorf("mise.lock pins no tool %q", tool)
	case 1:
		return as[0], nil
	}
	return "", fmt.Errorf("mise.lock pins %s twice, as %s", tool, strings.Join(as, " and as "))
}

// Locked reports whether a lock pins a tool for a platform by checksum,
// and says what is missing where it does not. A tool that is made of
// packages, as one from conda is, is locked where each of them is. A
// platform is mise's name for one: "linux-arm64", "linux-x64".
func Locked(lock []byte, tool, platform string) error {
	var file struct {
		Tools map[string][]map[string]any `toml:"tools"`
		// Packages are the packages tools are made of, by platform and
		// by name.
		Packages map[string]map[string]map[string]any `toml:"conda-packages"`
	}
	if err := toml.Unmarshal(lock, &file); err != nil {
		return fmt.Errorf("mise.lock: %w", err)
	}
	named, err := Named(lock, tool)
	if err != nil {
		return err
	}
	for _, entry := range file.Tools[named] {
		locked, _ := entry["platforms."+platform].(map[string]any)
		if checksum, _ := locked["checksum"].(string); checksum == "" {
			continue
		}
		parts, _ := locked["conda_deps"].([]any)
		for _, part := range parts {
			name, _ := part.(string)
			if checksum, _ := file.Packages[platform][name]["checksum"].(string); checksum == "" {
				return fmt.Errorf("mise.lock holds no checksum of %s, which %s is made of, for %s; lock it with: mise lock --platform %s", name, tool, platform, platform)
			}
		}
		return nil
	}
	return fmt.Errorf("mise.lock holds no checksum of %s for %s; lock it with: mise lock --platform %s", tool, platform, platform)
}

// Versions returns the version a mise.toml pins each tool at, by the
// tool's own name, whatever provides it. A tool is pinned by a version
// alone, or by a table that holds one. Two pins of one tool are an
// error.
func Versions(pins []byte) (map[string]string, error) {
	var file struct {
		Tools map[string]any `toml:"tools"`
	}
	if err := toml.Unmarshal(pins, &file); err != nil {
		return nil, fmt.Errorf("mise.toml: %w", err)
	}
	out := map[string]string{}
	for written, pinned := range file.Tools {
		version, _ := pinned.(string)
		if table, is := pinned.(map[string]any); is {
			version, _ = table["version"].(string)
		}
		if version == "" {
			continue
		}
		tool := plain(written)
		if _, twice := out[tool]; twice {
			return nil, fmt.Errorf("mise.toml pins %s twice", tool)
		}
		out[tool] = version
	}
	return out, nil
}

// Unset returns the variables a mise.toml's [env] unsets, by setting
// them to false: mise x removes each from the environment of what it
// runs, and its environment, as mise env says it, does not name them.
func Unset(config []byte) ([]string, error) {
	var whole map[string]any
	if err := toml.Unmarshal(config, &whole); err != nil {
		return nil, fmt.Errorf("mise.toml: %w", err)
	}
	var names []string
	var each func(env any)
	each = func(env any) {
		switch table := env.(type) {
		case []any:
			for _, item := range table {
				each(item)
			}
		case map[string]any:
			for name, value := range table {
				// FOO = false, or FOO = { value = false }.
				if options, is := value.(map[string]any); is {
					value = options["value"]
				}
				if unset, is := value.(bool); is && !unset {
					names = append(names, name)
				}
			}
		}
	}
	each(whole["env"])
	sort.Strings(names)
	return slices.Compact(names), nil
}

// templates are the spans of a value mise renders, and raw a span it
// does not.
var (
	templates = regexp.MustCompile(`(?s)\{\{.*?\}\}|\{%.*?%\}`)
	raw       = regexp.MustCompile(`(?s)\{%-?\s*raw\s*-?%\}.*?\{%-?\s*endraw\s*-?%\}`)
	// unsaid is what a template reads that the pins do not hold: the
	// directory mise is asked in, a file, a command's output.
	unsaid = regexp.MustCompile(`(?:^|[^\w.])(cwd|read_file|exec)\b`)
)

// sayable holds a mise.toml to what can be said once for every action of
// its repository, as it is (tools.Provisioner's environ): from the pins
// alone, in the repository's directory. A file its environment reads
// (_.file, _.source, env_file, dotenv) is not among the pins, and mise
// reads one that is not there as nothing, without a word; a template of
// the directory it is asked in (cwd) would name the repository's for
// every action; one that reads a file or runs a command (read_file,
// exec) reads what is not there. Each is refused by name, where it
// would otherwise be lost; [vars] are held as [env] is, since [env] is
// made of them.
func sayable(whole map[string]any) error {
	for _, reads := range []string{"env_file", "dotenv"} {
		if _, is := whole[reads]; is {
			return fmt.Errorf("%s reads a file of the repository, which an action's environment is not made from: set the variables in [env] itself", reads)
		}
	}
	if settings, _ := whole["settings"].(map[string]any); settings != nil {
		if _, is := settings["env_file"]; is {
			return fmt.Errorf("[settings] env_file reads a file of the repository, which an action's environment is not made from: set the variables in [env] itself")
		}
	}
	var err error
	var each func(section string, env any)
	each = func(section string, env any) {
		switch table := env.(type) {
		case []any:
			for _, item := range table {
				each(section, item)
			}
		case map[string]any:
			if directives, _ := table["_"].(map[string]any); directives != nil && err == nil {
				for _, reads := range []string{"file", "source"} {
					if _, is := directives[reads]; is {
						err = fmt.Errorf("[%s] _.%s reads a file of the repository, which an action's environment is not made from: set the variables in [env] itself", section, reads)
						return
					}
				}
			}
			walk(table, func(value string) {
				if err != nil {
					return
				}
				for _, span := range templates.FindAllString(raw.ReplaceAllString(value, ""), -1) {
					if found := unsaid.FindStringSubmatch(span); found != nil {
						err = fmt.Errorf("[%s] %q reads %s, which an action's environment is not made from: it is made once for its repository, from its pins, in the repository's directory; use config_root, or set the value itself", section, value, found[1])
						return
					}
				}
			})
		}
	}
	each("env", whole["env"])
	each("vars", whole["vars"])
	return err
}

// walk calls each with every text a TOML value holds.
func walk(value any, each func(string)) {
	switch v := value.(type) {
	case string:
		each(v)
	case []any:
		for _, item := range v {
			walk(item, each)
		}
	case map[string]any:
		for _, item := range v {
			walk(item, each)
		}
	}
}

// For returns the pins an action that runs with some tools is given, in
// place of the repository's: its mise.toml and its mise.lock, each
// without what is of no tool the action names. The mise.toml loses its
// tasks and the pins and aliases of other tools, and keeps the rest, its
// environment and settings among it, which mise reads when it runs a
// tool; the mise.lock keeps the named tools' entries and the packages they
// are made of. So an action's key holds the pins of its own tools and not,
// say, a task's or another tool's, which it never runs with; a tool's
// install is keyed the same way.
//
// What is kept is as the repository wrote it, in its order: mise reads
// an environment in order, one variable from those before it. Comments
// are not kept, so a comment changes no action. Tools written as an
// inline table at the root, tools = { ... }, are kept whole: every tool's
// pin is then in every action's key.
func For(config, lock []byte, tools []string) ([]byte, []byte, error) {
	named := of(tools)
	// What is cut is read as mise reads it first, strictly: a mise.toml
	// that is no TOML is not made one by what is left out of it.
	var whole map[string]any
	if err := toml.Unmarshal(config, &whole); err != nil {
		return nil, nil, fmt.Errorf("mise.toml: %w", err)
	}
	if err := sayable(whole); err != nil {
		return nil, nil, fmt.Errorf("mise.toml: %w", err)
	}
	cutConfig, err := cut(config, func(path []string, value *unstable.Node) bool {
		switch {
		case path[0] == "tasks" || path[0] == "task_config":
			return false
		case path[0] == "tools" && len(path) > 1:
			return named(path[1])
		case (path[0] == "tool_alias" || path[0] == "alias") && len(path) > 1:
			to := ""
			if value != nil && value.Kind == unstable.String && len(path) == 2 {
				to = string(value.Data)
			}
			return named(path[1]) || (to != "" && named(to))
		}
		return true
	})
	if err != nil {
		return nil, nil, fmt.Errorf("mise.toml: %w", err)
	}

	// The packages a named tool is made of, by platform and name.
	var locked struct {
		Tools map[string][]map[string]any `toml:"tools"`
	}
	if err := toml.Unmarshal(lock, &locked); err != nil {
		return nil, nil, fmt.Errorf("mise.lock: %w", err)
	}
	parts := map[string]bool{}
	for written, entries := range locked.Tools {
		if !named(written) {
			continue
		}
		for _, entry := range entries {
			for key, value := range entry {
				platform, _ := value.(map[string]any)
				deps, _ := platform["conda_deps"].([]any)
				for _, dep := range deps {
					if name, is := dep.(string); is {
						parts[strings.TrimPrefix(key, "platforms.")+"\x00"+name] = true
					}
				}
			}
		}
	}
	cutLock, err := cut(lock, func(path []string, _ *unstable.Node) bool {
		switch {
		case path[0] == "tools" && len(path) > 1:
			return named(path[1])
		case path[0] == "conda-packages" && len(path) > 2:
			return parts[path[1]+"\x00"+path[2]]
		}
		return true
	})
	if err != nil {
		return nil, nil, fmt.Errorf("mise.lock: %w", err)
	}
	return cutConfig, cutLock, nil
}

// cut returns a TOML document with each of its expressions, a table's
// header or a key and its value, that keep refuses left out, and the rest
// as the document writes them, in its order, each on a line of its own.
// keep is given an expression's whole key, its table's and its own, and
// its value where it has one. go-toml's parser says where each is.
func cut(document []byte, keep func(path []string, value *unstable.Node) bool) ([]byte, error) {
	var p unstable.Parser
	p.Reset(document)
	var out []byte
	var table []string
	for p.NextExpression() {
		expression := p.Expression()
		var keys []string
		var first, last *unstable.Node
		for it := expression.Key(); it.Next(); {
			if first == nil {
				first = it.Node()
			}
			last = it.Node()
			keys = append(keys, string(it.Node().Data))
		}
		var path []string
		var value *unstable.Node
		var start, end int
		switch expression.Kind {
		case unstable.Table, unstable.ArrayTable:
			table = keys
			path = keys
			start = int(first.Raw.Offset)
			// The header ends where its brackets close, after its key.
			end = int(last.Raw.Offset + last.Raw.Length)
			brackets := 1
			if expression.Kind == unstable.ArrayTable {
				brackets = 2
			}
			for end < len(document) && brackets > 0 {
				if document[end] == ']' {
					brackets--
				}
				end++
			}
			// From the brackets it opens with.
			for start > 0 && document[start-1] != '\n' {
				start--
			}
		case unstable.KeyValue:
			path = append(append([]string{}, table...), keys...)
			value = expression.Value()
			start = int(expression.Raw.Offset)
			end = int(expression.Raw.Offset + expression.Raw.Length)
		default:
			continue
		}
		if !keep(path, value) {
			continue
		}
		out = append(out, bytes.TrimLeft(document[start:end], " \t")...)
		out = append(out, '\n')
	}
	if err := p.Error(); err != nil {
		return nil, err
	}
	return out, nil
}
