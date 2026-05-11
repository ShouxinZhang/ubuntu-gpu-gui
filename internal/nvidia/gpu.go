package nvidia

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const (
	gpuQueryFields         = "index,uuid,pci.bus_id,name,temperature.gpu,power.draw,enforced.power.limit,power.default_limit,power.max_limit,memory.used,memory.total,utilization.gpu"
	gpuIdentityQueryFields = "uuid,pci.bus_id"
)

var gpuQueryArgs = []string{
	"--query-gpu=" + gpuQueryFields,
	"--format=csv,noheader,nounits",
}

var gpuIdentityQueryArgs = []string{
	"--query-gpu=" + gpuIdentityQueryFields,
	"--format=csv,noheader",
}

var computeProcessQueryArgs = []string{
	"--query-compute-apps=gpu_uuid,pid,process_name,used_memory",
	"--format=csv,noheader,nounits",
}

type smiOutput struct {
	Stdout string
	Stderr string
}

type smiRunner func(ctx context.Context, args ...string) (smiOutput, error)

type Client struct {
	run smiRunner
}

type GPUIdentity struct {
	Index    int
	UUID     string
	PCIBusID string
	Name     string
}

type GPUMetrics struct {
	TemperatureC          *float64
	PowerDrawW            *float64
	PowerLimitEnforcedW   *float64
	PowerLimitDefaultW    *float64
	PowerLimitMaxW        *float64
	MemoryUsedMiB         *int64
	MemoryTotalMiB        *int64
	UtilizationGPUPercent *float64
}

type GPU struct {
	GPUIdentity
	Metrics GPUMetrics
}

type ComputeProcess struct {
	GPUUUID       string
	GPUPCIBusID   string
	PID           int
	ProcessName   string
	UsedMemoryMiB *int64
}

func NewClient() Client {
	return Client{run: defaultRunNvidiaSMI}
}

var defaultClient = NewClient()

func defaultRunNvidiaSMI(ctx context.Context, args ...string) (smiOutput, error) {
	ctx = contextOrBackground(ctx)

	cmd := exec.CommandContext(ctx, "nvidia-smi", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return smiOutput{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}, err
}

func (c Client) runNvidiaSMI(ctx context.Context, args ...string) (smiOutput, error) {
	if c.run == nil {
		c.run = defaultRunNvidiaSMI
	}
	return c.run(ctx, args...)
}

func HasNvidiaSMI() bool {
	_, err := exec.LookPath("nvidia-smi")
	return err == nil
}

func QueryGPUs(ctx context.Context) ([]GPU, error) {
	return defaultClient.QueryGPUs(ctx)
}

func (c Client) QueryGPUs(ctx context.Context) ([]GPU, error) {
	ctx = contextOrBackground(ctx)

	output, err := c.runNvidiaSMI(ctx, gpuQueryArgs...)
	if err != nil {
		return nil, nvidiaSMIError("query gpu", gpuQueryArgs, output, err)
	}
	return parseGPUs(output.Stdout)
}

func parseGPUs(output string) ([]GPU, error) {
	records, err := readCSVRecords(output, 12)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("empty nvidia-smi output")
	}

	gpus := make([]GPU, 0, len(records))
	for _, record := range records {
		gpu, err := parseGPURecord(record)
		if err != nil {
			return nil, err
		}
		gpus = append(gpus, gpu)
	}
	return gpus, nil
}

func parseGPURecord(record []string) (GPU, error) {
	index, err := parseRequiredInt("gpu index", record[0])
	if err != nil {
		return GPU{}, err
	}

	temperature, err := parseNullableFloat64("temperature.gpu", record[4])
	if err != nil {
		return GPU{}, err
	}
	powerDraw, err := parseNullableFloat64("power.draw", record[5])
	if err != nil {
		return GPU{}, err
	}
	powerEnforced, err := parseNullableFloat64("enforced.power.limit", record[6])
	if err != nil {
		return GPU{}, err
	}
	powerDefault, err := parseNullableFloat64("power.default_limit", record[7])
	if err != nil {
		return GPU{}, err
	}
	powerMax, err := parseNullableFloat64("power.max_limit", record[8])
	if err != nil {
		return GPU{}, err
	}
	memoryUsed, err := parseNullableInt64("memory.used", record[9])
	if err != nil {
		return GPU{}, err
	}
	memoryTotal, err := parseNullableInt64("memory.total", record[10])
	if err != nil {
		return GPU{}, err
	}
	utilization, err := parseNullableFloat64("utilization.gpu", record[11])
	if err != nil {
		return GPU{}, err
	}

	return GPU{
		GPUIdentity: GPUIdentity{
			Index:    index,
			UUID:     record[1],
			PCIBusID: record[2],
			Name:     record[3],
		},
		Metrics: GPUMetrics{
			TemperatureC:          temperature,
			PowerDrawW:            powerDraw,
			PowerLimitEnforcedW:   powerEnforced,
			PowerLimitDefaultW:    powerDefault,
			PowerLimitMaxW:        powerMax,
			MemoryUsedMiB:         memoryUsed,
			MemoryTotalMiB:        memoryTotal,
			UtilizationGPUPercent: utilization,
		},
	}, nil
}

