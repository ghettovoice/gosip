package syncutil_test

import (
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/syncutil"
)

func checkShardMap(t *testing.T, m *syncutil.ShardMap[string, int], want map[string]int) {
	t.Helper()
	if diff := cmp.Diff(want, maps.Collect(m.All())); diff != "" {
		t.Errorf("map.All() mismatch (-want +got):\n%s", diff)
	}
	if got := m.Len(); got != len(want) {
		t.Errorf("map.Len() = %d, want %d", got, len(want))
	}
}

func TestShardMap_BasicMutations(t *testing.T) {
	t.Parallel()

	m := new(syncutil.ShardMap[string, int])

	m.Store("a", 1)
	checkShardMap(t, m, map[string]int{"a": 1})

	m.Store("a", 2)
	checkShardMap(t, m, map[string]int{"a": 2})

	if got, found := m.LoadOrStore("a", 3); got != 2 || !found {
		t.Errorf("map.LoadOrStore(a, 3) = (%d, %t), want (2, true)", got, found)
	}
	checkShardMap(t, m, map[string]int{"a": 2})
	if got, found := m.LoadOrStore("b", 3); got != 3 || found {
		t.Errorf("map.LoadOrStore(b, 3) = (%d, %t), want (3, false)", got, found)
	}
	checkShardMap(t, m, map[string]int{"a": 2, "b": 3})

	if prev, found := m.Swap("a", 7); prev != 2 || !found {
		t.Errorf("map.Swap(a, 7) = (%d, %t), want (2, true)", prev, found)
	}
	checkShardMap(t, m, map[string]int{"a": 7, "b": 3})
	if prev, found := m.Swap("c", 8); prev != 0 || found {
		t.Errorf("map.Swap(c, 8) = (%d, %t), want (0, false)", prev, found)
	}
	checkShardMap(t, m, map[string]int{"a": 7, "b": 3, "c": 8})

	if got, found := m.LoadAndDelete("b"); got != 3 || !found {
		t.Errorf("map.LoadAndDelete(b) = (%d, %t), want (3, true)", got, found)
	}
	checkShardMap(t, m, map[string]int{"a": 7, "c": 8})
	if got, found := m.LoadAndDelete("b"); got != 0 || found {
		t.Errorf("map.LoadAndDelete(b) = (%d, %t), want (0, false)", got, found)
	}
	checkShardMap(t, m, map[string]int{"a": 7, "c": 8})

	if got, found := m.Delete("a"); got != 7 || !found {
		t.Errorf("map.Delete(a) = (%d, %t), want (7, true)", got, found)
	}
	if got, found := m.Delete("missing"); got != 0 || found {
		t.Errorf("map.Delete(missing) = (%d, %t), want (0, false)", got, found)
	}
	checkShardMap(t, m, map[string]int{"c": 8})

	m.Clear()
	checkShardMap(t, m, map[string]int{})
}

func TestShardMap_Predicates(t *testing.T) {
	t.Parallel()

	m := new(syncutil.ShardMap[string, int])

	calls := 0
	accept := false
	check := func(actual int) bool {
		calls++
		if actual != 1 {
			t.Errorf("actual = %d, want 1", actual)
		}
		return accept
	}

	if m.CompareAndDelete("a", check) || m.CompareAndSwap("a", 2, check) || calls != 0 {
		t.Fatal("missing key invoked predicate or changed map")
	}

	m.Store("a", 1)
	if m.CompareAndDelete("a", check) || m.CompareAndSwap("a", 2, check) || calls != 2 {
		t.Fatal("rejected operation changed map or unexpected predicate count")
	}
	if got, _ := m.Load("a"); got != 1 {
		t.Fatalf("map.Load(a) = %d, want 1", got)
	}

	accept = true
	if !m.CompareAndSwap("a", 2, check) || calls != 3 {
		t.Fatal("accepted swap failed or unexpected predicate count")
	}
	if got, _ := m.Load("a"); got != 2 {
		t.Fatalf("map.Load(a) = %d, want 2", got)
	}
	if got := m.Len(); got != 1 {
		t.Errorf("map.Len() = %d, want 1", got)
	}
	if !m.CompareAndDelete("a", func(actual int) bool { return actual == 2 }) {
		t.Fatal("accepted deletion failed")
	}
	if got := m.Len(); got != 0 {
		t.Errorf("map.Len() = %d, want 0", got)
	}

	var sm syncutil.ShardMap[string, []int]
	sm.Store("k", []int{1, 2})
	if !sm.CompareAndSwap("k", []int{3}, func(actual []int) bool {
		return slices.Equal(actual, []int{1, 2})
	}) {
		t.Error("map.CompareAndSwap with slices.Equal check = false, want true")
	}
	if got, _ := sm.Load("k"); !slices.Equal(got, []int{3}) {
		t.Errorf("map.Load(k) = %v, want [3]", got)
	}
}

