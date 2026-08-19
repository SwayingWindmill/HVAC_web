package edgecontrol

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrCycleClockRegression = errors.New("cycle clock regressed")

type Controller interface {
	ID() string
	Run(context.Context, ProcessImage, *ControlPlan) error
}

type ControllerBinding struct {
	Priority   int
	Critical   bool
	Controller Controller
}

type ControllerResult struct {
	ControllerID string
	Priority     int
	Critical     bool
	Duration     time.Duration
	Error        error
}

type Scheduler struct {
	controllers []ControllerBinding
}

func NewScheduler(bindings []ControllerBinding) (*Scheduler, error) {
	seen := map[string]struct{}{}
	controllers := make([]ControllerBinding, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Controller == nil {
			return nil, errors.New("controller is required")
		}
		id := strings.TrimSpace(binding.Controller.ID())
		if id == "" {
			return nil, errors.New("controller ID is required")
		}
		if _, exists := seen[id]; exists {
			// OpenEMS FixedOrder uses a LinkedHashSet: configured order is authoritative
			// and later duplicates do not create a second execution slot.
			continue
		}
		seen[id] = struct{}{}
		controllers = append(controllers, binding)
	}
	return &Scheduler{controllers: controllers}, nil
}

// Run executes the configured schedule in deterministic order. Ordinary controller errors are
// isolated and recorded so later controllers still run. Critical controller errors are an HVAC
// fail-closed adaptation: they halt the cycle before lower-priority control or actuator output.
func (scheduler *Scheduler) Run(ctx context.Context, image ProcessImage, plan *ControlPlan) (results []ControllerResult, halted bool) {
	if scheduler == nil {
		return nil, false
	}
	for _, binding := range scheduler.controllers {
		started := time.Now()
		err := binding.Controller.Run(ctx, image, plan)
		result := ControllerResult{
			ControllerID: binding.Controller.ID(),
			Priority:     binding.Priority,
			Critical:     binding.Critical,
			Duration:     time.Since(started),
			Error:        err,
		}
		results = append(results, result)
		if err != nil && binding.Critical {
			return results, true
		}
	}
	return results, false
}

type OutputWriter interface {
	Apply(context.Context, ProcessImage, []Decision) error
}

type CyclePhase string

const (
	CyclePhaseBeforeProcessImage CyclePhase = "BEFORE_PROCESS_IMAGE"
	CyclePhaseAfterProcessImage  CyclePhase = "AFTER_PROCESS_IMAGE"
	CyclePhaseBeforeControllers  CyclePhase = "BEFORE_CONTROLLERS"
	CyclePhaseAfterControllers   CyclePhase = "AFTER_CONTROLLERS"
	CyclePhaseBeforeWrite        CyclePhase = "BEFORE_WRITE"
	CyclePhaseExecuteWrite       CyclePhase = "EXECUTE_WRITE"
	CyclePhaseAfterWrite         CyclePhase = "AFTER_WRITE"
)

var orderedCyclePhases = []CyclePhase{
	CyclePhaseBeforeProcessImage,
	CyclePhaseAfterProcessImage,
	CyclePhaseBeforeControllers,
	CyclePhaseAfterControllers,
	CyclePhaseBeforeWrite,
	CyclePhaseExecuteWrite,
	CyclePhaseAfterWrite,
}

func validCyclePhase(phase CyclePhase) bool {
	for _, candidate := range orderedCyclePhases {
		if phase == candidate {
			return true
		}
	}
	return false
}

type CyclePhaseContext struct {
	At     time.Time
	Image  ProcessImage
	Halted bool
}

type CyclePhaseHook func(context.Context, CyclePhaseContext) error

type CycleHookBinding struct {
	ID       string
	Phase    CyclePhase
	Critical bool
	Hook     CyclePhaseHook
}

type CycleHookResult struct {
	HookID   string
	Critical bool
	Duration time.Duration
	Error    error
}

type CyclePhaseResult struct {
	Phase       CyclePhase
	Duration    time.Duration
	HookResults []CycleHookResult
}

type CycleResult struct {
	Image             ProcessImage
	ControllerResults []ControllerResult
	Decisions         []Decision
	PhaseResults      []CyclePhaseResult
	Halted            bool
	ClockError        error
	OutputError       error
	Duration          time.Duration
}

type Cycle struct {
	runtime   *Runtime
	scheduler *Scheduler
	writer    OutputWriter

	runMu  sync.Mutex
	lastAt time.Time

	hooksMu sync.RWMutex
	hooks   map[CyclePhase][]CycleHookBinding
}

func NewCycle(runtime *Runtime, scheduler *Scheduler, writer OutputWriter) (*Cycle, error) {
	if runtime == nil {
		return nil, errors.New("runtime is required")
	}
	if scheduler == nil {
		return nil, errors.New("scheduler is required")
	}
	return &Cycle{runtime: runtime, scheduler: scheduler, writer: writer, hooks: map[CyclePhase][]CycleHookBinding{}}, nil
}

