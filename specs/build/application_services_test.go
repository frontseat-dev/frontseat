package build_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frontseat-dev/frontseat/testsupport/testpins"

	. "github.com/onsi/gomega"
)

// API relations select application providers; their service programs are built
// on the grid. Infrastructure bindings belong to the provider that names them.
// Readiness precedes dependent startup, and the test can reach only its API.
// Application test services / A consumed API starts its built provider after its own Resource is ready.
func TestAConsumedAPIStartsItsBuiltProviderAfterItsOwnResourceIsReady(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.applicationServiceWorkspace()

	build := workspace.build(of("//svc:go.test/."))

	spec.Expect(build.says()).To(BeEmpty())
	spec.Expect(build.outcomeOf("//svc:go.test/.")).To(Equal("passed"), "%s\n%s\n%s", build.says(), build.saysOf("//svc:go.test/."), build.printed("//svc:go.test/."))
	spec.Expect(build.cameFrom("//svc:go.test/.")).To(Equal("executed"))
	spec.Expect(build.printed("//svc:go.test/.")).To(ContainSubstring("provider-version=one"))
	spec.Expect(workspace.build(of("//svc:go.test/.")).cameFrom("//svc:go.test/.")).NotTo(Equal("executed"))
	program, err := os.ReadFile(filepath.Join(workspace.directory, "orders/main.go"))
	spec.Expect(err).NotTo(HaveOccurred())
	spec.Expect(string(program)).To(ContainSubstring(`"X-Provider-Version", "one"`))
	workspace.write("orders/main.go", strings.Replace(string(program), `"X-Provider-Version", "one"`, `"X-Provider-Version", "two"`, 1))
	changed := workspace.build(of("//svc:go.test/."))
	spec.Expect(changed.outcomeOf("//svc:go.test/.")).To(Equal("passed"), "%s\n%s", changed.saysOf("//svc:go.test/."), changed.printed("//svc:go.test/."))
	spec.Expect(changed.cameFrom("//svc:go.test/.")).To(Equal("executed"))
	spec.Expect(changed.printed("//svc:go.test/.")).To(ContainSubstring("provider-version=two"))
}

// Application test services / A pinned mise program serves an API through another provider and keys its runtime configuration.
func TestAPinnedMiseProgramServesAnAPIThroughAnotherProviderAndKeysItsRuntimeConfiguration(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.miseServiceWorkspace()
	label := "//svc:go.test/."
	first := workspace.build(of(label))
	spec.Expect(first.says()).To(BeEmpty())
	spec.Expect(first.outcomeOf(label)).To(Equal("passed"), "%s\n%s", first.saysOf(label), first.printed(label))
	spec.Expect(first.cameFrom(label)).To(Equal("executed"))
	spec.Expect(first.printed(label)).To(ContainSubstring("proxy-version=one"))
	spec.Expect(workspace.build(of(label)).cameFrom(label)).NotTo(Equal("executed"))
	workspace.write("proxy/caddy.json", strings.Replace(caddyRuntimeConfiguration, "one", "two", 1))
	changed := workspace.build(of(label))
	spec.Expect(changed.outcomeOf(label)).To(Equal("passed"), "%s\n%s", changed.saysOf(label), changed.printed(label))
	spec.Expect(changed.cameFrom(label)).To(Equal("executed"))
	spec.Expect(changed.printed(label)).To(ContainSubstring("proxy-version=two"))
	spec.Expect(changed.action(label)).NotTo(Equal(first.action(label)))
}