func TestShardMap_ZeroValue(t *testing.T) {
	t.Parallel()

	var m syncutil.ShardMap[string, int]

	if got, ok := m.Load("a"); ok || got != 0 {
		t.Errorf("map.Load(a) = (%d, %t), want (0, false)", got, ok)
	}
	if m.Has("a") {
		t.Error("map.Has(a) = true, want false")
	}
	if got := m.Len(); got != 0 {
		t.Errorf("map.Len() = %d, want 0", got)
	}

	m.Store("a", 1)
	if got, ok := m.Load("a"); !ok || got != 1 {
		t.Errorf("map.Load(a) after Store = (%d, %t), want (1, true)", got, ok)
	}
	if got := m.Len(); got != 1 {
		t.Errorf("map.Len() = %d, want 1", got)
	}

	clone := m.Clone()
	if clone == nil {
		t.Fatal("map.Clone() = nil, want empty map")
	}
	checkShardMap(t, clone, map[string]int{"a": 1})
	clone.Store("b", 2)
	checkShardMap(t, &m, map[string]int{"a": 1})
}

func TestShardMap_NilReceiver(t *testing.T) {
	t.Parallel()

	var m *syncutil.ShardMap[string, int]

	if got, ok := m.Load("a"); ok || got != 0 {
		t.Errorf("map.Load(a) = (%d, %t), want (0, false)", got, ok)
	}
	if got, ok := m.LoadAndDelete("a"); ok || got != 0 {
		t.Errorf("map.LoadAndDelete(a) = (%d, %t), want (0, false)", got, ok)
	}
	if got, ok := m.Delete("a"); ok || got != 0 {
		t.Errorf("map.Delete(a) = (%d, %t), want (0, false)", got, ok)
	}
	if got, ok := m.Swap("a", 1); ok || got != 0 {
		t.Errorf("map.Swap(a, 1) = (%d, %t), want (0, false)", got, ok)
	}
	called := false
	pred := func(int) bool { called = true; return true }
	if m.CompareAndDelete("a", pred) || called {
		t.Error("map.CompareAndDelete invoked predicate or returned true")
	}
	if m.CompareAndSwap("a", 1, pred) || called {
		t.Error("map.CompareAndSwap invoked predicate or returned true")
	}
	if m.Has("a") {
		t.Error("map.Has(a) = true, want false")
	}
	if got := m.Len(); got != 0 {
		t.Errorf("map.Len() = %d, want 0", got)
	}
	if got := m.Clear(); got != nil {
		t.Errorf("map.Clear() = %p, want nil", got)
	}
	if got := m.Clone(); got != nil {
		t.Errorf("map.Clone() = %p, want nil", got)
	}
	if got := m.Store("a", 1); got != nil {
		t.Errorf("map.Store(a, 1) = %p, want nil", got)
	}
	if got, _ := m.LoadOrStore("a", 1); got != 1 {
		t.Errorf("map.LoadOrStore(a, 1) = %d, want 1", got)
	}
}

func TestShardMap_All_Snapshot(t *testing.T) {
	t.Parallel()

	m := new(syncutil.ShardMap[string, int])
	m.Store("a", 1).Store("b", 2)

	seq := m.All()
	m.Store("c", 3)

	first := true
	for range seq {
		if first {
			first = false
			m.Clear()
			m.Store("z", 9)
		}
	}
	checkShardMap(t, m, map[string]int{"z": 9})

	n := 0
	for range m.All() {
		n++
		break
	}
	if n != 1 {
		t.Errorf("map.All() early break iterated %d times, want 1", n)
	}
}

