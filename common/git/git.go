// Package git is git as frontseat runs it, for the client's version
// control package and for the daemon's alike: the one place that starts
// the program, and the one place that reads a remote as git writes one.
//
// A repository can come from anywhere, and what its configuration says
// can tell git to run programs. So git is run here as devvm runs it on a
// host (devvm 2f0cc97, internal/hostgit, MIT), and further. What the
// tests of this package and of its two callers hold, each beside git
// asked plainly, which does it: a file monitor, the repository's hooks,
// a hook its configuration names (where git has those: 2.56 does, the
// Mac's 2.50 does not), each filter it names and the program that
// verifies a signature as the history is shown are not run; no program
// the repository names is asked for a password, and no credential helper
// it names is asked for a credential or given one to keep, while the one
// the environment told is; a remote is reached over a path, http or
// https alone, so no SSH client, command written as a remote or as a
// rewritten address, or helper for another system is run; and a
// repository whose configuration names a program for asking a remote
// (upload-pack, receive-pack), which git takes before any setting given
// after it, is refused wherever a remote may be reached. Where a remote
// may be reached, git fetches from and pushes to the address it was
// given (Remote) alone: origin is that address, whatever the repository
// says origin is or pushes to, since an empty address ends the list of
// those before it (as git 2.50, the Mac's, documents and does, and 2.56,
// the box's, does; a git that does not would keep the repository's);
// no bundle a server advertises is fetched; and a repository whose own
// configuration rewrites an address (url.<address>.insteadOf,
// pushInsteadOf), names a remote other than origin to fetch what it
// lacks from (remote.<name>.promisor, remote.<name>.partialCloneFilter,
// extensions.partialClone) or an address to fetch bundles from
// (fetch.bundleURI), names a proxy (http.proxy, remote.<name>.proxy),
// the address a host is reached at (http.curloptResolve) or the
// authorities a server's certificate is checked against (http.sslCAInfo,
// http.sslCAPath), or says anything of how one address is reached over
// http (http.<address>.*, which wins over every setting for every
// address), none of which a setting given after it puts back as the
// sandbox has it, is refused by the setting's name and nothing it says.
// Over http, a server's certificate is checked against what the sandbox
// told (GIT_SSL_CAINFO) or the machine's, and no header or cookie file
// the repository names is sent or written; what the sandbox told git of
// http, for every address or for one, holds over that. Git keeps what
// the server gives as cookies for as long as it runs. A repository
// whose settings cannot be listed is refused, since what is not listed
// is not turned off. A history is
// what its commits say, and not what a replacement, a graft or a graph
// of commits written beside them says instead; and a repository whose
// configuration puts its working tree elsewhere than the directory that
// holds it is refused. Git is given none of the git variables of the
// environment this runs in but what its options keep, so another
// repository it would be pointed at is not read. What git started is
// stopped with it when what asked is cancelled, where there are process
// groups (not on Windows).
//
// Also set, and held by no test here: no command for alternates' refs,
// no housekeeping, no question at the terminal, no credential helper of
// a person's own settings; and, for what asks no remote (Offline), no
// transport at all and no object fetched it lacks.
//
// Not held. Where a remote is: what the machine's and the sandbox's own
// settings say (the system's and a person's, and what git is told as it
// starts) is taken as theirs, a rewritten address and a proxy among it;
// and the rest of what a repository may say of how http is spoken for
// every address (the versions and ciphers of TLS; a certificate of its
// own to present) is git's to read.
// Over the file transport, which is not Offline and which tests clone
// from, an address may be any repository the program may read. What an
// object is: one written under another's name, which anyone who may
// write the repository can do, is read as that other (only git fsck
// finds it). A .git that is a file naming a repository elsewhere is
// read, as a linked working tree's and a submodule's are, for the
// directory that holds it. Nor is a repository within the repository
// asked as this one is: a caller asks nothing of one that is there.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// waitDelay is how long what git started may hold what git printed to
// once git has ended or was stopped.
const waitDelay = 2 * time.Second

