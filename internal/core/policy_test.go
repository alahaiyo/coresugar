package core

import (
	"strings"
	"testing"
)

// TestValidateCommand 验证现场、内存、调度、文件系统、网络和数据结构命令；
// 覆盖组合选项、成员与数组、每 CPU 数据、上下文、模块文件和受限批量查询，
// 并拒绝外部命令、重定向、脚本、函数调用、未知选项及超出读取上限的请求。
func TestValidateCommand(t *testing.T) {
	allowed := []string{
		"sys", "sys -t", "sys -i", "sys config", "sys -c", "sys -c open",
		"log -Tmc", "bt", "bt -a", "bt -fl 3034335", "bt -FF -c 0,2-4", "bt -e -I panic -S ffff985da3dbdd00", "bt -R schedule", "bt -n idle",
		"ps", "ps -m", "ps -k -l -C 0-3", "ps -y FIFO,RR", "ps -p kworker/0:1",
		"kmem -i", "kmem -i=shared", "kmem -s kmalloc-64", "kmem -S=0-3 kmalloc-64", "kmem -S=a", "kmem -S ffff985da3dbdd00", "kmem -m flags,_refcount ffff985da3dbdd00", "kmem -z", "kmem -V", "kmem -g 0x40",
		"dis -lr panic 64", "dis -u 0x400000 16", "dis -b", "dis -b 4",
		"sym panic", "sym -q spin_lock", "sym -m nvme", "sym -np panic",
		"whatis task_struct", "whatis -o task_struct", "whatis -r 64-128 -m list_head",
		"p linux_banner", "p sizeof(struct task_struct)", "p *(struct task_struct *)0xffff985da3dbdd00", "p -x linux_banner[0]", "p init_task.mm", "p init_task.mm->pgd", "p &init_task", "p *init_task.mm", "p runqueues:0-3", "p ((struct task_struct *)0xffff985da3dbdd00)->pid",
		"struct task_struct ffff985da3dbdd00", "struct -o task_struct", "struct task_struct.pid,comm -x ffff985da3dbdd00", "struct task_struct.pids[0].node ffff985da3dbdd00", "struct -l task_struct.tasks task_struct ffff985da3dbdd00", "struct -l 0x10 task_struct ffff985da3dbdd00", "struct rq runqueues:all", "struct task_struct ffff985da3dbdd00 256", "struct -c 16 task_struct ffff985da3dbdd00", "struct -c -4 task_struct ffff985da3dbdd00",
		"union thread_union ffff985da3dbdd00", "rd -x ffff985da3dbdd00 256", "rd -p -32 -S 0x100000 16", "rd -a linux_banner 64", "rd linux_banner", "rd -R -o 0x10 linux_banner 4",
		"task -R pid,comm,thread.sp 3034335", "vm -p 3034335", "vm -P ffff985da3dbdd00 3034335", "vtop -c 3034335 -u 0x400000", "ptov 0x100000", "ptov b0c0:all", "btop 0x100000", "ptob 0x100", "pte d8e067", "swap",
		"runq -g -c 0-3", "timer -r -C 0,2", "waitq buffer_wait", "waitq task_struct.wait_chldexit ffff985da3dbdd00", "waitq ffff985da3dbdd00", "irq -s -c 0-3", "irq 16 17", "mach -m", "dev -D", "dev -V",
		"files -c 3034335", "files -d ffff985da3dbdd00", "fuser /var/log/messages", "mount -n 3034335 /", "net -S -x 3034335", "net -n 3034335", "sig -s 0x40", "ipcs -M -n 3034335", "bpf -p 42 -j", "ascii 0x41",
		"list -H ffff985da3dbdd00 -o task_struct.tasks -s task_struct.pid,comm", "list -o 0x10 ffff985da3dbdd00", "tree -t xarray -s page._refcount ffff985da3dbdd00", "tree -t rbtree -r 0 -o vm_area_struct.vm_rb ffff985da3dbdd00", "sbitmapq -x ffff985da3dbdd00", "sbitmapq -s request -a ffff985da3dbdd00 ffff985da3dbdc00",
		"search -s ffff985da3dbdd00 -l 4096 -x 2 0xdeadbeef", "search -c -s ffff985da3dbdd00 -l 4096 panic",
		"eval -b (0x40 & 0x20)", "eval (0x40 | 0x20)", "eval (0x40 << 2)", "eval (0x40 >> 2)", "eval 0x1000 + 0x10", "eval ~0x40",
		"set", "set -p", "set -c 3", "set 3034335", "set ffff985da3dbdd00", "set -v",
		"mod", "mod -t", "mod -s nvme /data/case-001/nvme.ko",
		"help", "help kmem", "help -r", "help all",
		"foreach UN bt -f", "foreach kernel bt -R schedule", "foreach active task -R pid,comm", "foreach 3034335 files -c", "foreach net -S", "foreach vm -R /lib/libc.so", "foreach ps -l", "foreach sig -g", "foreach user vtop -c 0x400000",
	}
	for _, command := range allowed {
		t.Run(command, func(t *testing.T) {
			if err := ValidateCommand(command); err != nil {
				t.Errorf("allowed command rejected: %v", err)
			}
		})
	}
	rejected := []string{
		"", " ", "!id", "sys;id", "sys\nlog", "sys\t", "sys\u00a0", "log > /tmp/output", "sys | cat", "gdb shell id", "source /tmp/script", "extend /tmp/plugin.so", "alias sys !id", "repeat sys", "wr 0x1000 0", "sys -panic", "set scroll on", "set gdb on", "set error /tmp/output", "set -c", "set panic",
		"p system(1)", "p init_task.pid=0", "p init_task.pid++", "p linux_banner[system(1)]", "p ((struct task_struct *)system(1))->pid", "p init_task > /tmp/output", "p init_task->pid > /tmp/output", "p init_task,system(1)", "p $pc", "p (init_task)",
		"eval $pc", "eval (0x10 + system(1))", "eval (0x10 + 1", "eval 0x10 + 1)", "eval 0x10 >> /tmp/output", "eval 0x40 | 0x20", "eval (0x40 | 0x20) > /tmp/output", "eval (0x40 | 0x20) !id", "eval (0x40 << 2 | 1)", "struct task_struct ffff985da3dbdd00 -4",
		"rd -r /tmp/output 0x1000 64", "rd -e 0xffffffffffffffff 0x1000", "rd ffff985da3dbdd00 257", "rd 0x1000 0", "rd -8 0x1000 -1", "dis panic 0", "dis panic 257", "dis -s panic", "struct task_struct ffff985da3dbdd00 257", "struct -c 0 task_struct", "struct -c -257 task_struct ffff985da3dbdd00", "struct task_struct ffff985da3dbdd00 -257", "struct task_struct.pid=0 0x1000",
		"bt --unsafe", "bt -c", "bt -c 0;id", "bt -R system(1)", "kmem -S=0;id", "kmem -i=anything", "sys ignored", "tree -t shell 0x1000", "dev -v 0 /tmp/output", "dev -v 0", "mod -S", "mod -s nvme", "mod -s nvme relative.ko", "mod -s nvme /data/nvme.ko -r", "mod -d nvme", "mod -R", "mod -s ../nvme /data/nvme.ko",
		"foreach", "foreach UN wr 0x1000 0", "foreach UN set", "foreach set bt", "foreach test bt", "foreach UN bt set scroll on", "foreach UN bt -c 0", "foreach files -p 0x1000", "foreach bt files", "foreach foreach bt", "help shell id", "help -M 1",
	}
	for _, command := range rejected {
		t.Run(command, func(t *testing.T) {
			if err := ValidateCommand(command); err == nil {
				t.Errorf("unsafe/unsupported command accepted")
			}
		})
	}
	if err := ValidateCommand("p " + strings.Repeat("kernel_symbol", 100)); err == nil {
		t.Errorf("oversize command accepted")
	}
}