func (cycle *Cycle) AddHook(binding CycleHookBinding) error {
	if cycle == nil {
		return errors.New("cycle is nil")
	}
	binding.ID = strings.TrimSpace(binding.ID)
	if binding.ID == "" || binding.Hook == nil {
		return errors.New("cycle hook ID and hook are required")
	}
	if !validCyclePhase(binding.Phase) {
		return fmt.Errorf("unsupported cycle phase %q", binding.Phase)
	}
	cycle.hooksMu.Lock()
	defer cycle.hooksMu.Unlock()
	for _, existing := range cycle.hooks[binding.Phase] {
		if existing.ID == binding.ID {
			return fmt.Errorf("cycle hook %s is already registered for %s", binding.ID, binding.Phase)
		}
	}
	cycle.hooks[binding.Phase] = append(cycle.hooks[binding.Phase], binding)
	sort.SliceStable(cycle.hooks[binding.Phase], func(i, j int) bool {
		return cycle.hooks[binding.Phase][i].ID < cycle.hooks[binding.Phase][j].ID
	})
	return nil
}

func (cycle *Cycle) runPhase(ctx context.Context, phase CyclePhase, phaseContext CyclePhaseContext) (CyclePhaseResult, bool) {
	started := time.Now()
	cycle.hooksMu.RLock()
	bindings := append([]CycleHookBinding(nil), cycle.hooks[phase]...)
	cycle.hooksMu.RUnlock()
	result := CyclePhaseResult{Phase: phase}
	for _, binding := range bindings {
		hookStarted := time.Now()
		err := binding.Hook(ctx, phaseContext)
		result.HookResults = append(result.HookResults, CycleHookResult{
			HookID: binding.ID, Critical: binding.Critical, Duration: time.Since(hookStarted), Error: err,
		})
		if err != nil && binding.Critical {
			result.Duration = time.Since(started)
			return result, true
		}
	}
	result.Duration = time.Since(started)
	return result, false
}

func (cycle *Cycle) appendPhase(ctx context.Context, result *CycleResult, phase CyclePhase, phaseContext CyclePhaseContext) bool {
	phaseResult, halted := cycle.runPhase(ctx, phase, phaseContext)
	result.PhaseResults = append(result.PhaseResults, phaseResult)
	if halted {
		result.Halted = true
	}
	return halted
}

// RunOnce performs one explicit Input -> Process -> Output cycle. The phase order mirrors the
// lifecycle used by the pinned OpenEMS reference: Process Image boundary, Controllers, then Write.
// Driver polling remains asynchronous to Controller execution and publishes next values before this
// method is called; a Protocol Bridge can synchronize its read/write work through Cycle hooks.
func (cycle *Cycle) RunOnce(ctx context.Context, at time.Time) (result CycleResult) {
	cycle.runMu.Lock()
	defer cycle.runMu.Unlock()
	started := time.Now()
	defer func() { result.Duration = time.Since(started) }()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if !cycle.lastAt.IsZero() && at.Before(cycle.lastAt) {
		result.Halted = true
		result.ClockError = fmt.Errorf("%w: previous=%s current=%s", ErrCycleClockRegression, cycle.lastAt.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano))
		return result
	}

	if cycle.appendPhase(ctx, &result, CyclePhaseBeforeProcessImage, CyclePhaseContext{At: at}) {
		return result
	}

	image := cycle.runtime.SwitchProcessImage(at)
	cycle.lastAt = at
	result.Image = image
	if cycle.appendPhase(ctx, &result, CyclePhaseAfterProcessImage, CyclePhaseContext{At: at, Image: image}) {
		return result
	}
	if cycle.appendPhase(ctx, &result, CyclePhaseBeforeControllers, CyclePhaseContext{At: at, Image: image}) {
		return result
	}

	plan := NewControlPlan(image)
	controllerResults, halted := cycle.scheduler.Run(ctx, image, plan)
	result.ControllerResults = controllerResults
	result.Halted = halted
	if cycle.appendPhase(ctx, &result, CyclePhaseAfterControllers, CyclePhaseContext{At: at, Image: image, Halted: halted}) {
		return result
	}
	if halted {
		return result
	}

	result.Decisions = plan.Decisions()
	if cycle.appendPhase(ctx, &result, CyclePhaseBeforeWrite, CyclePhaseContext{At: at, Image: image}) {
		return result
	}

	accepted := make([]Decision, 0, len(result.Decisions))
	for _, decision := range result.Decisions {
		if decision.Accepted && decision.Effective != nil {
			accepted = append(accepted, decision)
		}
	}

	executeStarted := time.Now()
	executeHooks, executeHalted := cycle.runPhase(ctx, CyclePhaseExecuteWrite, CyclePhaseContext{At: at, Image: image})
	if !executeHalted && cycle.writer != nil && len(accepted) > 0 {
		result.OutputError = cycle.writer.Apply(ctx, image, accepted)
		if result.OutputError != nil {
			result.Halted = true
		}
	}
	executeHooks.Duration = time.Since(executeStarted)
	result.PhaseResults = append(result.PhaseResults, executeHooks)
	if executeHalted {
		result.Halted = true
		return result
	}

	cycle.appendPhase(ctx, &result, CyclePhaseAfterWrite, CyclePhaseContext{At: at, Image: image})
	return result
}
