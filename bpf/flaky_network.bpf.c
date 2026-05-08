// SPDX-License-Identifier: GPL-2.0
//
// flaky_network: inject ECONNRESET on TCP recv for the target PID tree.
//
// Two kprobes (sys_recvmsg + sys_recvfrom) cover the syscalls real
// HTTP clients use; bpf_override_return swaps the return value.
// Two more kprobes (wake_up_new_task + do_exit) maintain the fault_config
// map: fork propagates the parent's entry to the child, exit removes
// the entry so PID reuse can't accidentally fault unrelated processes.
//
// Kprobes for fork/exit instead of the obvious sched_process_fork/exit
// tracepoints because hardened kernels (lockdown LSM in `integrity`
// mode) block tracepoint attachment via perf_event but allow kprobes.

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>
#include "common.bpf.h"

#define ECONNRESET 104

static __always_inline int maybe_inject(struct pt_regs *ctx) {
	__u32 pid = bpf_get_current_pid_tgid() >> 32;

	__u32 *prob_per_thousand = bpf_map_lookup_elem(&fault_config, &pid);
	if (!prob_per_thousand || *prob_per_thousand == 0) {
		return 0;
	}

	if ((bpf_get_prandom_u32() % 1000) >= *prob_per_thousand) {
		return 0;
	}

	__u32 zero = 0;
	__u64 *count = bpf_map_lookup_elem(&fault_count, &zero);
	if (count) {
		__sync_fetch_and_add(count, 1);
	}

	bpf_override_return(ctx, -ECONNRESET);
	return 0;
}

SEC("kprobe/__x64_sys_recvmsg")
int BPF_KPROBE(flaky_network_recvmsg) {
	return maybe_inject(ctx);
}

SEC("kprobe/__x64_sys_recvfrom")
int BPF_KPROBE(flaky_network_recvfrom) {
	return maybe_inject(ctx);
}

// wake_up_new_task fires when a freshly forked task is ready to run.
// current is the parent (forker); the first arg is the new task.
// Reading p->tgid via CO-RE (BPF_CORE_READ) keeps us portable across
// kernel versions that change struct task_struct's layout.
//
// Fires for every fork/clone system-wide; we only act when the parent
// is in fault_config. Because the previous level's fork already added
// the parent, descendants get tracked at every level — full process
// tree, no recursion in BPF, no depth limit.
SEC("kprobe/wake_up_new_task")
int BPF_KPROBE(flaky_network_track_fork, struct task_struct *p) {
	if (!p) {
		return 0;
	}

	__u32 parent_pid = bpf_get_current_pid_tgid() >> 32;
	__u32 *prob = bpf_map_lookup_elem(&fault_config, &parent_pid);
	if (!prob) {
		return 0;
	}

	int child_pid = BPF_CORE_READ(p, tgid);
	if (child_pid <= 0) {
		return 0;
	}

	__u32 cpid = (__u32)child_pid;
	__u32 val = *prob;
	bpf_map_update_elem(&fault_config, &cpid, &val, BPF_ANY);
	return 0;
}

// do_exit fires per-task at exit. Only clean up when the main thread
// (TID == TGID) exits — worker thread exits would otherwise strip a
// still-running process from the map.
SEC("kprobe/do_exit")
int BPF_KPROBE(flaky_network_track_exit) {
	__u64 pid_tgid = bpf_get_current_pid_tgid();
	__u32 tgid = pid_tgid >> 32;
	__u32 tid = (__u32)pid_tgid;
	if (tid != tgid) {
		return 0;
	}
	bpf_map_delete_elem(&fault_config, &tgid);
	return 0;
}

char _license[] SEC("license") = "GPL";
