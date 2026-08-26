package commandservice

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestS309ConcurrentDuplicateSubmissionProducesOneIntent(t *testing.T) {
	service := New(fixedClock())
	request := validRequest()

	const submitters = 100
	results := make(chan SubmitResult, submitters)
	errorsFound := make(chan error, submitters)
	var wait sync.WaitGroup
	wait.Add(submitters)
	for range submitters {
		go func() {
			defer wait.Done()
			result, err := service.Submit(request)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- result
		}()
	}
	wait.Wait()
	close(results)
	close(errorsFound)

	for err := range errorsFound {
		t.Fatalf("concurrent duplicate submit failed: %v", err)
	}
	var commandID string
	nonReplayed := 0
	count := 0
	for result := range results {
		count++
		if commandID == "" {
			commandID = result.Intent.ID
		}
		if result.Intent.ID != commandID {
			t.Fatalf("duplicate submission created multiple intents: first=%s observed=%s", commandID, result.Intent.ID)
		}
		if !result.Replayed {
			nonReplayed++
		}
	}
	if count != submitters || nonReplayed != 1 {
		t.Fatalf("duplicate convergence count=%d nonReplayed=%d", count, nonReplayed)
	}
}

func TestS309InProcessCapacitySmokeMeetsCommandLatencyEnvelope(t *testing.T) {
	// This deterministic in-process smoke test guards algorithmic regressions only.
	// Formal S3-09 capacity certification still requires the target-environment
	// wall-clock attestation validated by run-s3-command-certification.mjs.
	service := New(fixedClock())

	const total = 1000
	const workers = 32
	jobs := make(chan int, total)
	latencies := make(chan time.Duration, total)
	errorsFound := make(chan error, total)
	var uniqueCommands sync.Map
	var completed atomic.Int64
	var wait sync.WaitGroup
	wait.Add(workers)
	started := time.Now()
	for range workers {
		go func() {
			defer wait.Done()
			for index := range jobs {
				request := s309CapacityRequest(index)
				requestStarted := time.Now()
				result, err := service.Submit(request)
				latencies <- time.Since(requestStarted)
				if err != nil {
					errorsFound <- err
					continue
				}
				if result.Replayed || result.Intent.Status != commandmodel.IntentQueued {
					errorsFound <- fmt.Errorf("unexpected capacity result replayed=%t status=%s", result.Replayed, result.Intent.Status)
					continue
				}
				if _, loaded := uniqueCommands.LoadOrStore(result.Intent.ID, struct{}{}); loaded {
					errorsFound <- fmt.Errorf("duplicate command identifier %s", result.Intent.ID)
					continue
				}
				completed.Add(1)
			}
		}()
	}
	for index := range total {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
	close(latencies)
	close(errorsFound)
	elapsed := time.Since(started)

	for err := range errorsFound {
		t.Fatalf("capacity smoke failed: %v", err)
	}
	if completed.Load() != total {
		t.Fatalf("completed commands=%d want=%d", completed.Load(), total)
	}
	observed := make([]time.Duration, 0, total)
	for latency := range latencies {
		observed = append(observed, latency)
	}
	sort.Slice(observed, func(left, right int) bool { return observed[left] < observed[right] })
	p95 := percentileDuration(observed, 0.95)
	p99 := percentileDuration(observed, 0.99)
	if p95 > 300*time.Millisecond || p99 > time.Second {
		t.Fatalf("in-process latency envelope exceeded: p95=%s p99=%s", p95, p99)
	}
	if rate := float64(total) / elapsed.Seconds(); rate < 100 {
		t.Fatalf("in-process throughput below 100 commands/s: rate=%.2f elapsed=%s", rate, elapsed)
	}
}

func s309CapacityRequest(index int) commandmodel.SubmitRequest {
	request := validRequest()
	request.DeviceID = fmt.Sprintf("device-capacity-%04d", index)
	request.IdempotencyKey = fmt.Sprintf("capacity-%04d", index)
	request.Authorization.DeviceID = request.DeviceID
	request.Authorization.GrantID = fmt.Sprintf("grant-capacity-%04d", index)
	return request
}

func percentileDuration(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values)-1) * percentile)
	return values[index]
}
