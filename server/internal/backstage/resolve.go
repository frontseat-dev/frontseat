package backstage

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/frontseat-dev/frontseat/sdk/go/entity"
	"github.com/frontseat-dev/frontseat/server/internal/plain"
	"github.com/frontseat-dev/frontseat/server/internal/source/record"
)

// Resolution is what entities resolved to: the repositories to clone,
// and where each entity is among them.
type Resolution struct {
	// Checkouts are the repositories, each at a branch, with the
	// directories the entities govern; none for all of it.
	Checkouts []Checkout
	// Located are the entities asked for, and the components they depend
	// on, each with where its source is.
	Located []Located
}

// Checkout is a repository an entity's source is in.
type Checkout struct {
	// Remote is its URL; Ref the branch or tag to stand at, empty for the
	// remote's default branch.
	Remote, Ref string
	// Roots are the directories of it to take; none for all of it.
	Roots []string
}

// Located is an entity and where its source is.
type Located struct {
	// Ref names it in full.
	Ref entity.Ref
	// Remote is the repository it is in, as a Checkout names it.
	Remote string
	// Root is its directory there; empty for all of it.
	Root string
}

// sourceLocation is the annotation an entity says where its source is
// by: url: and a forge's address of a directory.
const sourceLocation = "backstage.io/source-location"

// document is what is read of an entity the catalog API answers with.
type document struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Relations []struct {
		Type      string `json:"type"`
		TargetRef string `json:"targetRef"`
	} `json:"relations"`
}

// ref is the entity's reference, where the catalog gave it one.
func (d document) ref() (entity.Ref, bool) {
	if d.Kind == "" || d.Metadata.Name == "" {
		return entity.Ref{}, false
	}
	ref := entity.New(d.Kind, d.Metadata.Namespace, d.Metadata.Name)
	return ref, ref.Validate() == nil
}

// page is a page of the entities a query selects.
type page struct {
	Items    []document `json:"items"`
	PageInfo struct {
		NextCursor string `json:"nextCursor"`
	} `json:"pageInfo"`
}

// Resolve returns the repositories that hold entities and the components
// they depend on, each with the directories those entities govern, and
// where each is. Each entity is named as a person typed it
// (entity.Pattern): a namespace left out is any, and a name that names
// several is refused naming each. A system brings the components that
// are part of it, and a group those it owns; a component brings those
// it depends on, by the relations the catalog holds, and nothing else
// it depends on brings source. An API or a Resource asked for is
// refused: it brings no source.
//
// The owner is who asks; this catalog answers everyone alike.
func (c *Catalog) Resolve(ctx context.Context, _ string, typed []entity.Pattern) (Resolution, error) {
	ctx, cancel := context.WithTimeout(ctx, c.limits.resolution)
	defer cancel()
	r := &resolving{catalog: c}
	asked := make([]entity.Ref, 0, len(typed))
	for i, ref := range typed {
		if err := ref.Validate(); err != nil {
			return Resolution{}, refused(Invalid, "the entity asked for %s is no entity's name: one is a name, kind:name or kind:namespace/name", ordinal(i))
		}
		if ref.Whole() {
			asked = append(asked, entity.Ref(ref))
			continue
		}
		found, err := r.named(ctx, ref)
		if err != nil {
			return Resolution{}, err
		}
		asked = append(asked, found)
	}
	return r.resolve(ctx, asked)
}

// ordinal says which of those asked for an entity is, without saying
// what it was.
func ordinal(i int) string { return "at " + strconv.Itoa(i+1) }

// resolving is one resolution: what it took of the catalog so far.
type resolving struct {
	catalog *Catalog
	taken   int
}

// took counts entities a resolution takes, and refuses more than its
// limit.
func (r *resolving) took(n int) error {
	r.taken += n
	if r.taken > r.catalog.limits.entities {
		return refused(Unavailable, "the catalog answered more than %d entities for what was asked: ask for less", r.catalog.limits.entities)
	}
	return nil
}

