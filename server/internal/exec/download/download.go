// Package download brings what a URL names to the grid, checked by its
// sha256, through the grid's asset service: the Remote Asset API, which
// Bazel's remote downloader and Please's remote assets speak. The
// service fetches a URL once, checks the sha256 before it keeps anything,
// puts the content in the grid's store and remembers the answer by URL
// and checksum, so every repository and daemon of the grid shares one
// download. A daemon asks it for what a shared fetch declares
// (Action.downloads) and places the answer in the fetch's root, where
// the fetch unpacks it with no network (Unpacking).
//
// Where a lock holds no checksum of the whole of a download, only of
// parts of it, the service is asked for the URL unchecked, and the fetch
// cuts each part out and checks it on its worker (Cutting): nothing that
// is not what the lock says is left for a build. The service remembers
// an unchecked answer by its URL alone, and the proxy what it fetched: a
// download that fails its check fails it until one of them lets it go.
//
// A deployment may say which proxy or mirror stands in for each public
// site, as Bazel's --downloader_config does: a URL a rule maps is asked
// of the service by the proxy's address. One no rule maps is asked as it
// is, and only where it is a public site's: https, to a host named, not
// an address, nor this machine, nor with a user's name or password in it.
// What a lock names is the repository's to say, and the service is beside
// the grid's own ports. A name that resolves to the grid's own addresses
// is not held here. The service itself fetches whatever it is asked for,
// from the network it is on, and checks a checksum only where one is
// asked: who may ask it is the deployment's to limit, as it limits who
// may ask the grid.
package download

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/netip"
	neturl "net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	remoteasset "github.com/bazelbuild/remote-apis/build/bazel/remote/asset/v1"
	remoteexecution "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/internal/exec/access"
	"github.com/frontseat-dev/frontseat/server/telemetry"
)

// Config says where the asset service is and which proxy stands in for
// each public site.
type Config struct {
	// Address is the asset service's address, host:port.
	Address string
	// Instance is the instance name asked of, the grid's.
	Instance string
	// Rewrites say which proxy stands in for a public URL. The first
	// that matches, in the order given, is taken: a rule for some of a
	// site is given before the rule for the rest of it.
	Rewrites []Rewrite
	// Access is how the asset service is asked, which is how its grid is.
	Access access.Config
}

// Fetcher brings what a URL names to the grid, and lets go of what it
// remembers of the grid's answers.
type Fetcher interface {
	// Fetch returns the digest the grid holds the content of a URL by,
	// having checked that its sha256 is the one given. With no sha256
	// it is fetched unchecked: what is cut from it is checked where it
	// is cut.
	Fetch(ctx context.Context, url, sha256 string) (exec.Digest, error)
	// Forget lets go of every answer remembered: what a run does when it
	// starts over, having found that the grid let something go.
	Forget()
}

// Client is a client of one asset service.
type Client struct {
	conn     *grpc.ClientConn
	service  remoteasset.FetchClient
	instance string
	rewrites []rewrite
	// access is how the service is asked: its token's file is named
	// where the service does not admit the daemon.
	access access.Config
	// address is where the service is, and shaken what its connection
	// learned of its own TLS: a service that could not be asked over TLS
	// is said so, as its grid is.
	address string
	shaken  *access.Handshakes

	mu       sync.Mutex
	answered map[string]exec.Digest
	// forgotten counts the times Forget was called: an answer that
	// arrives for an ask begun before one is not remembered.
	forgotten int
	asking    singleflight.Group
}

var _ Fetcher = (*Client)(nil)

// named is a sha256 in hex, as the grid names content.
var named = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Rewrite is one rule, as it is given: a regular expression over a
// public URL, and the proxy's address for what it matches, with $1 and
// so on for what the expression captured.
type Rewrite struct {
	From, To string
}

// rewrite is one rule, compiled.
type rewrite struct {
	from *regexp.Regexp
	to   string
}

// fetchWithin is how long the service is given to fetch one thing from
// its origin.
const fetchWithin = 5 * time.Minute

// Dial reaches an asset service. The rules are compiled here, so that one
// that is no regular expression is an error at the start and not at the
// first download.
func Dial(ctx context.Context, config Config) (*Client, error) {
	if config.Address == "" {
		return nil, fmt.Errorf("no asset service: give --assets")
	}
	rules, err := rules(config.Rewrites)
	if err != nil {
		return nil, err
	}
	// The address is handed to the dialer as it is: gRPC's own resolver
	// asks DNS for records a plain host:port never has, and waited
	// eighteen seconds on "localhost" for them.
	options, shaken, err := config.Access.Options(telemetry.GRPCOptions(ctx)...)
	if err != nil {
		return nil, fmt.Errorf("the asset service at %s: %w", config.Address, err)
	}
	conn, err := grpc.NewClient("passthrough:///"+config.Address, options...)
	if err != nil {
		return nil, fmt.Errorf("the asset service at %s: %w", config.Address, err)
	}
	return &Client{conn: conn, service: remoteasset.NewFetchClient(conn), instance: config.Instance, rewrites: rules, access: config.Access, address: config.Address, shaken: shaken}, nil
}

