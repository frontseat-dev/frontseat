package evaluate

import (
	"encoding/json"
	"fmt"
	runnerv1 "github.com/frontseat-dev/frontseat/server/internal/runnerapi/frontseat/runner/v1alpha1"
	"google.golang.org/protobuf/types/known/durationpb"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	typev1 "github.com/frontseat-dev/frontseat/sdk/go/api/frontseat/type/v1alpha1"
	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/server/internal/engine/catalog"
	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/engine/graph"
	"github.com/frontseat-dev/frontseat/server/internal/engine/plan"
	"github.com/frontseat-dev/frontseat/server/internal/model"
	"github.com/frontseat-dev/frontseat/server/internal/testservice"
)

const (
	// defaultReady is how long a service is given to be ready where
	// neither its Resource nor its plugin says.
	defaultReady = 2 * time.Minute
)

// address is what a Resource names an address by: {host}, a port of its
// own ({port.http}), or one of a Resource it depends on
// ({db.port.sql}).
var address = regexp.MustCompile(`\{(?:host|(?:([a-z0-9][a-z0-9-]*)\.)?port\.(` + testservice.PortName + `))\}`)

// services gives each test action of a component its prepared Resource
// dependencies and API providers, and their recursive dependencies, as a
// runner sandbox. A test is given them
// whichever of the component's projects it is of; nothing else is. A
// Resource that is not started is none of a test's: it is what
// Backstage says of the component, and frontseat leaves it as it is.
func services(out *Evaluation, services map[entity.Ref]config.Service) error {
	if len(services) == 0 {
		return nil
	}
	// Inferred build dependencies are not runtime service dependencies.
	declared, apis, err := serviceRelationships(out.Catalog.Nodes(nil), out.descriptors, services)
	if err != nil {
		return err
	}
	programs, err := servicePrograms(out, services)
	if err != nil {
		return err
	}
	made := map[entity.Ref]*plan.Beside{}
	for i := range out.Actions {
		action := &out.Actions[i]
		if !action.IsTest() || action.IsShared() || action.InStep != nil {
			continue
		}
		owner, ok := graph.Owner(out.Entities, action.Label.Path)
		if !ok || owner.Ref.Kind != entity.KindComponent {
			continue
		}
		beside, done := made[owner.Ref]
		if !done {
			var err error
			if beside, err = besideOf(owner.Ref, declared, services, apis); err != nil {
				return err
			}
			made[owner.Ref] = beside
		}
		if beside != nil {
			copy := *beside
			copy.TestNeeds = slices.Clone(action.Needs)
			copy.Programs = map[string]plan.Action{}
			var described runnerv1.RunnerSandbox
			if err := proto.Unmarshal(beside.Sandbox, &described); err != nil {
				return err
			}
			for _, service := range described.GetServices() {
				ref, _ := entity.Parse(service.GetEntity())
				if program, found := programs[ref]; found {
					copy.Programs[service.GetName()] = program
					for _, need := range program.Needs {
						if !slices.Contains(action.Needs, need) {
							action.Needs = append(action.Needs, need)
						}
					}
				}
			}
			action.Beside = &copy
		}
	}
	return nil
}

// serviceDependencies projects the existing catalog relations onto prepared
// services. API entities describe contracts; the provider Component
// supplies the runtime and owns its Resource dependencies. An API without a
// prepared local provider supplies no runtime binding, just as a Resource
// without a service does not start anything. It never grants external access.
func serviceDependencies(nodes []catalog.Node, descriptors []catalog.Descriptor, services map[entity.Ref]config.Service) (map[entity.Ref][]entity.Ref, error) {
	dependencies, _, err := serviceRelationships(nodes, descriptors, services)
	return dependencies, err
}

// apiEdges retain the API identity on every direct provider relationship.
type apiEdges map[entity.Ref]map[entity.Ref][]string

