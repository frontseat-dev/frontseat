package pluginhost

import (
	"context"
	"path"

	pluginv1 "github.com/frontseat-dev/frontseat/build/engine/pluginapi/frontseat/plugin/v1alpha1"
)

// ByDirectory is the files a plugin's interests selected, by the
// directory each is directly in, the repository's root as "": what made
// each directory a root, which the plugin is told of it
// (AnalyzeRequest.matched), in the files' order.
func ByDirectory(files []string) map[string][]string {
	out := make(map[string][]string, len(files))
	for _, f := range files {
		dir := path.Dir(f)
		if dir == "." {
			dir = ""
		}
		out[dir] = append(out[dir], f)
	}
	return out
}

// Matched tells request what a host tells a plugin of the roots it asks
// about: the files of src that interests select directly in each
// (AnalyzeRequest.matched), the repository's root where it names none. It
// lists src itself, and records nothing a plugin read.
func Matched(ctx context.Context, src Source, interests []string, request *pluginv1.AnalyzeRequest) error {
	files, err := src.Glob(ctx, "", interests)
	if err != nil {
		return err
	}
	roots := request.GetRoots()
	if len(roots) == 0 {
		roots = []string{""}
	}
	by := ByDirectory(files)
	request.Matched = nil
	for _, root := range roots {
		request.Matched = append(request.Matched, by[root]...)
	}
	return nil
}
