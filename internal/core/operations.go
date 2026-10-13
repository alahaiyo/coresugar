package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// OperationRecord describes an observed tool call, rather than Agent reasoning.
type OperationRecord struct {
	CallID     string          `json:"call_id"`
	Timestamp  time.Time       `json:"timestamp"`
	Phase      string          `json:"phase"`
	Tool       string          `json:"tool"`
	SessionID  string          `json:"session_id,omitempty"`
	Client     string          `json:"client,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Status     string          `json:"status,omitempty"`
	Error      string          `json:"error,omitempty"`
	DurationMS int64           `json:"duration_ms"`
}

func openLogFile(directory *os.File, name string) (*os.File, error) {
	descriptor, err := unix.Openat(int(directory.Fd()), name, unix.O_RDWR|unix.O_APPEND|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), name)
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("log must be a regular file")
	}
	return file, nil
}

func appendOperation(directory *os.File, data []byte) error {
	// Lock before creating the log, including the first append from multiple servers.
	if err := unix.Flock(int(directory.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock operation directory: %w", err)
	}
	defer unix.Flock(int(directory.Fd()), unix.LOCK_UN)
	file, err := openLogFile(directory, "operations.jsonl")
	if err != nil {
		return err
	}
	defer file.Close()
	count, err := file.Write(data)
	if err != nil {
		return err
	}
	if count != len(data) {
		return io.ErrShortWrite
	}
	return file.Sync()
}

// LogOperation keeps a complete index and a copy for each existing dump session.
// Failed opens and invalid session IDs remain visible in the root index.
func (runtime *Runtime) LogOperation(record OperationRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	runtime.logMu.Lock()
	defer runtime.logMu.Unlock()
	root, err := openRelative(runtime.config.EvidenceDir, ".", true)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := appendOperation(root, data); err != nil {
		return err
	}
	if !evidenceIdentifier.MatchString(record.SessionID) {
		return nil
	}
	directory, err := openRelative(runtime.config.EvidenceDir, record.SessionID, true)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer directory.Close()
	return appendOperation(directory, data)
}