func serviceRelationships(nodes []catalog.Node, descriptors []catalog.Descriptor, services map[entity.Ref]config.Service) (map[entity.Ref][]entity.Ref, apiEdges, error) {
	apis := apiEdges{}
	// Resource runtime dependencies remain declarations by their consumer.
	// The graph's inverse relations describe the same catalog fact, but
	// do not let a Resource opt itself into another entity's execution.
	declared := map[entity.Ref][]entity.Ref{}
	for _, descriptor := range descriptors {
		if descriptor.Ref.Kind != "component" && descriptor.Ref.Kind != "resource" {
			continue
		}
		for _, relation := range descriptor.Relations {
			if relation.Type == "dependsOn" && relation.Target.Kind == "resource" {
				declared[descriptor.Ref] = append(declared[descriptor.Ref], relation.Target)
			}
		}
	}
	providers := map[entity.Ref][]entity.Ref{}
	for _, node := range nodes {
		if node.Ref.Kind != "api" {
			continue
		}
		for _, relation := range node.Relations {
			if relation.Type != "apiProvidedBy" || relation.Target.Kind != "component" || !relation.TargetPresent {
				continue
			}
			if _, prepared := services[relation.Target]; prepared {
				providers[node.Ref] = append(providers[node.Ref], relation.Target)
			}
		}
	}
	for _, node := range nodes {
		if node.Ref.Kind != "component" && node.Ref.Kind != "resource" {
			continue
		}
		for _, relation := range node.Relations {
			if node.Ref.Kind == "component" && relation.Type == "consumesApi" && relation.Target.Kind == "api" {
				matches := providers[relation.Target]
				if len(matches) > 1 {
					return nil, nil, &model.Mistake{File: path.Join(node.Repository, node.Manifest), Message: fmt.Sprintf("%s consumes %s, which has more than one prepared service provider: %s", node.Ref, relation.Target, matches)}
				}
				declared[node.Ref] = append(declared[node.Ref], matches...)
				for _, provider := range matches {
					if apis[node.Ref] == nil {
						apis[node.Ref] = map[entity.Ref][]string{}
					}
					apis[node.Ref][provider] = append(apis[node.Ref][provider], relation.Target.String())
				}
			}
		}
		dependencies := declared[node.Ref]
		slices.SortFunc(dependencies, func(a, b entity.Ref) int { return strings.Compare(a.String(), b.String()) })
		declared[node.Ref] = slices.Compact(dependencies)
	}
	return declared, apis, nil
}

// servicePrograms selects declared programs before preparation. Runtime ownership
// stays with the selecting entity, including when a Resource selects a program.
func servicePrograms(out *Evaluation, services map[entity.Ref]config.Service) (map[entity.Ref]plan.Action, error) {
	programs := map[entity.Ref]plan.Action{}
	byLabel := map[string]plan.Action{}
	for _, action := range out.Actions {
		if _, exists := byLabel[action.Label.String()]; exists {
			return nil, &model.Mistake{Message: fmt.Sprintf("action %s is declared more than once", action.Label)}
		}
		byLabel[action.Label.String()] = action
	}
	provided := map[entity.Ref]map[string]bool{}
	for _, node := range out.Catalog.Nodes(nil) {
		for _, relation := range node.Relations {
			if relation.Type == "providesApi" {
				if provided[node.Ref] == nil {
					provided[node.Ref] = map[string]bool{}
				}
				provided[node.Ref][relation.Target.String()] = true
			}
		}
	}
	for _, ref := range slices.SortedFunc(maps.Keys(services), func(a, b entity.Ref) int { return strings.Compare(a.String(), b.String()) }) {
		l := services[ref]
		for api := range l.Said.GetApis() {
			if ref.Kind != "component" || !provided[ref][api] {
				return nil, l.Mistake(fmt.Errorf("%s maps API %s, which it does not provide", ref, api))
			}
		}
		if l.Program == "" {
			continue
		}
		selected, err := model.ParseReference(path.Join(l.Repository, l.Directory), l.Program)
		if err != nil {
			return nil, l.Mistake(err)
		}
		program, found := byLabel["//"+selected.Path+":"+selected.Rest]
		if !found || !program.IsProgram() {
			return nil, l.Mistake(fmt.Errorf("%s names no program action", l.Program))
		}
		owner, owned := graph.Owner(out.Entities, program.Label.Path)
		if !owned || owner.Ref.Kind != "component" || (ref.Kind == "component" && owner.Ref != ref) {
			return nil, l.Mistake(fmt.Errorf("%s selects a program it does not own", ref))
		}
		programs[ref] = program
	}
	return programs, nil
}

