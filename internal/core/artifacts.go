package core

import (
	"bytes"
	"debug/elf"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Inputs struct {
	VMCore    string `json:"vmcore"`
	VMLinux   string `json:"vmlinux"`
	DebugInfo string `json:"debuginfo,omitempty"`
	SourceDir string `json:"source_dir"`
}

type Artifact struct {
	Path          string `json:"path"`
	Size          int64  `json:"size,omitempty"`
	Modified      string `json:"modified,omitempty"`
	Format        string `json:"format,omitempty"`
	Architecture  string `json:"architecture,omitempty"`
	Release       string `json:"release,omitempty"`
	KernelVersion string `json:"kernel_version,omitempty"`
	BuildID       string `json:"build_id,omitempty"`
	HasDebugInfo  bool   `json:"has_debug_info,omitempty"`
}

type Validation struct {
	Status   string   `json:"status"`
	Inputs   Inputs   `json:"inputs"`
	VMCore   Artifact `json:"vmcore"`
	VMLinux  Artifact `json:"vmlinux"`
	Warnings []string `json:"warnings,omitempty"`
	Errors   []string `json:"errors,omitempty"`
}

func Validate(inputs Inputs) Validation {
	result := Validation{Status: "PARTIAL", Inputs: inputs}
	for _, field := range []*string{&result.Inputs.VMCore, &result.Inputs.VMLinux, &result.Inputs.SourceDir, &result.Inputs.DebugInfo} {
		if *field != "" {
			if path, err := filepath.Abs(*field); err == nil {
				*field = path
			}
		}
	}
	for _, item := range []struct {
		kind, path string
		artifact   *Artifact
	}{{"vmcore", result.Inputs.VMCore, &result.VMCore}, {"vmlinux", result.Inputs.VMLinux, &result.VMLinux}} {
		artifact, err := inspectFile(item.path)
		*item.artifact = artifact
		if err != nil {
			result.Errors = append(result.Errors, item.kind+": "+err.Error())
		}
	}
	if len(result.Errors) == 0 {
		if err := inspectDump(&result.VMCore); err != nil {
			result.Errors = append(result.Errors, "vmcore: "+err.Error())
		}
		if err := inspectKernel(&result.VMLinux); err != nil {
			result.Errors = append(result.Errors, "vmlinux: "+err.Error())
		}
	}
	if result.Inputs.SourceDir == "" {
		result.Errors = append(result.Errors, "source_dir is required")
	} else {
		dir, err := os.Open(result.Inputs.SourceDir)
		if err == nil {
			var info os.FileInfo
			info, err = dir.Stat()
			if err == nil && !info.IsDir() {
				err = fmt.Errorf("not a directory")
			}
			if err == nil {
				_, err = dir.ReadDir(1)
				if err == io.EOF {
					err = nil
				}
			}
			dir.Close()
		}
		if err != nil {
			result.Errors = append(result.Errors, "source_dir: "+err.Error())
		}
	}
	if result.Inputs.DebugInfo != "" {
		if _, err := inspectFile(result.Inputs.DebugInfo); err != nil {
			result.Errors = append(result.Errors, "debuginfo: "+err.Error())
		} else {
			result.Warnings = append(result.Warnings, "separate debuginfo loading is not supported yet; supplied vmlinux must contain DWARF")
		}
	}
	if len(result.Errors) > 0 {
		result.Status = "INVALID"
		return result
	}
	if !result.VMLinux.HasDebugInfo {
		result.Status = "INVALID"
		result.Errors = append(result.Errors, "vmlinux lacks embedded DWARF; separate debug layouts are not supported yet")
		return result
	}
	if result.VMCore.Architecture != result.VMLinux.Architecture {
		result.Status = "MISMATCH"
		result.Errors = append(result.Errors, "vmcore and vmlinux architectures differ")
		return result
	}
	if result.VMCore.Release != "" && result.VMLinux.Release != "" && result.VMCore.Release != result.VMLinux.Release {
		result.Status = "MISMATCH"
		result.Errors = append(result.Errors, "vmcore and vmlinux kernel releases differ")
		return result
	}
	kernelVersion := result.VMLinux.KernelVersion
	// utsname.version is limited to 64 characters, even when linux_banner is longer.
	if len(kernelVersion) > 64 {
		kernelVersion = kernelVersion[:64]
	}
	if result.VMCore.KernelVersion != "" && kernelVersion != "" && result.VMCore.KernelVersion != kernelVersion {
		result.Status = "MISMATCH"
		result.Errors = append(result.Errors, "vmcore and vmlinux kernel build versions differ")
		return result
	}
	result.Warnings = append(result.Warnings, "exact vmcore/vmlinux build identity and source commit are not verified", "module symbols are not verified")
	return result
}

func inspectFile(path string) (Artifact, error) {
	item := Artifact{Path: path}
	if path == "" {
		return item, fmt.Errorf("path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return item, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return item, err
	}
	if !info.Mode().IsRegular() {
		return item, fmt.Errorf("not a regular file")
	}
	item.Size = info.Size()
	item.Modified = info.ModTime().UTC().Format("2006-01-02T15:04:05.999999999Z")
	return item, nil
}

func elfArchitecture(machine elf.Machine) string {
	switch machine {
	case elf.EM_X86_64:
		return "x86_64"
	case elf.EM_AARCH64:
		return "aarch64"
	case elf.EM_386:
		return "i386"
	default:
		return machine.String()
	}
}

func inspectDump(item *Artifact) error {
	file, err := os.Open(item.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 402)
	count, err := file.ReadAt(header, 0)
	if count >= 8 && string(header[:8]) == "KDUMP   " {
		if err != nil {
			return fmt.Errorf("truncated KDUMP header")
		}
		// makedumpfile disk_dump_header: signature[8], version[4], new_utsname fields[65].
		item.Format = "kdump"
		item.Release = strings.TrimRight(string(header[142:207]), "\x00")
		item.KernelVersion = strings.TrimRight(string(header[207:272]), "\x00")
		item.Architecture = strings.TrimRight(string(header[272:337]), "\x00")
		if item.Architecture == "" {
			return fmt.Errorf("KDUMP machine is empty")
		}
		return nil
	}
	image, err := elf.NewFile(file)
	if err != nil {
		return fmt.Errorf("unsupported dump format (expected KDUMP or ELF core)")
	}
	defer image.Close()
	if image.Type != elf.ET_CORE {
		return fmt.Errorf("ELF dump is not ET_CORE")
	}
	item.Format = "elf-core"
	item.Architecture = elfArchitecture(image.Machine)
	return nil
}

func inspectKernel(item *Artifact) error {
	image, err := elf.Open(item.Path)
	if err != nil {
		return err
	}
	defer image.Close()
	if image.Type != elf.ET_EXEC {
		return fmt.Errorf("vmlinux must be an ELF executable")
	}
	item.Format = "elf"
	item.Architecture = elfArchitecture(image.Machine)
	section := image.Section(".debug_info")
	item.HasDebugInfo = section != nil && section.Size > 0
	if section := image.Section(".rodata"); section != nil && section.Size <= 32<<20 {
		data, err := section.Data()
		if err != nil {
			return err
		}
		if offset := bytes.Index(data, []byte("Linux version ")); offset >= 0 {
			banner := strings.TrimSpace(string(bytes.SplitN(data[offset:], []byte{0}, 2)[0]))
			fields := strings.Fields(banner)
			if len(fields) >= 3 {
				item.Release = fields[2]
			}
			if position := strings.Index(banner, " #"); position >= 0 {
				item.KernelVersion = banner[position+1:]
			}
		}
	}
	for _, section := range image.Sections {
		if section.Type != elf.SHT_NOTE || section.Size > 1<<20 {
			continue
		}
		data, err := section.Data()
		if err != nil {
			return err
		}
		for len(data) >= 12 {
			namesize := uint64(image.ByteOrder.Uint32(data[:4]))
			descsize := uint64(image.ByteOrder.Uint32(data[4:8]))
			kind := image.ByteOrder.Uint32(data[8:12])
			nameEnd := 12 + namesize
			descStart := 12 + (namesize+3)&^uint64(3)
			end := descStart + (descsize+3)&^uint64(3)
			if end > uint64(len(data)) || nameEnd > uint64(len(data)) {
				return fmt.Errorf("invalid ELF notes")
			}
			if kind == 3 && string(bytes.TrimRight(data[12:nameEnd], "\x00")) == "GNU" {
				item.BuildID = hex.EncodeToString(data[descStart : descStart+descsize])
			}
			data = data[end:]
		}
	}
	return nil
}