// rules compiles the rewrites, in the order given.
func rules(rewrites []Rewrite) ([]rewrite, error) {
	out := make([]rewrite, 0, len(rewrites))
	for _, each := range rewrites {
		from, err := regexp.Compile(each.From)
		if err != nil {
			return nil, fmt.Errorf("the download rewrite %q: %w", each.From, err)
		}
		out = append(out, rewrite{from: from, to: each.To})
	}
	return out, nil
}

// Close lets go of the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Forget implements Fetcher.
func (c *Client) Forget() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.answered = nil
	c.forgotten++
}

// Fetch implements Fetcher. The service is asked once for each URL and
// sha256 in a daemon's life, until Forget, and once at a time: two
// fetches that ask for one thing together wait for one answer. What it
// answered names content by its digest, which is the sha256 itself, the
// grid's store being addressed by it.
func (c *Client) Fetch(ctx context.Context, url, sum string) (exec.Digest, error) {
	proxied, err := c.proxied(url)
	if err != nil {
		return exec.Digest{}, err
	}
	key := url + "\x00" + sum
	c.mu.Lock()
	digest, known := c.answered[key]
	forgotten := c.forgotten
	c.mu.Unlock()
	if known {
		return digest, nil
	}
	// A flight begun before everything was let go is not joined after:
	// what is asked after is asked afresh.
	flightKey := fmt.Sprintf("%d\x00%s", forgotten, key)
	// The ask is one flight for everyone who waits on it, so it runs on
	// no waiter's context: a waiter that goes away leaves the others
	// their answer, and each waits only as long as its own context lets
	// it. The flight has a deadline of its own.
	flight := context.WithoutCancel(ctx)
	answers := c.asking.DoChan(flightKey, func() (any, error) { return c.ask(flight, url, proxied, sum, key, forgotten) })
	select {
	case <-ctx.Done():
		return exec.Digest{}, fmt.Errorf("%s: %w", site(url), ctx.Err())
	case answer := <-answers:
		if answer.Err != nil {
			return exec.Digest{}, answer.Err
		}
		return answer.Val.(exec.Digest), nil
	}
}

// ask asks the service for one thing, and remembers what it answered,
// unless everything was let go while it asked.
func (c *Client) ask(ctx context.Context, url, proxied, sum, key string, forgotten int) (exec.Digest, error) {
	request := &remoteasset.FetchBlobRequest{
		InstanceName:   c.instance,
		Timeout:        durationpb.New(fetchWithin),
		Uris:           []string{proxied},
		DigestFunction: remoteexecution.DigestFunction_SHA256,
	}
	if sum != "" {
		raw, err := hex.DecodeString(sum)
		if err != nil || len(raw) != sha256.Size || strings.ToLower(sum) != sum {
			return exec.Digest{}, fmt.Errorf("%s: the checksum it is held to is no sha256", site(url))
		}
		request.Qualifiers = []*remoteasset.Qualifier{{Name: "checksum.sri", Value: "sha256-" + base64.StdEncoding.EncodeToString(raw)}}
	}
	ctx, done := context.WithTimeout(access.Watch(ctx), fetchWithin+time.Minute)
	defer done()
	answer, err := c.service.FetchBlob(ctx, request)
	if err != nil {
		// A daemon the service does not admit, or one that could not read
		// its token and sent nothing, is said so as it is of the grid,
		// with the status kept: asking again would say the same.
		told := access.Told(ctx, err)
		if said := c.shaken.Said("the asset service", c.address, told); access.Untrusted(said) {
			return exec.Digest{}, fmt.Errorf("%s, asked of the asset service: %w", site(url), said)
		}
		if access.Unsent(told) || access.NotAdmitted(told) {
			return exec.Digest{}, fmt.Errorf("%s, asked of the asset service: %w", site(url), c.access.Refused("the asset service", told))
		}
		return exec.Digest{}, fmt.Errorf("%s, asked of the asset service: %s", site(url), status.Convert(err).Message())
	}
	if said := status.FromProto(answer.GetStatus()); said.Err() != nil {
		return exec.Digest{}, fmt.Errorf("%s, asked of the asset service: %s", site(url), said.Message())
	}
	switch hash := answer.GetBlobDigest().GetHash(); {
	case sum != "" && hash != sum:
		return exec.Digest{}, fmt.Errorf("%s: the asset service answered with content whose sha256 is %s, not %s", site(url), hash, sum)
	case !named.MatchString(hash) || answer.GetBlobDigest().GetSizeBytes() < 0:
		return exec.Digest{}, fmt.Errorf("%s: the asset service answered with %q, which names no content", site(url), hash)
	}
	digest := exec.Digest{Hash: answer.GetBlobDigest().GetHash(), Size: answer.GetBlobDigest().GetSizeBytes()}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.forgotten != forgotten {
		return digest, nil
	}
	if c.answered == nil {
		c.answered = map[string]exec.Digest{}
	}
	c.answered[key] = digest
	return digest, nil
}

