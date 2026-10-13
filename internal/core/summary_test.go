package core

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRecordAnalysisStep 验证打开会话自动创建 Markdown、中文多段摘要和证据链接，
// 顺序编号及文件权限；关闭并重启 Runtime 后仍能追加停止原因，不覆盖先前记录。
func TestRecordAnalysisStep(t *testing.T) {
	runtime := testRuntime(t)
	if runtime == nil {
		return
	}
	id := "cs-summary-case"
	if err := runtime.prepareEvidence(SessionInfo{ID: id, Status: "READY"}, "crash ready"); err != nil {
		t.Errorf("prepare: %v", err)
		return
	}
	path := filepath.Join(runtime.config.EvidenceDir, id, "analysis-summary.md")
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "# 调查过程总结") {
		t.Errorf("initial summary: %s, %v", data, err)
	}
	evidence, err := runtime.saveEvidence(id, "crash_exec", "bt", "RCX: 0000000000000000", "SUCCESS", "", false, time.Now())
	if err != nil {
		t.Errorf("evidence: %v", err)
		return
	}
	request := AnalysisStepRequest{SessionID: id, Title: "查看调用栈和寄存器", Summary: "执行 bt，定位到 transfer_surpluses+878，发现 RCX 为 0。\n\n尚未确认该寄存器对应哪个源码变量。", EvidenceIDs: []string{evidence}}
	result, err := runtime.RecordAnalysisStep(request)
	if err != nil || result.Step != 1 || result.Path != path {
		t.Errorf("first step: %+v, %v", result, err)
		return
	}
	runtime.Close()
	restarted, err := NewRuntime(runtime.config)
	if err != nil {
		t.Errorf("restart: %v", err)
		return
	}
	defer restarted.Close()
	result, err = restarted.RecordAnalysisStep(AnalysisStepRequest{SessionID: id, Title: "记录停止原因", Summary: "本轮只完成工具链验证，尚未继续读取相关内存对象；不能视为根因已经查清。"})
	if err != nil || result.Step != 2 {
		t.Errorf("after restart: %+v, %v", result, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read summary: %v", err)
		return
	}
	for _, text := range []string{"1. **查看调用栈和寄存器**", "   执行 bt", "   尚未确认", "2. **记录停止原因**", "不能视为根因已经查清", "[" + evidence + "](" + evidence + ".txt)"} {
		if !strings.Contains(string(data), text) {
			t.Errorf("summary lacks %q: %s", text, data)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("summary permissions: %v, %v", info, err)
	}
}

// TestRecordAnalysisStepInvalid 验证空标题/内容、超长摘要、多行标题、非法会话路径，
// 以及不存在或属于其他会话的证据被拒绝，原始 Markdown 不被修改。
func TestRecordAnalysisStepInvalid(t *testing.T) {
	runtime := testRuntime(t)
	if runtime == nil {
		return
	}
	id := "cs-summary-validation"
	if err := runtime.prepareEvidence(SessionInfo{ID: id}, ""); err != nil {
		t.Errorf("prepare: %v", err)
		return
	}
	other, err := runtime.saveEvidence("cs-other-case", "crash_exec", "sys", "other kernel", "SUCCESS", "", false, time.Now())
	if err != nil {
		t.Errorf("other evidence: %v", err)
		return
	}
	for _, scenario := range []struct {
		name   string
		change func(*AnalysisStepRequest)
	}{
		{"empty-title", func(request *AnalysisStepRequest) { request.Title = " " }},
		{"empty-summary", func(request *AnalysisStepRequest) { request.Summary = "" }},
		{"multiline-title", func(request *AnalysisStepRequest) { request.Title = "标题\n第二行" }},
		{"oversized-summary", func(request *AnalysisStepRequest) { request.Summary = strings.Repeat("内容", 3000) }},
		{"parent-path", func(request *AnalysisStepRequest) { request.SessionID = "../outside-case" }},
		{"unknown-session", func(request *AnalysisStepRequest) { request.SessionID = "cs-never-opened" }},
		{"unknown-evidence", func(request *AnalysisStepRequest) { request.EvidenceIDs = []string{"ev-not-recorded"} }},
		{"other-case-evidence", func(request *AnalysisStepRequest) { request.EvidenceIDs = []string{other} }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := AnalysisStepRequest{SessionID: id, Title: "读取系统信息", Summary: "执行 sys，核对系统信息。"}
			scenario.change(&request)
			if _, err := runtime.RecordAnalysisStep(request); err == nil {
				t.Errorf("invalid step accepted: %+v", request)
			}
			data, err := os.ReadFile(filepath.Join(runtime.config.EvidenceDir, id, "analysis-summary.md"))
			if err != nil || string(data) != analysisSummaryHeader(id) {
				t.Errorf("invalid step changed summary: %s, %v", data, err)
			}
		})
	}
}

