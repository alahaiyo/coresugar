package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Open each component relative to its parent descriptor without following symlinks.
// This avoids the check/open race of EvalSymlinks followed by os.Open.
func openRelative(root, relative string, directory bool) (*os.File, error) {
	if filepath.IsAbs(relative) || strings.ContainsRune(relative, 0) {
		return nil, fmt.Errorf("COMMAND_REJECTED: path must be relative")
	}
	for _, part := range strings.Split(relative, string(os.PathSeparator)) {
		if part == ".." {
			return nil, fmt.Errorf("COMMAND_REJECTED: parent traversal is forbidden")
		}
	}
	descriptor, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	clean := filepath.Clean(relative)
	if clean == "." {
		if directory {
			return os.NewFile(uintptr(descriptor), root), nil
		}
		unix.Close(descriptor)
		return nil, fmt.Errorf("expected a relative file path")
	}
	parts := strings.Split(clean, string(os.PathSeparator))
	for index, part := range parts {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if index < len(parts)-1 || directory {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(descriptor, part, flags, 0)
		unix.Close(descriptor)
		if err != nil {
			return nil, fmt.Errorf("COMMAND_REJECTED: path inaccessible or contains symlink: %w", err)
		}
		descriptor = next
	}
	file := os.NewFile(uintptr(descriptor), relative)
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !directory && !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("expected a regular file")
	}
	return file, nil
}