// besideOf derives one independent network for every service and grants only
// the immediate consumer's endpoints. Equal listening ports do not collide.
func besideOf(component entity.Ref, declared map[entity.Ref][]entity.Ref, services map[entity.Ref]config.Service, apis apiEdges) (*plan.Beside, error) {
	order, err := startOrder(component, declared, services)
	if err != nil || len(order) == 0 {
		return nil, err
	}
	if len(order) > 32 {
		return nil, fmt.Errorf("%s needs more than 32 runtime services", component)
	}
	names := map[entity.Ref]string{}
	for i, ref := range order {
		names[ref] = "service-" + strconv.Itoa(i)
	}
	sandbox := &runnerv1.RunnerSandbox{}
	for _, ref := range order {
		l := services[ref]
		routes, bindings, _, err := directRoutes(ref, declared, services, apis, names)
		if err != nil {
			return nil, l.Mistake(err)
		}
		said := proto.Clone(l.Said).(*typev1.Service)
		said.Exports, said.Endpoints = nil, nil
		fill := routedFiller(ref, services, routes, names, false)
		for i, argument := range said.Arguments {
			if said.Arguments[i], err = fill(argument); err != nil {
				return nil, l.Mistake(err)
			}
		}
		for name, value := range said.Environment {
			if said.Environment[name], err = fill(value); err != nil {
				return nil, l.Mistake(err)
			}
		}
		// Image Resources keep explicit address substitution. A program
		// receives its dependencies' bindings, including a Resource program.
		if ref.Kind == "component" || l.Program != "" {
			for _, name := range slices.Sorted(maps.Keys(bindings)) {
				if _, set := said.Environment[name]; set {
					return nil, l.Mistake(fmt.Errorf("%s sets %s, which a dependency exports to it", ref, name))
				}
				if said.Environment == nil {
					said.Environment = map[string]string{}
				}
				said.Environment[name] = bindings[name]
			}
		}
		for _, ready := range said.Ready {
			arguments := ready.GetCommand().GetArguments()
			for i, argument := range arguments {
				if arguments[i], err = fill(argument); err != nil {
					return nil, l.Mistake(err)
				}
			}
		}
		if said.Timeout == nil {
			said.Timeout = durationpb.New(defaultReady)
		}
		sandbox.Services = append(sandbox.Services, &runnerv1.RunnerService{Name: names[ref], Entity: ref.String(), Service: said, Routes: routes})
	}
	routes, environment, endpoints, err := directRoutes(component, declared, services, apis, names)
	if err != nil {
		return nil, err
	}
	// What a test reaches each service at is told in one variable, of
	// every service it is given an endpoint of, by its entity; no export
	// is named so, whether or not there are endpoints to tell.
	if _, exported := environment[EndpointsVariable]; exported {
		return nil, fmt.Errorf("%s is given %s by an export, and it is where a test is told its services' endpoints", component, EndpointsVariable)
	}
	if len(endpoints) > 0 {
		told, err := json.Marshal(endpoints)
		if err != nil {
			return nil, err
		}
		environment[EndpointsVariable] = string(told)
	}
	sandbox.Routes = routes
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(sandbox)
	if err != nil {
		return nil, err
	}
	return &plan.Beside{Sandbox: encoded, Environment: environment}, nil
}

// EndpointsVariable is what a test is told its services' endpoints in:
// a JSON object of each service by its entity reference, each holding its
// endpoints, by name, as the test reaches them.
const EndpointsVariable = "FRONTSEAT_ENDPOINTS"

// directRoutes maps a dependency's port to a free consumer loopback port, then
// renders only that edge's exports and endpoints. API identity is retained
// in every grant. A Component's endpoint is rendered only where its port
// is granted by an API the consumer consumes.
func directRoutes(consumer entity.Ref, declared map[entity.Ref][]entity.Ref, services map[entity.Ref]config.Service, apis apiEdges, names map[entity.Ref]string) ([]*runnerv1.RunnerRoute, map[string]string, map[string]map[string]string, error) {
	held := map[int32]bool{}
	for _, port := range services[consumer].Said.GetPorts() {
		held[port] = true
	}
	type endpoint struct {
		ref  entity.Ref
		port string
	}
	assigned := map[endpoint]int32{}
	var routes []*runnerv1.RunnerRoute
	environment := map[string]string{}
	endpoints := map[string]map[string]string{}
	reach := func(ref entity.Ref, name, value string) {
		if endpoints[ref.String()] == nil {
			endpoints[ref.String()] = map[string]string{}
		}
		endpoints[ref.String()][name] = value
	}
	from := map[string]entity.Ref{}
	grant := func(ref entity.Ref, port, api string) (int32, error) {
		key := endpoint{ref, port}
		if local, found := assigned[key]; found {
			// One route per API is retained, even if APIs share an endpoint.
			if api != "" {
				routes = append(routes, &runnerv1.RunnerRoute{Service: names[ref], Port: port, LocalPort: local, Api: api})
			}
			return local, nil
		}
		local := services[ref].Said.GetPorts()[port]
		if local < 1 || local > 65535 {
			return 0, fmt.Errorf("%s names a port %s does not serve", consumer, ref)
		}
		if held[local] {
			local = 20000
			for local <= 65535 && held[local] {
				local++
			}
			if local > 65535 {
				return 0, fmt.Errorf("%s has no free service port", consumer)
			}
		}
		held[local], assigned[key] = true, local
		routes = append(routes, &runnerv1.RunnerRoute{Service: names[ref], Port: port, LocalPort: local, Api: api})
		return local, nil
	}
	bind := func(ref entity.Ref, exports map[string]string, fill func(string) (string, error)) error {
		for _, name := range slices.Sorted(maps.Keys(exports)) {
			if other, twice := from[name]; twice {
				return fmt.Errorf("%s and %s both export %s to %s", other, ref, name, consumer)
			}
			value, err := fill(exports[name])
			if err != nil {
				return err
			}
			from[name], environment[name] = ref, value
		}
		return nil
	}
	for _, ref := range started(declared[consumer], services) {
		l := services[ref]
		if ref.Kind == "component" {
			for _, api := range slices.Sorted(slices.Values(apis[consumer][ref])) {
				binding := l.Said.GetApis()[api]
				if binding == nil {
					return nil, nil, nil, l.Mistake(fmt.Errorf("%s provides consumed API %s without an endpoint mapping", ref, api))
				}
				if err := testservice.Shaped(l.Said); err != nil {
					return nil, nil, nil, l.Mistake(err)
				}
				local, err := grant(ref, binding.GetPort(), api)
				if err != nil {
					return nil, nil, nil, err
				}
				fill := func(value string) (string, error) {
					return strings.ReplaceAll(strings.ReplaceAll(value, "{host}", "127.0.0.1"), "{port."+binding.GetPort()+"}", strconv.Itoa(int(local))), nil
				}
				if err := bind(ref, binding.GetExports(), fill); err != nil {
					return nil, nil, nil, l.Mistake(err)
				}
				for name, value := range l.Said.GetEndpoints() {
					if port, err := testservice.EndpointPort(name, value); err != nil {
						return nil, nil, nil, l.Mistake(err)
					} else if port == binding.GetPort() {
						filled, _ := fill(value)
						reach(ref, name, filled)
					}
				}
			}
		} else {
			for _, port := range slices.Sorted(maps.Keys(l.Said.GetPorts())) {
				if _, err := grant(ref, port, ""); err != nil {
					return nil, nil, nil, err
				}
			}
			fill := routedFiller(ref, services, routes, names, true)
			if err := bind(ref, l.Said.GetExports(), fill); err != nil {
				return nil, nil, nil, l.Mistake(err)
			}
			for name, value := range l.Said.GetEndpoints() {
				filled, err := fill(value)
				if err != nil {
					return nil, nil, nil, l.Mistake(err)
				}
				reach(ref, name, filled)
			}
		}
	}
	return routes, environment, endpoints, nil
}

