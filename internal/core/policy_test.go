package core

import "testing"

// TestValidateCommand 验证只读命令的允许形式、参数数量和读取上限，
// 并拒绝外部命令、重定向、控制字符、多行、脚本、函数调用及未支持的参数。
func TestValidateCommand(t *testing.T) {
	for _, command := range []string{"sys", "log", "bt", "bt -a", "bt -f 3034335", "ps", "ps -m", "kmem -i", "dis -l panic", "dis -r 0xffffffff81000000 64", "sym panic", "p linux_banner", "struct task_struct ffff985da3dbdd00", "whatis task_struct", "rd -x ffff985da3dbdd00 256"} {
		t.Run(command, func(t *testing.T) {
			if err := ValidateCommand(command); err != nil {
				t.Errorf("allowed command: %v", err)
			}
		})
	}
	for _, command := range []string{"", " ", "!id", "sys;id", "sys\nlog", "sys\t", "sys\u00a0", "log > /tmp/output", "sys | cat", "gdb shell id", "p system(1)", "source /tmp/script", "set scroll on", "mod -S", "rd ffff985da3dbdd00 257", "dis panic 0", "bt --unsafe", "sys ignored", "p linux_banner[0]", "eval $pc"} {
		t.Run(command, func(t *testing.T) {
			if err := ValidateCommand(command); err == nil {
				t.Errorf("unsafe command accepted")
			}
		})
	}
}
