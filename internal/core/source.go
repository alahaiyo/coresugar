package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

type SourceRequest struct {
	SessionID string `json:"session_id"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type SourceResult struct {
	File           string `json:"file"`
	StartLine      int    `json:"start_line"`
	EndLine        int    `json:"end_line"`
	Content        string `json:"content"`
	SourceIdentity string `json:"source_identity"`
	SourceStatus   string `json:"source_status"`
	EvidenceID     string `json:"evidence_id"`
}

func (runtime *Runtime) sourceRoot(id string) (string, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	item := runtime.sessions[id]
	if item == nil {
		return "", fmt.Errorf("SESSION_NOT_FOUND: %s", id)
	}
	if item.info.Status != "READY" && item.info.Status != "BUSY" {
		return "", fmt.Errorf("SESSION_FAILED: source access requires an active session")
	}
	item.sourceOps++
	item.info.LastActivity = time.Now().UTC()
	return item.info.Validation.Inputs.SourceDir, nil
}

func (runtime *Runtime) sourcePath(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	longest := ""
	replacement := ""
	for prefix, target := range runtime.config.SourceMappings {
		if (path == prefix || strings.HasPrefix(path, prefix+string(filepath.Separator))) && len(prefix) > len(longest) {
			longest = prefix
			replacement = target
		}
	}
	if longest != "" {
		return filepath.Join(replacement, strings.TrimPrefix(strings.TrimPrefix(path, longest), string(filepath.Separator)))
	}
	return path
}

func readSourceFile(root, relative string) ([]byte, error) {
	file, err := openRelative(root, relative, false)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 8<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 8<<20 {
		return nil, fmt.Errorf("SOURCE_LIMIT: file exceeds 8 MiB")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("SOURCE_INVALID: binary file")
	}
	return data, nil
}

func (runtime *Runtime) ReadSource(request SourceRequest) (SourceResult, error) {
	started := time.Now()
	if request.StartLine < 1 || request.EndLine < request.StartLine || request.EndLine-request.StartLine >= 300 {
		return SourceResult{}, fmt.Errorf("INVALID_ARGUMENT: source range must contain 1..300 lines")
	}
	root, err := runtime.sourceRoot(request.SessionID)
	if err != nil {
		return SourceResult{}, err
	}
	defer runtime.sourceFinished(request.SessionID)
	path := runtime.sourcePath(request.File)
	data, err := readSourceFile(root, path)
	if err != nil {
		return SourceResult{}, err
	}
	lines := bytes.Split(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] == '\n' {
		lines = lines[:len(lines)-1]
	}
	if request.StartLine > len(lines) {
		return SourceResult{}, fmt.Errorf("INVALID_ARGUMENT: start line beyond end of file")
	}
	end := request.EndLine
	if end > len(lines) {
		end = len(lines)
	}
	var content strings.Builder
	for index := request.StartLine - 1; index < end; index++ {
		fmt.Fprintf(&content, "%d: %s\n", index+1, lines[index])
		if content.Len() > runtime.config.MaxResponseBytes {
			return SourceResult{}, fmt.Errorf("OUTPUT_LIMIT_EXCEEDED: reduce source line range")
		}
	}
	hash := sha256.Sum256(data)
	result := SourceResult{File: filepath.Clean(path), StartLine: request.StartLine, EndLine: end, Content: content.String(), SourceIdentity: "sha256:" + hex.EncodeToString(hash[:]), SourceStatus: "PARTIAL"}
	encoded, err := json.Marshal(result)
	if err != nil {
		return SourceResult{}, err
	}
	result.EvidenceID, err = runtime.saveEvidence(request.SessionID, "read_source", fmt.Sprintf("%s:%d-%d", result.File, result.StartLine, result.EndLine), string(encoded), "SUCCESS", "", false, started)
	if err != nil {
		return SourceResult{}, fmt.Errorf("EVIDENCE_WRITE_FAILED: %w", err)
	}
	return result, nil
}

type SearchRequest struct {
	SessionID  string `json:"session_id"`
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

type SourceMatch struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}
type SearchResult struct {
	Matches      []SourceMatch `json:"matches"`
	Truncated    bool          `json:"truncated"`
	LimitReason  string        `json:"limit_reason,omitempty"`
	SourceStatus string        `json:"source_status"`
	EvidenceID   string        `json:"evidence_id"`
}

func (runtime *Runtime) SearchSource(ctx context.Context, request SearchRequest) (SearchResult, error) {
	started := time.Now()
	maximum := request.MaxResults
	if maximum == 0 {
		maximum = 20
	}
	if request.Pattern == "" || len(request.Pattern) > 256 || maximum < 1 || maximum > 100 {
		return SearchResult{}, fmt.Errorf("INVALID_ARGUMENT: pattern length 1..256 and max_results 1..100 required")
	}
	root, err := runtime.sourceRoot(request.SessionID)
	if err != nil {
		return SearchResult{}, err
	}
	defer runtime.sourceFinished(request.SessionID)
	path := request.Path
	if path == "" {
		path = "."
	}
	path = runtime.sourcePath(path)
	directory, err := openRelative(root, path, true)
	if err != nil {
		return SearchResult{}, err
	}
	directory.Close()
	searchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result := SearchResult{Matches: []SourceMatch{}, SourceStatus: "PARTIAL"}
	var scanned int64
	var outputBytes int
	err = filepath.WalkDir(filepath.Join(root, path), func(full string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if searchCtx.Err() != nil {
			return searchCtx.Err()
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		relative, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		data, err := readSourceFile(root, relative)
		if err != nil {
			if strings.HasPrefix(err.Error(), "SOURCE_INVALID:") {
				return nil
			}
			if strings.HasPrefix(err.Error(), "SOURCE_LIMIT:") {
				result.Truncated = true
				result.LimitReason = "file size limit"
				return nil
			}
			return err
		}
		scanned += int64(len(data))
		if scanned > 256<<20 {
			result.Truncated = true
			result.LimitReason = "scan exceeds 256 MiB; narrow path"
			return filepath.SkipAll
		}
		for index, line := range bytes.Split(data, []byte{'\n'}) {
			if !bytes.Contains(line, []byte(request.Pattern)) {
				continue
			}
			if len(result.Matches) >= maximum {
				result.Truncated = true
				result.LimitReason = "max_results"
				return filepath.SkipAll
			}
			if len(line) > 2048 {
				line = line[:2048]
				result.Truncated = true
				result.LimitReason = "matching line exceeds 2048 bytes"
			}
			match := SourceMatch{File: relative, Line: index + 1, Text: string(line)}
			encoded, _ := json.Marshal(match)
			if outputBytes+len(encoded) > runtime.config.MaxResponseBytes-1024 {
				result.Truncated = true
				result.LimitReason = "response byte limit"
				return filepath.SkipAll
			}
			outputBytes += len(encoded)
			result.Matches = append(result.Matches, match)
		}
		return nil
	})
	if err != nil {
		return SearchResult{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return SearchResult{}, err
	}
	result.EvidenceID, err = runtime.saveEvidence(request.SessionID, "search_source", request.Pattern+" in "+path, string(encoded), "SUCCESS", "", result.Truncated, started)
	if err != nil {
		return SearchResult{}, fmt.Errorf("EVIDENCE_WRITE_FAILED: %w", err)
	}
	return result, nil
}
