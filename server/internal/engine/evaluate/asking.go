package evaluate

import (
	"context"

	"github.com/frontseat-dev/frontseat/server/internal/engine/config"
	"github.com/frontseat-dev/frontseat/server/internal/pluginhost"
	"google.golang.org/protobuf/proto"
)

// asking is the evaluator's query boundary. Answers serves ordinary evaluation;
// a retained session can collect query dependencies through the same operations.
// Repository reads and standalone calls are the same through either.
type asking interface {
	begin()
	said(context.Context, repositoryView) (*config.Repository, map[string]string, error)
	ask(context.Context, *pluginhost.Plugin, repositoryView, string, proto.Message, proto.Message) (bool, error)
	askObserved(context.Context, *pluginhost.Plugin, repositoryView, string, proto.Message, proto.Message) (bool, []pluginhost.Read, error)
	askAlone(context.Context, *pluginhost.Plugin, string, proto.Message, proto.Message) (bool, error)
}