// Options say how git is run beyond what every run is told.
type Options struct {
	// Offline is git that reaches nothing over any transport, fetches no
	// object it lacks and starts no SSH client: what asks no remote, as
	// the client reads a checkout of this machine and the daemon asks
	// where a repository stands.
	Offline bool
	// Told keeps what a sandbox tells git in its environment of how to
	// reach a remote: the settings it counts (GIT_CONFIG_COUNT,
	// GIT_CONFIG_KEY_n, GIT_CONFIG_VALUE_n), as a daemon is told how a
	// credential is presented, and the authorities a server's certificate
	// is checked against (GIT_SSL_CAINFO, GIT_SSL_CAPATH), as OpenShell
	// tells it those of its proxy. The settings come after what is told
	// here of how a remote is reached (pinned), so they hold over it, and
	// before what every run is told (hardened), which holds over them;
	// every other git variable of the environment is left out all the same.
	Told bool
	// Remote is the one address git fetches from, as whoever asked for
	// the repository gave it: origin is that address, whatever the
	// repository's configuration says origin is, and what it fetches as a
	// partial clone is fetched from there. It is what a clone is made from
	// and nothing else; empty, origin is as the repository says.
	Remote string
}

// Git is git as it is run in one directory: hardened, and with every
// hook and filter the directory's configuration names turned off.
type Git struct {
	directory string
	options   Options
	settings  [][2]string
	// refused is why nothing is asked of the directory.
	refused error
}

// In is git as it is run in a directory. What the directory's own
// configuration would have git run is turned off by its name before
// anything else is asked of it: a filter is what git would run to say
// whether a file changed. A program for asking a remote cannot be turned
// off so, since git takes the first it reads, so a repository that names
// one is refused unless git is Offline; and so is one whose settings
// cannot be listed, whatever the reason, since what is not listed is not
// turned off. A repository whose configuration puts its working tree
// somewhere other than the directory that holds it is refused too: git
// would say nothing of what is changed there, and read another.
func In(ctx context.Context, directory string, options Options) Git {
	g := Git{directory: directory, options: options}
	// An empty helper ends the list of those named before it: those of
	// the repository and of a person's own settings. What the sandbox
	// told comes after, and is kept.
	g.settings = [][2]string{{"credential.helper", ""}}
	if !options.Offline {
		g.settings = append(g.settings, pinned(options.Remote)...)
	}
	if options.Told {
		g.settings = append(g.settings, told()...)
	}
	g.settings = append(g.settings, hardened(options.Offline)...)
	out, err := g.Run(ctx, "config", "--show-scope", "-z", "--get-regexp", listed)
	// git config says it found nothing by its status of 1, and nothing
	// else.
	var exit *osexec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		g.refused = fmt.Errorf("git: the settings of the directory are not listed, so what they name cannot be turned off: %w", err)
		return g
	}
	seen := map[string]bool{}
	// Each setting is its scope, then its key and, after a newline, its
	// value, each ended by a NUL.
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		scope := fields[i]
		key, value, _ := strings.Cut(fields[i+1], "\n")
		if !options.Offline && g.refused == nil {
			g.refused = reaching(scope, key, value)
		}
		// A name may be empty ("filter..clean"), and is one all the same.
		section, rest, found := strings.Cut(key, ".")
		last := strings.LastIndex(rest, ".")
		if !found || last < 0 || seen[section+"."+rest[:last]] {
			continue
		}
		name := rest[:last]
		seen[section+"."+name] = true
		switch section {
		case "hook":
			g.settings = append(g.settings, [2]string{"hook." + name + ".enabled", "false"})
		case "filter":
			for _, each := range []string{"clean", "smudge", "process"} {
				g.settings = append(g.settings, [2]string{"filter." + name + "." + each, ""})
			}
			g.settings = append(g.settings, [2]string{"filter." + name + ".required", "false"})
		}
	}
	if g.refused == nil {
		g.refused = g.worktree(ctx)
	}
	return g
}

// listed is what In asks the configuration for, as git config matches a
// key, its section and its last part in lower case: what git would run
// (a filter, a hook, a program to ask a remote with), and what would
// have git reach another address than the one it was given, or reach it
// otherwise than the sandbox says.
const listed = `^(filter|hook)\.|^remote\..*\.(uploadpack|receivepack|promisor|partialclonefilter|proxy)$|^url\.|^extensions\.partialclone$|^http\.|^fetch\.bundleuri$`

