// SPDX-License-Identifier: Unlicense OR MIT

package program

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// hogEnv makes the test binary a program that takes memory, 32 MiB at a
// time, up to a GiB, and tells how much it took after each step.
const hogEnv = "KOMARUGRAM_TEST_HOG"

func TestMain(m *testing.M) {
	if os.Getenv(hogEnv) != "" {
		var kept [][]byte
		for mib := 32; mib <= 1024; mib += 32 {
			b := make([]byte, 32<<20)
			for i := 0; i < len(b); i += 4096 {
				b[i] = 1
			}
			kept = append(kept, b)
			fmt.Println(mib)
			time.Sleep(5 * time.Millisecond)
		}
		runtime.KeepAlive(kept)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A program that takes more memory than StartLimited lets it is stopped
// well before it takes much more, and where the system tells it, the
// caller is told.
func TestStartLimitedStopsAHog(t *testing.T) {
	switch runtime.GOOS {
	case "windows", "linux", "darwin":
	default:
		t.Skip("memory is not bounded here")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command(self, "-test.run=^$")
	cmd.Env = append(os.Environ(), hogEnv+"=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var exceeded atomic.Bool
	start := time.Now()
	if err := StartLimited(cmd, Limits{Memory: 192 << 20}, func() { exceeded.Store(true) }); err != nil {
		t.Fatal(err)
	}
	most := 0
	lines := bufio.NewScanner(out)
	for lines.Scan() {
		if n, err := strconv.Atoi(strings.TrimSpace(lines.Text())); err == nil {
			most = max(most, n)
		}
	}
	waitErr := cmd.Wait()
	t.Logf("took %d MiB in %v, then: %v; told: %v", most, time.Since(start).Round(time.Millisecond), waitErr, exceeded.Load())
	if most >= 1024 || waitErr == nil {
		t.Fatalf("the program took %d MiB, and ended with %v", most, waitErr)
	}
	// Over 192 MiB a step or two, the time of a look at it: the job
	// object stops the first allocation over it, RLIMIT_DATA too, and a
	// watch within 50 ms, some 10 steps of 5 ms.
	if most > 192+12*32 {
		t.Fatalf("the program took %d MiB before it was stopped", most)
	}
	if runtime.GOOS != "linux" && !exceeded.Load() {
		t.Fatal("the caller was not told")
	}
}

// A program within its limit runs to its end, untold.
func TestStartLimitedLetsAProgramBe(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command(self, "-test.run=^$")
	cmd.Env = append(os.Environ(), hogEnv+"=1")
	var exceeded atomic.Bool
	if err := StartLimited(cmd, Limits{Memory: 4 << 30}, func() { exceeded.Store(true) }); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil || exceeded.Load() {
		t.Fatalf("a program within its limit: %v, told %v", err, exceeded.Load())
	}
}
