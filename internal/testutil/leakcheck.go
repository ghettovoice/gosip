package testutil

import (
	"fmt"
	"runtime/pprof"
	"sort"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

const (
	goroutineLeakProfileName = "goroutineleak"
	goroutineLeakMarker      = "(leaked)"
	profileDebugLevel        = 2
	profileCheckAttempts     = 8
	profileCheckRetryDelay   = 25 * time.Millisecond
)

// VerifyTestMain checks for leaked goroutines after all package tests finish.
//
// It uses runtime/pprof profile "goroutineleak" when available.
// If the profile is unavailable, it falls back to go.uber.org/goleak.
func VerifyTestMain(m *testing.M, options ...goleak.Option) {
	profile := pprof.Lookup(goroutineLeakProfileName)

	if profile == nil {
		goleak.VerifyTestMain(m, options...)
		return
	}

	baseline, err := collectLeakedStacks(profile)
	if err != nil {
		panic(fmt.Sprintf("collect %s baseline: %v", goroutineLeakProfileName, err))
	}

	exitCode := m.Run()

	leaks, err := awaitLeakedStacks(profile, baseline)
	if err != nil {
		panic(fmt.Sprintf("collect %s result: %v", goroutineLeakProfileName, err))
	}

	if len(leaks) > 0 {
		report := formatLeakReport(leaks)
		if exitCode == 0 {
			panic(report)
		}

		fmt.Println(report)
	}
}

// VerifyNone checks for leaked goroutines at the end of a single test.
//
// It uses runtime/pprof profile "goroutineleak" when available.
// If the profile is unavailable, it falls back to go.uber.org/goleak.
func VerifyNone(t *testing.T, options ...goleak.Option) {
	t.Helper()

	profile := pprof.Lookup(goroutineLeakProfileName)
	if profile == nil {
		goleak.VerifyNone(t, options...)
		return
	}

	baseline, err := collectLeakedStacks(profile)
	if err != nil {
		t.Fatalf("collect %s baseline: %v", goroutineLeakProfileName, err)
	}

	t.Cleanup(func() {
		t.Helper()

		leaks, leakErr := awaitLeakedStacks(profile, baseline)
		if leakErr != nil {
			t.Fatalf("collect %s result: %v", goroutineLeakProfileName, leakErr)
		}

		if len(leaks) == 0 {
			return
		}

		t.Fatalf("%s", formatLeakReport(leaks))
	})
}

func awaitLeakedStacks(profile *pprof.Profile, baseline map[string]int) ([]string, error) {
	for attempt := range profileCheckAttempts {
		current, err := collectLeakedStacks(profile)
		if err != nil {
			return nil, err
		}

		leaks := diffLeakedStacks(baseline, current)
		if len(leaks) == 0 {
			return nil, nil
		}

		if attempt == profileCheckAttempts-1 {
			return leaks, nil
		}

		time.Sleep(profileCheckRetryDelay)
	}

	return nil, nil
}

func collectLeakedStacks(profile *pprof.Profile) (map[string]int, error) {
	var profileData strings.Builder
	if err := profile.WriteTo(&profileData, profileDebugLevel); err != nil {
		return nil, err
	}

	leakedStacks := make(map[string]int)
	for block := range strings.SplitSeq(profileData.String(), "\n\n") {
		stack := strings.TrimSpace(block)
		if stack == "" || !strings.Contains(stack, goroutineLeakMarker) {
			continue
		}

		leakedStacks[stack]++
	}

	return leakedStacks, nil
}

func diffLeakedStacks(baseline, current map[string]int) []string {
	leaks := make([]string, 0, len(current))
	for stack, currentCount := range current {
		newLeakCount := currentCount - baseline[stack]
		for ; newLeakCount > 0; newLeakCount-- {
			leaks = append(leaks, stack)
		}
	}

	sort.Strings(leaks)

	return leaks
}

func formatLeakReport(leaks []string) string {
	var report strings.Builder
	report.WriteString("goroutine leaks detected by runtime/pprof profile \"goroutineleak\":\n\n")
	report.WriteString(strings.Join(leaks, "\n\n"))

	return report.String()
}
