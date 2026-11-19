// Package backstagetest serves a fake Backstage for a test: the catalog
// API a control plane resolves entities through, over entities a test
// writes as catalog files write them. It answers what the control plane
// asks, by name and by query, a page at a time, with the relations
// Backstage derives from what the files say (dependsOn, partOf from a
// system, ownedBy from an owner), and to the bearer of its token alone.
package backstagetest

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"go.yaml.in/yaml/v4"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
)

// Served is a fake Backstage a test serves.
type Served struct {
	// URL is its base address, as an operator gives a control plane one:
	// the catalog API is beneath it, at /api/catalog.
	URL string
	// Token is what it answers the bearer of, and TokenFile a file that
	// holds it as an operator's would: a line, the file 0600 in a
	// directory 0700.
	Token, TokenFile string
	held             *catalog
	stop             func()
}

// Close stops serving: a Backstage that does not answer.
func (s Served) Close() { s.stop() }

// Set replaces the entities it holds with those documents write: what a
// Backstage holds once it has read its locations again.
func (s Served) Set(t testing.TB, documents string) {
	t.Helper()
	entities, err := read(documents)
	if err != nil {
		t.Fatal(err)
	}
	s.held.mu.Lock()
	s.held.entities = entities
	s.held.mu.Unlock()
}

// Asked is the paths and queries it was asked, in order.
func (s Served) Asked() []string {
	s.held.mu.Lock()
	defer s.held.mu.Unlock()
	return append([]string(nil), s.held.asked...)
}

// Serve serves the entities documents write, YAML documents as a catalog
// file holds them, until the test ends.
func Serve(t testing.TB, documents string) Served {
	t.Helper()
	served, close := Start(t, documents)
	t.Cleanup(close)
	return served
}

// Start serves as Serve does, until close is called; it keeps no test
// handle.
func Start(t testing.TB, documents string) (Served, func()) {
	t.Helper()
	entities, err := read(documents)
	if err != nil {
		t.Fatal(err)
	}
	var minted [16]byte
	if _, err := rand.Read(minted[:]); err != nil {
		t.Fatal(err)
	}
	token := "test-" + hex.EncodeToString(minted[:])
	directory, err := os.MkdirTemp("", "backstage-token-")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "token")
	if err := os.WriteFile(file, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	held := &catalog{entities: entities, token: token}
	server := httptest.NewServer(held)
	stop := func() {
		server.Close()
		_ = os.RemoveAll(directory)
	}
	return Served{URL: server.URL, Token: token, TokenFile: file, held: held, stop: stop}, stop
}

// read reads catalog documents into entities as the catalog API answers
// them: each with its namespace, and the relations Backstage derives.
func read(documents string) ([]map[string]any, error) {
	var out []map[string]any
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(documents)))
	for {
		var e map[string]any
		err := decoder.Decode(&e)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if e == nil {
			continue
		}
		metadata, _ := e["metadata"].(map[string]any)
		if metadata == nil {
			continue
		}
		namespace, _ := metadata["namespace"].(string)
		if namespace == "" {
			namespace = entity.NamespaceDefault
			metadata["namespace"] = namespace
		}
		spec, _ := e["spec"].(map[string]any)
		var relations []map[string]any
		relate := func(kind, written, defaultKind string) error {
			if !strings.Contains(written, ":") {
				written = defaultKind + ":" + written
			}
			target, err := entity.ParseIn(written, namespace)
			if err != nil {
				return err
			}
			relations = append(relations, map[string]any{"type": kind, "targetRef": target.String()})
			return nil
		}
		if on, ok := spec["dependsOn"].([]any); ok {
			for _, each := range on {
				written, _ := each.(string)
				if err := relate("dependsOn", written, entity.KindComponent); err != nil {
					return nil, err
				}
			}
		}
		if system, ok := spec["system"].(string); ok && system != "" {
			if err := relate("partOf", system, "system"); err != nil {
				return nil, err
			}
		}
		if owner, ok := spec["owner"].(string); ok && owner != "" {
			if err := relate("ownedBy", owner, "group"); err != nil {
				return nil, err
			}
		}
		e["relations"] = relations
		out = append(out, e)
	}
}

// catalog is what a fake Backstage holds, and what it was asked.
type catalog struct {
	token    string
	mu       sync.Mutex
	entities []map[string]any
	asked    []string
}

// refOf is an entity's reference as the catalog writes it.
func refOf(e map[string]any) string {
	kind, _ := e["kind"].(string)
	metadata, _ := e["metadata"].(map[string]any)
	namespace, _ := metadata["namespace"].(string)
	name, _ := metadata["name"].(string)
	return strings.ToLower(kind + ":" + namespace + "/" + name)
}

func (c *catalog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	presented, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(presented), []byte(c.token)) != 1 {
		http.Error(w, `{"error":{"name":"AuthenticationError"}}`, http.StatusUnauthorized)
		return
	}
	c.mu.Lock()
	c.asked = append(c.asked, r.URL.RequestURI())
	entities := c.entities
	c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if named, ok := strings.CutPrefix(r.URL.Path, "/api/catalog/entities/by-name/"); ok {
		parts := strings.Split(named, "/")
		if len(parts) == 3 {
			want := strings.ToLower(parts[0] + ":" + parts[1] + "/" + parts[2])
			for _, e := range entities {
				if refOf(e) == want {
					_ = json.NewEncoder(w).Encode(e)
					return
				}
			}
		}
		http.Error(w, `{"error":{"name":"NotFoundError"}}`, http.StatusNotFound)
		return
	}
	if r.URL.Path != "/api/catalog/entities/by-query" {
		http.Error(w, `{"error":{"name":"NotFoundError"}}`, http.StatusNotFound)
		return
	}
	// A cursor is where the page before ended, and carries the filter it
	// was asked by, as Backstage's does.
	filter, from := r.URL.Query().Get("filter"), 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		at, rest, _ := strings.Cut(cursor, ";")
		from, _ = strconv.Atoi(at)
		filter = rest
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 20
	}
	var kept []map[string]any
	for _, e := range entities {
		if matches(e, filter) {
			kept = append(kept, e)
		}
	}
	page := map[string]any{"items": []map[string]any{}, "totalItems": len(kept), "pageInfo": map[string]any{}}
	if from < len(kept) {
		to := min(from+limit, len(kept))
		page["items"] = kept[from:to]
		if to < len(kept) {
			page["pageInfo"] = map[string]any{"nextCursor": strconv.Itoa(to) + ";" + filter}
		}
	}
	_ = json.NewEncoder(w).Encode(page)
}

// matches is whether an entity is what a filter keeps: each condition,
// separated by commas, a field and what it is, without regard to case.
func matches(e map[string]any, filter string) bool {
	metadata, _ := e["metadata"].(map[string]any)
	for _, condition := range strings.Split(filter, ",") {
		field, want, _ := strings.Cut(condition, "=")
		var got []string
		switch field {
		case "kind":
			kind, _ := e["kind"].(string)
			got = []string{kind}
		case "metadata.name", "metadata.namespace":
			value, _ := metadata[strings.TrimPrefix(field, "metadata.")].(string)
			got = []string{value}
		default:
			if !strings.HasPrefix(field, "relations.") {
				return false
			}
			relations, _ := e["relations"].([]map[string]any)
			for _, each := range relations {
				if "relations."+each["type"].(string) == field {
					got = append(got, each["targetRef"].(string))
				}
			}
		}
		found := false
		for _, each := range got {
			found = found || strings.EqualFold(each, want)
		}
		if !found {
			return false
		}
	}
	return true
}
