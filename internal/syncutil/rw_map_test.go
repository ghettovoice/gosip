package syncutil_test

import (
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/syncutil"
)

func checkRWMap(t *testing.T, m *syncutil.RWMap[string, int], want map[string]int) {
	t.Helper()
	if diff := cmp.Diff(want, maps.Collect(m.All())); diff != "" {
		t.Errorf("map.All() mismatch (-want +got):\n%s", diff)
	}
	if got := m.Len(); got != len(want) {
		t.Errorf("map.Len() = %d, want %d", got, len(want))
	}
}

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected panic, got none")
		}
	}()
	fn()
}

func TestRWMap_ZeroValue(t *testing.T) {
	t.Parallel()

	var m syncutil.RWMap[string, int]

	if got, ok := m.Load("a"); ok || got != 0 {
		t.Errorf("map.Load(a) = (%d, %t), want (0, false)", got, ok)
	}
	if m.Has("a") {
		t.Error("map.Has(a) = true, want false")
	}
	if !m.HasAll() {
		t.Error("map.HasAll() = false, want true")
	}
	if m.HasAny() {
		t.Error("map.HasAny() = true, want false")
	}
	if m.HasAll("missing") {
		t.Error("map.HasAll(missing) = true, want false")
	}
	if got, ok := m.LoadAndDelete("a"); ok || got != 0 {
		t.Errorf("map.LoadAndDelete(a) = (%d, %t), want (0, false)", got, ok)
	}
	checkRWMap(t, &m, map[string]int{})

	clone := m.Clone()
	if clone == nil {
		t.Fatal("map.Clone() = nil, want empty map")
	}
	checkRWMap(t, clone, map[string]int{})
	clone.Store("x", 1)
	checkRWMap(t, &m, map[string]int{})

	if got := m.Clear(); got != &m {
		t.Errorf("map.Clear() = %p, want receiver %p", got, &m)
	}
	if got := m.Delete("a"); got != &m {
		t.Errorf("map.Delete(a) = %p, want receiver %p", got, &m)
	}

	m.Store("a", 1)
	if got, ok := m.Load("a"); !ok || got != 1 {
		t.Errorf("map.Load(a) after Store = (%d, %t), want (1, true)", got, ok)
	}
}

func TestRWMap_Mutations(t *testing.T) {
	t.Parallel()

	var m syncutil.RWMap[string, int]

	m.Store("a", 1)
	checkRWMap(t, &m, map[string]int{"a": 1})
	m.Store("a", 2)
	checkRWMap(t, &m, map[string]int{"a": 2})

	if got, found := m.LoadOrStore("a", 3); got != 2 || !found {
		t.Errorf("map.LoadOrStore(a, 3) = (%d, %t), want (2, true)", got, found)
	}
	checkRWMap(t, &m, map[string]int{"a": 2})
	if got, found := m.LoadOrStore("b", 3); got != 3 || found {
		t.Errorf("map.LoadOrStore(b, 3) = (%d, %t), want (3, false)", got, found)
	}
	checkRWMap(t, &m, map[string]int{"a": 2, "b": 3})

	m.StoreMany(func(yield func(string, int) bool) {
		if !yield("a", 4) {
			return
		}
		if !yield("b", 5) {
			return
		}
		yield("a", 6)
	})
	checkRWMap(t, &m, map[string]int{"a": 6, "b": 5})

	if prev, found := m.Swap("a", 7); prev != 6 || !found {
		t.Errorf("map.Swap(a, 7) = (%d, %t), want (6, true)", prev, found)
	}
	checkRWMap(t, &m, map[string]int{"a": 7, "b": 5})
	if prev, found := m.Swap("c", 8); prev != 0 || found {
		t.Errorf("map.Swap(c, 8) = (%d, %t), want (0, false)", prev, found)
	}
	checkRWMap(t, &m, map[string]int{"a": 7, "b": 5, "c": 8})

	if got, found := m.LoadAndDelete("b"); got != 5 || !found {
		t.Errorf("map.LoadAndDelete(b) = (%d, %t), want (5, true)", got, found)
	}
	checkRWMap(t, &m, map[string]int{"a": 7, "c": 8})
	if got, found := m.LoadAndDelete("b"); got != 0 || found {
		t.Errorf("map.LoadAndDelete(b) = (%d, %t), want (0, false)", got, found)
	}
	checkRWMap(t, &m, map[string]int{"a": 7, "c": 8})

	m.Delete("a", "a", "missing", "c")
	checkRWMap(t, &m, map[string]int{})

	m.Store("d", 4)
	m.Clear()
	checkRWMap(t, &m, map[string]int{})
}

