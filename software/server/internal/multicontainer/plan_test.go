package multicontainer

import (
	"reflect"
	"testing"

	"github.com/kitpro/kitpro/software/server/internal/manifest"
)

func TestStartOrderIsDependencyFirst(t *testing.T) {
	got, err := StartOrder([]manifest.Component{
		{ID: "web", DependsOn: []string{"db", "cache"}},
		{ID: "db"},
		{ID: "cache"},
	})
	if err != nil || !reflect.DeepEqual(got, []string{"cache", "db", "web"}) {
		t.Fatalf("order=%v err=%v", got, err)
	}
}

func TestStartOrderDoesNotDependOnManifestSerialization(t *testing.T) {
	first, err := StartOrder([]manifest.Component{{ID: "web", DependsOn: []string{"db", "cache"}}, {ID: "db"}, {ID: "cache"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := StartOrder([]manifest.Component{{ID: "cache"}, {ID: "web", DependsOn: []string{"cache", "db"}}, {ID: "db"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("orders differ: %v vs %v", first, second)
	}
	topology, err := BuildTopology([]manifest.Component{{ID: "web", DependsOn: []string{"db", "cache"}}, {ID: "db"}, {ID: "cache"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(topology.StopOrder, []string{"web", "db", "cache"}) || topology.Hash == "" {
		t.Fatalf("unexpected topology: %#v", topology)
	}
}

func TestStartOrderRejectsCycleAndMissingDependency(t *testing.T) {
	for _, components := range [][]manifest.Component{
		{{ID: "a", DependsOn: []string{"b"}}, {ID: "b", DependsOn: []string{"a"}}},
		{{ID: "a", DependsOn: []string{"missing"}}, {ID: "b"}},
	} {
		if _, err := StartOrder(components); err == nil {
			t.Fatal("accepted invalid dependency graph")
		}
	}
}
