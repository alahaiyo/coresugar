package core

import (
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type AnalysisStepRequest struct {
	SessionID   string   `json:"session_id"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

type AnalysisStepResult struct {
	SessionID string `json:"session_id"`
	Step      int    `json:"step"`
	Path      string `json:"path"`
}

var summaryStep = regexp.MustCompile(`(?m)^[0-9]+\. \*\*`)

func analysisSummaryHeader(id string) string {
	return "# 调查过程总结\n\n会话：`" + id + "`\n\n以下记录由 Agent 根据实际操作与证据提交，原始工具调用保存在 operations.jsonl。\n\n"
}

// RecordAnalysisStep persists an Agent's account, without inferring findings from
// command names. Disk-based numbering works after session closure or restart.
func (runtime *Runtime) RecordAnalysisStep(request AnalysisStepRequest) (AnalysisStepResult, error) {
	request.Title = strings.TrimSpace(request.Title)
	request.Summary = strings.TrimSpace(request.Summary)
	if !strings.HasPrefix(request.SessionID, "cs-") || !evidenceIdentifier.MatchString(request.SessionID) || request.Title == "" || len(request.Title) > 512 || strings.ContainsAny(request.Title, "\r\n") || request.Summary == "" || len(request.Summary) > 16384 || !utf8.ValidString(request.Title) || !utf8.ValidString(request.Summary) || len(request.EvidenceIDs) > 32 {
		return AnalysisStepResult{}, fmt.Errorf("INVALID_ARGUMENT: require a session ID, single-line title (1..512 bytes), summary (1..16384 bytes), and at most 32 evidence IDs")
	}
	for _, id := range request.EvidenceIDs {
		if _, err := runtime.GetEvidence(EvidenceRequest{SessionID: request.SessionID, EvidenceID: id, Limit: 1}); err != nil {
			return AnalysisStepResult{}, fmt.Errorf("INVALID_ARGUMENT: summary evidence %s: %w", id, err)
		}
	}
	directory, err := openRelative(runtime.config.EvidenceDir, request.SessionID, true)
	if err != nil {
		return AnalysisStepResult{}, fmt.Errorf("SESSION_NOT_FOUND: %w", err)
	}
	defer directory.Close()
	if err := unix.Flock(int(directory.Fd()), unix.LOCK_EX); err != nil {
		return AnalysisStepResult{}, fmt.Errorf("SUMMARY_WRITE_FAILED: %w", err)
	}
	defer unix.Flock(int(directory.Fd()), unix.LOCK_UN)
	file, err := openLogFile(directory, "analysis-summary.md")
	if err != nil {
		return AnalysisStepResult{}, fmt.Errorf("SUMMARY_WRITE_FAILED: %w", err)
	}
	defer file.Close()
	const maxSummaryBytes = 1 << 20
	data, err := io.ReadAll(io.LimitReader(file, maxSummaryBytes+1))
	if err != nil {
		return AnalysisStepResult{}, fmt.Errorf("SUMMARY_WRITE_FAILED: %w", err)
	}
	step := len(summaryStep.FindAllIndex(data, -1)) + 1
	indent := strings.Repeat(" ", len(strconv.Itoa(step))+2)
	text := ""
	if len(data) == 0 {
		text = analysisSummaryHeader(request.SessionID)
	}
	title := strings.ReplaceAll(request.Title, "*", "\\*")
	text += fmt.Sprintf("%d. **%s**\n\n%s%s\n\n", step, title, indent, strings.ReplaceAll(request.Summary, "\n", "\n"+indent))
	if len(request.EvidenceIDs) > 0 {
		var links []string
		for _, id := range request.EvidenceIDs {
			links = append(links, fmt.Sprintf("[%s](%s.txt)", id, id))
		}
		text += indent + "证据：" + strings.Join(links, "、") + "。\n\n"
	}
	if len(data)+len(text) > maxSummaryBytes {
		return AnalysisStepResult{}, fmt.Errorf("SUMMARY_WRITE_FAILED: summary exceeds 1 MiB")
	}
	if count, err := file.WriteString(text); err != nil {
		return AnalysisStepResult{}, fmt.Errorf("SUMMARY_WRITE_FAILED: %w", err)
	} else if count != len(text) {
		return AnalysisStepResult{}, fmt.Errorf("SUMMARY_WRITE_FAILED: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return AnalysisStepResult{}, fmt.Errorf("SUMMARY_WRITE_FAILED: %w", err)
	}
	return AnalysisStepResult{SessionID: request.SessionID, Step: step, Path: filepath.Join(runtime.config.EvidenceDir, request.SessionID, "analysis-summary.md")}, nil
}
