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

// idleEnv makes the test binary a program that waits for a line on its
// standard input, then takes idleTakes and says "done".
const idleEnv = "KOMARUGRAM_TEST_IDLE"

const idleTakes = 80 << 20

// reservedForTest is a .bss of the test binary's, of the kind a library of
// FFmpeg's has: reserved, never touched, and counted by RLIMIT_DATA.
var reservedForTest [64 << 20]byte

func TestMain(m *testing.M) {
	if os.Getenv(idleEnv) != "" {
		runtime.KeepAlive(&reservedForTest)
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			os.Exit(3)
		}
		b := make([]byte, idleTakes)
		for i := 0; i < len(b); i += 4096 {
			b[i] = 1
		}
		runtime.KeepAlive(b)
		fmt.Println("done")
		os.Exit(0)
	}
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
	case "windows", "linux", "darwin", "haiku":
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

// What a program reserves as it starts, its libraries' .bss, is measured,
// and is not counted in its bound: a program that takes less than its
// bound runs, however much its libraries reserve.
func TestReservedIsNotCounted(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("nothing counts memory reserved here")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(idleEnv, "1")
	reserved := Reserved(self, "-test.run=^$")
	if reserved < uint64(len(reservedForTest)) {
		t.Fatalf("reserved %d MiB, the .bss alone is %d", reserved>>20, len(reservedForTest)>>20)
	}
	run := func(limits Limits) error {
		cmd := Command(self, "-test.run=^$")
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		cmd.Stdout = &out
		if err := StartLimited(cmd, limits, nil); err != nil {
			t.Fatal(err)
		}
		_, _ = in.Write([]byte("\n"))
		_ = in.Close()
		err = cmd.Wait()
		if err == nil && strings.TrimSpace(out.String()) != "done" {
			err = fmt.Errorf("said %q", out.String())
		}
		return err
	}
	// It takes 80 MiB, of a bound of 128, and its .bss is 64 more.
	memory := uint64(128 << 20)
	if err := run(Limits{Memory: memory, Reserved: reserved}); err != nil {
		t.Fatalf("a program within its bound, its .bss aside: %v", err)
	}
	if err := run(Limits{Memory: memory}); err == nil {
		t.Fatal("the .bss counted in the bound left room: the test shows nothing")
	}
}
