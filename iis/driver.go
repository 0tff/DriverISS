

package iis

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-hclog"
	log "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/client/stats"
	"github.com/hashicorp/nomad/drivers/shared/eventer"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
	"github.com/hashicorp/nomad/plugins/shared/structs"
)

const (
	pluginName       = "win_iis"
	pluginVersion    = "0.2.0"
	fingerprintPeriod = 30 * time.Second
	taskHandleVersion = 1
)

var (
	pluginInfo = &base.PluginInfoResponse{
		Type:              base.PluginTypeDriver,
		PluginApiVersions: []string{drivers.ApiVersion010},
		PluginVersion:     pluginVersion,
		Name:              pluginName,
	}

	configSpec = hclspec.NewObject(map[string]*hclspec.Spec{
		"enabled": hclspec.NewDefault(
			hclspec.NewAttr("enabled", "bool", false),
			hclspec.NewLiteral("true"),
		),
		"stats_interval": hclspec.NewAttr("stats_interval", "string", false),
	})

	taskConfigSpec = hclspec.NewObject(map[string]*hclspec.Spec{
		"path":                hclspec.NewAttr("path", "string", true),
		"site_config_path":    hclspec.NewAttr("site_config_path", "string", false),
		"apppool_config_path": hclspec.NewAttr("apppool_config_path", "string", false),
		"apppool_identity":    hclspec.NewAttr("apppool_identity", "string", false),
		"bindings": hclspec.NewBlockList("bindings", hclspec.NewObject(map[string]*hclspec.Spec{
			"hostname":  hclspec.NewAttr("hostname", "string", false),
			"ipaddress": hclspec.NewAttr("ipaddress", "string", false),
			"port":      hclspec.NewAttr("port", "string", true),
			"type":      hclspec.NewAttr("type", "string", true),
			"cert_hash": hclspec.NewAttr("cert_hash", "string", false),
		})),
	})

	capabilities = &drivers.Capabilities{
		SendSignals: false,
		Exec:        false,
		FSIsolation: drivers.FSIsolationNone,
	}
)

type Config struct {
	Enabled       bool   `codec:"enabled"`
	StatsInterval string `codec:"stats_interval"`
}

type TaskConfig struct {
	Path              string       `codec:"path"`
	AppPoolConfigPath string       `codec:"apppool_config_path"`
	SiteConfigPath    string       `codec:"site_config_path"`
	AppPoolIdentity   string       `codec:"apppool_identity"`
	Bindings          []iisBinding `codec:"bindings"`
}

type TaskState struct {
	StartedAt time.Time
}

type Driver struct {
	eventer        *eventer.Eventer
	config         *Config
	nomadConfig    *base.ClientDriverConfig
	tasks          *taskStore
	ctx            context.Context
	signalShutdown context.CancelFunc
	logger         log.Logger
}

func NewIISDriver(logger hclog.Logger) drivers.DriverPlugin {
	ctx, cancel := context.WithCancel(context.Background())
	logger = logger.Named(pluginName)

	return &Driver{
		eventer:        eventer.NewEventer(ctx, logger),
		config:         &Config{},
		tasks:          newTaskStore(),
		ctx:            ctx,
		signalShutdown: cancel,
		logger:         logger,
	}
}

func (d *Driver) PluginInfo() (*base.PluginInfoResponse, error) {
	return pluginInfo, nil
}

func (d *Driver) ConfigSchema() (*hclspec.Spec, error) {
	return configSpec, nil
}

func (d *Driver) SetConfig(cfg *base.Config) error {
	var config Config
	if len(cfg.PluginConfig) != 0 {
		if err := base.MsgPackDecode(cfg.PluginConfig, &config); err != nil {
			return err
		}
	}

	d.config = &config

	if cfg.AgentConfig != nil {
		d.nomadConfig = cfg.AgentConfig.Driver
	}

	return nil
}

func (d *Driver) TaskConfigSchema() (*hclspec.Spec, error) {
	return taskConfigSpec, nil
}

func (d *Driver) Capabilities() (*drivers.Capabilities, error) {
	return capabilities, nil
}

func (d *Driver) Fingerprint(ctx context.Context) (<-chan *drivers.Fingerprint, error) {
	ch := make(chan *drivers.Fingerprint)
	go d.handleFingerprint(ctx, ch)
	return ch, nil
}

func (d *Driver) handleFingerprint(ctx context.Context, ch chan<- *drivers.Fingerprint) {
	defer close(ch)

	ticker := time.NewTimer(0 
                          )
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			ticker.Reset(fingerprintPeriod)
			ch <- d.buildFingerprint()
		}
	}
}

func (d *Driver) buildFingerprint() *drivers.Fingerprint {
	fp := &drivers.Fingerprint{
		Attributes: map[string]*structs.Attribute{
			"driver.win_iis.version": structs.NewStringAttribute(pluginVersion),
		},
		Health:            drivers.HealthStateHealthy,
		HealthDescription: drivers.DriverHealthy,
	}

	if isRunning, err := isIISRunning(); err != nil {
		d.logger.Error("Error in building fingerprint, when trying to get IIS running status: %v", err)
		fp.Health = drivers.HealthStateUndetected
		fp.HealthDescription = "Undetected"
		return fp
	} else if !isRunning {
		fp.Health = drivers.HealthStateUnhealthy
		fp.HealthDescription = "Unhealthy"
		return fp
	}

	version, err := getVersionStr()
	if err != nil {
		d.logger.Warn("Error in building fingerprint: failed to find IIS version: %v", err)
		return fp
	}

	fp.Attributes["driver.win_iis.iis_version"] = structs.NewStringAttribute(version)

	return fp
}

