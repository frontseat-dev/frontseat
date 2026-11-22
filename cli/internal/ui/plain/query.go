package plain

import (
	"fmt"
	"path"
	"strings"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// Answer is what frontseat query answers, as it is printed and as its
// --json writes it: the entities and projects asked about, or why one
// entity depends on another. Its JSON names are kept, for a program
// that reads them; an entity is written in full, kind:namespace/name.
type Answer struct {
	Entities []Entity  `json:"entities,omitempty"`
	Projects []Project `json:"projects,omitempty"`
	Why      *Why      `json:"why,omitempty"`
}

// Entity is an entity of the workspace: where it is, what it depends on
// and what depends on it.
type Entity struct {
	Ref          string   `json:"ref"`
	Repository   string   `json:"repository"`
	Roots        []string `json:"roots,omitempty"`
	DependsOn    []string `json:"dependsOn,omitempty"`
	DependedOnBy []string `json:"dependedOnBy,omitempty"`
	// Projects are the projects it owns, where one entity is asked about.
	Projects []Project `json:"projects,omitempty"`
}

// Project is a project a plugin found, and what it requires.
type Project struct {
	Repository string        `json:"repository"`
	Root       string        `json:"root"`
	Plugin     string        `json:"plugin"`
	Kind       string        `json:"kind"`
	Name       string        `json:"name"`
	Owner      string        `json:"owner,omitempty"`
	Requires   []Requirement `json:"requires,omitempty"`
}

// Requirement is one thing a project requires: what, where its
// ecosystem's files say so, and the entity whose project provides it,
// where the workspace holds it.
type Requirement struct {
	Coordinate string `json:"coordinate"`
	Origin     string `json:"origin"`
	// Version is the version the project's lock resolves it to, as its
	// ecosystem spells it; empty where no lock says one.
	Version  string `json:"version,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// Why is why one entity depends on another, or on what it requires from
// outside the workspace: each path from the one to the other.
type Why struct {
	From  string   `json:"from"`
	To    string   `json:"to"`
	Paths [][]Step `json:"paths"`
}

// Step is one step of a path: From depends on To because of what its
// projects require. Repository and Commit say which version of To the
// workspace holds; unset where To is outside the workspace.
type Step struct {
	From       string        `json:"from"`
	To         string        `json:"to"`
	Because    []Requirement `json:"because"`
	Repository string        `json:"repository,omitempty"`
	Commit     string        `json:"commit,omitempty"`
}

// Answer writes what query answers as lines.
func (p Printer) Answer(a Answer) {
	for _, e := range a.Entities {
		fmt.Fprintf(p.Out, "%s  %s  %s\n", short(e.Ref), shown(e.Repository), shown(strings.Join(e.Roots, " ")))
		if len(e.DependsOn) > 0 {
			fmt.Fprintf(p.Out, "  depends on %s\n", shorts(e.DependsOn))
		}
		if len(e.DependedOnBy) > 0 {
			fmt.Fprintf(p.Out, "  needed by %s\n", shorts(e.DependedOnBy))
		}
		for _, project := range e.Projects {
			p.project(project, false)
		}
	}
	for _, project := range a.Projects {
		p.project(project, true)
	}
	if a.Why == nil {
		return
	}
	if len(a.Why.Paths) == 0 {
		fmt.Fprintf(p.Out, "%s does not depend on %s\n", short(a.Why.From), short(a.Why.To))
	}
	for _, path := range a.Why.Paths {
		chain := []string{short(path[0].From)}
		for _, step := range path {
			chain = append(chain, short(step.To))
		}
		fmt.Fprintln(p.Out, strings.Join(chain, " -> "))
		for _, step := range path {
			var because []string
			for _, r := range step.Because {
				if r.Coordinate == step.To {
					because = append(because, shown(r.Origin)+r.at())
				} else {
					because = append(because, shown(r.Coordinate)+"  "+shown(r.Origin)+r.at())
				}
			}
			line := fmt.Sprintf("  %s -> %s  %s", short(step.From), short(step.To), strings.Join(because, ", "))
			if step.Commit != "" {
				line += fmt.Sprintf("  %s at %s@%.12s", short(step.To), shown(step.Repository), shown(step.Commit))
			}
			fmt.Fprintln(p.Out, line)
		}
	}
}

// project writes a project and what it requires; with its owner where
// it is listed by itself.
func (p Printer) project(project Project, owned bool) {
	line := fmt.Sprintf("  %s %s  %s", shown(project.Kind), shown(project.Name), shown(path.Join(project.Repository, project.Root)))
	if owned && project.Owner != "" {
		line += "  " + short(project.Owner)
	}
	fmt.Fprintln(p.Out, line)
	for _, r := range project.Requires {
		line := fmt.Sprintf("    %s  %s%s", shown(r.Coordinate), shown(r.Origin), r.at())
		if r.Provider != "" {
			line += "  " + short(r.Provider)
		}
		fmt.Fprintln(p.Out, line)
	}
}

// at is the version a requirement resolves to, as a line says it after
// where it is required: " at v1.9.1"; nothing where no lock says one.
func (r Requirement) at() string {
	if r.Version == "" {
		return ""
	}
	return " at " + shown(r.Version)
}

// short is an entity written in full, or a requirement, as a line names
// it: an entity as it is written short (entity.Ref.Short), a component
// of the default namespace by its name alone. A requirement, which is no
// entity written in full, is written as it is.
func short(full string) string {
	if ref, err := entity.Parse(full); err == nil && ref.String() == full {
		return shown(ref.Short())
	}
	return shown(full)
}

// shorts are entities, as a line names them, one after another.
func shorts(full []string) string {
	var out []string
	for _, f := range full {
		out = append(out, short(f))
	}
	return strings.Join(out, ", ")
}