func TestRWMap_Predicates(t *testing.T) {
	t.Parallel()

	var m syncutil.RWMap[string, int]
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
}

func TestRWMap_PredicatePanic(t *testing.T) {
	t.Parallel()

	var m syncutil.RWMap[string, int]
	m.Store("a", 1)

	mustPanic(t, func() {
		m.CompareAndDelete("a", func(int) bool { panic("check panic") })
	})
	checkRWMap(t, &m, map[string]int{"a": 1})
	mustPanic(t, func() {
		m.CompareAndSwap("a", 2, func(int) bool { panic("check panic") })
	})
	checkRWMap(t, &m, map[string]int{"a": 1})

	m.Store("b", 2)
	checkRWMap(t, &m, map[string]int{"a": 1, "b": 2})

	if m.CompareAndDelete("missing", nil) {
		t.Error("map.CompareAndDelete(missing, nil) = true, want false")
	}
	if m.CompareAndSwap("missing", 9, nil) {
		t.Error("map.CompareAndSwap(missing, 9, nil) = true, want false")
	}

	var sm syncutil.RWMap[string, []int]
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

const errFactory errors.Error = "factory failed"

func TestRWMap_LoadOrStoreFunc(t *testing.T) {
	t.Parallel()

	var m syncutil.RWMap[string, int]
	m.Store("a", 1)

	called := false
	actual, found, err := m.LoadOrStoreFunc("a", func() (int, error) {
		called = true
		return 9, nil
	})
	if called {
		t.Error("factory called for existing key")
	}
	if actual != 1 || !found || err != nil {
		t.Errorf("map.LoadOrStoreFunc(a) = (%d, %t, %v), want (1, true, nil)", actual, found, err)
	}

	n := 0
	actual, found, err = m.LoadOrStoreFunc("b", func() (int, error) {
		n++
		return 5, nil
	})
	if actual != 5 || found || err != nil || n != 1 {
		t.Errorf("map.LoadOrStoreFunc(b) = (%d, %t, %v) calls %d, want (5, false, nil) calls 1",
			actual, found, err, n)
	}
	checkRWMap(t, &m, map[string]int{"a": 1, "b": 5})

	actual, found, err = m.LoadOrStoreFunc("c", func() (int, error) {
		return 7, errFactory
	})
	if actual != 7 || found || !errors.Is(err, errFactory) {
		t.Errorf("map.LoadOrStoreFunc(c) = (%d, %t, %v), want (7, false, %v)", actual, found, err, errFactory)
	}
	if _, ok := m.Load("c"); ok {
		t.Error("map.Load(c) after factory error = true, want false")
	}

	mustPanic(t, func() {
		_, _, _ = m.LoadOrStoreFunc("d", func() (int, error) { panic("factory panic") })
	})
	if _, ok := m.Load("d"); ok {
		t.Error("map.Load(d) after factory panic = true, want false")
	}
	m.Store("e", 8)
	checkRWMap(t, &m, map[string]int{"a": 1, "b": 5, "e": 8})
}

func TestRWMap_Copy(t *testing.T) {
	t.Parallel()

	newSrc := func() *syncutil.RWMap[string, int] {
		var m syncutil.RWMap[string, int]
		m.Store("a", 1)
		return &m
	}
	newDst := func() *syncutil.RWMap[string, int] {
		var m syncutil.RWMap[string, int]
		m.Store("b", 2).Store("c", 3)
		return &m
	}

	for _, tt := range []struct {
		name     string
		apply    func(src, dst *syncutil.RWMap[string, int]) *syncutil.RWMap[string, int]
		retIsSrc bool
	}{
		{name: "CopyTo", retIsSrc: true, apply: func(src, dst *syncutil.RWMap[string, int]) *syncutil.RWMap[string, int] {
			return src.CopyTo(dst)
		}},
		{name: "CopyFrom", retIsSrc: false, apply: func(src, dst *syncutil.RWMap[string, int]) *syncutil.RWMap[string, int] {
			return dst.CopyFrom(src)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src, dst := newSrc(), newDst()
			wantRet := dst
			if tt.retIsSrc {
				wantRet = src
			}
			if got := tt.apply(src, dst); got != wantRet {
				t.Errorf("copy returned %p, want %p", got, wantRet)
			}
			checkRWMap(t, dst, map[string]int{"a": 1})
			checkRWMap(t, src, map[string]int{"a": 1})

			dst.Store("a", 100)
			checkRWMap(t, src, map[string]int{"a": 1})

			var empty syncutil.RWMap[string, int]
			tt.apply(&empty, dst)
			checkRWMap(t, dst, map[string]int{})

			var zero syncutil.RWMap[string, int]
			tt.apply(src, &zero)
			checkRWMap(t, &zero, map[string]int{"a": 1})
		})
	}

	t.Run("self copy", func(t *testing.T) {
		t.Parallel()

		m := newSrc()
		if got := m.CopyTo(m); got != m {
			t.Errorf("map.CopyTo(self) = %p, want receiver %p", got, m)
		}
		checkRWMap(t, m, map[string]int{"a": 1})
		if got := m.CopyFrom(m); got != m {
			t.Errorf("map.CopyFrom(self) = %p, want receiver %p", got, m)
		}
		checkRWMap(t, m, map[string]int{"a": 1})
	})

	t.Run("clone", func(t *testing.T) {
		t.Parallel()

		src := newSrc()
		clone := src.Clone()
		checkRWMap(t, clone, map[string]int{"a": 1})
		clone.Store("b", 2)
		src.Store("a", 100)
		checkRWMap(t, clone, map[string]int{"a": 1, "b": 2})
		checkRWMap(t, src, map[string]int{"a": 100})
	})
}

func TestRWMap_AllSnapshot(t *testing.T) {
	t.Parallel()

	var m syncutil.RWMap[string, int]
	m.Store("a", 1).Store("b", 2)

	seq := m.All()
	m.Store("c", 3)

	got := map[string]int{}
	first := true
	for k, v := range seq {
		got[k] = v
		if first {
			first = false
			m.Clear()
			m.Store("z", 9)
		}
	}
	if diff := cmp.Diff(map[string]int{"a": 1, "b": 2, "c": 3}, got); diff != "" {
		t.Errorf("map.All() yielded mismatch (-want +got):\n%s", diff)
	}
	checkRWMap(t, &m, map[string]int{"z": 9})

	n := 0
	for range m.All() {
		n++
		break
	}
	if n != 1 {
		t.Errorf("map.All() early break iterated %d times, want 1", n)
	}
	m.Store("y", 8)
	checkRWMap(t, &m, map[string]int{"z": 9, "y": 8})
}

func TestRWMap_StoreMany_Self(t *testing.T) {
	t.Parallel()

	var m syncutil.RWMap[string, int]
	m.Store("a", 1).Store("b", 2)

	m.StoreMany(m.All())
	checkRWMap(t, &m, map[string]int{"a": 1, "b": 2})

	m.StoreMany(func(yield func(string, int) bool) {
		if got, ok := m.Load("a"); !ok || got != 1 {
			t.Errorf("map.Load(a) inside iterator = (%d, %t), want (1, true)", got, ok)
		}
		if !m.Has("b") {
			t.Error("map.Has(b) inside iterator = false, want true")
		}
		m.Store("outside", 3)
		yield("c", 4)
	})
	checkRWMap(t, &m, map[string]int{"a": 1, "b": 2, "outside": 3, "c": 4})
}

func TestRWMap_StoreMany_Panic(t *testing.T) {
	t.Parallel()

	var m syncutil.RWMap[string, int]
	m.Store("kept", 1)

	mustPanic(t, func() {
		m.StoreMany(func(yield func(string, int) bool) {
			if !yield("a", 2) {
				return
			}
			panic("iterator panic")
		})
	})
	checkRWMap(t, &m, map[string]int{"kept": 1})

	m.Store("after", 3)
	checkRWMap(t, &m, map[string]int{"kept": 1, "after": 3})

	var am syncutil.RWMap[any, int]
	am.Store("kept", 1)
	mustPanic(t, func() {
		am.StoreMany(func(yield func(any, int) bool) {
			if !yield("valid", 1) {
				return
			}
			yield([]int{1}, 2)
		})
	})
	if got := am.Len(); got != 1 {
		t.Errorf("map[any,int].Len() = %d, want 1", got)
	}
	if _, ok := am.Load("valid"); ok {
		t.Error("map[any,int].Load(valid) after panic = true, want false")
	}
}

func TestRWMap_Delete_Panic(t *testing.T) {
	t.Parallel()

	var m syncutil.RWMap[any, int]
	m.Store("a", 1).Store("b", 2)

	mustPanic(t, func() {
		m.Delete("a", []int{1})
	})
	if _, ok := m.Load("a"); ok {
		t.Error("map[any,int].Load(a) after panic = true, want false")
	}
	if got, ok := m.Load("b"); !ok || got != 2 {
		t.Errorf("map[any,int].Load(b) = (%d, %t), want (2, true)", got, ok)
	}
	if got := m.Len(); got != 1 {
		t.Errorf("map[any,int].Len() = %d, want 1", got)
	}

	m.Store("c", 3)
	if got := m.Len(); got != 2 {
		t.Errorf("map[any,int].Len() after Store = %d, want 2", got)
	}
}

func TestRWMap_NilReceiver(t *testing.T) {
	t.Parallel()

	var m *syncutil.RWMap[string, int]

	if got, ok := m.Load("a"); ok || got != 0 {
		t.Errorf("map.Load(a) = (%d, %t), want (0, false)", got, ok)
	}
	if got, ok := m.LoadAndDelete("a"); ok || got != 0 {
		t.Errorf("map.LoadAndDelete(a) = (%d, %t), want (0, false)", got, ok)
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
	if m.HasAll("a") || m.HasAny("a") {
		t.Error("map.HasAll/HasAny(a) = true, want false")
	}
	if got := m.Clear(); got != nil {
		t.Errorf("map.Clear() = %p, want nil", got)
	}
	if got := m.Delete("a"); got != nil {
		t.Errorf("map.Delete(a) = %p, want nil", got)
	}
	if got := m.Clone(); got != nil {
		t.Errorf("map.Clone() = %p, want nil", got)
	}
	if got := m.Len(); got != 0 {
		t.Errorf("map.Len() = %d, want 0", got)
	}
	if n := len(maps.Collect(m.All())); n != 0 {
		t.Errorf("map.All() yielded %d items, want 0", n)
	}

	var dst syncutil.RWMap[string, int]
	dst.Store("a", 1)
	if got := m.CopyTo(&dst); got != nil {
		t.Errorf("map.CopyTo(dst) = %p, want nil", got)
	}
	if got := dst.CopyTo(nil); got != &dst {
		t.Errorf("map.CopyTo(nil) = %p, want receiver %p", got, &dst)
	}
	if got := m.CopyFrom(&dst); got != nil {
		t.Errorf("map.CopyFrom(dst) = %p, want nil", got)
	}
	if got := dst.CopyFrom(nil); got != nil {
		t.Errorf("map.CopyFrom(nil) = %p, want nil", got)
	}
	checkRWMap(t, &dst, map[string]int{"a": 1})
}

func TestRWMap_LoadOrStoreFunc_Concurrent(t *testing.T) {
	t.Parallel()

	var m syncutil.RWMap[string, int]
	const n = 32
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		calls atomic.Int64
		found atomic.Int64
	)
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			<-start
			actual, f, err := m.LoadOrStoreFunc("k", func() (int, error) {
				calls.Add(1)
				return 1, nil
			})
			if err != nil {
				t.Errorf("map.LoadOrStoreFunc err = %v, want nil", err)
			}
			if actual != 1 {
				t.Errorf("map.LoadOrStoreFunc actual = %d, want 1", actual)
			}
			if f {
				found.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("factory calls = %d, want 1", got)
	}
	if got := found.Load(); got != n-1 {
		t.Errorf("found count = %d, want %d", got, n-1)
	}
	checkRWMap(t, &m, map[string]int{"k": 1})
}

func TestRWMap_CompareAndSwap_Concurrent(t *testing.T) {
	t.Parallel()

	var m syncutil.RWMap[string, int]
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

	checkRWMap(t, &m, map[string]int{"count": g * iters})
}

func TestRWMap_CompareAndDelete_Concurrent(t *testing.T) {
	t.Parallel()

	var m syncutil.RWMap[string, int]
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
	checkRWMap(t, &m, map[string]int{})
}

func TestRWMap_Copy_Concurrent(t *testing.T) {
	t.Parallel()

	var a, b syncutil.RWMap[string, int]
	a.Store("a", 1)
	b.Store("a", 1)

	const n, iters = 4, 100
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
	)
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			<-start
			for range iters {
				a.CopyTo(&b)
				b.CopyTo(&a)
				a.CopyFrom(&b)
				b.CopyFrom(&a)
				a.StoreMany(b.All())
				b.StoreMany(a.All())
			}
		}()
	}
	close(start)
	wg.Wait()

	checkRWMap(t, &a, map[string]int{"a": 1})
	checkRWMap(t, &b, map[string]int{"a": 1})
}

