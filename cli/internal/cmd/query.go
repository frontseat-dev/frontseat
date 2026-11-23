package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frontseat-dev/frontseat/cli/internal/ui/plain"
	"github.com/frontseat-dev/frontseat/sdk/go"
	buildv1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/build/v1alpha1"
	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

var (
	queryWhy  string
	queryJSON bool
)

// queryCmd says what the workspace is made of.
var queryCmd = &cobra.Command{
	Use:   "query [TARGET]",
	Short: "Say what the workspace is made of, and why one thing depends on another",
	Long: `Say what the workspace is made of, as it stands; nothing is built.

With nothing named, every entity: where it is, and what it depends on.
Named an entity, that entity: what it depends on, what depends on it,
and each of its projects with what it requires, the line that says so,
and the entity that provides it. Named a path, the projects at or
beneath it.

With --why, why the entity named depends on another: the shortest chain
from the one to the other, every step with what is required, the line
that says so, and the commit the workspace holds the repository of the
entity required at. Or why it depends on something from outside the
workspace, as its projects require it (go:MODULE, npm:PACKAGE): the
shortest chain to each entity whose projects require it, the line that
says so, and the version its lock resolves it to there ("at v1.9.1").
One that does not depend on the other says so, and the command exits 1.

With --json, the answer is one line of JSON, whose names are kept for a
program to read.`,
	Example: `  frontseat query
  frontseat query gateway
  frontseat query platform/
  frontseat query gateway --why ledger
  frontseat query gateway --why go:github.com/spf13/cobra --json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := ""
		if len(args) == 1 {
			target = args[0]
		}
		switch {
		case strings.HasPrefix(target, "//"):
			return fmt.Errorf("%s is an action, which a build runs: query names an entity or a path", target)
		case queryWhy != "" && target == "":
			return errors.New("--why says why an entity depends on another: name the entity it is asked of")
		}
		w, err := building()
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		entities, err := frontseat.Collect(w.Entities(ctx))
		if err != nil {
			return err
		}
		projects, err := frontseat.Collect(w.Projects(ctx))
		if err != nil {
			return err
		}
		g := graphOf(entities, projects)
		var answer plain.Answer
		switch {
		case queryWhy != "":
			// Which commit each repository stands at the daemon says of
			// each entity, as the workspace records it; of a checkout it
			// was given it says none, and a chain is said without it.
			commits := map[string]string{}
			for _, e := range g.entities {
				if e.GetCommit() != "" {
					commits[e.GetRepository()] = e.GetCommit()
				}
			}
			why, err := g.why(target, queryWhy, commits)
			if err != nil {
				return err
			}
			answer.Why = why
			if len(why.Paths) == 0 {
				exitCode = 1
			}
		case target == "":
			for _, e := range entities {
				answer.Entities = append(answer.Entities, g.entity(e, false))
			}
		case target == "." || strings.Contains(target, "/") && !strings.Contains(target, ":"):
			answer.Projects = g.beneath(target)
			if len(answer.Projects) == 0 {
				return fmt.Errorf("there is no project at or beneath %s", target)
			}
		default:
			e, err := g.find(target)
			if err != nil {
				return err
			}
			answer.Entities = []plain.Entity{g.entity(e, true)}
		}
		if queryJSON {
			raw, err := json.Marshal(answer)
			if err != nil {
				return err
			}
			said("%s", raw)
			return nil
		}
		plain.Printer{Out: out}.Answer(answer)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(queryCmd)
	worksWhereItIsRun(queryCmd)
	queryCmd.Flags().StringVar(&queryWhy, "why", "", "say why the entity named depends on this entity, or on this requirement from outside the workspace")
	queryCmd.Flags().BoolVar(&queryJSON, "json", false, "write the answer as one line of JSON, for a program to read")
}

// graph is the workspace's entities and projects, as query reads them.
type graph struct {
	entities []*buildv1.Entity
	projects []*buildv1.Project
	// owner is the entity that owns each project, by where it is.
	owner map[projectAt]string
}

// projectAt is where a project is: one plugin's, in one directory of one
// repository.
type projectAt struct{ repository, root, plugin string }

func graphOf(entities []*buildv1.Entity, projects []*buildv1.Project) graph {
	g := graph{entities: entities, projects: projects, owner: map[projectAt]string{}}
	for _, p := range projects {
		if p.GetOwner() != nil {
			g.owner[projectAt{p.GetRef().GetRepository(), p.GetRef().GetRoot(), p.GetRef().GetPlugin()}] = full(p.GetOwner())
		}
	}
	return g
}

// full is an entity's reference, written in full.
func full(ref *typev1.EntityRef) string { return entity.FromProto(ref).String() }

// errNoEntity is a name of an entity the workspace holds none of.
var errNoEntity = errors.New("the workspace holds no entity")

// find is the one entity a person's name for it names; a name that names
// several is refused, saying which.
func (g graph) find(written string) (*buildv1.Entity, error) {
	typed, err := entity.ParsePattern(written)
	if err != nil {
		return nil, fmt.Errorf("%w %q: %v", errNoEntity, written, err)
	}
	var refs []entity.Ref
	held := map[entity.Ref]*buildv1.Entity{}
	for _, e := range g.entities {
		ref := entity.FromProto(e.GetRef())
		refs = append(refs, ref)
		held[ref] = e
	}
	one, found, err := typed.Find(refs)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w %s", errNoEntity, written)
	}
	return held[one], nil
}

// entity is an entity as query answers it: with what depends on it and
// its projects where it is the one asked about.
func (g graph) entity(e *buildv1.Entity, whole bool) plain.Entity {
	out := plain.Entity{Ref: full(e.GetRef()), Repository: e.GetRepository(), Roots: e.GetRoots()}
	for _, d := range e.GetDependsOn() {
		out.DependsOn = append(out.DependsOn, full(d))
	}
	if !whole {
		return out
	}
	for _, other := range g.entities {
		for _, d := range other.GetDependsOn() {
			if full(d) == out.Ref {
				out.DependedOnBy = append(out.DependedOnBy, full(other.GetRef()))
			}
		}
	}
	for _, p := range g.projects {
		if p.GetOwner() != nil && full(p.GetOwner()) == out.Ref {
			out.Projects = append(out.Projects, g.project(p))
		}
	}
	return out
}

// project is a project as query answers it.
func (g graph) project(p *buildv1.Project) plain.Project {
	out := plain.Project{Repository: p.GetRef().GetRepository(), Root: p.GetRef().GetRoot(), Plugin: p.GetRef().GetPlugin(), Kind: p.GetKind(), Name: p.GetName()}
	if p.GetOwner() != nil {
		out.Owner = full(p.GetOwner())
	}
	for _, r := range p.GetRequires() {
		out.Requires = append(out.Requires, g.requirement(r))
	}
	return out
}

func (g graph) requirement(r *buildv1.ProjectRequirement) plain.Requirement {
	// Where it is said is written from the workspace: its repository's
	// name, the file, and the line.
	origin := r.GetOrigin()
	out := plain.Requirement{Coordinate: r.GetCoordinate(), Origin: fmt.Sprintf("%s:%d", path.Join(origin.GetRepository(), origin.GetPath()), origin.GetLine()), Version: r.GetVersion()}
	if at := r.GetProviderProject(); at != nil {
		out.Provider = g.owner[projectAt{at.GetRepository(), at.GetRoot(), at.GetPlugin()}]
	}
	return out
}

// beneath are the projects at or beneath a path of the workspace,
// written as its repository's name and a directory of it.
func (g graph) beneath(written string) []plain.Project {
	at := strings.Trim(path.Clean(written), "/")
	if at == "." {
		at = ""
	}
	var out []plain.Project
	for _, p := range g.projects {
		where := path.Join(p.GetRef().GetRepository(), p.GetRef().GetRoot())
		if at == "" || where == at || strings.HasPrefix(where, at+"/") {
			out = append(out, g.project(p))
		}
	}
	return out
}

// why is why one entity depends on another, or on a requirement from
// outside the workspace: the shortest chain to the entity, or the
// shortest chain to each entity whose projects require the requirement.
// commits are the commits of the workspace's repositories, by name.
func (g graph) why(from, to string, commits map[string]string) (*plain.Why, error) {
	start, err := g.find(from)
	if err != nil {
		return nil, err
	}
	// The chain to each entity it depends on, found nearest first.
	reached := map[string][]string{full(start.GetRef()): {full(start.GetRef())}}
	byRef := map[string]*buildv1.Entity{}
	for _, e := range g.entities {
		byRef[full(e.GetRef())] = e
	}
	order := []string{full(start.GetRef())}
	for i := 0; i < len(order); i++ {
		for _, d := range byRef[order[i]].GetDependsOn() {
			next := full(d)
			if _, seen := reached[next]; seen {
				continue
			}
			reached[next] = append(append([]string{}, reached[order[i]]...), next)
			order = append(order, next)
		}
	}
	steps := func(chain []string) []plain.Step {
		var out []plain.Step
		for i := 1; i < len(chain); i++ {
			step := plain.Step{From: chain[i-1], To: chain[i], Repository: byRef[chain[i]].GetRepository()}
			step.Commit = commits[step.Repository]
			step.Because = g.because(chain[i-1], func(r plain.Requirement) bool { return r.Provider == chain[i] })
			out = append(out, step)
		}
		return out
	}
	target, err := g.find(to)
	if err != nil && !errors.Is(err, errNoEntity) {
		return nil, err
	}
	if err == nil {
		answer := &plain.Why{From: full(start.GetRef()), To: full(target.GetRef()), Paths: [][]plain.Step{}}
		if chain, ok := reached[answer.To]; ok && len(chain) > 1 {
			answer.Paths = append(answer.Paths, steps(chain))
		}
		return answer, nil
	}
	answer := &plain.Why{From: full(start.GetRef()), To: to, Paths: [][]plain.Step{}}
	required := false
	for _, p := range g.projects {
		for _, r := range p.GetRequires() {
			required = required || r.GetCoordinate() == to
		}
	}
	if !required {
		return nil, fmt.Errorf("%s names no entity of the workspace, and nothing its projects require", to)
	}
	for _, at := range order {
		because := g.because(at, func(r plain.Requirement) bool { return r.Coordinate == to })
		if len(because) == 0 {
			continue
		}
		path := steps(reached[at])
		path = append(path, plain.Step{From: at, To: to, Because: because})
		answer.Paths = append(answer.Paths, path)
	}
	return answer, nil
}

// because are the requirements of an entity's projects that keep says
// are why it depends on what is asked.
func (g graph) because(owner string, keep func(plain.Requirement) bool) []plain.Requirement {
	out := []plain.Requirement{}
	for _, p := range g.projects {
		if p.GetOwner() == nil || full(p.GetOwner()) != owner {
			continue
		}
		for _, r := range p.GetRequires() {
			if made := g.requirement(r); keep(made) {
				out = append(out, made)
			}
		}
	}
	return out
}
