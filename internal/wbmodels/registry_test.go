package wbmodels

import (
	"fmt"
	"sync"
	"testing"
)

// --- ResolveModel 基本 ---

func TestRegistry_ResolveModel(t *testing.T) {
	reg := NewRegistry()
	reg.Store("auth-1", map[string]string{
		"workbuddy/Model A": "model-a",
		"workbuddy/Model B": "model-b",
	})
	got, err := reg.ResolveModel("auth-1", "workbuddy/Model A")
	if err != nil {
		t.Fatal(err)
	}
	if got != "model-a" {
		t.Errorf("got %q, want model-a", got)
	}
}

func TestRegistry_ResolveModel_UnknownID(t *testing.T) {
	reg := NewRegistry()
	reg.Store("auth-1", map[string]string{"workbuddy/A": "a"})
	_, err := reg.ResolveModel("auth-1", "workbuddy/Nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown public ID")
	}
	var nf *ModelNotFoundError
	if ok := fmt.Sprintf("%T", err); ok != "*wbmodels.ModelNotFoundError" {
		t.Errorf("error type = %q, want *ModelNotFoundError", ok)
	}
	_ = nf
}

func TestRegistry_ResolveModel_PrefixCompat(t *testing.T) {
	reg := NewRegistry()
	reg.Store("auth-1", map[string]string{"workbuddy/A": "a"})
	// 带 provider 前缀
	got, err := reg.ResolveModel("auth-1", "workbuddy/A")
	if err != nil || got != "a" {
		t.Errorf("带前缀: got=%q err=%v", got, err)
	}
	// 不带 provider 前缀
	got, err = reg.ResolveModel("auth-1", "A")
	if err != nil || got != "a" {
		t.Errorf("不带前缀: got=%q err=%v", got, err)
	}
}

func TestRegistry_ResolveModel_Nil(t *testing.T) {
	var reg *Registry
	_, err := reg.ResolveModel("auth-1", "workbuddy/A")
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
}

// --- per-AuthID 隔离 ---

func TestRegistry_PerAuthIDIsolation(t *testing.T) {
	reg := NewRegistry()
	reg.Store("auth-1", map[string]string{"workbuddy/X": "x"})
	reg.Store("auth-2", map[string]string{"workbuddy/X": "x2"})
	got1, _ := reg.ResolveModel("auth-1", "workbuddy/X")
	got2, _ := reg.ResolveModel("auth-2", "workbuddy/X")
	if got1 != "x" || got2 != "x2" {
		t.Errorf("isolation broken: auth-1=%q auth-2=%q", got1, got2)
	}
}

func TestRegistry_StoreOverwrite(t *testing.T) {
	reg := NewRegistry()
	reg.Store("auth-1", map[string]string{"workbuddy/A": "a"})
	reg.Store("auth-1", map[string]string{"workbuddy/B": "b"})
	got, _ := reg.ResolveModel("auth-1", "workbuddy/A")
	if got != "" {
		t.Errorf("old mapping should be gone, got %q", got)
	}
	got, _ = reg.ResolveModel("auth-1", "workbuddy/B")
	if got != "b" {
		t.Errorf("got %q, want b", got)
	}
}

func TestRegistry_StoreEmptyAuthID(t *testing.T) {
	reg := NewRegistry()
	reg.Store("", map[string]string{"workbuddy/A": "a"})
	// 不应 panic，且不应有数据
	_, err := reg.ResolveModel("", "workbuddy/A")
	if err == nil {
		t.Fatal("expected error for empty authID")
	}
}

// --- ModelStatic ---

func TestModelStatic(t *testing.T) {
	if got := ModelStatic(); got != nil {
		t.Errorf("ModelStatic() = %v, want nil", got)
	}
}

// --- 并发读写 ---

func TestRegistry_ConcurrentReadWrite(t *testing.T) {
	reg := NewRegistry()
	var wg sync.WaitGroup
	// 并发写
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			authID := fmt.Sprintf("auth-%d", i)
			mapping := map[string]string{
				fmt.Sprintf("workbuddy/Model-%d", i): fmt.Sprintf("model-%d", i),
			}
			reg.Store(authID, mapping)
		}(i)
	}
	// 并发读
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			authID := fmt.Sprintf("auth-%d", i)
			publicID := fmt.Sprintf("workbuddy/Model-%d", i)
			got, err := reg.ResolveModel(authID, publicID)
			if err == nil && got != fmt.Sprintf("model-%d", i) {
				t.Errorf("concurrent read auth-%d: got %q", i, got)
			}
		}(i)
	}
	wg.Wait()
}

func TestRegistry_ConcurrentOverwrite(t *testing.T) {
	reg := NewRegistry()
	var wg sync.WaitGroup
	// 同一个 authID 并发写不同映射
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reg.Store("shared", map[string]string{
				fmt.Sprintf("workbuddy/M-%d", i): fmt.Sprintf("m-%d", i),
			})
		}(i)
	}
	// 并发读
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			publicID := fmt.Sprintf("workbuddy/M-%d", i)
			got, err := reg.ResolveModel("shared", publicID)
			if err == nil && got != fmt.Sprintf("m-%d", i) {
				t.Errorf("concurrent overwrite: got %q for %s", got, publicID)
			}
		}(i)
	}
	wg.Wait()
}