// reaching refuses a setting that would have git run a program to ask a
// remote with, which it takes before any setting given after it, in any
// scope; and one of the repository's own, in any scope but the
// machine's and what git is told as it is started, that would have it
// fetch from another address than it was given, or reach that address
// otherwise than the sandbox says, which no setting given after it puts
// back as the sandbox has it: a setting of http for one address is one,
// since it wins over any for every address, whatever comes after it.
// Nothing a setting says is repeated: an address may hold a token.
func reaching(scope, key, value string) error {
	section, rest, _ := strings.Cut(key, ".")
	last := strings.LastIndex(rest, ".")
	subsection, variable := "", rest
	if last >= 0 {
		subsection, variable = rest[:last], rest[last+1:]
	}
	if section == "remote" && (variable == "uploadpack" || variable == "receivepack") {
		return fmt.Errorf("git: the configuration names a program to ask a remote with (remote.<name>.%s), which git would run: remove it", variable)
	}
	switch scope {
	case "system", "global", "command":
		return nil
	}
	switch {
	case section == "url":
		return fmt.Errorf("git: the repository's configuration rewrites an address (url.<address>.%s), and git would fetch from another: remove it", variable)
	case section == "remote" && (variable == "promisor" || variable == "partialclonefilter") && subsection != "origin":
		return fmt.Errorf("git: the repository's configuration names a remote other than origin to fetch what it lacks from (remote.<name>.%s): remove it", variable)
	case section == "extensions" && value != "origin":
		return errors.New("git: the repository's configuration names a remote other than origin to fetch what it lacks from (extensions.partialClone): remove it")
	case section == "fetch":
		return errors.New("git: the repository's configuration names an address to fetch bundles from (fetch.bundleURI), which git would ask: remove it")
	case section == "remote" && variable == "proxy":
		return errors.New("git: the repository's configuration names a proxy for a remote (remote.<name>.proxy), which would stand in for the sandbox's: remove it")
	case section == "http" && subsection != "":
		return fmt.Errorf("git: the repository's configuration says how one address is reached over http (http.<address>.%s), which would win over what the sandbox says: remove it", variable)
	case section == "http" && variable == "proxy":
		return errors.New("git: the repository's configuration names a proxy (http.proxy), which would stand in for the sandbox's: remove it")
	case section == "http" && variable == "curloptresolve":
		return errors.New("git: the repository's configuration names the address a host is reached at (http.curloptResolve), which git would reach instead: remove it")
	case section == "http" && (variable == "sslcainfo" || variable == "sslcapath"):
		return fmt.Errorf("git: the repository's configuration names the authorities a server's certificate is checked against (http.%s), which would stand in for the sandbox's: remove it", variable)
	}
	return nil
}

// pinned is what git is told of how to reach a remote, where it may:
// origin is the remote it was given and nothing the repository says, for
// a fetch and for a push, since an empty address ends the list of those
// before it; a push to it sends what was asked and nothing the
// repository adds, which holds for origin alone, so that whoever pushes
// names origin and never the address, which git would first read as the
// name of a remote the repository describes; no bundle a server advertises is fetched; and over http, a
// server's certificate is checked, and no header or cookie the
// repository names is sent or kept. Each is a setting for every address,
// which a repository's setting for one would win over, and such a
// setting of the repository's is refused (reaching). They come before
// what the sandbox told, which so holds over them, for every address or
// for one.
func pinned(remote string) [][2]string {
	var settings [][2]string
	if remote != "" {
		settings = append(settings,
			[2]string{"remote.origin.url", ""},
			[2]string{"remote.origin.url", remote},
			[2]string{"remote.origin.pushurl", ""},
			// A push sends what it was asked to send, to origin, and
			// says and runs nothing the repository adds: no option for
			// the server (an empty one ends the list of those before
			// it), no program to sign with, nothing of a submodule, no
			// tag beside what was asked, and never every reference the
			// repository has.
			[2]string{"push.pushOption", ""},
			[2]string{"push.gpgSign", "false"},
			[2]string{"push.recurseSubmodules", "no"},
			[2]string{"push.followTags", "false"},
			[2]string{"remote.origin.mirror", "false"},
		)
	}
	return append(settings,
		[2]string{"transfer.bundleURI", "false"},
		[2]string{"http.sslVerify", "true"},
		[2]string{"http.extraHeader", ""},
		[2]string{"http.cookieFile", ""},
		[2]string{"http.saveCookies", "false"},
	)
}

