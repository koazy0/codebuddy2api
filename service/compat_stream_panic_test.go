package service

import (
	"io"
	"sync"
	"testing"
	"time"
)

// panicFlusher 复刻 gin responseWriter 的行为：它的 Flush 内部做了
// 无 ok 检查的断言，底层 writer 不支持 Flush 时必然 panic。
// 这正是线上 SIGSEGV 的直接来源。
type panicFlusher struct{}

func (panicFlusher) Write(p []byte) (int, error) { return len(p), nil }
func (panicFlusher) Flush()                      { panic("nil Flusher inside gin") }

// brokenWriter 复刻「底层 writer 已失效」：写返回错误。
type brokenWriter struct{}

func (brokenWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }

func TestSSESinkSurvivesFlushPanic(t *testing.T) {
	sink := newSSESink(panicFlusher{}, panicFlusher{})
	// 不复现 panic 的话这里就会把测试进程炸掉
	n, err := sink.Write([]byte("data: hi\n\n"))
	if err != nil {
		t.Fatalf("Write 应成功（panic 被吞掉），实际 err=%v", err)
	}
	if n != len("data: hi\n\n") {
		t.Fatalf("长度不对: %d", n)
	}
	sink.Flush() // 同样不能 panic
}

// TestHeartbeatStopWaitsForGoroutine 锁住「stop 必须等待 goroutine 退出」。
//
// 判定方式：stop() 返回之后，writer 上不允许再出现任何写入。
//
// 注意 select 的语义：done 与 ticker.C 同时就绪时 Go 随机二选一，
// 所以 close(done) 之后仍可能发生一次 Write —— 那不是逻辑漏洞
// （生产路径有 safeFlush 兜底，这一写不会 panic 掀进程）。
// 本测试只断言 stop 返回之后的静默，用延迟关闭 gate 避开上述随机窗口。
func TestHeartbeatStopWaitsForGoroutine(t *testing.T) {
	var mu sync.Mutex
	before, after := 0, 0
	gate := make(chan struct{})

	w := gatedWriter{
		before: func() { mu.Lock(); before++; mu.Unlock() },
		after:  func() { mu.Lock(); after++; mu.Unlock() },
		gate:   gate,
	}

	stop := startSSEHeartbeatInterval(w, 5*time.Millisecond)
	time.Sleep(40 * time.Millisecond) // 让心跳至少跑几轮
	mu.Lock()
	gotBefore := before
	mu.Unlock()
	if gotBefore == 0 {
		t.Fatal("心跳应至少写过一次")
	}

	stop() // 必须等 goroutine 真正退出
	// gate 在 stop 之后才关：此刻 goroutine 已退出，任何写入都算 regression
	close(gate)
	time.Sleep(60 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if after != 0 {
		t.Fatalf("stop 返回后仍有 %d 次写入", after)
	}
	t.Logf("心跳共写 %d 次，stop 后静默", gotBefore)
}

// gatedWriter 在 gate 关闭前后行为不同：之前计数，之后 panic。
type gatedWriter struct {
	before func()
	after  func()
	gate   chan struct{}
}

func (w gatedWriter) Write(p []byte) (int, error) {
	select {
	case <-w.gate:
		w.after()
	default:
		w.before()
	}
	return len(p), nil
}

type countingPanicWriter struct{ onWrite func() }

func (w countingPanicWriter) Write(p []byte) (int, error) {
	w.onWrite()
	return len(p), nil
}
