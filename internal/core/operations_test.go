package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestLogOperation 验证总日志和会话日志自动创建、关闭 Runtime 后可继续读取、
// 文件权限为 0600；未建立会话及越界 session_id 只进入总日志。
func TestLogOperation(t *testing.T) {
	runtime := testRuntime(t)
	if runtime == nil {
		return
	}
	id := "cs-operation-case"
	if err := os.Mkdir(filepath.Join(runtime.config.EvidenceDir, id), 0o700); err != nil {
		t.Errorf("create session directory: %v", err)
		return
	}
	for _, sessionID := range []string{id, "cs-not-opened", "../outside-session", ""} {
		record := OperationRecord{CallID: "call-io-cost", Timestamp: time.Now().UTC(), Phase: "finished", Tool: "get_session", SessionID: sessionID, Status: "OK"}
		if err := runtime.LogOperation(record); err != nil {
			t.Errorf("log %q: %v", sessionID, err)
		}
	}
	runtime.Close()
	for _, expected := range []struct {
		directory string
		lines     int
	}{{runtime.config.EvidenceDir, 4}, {filepath.Join(runtime.config.EvidenceDir, id), 1}} {
		path := filepath.Join(expected.directory, "operations.jsonl")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read log: %v", err)
			continue
		}
		if bytes.Count(data, []byte{'\n'}) != expected.lines {
			t.Errorf("unexpected log records: %s", data)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("log permissions: %v, %v", info, err)
		}
	}
}

// TestLogOperationConcurrent 验证两个 Runtime 共享日志目录并发追加时，
// JSON 行完整、调用编号不丢失且不重复。
func TestLogOperationConcurrent(t *testing.T) {
	config := DefaultConfig()
	config.AllowedRoots = []string{t.TempDir()}
	config.EvidenceDir = t.TempDir()
	var runtimes []*Runtime
	for range 2 {
		runtime, err := NewRuntime(config)
		if err != nil {
			t.Errorf("runtime: %v", err)
			return
		}
		t.Cleanup(runtime.Close)
		runtimes = append(runtimes, runtime)
	}
	var group sync.WaitGroup
	for index, runtime := range runtimes {
		for call := range 10 {
			group.Add(1)
			go func() {
				defer group.Done()
				record := OperationRecord{CallID: fmt.Sprintf("client-%d-call-%d", index, call), Tool: "sys", Phase: "started"}
				if err := runtime.LogOperation(record); err != nil {
					t.Errorf("append: %v", err)
				}
			}()
		}
	}
	group.Wait()
	data, err := os.ReadFile(filepath.Join(config.EvidenceDir, "operations.jsonl"))
	if err != nil {
		t.Errorf("read: %v", err)
		return
	}
	seen := make(map[string]bool)
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		var record OperationRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Errorf("invalid log line: %v", err)
		}
		if seen[record.CallID] {
			t.Errorf("duplicate call: %s", record.CallID)
		}
		seen[record.CallID] = true
	}
	if len(seen) != len(runtimes)*10 {
		t.Errorf("lost calls: %d", len(seen))
	}
}

// TestLogOperationUnsafeFile 验证日志文件和会话目录的符号链接、非普通文件被拒绝，
// 日志写入不会修改链接指向的文件。
func TestLogOperationUnsafeFile(t *testing.T) {
	for _, scenario := range []string{"file-symlink", "session-symlink", "directory-log"} {
		t.Run(scenario, func(t *testing.T) {
			runtime := testRuntime(t)
			if runtime == nil {
				return
			}
			outside := t.TempDir()
			protected := filepath.Join(outside, "protected.txt")
			writeFixture(t, protected, []byte("unchanged"))
			record := OperationRecord{Tool: "close_dump", Phase: "finished"}
			var err error
			switch scenario {
			case "file-symlink":
				err = os.Symlink(protected, filepath.Join(runtime.config.EvidenceDir, "operations.jsonl"))
			case "session-symlink":
				record.SessionID = "cs-linked-session"
				err = os.Symlink(outside, filepath.Join(runtime.config.EvidenceDir, record.SessionID))
			case "directory-log":
				err = os.Mkdir(filepath.Join(runtime.config.EvidenceDir, "operations.jsonl"), 0o700)
			}
			if err != nil {
				t.Errorf("setup: %v", err)
				return
			}
			if err := runtime.LogOperation(record); err == nil {
				t.Errorf("unsafe log accepted")
			}
			data, err := os.ReadFile(protected)
			if err != nil || string(data) != "unchanged" {
				t.Errorf("outside file modified: %s, %v", data, err)
			}
		})
	}
}
