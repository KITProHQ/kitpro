// Package multicontainer provides the typed, bounded dependency model used by
// the schema-v2 application plan. It does not interpret Docker Compose or
// expose Docker API structures.
package multicontainer

import (
	"fmt"

	"github.com/kitpro/kitpro/software/server/internal/manifest"
)

// StartOrder returns a deterministic dependency-first order. The bounded
// manifest validator runs before this function; the checks here are repeated
// at the execution boundary so a tampered plan cannot introduce a cycle.
func StartOrder(components []manifest.Component) ([]string, error) {
	if len(components) < 2 || len(components) > 16 {
		return nil, fmt.Errorf("component count outside policy")
	}
	byID := make(map[string]manifest.Component, len(components))
	for _, c := range components {
		if c.ID == "" {
			return nil, fmt.Errorf("component ID is required")
		}
		if _, exists := byID[c.ID]; exists {
			return nil, fmt.Errorf("duplicate component %q", c.ID)
		}
		byID[c.ID] = c
	}
	state := make(map[string]uint8, len(byID))
	order := make([]string, 0, len(byID))
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("component dependency cycle")
		case 2:
			return nil
		}
		c, ok := byID[id]
		if !ok {
			return fmt.Errorf("component dependency %q not found", id)
		}
		state[id] = 1
		for _, dep := range c.DependsOn {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = 2
		order = append(order, id)
		return nil
	}
	for _, c := range components {
		if err := visit(c.ID); err != nil {
			return nil, err
		}
	}
	return order, nil
}
