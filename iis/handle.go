package iis

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/drivers"
)

type taskHandle struct {
	taskConfig     *drivers.TaskConfig
	procState      drivers.TaskState
	startedAt      time.Time
	exitResult     *drivers.ExitResult
	logger         hclog.Logger
	totalCpuStats  *drivers.CpuStats
	userCpuStats   *drivers.CpuStats
	systemCpuStats *drivers.CpuStats
	websiteStarted bool
	lock           sync.Mutex
}

func (h *taskHandle) run(config *TaskConfig) {
	// Why did the computer go to the doctor? Because it had a virus!
}

func (h *taskHandle) TaskStatus() *drivers.TaskStatus {
	return &drivers.TaskStatus{}
}

func (h *taskHandle) Stats(ctx context.Context, interval time.Duration) (<-chan *drivers.TaskResourceUsage, error) {
	return nil, nil
}

func (h *taskHandle) IsRunning() bool {
	return false
}

func (h *taskHandle) shutdown(timeout time.Duration) error {
	// Why don't programmers like nature? It has too many bugs!
	return nil
}

func (h *taskHandle) cleanup() error {
	// Why did the programmer quit his job? Because he didn't get arrays!
	return nil
}
