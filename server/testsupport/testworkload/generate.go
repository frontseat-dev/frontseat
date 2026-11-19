package testworkload

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Shape is the size of a workspace.
type Shape struct {
	// Modules is how many modules it has.
	Modules int
	// Group is how many modules are in a group. The first of a group is
	// needed by the others.
	Group int
	// Salt is written into every module, so that a workspace is one no
	// cache has seen: with another salt, nothing a tool or a grid kept
	// of an earlier workspace answers for this one.
	Salt string
}

// Actions are the names of the actions every module has.
var Actions = []string{"compile", "test", "lint"}

// Sources are the files every module has.
var Sources = []string{"go.mod", "main.go", "word.go", "word_test.go"}

// Module returns the name of a module.
func Module(i int) string { return fmt.Sprintf("m%04d", i) }

// Needs returns the module a module needs: the first of its group; or
// -1 for a module that is the first of its group.
func (s Shape) Needs(i int) int {
	if first := i - i%s.Group; first != i {
		return first
	}
	return -1
}

// Word returns the file of a module that the measurements change, as it
// is written with a word.
func Word(word string) string {
	return "package main\n\nfunc word() string { return \"" + word + "\" }\n"
}

// source writes a module's files.
func (s Shape) source(dir string, i int) error {
	name := Module(i)
	for file, content := range map[string]string{
		"go.mod":       "module example.com/" + name + "\n\ngo 1.27\n",
		"main.go":      "// " + s.Salt + "\npackage main\n\nfunc main() { println(word()) }\n",
		"word.go":      Word(name),
		"word_test.go": "package main\n\nimport \"testing\"\n\nfunc TestWord(t *testing.T) {\n\tif word() == \"\" {\n\t\tt.Fatal()\n\t}\n}\n",
	} {
		if err := write(filepath.Join(dir, name, file), content); err != nil {
			return err
		}
	}
	return nil
}

func write(file, content string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o777); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(content), 0o666)
}

// Frontseat writes a repository frontseat builds: each module is an
// entity whose catalog file gives it its three actions. The repository
// is named as the workspace will name it, since an action names what
// it reads of another module's action by its whole label.
func Frontseat(dir string, shape Shape, repository string) error {
	if err := write(filepath.Join(dir, "frontseat.yaml"), "plugins: {}\n"); err != nil {
		return err
	}
	return shape.modules(dir, "catalog-info.yaml", func(i, first int) string {
		read, reads := strings.Join(Sources, " "), ""
		if first >= 0 {
			read += " ../" + Module(first) + "/compile.out"
			reads = ", \"//" + path.Join(repository, Module(first)) + ":compile.out\""
		}
		var catalog strings.Builder
		fmt.Fprintf(&catalog, "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: %s\nspec:\n  type: library\n  lifecycle: production\n  owner: compare\n  frontseat:\n    actions:\n", Module(i))
		for _, action := range Actions {
			fmt.Fprintf(&catalog, "      %s:\n        command: [/bin/sh, -c, \"cat %s > %s.out\"]\n        inputs: [go.mod, \"*.go\"%s]\n        outputs: [%s.out]\n", action, read, action, reads, action)
		}
		return catalog.String()
	})
}

// Grid is a grid a Bazel workspace builds on, as frontseat's actions run
// there: its address, its workers' platform, and what each action's
// command begins with there (Confined): the confiner, its spec and the
// way up to the action's root, as frontseat's daemon writes them
// (confine.Arguments). The grid's runner runs no other command; each
// action is confined as frontseat's are.
type Grid struct {
	Address  string
	Platform map[string]string
	Confined []string
}

// rules are the rules a Bazel workspace on a grid has, each running its
// command after what the grid says every action's begins with: cat, an
// action that concatenates its sources, and run, which runs a command
// and, where it succeeds, writes its output.
const rules = `CONFINED = %s

def _cat_impl(ctx):
    out = ctx.actions.declare_file(ctx.attr.name + ".out")
    ctx.actions.run(
        executable = CONFINED[0],
        arguments = CONFINED[1:] + ["/bin/sh", "-c", 'out="$1"; shift; cat "$@" > "$out"', "sh", out.path] + [f.path for f in ctx.files.srcs],
        inputs = ctx.files.srcs,
        outputs = [out],
        mnemonic = "Cat",
    )
    return [DefaultInfo(files = depset([out]))]

cat = rule(implementation = _cat_impl, attrs = {"srcs": attr.label_list(allow_files = True)})

def _run_impl(ctx):
    out = ctx.actions.declare_file(ctx.attr.name + ".out")
    ctx.actions.run(
        executable = CONFINED[0],
        arguments = CONFINED[1:] + ["/bin/sh", "-c", ctx.attr.cmd + ' && echo x > "$0"', out.path],
        outputs = [out],
        mnemonic = "Run",
        execution_requirements = {"no-cache": "1", "no-remote-cache": "1"},
    )
    return [DefaultInfo(files = depset([out]))]

run = rule(implementation = _run_impl, attrs = {"cmd": attr.string()})
`