func TestRWMap_Membership(t *testing.T) {
	t.Parallel()

	var m syncutil.RWMap[string, int]
	m.Store("a", 1).Store("b", 2)

	for _, tt := range []struct {
		name    string
		keys    []string
		wantAll bool
		wantAny bool
	}{
		{name: "empty", wantAll: true},
		{name: "present", keys: []string{"a"}, wantAll: true, wantAny: true},
		{name: "all present", keys: []string{"a", "b"}, wantAll: true, wantAny: true},
		{name: "absent", keys: []string{"missing"}},
		{name: "missing first", keys: []string{"missing", "b"}, wantAny: true},
		{name: "missing last", keys: []string{"a", "missing"}, wantAny: true},
		{name: "duplicates", keys: []string{"a", "a"}, wantAll: true, wantAny: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := m.HasAll(tt.keys...); got != tt.wantAll {
				t.Errorf("map.HasAll(%v) = %t, want %t", tt.keys, got, tt.wantAll)
			}
			if got := m.HasAny(tt.keys...); got != tt.wantAny {
				t.Errorf("map.HasAny(%v) = %t, want %t", tt.keys, got, tt.wantAny)
			}
		})
	}
	if !m.Has("a") || m.Has("missing") {
		t.Error("map.Has() returned incorrect membership")
	}
	checkRWMap(t, &m, map[string]int{"a": 1, "b": 2})
}