func TestShardMap_Clear_SnapshotSemantics(t *testing.T) {
	t.Parallel()

	const n = 1000
	m := syncutil.NewShardMap[int, int](8)
	for i := range n {
		m.Store(i, i)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Go(func() {
		<-start
		for i := range n {
			m.Store(n+i, n+i)
		}
	})
	close(start)
	m.Clear()
	wg.Wait()

	for k := range m.All() {
		if k < n {
			t.Errorf("pre-Clear item %d survived; Clear must have snapshot semantics", k)
		}
	}

	count := 0
	for range m.All() {
		count++
	}
	if got := m.Len(); got != count {
		t.Errorf("Len() = %d, but All() yielded %d items", got, count)
	}
}

func TestShardMap_Clone(t *testing.T) {
	t.Parallel()

	m := new(syncutil.ShardMap[string, int])
	m.Store("a", 1).Store("b", 2)

	clone := m.Clone()
	checkShardMap(t, clone, map[string]int{"a": 1, "b": 2})
	clone.Store("c", 3)
	m.Store("a", 100)
	checkShardMap(t, clone, map[string]int{"a": 1, "b": 2, "c": 3})
	checkShardMap(t, m, map[string]int{"a": 100, "b": 2})

	var empty syncutil.ShardMap[string, int]
	cloneEmpty := empty.Clone()
	if cloneEmpty == nil {
		t.Fatal("empty map.Clone() = nil, want empty map")
	}
	checkShardMap(t, cloneEmpty, map[string]int{})
}

func TestShardMap_CustomShardsNum(t *testing.T) {
	t.Parallel()

	m := syncutil.NewShardMap[int, int](4)
	for i := range 100 {
		m.Store(i, i)
	}
	if got := m.Len(); got != 100 {
		t.Errorf("map.Len() = %d, want 100", got)
	}
	for i := range 100 {
		if got, ok := m.Load(i); !ok || got != i {
			t.Errorf("map.Load(%d) = (%d, %t), want (%d, true)", i, got, ok, i)
		}
	}
}

func TestShardMap_Concurrent(t *testing.T) {
	t.Parallel()

	m := new(syncutil.ShardMap[int, int])
	const g, n = 8, 100
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
	)
	wg.Add(g)
	for i := range g {
		go func(id int) {
			defer wg.Done()
			<-start
			for j := range n {
				key := id*n + j
				m.Store(key, key)
				if _, ok := m.Load(key); !ok {
					t.Errorf("map.Load(%d) = false after Store", key)
				}
				if j%2 == 0 {
					m.Delete(key)
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()

	wantLen := g * n / 2
	if got := m.Len(); got != wantLen {
		t.Errorf("map.Len() = %d, want %d", got, wantLen)
	}

	count := 0
	for k, v := range m.All() {
		if k != v || k%n%2 != 1 {
			t.Errorf("unexpected item (%d, %d)", k, v)
		}
		count++
	}
	if count != wantLen {
		t.Errorf("map.All() yielded %d items, want %d", count, wantLen)
	}
}

func TestShardMap_CompareAndSwap_Concurrent(t *testing.T) {
	t.Parallel()

	m := new(syncutil.ShardMap[string, int])
	m.Store("count", 0)

	const g, iters = 8, 100
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
	)
	wg.Add(g)
	for range g {
		go func() {
			defer wg.Done()
			<-start
			for range iters {
				loaded, _ := m.Load("count")
				for !m.CompareAndSwap("count", loaded+1, func(actual int) bool {
					return actual == loaded
				}) {
					loaded, _ = m.Load("count")
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	checkShardMap(t, m, map[string]int{"count": g * iters})
}

func TestShardMap_CompareAndDelete_Concurrent(t *testing.T) {
	t.Parallel()

	m := new(syncutil.ShardMap[string, int])
	m.Store("k", 1)

	const n = 32
	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		deleted atomic.Int64
	)
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			<-start
			if m.CompareAndDelete("k", func(int) bool { return true }) {
				deleted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := deleted.Load(); got != 1 {
		t.Errorf("deleted count = %d, want 1", got)
	}
	checkShardMap(t, m, map[string]int{})
}
