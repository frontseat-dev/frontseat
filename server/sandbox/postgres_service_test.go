package sandbox_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	sandboxv1 "github.com/frontseat-dev/frontseat/server/sandbox/api/frontseat/sandbox/v1alpha1"
	"github.com/frontseat-dev/frontseat/server/testsupport/testrequired"
	"google.golang.org/protobuf/types/known/durationpb"
)

// A real PostgreSQL proof below the build path. These fixtures exercise
// native clients in separate consumer sandboxes, not a served provider API.
func TestSharedPostgresAllocationsStaySeparate(t *testing.T) {
	p, pg, a, b, ca, cb := twoConsumers(t)
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for _, item := range []struct{ consumer, value string }{{ca, "first"}, {cb, "second"}} {
		workers.Go(func() {
			code, out, err := p.exec(context.Background(), item.consumer, "sql", "", "CREATE TABLE same_name(value text); INSERT INTO same_name VALUES ('"+item.value+"'); SELECT value FROM same_name;")
			if err != nil || code != 0 || strings.TrimSpace(out) != item.value {
				results <- fmt.Errorf("isolated SQL: exit %d: %v: %s", code, err, out)
			}
		})
	}
	workers.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}
	pg.expect(ca, "", "SELECT current_user, current_database();", a.name+"|"+a.name)
	pg.expect(cb, "", "SELECT current_user, current_database();", b.name+"|"+b.name)
	pg.expect(ca, "", "SELECT value FROM same_name;", "first")
	pg.expect(cb, "", "SELECT value FROM same_name;", "second")
	t.Log("two concurrent native SQL clients have independent same-name tables")

	for _, attempt := range []struct{ consumer, database string }{{ca, b.name}, {cb, a.name}, {ca, "postgres"}, {ca, "template1"}} {
		code, out := p.run(attempt.consumer, "sql", "dbname="+attempt.database, "SELECT 1;")
		if code == 0 || !strings.Contains(out, "permission denied for database") {
			t.Errorf("connection outside the allocation was not refused for database permissions: exit %d: %s", code, out)
		}
	}
	if code, out := p.run(ca, "sql", "password=deliberately-wrong", "SELECT 1;"); code == 0 || !strings.Contains(out, "password authentication failed") {
		t.Errorf("wrong password was not refused by authentication: exit %d: %s", code, out)
	}
	if code, out := p.run(ca, "sql", "user=postgres dbname=postgres", "SELECT 1;"); code == 0 || !strings.Contains(out, "pg_hba.conf rejects connection") {
		t.Errorf("the service's administration identity was not refused by HBA: exit %d: %s", code, out)
	}
	for _, statement := range []string{
		"CREATE DATABASE forbidden;", "CREATE ROLE forbidden;", "SET ROLE postgres;",
		"COPY (SELECT 'control') TO PROGRAM 'true';",
		"ALTER DATABASE " + a.name + " ALLOW_CONNECTIONS false;",
	} {
		code, out := p.run(ca, "sql", "", statement)
		if code == 0 || !strings.Contains(out, "42501") {
			t.Errorf("administrative SQL was not refused for insufficient privilege: exit %d: %s", code, out)
		}
	}
	// GRANT can succeed with a warning and change nothing; check the resulting
	// authority, not only the command's status. Database ownership stays with
	// the provider so the consumer cannot weaken its connection boundary.
	if code, out := p.run(ca, "sql", "", "GRANT CONNECT ON DATABASE "+a.name+" TO PUBLIC;"); code != 0 {
		t.Fatalf("grant probe: %s", out)
	}
	pg.expect(ca, "", "SELECT has_database_privilege('"+b.name+"', '"+a.name+"', 'CONNECT');", "f")
	pg.admin("COPY (SELECT 'control') TO PROGRAM 'true';")
	pg.expect(cb, "", "SELECT value FROM same_name;", "second")
	if !t.Failed() {
		t.Log("own-access controls passed; cross-database access, wrong password and administrator operations refused")
	}
}

