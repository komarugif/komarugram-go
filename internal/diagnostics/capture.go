package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"runtime/trace"
	"time"
)

// Capture writes a private, opt-in local artifact. No HTTP/pprof listener is opened.
// A profile of the process is recorded in the processes of the client's
// own that run (SetChildProfiler) at the same time, each to a file of its
// own beside it; paths are those files, the process's first.
func (r *Recorder) Capture(directory, kind string) (paths []string, err error) {
	if !r.capture.CompareAndSwap(false, true) {
		return nil, errors.New("another capture is running")
	}
	defer r.capture.Store(false)
	r.captureMu.Lock()
	defer r.captureMu.Unlock()
	if Current() != r {
		return nil, errors.New("profiler is closed")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	extension := ".pprof"
	if kind == "snapshot" {
		extension = ".json"
	}
	if kind == "trace" {
		extension = ".trace"
	}
	stamp := time.Now().Format("20060102-150405.000000000")
	path, err := filepath.Abs(filepath.Join(directory, fmt.Sprintf("%s-%s%s", stamp, kind, extension)))
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lasts time.Duration
	switch kind {
	case "cpu":
		lasts = 15 * time.Second
	case "trace":
		lasts = 5 * time.Second
	}
	var children []ChildProfile
	childrenDone := make(chan struct{})
	if kind == "snapshot" {
		close(childrenDone)
	} else {
		// They stop waiting when the profiler closes, as the process's own
		// profile stops then.
		ctx, cancel := context.WithTimeout(context.Background(), lasts+30*time.Second)
		defer cancel()
		go func() {
			select {
			case <-r.done:
				cancel()
			case <-ctx.Done():
			}
		}()
		go func() {
			defer close(childrenDone)
			children = profileChildren(ctx, kind, lasts)
		}()
	}
	switch kind {
	case "snapshot":
		enc := json.NewEncoder(file)
		enc.SetIndent("", "  ")
		err = enc.Encode(r.Snapshot())
	case "cpu":
		if err = pprof.StartCPUProfile(file); err == nil {
			r.wait(lasts)
			pprof.StopCPUProfile()
		}
	case "trace":
		if err = trace.Start(file); err == nil {
			r.wait(lasts)
			trace.Stop()
		}
	case "heap", "allocs", "goroutine":
		err = pprof.Lookup(kind).WriteTo(file, 0)
	default:
		err = errors.New("unknown capture kind")
	}
	if err == nil {
		err = file.Sync()
	}
	paths = []string{path}
	<-childrenDone
	if err != nil {
		return paths, err
	}
	var errs []error
	for _, c := range children {
		if c.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.Name, c.Err))
			continue
		}
		child := filepath.Join(filepath.Dir(path), fmt.Sprintf("%s-%s-%s-%d%s", stamp, kind, c.Name, c.PID, extension))
		if werr := writeNew(child, c.Data); werr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.Name, werr))
			continue
		}
		paths = append(paths, child)
	}
	return paths, errors.Join(errs...)
}

// writeNew writes data to a new file of path, private.
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (r *Recorder) wait(d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-r.done:
	}
}
