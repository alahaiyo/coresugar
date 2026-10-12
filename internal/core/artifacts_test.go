package core

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Errorf("write fixture: %v", err)
	}
}

func kernelFixture(t *testing.T, machine elf.Machine, debug bool, release string) string {
	t.Helper()
	// Minimal ELF64 executable with string, DWARF marker, rodata and GNU Build ID sections.
	names := []byte("\x00.shstrtab\x00.debug_info\x00.rodata\x00.notes\x00")
	rodata := []byte("Linux version " + release + " (builder) #1 SMP Tue May 16 10:26:48 CST 2023\x00")
	note := make([]byte, 20)
	binary.LittleEndian.PutUint32(note, 4)
	binary.LittleEndian.PutUint32(note[4:], 4)
	binary.LittleEndian.PutUint32(note[8:], 3)
	copy(note[12:], "GNU\x00")
	copy(note[16:], []byte{0x84, 0x73, 0x61, 0x7a})
	data := make([]byte, 384+len(names)+1+len(rodata)+len(note))
	copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(data[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(data[18:], uint16(machine))
	binary.LittleEndian.PutUint32(data[20:], 1)
	binary.LittleEndian.PutUint64(data[40:], 64)
	binary.LittleEndian.PutUint16(data[52:], 64)
	binary.LittleEndian.PutUint16(data[58:], 64)
	binary.LittleEndian.PutUint16(data[60:], 5)
	binary.LittleEndian.PutUint16(data[62:], 1)
	offset := 384
	for index, section := range []struct {
		name    uint32
		kind    elf.SectionType
		content []byte
	}{{1, elf.SHT_STRTAB, names}, {11, elf.SHT_PROGBITS, []byte{1}}, {23, elf.SHT_PROGBITS, rodata}, {31, elf.SHT_NOTE, note}} {
		header := data[64+(index+1)*64:]
		binary.LittleEndian.PutUint32(header, section.name)
		binary.LittleEndian.PutUint32(header[4:], uint32(section.kind))
		binary.LittleEndian.PutUint64(header[24:], uint64(offset))
		length := len(section.content)
		if index == 1 && !debug {
			length = 0
		}
		binary.LittleEndian.PutUint64(header[32:], uint64(length))
		copy(data[offset:], section.content)
		offset += len(section.content)
	}
	path := filepath.Join(t.TempDir(), "vmlinux")
	writeFixture(t, path, data)
	return path
}

func dumpFixture(t *testing.T) string {
	t.Helper()
	header := make([]byte, 4096)
	copy(header, "KDUMP   ")
	binary.LittleEndian.PutUint32(header[8:], 6)
	copy(header[142:], "4.18.0-test")
	copy(header[207:], "#1 SMP Tue May 16 10:26:48 CST 2023")
	copy(header[272:], "x86_64")
	path := filepath.Join(t.TempDir(), "vmcore")
	writeFixture(t, path, header)
	return path
}

// TestValidate 验证输入状态：可读且版本相符仍为 PARTIAL、缺失输入、架构不符、
// 版本不符、同 release 不同构建、缺少 DWARF、缺失额外符号、源码目录错误及损坏的 vmcore。
func TestValidate(t *testing.T) {
	valid := Inputs{VMCore: dumpFixture(t), VMLinux: kernelFixture(t, elf.EM_X86_64, true, "4.18.0-test"), SourceDir: t.TempDir()}
	for _, tc := range []struct {
		name   string
		change func(*Inputs)
		status string
	}{
		{"readable-partial", func(*Inputs) {}, "PARTIAL"},
		{"required-input", func(input *Inputs) { input.VMCore = "" }, "INVALID"},
		{"different-architecture", func(input *Inputs) { input.VMLinux = kernelFixture(t, elf.EM_AARCH64, true, "4.18.0-test") }, "MISMATCH"},
		{"different-release", func(input *Inputs) { input.VMLinux = kernelFixture(t, elf.EM_X86_64, true, "6.1.0-test") }, "MISMATCH"},
		{"different-build-same-release", func(input *Inputs) {
			input.VMCore = dumpFixture(t)
			data, err := os.ReadFile(input.VMCore)
			if err != nil {
				t.Errorf("read dump header: %v", err)
				return
			}
			clear(data[207:272])
			copy(data[207:], "#1 SMP Thu May 18 17:06:28 CST 2023")
			writeFixture(t, input.VMCore, data)
		}, "MISMATCH"},
		{"without-dwarf", func(input *Inputs) { input.VMLinux = kernelFixture(t, elf.EM_X86_64, false, "4.18.0-test") }, "INVALID"},
		{"debug-file-absent", func(input *Inputs) { input.DebugInfo = filepath.Join(t.TempDir(), "vmlinux.debug") }, "INVALID"},
		{"source-is-file", func(input *Inputs) { input.SourceDir = input.VMLinux }, "INVALID"},
		{"corrupt-dump", func(input *Inputs) {
			input.VMCore = filepath.Join(t.TempDir(), "corrupt-vmcore")
			writeFixture(t, input.VMCore, []byte("invalid dump"))
		}, "INVALID"},
		{"corrupt-kernel", func(input *Inputs) { input.VMLinux = input.VMCore }, "INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := valid
			tc.change(&input)
			result := Validate(input)
			if result.Status != tc.status {
				t.Errorf("status = %s, want %s: %+v", result.Status, tc.status, result)
			}
			if tc.status == "PARTIAL" && (result.VMLinux.BuildID != "8473617a" || len(result.Warnings) == 0) {
				t.Errorf("identity/warnings: %+v", result)
			}
		})
	}
}

// TestInspectDump 验证截断 KDUMP 头、空 machine、非 core ELF 及未知文件格式被拒绝。
func TestInspectDump(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{{"truncated-header", []byte("KDUMP   ")}, {"empty-machine", append([]byte("KDUMP   "), make([]byte, 394)...)}, {"unrecognized-header", []byte("not a dump")}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-vmcore")
			writeFixture(t, path, tc.data)
			if err := inspectDump(&Artifact{Path: path}); err == nil {
				t.Errorf("invalid dump accepted")
			}
		})
	}
	if err := inspectDump(&Artifact{Path: kernelFixture(t, elf.EM_X86_64, true, "4.18.0-test")}); err == nil {
		t.Errorf("ET_EXEC accepted as dump")
	}
}
