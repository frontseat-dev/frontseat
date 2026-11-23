package openshell_test

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/ai-openshell/sdk-go/openshell/v1"
	"github.com/frontseat-dev/frontseat/local/testsupport/testgateway"
	"github.com/frontseat-dev/frontseat/sandbox/driver"
	"github.com/frontseat-dev/frontseat/sandbox/driver/openshell"
	"github.com/frontseat-dev/frontseat/sandbox/testsupport/drivertest"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
	"github.com/frontseat-dev/frontseat/testsupport/testrun"
)

// The most of a supervisor's log a failed test is given: its last lines,
// each cut to a length a reader can take in.
const (
	keptLines   = 200
	keptLineLen = 400
)

// running has the driver make a sandbox of the test's own (drivertest.Running)
// and, where the test fails, puts what the sandbox's supervisor logged in
// the test's output before the sandbox is ended: its proxy says there why
// it let a request through or did not, and that is gone with the sandbox.
func running(t *testing.T, d *openshell.Driver, record driver.Sandbox) {
	testrequired.Stack(t)
	t.Helper()
	gateway := gatewayClient(t)
	drivertest.Running(t, d, record, func() {
		if !t.Failed() {
			return
		}
		ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		logged, err := supervisorLog(ctx, gateway, record.ID)
		if err != nil {
			t.Logf("the supervisor of %s could not be read: %v", record.ID, err)
			return
		}
		t.Logf("what the supervisor of %s logged, its last %d lines:\n%s", record.ID, keptLines, logged)
	})
}

// supervisorLog is the end of what the supervisor of the sandbox of an id
// logged (containersOf), with whatever reads as a secret taken out.
func supervisorLog(ctx context.Context, gateway *sdk.Client, id string) (string, error) {
	names, err := containersOf(ctx, gateway, id)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, name := range names {
		if !supervisor(name) {
			continue
		}
		logged, err := testgateway.Runtime(ctx, "logs", "--timestamps", "--tail", strconv.Itoa(keptLines), "--", name)
		if err != nil {
			return "", err
		}
		out.WriteString(redacted(logged))
	}
	return out.String(), nil
}

// supervisor says whether a container the gateway runs a sandbox in is
// its supervisor's, as either of its drivers names one: Docker's
// <sandbox>-supervisor, Podman's openshell-supervisor-<id>.
func supervisor(name string) bool {
	return strings.HasSuffix(name, "-supervisor") || strings.HasPrefix(name, "openshell-supervisor-")
}

// containersOf names the containers the gateway runs the sandbox of an id
// in: the sandbox is found as the driver finds it, by its id's label at
// the gateway, and its containers by the gateway's id of it, which the
// container runtime labels them with; never by a name, which another's
// sandbox may share.
func containersOf(ctx context.Context, gateway *sdk.Client, id string) ([]string, error) {
	held, err := gateway.Sandboxes().ListAll(ctx, "default", sdk.ListOptions{LabelSelector: "frontseat.dev/sandbox=" + id})
	if err != nil {
		return nil, err
	}
	var names []string
	for _, sandbox := range held {
		if sandbox.Labels["frontseat.dev/sandbox"] != id || sandbox.ID == "" {
			continue
		}
		listed, err := testgateway.Runtime(ctx, "ps", "--all", "--filter", "label=openshell.ai/sandbox-id="+sandbox.ID, "--format", "{{.Names}}")
		if err != nil {
			return nil, err
		}
		names = append(names, strings.Fields(string(listed))...)
	}
	return names, nil
}