// routedFiller resolves addresses in a service's own arguments or in its
// exports to a consumer. Only routes granted to that consumer are visible.
func routedFiller(of entity.Ref, services map[entity.Ref]config.Service, routes []*runnerv1.RunnerRoute, names map[entity.Ref]string, exporting bool) func(string) (string, error) {
	return func(text string) (string, error) {
		var failure error
		filled := address.ReplaceAllStringFunc(text, func(token string) string {
			if token == "{host}" {
				return "127.0.0.1"
			}
			parts := address.FindStringSubmatch(token)
			name, port := parts[1], parts[2]
			if !exporting && (name == "" || name == services[of].Name) {
				if value, found := services[of].Said.GetPorts()[port]; found {
					return strconv.Itoa(int(value))
				}
			} else {
				var matches []int32
				for _, route := range routes {
					for ref, id := range names {
						if route.Service == id && route.Port == port && ((exporting && ref == of && (name == "" || name == services[of].Name)) || (!exporting && services[ref].Name == name)) {
							matches = append(matches, route.LocalPort)
						}
					}
				}
				slices.Sort(matches)
				matches = slices.Compact(matches)
				if len(matches) == 1 {
					return strconv.Itoa(int(matches[0]))
				}
			}
			failure = fmt.Errorf("%s names %s, which is not an unambiguous granted port", of, token)
			return token
		})
		return filled, failure
	}
}

// startOrder is the Resources a component's tests are given, each after
// what it depends on.
func startOrder(component entity.Ref, declared map[entity.Ref][]entity.Ref, services map[entity.Ref]config.Service) ([]entity.Ref, error) {
	var order []entity.Ref
	state := map[entity.Ref]int{} // 1 being placed, 2 placed
	var visit func(ref entity.Ref, path []string) error
	visit = func(ref entity.Ref, path []string) error {
		switch state[ref] {
		case 2:
			return nil
		case 1:
			return &model.Mistake{File: services[ref].File, Message: fmt.Sprintf("runtime service startup cycle: %s depends on itself: %s", ref, strings.Join(append(path, ref.String()), " -> "))}
		}
		state[ref] = 1
		for _, dep := range started(declared[ref], services) {
			if err := visit(dep, append(path, ref.String())); err != nil {
				return err
			}
		}
		state[ref] = 2
		order = append(order, ref)
		return nil
	}
	for _, ref := range started(declared[component], services) {
		if err := visit(ref, []string{component.String()}); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// started are the references that name Resources which are started,
// each once, in the order written.
func started(refs []entity.Ref, services map[entity.Ref]config.Service) []entity.Ref {
	var out []entity.Ref
	seen := map[entity.Ref]bool{}
	for _, ref := range refs {
		if _, ok := services[ref]; ok && !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out
}