// query returns the entities a filter selects, a page at a time.
func (r *resolving) query(ctx context.Context, filter, what string) ([]entity.Ref, error) {
	var out []entity.Ref
	asked := url.Values{"filter": {filter}}
	for {
		asked.Set("fields", "kind,metadata.name,metadata.namespace")
		asked.Set("limit", strconv.Itoa(r.catalog.limits.page))
		var answered page
		if _, err := r.catalog.get(ctx, "/entities/by-query", asked, what, &answered); err != nil {
			return nil, err
		}
		if err := r.took(len(answered.Items)); err != nil {
			return nil, err
		}
		for _, each := range answered.Items {
			// A name that is none is no entity a person can have named.
			if ref, ok := each.ref(); ok {
				out = append(out, ref)
			}
		}
		next := answered.PageInfo.NextCursor
		if next == "" {
			return out, nil
		}
		if len(answered.Items) == 0 || len(next) > 4096 {
			return nil, refused(Unavailable, "the catalog's pages of %s do not end", what)
		}
		asked = url.Values{"cursor": {next}}
	}
}

// named is the one entity a pattern names.
func (r *resolving) named(ctx context.Context, typed entity.Pattern) (entity.Ref, error) {
	filter := "kind=" + typed.Kind + ",metadata.name=" + typed.Name
	if typed.Namespace != "" {
		filter += ",metadata.namespace=" + typed.Namespace
	}
	held, err := r.query(ctx, filter, typed.String())
	if err != nil {
		return entity.Ref{}, err
	}
	found, ok, err := typed.Find(held)
	if err != nil {
		return entity.Ref{}, refused(Ambiguous, "%v", err)
	}
	if !ok {
		return entity.Ref{}, refused(NotFound, "the catalog holds no %s", typed)
	}
	return found, nil
}

// contents are the components a system holds or a group owns, by the
// relation the catalog holds from each to it.
func (r *resolving) contents(ctx context.Context, container entity.Ref) ([]entity.Ref, error) {
	relation := "ownedBy"
	if container.Kind == "system" {
		relation = "partOf"
	}
	return r.query(ctx, "kind=component,relations."+relation+"="+container.String(), "what is in "+container.String())
}

// place is where an entity's source is, and the components it depends
// on.
type place struct {
	remote, ref, root string
	dependsOn         []entity.Ref
}

// locate asks the catalog for an entity by its name, and reads where its
// source is.
func (r *resolving) locate(ctx context.Context, ref entity.Ref) (place, bool, error) {
	var held document
	found, err := r.catalog.get(ctx, "/entities/by-name/"+url.PathEscape(ref.Kind)+"/"+url.PathEscape(ref.Namespace)+"/"+url.PathEscape(ref.Name), nil, ref.String(), &held)
	if err != nil || !found {
		return place{}, false, err
	}
	if err := r.took(1); err != nil {
		return place{}, false, err
	}
	if answered, ok := held.ref(); !ok || answered != ref {
		return place{}, false, refused(Unavailable, "the catalog answered another entity when asked for %s", ref)
	}
	target, ok := strings.CutPrefix(held.Metadata.Annotations[sourceLocation], "url:")
	if !ok {
		return place{}, false, refused(Invalid, "%s has no %s annotation that starts with url:, and so no source to clone", ref, sourceLocation)
	}
	remote, branch, root, err := source(target)
	if err != nil {
		return place{}, false, refused(Invalid, "%s says where its source is (%s) as no forge's address of a repository: %v", ref, sourceLocation, err)
	}
	// A repository an entity is in is held as one a person names: its
	// address holds no secret, and it is cloned where the workspace holds
	// it, at a branch written plainly.
	if at, err := url.Parse(remote); err != nil || at.User != nil {
		return place{}, false, refused(Invalid, "%s says its source is at an address that holds a user or a password, which a workspace is never given so", ref)
	}
	if branch != "" && (!plain.Text(branch) || strings.HasPrefix(branch, "-") || strings.Contains(branch, "..")) {
		return place{}, false, refused(Invalid, "%s says its source is at a branch that is not written plainly", ref)
	}
	if root != "" && !filepath.IsLocal(filepath.FromSlash(root)) {
		return place{}, false, refused(Invalid, "%s says its source is in a directory outside its repository", ref)
	}
	out := place{remote: remote, ref: branch, root: root}
	for _, relation := range held.Relations {
		if relation.Type != "dependsOn" {
			continue
		}
		on, err := entity.Parse(relation.TargetRef)
		if err != nil || !strings.Contains(relation.TargetRef, "/") {
			return place{}, false, refused(Unavailable, "the catalog says %s depends on what is no entity's reference", ref)
		}
		if on.Kind == entity.KindComponent {
			out.dependsOn = append(out.dependsOn, on)
		}
	}
	return out, true, nil
}