// proxied returns the address a URL is asked of the service by: the
// proxy's for it, by the first rule that matches it, or, where no rule
// does, the URL itself where it is a public site's (public). A URL that
// carries a user is refused before any rule is tried: a proxy is no
// place for a user's password. A URL that is refused is not repeated: it
// may hold a user's password, or a token among what it asks for.
func (c *Client) proxied(url string) (string, error) {
	if parsed, err := neturl.Parse(url); err == nil && parsed.User != nil {
		return "", fmt.Errorf("%s: it carries a user, and is neither proxied nor downloaded as it is", site(url))
	}
	for _, rule := range c.rewrites {
		if rule.from.MatchString(url) {
			return rule.from.ReplaceAllString(url, rule.to), nil
		}
	}
	if err := public(url); err != nil {
		return "", fmt.Errorf("%s: no proxy of the grid's stands in for it, and it is not downloaded as it is: %w", site(url), err)
	}
	return url, nil
}

// site is how a download is said in an error: by its scheme and host
// where the host is a public site's name, else as "a download". The rest
// of its URL is never said, whether or not a rule proxies it: a user's
// password, or a token among what it asks for, may be there.
func site(raw string) string {
	parsed, err := neturl.Parse(raw)
	if err != nil || parsed.Scheme == "" || publicHost(parsed.Hostname()) != nil {
		return "a download"
	}
	return parsed.Scheme + "://" + strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
}

// public returns why a URL is no public site's, which is asked as it is:
// one over https, to a host named, which is neither an address nor this
// machine, with no user's name or password.
func public(raw string) error {
	parsed, err := neturl.Parse(raw)
	switch {
	case err != nil:
		return fmt.Errorf("it is no URL")
	case parsed.Scheme != "https":
		return fmt.Errorf("it is not https")
	case parsed.User != nil:
		return fmt.Errorf("it carries a user")
	}
	return publicHost(parsed.Hostname())
}

// publicHost returns why a URL's host is no public site's name.
func publicHost(hostname string) error {
	host := strings.ToLower(strings.TrimSuffix(hostname, "."))
	// A name of anything but plain letters, digits, dots and dashes is
	// another name once the client reads it (IDNA: a full-width dot is a
	// dot), and is not checked as it will be reached.
	for _, r := range host {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
			return fmt.Errorf("its host is no plain name")
		}
	}
	if _, err := netip.ParseAddr(host); err == nil || host == "" || !strings.Contains(host, ".") || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") {
		return fmt.Errorf("its host is no public site's name")
	}
	return nil
}

// Unpacking is the command of a fetch that downloads: for each archive
// and the directory it goes into, both relative to where the command
// runs, it makes the directory and unpacks the archive there, as tar -xz
// does, never over a file that is there already (-k): what the fetch was
// given beside the archive, such as the checksum cargo reads, is never
// replaced by what the archive holds. Nothing of what was downloaded is
// put into the command's text: each is an argument, and the text is the
// same for every such fetch.
func Unpacking(archivesAndDirectories ...string) []string {
	return append([]string{"/bin/sh", "-c", unpack, "sh"}, archivesAndDirectories...)
}

// Cutting is the command of a fetch that keeps parts of what it
// downloaded: for each part, in sevens, what was downloaded, where the
// part starts in it and how long it is, the checksum's kind ("sha1" or
// "sha256") and its hex, the file the part is kept as, and the public
// address it came from, to say so by. Every part is checked first, and
// only where each is what its checksum says is any kept: at the first
// that is not, the command fails, says which, and leaves nothing. As with
// Unpacking, each is an argument and the text is the same for every such
// fetch.
func Cutting(parts ...string) []string {
	return append([]string{"/bin/sh", "-c", cut, "sh"}, parts...)
}

// cut is the text of Cutting: $1 what was downloaded, $2 the offset, $3
// the length, $4 the kind of checksum, $5 the checksum, $6 the file, $7
// the address. The first pass checks each part and puts its seven
// arguments back at the end, so that the second finds them as they were.
const cut = `set -e
n=$#
while [ $n -ge 7 ]; do
  said=$(tail -c "+$(($2 + 1))" "$1" | head -c "$3" | "$4sum")
  said=${said%% *}
  if [ "$said" != "$5" ]; then
    echo "$7: the $3 bytes at $2 have the $4 $said, and the lock holds $5" >&2
    exit 1
  fi
  set -- "$@" "$1" "$2" "$3" "$4" "$5" "$6" "$7"
  shift 7
  n=$((n - 7))
done
while [ $# -ge 7 ]; do
  mkdir -p "$(dirname "$6")"
  tail -c "+$(($2 + 1))" "$1" | head -c "$3" > "$6"
  shift 7
done`

// unpack is the text of Unpacking: $1 an archive, $2 where it goes, and
// so on in pairs.
const unpack = `set -e
while [ $# -ge 2 ]; do
  mkdir -p "$2"
  tar -xzkf "$1" -C "$2"
  shift 2
done`
