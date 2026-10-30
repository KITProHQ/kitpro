package catalog

import (
	"embed"
	"fmt"
	"github.com/kitpro/kitpro/software/server/internal/manifest"
	"sort"
)

// Catalog content is shipped with KITPro and remains subject to strict validation.
//
//go:embed manifests/*.json
var files embed.FS

type Entry struct{ Manifest manifest.Manifest }

func Load() (map[string]Entry, error) {
	out := map[string]Entry{}
	names, err := files.ReadDir("manifests")
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		b, e := files.ReadFile("manifests/" + n.Name())
		if e != nil {
			return nil, e
		}
		m, e := manifest.Parse(b)
		if e != nil {
			return nil, fmt.Errorf("catalog %s: %w", n.Name(), e)
		}
		if _, ok := out[m.ID]; ok {
			return nil, fmt.Errorf("duplicate catalog id %s", m.ID)
		}
		out[m.ID] = Entry{m}
	}
	return out, nil
}
func IDs(c map[string]Entry) []string {
	ids := make([]string, 0, len(c))
	for id := range c {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
