package machine

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/frontseat-dev/frontseat/local/ports"
	"github.com/frontseat-dev/frontseat/sdk/go/bearer"
)

// tokenFile is where the local stack lays out its owner's token
// (frontseat-local up): frontseat/local/token.
func tokenFile() (string, error) {
	return beneath("local", "token")
}

// serverFile names the control plane the command line asks, where it
// is not the local stack's: frontseat/local/server. Whoever lays out a
// token for another control plane writes it beside the token, and the
// local stack removes it when it lays out its own.
func serverFile() (string, error) {
	return beneath("local", "server")
}

// written is a token as its file holds it: one word of what a header
// carries as it is, on one line. Its form is the control plane's to
// choose, and is not read here.
var written = regexp.MustCompile(`^[\x21-\x7e]{1,126}\n?$`)

// Stack is the control plane of the local stack, the one the command
// line asks where nothing names another.
var Stack = "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.ControlPlane))

// Token is the token the control plane the command line asks knows this
// machine's person by, as it was laid out beside it (Server); "" where
// none was. One another could read, or a file that holds no token, is
// an error, which never says the token.
func Token() (string, error) {
	path, err := tokenFile()
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s can be read by others (%s): it is worth everything the control plane holds of yours, so it is yours alone (chmod 600)", path, info.Mode().Perm())
	}
	raw, err := io.ReadAll(io.LimitReader(file, 128))
	if err != nil {
		return "", err
	}
	if !written.Match(raw) {
		return "", fmt.Errorf("%s holds no token: mise start lays the local stack's out again", path)
	}
	return strings.TrimSuffix(string(raw), "\n"), nil
}

// Server is the control plane the command line asks: the one
// frontseat/local/server names, written as https://HOST[:PORT] on one
// line, or the local stack's where there is no such file. The token
// beside it is for it, and is presented to it alone. One on another
// machine is asked over https alone (bearer.Private): the token, a
// credential and what a workspace's shell says are read by no one on the
// way, and the control plane is known to be the one named, which is
// what vouches for a sandbox's host key. One on this machine is written
// with http as well.
func Server() (string, error) {
	path, err := serverFile()
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Stack, nil
	}
	if err != nil {
		return "", err
	}
	// It says where the token goes, and so is the person's alone to
	// write, as the token is theirs alone to read.
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("%s can be written by others (%s), or is no file: it says where the owner's token is sent, so it is yours alone (chmod 600)", path, info.Mode().Perm())
	}
	if dir, err := os.Stat(filepath.Dir(path)); err != nil || dir.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("%s can be written by others: it holds the owner's token and where it is sent, so it is yours alone (chmod 700)", filepath.Dir(path))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	named := strings.TrimSuffix(string(raw), "\n")
	at, err := url.Parse(named)
	if err != nil || (at.Scheme != "http" && at.Scheme != "https") || at.Host == "" || at.User != nil || (at.Path != "" && at.Path != "/") || at.RawQuery != "" || at.Fragment != "" || strings.ContainsAny(named, "\r\n") {
		return "", fmt.Errorf("%s names no control plane: it holds one address, written as https://HOST[:PORT], or http://HOST[:PORT] where it is on this machine", path)
	}
	if !bearer.Private(at) {
		return "", fmt.Errorf("%s names a control plane on another machine without TLS: your token would be sent to it in clear, so it is written https://HOST[:PORT]; http is for one on this machine (localhost, a loopback address)", path)
	}
	return strings.TrimSuffix(named, "/"), nil
}
