package sip

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/sip/header"
)

func TestProxyTargetPlan_Clone(t *testing.T) {
	t.Parallel()

	uriA := &URI{Addr: MakeHostAddr("a.example.com")}
	uriB := &URI{Addr: MakeHostAddr("b.example.com")}

	t.Run("cloned plan is independent", func(t *testing.T) {
		t.Parallel()

		plan := ProxyTargetPlan{Groups: [][]ProxyTarget{
			{
				{
					URI:        uriA,
					Routes:     []header.RouteHop{{URI: &URI{Addr: MakeHostAddr("hop.example.com")}}},
					Attributes: map[string]string{"k": "original"},
				},
				{URI: uriA},
			},
			{
				{ID: "x", URI: uriB},
			},
		}}

		cloned, err := clonePrxTargetPlan(plan)
		if err != nil {
			t.Fatalf("clonePrxTargetPlan() error = %v, want nil", err)
		}

		if got, want := cloned.Groups[0][0].ID, "1.1"; got != want {
			t.Fatalf("cloned.Groups[0][0].ID = %q, want %q", got, want)
		}
		if got, want := cloned.Groups[0][1].ID, "1.2"; got != want {
			t.Fatalf("cloned.Groups[0][1].ID = %q, want %q", got, want)
		}
		if got, want := cloned.Groups[1][0].ID, "x"; got != want {
			t.Fatalf("cloned.Groups[1][0].ID = %q, want %q", got, want)
		}

		plan.Groups[0][0].Attributes["k"] = "mutated"
		if got := cloned.Groups[0][0].Attributes["k"]; got != "original" {
			t.Fatalf("cloned attributes mutated: %q, want %q", got, "original")
		}

		plan.Groups[0][0].URI.User = MakeUserInfo("mutated")
		if got := cloned.Groups[0][0].URI.User.String(); got != "" {
			t.Fatalf("cloned URI user mutated: %q, want empty", got)
		}

		plan.Groups[0][0].Routes[0].URI.(*URI).User = MakeUserInfo("mutated")
		if got := cloned.Groups[0][0].Routes[0].URI.(*URI).User.String(); got != "" {
			t.Fatalf("cloned route URI user mutated: %q, want empty", got)
		}

		cloned.Groups[1][0].Attributes = map[string]string{"k": "clone-only"}
		if _, ok := plan.Groups[1][0].Attributes["k"]; ok {
			t.Fatal("source attributes mutated through clone")
		}
	})

	t.Run("duplicate final IDs rejected", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			plan ProxyTargetPlan
		}{
			{
				name: "explicit duplicates",
				plan: ProxyTargetPlan{Groups: [][]ProxyTarget{
					{{ID: "dup", URI: uriA}, {URI: uriB}},
					{{ID: "dup", URI: uriB}},
				}},
			},
			{
				name: "explicit generated collision",
				plan: ProxyTargetPlan{Groups: [][]ProxyTarget{
					{{URI: uriA}, {ID: "1.1", URI: uriB}},
				}},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if _, err := clonePrxTargetPlan(tt.plan); err == nil {
					t.Fatal("clonePrxTargetPlan() error = nil, want duplicate target ID error")
				}
			})
		}
	})

	t.Run("same URI targets preserved", func(t *testing.T) {
		t.Parallel()

		plan := ProxyTargetPlan{Groups: [][]ProxyTarget{
			{
				{ID: "a", URI: uriA},
				{ID: "b", URI: uriA, Delivery: ProxyDeliveryRef{Kind: "flow", Key: "f-1"}},
			},
		}}

		cloned, err := clonePrxTargetPlan(plan)
		if err != nil {
			t.Fatalf("clonePrxTargetPlan() error = %v, want nil", err)
		}

		if got, want := len(cloned.Groups[0]), 2; got != want {
			t.Fatalf("len(cloned.Groups[0]) = %d, want %d", got, want)
		}
		if diff := cmp.Diff(
			[]string{"a", "b"},
			[]string{cloned.Groups[0][0].ID, cloned.Groups[0][1].ID},
		); diff != "" {
			t.Fatalf("target IDs mismatch (-want +got):\n%s", diff)
		}
	})
}
