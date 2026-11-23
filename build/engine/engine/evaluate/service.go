package evaluate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/frontseat-dev/frontseat/build/engine/engine/config"
	"github.com/frontseat-dev/frontseat/build/engine/model"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
	"github.com/frontseat-dev/frontseat/common/runner/testservice"
)

// claimants are the plugins of a repository that service something, by
// the name each claims: what an entity's service names as what it is
// started as. A name two of them claim is refused, as either would be
// a guess.
func claimants(repository string, selected []*pluginhost.Plugin) (map[string]*pluginhost.Plugin, error) {
	claimed := map[string]*pluginhost.Plugin{}
	for _, plugin := range selected {
		for _, as := range plugin.Description.GetServices() {
			if other, twice := claimed[as]; twice {
				return nil, &model.Mistake{Repository: repository, File: config.File, Message: fmt.Sprintf("the plugins %s and %s both start %s: a repository names one of them", other.Description.GetName(), plugin.Description.GetName(), as)}
			}
			claimed[as] = plugin
		}
	}
	return claimed, nil
}

// startedAs is how an entity is started: what the plugin that claims
// the name its service says answered, given what was written beside it.
// The answer keeps each thing written (pluginhost.CheckService) and is
// held to what every service is (testservice.Check), so that what a plugin
// said is trusted no further than what a person wrote was. The plugin
// is asked with no repository at all (askAlone): a read through the
// host fails the call, what it says is of what it was told alone, and
// the same question is answered once and kept.
func startedAs(ctx context.Context, answers asking, claimed map[string]*pluginhost.Plugin, written config.Service, out *Evaluation) (config.Service, error) {
	plugin := claimed[written.As]
	if plugin == nil {
		names := make([]string, 0, len(claimed))
		for as := range claimed {
			names = append(names, as)
		}
		sort.Strings(names)
		if len(names) == 0 {
			mistake := written.Mistake(fmt.Errorf("%s is started as %s, which no plugin of the repository starts: none does", written.Entity, written.As))
			mistake.Help = "name a plugin that starts services in " + config.File
			return config.Service{}, mistake
		}
		return config.Service{}, written.Mistake(fmt.Errorf("%s is started as %s, which no plugin of the repository starts: %s", written.Entity, written.As, strings.Join(names, ", ")))
	}
	// A plugin is told only what could be a service.
	if err := testservice.Shaped(written.Said); err != nil {
		return config.Service{}, written.Mistake(err)
	}
	request := &pluginv1.ServiceRequest{Name: written.As, Written: written.Said}
	response := &pluginv1.ServiceResponse{}
	again, err := answers.askAlone(ctx, plugin, "Service", request, response)
	if err != nil {
		// What a plugin refuses of what was written is its author's
		// mistake, and is said where it was written: the plugin was told
		// no file. Anything else is the plugin's own failure.
		var said *pluginhost.Error
		if errors.As(err, &said) && said.Status.GetCode() == int32(codes.InvalidArgument) {
			return config.Service{}, &model.Mistake{Repository: written.Repository, File: written.File, Line: written.Line, Message: fmt.Sprintf("%s, started as %s: %s", written.Entity, written.As, said.Error()), Cause: said}
		}
		return config.Service{}, fmt.Errorf("plugin %s, asked how %s is started: %w", plugin.Description.GetName(), written.Entity, err)
	}
	out.count(again)
	if err := pluginhost.CheckService(plugin.Description.GetName(), request, response); err != nil {
		return config.Service{}, err
	}
	l := written
	l.Said = response.GetService()
	if err := testservice.Check(l.Said); err != nil {
		// What is wrong is said where the service is written, which is
		// where its author can put it right; and where the plugin said
		// more than was written, that it is the plugin's saying, since
		// what is wrong may be the plugin's own.
		if !proto.Equal(l.Said, written.Said) {
			err = fmt.Errorf("as the plugin %s says %s is started: %w", plugin.Description.GetName(), written.As, err)
		}
		return config.Service{}, l.Mistake(err)
	}
	return l, nil
}
