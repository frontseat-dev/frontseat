package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/core/ports"
	"github.com/frontseat-dev/frontseat/stack/testsupport/testbox"
	"github.com/frontseat-dev/frontseat/testsupport/testrequired"
)

// The local stack serves the portal (portal/): its backend says it is
// ready, where core/ports says, and serves the app, the page its build
// titled, and whoever reaches it reads the catalog and changes none of it.
// The control: the same stack with the portal's service brought down is
// not ready, so what answered was the portal and nothing else.
func TestTheStackServesThePortal(t *testing.T) {
	testrequired.Stack(t)
	own := testbox.Stack(t)
	plain := testbox.Plain(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	ready := "/.backstage/health/v1/readiness"
	var status string
	for deadline := time.Now().Add(5 * time.Minute); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		if status, _ = fetched(ctx, own, plain, ready); status == "200" {
			break
		}
	}
	if status != "200" {
		logs, _ := exec.Command("docker", "logs", "--tail", "80", own).CombinedOutput()
		t.Fatalf("the portal answered %s %q, want 200\n%s", ready, status, logs)
	}
	status, page := fetched(ctx, own, plain, "/")
	if status != "200" || !strings.Contains(string(page), "<title>Scaffolded Backstage App</title>") {
		t.Errorf("the portal served its app %q, %.300q; want 200 and the page its build titled", status, page)
	}
	// Whoever reaches it is a guest, who reads the catalog and changes
	// none of it: no location is registered, analysed or fetched, no
	// entity deleted or refreshed, and no scaffolder runs a task. The
	// control is the read, which the same guest is answered.
	status, minted := fetched(ctx, own, plain, "/api/auth/guest/refresh", "-H", "X-Requested-With: XMLHttpRequest")
	var session struct {
		BackstageIdentity struct{ Token string } `json:"backstageIdentity"`
	}
	if err := json.Unmarshal(minted, &session); status != "200" || err != nil || session.BackstageIdentity.Token == "" {
		t.Fatalf("a guest was not signed in: %q %v %.300q", status, err, minted)
	}
	bearer := []string{"-H", "Authorization: Bearer " + session.BackstageIdentity.Token}
	// The catalog takes in its locations after it is ready: what it read
	// of them is waited for.
	var read []byte
	var entities []struct {
		Kind     string `json:"kind"`
		Metadata struct {
			UID       string `json:"uid"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	var err error
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		status, read = fetched(ctx, own, plain, "/api/catalog/entities?limit=1", bearer...)
		if err = json.Unmarshal(read, &entities); status == "200" && err == nil && len(entities) > 0 {
			break
		}
	}
	if status != "200" || err != nil || len(entities) == 0 || entities[0].Metadata.UID == "" {
		t.Fatalf("a guest reading the catalog was answered %q, %v, %.300q; want 200 and an entity", status, err, read)
	}
	// What a read-only catalog still lets a caller do is what the
	// permission policy refuses: deleting an entity, refreshing one, and
	// having the portal fetch a location it is named.
	if status, _ := fetched(ctx, own, plain, "/api/catalog/entities/by-uid/"+entities[0].Metadata.UID, append(bearer, "-X", "DELETE")...); status != "403" {
		t.Errorf("a guest deleting an entity was answered %q, want 403", status)
	}
	posted := func(path, body string) string {
		status, _ := fetched(ctx, own, plain, path, append(bearer, "-X", "POST", "-H", "Content-Type: application/json", "-d", body)...)
		return status
	}
	location := `{"type":"url","target":"https://github.com/backstage/backstage/blob/master/catalog-info.yaml"}`
	refreshed := fmt.Sprintf(`{"entityRef":%q}`, strings.ToLower(entities[0].Kind)+":"+entities[0].Metadata.Namespace+"/"+entities[0].Metadata.Name)
	for path, body := range map[string]string{
		"/api/catalog/locations":             location,
		"/api/catalog/locations?dryRun=true": location,
		"/api/catalog/analyze-location":      `{"location":` + location + `}`,
		"/api/catalog/refresh":               refreshed,
	} {
		if status := posted(path, body); status != "403" {
			t.Errorf("a guest's POST of %s was answered %q, want 403", path, status)
		}
	}
	// A scaffolder lists the actions its tasks run, to whoever may start
	// one: a portal that serves none answers no such path.
	if status, _ := fetched(ctx, own, plain, "/api/scaffolder/v2/actions", bearer...); status != "404" {
		t.Errorf("a guest asking for the scaffolder's actions was answered %q, want 404: the portal serves no scaffolder", status)
	}
	// The control: the portal's service brought down, nothing answers.
	if out, err := exec.CommandContext(ctx, "docker", "exec", own, "/command/s6-svc", "-wd", "-T", "60000", "-d", "/run/service/portal").CombinedOutput(); err != nil {
		t.Fatalf("bringing the portal down: %v\n%s", err, out)
	}
	if status, _ := fetched(ctx, own, plain, ready); status == "200" {
		t.Errorf("with the portal down, %s answered 200: something else answers there", ready)
	}
}

// fetched asks the portal of a stack's container for a path, with what
// else curl is told (options), as the control plane is asked (asked), and
// returns the status it answered
// with and what it answered; 000 where nothing answered within a
// minute.
func fetched(ctx context.Context, own, plain, path string, options ...string) (status string, answer []byte) {
	arguments := append([]string{"run", "--rm", "--pull=never", "--network", "container:" + own, "--entrypoint", "curl", plain,
		"-s", "--max-time", "60", "-w", "\n%{http_code}"}, options...)
	out, _ := exec.CommandContext(ctx, "docker", append(arguments, "http://127.0.0.1:"+strconv.Itoa(ports.Portal)+path)...).Output()
	return curled(out)
}
