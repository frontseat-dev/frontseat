package confine

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Confiner is where this form of the confiner, frontseat-action, is on a
// worker: in the directory the runner is installed into, which the runner
// and every runner sandbox of a test with services hold at /bb, under a
// name of its form, so that it is in every action's key.
var Confiner = Confiners + Form

// Confiners is what every form's path begins with.
const Confiners = "/bb/frontseat-action-"

// MiseInstalls is the prepared tool directory, shared with action staging.
// Bare service executables must resolve beneath it, never in the image PATH.
const MiseInstalls = ".frontseat/mise/installs"

// Form is what this program is: the digest of confine.go and routes.go,
// which hold all it does, and of the Go release that built it. The
// package's doc says why, and what is not in it; it says so in doc.go,
// which is not in the digest, so that prose about where things are
// changes no action's key.
var Form = func() string {
	digest := sha256.New()
	digest.Write(source)
	digest.Write(routeSource)
	digest.Write([]byte("\x00" + runtime.Version()))
	return hex.EncodeToString(digest.Sum(nil))
}()

//go:embed confine.go
var source []byte

//go:embed routes.go
var routeSource []byte

// Spec is what the daemon tells frontseat-action of an action. Paths are
// relative to the action's root, and a value is cut where it names the
// root, which is known only on the worker.
type Spec struct {
	// Form is the form of frontseat-action the spec is for (Form).
	Form string `json:"form"`
	// Home and Temporary are made beneath the root: Home is the command's
	// HOME. The command's TMPDIR is a directory of its own on the
	// runner's filesystem, beside the root, since the root is the worker's
	// mount, which keeps no file's mode or owner.
	Home      string `json:"home"`
	Temporary string `json:"temporary"`
	// Network is whether the action reaches the network: a fetch, or the
	// install of a tool. The program does nothing with it: the daemon
	// sends such an action to the runners whose sandboxes reach the
	// network (stage, NetworkProperty), and every other to those whose
	// sandboxes reach nothing. It is said here so that it is in the key.
	Network bool `json:"network,omitempty"`
	// Program restricts a service executable to prepared inputs; a bare
	// executable must come from its declared mise installations.
	Program bool `json:"program,omitempty"`
	// SayRoot is whether the root is printed, on a line of its own,
	// before the command runs: what asks mise for an environment reads
	// where the root was, to cut it out of mise's answer.
	SayRoot bool `json:"say_root,omitempty"`
	// Environment is set, in order, after HOME and TMPDIR: a later one of
	// a name replaces an earlier.
	Environment []Variable `json:"environment,omitempty"`
}

// Variable is one variable of an action's environment: its value cut
// where it names the root, the parts joined by the root on the worker.
// One with no parts is unset.
type Variable struct {
	Name  string   `json:"name"`
	Parts []string `json:"parts,omitempty"`
}

// Arguments is an action's command as the worker runs it: frontseat-action
// told the spec and the way up from the directory the command runs in to
// the action's root ("." where it runs in the root), then the command.
func Arguments(spec, up string, command []string) []string {
	out := make([]string, 0, 3+len(command))
	return append(append(out, Confiner, spec, up), command...)
}

// Encode is a spec as Arguments takes it: the same spec is always the
// same text, so an action's key does not change between runs.
func Encode(spec Spec) (string, error) {
	spec.Form = Form
	encoded, err := json.Marshal(spec)
	return string(encoded), err
}

// ForProgram marks a prepared command as a service program. Its inputs and
// tool environment are otherwise the ordinary action preparation contract.
func ForProgram(arguments []string) ([]string, error) {
	if len(arguments) < 4 || arguments[0] != Confiner {
		return nil, errors.New("a program has no confiner")
	}
	spec, err := read(arguments[1])
	if err != nil {
		return nil, err
	}
	spec.Program, spec.Network = true, true
	encoded, err := Encode(spec)
	if err != nil {
		return nil, err
	}
	out := append([]string(nil), arguments...)
	out[1] = encoded
	return out, nil
}

// ProgramLayout validates a prepared program and returns its private writable
// directories. The worker preserves any prepared files beneath Home.
func ProgramLayout(arguments []string) (Spec, error) {
	if len(arguments) < 4 || arguments[0] != Confiner {
		return Spec{}, errors.New("a program has no confiner")
	}
	spec, err := read(arguments[1])
	if err != nil {
		return Spec{}, err
	}
	if !spec.Program || !spec.Network {
		return Spec{}, errors.New("a service program is not confined as a program with private networking")
	}
	return spec, nil
}

