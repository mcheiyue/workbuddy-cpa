package wbauth_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbauth"
)

// E6①：同 key 并发 Do 合并为一次上游刷新，后到者复用结果（singleflight 语义）。
func TestRefreshDo_ConcurrentMergesToOne(t *testing.T) {
	m := wbauth.NewRefreshMutex()
	var calls int32
	gate := make(chan struct{})
	fn := func() (wbauth.RefreshResult, error) {
		atomic.AddInt32(&calls, 1)
		<-gate // 执行者挂住，等第二个 goroutine 挂上等待
		return wbauth.RefreshResult{NextRefreshAfter: time.Unix(100, 0)}, nil
	}
	var wg sync.WaitGroup
	res := make([]wbauth.RefreshResult, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i], errs[i] = m.Do("auth-1", fn)
		}(i)
	}
	// 等执行者进入 fn，再给等待者 50ms 挂上，然后放行。
	for atomic.LoadInt32(&calls) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("fn calls=%d, want 1 (concurrent same-key must merge)", got)
	}
	for i := range 2 {
		if errs[i] != nil {
			t.Fatalf("res[%d] err=%v", i, errs[i])
		}
		if !res[i].NextRefreshAfter.Equal(time.Unix(100, 0)) {
			t.Fatalf("res[%d]=%v, want shared result", i, res[i])
		}
	}
}

// E6①：失败结果同样共享给等待者。
func TestRefreshDo_ErrorMessageShared(t *testing.T) {
	m := wbauth.NewRefreshMutex()
	sentinel := errors.New("refresh_failed: upstream 500")
	gate := make(chan struct{})
	var calls int32
	fn := func() (wbauth.RefreshResult, error) {
		atomic.AddInt32(&calls, 1)
		<-gate
		return wbauth.RefreshResult{}, sentinel
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = m.Do("auth-1", fn)
		}(i)
	}
	for atomic.LoadInt32(&calls) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("calls=%d, want 1", calls)
	}
	for i := range 2 {
		if !errors.Is(errs[i], sentinel) {
			t.Fatalf("errs[%d]=%v, want shared sentinel", i, errs[i])
		}
	}
}

// E6①：不同 key 不合并。
func TestRefreshDo_DistinctKeysRunSeparately(t *testing.T) {
	m := wbauth.NewRefreshMutex()
	var calls int32
	fn := func() (wbauth.RefreshResult, error) {
		atomic.AddInt32(&calls, 1)
		return wbauth.RefreshResult{}, nil
	}
	var wg sync.WaitGroup
	for _, id := range []string{"auth-a", "auth-b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := m.Do(id, fn); err != nil {
				t.Errorf("%s: %v", id, err)
			}
		}(id)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls=%d, want 2 (distinct keys must not merge)", got)
	}
}

// E6①：完成后的下一次 Do 重新执行，不复用旧结果。
func TestRefreshDo_RerunsAfterCompletion(t *testing.T) {
	m := wbauth.NewRefreshMutex()
	var calls int32
	fn := func() (wbauth.RefreshResult, error) {
		n := atomic.AddInt32(&calls, 1)
		return wbauth.RefreshResult{NextRefreshAfter: time.Unix(int64(n), 0)}, nil
	}
	r1, err := m.Do("auth-1", fn)
	if err != nil || !r1.NextRefreshAfter.Equal(time.Unix(1, 0)) {
		t.Fatalf("first: r=%v err=%v", r1, err)
	}
	r2, err := m.Do("auth-1", fn)
	if err != nil || !r2.NextRefreshAfter.Equal(time.Unix(2, 0)) {
		t.Fatalf("second must re-run: r=%v err=%v", r2, err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("calls=%d, want 2", calls)
	}
}

// E6①：空 key 不合并、不卡死（对齐 lockAuthRefresh("") 空操作语义）。
func TestRefreshDo_EmptyKeyRunsInline(t *testing.T) {
	m := wbauth.NewRefreshMutex()
	var calls int32
	fn := func() (wbauth.RefreshResult, error) {
		atomic.AddInt32(&calls, 1)
		return wbauth.RefreshResult{}, nil
	}
	if _, err := m.Do("", fn); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("calls=%d, want 1", calls)
	}
}