func TestReleasingAPostgresAllocationEndsItsSessionsAndARetryStartsFresh(t *testing.T) {
	testrequired.Stack(t)
	p, pg, a, _, ca, cb := twoConsumers(t)
	pg.expect(ca, "", "CREATE TABLE attempt_state(value int); INSERT INTO attempt_state VALUES (1); SELECT count(*) FROM attempt_state;", "1")
	pg.expect(cb, "", "SELECT 42;", "42")
	active := make(chan sqlResult, 1)
	go func() {
		code, out, err := p.exec(context.Background(), ca, "sql", "application_name=held_session", "SELECT pg_sleep(60);")
		active <- sqlResult{code, out, err}
	}()
	// Always join this caller, even if a readiness or release assertion fails.
	t.Cleanup(func() {
		select {
		case <-active:
		case <-time.After(25 * time.Second):
			t.Error("native SQL caller did not finish")
		}
	})
	pg.waitSQL("SELECT count(*) FROM pg_stat_activity WHERE usename='"+a.name+"' AND application_name='held_session' AND state='active';", "1")
	pg.admin("ALTER ROLE " + a.name + " VALID UNTIL '2000-01-01T00:00:00Z';")
	if code, out := p.run(ca, "sql", "", "SELECT 1;"); code == 0 || !strings.Contains(out, "password authentication failed") {
		t.Fatalf("expired password was not refused by authentication: exit %d: %s", code, out)
	}
	pg.expectAdmin("SELECT count(*) FROM pg_stat_activity WHERE usename='"+a.name+"' AND application_name='held_session' AND state='active';", "1")
	pg.release(a)
	select {
	case result := <-active:
		active <- result // cleanup still joins the already-finished caller
		if result.err != nil || result.code == 0 || !strings.Contains(result.output, "57P01") {
			t.Fatalf("live session was not terminated by cleanup: exit %d: %v: %s", result.code, result.err, result.output)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("release left the active session running")
	}
	pg.expectAdmin("SELECT count(*) FROM pg_roles WHERE rolname='"+a.name+"';", "0")
	pg.expectAdmin("SELECT count(*) FROM pg_database WHERE datname='"+a.name+"';", "0")
	if code, out := p.run(ca, "sql", "", "SELECT 1;"); code == 0 || !strings.Contains(out, "password authentication failed") {
		t.Fatalf("the old credential still connects or failed for another reason: exit %d: %s", code, out)
	}
	pg.expect(cb, "", "SELECT 42;", "42")
	retry := pg.allocate()
	if retry.name == a.name || retry.password == a.password {
		t.Fatal("a repeated execution reused an allocation or password")
	}
	cr := pg.consumer(retry)
	pg.expect(cr, "", "SELECT to_regclass('attempt_state') IS NULL;", "t")
	pg.release(a) // repeated cleanup must not delete another allocation
	pg.expect(cb, "", "SELECT 42;", "42")
	t.Log("release ended a live session and revoked its credential; a fresh allocation and the neighbour both work")
}

func TestAPostgresAllocationIsClosedWhileItsGrantsArePrepared(t *testing.T) {
	testrequired.Stack(t)
	p := newServiceRoutes(t)
	pg := newPostgres(p)
	first := pg.consumer(pg.allocate())
	pg.expect(first, "", "SELECT 41;", "41")
	next := pg.allocateObserved(func(a pgAllocation) {
		// Allocation is paused after CREATE DATABASE and before its grants.
		// An already-running consumer has both a live route and valid credentials.
		pg.expectAdmin("SELECT count(*) FROM pg_database WHERE datname='"+a.name+"';", "1")
		code, out := p.run(first, "sql", "dbname="+a.name, "SELECT 1;")
		if code == 0 || !strings.Contains(out, "not currently accepting connections") {
			t.Errorf("unfinished allocation was not closed to connections: exit %d: %s", code, out)
		}
	})
	pg.expect(pg.consumer(next), "", "SELECT 42;", "42")
	if code, out := p.run(first, "sql", "dbname="+next.name, "SELECT 1;"); code == 0 || !strings.Contains(out, "permission denied for database") {
		t.Fatalf("finished allocation was not restricted to its consumer: exit %d: %s", code, out)
	}
	pg.expect(first, "", "SELECT 41;", "41")
}

func TestADedicatedPostgresInstanceIsSeparate(t *testing.T) {
	testrequired.Stack(t)
	p := newServiceRoutes(t)
	shared, dedicated := newPostgres(p), newPostgres(p)
	sharedID := shared.admin("SELECT system_identifier FROM pg_control_system();")
	dedicatedID := dedicated.admin("SELECT system_identifier FROM pg_control_system();")
	if shared.id == dedicated.id || sharedID == dedicatedID {
		t.Fatal("dedicated allocation reused the shared instance")
	}
	cs, cd := shared.consumer(shared.allocate()), dedicated.consumer(dedicated.allocate())
	shared.expect(cs, "", "SELECT 1;", "1")
	dedicated.expect(cd, "", "SELECT 2;", "2")
	if _, err := p.client.DeleteSandbox(context.Background(), &sandboxv1.DeleteSandboxRequest{SandboxId: dedicated.id}); err != nil {
		t.Fatal(err)
	}
	shared.expect(cs, "", "SELECT 1;", "1")
	t.Log("separate PostgreSQL cluster identities; ending the dedicated instance left shared consumers working")
}

type sqlResult struct {
	code   int32
	output string
	err    error
}

type pgAllocation struct{ name, password string }

type postgres struct {
	p         *serviceRoutes
	id, route string
	owned     map[string]pgAllocation
}

// twoConsumers starts a PostgreSQL with two allocations, each given a
// consumer sandbox of its own, and returns them with the consumers.
func twoConsumers(t *testing.T) (p *serviceRoutes, pg *postgres, a, b pgAllocation, ca, cb string) {
	testrequired.Stack(t)
	t.Helper()
	p = newServiceRoutes(t)
	pg = newPostgres(p)
	a, b = pg.allocate(), pg.allocate()
	ca, cb = pg.consumer(a), pg.consumer(b)
	return p, pg, a, b, ca, cb
}

func newPostgres(p *serviceRoutes) *postgres {
	p.t.Helper()
	pg := &postgres{p: p, route: p.route(), owned: make(map[string]pgAllocation)}
	spec := p.spec([]string{"postgres", "unused", "unused"}, pg.route, false)
	spec.Arguments[1] = "postgres"
	pg.id = p.create(spec)
	pg.waitSQL("SELECT 1;", "1")
	pg.expectAdmin("SELECT current_setting('server_version_num')::int BETWEEN 170000 AND 179999;", "t")
	pg.admin("REVOKE CONNECT, TEMPORARY ON DATABASE postgres, template1 FROM PUBLIC;")
	return pg
}

func (pg *postgres) allocate() pgAllocation {
	return pg.allocateObserved(nil)
}

func (pg *postgres) allocateObserved(afterCreate func(pgAllocation)) pgAllocation {
	pg.p.t.Helper()
	// Only private, random identifiers generated here reach these SQL strings;
	// this fixture accepts no catalog-provided name or executable provisioner.
	a := pgAllocation{name: "alloc_" + unique(), password: unique() + unique()}
	pg.owned[a.name] = a
	pg.admin("CREATE ROLE " + a.name + " LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 4 PASSWORD '" + a.password + "' VALID UNTIL '" + time.Now().UTC().Add(10*time.Minute).Format(time.RFC3339) + "';")
	pg.admin("CREATE DATABASE " + a.name + " OWNER postgres TEMPLATE template0 ALLOW_CONNECTIONS false;")
	if afterCreate != nil {
		afterCreate(a)
	}
	pg.admin("REVOKE CONNECT, TEMPORARY ON DATABASE " + a.name + " FROM PUBLIC;")
	pg.admin("GRANT CONNECT, TEMPORARY ON DATABASE " + a.name + " TO " + a.name + ";")
	pg.admin("ALTER DATABASE " + a.name + " ALLOW_CONNECTIONS true;")
	pg.adminAt(a.name, "GRANT USAGE, CREATE ON SCHEMA public TO "+a.name+";")
	return a
}

func (pg *postgres) consumer(a pgAllocation) string {
	pg.p.t.Helper()
	spec := pg.p.spec([]string{"proxy", "/route/service.sock", "127.0.0.1:7000"}, pg.route, true)
	spec.Environment = a.environment()
	id := pg.p.create(spec)
	pg.p.waitFor(id, []string{"sql", "", "SELECT 1;"})
	return id
}

func (pg *postgres) release(a pgAllocation) {
	pg.p.t.Helper()
	owned, ok := pg.owned[a.name]
	if !ok {
		return
	}
	if owned != a {
		pg.p.t.Fatal("allocation is not owned by this probe")
	}
	pg.admin("ALTER ROLE " + a.name + " NOLOGIN; ALTER DATABASE " + a.name + " ALLOW_CONNECTIONS false;")
	pg.expectAdmin("SELECT COALESCE(bool_and(pg_terminate_backend(pid, 5000)), true) FROM pg_stat_activity WHERE usename='"+a.name+"' OR datname='"+a.name+"';", "t")
	pg.expectAdmin("SELECT count(*) FROM pg_stat_activity WHERE usename='"+a.name+"' OR datname='"+a.name+"';", "0")
	pg.admin("DROP DATABASE " + a.name + " WITH (FORCE);")
	pg.admin("DROP ROLE " + a.name + ";")
	delete(pg.owned, a.name)
}

func (pg *postgres) adminSQL(database, statement string) (int32, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	response, err := pg.p.client.ExecSandbox(ctx, &sandboxv1.ExecSandboxRequest{
		SandboxId: pg.id, Arguments: []string{"gosu", "postgres", "/probe/actor", "sql", "host=/tmp user=postgres dbname=" + database, statement},
		Timeout: durationpb.New(15 * time.Second),
	})
	if err != nil {
		return 0, "", err
	}
	return response.GetExitCode(), strings.TrimSpace(string(response.GetOutput())), nil
}

func (pg *postgres) admin(statement string) string { return pg.adminAt("postgres", statement) }

func (pg *postgres) adminAt(database, statement string) string {
	pg.p.t.Helper()
	code, out, err := pg.adminSQL(database, statement)
	if err != nil || code != 0 {
		pg.p.t.Fatalf("provider SQL: exit %d: %v: %s", code, err, out)
	}
	return out
}

func (pg *postgres) expectAdmin(statement, want string) {
	pg.p.t.Helper()
	if got := pg.admin(statement); got != want {
		pg.p.t.Fatalf("provider result: got %q, want %q", got, want)
	}
}

func (pg *postgres) expect(consumer, connection, statement, want string) {
	pg.p.t.Helper()
	code, out := pg.p.run(consumer, "sql", connection, statement)
	if code != 0 || strings.TrimSpace(out) != want {
		pg.p.t.Fatalf("consumer result: exit %d: got %q, want %q", code, out, want)
	}
}

func (pg *postgres) waitSQL(statement, want string) {
	pg.p.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		code, out, err := pg.adminSQL("postgres", statement)
		if err == nil && code == 0 && out == want {
			return
		}
		if time.Now().After(deadline) {
			pg.p.t.Fatalf("provider readiness: exit %d: %v: %s", code, err, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