var (
	// The terminal's escapes, which say nothing in a test's output and may
	// hide a word from what follows: its colours and its commands.
	escapes = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b.?")
	// A credential presented by its scheme, anywhere it is written.
	presented = regexp.MustCompile(`(?i)\b(bearer|basic|token)\s+[A-Za-z0-9+/=._~-]{4,}`)
	// A value whose shape is a token's, by the prefix its issuer gives it.
	shaped = regexp.MustCompile(`\b(?:gh[pousr]|github_pat|glpat|xox[abp])[-_][A-Za-z0-9_-]+`)
	// A private key, to the end of its line.
	privateKey = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----.*$`)
	// A name that says it holds a secret, and its value: where a space
	// follows what sets it, to the end of the line; otherwise a quoted
	// string, a bracketed list, or what runs to the next separator.
	spaced = regexp.MustCompile(`(?i)([\w.-]*(?:token|secret|passw|passphrase|pwd|credential|key|session|auth|cookie)[\w.-]*)(["']?[ \t]*[:=][ \t]+|[ \t]+)\S.*$`)
	named  = regexp.MustCompile(`(?i)([\w.-]*(?:token|secret|passw|passphrase|pwd|credential|key|session|auth|cookie)[\w.-]*)(["']?[ \t]*[:=][ \t]*)("[^"]*"|'[^']*'|\[[^\]]*\]|[^\s&,;"'\]}]+)`)
	// Who is asked for and with what, before a host: with a scheme or not.
	userinfo = regexp.MustCompile(`(?:://[^/@\s]+|[\w.%+-]+:[^@\s/]+)@`)
)

// redacted is a log with every secret it spells out taken out, each line
// (a carriage return ends one too) cut to keptLineLen.
func redacted(logged []byte) string {
	var out strings.Builder
	lines := bufio.NewScanner(bytes.NewReader(bytes.ReplaceAll(logged, []byte("\r"), []byte("\n"))))
	lines.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for lines.Scan() {
		line := escapes.ReplaceAllString(lines.Text(), "")
		line = privateKey.ReplaceAllString(line, "[redacted]")
		line = presented.ReplaceAllString(line, "$1 [redacted]")
		line = shaped.ReplaceAllString(line, "[redacted]")
		line = userinfo.ReplaceAllStringFunc(line, func(found string) string {
			if strings.HasPrefix(found, "://") {
				return "://[redacted]@"
			}
			return "[redacted]@"
		})
		line = named.ReplaceAllString(line, "$1$2[redacted]")
		line = spaced.ReplaceAllString(line, "$1$2[redacted]")
		if len(line) > keptLineLen {
			line = line[:keptLineLen] + " …"
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

// What a failed test is told of its sandbox's supervisor is read from the
// supervisor of that sandbox, whatever name the gateway gave it; a sandbox
// that was never made has nothing to say.
func TestAFailedTestIsToldWhatItsSandboxsSupervisorLogged(t *testing.T) {
	testrequired.Stack(t)
	d := driven(t)
	gateway := gatewayClient(t)
	record := box(t, "frontseat-openshell-evidence")
	running(t, d, record)
	ctx := context.Background()
	// It carries the label of the run that made it, by which a person
	// finds what a run that crashed left.
	labelled, err := gateway.Sandboxes().ListAll(ctx, "default", sdk.ListOptions{LabelSelector: testrun.Label + "=" + testrun.This()})
	if err != nil || !slices.ContainsFunc(labelled, func(s *sdk.Sandbox) bool { return s.Labels["frontseat.dev/sandbox"] == record.ID }) {
		t.Errorf("the sandbox is not found by its run's label (%v)", err)
	}
	// Docker's driver's supervisor says it starts; Podman's says nothing
	// below a warning, and says that of its proxy as it starts.
	logged, err := supervisorLog(ctx, gateway, record.ID)
	if err != nil || (!strings.Contains(logged, "Starting sandbox supervision") && !strings.Contains(logged, "openshell_supervisor_network::proxy")) {
		t.Errorf("the supervisor's log is %q, %v; want its own", logged, err)
	}
	if never, err := supervisorLog(ctx, gateway, testrun.ID(t, "frontseat-openshell-never")); err != nil || never != "" {
		t.Errorf("a sandbox never made logged %q, %v; want nothing", never, err)
	}
}

// What reads as a secret is taken out of a log a test is given, in each
// way a review found one written, and what does not is left as it is.
func TestASecretInASupervisorsLogIsTakenOut(t *testing.T) {
	for line, secrets := range map[string][]string{
		"GET http://someone:" + password + "@forge/code":       {password},
		"header Authorization: Basic c29tZW9uZTpzZWNyZXQ=":     {"c29tZW9uZTpzZWNyZXQ="},
		"password=" + password + " sent":                       {password},
		`{"token":"abc.def"}`:                                  {"abc.def"},
		"X-Api-Token: Bearer abc.def.ghi":                      {"abc.def.ghi"},
		"upstream said: Bearer eyJhbGciOi.payload.sig ok":      {"eyJhbGciOi"},
		`{"token":"abc.def","access_token" : "zzz"}`:           {"abc.def", "zzz"},
		`{"Authorization":"Basic c29tZW9uZTpzZWNyZXQ="}`:       {"c29tZW9uZTpzZWNyZXQ="},
		"GET https://forge/code?access_token=ghp_AAAA&x=1":     {"ghp_AAAA"},
		"GET https://forge/code?private_token=glpat-AAAA":      {"glpat-AAAA"},
		"GET https://c29tZW9uZTpzZWNyZXQ=@forge/code":          {"c29tZW9uZTpzZWNyZXQ="},
		"CONNECT someone:hunter2@forge:443":                    {"hunter2"},
		"git credential: username=someone password=hunter2":    {"hunter2"},
		"https://x-access-token:ghs_abcdef@github.com/a/b":     {"ghs_abcdef"},
		"env GITHUB_TOKEN=ghp_AAAA GH_PAT=ghp_BBBB":            {"ghp_AAAA", "ghp_BBBB"},
		"AWS_SECRET_ACCESS_KEY=abcd/efgh":                      {"abcd/efgh"},
		"passphrase: hunter2":                                  {"hunter2"},
		"pwd=hunter2":                                          {"hunter2"},
		"password: \"hunter two\" tail":                        {"hunter"},
		"password = hunter two":                                {"hunter", "two"},
		"secret:\thunter2":                                     {"hunter2"},
		"apikey=AAAA api_key=BBBB api-key: CCCC":               {"AAAA", "BBBB", "CCCC"},
		"Authorization: Basic AAAA\rpassword=BBBB":             {"AAAA", "BBBB"},
		"ssh -----BEGIN OPENSSH PRIVATE KEY----- b3BlbnNza":    {"b3BlbnNza"},
		"credentials=AAAA  credential_id=BBBB":                 {"AAAA", "BBBB"},
		"client_secret=AAAA":                                   {"AAAA"},
		"password=[AAAA,BBBB]":                                 {"AAAA", "BBBB"},
		"mytoken=AAAA refresh-token=BBBB":                      {"AAAA", "BBBB"},
		"sessionid=AAAA; session=BBBB":                         {"AAAA", "BBBB"},
		strings.Repeat("x", 395) + " password=hunter2":         {"hunter"},
		"\x1b[31mpassword\x1b[0m=hunter2":                      {"hunter2"},
		"pass\x1b[0mword=hunter2":                              {"hunter2"},
		"\x1b]8;;http://u:p@h\x07link\x1b[1K password=hunter2": {"hunter2", "\x1b"},
	} {
		got := redacted([]byte(line))
		for _, secret := range secrets {
			if strings.Contains(got, secret) {
				t.Errorf("redacted(%q) = %q, which holds %q", line, got, secret)
			}
		}
	}
	// The control: what a supervisor logs of a request is kept as it is.
	for _, line := range []string{
		"2026-11-16T08:41:31.276Z OCSF HTTP:GET [INFO] ALLOWED GET http://host.docker.internal:54095/code/info/refs [policy:rule-3 engine:l7]",
		"2026-11-16T08:41:31.384Z OCSF NET:OPEN [MED] DENIED /usr/bin/curl(0) -> host.docker.internal:54095 [reason:transparent_tcp_policy_denied]",
		"OCSF CONFIG:PUBLISHED [INFO] Policy DNS mapped host.docker.internal resolved=127.0.0.1 synthetic=198.18.0.2 ports=8980,8983,54095",
	} {
		if got := strings.TrimSuffix(redacted([]byte(line)), "\n"); got != line {
			t.Errorf("redacted(%q) = %q, want it as it is", line, got)
		}
	}
}

// The sandboxes the driver's tests made that are still at the gateway,
// by the run that made them (testrun.Label), listed where a person
// asks with FRONTSEAT_TEST_LEFTOVERS=list, and those of one run ended
// where the person names it there: a run that crashed leaves its own, and
// nothing ends them, or another run's, by itself.
func TestTheSandboxesARunLeftAreListedWhenAsked(t *testing.T) {
	asked := os.Getenv("FRONTSEAT_TEST_LEFTOVERS")
	if asked == "" {
		t.Skip("FRONTSEAT_TEST_LEFTOVERS=list lists what the tests left; =<run> ends one run's")
	}
	d := driven(t)
	held, err := gatewayClient(t).Sandboxes().ListAll(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	for _, sandbox := range held {
		run, id := sandbox.Labels[testrun.Label], sandbox.Labels["frontseat.dev/sandbox"]
		if run == "" || id == "" {
			continue
		}
		t.Logf("%s of the run %s, made %s", id, run, sandbox.CreatedAt.Format(time.RFC3339))
		if asked == run {
			if err := d.Delete(context.Background(), id); err != nil {
				t.Errorf("ending %s: %v", id, err)
			}
		}
	}
}
