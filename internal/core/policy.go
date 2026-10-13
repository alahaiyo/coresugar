package core

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const identifier = `[A-Za-z_][A-Za-z0-9_]*`
const atom = `(?:[A-Za-z_][A-Za-z0-9_.]*|(?:0x)?[0-9a-fA-F]+)(?:\+0x[0-9a-fA-F]+)?`
const member = identifier + `(?:\[[0-9]+\])?(?:\.` + identifier + `(?:\[[0-9]+\])?)*`
const arithmeticValue = `~?` + atom + `[kKmMgG]?`
const typeCast = `\((?:(?:struct|union) )?` + identifier + `(?: ?\*)+\)`
const cpuList = `(?:[0-9]+(?:-[0-9]+)?)(?:,[0-9]+(?:-[0-9]+)?)*`

var (
	moduleName      = regexp.MustCompile(`^` + identifier + `$`)
	symbolOrAddress = regexp.MustCompile(`^` + atom + `$`)
	members         = regexp.MustCompile(`^` + member + `(?:,` + member + `)*$`)
	perCPU          = regexp.MustCompile(`^` + atom + `(?::(?:a|all|` + cpuList + `)?)?$`)
	cpus            = regexp.MustCompile(`^` + cpuList + `$`)
	offset          = regexp.MustCompile(`^(?:` + member + `|(?:0x)?[0-9a-fA-F]+)$`)
	countNumber     = regexp.MustCompile(`^-?[0-9]+$`)
	number          = regexp.MustCompile(`^(?:0x[0-9a-fA-F]+|[0-9]+)$`)
	address         = regexp.MustCompile(`^(?:0x)?[0-9a-fA-F]+$`)
	word            = regexp.MustCompile(`^[A-Za-z0-9_./:+,@-]+$`)
	treeType        = regexp.MustCompile(`^(?:radix|rbtree|xarray|maple|ra|rb|x|m)$`)
	sizeRange       = regexp.MustCompile(`^[0-9]+(?:-[0-9]+)?$`)
	// Only data references: no calls, assignment, comma expressions or GDB variables.
	dataReference = regexp.MustCompile(`^[*&]?(?:` + atom + `|` + typeCast + ` ?` + atom + `|\(` + typeCast + atom + `\))` +
		`(?:\[[0-9]+\]|\.` + identifier + `|->` + identifier + `)*(?::(?:a|all|` + cpuList + `)?)?$`)
	sizeOf     = regexp.MustCompile(`^sizeof\((?:(?:struct|union) )?` + identifier + `(?: ?\*)*\)$`)
	arithmetic = regexp.MustCompile(`^(?:` + arithmeticValue + `(?: [+*/%&^-] ` + arithmeticValue + `)?|\(` + arithmeticValue + `(?: (?:[+*/%&^|\-]|<<|>>) ` + arithmeticValue + `)?\))$`)
)

// Argument-free flags and options taking one typed argument. Unknown options
// never fall through to GDB or shell evaluation.
type commandPolicy struct {
	flags    string
	values   map[string]*regexp.Regexp
	operand  *regexp.Regexp
	min, max int
}

