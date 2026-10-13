package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
	"unicode/utf8"
)

var evidenceIdentifier = regexp.MustCompile(`^(?:cs|ev)-[A-Za-z0-9_-]{1,80}$`)

type EvidenceRecord struct {
	ID               string    `json:"evidence_id"`
	SessionID        string    `json:"session_id"`
	Tool             string    `json:"tool"`
	Command          string    `json:"command"`
	Timestamp        time.Time `json:"timestamp"`
	DurationMS       int64     `json:"duration_ms"`
	Status           string    `json:"status"`
	Error            string    `json:"error,omitempty"`
	CaptureTruncated bool      `json:"capture_truncated"`
	SHA256           string    `json:"sha256"`
	Bytes            int64     `json:"bytes"`
}

func writeNewFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	return err
}

func (runtime *Runtime) prepareEvidence(info SessionInfo, startup string) error {
	path := filepath.Join(runtime.config.EvidenceDir, info.ID)
	if err := os.Mkdir(path, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Session SessionInfo `json:"session"`
		Startup string      `json:"startup"`
	}{info, startup})
	if err != nil {
		return err
	}
	if err := writeNewFile(filepath.Join(path, "manifest.json"), data); err != nil {
		return err
	}
	return writeNewFile(filepath.Join(path, "analysis-summary.md"), []byte(analysisSummaryHeader(info.ID)))
}

func (runtime *Runtime) saveEvidence(sessionID, tool, command, output, status, executionError string, truncated bool, started time.Time) (string, error) {
	if !evidenceIdentifier.MatchString(sessionID) {
		return "", fmt.Errorf("invalid session identifier")
	}
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return "", err
	}
	id := "ev-" + hex.EncodeToString(identifier)
	path := filepath.Join(runtime.config.EvidenceDir, sessionID)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(output))
	record := EvidenceRecord{ID: id, SessionID: sessionID, Tool: tool, Command: command, Timestamp: started.UTC(), DurationMS: time.Since(started).Milliseconds(), Status: status, Error: executionError, CaptureTruncated: truncated, SHA256: hex.EncodeToString(hash[:]), Bytes: int64(len(output))}
	if err := writeNewFile(filepath.Join(path, id+".txt"), []byte(output)); err != nil {
		return "", err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	if err := writeNewFile(filepath.Join(path, id+".json"), data); err != nil {
		return "", err
	}
	file, err := os.OpenFile(filepath.Join(path, "commands.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	_, err = file.Write(append(data, '\n'))
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

type EvidenceRequest struct {
	SessionID  string `json:"session_id"`
	EvidenceID string `json:"evidence_id"`
	Offset     int64  `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type EvidencePage struct {
	EvidenceRecord
	Output       string `json:"output"`
	OutputBase64 string `json:"output_base64,omitempty"`
	Offset       int64  `json:"offset"`
	NextOffset   *int64 `json:"next_offset"`
	Complete     bool   `json:"complete"`
}

func (runtime *Runtime) GetEvidence(request EvidenceRequest) (EvidencePage, error) {
	if !evidenceIdentifier.MatchString(request.SessionID) || !evidenceIdentifier.MatchString(request.EvidenceID) || request.Offset < 0 {
		return EvidencePage{}, fmt.Errorf("INVALID_ARGUMENT: invalid evidence identifier or offset")
	}
	limit := request.Limit
	if limit == 0 {
		limit = runtime.config.MaxResponseBytes
	}
	if limit < 1 || limit > runtime.config.MaxResponseBytes {
		return EvidencePage{}, fmt.Errorf("INVALID_ARGUMENT: evidence limit out of range")
	}
	metadata, err := openRelative(runtime.config.EvidenceDir, filepath.Join(request.SessionID, request.EvidenceID+".json"), false)
	if err != nil {
		return EvidencePage{}, fmt.Errorf("EVIDENCE_NOT_FOUND: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(metadata, 65537))
	metadata.Close()
	if len(data) > 65536 {
		return EvidencePage{}, fmt.Errorf("EVIDENCE_INVALID: metadata too large")
	}
	if err != nil {
		return EvidencePage{}, fmt.Errorf("EVIDENCE_NOT_FOUND: %w", err)
	}
	var page EvidencePage
	if err := json.Unmarshal(data, &page.EvidenceRecord); err != nil {
		return page, fmt.Errorf("EVIDENCE_INVALID: %w", err)
	}
	if page.ID != request.EvidenceID || page.SessionID != request.SessionID {
		return page, fmt.Errorf("EVIDENCE_INVALID: identity mismatch")
	}
	file, err := openRelative(runtime.config.EvidenceDir, filepath.Join(request.SessionID, request.EvidenceID+".txt"), false)
	if err != nil {
		return page, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return page, err
	}
	if !info.Mode().IsRegular() || info.Size() != page.Bytes || request.Offset > page.Bytes {
		return page, fmt.Errorf("EVIDENCE_INVALID: size or offset mismatch")
	}
	buffer := make([]byte, limit)
	count, err := file.ReadAt(buffer, request.Offset)
	if err != nil && err != io.EOF {
		return page, err
	}
	buffer = buffer[:count]
	page.Output = string(buffer)
	if !utf8.Valid(buffer) {
		page.OutputBase64 = base64.StdEncoding.EncodeToString(buffer)
	}
	page.Offset = request.Offset
	next := request.Offset + int64(count)
	page.Complete = next == page.Bytes
	if !page.Complete {
		page.NextOffset = &next
	}
	return page, nil
}
