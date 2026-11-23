package pluginhost_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
	"github.com/frontseat-dev/frontseat/build/engine/pluginhost"
)

// A file larger than a plugin takes is answered too large, and none of
// it is copied into the module: the plugin says so by the SDK's reason,
// naming the file. The control is the same module of a go.mod at the
// bound exactly, which is read.
func TestAFileLargerThanAPluginTakesIsSaidToBeAndNotCopied(t *testing.T) {
	plugin := load(t)
	padded := func(size int) string {
		return goMod + "//" + strings.Repeat("x", size-len(goMod)-3) + "\n"
	}
	got := &pluginv1.AnalyzeResponse{}
	if _, err := plugin.Call(context.Background(), files{"go.mod": padded(pluginhost.ReadAtMost)}, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext()}, got); err != nil {
		t.Fatalf("a go.mod of %d bytes, the most a plugin takes, was not read: %v", pluginhost.ReadAtMost, err)
	}
	_, err := plugin.Call(context.Background(), files{"go.mod": padded(pluginhost.ReadAtMost + 1)}, "Analyze", &pluginv1.AnalyzeRequest{Context: goContext()}, got)
	var refused *pluginhost.Error
	if !errors.As(err, &refused) || refused.Reason != "FILE_TOO_LARGE" || !strings.Contains(err.Error(), "go.mod") {
		t.Fatalf("a go.mod a byte past the most a plugin takes: %v, want FILE_TOO_LARGE of go.mod", err)
	}
}