var commandPolicies = map[string]commandPolicy{
	"sys":      {flags: "ti", operand: word, max: 1}, // -c has an optional argument.
	"log":      {flags: "TtdmascR"},
	"bt":       {flags: "aAgrtTleEfFovpsxd", values: map[string]*regexp.Regexp{"-c": cpus, "-R": symbolOrAddress, "-I": symbolOrAddress, "-S": symbolOrAddress, "-n": regexp.MustCompile(`^idle$`)}, operand: symbolOrAddress, max: 64},
	"ps":       {flags: "kuGIsptclmagrSAHY", values: map[string]*regexp.Regexp{"-C": cpus, "-y": word}, operand: word, max: 64},
	"kmem":     {flags: "fFcCivVnzohpsSrgP", values: map[string]*regexp.Regexp{"-m": members, "-I": word}, operand: word, max: 2},
	"dis":      {flags: "rfludx", values: map[string]*regexp.Regexp{"-b": number}, operand: symbolOrAddress, min: 1, max: 2},
	"sym":      {flags: "lMpn", values: map[string]*regexp.Regexp{"-m": word, "-q": word}, operand: symbolOrAddress, max: 1},
	"whatis":   {flags: "o", values: map[string]*regexp.Regexp{"-r": sizeRange, "-m": members}, operand: members, max: 1},
	"struct":   {flags: "orfuxdp", values: map[string]*regexp.Regexp{"-l": offset, "-c": countNumber}, operand: perCPU, min: 1, max: 3},
	"union":    {flags: "orfuxdp", values: map[string]*regexp.Regexp{"-l": offset, "-c": countNumber}, operand: perCPU, min: 1, max: 3},
	"rd":       {flags: "adDsSupxmfNR", values: map[string]*regexp.Regexp{"-o": symbolOrAddress}, operand: symbolOrAddress, min: 1, max: 2},
	"task":     {flags: "dx", values: map[string]*regexp.Regexp{"-R": members}, operand: symbolOrAddress, max: 64},
	"vm":       {flags: "pvmxd", values: map[string]*regexp.Regexp{"-P": symbolOrAddress, "-M": symbolOrAddress, "-R": word, "-f": symbolOrAddress}, operand: symbolOrAddress, max: 1},
	"vtop":     {flags: "uk", values: map[string]*regexp.Regexp{"-c": symbolOrAddress}, operand: symbolOrAddress, min: 1, max: 64},
	"ptov":     {operand: perCPU, min: 1, max: 1},
	"btop":     {operand: symbolOrAddress, min: 1, max: 64},
	"ptob":     {operand: symbolOrAddress, min: 1, max: 64},
	"pte":      {operand: symbolOrAddress, min: 1, max: 64},
	"swap":     {},
	"runq":     {flags: "tTmg", values: map[string]*regexp.Regexp{"-c": cpus}},
	"timer":    {flags: "r", values: map[string]*regexp.Regexp{"-C": cpus}},
	"waitq":    {operand: offset, min: 1, max: 2},
	"irq":      {flags: "udbas", values: map[string]*regexp.Regexp{"-c": cpus}, operand: number, max: 64},
	"mach":     {flags: "mcxdo"},
	"dev":      {flags: "ipdDV"}, // -v can write a device dump to a host file.
	"files":    {flags: "c", values: map[string]*regexp.Regexp{"-d": symbolOrAddress, "-p": symbolOrAddress, "-n": symbolOrAddress, "-R": word}, operand: symbolOrAddress, max: 64},
	"fuser":    {operand: word, min: 1, max: 1},
	"mount":    {flags: "fi", values: map[string]*regexp.Regexp{"-n": symbolOrAddress}, operand: word, max: 1},
	"net":      {flags: "sSxdan", values: map[string]*regexp.Regexp{"-R": word, "-N": symbolOrAddress}, operand: symbolOrAddress, max: 1},
	"sig":      {flags: "lg", values: map[string]*regexp.Regexp{"-s": symbolOrAddress}, operand: symbolOrAddress, max: 64},
	"ipcs":     {flags: "smMq", values: map[string]*regexp.Regexp{"-n": symbolOrAddress}, operand: symbolOrAddress, max: 1},
	"bpf":      {flags: "PMtTjsxd", values: map[string]*regexp.Regexp{"-p": number, "-m": number}},
	"list":     {flags: "rBhHxd", values: map[string]*regexp.Regexp{"-o": offset, "-e": symbolOrAddress, "-s": members, "-S": members, "-l": offset, "-O": offset}, operand: symbolOrAddress, min: 1, max: 2},
	"tree":     {flags: "lpvNxd", values: map[string]*regexp.Regexp{"-t": treeType, "-r": offset, "-o": offset, "-s": members, "-S": members}, operand: symbolOrAddress, min: 1, max: 1},
	"sbitmapq": {flags: "pvxd", values: map[string]*regexp.Regexp{"-s": members, "-a": symbolOrAddress}, operand: symbolOrAddress, min: 1, max: 1},
	"search":   {flags: "kKVuptTcwh", values: map[string]*regexp.Regexp{"-s": symbolOrAddress, "-e": symbolOrAddress, "-l": number, "-m": symbolOrAddress, "-x": number}, operand: word, min: 1, max: 64},
	"ascii":    {operand: symbolOrAddress, max: 1},
}

func ValidateCommand(command string) error {
	if len(command) == 0 || len(command) > 1024 {
		return fmt.Errorf("COMMAND_REJECTED: command length must be 1..1024 bytes")
	}
	for _, character := range command {
		// crash protects parenthesized expressions from redirection. The eval
		// grammar below admits shifts and OR only inside balanced parentheses.
		if strings.ContainsRune("|<", character) && strings.HasPrefix(strings.TrimSpace(command), "eval ") {
			continue
		}
		if unicode.IsControl(character) || (unicode.IsSpace(character) && character != ' ') || strings.ContainsRune(";|<`$'\"\\{}=!", character) {
			// kmem has two documented options containing '='; validated below.
			if character == '=' && strings.HasPrefix(strings.TrimSpace(command), "kmem ") {
				continue
			}
			return fmt.Errorf("COMMAND_REJECTED: control characters and shell syntax are forbidden")
		}
	}
	fields := strings.Fields(command)
	if len(fields) == 0 || !validateFields(fields) {
		return fmt.Errorf("COMMAND_REJECTED: unsupported command or arguments; see skills/coresugar-analysis/SKILL.md")
	}
	return nil
}

