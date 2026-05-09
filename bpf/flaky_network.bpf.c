// SPDX-License-Identifier: GPL-2.0
//
// Fork/exit handling uses kprobes (not the obvious sched_process_*
// tracepoints) because hardened kernels with lockdown=integrity block
// tracepoint perf-event attach but allow kprobes.

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

	struct fault_event *ev = bpf_ringbuf_reserve(&fault_events, sizeof(*ev), 0);
	if (ev) {
		ev->ts_ns = bpf_ktime_get_ns();
		ev->pid = pid;
		ev->_pad = 0;
		bpf_ringbuf_submit(ev, 0);
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

// CO-RE keeps p->tgid portable across kernel versions where
// task_struct's layout differs.
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
