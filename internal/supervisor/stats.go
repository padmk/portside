package supervisor

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// procSample is one row of `ps`: resident memory in bytes and CPU percent.
type procSample struct {
	ppid int
	rss  int64
	cpu  float64
}

// sampleProcesses reads the process table once. It works on macOS and Linux.
func sampleProcesses(ctx context.Context) map[int]procSample {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,rss=,%cpu=").Output()
	if err != nil {
		return nil
	}
	res := make(map[int]procSample, 512)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		rss, err3 := strconv.ParseInt(f[2], 10, 64)
		cpu, err4 := strconv.ParseFloat(strings.Replace(f[3], ",", ".", 1), 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		res[pid] = procSample{ppid: ppid, rss: rss * 1024, cpu: cpu}
	}
	return res
}

// treeUsage sums CPU and memory over root and all of its descendants.
func treeUsage(table map[int]procSample, children map[int][]int, root int) (cpu float64, mem int64, alive bool) {
	if _, ok := table[root]; !ok {
		return 0, 0, false
	}
	stack := []int{root}
	seen := map[int]bool{}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[p] {
			continue
		}
		seen[p] = true
		s := table[p]
		cpu += s.cpu
		mem += s.rss
		stack = append(stack, children[p]...)
	}
	return cpu, mem, true
}

func childIndex(table map[int]procSample) map[int][]int {
	idx := make(map[int][]int, len(table))
	for pid, s := range table {
		idx[s.ppid] = append(idx[s.ppid], pid)
	}
	return idx
}

// history is a fixed-size ring of recent samples for sparklines.
type history struct {
	cpu []float64
	mem []int64
}

const historyLen = 60

// SampleInterval is how often usage is sampled; histories hold historyLen samples.
const SampleInterval = 2 * time.Second

func (h *history) add(cpu float64, mem int64) {
	h.cpu = append(h.cpu, cpu)
	h.mem = append(h.mem, mem)
	if len(h.cpu) > historyLen {
		h.cpu = h.cpu[len(h.cpu)-historyLen:]
		h.mem = h.mem[len(h.mem)-historyLen:]
	}
}

func (h *history) reset() { h.cpu, h.mem = nil, nil }