func validateFields(fields []string) bool {
	name, args := fields[0], fields[1:]
	switch name {
	case "help":
		if len(args) == 0 {
			return true
		}
		if len(args) != 1 {
			return false
		}
		_, known := commandPolicies[args[0]]
		return known || strings.Contains(" help p eval set mod foreach input output all -r -D -n -o -z ", " "+args[0]+" ")
	case "dis":
		if len(args) == 1 && args[0] == "-b" || len(args) == 2 && args[0] == "-b" && number.MatchString(args[1]) {
			return true
		}
	case "set":
		return len(args) == 0 || len(args) == 1 && (address.MatchString(args[0]) || args[0] == "-p" || args[0] == "-v") || len(args) == 2 && args[0] == "-c" && number.MatchString(args[1])
	case "mod":
		return len(args) == 0 || len(args) == 1 && args[0] == "-t" || len(args) == 3 && args[0] == "-s" && moduleName.MatchString(args[1]) && word.MatchString(args[2]) && filepath.IsAbs(args[2])
	case "p":
		for len(args) > 0 && (args[0] == "-x" || args[0] == "-d" || args[0] == "-u") {
			args = args[1:]
		}
		expression := strings.Join(args, " ")
		return dataReference.MatchString(expression) || sizeOf.MatchString(expression)
	case "eval":
		for len(args) > 0 && (args[0] == "-b" || args[0] == "-l") {
			args = args[1:]
		}
		expression := strings.Join(args, " ")
		return arithmetic.MatchString(expression) && strings.Count(expression, "(") == strings.Count(expression, ")")
	case "foreach":
		for index, arg := range args {
			if strings.Contains(" bt vm task files net ps sig vtop ", " "+arg+" ") {
				policy := commandPolicies[arg]
				// foreach has its own option grammar, not the full native command grammar.
				policy.flags = map[string]string{"bt": "rtlefFoxds", "vm": "pvmxd", "task": "dx", "files": "c", "net": "sSdx", "ps": "Gsptclmagr", "sig": "g", "vtop": "cuk"}[arg]
				policy.min, policy.max = 0, 0
				policy.values = map[string]*regexp.Regexp{}
				if arg == "bt" || arg == "vm" || arg == "files" || arg == "net" {
					policy.values["-R"] = word
				}
				if arg == "task" {
					policy.values["-R"] = members
				}
				if arg == "ps" {
					policy.values["-y"] = word
				}
				if arg == "vtop" {
					policy.min, policy.max = 1, 64
				}
				return validateArguments(arg, args[index+1:], policy)
			}
			if arg == "foreach" || arg == "set" || arg == "test" || !word.MatchString(arg) || strings.HasPrefix(arg, "-") {
				return false
			}
		}
		return false
	}
	policy, ok := commandPolicies[name]
	return ok && validateArguments(name, args, policy)
}

func validateArguments(name string, args []string, policy commandPolicy) bool {
	var operands []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if name == "sys" && arg == "-c" {
			if index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
				index++
				if !word.MatchString(args[index]) {
					return false
				}
			}
			continue
		}
		if name == "kmem" && (arg == "-i=shared" || strings.HasPrefix(arg, "-S=")) {
			if arg != "-i=shared" && !cpus.MatchString(strings.TrimPrefix(arg, "-S=")) && arg != "-S=all" && arg != "-S=a" {
				return false
			}
			continue
		}
		if name == "rd" && (arg == "-8" || arg == "-16" || arg == "-32" || arg == "-64") {
			continue
		}
		if value := policy.values[arg]; value != nil {
			index++
			if index >= len(args) || !value.MatchString(args[index]) {
				return false
			}
			if (name == "struct" || name == "union") && arg == "-c" && !validStructureCount(args[index]) {
				return false
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if len(arg) == 1 {
				return false
			}
			for _, flag := range arg[1:] {
				if !strings.ContainsRune(policy.flags, flag) {
					return false
				}
			}
			continue
		}
		operands = append(operands, arg)
	}
	if len(operands) < policy.min || len(operands) > policy.max {
		return false
	}
	for index, operand := range operands {
		if (name == "struct" || name == "union") && index == 0 {
			if !members.MatchString(operand) {
				return false
			}
		} else if (name == "struct" || name == "union") && index == 2 {
			if !validStructureCount(operand) {
				return false
			}
		} else if policy.operand == nil || !policy.operand.MatchString(operand) {
			return false
		}
	}
	if name == "sys" && len(operands) != 0 && operands[0] != "config" {
		return false
	}
	if (name == "rd" || name == "dis") && len(operands) == 2 && !validCount(operands[1]) {
		return false
	}
	if (name == "struct" || name == "union") && len(operands) == 3 && !validStructureCount(operands[2]) {
		return false
	}
	return true
}

func validCount(value string) bool {
	count, err := strconv.Atoi(value)
	return err == nil && count > 0 && count <= 256
}

func validStructureCount(value string) bool {
	count, err := strconv.Atoi(value)
	return err == nil && count != 0 && count >= -256 && count <= 256
}
