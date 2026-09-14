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
	if err != nil || !reflect.DeepEqual(got, []string{"db", "cache", "web"}) {
		t.Fatalf("order=%v err=%v", got, err)
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