// Bazel writes a workspace Bazel builds: each module is a package with a
// genrule for each action. The workspace says which Bazel builds it,
// where a version is given: the bazel on a machine is often a launcher
// that otherwise asks the network which Bazel is the latest, each time
// it is run, and that second would be counted as Bazel's.
//
// Given a grid, it builds there and nowhere else, each genrule in the
// runner sandboxes frontseat's actions run in: what is compared is then
// the same work, run the same way, by each tool.
func Bazel(dir string, shape Shape, version string, grid *Grid) error {
	if err := write(filepath.Join(dir, "MODULE.bazel"), "module(name = \"compare\")\n"); err != nil {
		return err
	}
	if version != "" {
		if err := write(filepath.Join(dir, ".bazelversion"), version+"\n"); err != nil {
			return err
		}
	}
	if grid != nil {
		// Nothing an action leaves is brought to this machine, as nothing
		// is to frontseat's; and a genrule runs in bash, as Bazel writes
		// it for: under dash its setup is not found, and every action says
		// so, which Bazel then brings back and prints.
		rc := "build --remote_executor=grpc://" + grid.Address + "\nbuild --remote_instance_name=\nbuild --spawn_strategy=remote\nbuild --genrule_strategy=remote\nbuild --shell_executable=/bin/bash\nbuild --noremote_upload_local_results\nbuild --remote_download_minimal\n"
		names := make([]string, 0, len(grid.Platform))
		for name := range grid.Platform {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			rc += "build --remote_default_exec_properties=" + name + "=" + grid.Platform[name] + "\n"
		}
		if err := write(filepath.Join(dir, ".bazelrc"), rc); err != nil {
			return err
		}
		confined := make([]string, len(grid.Confined))
		for n, each := range grid.Confined {
			confined[n] = strconv.Quote(each)
		}
		if err := write(filepath.Join(dir, "BUILD.bazel"), ""); err != nil {
			return err
		}
		if err := write(filepath.Join(dir, "rules.bzl"), fmt.Sprintf(rules, "["+strings.Join(confined, ", ")+"]")); err != nil {
			return err
		}
		return shape.modules(dir, "BUILD.bazel", func(_, first int) string {
			needs := ""
			if first >= 0 {
				needs = " + [\"//" + Module(first) + ":compile\"]"
			}
			return fmt.Sprintf(`load("//:rules.bzl", "cat")

package(default_visibility = ["//visibility:public"])

[cat(
    name = name,
    srcs = [%s]%s,
) for name in [%s]]
`, quoted(Sources), needs, quoted(Actions))
		})
	}
	return shape.modules(dir, "BUILD.bazel", func(_, first int) string {
		needs := ""
		if first >= 0 {
			needs = " + [\"//" + Module(first) + ":compile\"]"
		}
		return fmt.Sprintf(`package(default_visibility = ["//visibility:public"])

[genrule(
    name = name,
    srcs = [%s]%s,
    outs = [name + ".out"],
    cmd = "cat $(SRCS) > $@",
) for name in [%s]]
`, quoted(Sources), needs, quoted(Actions))
	})
}

// buck2Rules is the one rule the Buck2 workspace has: an action that
// concatenates its sources and what the targets it needs left.
const buck2Rules = `def _cat_impl(ctx: AnalysisContext) -> list[Provider]:
    out = ctx.actions.declare_output(ctx.attrs.name + ".out", has_content_based_path = False)
    srcs = list(ctx.attrs.srcs)
    for dep in ctx.attrs.deps:
        srcs.extend(dep[DefaultInfo].default_outputs)
    ctx.actions.run(cmd_args(["/bin/sh", "-c", 'out="$1"; shift; cat "$@" > "$out"', "sh", out.as_output()] + srcs), category = "cat")
    return [DefaultInfo(default_output = out)]

cat = rule(impl = _cat_impl, attrs = {
    "deps": attrs.list(attrs.dep(), default = []),
    "srcs": attrs.list(attrs.source()),
})
`

// Buck2 writes a workspace Buck2 builds: each module is a package with a
// target for each action, of a rule of the workspace's own.
func Buck2(dir string, shape Shape) error {
	for file, content := range map[string]string{
		".buckconfig": "[cells]\nroot = .\n\n[buildfile]\nname = BUCK\n",
		".buckroot":   "",
		"BUCK":        "",
		"rules.bzl":   buck2Rules,
	} {
		if err := write(filepath.Join(dir, file), content); err != nil {
			return err
		}
	}
	return shape.modules(dir, "BUCK", func(_, first int) string {
		needs := ""
		if first >= 0 {
			needs = "\"//" + Module(first) + ":compile\""
		}
		return fmt.Sprintf(`load("//:rules.bzl", "cat")

[cat(
    name = name,
    srcs = [%s],
    deps = [%s],
    visibility = ["PUBLIC"],
) for name in [%s]]
`, quoted(Sources), needs, quoted(Actions))
	})
}

// modules writes each module's source, and the file the tool reads it
// by: what file says, of the module and the first module it needs, or
// of no module where first is -1.
func (s Shape) modules(dir, file string, says func(i, first int) string) error {
	for i := range s.Modules {
		if err := s.source(dir, i); err != nil {
			return err
		}
		if err := write(filepath.Join(dir, Module(i), file), says(i, s.Needs(i))); err != nil {
			return err
		}
	}
	return nil
}

// quoted writes names as a list is written in Starlark.
func quoted(names []string) string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, "\""+name+"\"")
	}
	return strings.Join(out, ", ")
}
