package core

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestEvidence 验证证据落盘、字节分页重组、末尾空页、无效 ID/偏移/限额、
// 超时与采集截断标记，以及重新创建 Runtime 后证据仍可查询。
func TestEvidence(t *testing.T) {
	runtime := testRuntime(t)
	if runtime == nil {
		return
	}
	content := "内核 panic evidence\n"
	id, err := runtime.saveEvidence("cs-test-session", "crash", "log", content, "ERROR", "timeout", true, time.Now())
	if err != nil {
		t.Errorf("save evidence: %v", err)
		return
	}
	var assembled []byte
	var offset int64
	for {
		page, err := runtime.GetEvidence(EvidenceRequest{SessionID: "cs-test-session", EvidenceID: id, Offset: offset, Limit: 4})
		if err != nil {
			t.Errorf("page: %v", err)
			return
		}
		data := []byte(page.Output)
		if page.OutputBase64 != "" {
			data, err = base64.StdEncoding.DecodeString(page.OutputBase64)
			if err != nil {
				t.Errorf("base64: %v", err)
				return
			}
		}
		assembled = append(assembled, data...)
		if page.Complete {
			break
		}
		if page.NextOffset == nil {
			t.Errorf("no next offset")
			return
		}
		offset = *page.NextOffset
	}
	if string(assembled) != content {
		t.Errorf("reassembled = %q", assembled)
	}
	restarted, err := NewRuntime(runtime.config)
	if err != nil {
		t.Errorf("restart: %v", err)
		return
	}
	defer restarted.Close()
	page, err := restarted.GetEvidence(EvidenceRequest{SessionID: "cs-test-session", EvidenceID: id, Offset: int64(len(content))})
	if err != nil || !page.Complete || !page.CaptureTruncated || page.Status != "ERROR" {
		t.Errorf("restart/eof: %+v %v", page, err)
	}
	for _, request := range []EvidenceRequest{{SessionID: "../outside", EvidenceID: id}, {SessionID: "cs-test-session", EvidenceID: "ev-not-found"}, {SessionID: "cs-test-session", EvidenceID: id, Offset: -1}, {SessionID: "cs-test-session", EvidenceID: id, Offset: 999999}, {SessionID: "cs-test-session", EvidenceID: id, Limit: 1 << 20}} {
		if _, err := runtime.GetEvidence(request); err == nil {
			t.Errorf("invalid evidence request accepted: %+v", request)
		}
	}
}

// TestEvidenceWriteFailure 验证证据目录被替换为文件时返回写入错误，不返回可引用的证据编号。
func TestEvidenceWriteFailure(t *testing.T) {
	runtime := testRuntime(t)
	if runtime == nil {
		return
	}
	path := filepath.Join(t.TempDir(), "blocked-evidence")
	writeFixture(t, path, []byte("not a directory"))
	runtime.config.EvidenceDir = path
	if id, err := runtime.saveEvidence("cs-test-session", "crash", "sys", "output", "SUCCESS", "", false, time.Now()); err == nil || id != "" {
		t.Errorf("write failure: id=%s err=%v", id, err)
	}
}

// TestEvidenceSymlink 验证落盘输出被替换为目录外符号链接时，不会返回目录外文件内容。
func TestEvidenceSymlink(t *testing.T) {
	runtime := testRuntime(t)
	if runtime == nil {
		return
	}
	id, err := runtime.saveEvidence("cs-test-session", "crash", "sys", "kernel output", "SUCCESS", "", false, time.Now())
	if err != nil {
		t.Errorf("save: %v", err)
		return
	}
	path := filepath.Join(runtime.config.EvidenceDir, "cs-test-session", id+".txt")
	outside := filepath.Join(t.TempDir(), "outside-output")
	writeFixture(t, outside, []byte("kernel output"))
	if err := os.Remove(path); err != nil {
		t.Errorf("remove own fixture: %v", err)
		return
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Errorf("symlink: %v", err)
		return
	}
	if _, err := runtime.GetEvidence(EvidenceRequest{SessionID: "cs-test-session", EvidenceID: id}); err == nil {
		t.Errorf("escaping evidence link accepted")
	}
}