// resolve brings entities and the components they depend on, breadth
// first, each once.
func (r *resolving) resolve(ctx context.Context, entities []entity.Ref) (Resolution, error) {
	type key struct{ remote, ref string }
	type asked struct {
		ref entity.Ref
		by  *entity.Ref
	}
	var out Resolution
	roots := map[key][]string{}
	whole := map[key]bool{}
	var order []key
	seen := map[entity.Ref]bool{}
	var pending []asked
	for _, ref := range entities {
		pending = append(pending, asked{ref: ref})
	}
	for len(pending) > 0 {
		next := pending[0]
		pending = pending[1:]
		ref := next.ref
		if seen[ref] {
			continue
		}
		seen[ref] = true
		switch ref.Kind {
		case "system", "group":
			// A system is what it holds, and a group what it owns.
			held, err := r.contents(ctx, ref)
			if err != nil {
				return Resolution{}, err
			}
			if len(held) == 0 {
				return Resolution{}, refused(NotFound, "nothing is in %s", ref)
			}
			for _, each := range held {
				pending = append(pending, asked{ref: each})
			}
			continue
		case entity.KindComponent:
		default:
			// An API, a Resource or anything else but a component brings no
			// source of its own.
			return Resolution{}, refused(Invalid, "%s is a %s, which brings no source: ask for a component, a system or a group", ref, ref.Kind)
		}
		at, found, err := r.locate(ctx, ref)
		if err == nil && !found {
			err = refused(NotFound, "the catalog holds no %s", ref)
		}
		if err != nil {
			if next.by != nil {
				return Resolution{}, fmt.Errorf("%w: %s depends on it", err, *next.by)
			}
			return Resolution{}, err
		}
		out.Located = append(out.Located, Located{Ref: ref, Remote: at.remote, Root: at.root})
		k := key{at.remote, at.ref}
		if _, known := roots[k]; !known {
			order = append(order, k)
			roots[k] = nil
		}
		if at.root == "" {
			whole[k] = true
		} else if !slices.Contains(roots[k], at.root) {
			roots[k] = append(roots[k], at.root)
		}
		for _, on := range at.dependsOn {
			pending = append(pending, asked{ref: on, by: &ref})
		}
	}
	for _, k := range order {
		c := Checkout{Remote: k.remote, Ref: k.ref}
		if !whole[k] {
			c.Roots = roots[k]
			sort.Strings(c.Roots)
		}
		out.Checkouts = append(out.Checkouts, c)
	}
	return out, nil
}

// Named is a resolution as a workspace holds it: each repository named
// as nameOf names one from its remote, which is the one directory it is
// cloned into, and each entity in the repository so named. A remote that
// names no such directory is refused by name.
func (r Resolution) Named(nameOf func(remote string) string) ([]record.Checkout, []record.Located, error) {
	var checkouts []record.Checkout
	for _, c := range r.Checkouts {
		name := nameOf(c.Remote)
		if err := record.Directory(name); err != nil {
			return nil, nil, refused(Invalid, "the catalog says an entity's source is in a repository whose address names no directory to clone it into")
		}
		checkouts = append(checkouts, record.Checkout{Name: name, Remote: c.Remote, Ref: c.Ref, Roots: c.Roots})
	}
	located := make([]record.Located, 0, len(r.Located))
	for _, l := range r.Located {
		located = append(located, record.Located{Ref: l.Ref, Repository: nameOf(l.Remote), Root: l.Root})
	}
	return checkouts, located, nil
}