// worktree refuses a repository whose working tree, as git reads it, is
// not the directory that holds the repository's .git, a directory or a
// file as a linked working tree has: what the configuration says
// (core.worktree) is never repeated, since it may name anything. Where
// no .git holds the directory there is no working tree to put elsewhere,
// and git says so itself where one is asked for.
func (g Git) worktree(ctx context.Context) error {
	if g.directory == "" {
		return nil
	}
	holder, err := filepath.Abs(g.directory)
	if err == nil {
		holder, err = filepath.EvalSymlinks(holder)
	}
	if err != nil {
		return fmt.Errorf("git: the directory is not found: %w", err)
	}
	for {
		if _, err := os.Lstat(filepath.Join(holder, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(holder)
		if parent == holder {
			return nil
		}
		holder = parent
	}
	top, err := g.Run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	read, err := filepath.EvalSymlinks(strings.TrimRight(top, "\r\n"))
	if err != nil || read != holder {
		return errors.New("git: the configuration puts the working tree somewhere other than the directory that holds the repository (core.worktree), and what is there would be read: remove it")
	}
	return nil
}

// hardened is what git is told whatever is asked of it, as settings that
// come after a repository's own and after what the environment told it.
func hardened(offline bool) [][2]string {
	settings := [][2]string{
		{"core.fsmonitor", "false"},
		{"core.hooksPath", os.DevNull},
		{"core.alternateRefsCommand", "true"},
		// A graph of commits is a copy of what the commits say, kept
		// beside them, and anyone who may write the repository may write
		// it to say otherwise: what HEAD's tree is, and what its history.
		{"core.commitGraph", "false"},
		{"core.askPass", ""},
		{"log.showSignature", "false"},
		{"maintenance.auto", "false"},
		{"gc.auto", "0"},
	}
	if offline {
		settings = append(settings, [2]string{"protocol.allow", "never"})
	}
	return settings
}

// told is the settings the environment this runs in counts, in their
// order. A count that is none, or a key that is missing, ends them.
func told() [][2]string {
	count, err := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	if err != nil || count < 0 {
		return nil
	}
	var settings [][2]string
	for i := range count {
		n := strconv.Itoa(i)
		key, found := os.LookupEnv("GIT_CONFIG_KEY_" + n)
		if !found {
			break
		}
		settings = append(settings, [2]string{key, os.Getenv("GIT_CONFIG_VALUE_" + n)})
	}
	return settings
}

// Run runs git with arguments in its directory and returns what it
// printed as it printed it. Its settings are given in its environment,
// where a name holds any character. Nothing of git's in the environment
// this runs in reaches it but where a person's own settings are kept:
// not the repository it is pointed at, nor settings handed down by
// whatever started this, but what its options keep.
func (g Git) Run(ctx context.Context, args ...string) (string, error) {
	if g.refused != nil {
		return "", g.refused
	}
	var stdout, stderr bytes.Buffer
	cmd := osexec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.directory
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// What git starts, the helper that speaks http among them, is stopped
	// with it, and what still holds what git printed to is let go of.
	stopTogether(cmd)
	cmd.WaitDelay = waitDelay
	var env []string
	for _, each := range os.Environ() {
		name, _, _ := strings.Cut(each, "=")
		own := name == "GIT_CONFIG_GLOBAL" || name == "GIT_CONFIG_SYSTEM" || name == "GIT_CONFIG_NOSYSTEM" ||
			g.options.Told && (name == "GIT_SSL_CAINFO" || name == "GIT_SSL_CAPATH")
		if !strings.HasPrefix(name, "GIT_") || own {
			env = append(env, each)
		}
	}
	// Asked in git's own words, which one refusal is known by.
	env = append(env, "LC_ALL=C", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_COUNT="+strconv.Itoa(len(g.settings)))
	// A history is what its commits say: not what a replacement of one,
	// or a graft of another parent onto one, says instead.
	env = append(env, "GIT_NO_REPLACE_OBJECTS=1", "GIT_GRAFT_FILE="+os.DevNull)
	if g.options.Offline {
		env = append(env, "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=none", "GIT_SSH_COMMAND=false")
	} else {
		env = append(env, "GIT_ALLOW_PROTOCOL=file:http:https")
	}
	for i, setting := range g.settings {
		n := strconv.Itoa(i)
		env = append(env, "GIT_CONFIG_KEY_"+n+"="+setting[0], "GIT_CONFIG_VALUE_"+n+"="+setting[1])
	}
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// Authoring is who commits, as git reads it from its environment before
// anything a repository's own settings say: as the author and as the
// committer. A name or an address that would end a line is not told.
func Authoring(name, email string) map[string]string {
	told := map[string]string{}
	if name != "" && !strings.ContainsAny(name, "\r\n\x00") {
		told["GIT_AUTHOR_NAME"], told["GIT_COMMITTER_NAME"] = name, name
	}
	if email != "" && !strings.ContainsAny(email, "\r\n\x00") {
		told["GIT_AUTHOR_EMAIL"], told["GIT_COMMITTER_EMAIL"] = email, email
	}
	return told
}