// Application test services / Two Resources selecting one program keep separate instances and their own dependencies.
func TestTwoResourcesSelectingOneProgramKeepSeparateInstancesAndTheirOwnDependencies(t *testing.T) {
	t.Parallel()
	spec := newScenario(t)

	workspace := spec.applicationServiceWorkspace()
	second := strings.Replace(greeterResource(greeterService), "name: greeter", "name: second-greeter", 1)
	second = strings.Replace(second, "echo, hello", "echo, second", 1)
	resources := greeterResource(greeterService) + "---\n" + second
	for _, item := range []struct{ name, dependency, export, body string }{{"first", "greeter", "ORDERS_URL", "hello"}, {"second", "second-greeter", "SECOND_URL", "second"}} {
		resources += `---
apiVersion: backstage.io/v1alpha1
kind: Resource
metadata: {name: ` + item.name + `}
spec:
  type: test-application
  owner: team
  dependsOn: [resource:default/` + item.dependency + `]
  frontseat:
    service:
      program: "//orders:go.run/orders"
      ports: {http: 8080}
      ready: [{http: {port: http, path: /, body: ` + item.body + `}}]
      exports: {` + item.export + `: "http://{host}:{port.http}"}
`
	}
	workspace.write("catalog-info.yaml", greeterComponent(false)+"  dependsOn: [resource:default/first, resource:default/second]\n---\n"+resources)
	workspace.write("svc/second_test.go", `package svc
import("io";"net/http";"os";"strings";"testing";"time")
func TestSecond(t *testing.T){
  if os.Getenv("SECOND_URL")==os.Getenv("ORDERS_URL"){t.Fatal("two resources share one instance")}
  client:=&http.Client{Timeout:5*time.Second}
  response,err:=client.Get(os.Getenv("SECOND_URL"));if err!=nil{t.Fatal(err)};defer response.Body.Close()
  body,err:=io.ReadAll(response.Body);if err!=nil||strings.TrimSpace(string(body))!="second"{t.Fatalf("resource inherited the program owner's dependency: %q %v",body,err)}
}
`)
	build := workspace.build(of("//svc:go.test/."))
	spec.Expect(build.says()).To(BeEmpty())
	spec.Expect(build.outcomeOf("//svc:go.test/.")).To(Equal("passed"), "%s\n%s", build.saysOf("//svc:go.test/."), build.printed("//svc:go.test/."))
	spec.Expect(build.cameFrom("//svc:go.test/.")).To(Equal("executed"))

}

const caddyRuntimeConfiguration = `{
  "admin":{"disabled":true,"config":{"persist":false}},
  "apps":{"http":{"servers":{"proxy":{
    "listen":["127.0.0.1:8080"],"automatic_https":{"disable":true},
    "routes":[{"handle":[
      {"handler":"headers","response":{"set":{"X-Proxy-Version":["one"]}}},
      {"handler":"reverse_proxy","upstreams":[{"dial":"{env.ORDERS_ADDR}"}]}
    ]}]
  }}}}
}`

func (spec *scenario) miseServiceWorkspace() *Workspace {
	spec.Helper()
	w := spec.applicationServiceWorkspace()
	testpins.Caddy(spec.TB, w.directory)
	w.write("catalog-info.yaml", greeterComponent(false)+"  consumesApis: [api:default/gateway]\n---\n"+greeterResource(greeterService))
	provider, err := os.ReadFile(filepath.Join(w.directory, "orders/catalog-info.yaml"))
	spec.Expect(err).NotTo(HaveOccurred())
	w.write("orders/catalog-info.yaml", strings.Replace(string(provider), `exports: {ORDERS_URL: "http://{host}:{port.http}"}`, `exports: {ORDERS_URL: "http://{host}:{port.http}", ORDERS_ADDR: "{host}:{port.http}"}`, 1))
	w.write("proxy/catalog-info.yaml", `apiVersion: backstage.io/v1alpha1
kind: Component
metadata: {name: proxy}
spec:
  type: service
  lifecycle: production
  owner: team
  providesApis: [api:default/gateway]
  consumesApis: [api:default/orders]
  frontseat:
    actions:
      serve:
        kind: program
        command: [caddy, run, --config, caddy.json]
        tools: [caddy]
        inputs: [caddy.json]
    service:
      program: ":custom.serve"
      ports: {http: 8080}
      ready: [{http: {port: http, path: /, body: hello}}]
      apis:
        api:default/gateway:
          port: http
          exports: {ORDERS_URL: "http://{host}:{port.http}"}
---
apiVersion: backstage.io/v1alpha1
kind: API
metadata: {name: gateway}
spec:
  type: openapi
  lifecycle: production
  owner: team
  definition: gateway
`)
	w.write("proxy/caddy.json", caddyRuntimeConfiguration)
	consumer, err := os.ReadFile(filepath.Join(w.directory, "svc/greeter_test.go"))
	spec.Expect(err).NotTo(HaveOccurred())
	w.write("svc/greeter_test.go", strings.Replace(string(consumer), "defer response.Body.Close()", "defer response.Body.Close()\n  println(\"proxy-version=\" + response.Header.Get(\"X-Proxy-Version\"))", 1))
	return w
}
