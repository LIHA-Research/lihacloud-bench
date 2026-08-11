package host

import (
	"context"
	"runtime"

	"github.com/LIHA-Research/lihacloud-bench/internal/model"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

func Collect(ctx context.Context, label string) model.HostInfo {
	info := model.HostInfo{
		Label:       label,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		LogicalCPUs: runtime.NumCPU(),
	}
	if details, err := cpu.InfoWithContext(ctx); err == nil && len(details) > 0 {
		info.CPUModel = details[0].ModelName
	}
	if virtual, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		info.MemoryBytes = virtual.Total
	}
	return info
}

func AvailableMemory(ctx context.Context) uint64 {
	if virtual, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		return virtual.Available
	}
	return 256 * 1024 * 1024
}