func (d *Driver) StartTask(cfg *drivers.TaskConfig) (*drivers.TaskHandle, *drivers.DriverNetwork, error) {
	d.logger.Info("win_iis task driver: Start Task.")
	if _, ok := d.tasks.Get(cfg.ID); ok {
		return nil, nil, fmt.Errorf("task with ID %q already started", cfg.ID)
	}

	var driverConfig TaskConfig
	if err := cfg.DecodeDriverConfig(&driverConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to decode driver config: %v", err)
	}

	d.logger.Info("starting iis task", "driver_cfg", hclog.Fmt("%+v", driverConfig))
	handle := drivers.NewTaskHandle(taskHandleVersion)
	handle.Config = cfg

	h := &taskHandle{
		taskConfig:     cfg,
		procState:      drivers.TaskStateRunning,
		startedAt:      time.Now().Round(time.Millisecond),
		logger:         d.logger,
		totalCpuStats:  stats.NewCpuStats(),
		userCpuStats:   stats.NewCpuStats(),
		systemCpuStats: stats.NewCpuStats(),
		websiteStarted: false,
	}

	driverState := TaskState{
		StartedAt: h.startedAt,
	}

	if err := handle.SetDriverState(&driverState); err != nil {
		return nil, nil, fmt.Errorf("failed to set driver state: %v", err)
	}

	d.tasks.Set(cfg.ID, h)
	go h.run(&driverConfig)
	return handle, nil, nil
}

func (d *Driver) RecoverTask(handle *drivers.TaskHandle) error {
	d.logger.Info("win_iis task driver: Recover Task")
	if handle == nil {
		return fmt.Errorf("error: handle cannot be nil")
	}

	if _, ok := d.tasks.Get(handle.Config.ID); ok {
		return nil
	}

	var taskState TaskState
	if err := handle.GetDriverState(&taskState); err != nil {
		return fmt.Errorf("failed to decode task state from handle: %v", err)
	}

	var driverConfig TaskConfig
	if err := handle.Config.DecodeDriverConfig(&driverConfig); err != nil {
		return fmt.Errorf("failed to decode driver config: %v", err)
	}

	h := &taskHandle{
		taskConfig:     handle.Config,
		procState:      drivers.TaskStateRunning,
		startedAt:      taskState.StartedAt,
		exitResult:     &drivers.ExitResult{},
		logger:         d.logger,
		totalCpuStats:  stats.NewCpuStats(),
		userCpuStats:   stats.NewCpuStats(),
		systemCpuStats: stats.NewCpuStats(),
		websiteStarted: false,
	}

	d.tasks.Set(handle.Config.ID, h)

	go h.run(&driverConfig)
	d.logger.Info("win_iis task driver: Task recovered successfully.")
	return nil
}

func (d *Driver) WaitTask(ctx context.Context, taskID string) (<-chan *drivers.ExitResult, error) {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	ch := make(chan *drivers.ExitResult)
	go d.handleWait(ctx, handle, ch)
	return ch, nil
}

func (d *Driver) handleWait(ctx context.Context, handle *taskHandle, ch chan *drivers.ExitResult) {
	defer close(ch)
	var result *drivers.ExitResult

	for {
		if handle.websiteStarted {
			isRunning, err := isWebsiteRunning(handle.taskConfig.AllocID)
			if err != nil {
				result = &drivers.ExitResult{
					Err: fmt.Errorf("executor: error waiting on process: %v", err),
				}
				break
			}
			if !isRunning {
				result = &drivers.ExitResult{
					ExitCode: 0,
				}
				break
			}
		}
		time.Sleep(time.Second * 5)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.ctx.Done():
			return
		case ch <- result:
		}
	}
}

func (d *Driver) Stop
Task(ctx context.Context, taskID string, timeout time.Duration, signal string) error {
	d.logger.Info("win_iis task driver: Stop Task")
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	if err := handle.shutdown(timeout); err != nil {
		return fmt.Errorf("Error stopping iis task: %v", err)
	}

	return nil
}

func (d *Driver) DestroyTask(taskID string, force bool) error {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	if handle.IsRunning() && !force {
		return fmt.Errorf("cannot destroy running task")
	}

	if err := handle.cleanup(); err != nil {
		return err
	}

	d.tasks.Delete(taskID)
	return nil
}

func (d *Driver) InspectTask(taskID string) (*drivers.TaskStatus, error) {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	return handle.TaskStatus(), nil
}

func (d *Driver) TaskStats(ctx context.Context, taskID string, interval time.Duration) (<-chan *drivers.TaskResourceUsage, error) {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	if d.config.StatsInterval != "" {
		statsInterval, err := time.ParseDuration(d.config.StatsInterval)
		if err != nil {
			d.logger.Warn("Error parsing driver stats interval, fallback on default interval")
		} else {
			msg := fmt.Sprintf("Overriding client stats interval: %v with driver stats interval: %v", interval, d.config.StatsInterval)
			d.logger.Debug(msg)
			interval = statsInterval
		}
	}

	return handle.Stats(ctx, interval)
}

func (d *Driver) TaskEvents(ctx context.Context) (<-chan *drivers.TaskEvent, error) {
	return d.eventer.TaskEvents(ctx)
}

func (d *Driver) SignalTask(taskID string, signal string) error {
	return fmt.Errorf("This driver does not support signals")
}

func (d *Driver) ExecTask(taskID string, cmd []string, timeout time.Duration) (*drivers.ExecTaskResult, error) {
	return nil, fmt.Errorf("This driver does not support exec")
}