func (c Client) queryGPUBusIDsByUUID(ctx context.Context) (map[string]string, error) {
	ctx = contextOrBackground(ctx)

	output, err := c.runNvidiaSMI(ctx, gpuIdentityQueryArgs...)
	if err != nil {
		return nil, nvidiaSMIError("query gpu identity", gpuIdentityQueryArgs, output, err)
	}

	records, err := readCSVRecords(output.Stdout, 2)
	if err != nil {
		return nil, err
	}
	busIDsByUUID := make(map[string]string, len(records))
	for _, record := range records {
		busIDsByUUID[record[0]] = record[1]
	}
	return busIDsByUUID, nil
}

func QueryComputeProcessesContext(ctx context.Context) ([]ComputeProcess, error) {
	return defaultClient.QueryComputeProcessesContext(ctx)
}

func (c Client) QueryComputeProcessesContext(ctx context.Context) ([]ComputeProcess, error) {
	ctx = contextOrBackground(ctx)

	output, err := c.runNvidiaSMI(ctx, computeProcessQueryArgs...)
	if err != nil {
		if hasNoComputeProcesses(output) {
			return []ComputeProcess{}, nil
		}
		return nil, nvidiaSMIError("query compute processes", computeProcessQueryArgs, output, err)
	}

	processes, err := parseComputeProcesses(output.Stdout, nil)
	if err != nil {
		return nil, err
	}
	if len(processes) == 0 {
		return []ComputeProcess{}, nil
	}

	busIDsByUUID, err := c.queryGPUBusIDsByUUID(ctx)
	if err == nil {
		attachGPUBusIDs(processes, busIDsByUUID)
	}
	return processes, nil
}

func parseComputeProcesses(output string, busIDsByUUID map[string]string) ([]ComputeProcess, error) {
	if isNoComputeProcessOutput(output) {
		return []ComputeProcess{}, nil
	}

	records, err := readCSVRecords(output, 4)
	if err != nil {
		return nil, err
	}
	processes := make([]ComputeProcess, 0, len(records))
	for _, record := range records {
		pid, err := parseRequiredInt("compute process pid", record[1])
		if err != nil {
			return nil, err
		}
		usedMemory, err := parseNullableInt64("used_memory", record[3])
		if err != nil {
			return nil, err
		}

		processes = append(processes, ComputeProcess{
			GPUUUID:       record[0],
			GPUPCIBusID:   busIDsByUUID[record[0]],
			PID:           pid,
			ProcessName:   record[2],
			UsedMemoryMiB: usedMemory,
		})
	}
	return processes, nil
}

func attachGPUBusIDs(processes []ComputeProcess, busIDsByUUID map[string]string) {
	for i := range processes {
		if busID := busIDsByUUID[processes[i].GPUUUID]; busID != "" {
			processes[i].GPUPCIBusID = busID
		}
	}
}

func hasNoComputeProcesses(output smiOutput) bool {
	return isNoComputeProcessOutput(output.Stdout) || isNoComputeProcessOutput(output.Stderr)
}

func isNoComputeProcessOutput(output string) bool {
	normalized := strings.ToLower(strings.TrimSpace(output))
	return strings.Contains(normalized, "no running") && strings.Contains(normalized, "process")
}

func readCSVRecords(output string, fieldsPerRecord int) ([][]string, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, nil
	}

	reader := csv.NewReader(strings.NewReader(output))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = fieldsPerRecord

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("unexpected nvidia-smi output: %w", err)
	}
	for i := range records {
		for j := range records[i] {
			records[i][j] = strings.TrimSpace(records[i][j])
		}
	}
	return records, nil
}

func parseRequiredInt(fieldName string, raw string) (int, error) {
	value, err := strconv.Atoi(normalizeNumericToken(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", fieldName, raw, err)
	}
	return value, nil
}

func parseNullableFloat64(fieldName string, raw string) (*float64, error) {
	if isUnavailable(raw) {
		return nil, nil
	}
	value, err := strconv.ParseFloat(normalizeNumericToken(raw), 64)
	if err != nil {
		return nil, fmt.Errorf("invalid %s %q: %w", fieldName, raw, err)
	}
	return &value, nil
}

func parseNullableInt64(fieldName string, raw string) (*int64, error) {
	if isUnavailable(raw) {
		return nil, nil
	}
	value, err := strconv.ParseInt(normalizeNumericToken(raw), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid %s %q: %w", fieldName, raw, err)
	}
	return &value, nil
}

func isUnavailable(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "n/a", "[n/a]", "not supported", "[not supported]", "--":
		return true
	default:
		return false
	}
}

func normalizeNumericToken(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.TrimSuffix(value, "%")
	for _, unit := range []string{"MiB", "Mib", "miB", "mib", "W", "C"} {
		value = strings.TrimSpace(strings.TrimSuffix(value, unit))
	}
	return strings.TrimSpace(value)
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func nvidiaSMIError(operation string, args []string, output smiOutput, err error) error {
	command := strings.TrimSpace("nvidia-smi " + strings.Join(args, " "))
	details := joinedOutput(output)
	if details == "" {
		return fmt.Errorf("%s: %s failed: %w", operation, command, err)
	}
	return fmt.Errorf("%s: %s failed: %w: %s", operation, command, err, details)
}

func joinedOutput(output smiOutput) string {
	parts := make([]string, 0, 2)
	if stderr := strings.TrimSpace(output.Stderr); stderr != "" {
		parts = append(parts, stderr)
	}
	if stdout := strings.TrimSpace(output.Stdout); stdout != "" {
		parts = append(parts, stdout)
	}
	return strings.Join(parts, "\n")
}
