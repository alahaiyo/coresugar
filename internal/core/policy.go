package core

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var symbolOrAddress = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_.]*|(?:0x)?[0-9a-fA-F]+)(?:\+0x[0-9a-fA-F]+)?$`)

func ValidateCommand(command string) error {
	if len(command) == 0 || len(command) > 1024 {
		return fmt.Errorf("COMMAND_REJECTED: command length must be 1..1024 bytes")
	}
	for _, character := range command {
		if unicode.IsControl(character) || (unicode.IsSpace(character) && character != ' ') || strings.ContainsRune(";&|><`$'\"\\(){}[]=", character) {
			return fmt.Errorf("COMMAND_REJECTED: control characters, expressions and shell syntax are forbidden")
		}
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return fmt.Errorf("COMMAND_REJECTED: empty command")
	}
	args := fields[1:]
	valid := false
	switch fields[0] {
	case "sys", "log":
		valid = len(args) == 0
	case "kmem":
		valid = len(args) == 1 && args[0] == "-i"
	case "ps":
		valid = len(args) == 0 || len(args) == 1 && (args[0] == "-m" || args[0] == "-p" || args[0] == "-l")
	case "bt":
		if len(args) > 0 && (args[0] == "-a" || args[0] == "-f" || args[0] == "-l" || args[0] == "-p") {
			args = args[1:]
		}
		valid = len(args) == 0 || len(args) == 1 && symbolOrAddress.MatchString(args[0])
	case "dis":
		if len(args) > 0 && (args[0] == "-l" || args[0] == "-r" || args[0] == "-f") {
			args = args[1:]
		}
		valid = len(args) >= 1 && len(args) <= 2 && symbolOrAddress.MatchString(args[0])
		if valid && len(args) == 2 {
			count, err := strconv.Atoi(args[1])
			valid = err == nil && count > 0 && count <= 256
		}
	case "sym", "whatis", "p":
		valid = len(args) == 1 && symbolOrAddress.MatchString(args[0])
	case "struct":
		valid = len(args) == 2 && symbolOrAddress.MatchString(args[0]) && symbolOrAddress.MatchString(args[1])
	case "rd":
		if len(args) > 0 && (args[0] == "-x" || args[0] == "-d" || args[0] == "-u") {
			args = args[1:]
		}
		if len(args) == 2 {
			count, err := strconv.Atoi(args[1])
			valid = symbolOrAddress.MatchString(args[0]) && err == nil && count > 0 && count <= 256
		}
	}
	if !valid {
		return fmt.Errorf("COMMAND_REJECTED: unsupported command or arguments")
	}
	return nil
}