// Run is frontseat-action: it runs the command it is told, or says why it
// could not and exits 127, as a shell does for a command it cannot run.
// Told "form" alone, it prints its form; told "install" and a directory,
// it installs itself there (Install).
func Run() {
	if len(os.Args) == 2 && os.Args[1] == "form" {
		fmt.Println(Form)
		os.Exit(0)
	}
	if len(os.Args) == 3 && os.Args[1] == "install" {
		at, err := Install(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "frontseat-action:", err)
			os.Exit(1)
		}
		fmt.Println(at)
		os.Exit(0)
	}
	if err := Main(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "frontseat-action:", err)
		os.Exit(127)
	}
	os.Exit(0)
}

// Install puts this program in a directory under the name of its form,
// where a worker finds it (Confiner), and returns where. A confiner of
// this form already there, whatever build of it, that answers in time is
// left as it is: an action that is starting holds it by what it is. One too
// slow to answer is replaced by this one, of the same form. It is known by
// what it says it is when asked (form), as actions and the grid's cache
// know it, and not by its bytes, which differ between two builds of one
// form (the go command stamps each with its commit). It is written beside
// and linked into place, which fails rather than replace, so that two
// installs at once each leave one whole file and a runner never runs half
// of one. What else is there under its name, a link, a file that cannot
// be run or cannot say, one that says another form, is no confiner of
// this form, and is replaced whole.
func Install(dir string) (string, error) {
	at := filepath.Join(dir, filepath.Base(Confiner))
	if installed(at) {
		return at, nil
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(self)
	if err != nil {
		return "", err
	}
	beside, err := os.CreateTemp(dir, ".frontseat-action-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(beside.Name())
	if _, err := beside.Write(content); err != nil {
		beside.Close()
		return "", err
	}
	if err := beside.Chmod(0o755); err != nil {
		beside.Close()
		return "", err
	}
	if err := beside.Sync(); err != nil {
		beside.Close()
		return "", err
	}
	if err := beside.Close(); err != nil {
		return "", err
	}
	err = os.Link(beside.Name(), at)
	if errors.Is(err, fs.ErrExist) {
		if installed(at) {
			// Another install put it there first.
			return at, nil
		}
		err = os.Rename(beside.Name(), at)
	}
	if err != nil {
		return "", err
	}
	return at, nil
}

// installed says whether what is at a path is a confiner of this form: a
// file of its own, not a link, that anyone may read and run (an action
// runs it as a user of no account, not as the one installing), and that
// says it is of this form when asked, with nothing in its environment,
// within asking. What it starts is in a process group of its own, which
// is ended whenever it is done with, answered or not: what it left behind
// neither keeps the install waiting nor goes on running. What leaves the
// group (a new session) is not ended, and keeps nothing waiting either.
func installed(at string) bool {
	info, err := os.Lstat(at)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o005 != 0o005 {
		return false
	}
	reading, writing, err := os.Pipe()
	if err != nil {
		return false
	}
	defer reading.Close()
	asked := exec.Command(at, "form")
	asked.Env = []string{}
	asked.Stdout = writing
	asked.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = asked.Start()
	writing.Close()
	if err != nil {
		return false
	}
	// Its answer, until all that holds its output lets go, or until it
	// is given up on.
	answered := make(chan []byte, 1)
	go func() {
		said, _ := io.ReadAll(io.LimitReader(reading, 4096))
		answered <- said
	}()
	var said []byte
	select {
	case said = <-answered:
	case <-time.After(asking):
	}
	// The group is ended before its leader is reaped: until then the
	// leader's number, which is the group's, is no other process's.
	_ = SignalGroup(asked.Process.Pid, syscall.SIGKILL)
	reading.Close()
	err = asked.Wait()
	return err == nil && strings.TrimSpace(string(said)) == Form
}

// asking is how long what is under a confiner's name is given to say its
// form.
var asking = 10 * time.Second

// Main runs frontseat-action with its arguments, the program's name left
// out. It returns when a finite control command finishes or execution fails.
func Main(args []string) error {
	if len(args) > 0 && args[0] == "routes" {
		return runRoutes(args[1:])
	}
	if len(args) > 0 && args[0] == "at" {
		if len(args) < 5 || !filepath.IsAbs(args[1]) || filepath.Clean(args[1]) != args[1] {
			return errors.New("a program starts in a plain absolute directory")
		}
		if err := os.Chdir(args[1]); err != nil {
			return err
		}
		return start(args[2:])
	}
	return start(args)
}

// start finds the action's root, makes what the action keeps in it, sets
// the action's environment, confines it to its root (confineTo) and runs
// the command in its place. Around it is the runner sandbox it runs in,
// which holds its slot of the worker and nothing of another slot's.
func start(args []string) error {
	a, err := prepare(args)
	if err != nil {
		return err
	}
	// Its temporary directory is its own, on the runner's filesystem: the
	// root is the worker's mount, which keeps no file's mode or owner, and
	// git, among others, refuses what it made there. It is held while the
	// action runs, and what an action killed before it could remove its
	// own left is removed first.
	sweep(temporaries, time.Now())
	if a.temporary, err = os.MkdirTemp(temporaries, TemporaryPrefix); err != nil {
		return err
	}
	if a.held, err = hold(a.temporary); err != nil {
		a.leave()
		return err
	}
	a.environment = append(slices.DeleteFunc(a.environment, func(each string) bool { return strings.HasPrefix(each, "TMPDIR=") }), "TMPDIR="+a.temporary)
	if err := confineTo(a.root, a.temporary); err != nil {
		a.leave()
		return err
	}
	// Made once confined: a link the root holds where they are leads
	// nowhere it may not write.
	for _, dir := range a.made {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return supervise(a)
}

// supervise runs the action's command, confined as this thread is, as
// its child, and ends with it: its exit status, or 128 and the number of
// the signal that ended it. It is a subreaper (PR_SET_CHILD_SUBREAPER): whatever the command
// starts stays beneath it, whatever its parent does, and once the
// command has ended every process it left is killed, until none is.
// A runner sandbox runs one action after another: nothing of one is
// left running into the next.
func supervise(a prepared) error {
	if _, _, e := syscall.Syscall6(prctl[runtime.GOARCH], prSetChildSubreaper, 1, 0, 0, 0, 0); e != 0 {
		return fmt.Errorf("keeping what the action starts beneath it: %w", e)
	}
	// This program stays dumpable: OpenShell knows a connection by what
	// opened it and its ancestors, read through /proc, and refuses one
	// whose ancestor it cannot read. What the command could do to this
	// program through /proc is the kernel's ptrace rules' to refuse.
	// What the runner asks of the action, it asks of its command: watched
	// for before the command starts, so that none is missed meanwhile.
	asked := make(chan os.Signal, 8)
	signal.Notify(asked, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT)
	attr := &syscall.ProcAttr{Env: a.environment, Files: []uintptr{0, 1, 2}}
	child, err := syscall.ForkExec(a.program, a.command, attr)
	if errors.Is(err, syscall.ENOEXEC) {
		// A file of commands with no #! line is run by the shell, as a
		// shell runs one it is told to run.
		child, err = syscall.ForkExec("/bin/sh", append([]string{"sh", "--", a.program}, a.command[1:]...), attr)
	}
	if err != nil {
		a.leave()
		return fmt.Errorf("%s: %w", a.program, err)
	}
	go func() {
		for sig := range asked {
			_ = syscall.Kill(child, sig.(syscall.Signal))
		}
	}()
	var status syscall.WaitStatus
	for {
		_, err := syscall.Wait4(child, &status, 0, nil)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
	signal.Stop(asked)
	reap()
	// Nothing it started is left to use its temporary directory.
	a.leave()
	// A command a signal ended ends the action as a shell says one did:
	// 128 and the signal's number. Raised again here, the signal would
	// meet the Go runtime's own handling of it.
	if status.Signaled() {
		os.Exit(128 + int(status.Signal()))
	}
	os.Exit(status.ExitStatus())
	return nil
}

// reap kills every child this process has, and each that becomes its
// child as its parent ends, until it has none: what the action left.
func reap() {
	self := os.Getpid()
	for {
		for _, pid := range childrenOf(self) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if _, err := syscall.Wait4(-1, nil, 0, nil); errors.Is(err, syscall.ECHILD) {
			return
		}
	}
}

// childrenOf is the living processes whose parent is the one given.
func childrenOf(parent int) []int {
	listed, _ := processes()
	var out []int
	for _, each := range listed {
		if each.parent == parent {
			out = append(out, each.pid)
		}
	}
	return out
}

// SignalGroup sends a signal to a process group: every process of it that
// has not left it. A sandbox may refuse a signal to a group: OpenShell's
// filter of a workload's calls refuses kill with a negative pid, in a
// workspace's sandbox and a runner's alike. Each living process of the
// group is then signalled by itself, as the system lists them; and a kill
// is sent again until none is left, since one may start another
// meanwhile. A group that is gone is no error.
func SignalGroup(group int, signal syscall.Signal) error {
	refused := killGroup(group, signal)
	if refused == nil || errors.Is(refused, syscall.ESRCH) {
		return nil
	}
	// Each process is signalled once, known by its number and when it
	// started, which no process that takes the number later shares: one
	// killed may take a while to end, and is not killed again meanwhile.
	signalled := map[[2]int]bool{}
	for {
		listed, err := processes()
		if err != nil {
			return fmt.Errorf("the group could not be signalled (%v), nor its processes listed: %w", refused, err)
		}
		more := 0
		for _, each := range listed {
			if each.group != group || signalled[[2]int{each.pid, each.started}] {
				continue
			}
			signalled[[2]int{each.pid, each.started}] = true
			more++
			if err := syscall.Kill(each.pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
				return fmt.Errorf("the process %d of the group could not be signalled: %w", each.pid, err)
			}
		}
		if more == 0 || signal != syscall.SIGKILL {
			return nil
		}
	}
}

// killGroup signals a process group as the system is asked to; a test
// of this package refuses it, as OpenShell does.
var killGroup = func(group int, signal syscall.Signal) error { return syscall.Kill(-group, signal) }

// process is a living process as /proc lists it.
type process struct{ pid, parent, group, started int }

// processes is every living process /proc lists: one that has ended and
// not been reaped (a zombie) is no longer anyone's to signal.
func processes() ([]process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []process
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			// It ended while it was being looked at.
			continue
		}
		if state, parent, group, started, ok := statOf(stat); ok && state != 'Z' {
			out = append(out, process{pid: pid, parent: parent, group: group, started: started})
		}
	}
	return out, nil
}

// statOf reads a process's state, parent, group and when it started (in
// clock ticks since the system booted) from its stat: "pid (name) state
// ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt utime
// stime cutime cstime priority nice threads itrealvalue starttime ...",
// the name ending at the last ")", whatever it holds.
func statOf(stat []byte) (state byte, parent, group, started int, ok bool) {
	at := bytes.LastIndexByte(stat, ')')
	if at < 0 {
		return 0, 0, 0, 0, false
	}
	fields := strings.Fields(string(stat[at+1:]))
	if len(fields) < 20 || len(fields[0]) != 1 {
		return 0, 0, 0, 0, false
	}
	var err error
	if parent, err = strconv.Atoi(fields[1]); err != nil {
		return 0, 0, 0, 0, false
	}
	if group, err = strconv.Atoi(fields[2]); err != nil {
		return 0, 0, 0, 0, false
	}
	if started, err = strconv.Atoi(fields[19]); err != nil {
		return 0, 0, 0, 0, false
	}
	return fields[0][0], parent, group, started, true
}

// prepared is an action ready to run: its root, its program as found, its
// command and its environment.
type prepared struct {
	root, program string
	command       []string
	environment   []string
	// made are the directories the action keeps, made in its root once
	// it is confined.
	made []string
	// temporary is the action's own temporary directory, beside the
	// root, and held the hold on it while the action runs.
	temporary string
	held      *os.File
}

// leave empties the action's temporary directory and lets go of it.
// What it holds goes now; the directory itself is the next action's to
// sweep (sweep), before it is confined: once confined, the action may
// remove nothing of the directory it is in.
func (a prepared) leave() {
	// Its own to delete: what is in the directory this confiner made
	// for its one action, and holds.
	if a.temporary != "" {
		if entries, err := os.ReadDir(a.temporary); err == nil {
			for _, entry := range entries {
				_ = os.RemoveAll(filepath.Join(a.temporary, entry.Name()))
			}
		}
	}
	if a.held != nil {
		_ = a.held.Close()
	}
}

// prepare finds the action's root, makes what the action keeps in it, and
// works out the action's environment and its program.
func prepare(args []string) (prepared, error) {
	if len(args) < 3 {
		return prepared{}, errors.New("frontseat-action is given a spec, the way up to the action's root, and a command")
	}
	spec, err := read(args[0])
	if err != nil {
		return prepared{}, err
	}
	up := args[1]
	for _, step := range strings.Split(up, "/") {
		if step != ".." && step != "." {
			return prepared{}, errors.New("the way up to the root is not .. or . alone")
		}
	}
	// Where it runs, as the system says, never as the environment does:
	// os.Getwd takes $PWD where it names the same directory, by whatever
	// way, and the way up is taken from it.
	here, err := syscall.Getwd()
	if err != nil {
		return prepared{}, err
	}
	root := filepath.Join(here, up)
	if spec.SayRoot {
		if _, err := fmt.Fprintln(os.Stdout, root); err != nil {
			return prepared{}, err
		}
	}
	env := environ()
	env.set("HOME", filepath.Join(root, spec.Home))
	env.set("TMPDIR", filepath.Join(root, spec.Temporary))
	for _, each := range spec.Environment {
		if each.Parts == nil {
			env.unset(each.Name)
			continue
		}
		env.set(each.Name, strings.Join(each.Parts, root))
	}
	// Where the command runs, as the shell that ran it said: whatever the
	// environment said of it before.
	env.set("PWD", here)
	command := args[2:]
	word := command[0]
	if spec.Program && strings.HasPrefix(word, "~/") {
		word = filepath.Join(env.get("HOME"), strings.TrimPrefix(word, "~/"))
	}
	program, err := find(word, env.get("PATH"))
	if err != nil {
		return prepared{}, err
	}
	if spec.Program {
		plain, err := filepath.EvalSymlinks(program)
		if err != nil {
			return prepared{}, err
		}
		plain, err = filepath.Abs(plain)
		if err != nil {
			return prepared{}, err
		}
		base, err := filepath.EvalSymlinks(root)
		if err != nil {
			return prepared{}, err
		}
		what := "prepared input"
		if !strings.Contains(command[0], "/") {
			base, what = filepath.Join(base, MiseInstalls), "prepared tool"
		}
		rel, err := filepath.Rel(base, plain)
		if err != nil || !filepath.IsLocal(rel) || rel == "." {
			return prepared{}, fmt.Errorf("a service program is not a %s", what)
		}
		program = plain
	}
	return prepared{root: root, program: program, command: command, environment: env.list(),
		made: []string{filepath.Join(root, spec.Home), filepath.Join(root, spec.Temporary)}}, nil
}

// confineTo confines what this thread runs from now on, and what it
// becomes (exec), to the action's root, by Landlock: it may read and run
// what the system holds, write its root and the devices that keep nothing,
// and nothing else; it reaches a socket by its path in its root alone,
// where the kernel says so (ABI 9: Linux 7.1), and not the runner's, which
// runs what it is sent unconfined; and it asks that the action not
// signal, nor reach by an abstract socket, a process outside what it runs
// (ABI 6), which holds in a plain container and was measured not to
// inside an OpenShell sandbox, where nothing then holds it. A runner
// sandbox runs one action after another, each sharing the sandbox's user
// and its slot: so no action writes the slot, where the next action's
// directory and the runner's socket are, nor what another left. Landlock
// needs no privilege and makes no namespace, so it holds inside an
// OpenShell sandbox, which refuses one made in it. Where the kernel has no
// Landlock, the action is not run.
//
// Landlock holds the thread that asks for it, and what it becomes: the
// thread is locked, and the program execs on it. What reaches the network
// is the runner sandbox's to say (an offline slot reaches nothing); a rule
// of Landlock's names ports, and would take the loopback a test serves
// itself on with the rest.
func confineTo(root, temporary string) error {
	top, below, _ := strings.Cut(strings.TrimPrefix(root, "/"), "/")
	if top == "" || below == "" {
		return fmt.Errorf("an action's root is %s: below a directory of the system's, never one itself", root)
	}
	if _, known := prctl[runtime.GOARCH]; runtime.GOOS != "linux" || !known {
		return errors.New("an action is confined by Landlock, which is Linux's on arm64 and amd64: it is run there alone")
	}
	runtime.LockOSThread()
	abi, _, e := syscall.Syscall(landlockCreateRuleset, 0, 0, landlockVersion)
	if e != 0 || abi < 1 {
		return fmt.Errorf("the kernel has no Landlock (%v), which confines an action: it is not run", e)
	}
	handled := uint64(1<<13 - 1) // ABI 1: execute to make a symbolic link
	if abi >= 2 {
		handled |= accessRefer
	}
	if abi >= 3 {
		handled |= accessTruncate
	}
	if abi >= 5 {
		handled |= accessIoctlDev
	}
	if abi >= 9 {
		// A socket is reached by its path in the root alone: not the
		// runner's, in the slot, which runs what it is sent unconfined.
		handled |= accessResolveUnix
	}
	attr := rulesetAttr{handledFS: handled}
	size := unsafe.Sizeof(attr)
	if abi >= 6 {
		attr.scoped = scopeAbstractUnixSocket | scopeSignal
	} else {
		// The kernel reads as much of the attribute as it knows of.
		size = unsafe.Offsetof(attr.scoped)
	}
	ruleset, _, e := syscall.Syscall(landlockCreateRuleset, uintptr(unsafe.Pointer(&attr)), size, 0)
	if e != 0 {
		return fmt.Errorf("making the action's Landlock rules: %w", e)
	}
	defer syscall.Close(int(ruleset))
	device := (accessWriteFile | accessReadFile | accessTruncate | accessIoctlDev) & handled
	// What the system holds is read and run: the slot too, which holds
	// nothing of another action's, and whose runner socket is root's
	// alone (the pool). A sandbox may list no directory OpenShell's policy
	// does not name, so the system's directories cannot be named here one
	// by one.
	if err := allow(ruleset, "/", (accessExecute|accessReadFile|accessReadDir)&handled); err != nil {
		return err
	}
	// What the runner keeps the command's output in, beside the root
	// (its stdout and stderr files), is written as the command writes
	// its output: through /dev/stdout, which leads there.
	output := (accessWriteFile | accessTruncate) & handled
	for _, rule := range []struct {
		path    string
		allowed uint64
	}{
		{"/dev/null", device},
		{"/dev/zero", device},
		{"/dev/full", device},
		{filepath.Join(filepath.Dir(root), "stdout"), output},
		{filepath.Join(filepath.Dir(root), "stderr"), output},
	} {
		if err := allow(ruleset, rule.path, rule.allowed); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// Its own temporary directory, beside the root, is written as the
	// root is.
	if err := allow(ruleset, temporary, handled); err != nil {
		return err
	}
	if err := allow(ruleset, root, handled); err != nil {
		return err
	}
	// What becomes root by what it runs gains nothing: Landlock is asked
	// for by a process that may not.
	if _, _, e := syscall.Syscall6(prctl[runtime.GOARCH], prSetNoNewPrivs, 1, 0, 0, 0, 0); e != 0 {
		return fmt.Errorf("refusing the action new privileges: %w", e)
	}
	if _, _, e := syscall.Syscall(landlockRestrictSelf, ruleset, 0, 0); e != 0 {
		return fmt.Errorf("confining the action: %w", e)
	}
	return nil
}

// allow lets what is beneath a path be reached as allowed.
func allow(ruleset uintptr, path string, allowed uint64) error {
	opened, err := os.OpenFile(path, os.O_RDONLY|syscall.O_CLOEXEC|oPath, 0)
	if err != nil {
		return err
	}
	defer opened.Close()
	beneath := pathBeneath{allowed: allowed, parent: int32(opened.Fd())}
	if _, _, e := syscall.Syscall6(landlockAddRule, ruleset, ruleBeneath, uintptr(unsafe.Pointer(&beneath)), 0, 0, 0); e != 0 {
		return fmt.Errorf("letting the action reach %s: %w", path, e)
	}
	return nil
}

// Landlock's calls, rights and attributes (linux/landlock.h), and what of
// the kernel it needs beside, by their numbers, which are the same on
// arm64 and amd64.
const (
	landlockCreateRuleset = 444
	landlockAddRule       = 445
	landlockRestrictSelf  = 446
	landlockVersion       = 1
	ruleBeneath           = 1
	prSetNoNewPrivs       = 38
	prSetChildSubreaper   = 36

	accessExecute   = 1 << 0
	accessWriteFile = 1 << 1
	accessReadFile  = 1 << 2
	accessReadDir   = 1 << 3
	accessRefer     = 1 << 13
	accessTruncate  = 1 << 14
	accessIoctlDev  = 1 << 15
	// accessResolveUnix is a connection to a socket by its path (ABI 9).
	accessResolveUnix = 1 << 16

	scopeAbstractUnixSocket = 1 << 0
	scopeSignal             = 1 << 1
)

// prctl is the number of prctl(2), which differs between the two.
var prctl = map[string]uintptr{"amd64": 157, "arm64": 167}

type rulesetAttr struct {
	handledFS, handledNet, scoped uint64
}

// pathBeneath is packed as the kernel's is: eight bytes and four.
type pathBeneath struct {
	allowed uint64
	parent  int32
	_       [4]byte
}

// read reads a spec, of this program's form alone, and holds what it
// names to the root: what is made is made beneath it.
func read(given string) (Spec, error) {
	var spec Spec
	decoder := json.NewDecoder(strings.NewReader(given))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return Spec{}, fmt.Errorf("the spec: %w", err)
	}
	if spec.Form != Form {
		return Spec{}, fmt.Errorf("the spec is for frontseat-action %.12s, and this is %.12s: the worker's is not the daemon's", spec.Form, Form)
	}
	for _, dir := range []string{spec.Home, spec.Temporary} {
		if !filepath.IsLocal(dir) {
			return Spec{}, errors.New("the spec names its home or its temporary directory by what is not a directory beneath the root")
		}
	}
	return spec, nil
}

// find is the program a command's first word names, as a shell finds
// it: a word with a slash in it is a path, and any other is looked for
// in each directory of the path, an empty one being where it runs.
func find(word, path string) (string, error) {
	if word == "" {
		return "", errors.New("the command names no program")
	}
	if strings.Contains(word, "/") {
		return word, nil
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, word)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", &fs.PathError{Op: "find", Path: word, Err: fs.ErrNotExist}
}

// environment is an environment as name=value, in the order it was set.
type environment []string

func environ() *environment {
	env := environment(os.Environ())
	return &env
}

func (e *environment) at(name string) int {
	for i, each := range *e {
		if n, _, _ := strings.Cut(each, "="); n == name {
			return i
		}
	}
	return -1
}

func (e *environment) get(name string) string {
	if i := e.at(name); i >= 0 {
		_, value, _ := strings.Cut((*e)[i], "=")
		return value
	}
	return ""
}

func (e *environment) set(name, value string) {
	if i := e.at(name); i >= 0 {
		(*e)[i] = name + "=" + value
		return
	}
	*e = append(*e, name+"="+value)
}

func (e *environment) unset(name string) {
	if i := e.at(name); i >= 0 {
		*e = append((*e)[:i], (*e)[i+1:]...)
	}
}

func (e *environment) list() []string { return *e }

// Temporaries is where an action's own temporary directory is made: the
// runner sandbox's own filesystem, at a place fixed here, never one an
// environment names, since the environment the confiner starts with is
// the request's, which any client of the grid may write.
const Temporaries = "/tmp"

// temporaries is where this program makes them: Temporaries, but where
// this package's own tests run it inside an action, whose confinement
// keeps it from Temporaries. Nothing but a test of this package sets it,
// from within that test's program; nothing an action is given does.
var temporaries = Temporaries

// TemporaryPrefix names an action's temporary directory: the confiner
// makes every directory so named, and nothing else does.
const TemporaryPrefix = "frontseat-action-"

// fresh is how long after it was made an action's temporary directory
// is left alone whether or not it is held: what made it may not have
// taken hold of it yet.
const fresh = time.Minute

// hold takes hold of an action's temporary directory, for as long as the
// file it returns is open.
func hold(dir string) (*os.File, error) {
	file, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("holding %s: %w", dir, err)
	}
	return file, nil
}

// sweep removes the temporary directories of actions that ended without
// removing them, as one killed does. They are its own to remove: each
// was made by a confiner, as an action's own, and one that no running
// action holds, and that is not just made, is no running action's.
func sweep(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), TemporaryPrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < fresh {
			continue
		}
		at := filepath.Join(dir, entry.Name())
		held, err := hold(at)
		if err != nil {
			continue
		}
		_ = os.RemoveAll(at)
		_ = held.Close()
	}
}

// Clear removes every action's temporary directory in dir, whatever its
// age and whether or not it is held: what runs actions one at a time
// clears them between two (the runner), so that nothing one action left,
// killed before it could empty its own, is read by the next. They are
// its caller's to delete: each is named as a confiner names an action's
// own, in the directory the caller's actions alone are given theirs in.
func Clear(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), TemporaryPrefix) {
			_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
		}
	}
}
