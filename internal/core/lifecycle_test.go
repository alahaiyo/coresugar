package core

import (
	"context"
	"testing"
	"time"
)

// TestIndependentSessions 验证一个会话执行缓慢命令时另一个可执行，
// 输出与证据编号归属各自会话，关闭第一个不会影响第二个。
func TestIndependentSessions(t *testing.T) {
	runtime := testRuntime(t)
	if runtime == nil {
		return
	}
	alpha := attachDebugger(t, runtime, "hang", "cs-alpha-session")
	beta := attachDebugger(t, runtime, "normal", "cs-beta-session")
	if alpha == "" || beta == "" {
		return
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runtime.Exec(context.Background(), ExecRequest{SessionID: alpha, Command: "sys"})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		info, _ := runtime.Get(alpha)
		if info.Status == "BUSY" {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("alpha did not start")
			return
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := runtime.Exec(ctx, ExecRequest{SessionID: beta, Command: "log"})
	if err != nil || result.SessionID != beta || result.EvidenceID == "" {
		t.Errorf("independent execution: %+v %v", result, err)
	}
	runtime.CloseSession(alpha)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Errorf("alpha close stalled")
	}
	info, err := runtime.Get(beta)
	if err != nil || info.Status != "READY" {
		t.Errorf("beta changed: %+v %v", info, err)
	}
}

// TestReapIdle 验证空闲会话回收并释放进程，执行中及读取源码中的会话不提前回收，
// 已关闭会话状态过期后释放内存，落盘证据不随状态回收删除。
func TestReapIdle(t *testing.T) {
	runtime := testRuntime(t)
	if runtime == nil {
		return
	}
	id := attachDebugger(t, runtime, "normal", "cs-idle-session")
	if id == "" {
		return
	}
	runtime.mu.Lock()
	debugger := runtime.sessions[id].debugger
	runtime.sessions[id].info.Status = "BUSY"
	runtime.mu.Unlock()
	runtime.reapIdle(time.Now().Add(time.Hour))
	if !debugger.Alive() {
		t.Errorf("busy session reaped")
	}
	runtime.mu.Lock()
	runtime.sessions[id].info.Status = "READY"
	runtime.sessions[id].sourceOps = 1
	runtime.mu.Unlock()
	runtime.reapIdle(time.Now().Add(time.Hour))
	if !debugger.Alive() {
		t.Errorf("source operation reaped")
	}
	runtime.sourceFinished(id)
	runtime.reapIdle(time.Now().Add(time.Hour))
	if debugger.Alive() {
		t.Errorf("idle process remains")
	}
	if info, _ := runtime.Get(id); info.Status != "CLOSED" {
		t.Errorf("idle status = %s", info.Status)
	}
	runtime.reapIdle(time.Now().Add(2 * time.Hour))
	if _, err := runtime.Get(id); err == nil {
		t.Errorf("expired closed state retained")
	}
}