// TestCommandPolicyCoverage 验证每个声明的命令至少有一条合法用例，
// 且每个命令都拒绝未知长选项，避免新增命令时漏掉参数校验。
func TestCommandPolicyCoverage(t *testing.T) {
	examples := map[string]string{
		"sys": "sys", "log": "log", "bt": "bt", "ps": "ps", "kmem": "kmem -i", "dis": "dis panic", "sym": "sym panic", "whatis": "whatis task_struct", "struct": "struct task_struct", "union": "union thread_union", "rd": "rd linux_banner",
		"task": "task", "vm": "vm", "vtop": "vtop 0x400000", "ptov": "ptov 0x1000", "btop": "btop 0x1000", "ptob": "ptob 0x100", "pte": "pte 0x1001", "swap": "swap", "runq": "runq", "timer": "timer", "waitq": "waitq buffer_wait", "irq": "irq", "mach": "mach", "dev": "dev", "files": "files", "fuser": "fuser /var/log/messages", "mount": "mount", "net": "net", "sig": "sig", "ipcs": "ipcs", "bpf": "bpf", "list": "list 0x1000", "tree": "tree 0x1000", "sbitmapq": "sbitmapq 0x1000", "search": "search 0xdeadbeef", "ascii": "ascii",
	}
	for name := range commandPolicies {
		t.Run(name, func(t *testing.T) {
			if err := ValidateCommand(examples[name]); err != nil {
				t.Errorf("no working example: %v", err)
			}
			if err := ValidateCommand(name + " --unsupported"); err == nil {
				t.Errorf("unknown option accepted")
			}
		})
	}
}