// TestRecordAnalysisStepConcurrent 验证两个 Runtime 并发提交阶段总结时，
// 编号连续且不重复，所有正文均被保留，十位数编号的正文正确缩进。
func TestRecordAnalysisStepConcurrent(t *testing.T) {
	runtime := testRuntime(t)
	if runtime == nil {
		return
	}
	id := "cs-concurrent-summary"
	if err := runtime.prepareEvidence(SessionInfo{ID: id}, ""); err != nil {
		t.Errorf("prepare: %v", err)
		return
	}
	other, err := NewRuntime(runtime.config)
	if err != nil {
		t.Errorf("other runtime: %v", err)
		return
	}
	defer other.Close()
	writers := []*Runtime{runtime, other}
	var group sync.WaitGroup
	steps := make(chan int, 12)
	for index := range 12 {
		writer := writers[index%len(writers)]
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := writer.RecordAnalysisStep(AnalysisStepRequest{SessionID: id, Title: "核对故障现场", Summary: "读取寄存器并核对对应指令。"})
			if err != nil {
				t.Errorf("append: %v", err)
				return
			}
			steps <- result.Step
		}()
	}
	group.Wait()
	close(steps)
	seen := make(map[int]bool)
	for step := range steps {
		if seen[step] {
			t.Errorf("duplicate step: %d", step)
		}
		seen[step] = true
	}
	data, err := os.ReadFile(filepath.Join(runtime.config.EvidenceDir, id, "analysis-summary.md"))
	if err != nil || len(seen) != cap(steps) || !seen[cap(steps)] || strings.Count(string(data), "读取寄存器") != cap(steps) || !strings.Contains(string(data), "10. **核对故障现场**\n\n    读取寄存器") {
		t.Errorf("lost/reordered summary: %v, %s, %v", seen, data, err)
	}
}

// TestRecordAnalysisStepUnsafeFile 验证摘要文件及会话目录的符号链接被拒绝，
// 同名目录导致写入失败，目录外的文件始终保持原样。
func TestRecordAnalysisStepUnsafeFile(t *testing.T) {
	for _, scenario := range []string{"file-symlink", "session-symlink", "directory-summary"} {
		t.Run(scenario, func(t *testing.T) {
			runtime := testRuntime(t)
			if runtime == nil {
				return
			}
			outside := t.TempDir()
			protected := filepath.Join(outside, "protected.md")
			writeFixture(t, protected, []byte("unchanged"))
			id := "cs-protected-summary"
			var err error
			if scenario == "session-symlink" {
				err = os.Symlink(outside, filepath.Join(runtime.config.EvidenceDir, id))
			} else {
				err = os.Mkdir(filepath.Join(runtime.config.EvidenceDir, id), 0o700)
				if err == nil {
					path := filepath.Join(runtime.config.EvidenceDir, id, "analysis-summary.md")
					if scenario == "file-symlink" {
						err = os.Symlink(protected, path)
					} else {
						err = os.Mkdir(path, 0o700)
					}
				}
			}
			if err != nil {
				t.Errorf("setup: %v", err)
				return
			}
			if _, err := runtime.RecordAnalysisStep(AnalysisStepRequest{SessionID: id, Title: "核对材料", Summary: "读取内核版本。"}); err == nil {
				t.Errorf("unsafe summary accepted")
			}
			data, err := os.ReadFile(protected)
			if err != nil || string(data) != "unchanged" {
				t.Errorf("outside file modified: %s, %v", data, err)
			}
		})
	}
}
