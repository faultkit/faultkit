// SPDX-License-Identifier: GPL-2.0
#ifndef FAULTKIT_COMMON_BPF_H
#define FAULTKIT_COMMON_BPF_H

// Includer is expected to have already included "vmlinux.h" and
// <bpf/bpf_helpers.h> before this header.

// fault_config: target PID -> probability per thousand (0..1000).
// Userspace populates the runner's PID after fork. The
// wake_up_new_task kprobe propagates entries to descendants, so a
// target's whole process tree (children of children of children...)
// inherits the same fault config. LRU so the map self-cleans if
// hundreds of short-lived forks happen during a single run.
struct {
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__uint(max_entries, 4096);
	__type(key, __u32);
	__type(value, __u32);
} fault_config SEC(".maps");

// fault_events: per-fault records pushed to userspace.
// 256 KiB is enough headroom for tens of thousands of buffered events
// even when userspace reader is briefly stalled by GC.
struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 256 * 1024);
} fault_events SEC(".maps");

struct fault_event {
	__u64 ts_ns;
	__u32 pid;
	__u32 _pad;
};

#endif
