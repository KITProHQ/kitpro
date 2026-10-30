// Package multicontainer provides the typed, bounded dependency model used by
// the schema-v2 application plan. It does not interpret Docker Compose or
// expose Docker API structures.
package multicontainer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/kitpro/kitpro/software/server/internal/manifest"
)

// Topology is the normalized dependency graph accepted at the trusted-plan
// boundary. It is safe to persist as recovery evidence: neither its edges nor
// its order depend on manifest serialization or map iteration order.
type Topology struct {
	Components []TopologyComponent `json:"components"`
	StartOrder []string            `json:"start_order"`
	StopOrder  []string            `json:"stop_order"`
	Hash       string              `json:"hash"`
}

type TopologyComponent struct {
	ID        string   `json:"id"`
	DependsOn []string `json:"depends_on"`
	Ordinal   int      `json:"start_ordinal"`
}

func BuildTopology(components []manifest.Component) (Topology, error) {
	order, err := StartOrder(components)
	if err != nil {
		return Topology{}, err
	}
	byID := make(map[string][]string, len(components))
	for _, component := range components {
		dependencies := append([]string(nil), component.DependsOn...)
		sort.Strings(dependencies)
		byID[component.ID] = dependencies
	}
	topology := Topology{StartOrder: order, StopOrder: append([]string(nil), order...)}
	for left, right := 0, len(topology.StopOrder)-1; left < right; left, right = left+1, right-1 {
		topology.StopOrder[left], topology.StopOrder[right] = topology.StopOrder[right], topology.StopOrder[left]
	}
	for ordinal, id := range order {
		topology.Components = append(topology.Components, TopologyComponent{ID: id, DependsOn: byID[id], Ordinal: ordinal})
	}
	encoded, _ := json.Marshal(struct {
		Components []TopologyComponent `json:"components"`
		StartOrder []string            `json:"start_order"`
	}{topology.Components, topology.StartOrder})
	hash := sha256.Sum256(encoded)
	topology.Hash = hex.EncodeToString(hash[:])
	return topology, nil
}

// StartOrder returns a deterministic dependency-first order. The bounded
// manifest validator runs before this function; the checks here are repeated
// at the execution boundary so a tampered plan cannot introduce a cycle.
func StartOrder(components []manifest.Component) ([]string, error) {
	if len(components) < 2 || len(components) > 16 {
		return nil, fmt.Errorf("component count outside policy")
	}
	byID := make(map[string]manifest.Component, len(components))
	ids := make([]string, 0, len(components))
	for _, c := range components {
		if c.ID == "" {
			return nil, fmt.Errorf("component ID is required")
		}
		if _, exists := byID[c.ID]; exists {
			return nil, fmt.Errorf("duplicate component %q", c.ID)
		}
		byID[c.ID] = c
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
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
		dependencies := append([]string(nil), c.DependsOn...)
		sort.Strings(dependencies)
		for _, dep := range dependencies {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = 2
		order = append(order, id)
		return nil
	}
	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return order, nil
}
