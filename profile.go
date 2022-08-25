// Package profile provides a simple way to manage runtime/pprof
// profiling of your Go application.
package profile

import (
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"sync/atomic"
)

const (
	cpuMode = iota
	memMode
	mutexMode
	blockMode
	traceMode
	threadCreateMode
	goroutineMode
)

// Profile represents an active profiling session.
type Profile struct {
	// quiet suppresses informational messages during profiling.
	quiet bool

	// noShutdownHook controls whether the profiling package should
	// hook SIGINT to write profiles cleanly.
	noShutdownHook bool

	// mode holds the type of profiling that will be made
	mode int

	// path holds the base path where various profiling files are written.
	// If blank, the base path will be generated by ioutil.TempDir.
	path string

	// fname holds the filename of the profile file.
	fname string

	// memProfileRate holds the rate for the memory profile.
	memProfileRate int

	// memProfileType holds the profile type for memory
	// profiles. Allowed values are `heap` and `allocs`.
	memProfileType string

	// closer holds a cleanup function that run after each profile
	closer func()

	// stopped records if a call to profile.Stop has been made
	stopped uint32
}

// NoShutdownHook controls whether the profiling package should
// hook SIGINT to write profiles cleanly.
// Programs with more sophisticated signal handling should set
// this to true and ensure the Stop() function returned from Start()
// is called during shutdown.
func NoShutdownHook(p *Profile) { p.noShutdownHook = true }

// Quiet suppresses informational messages during profiling.
func Quiet(p *Profile) { p.quiet = true }

// CPUProfile enables cpu profiling.
// It disables any previous profiling settings.
func CPUProfile(p *Profile) { p.mode = cpuMode }

// DefaultMemProfileRate is the default memory profiling rate.
// See also http://golang.org/pkg/runtime/#pkg-variables
const DefaultMemProfileRate = 4096

// MemProfile enables memory profiling.
// It disables any previous profiling settings.
func MemProfile(p *Profile) {
	p.memProfileRate = DefaultMemProfileRate
	p.mode = memMode
}

// MemProfileRate enables memory profiling at the preferred rate.
// It disables any previous profiling settings.
func MemProfileRate(rate int) func(*Profile) {
	return func(p *Profile) {
		p.memProfileRate = rate
		p.mode = memMode
	}
}

// MemProfileHeap changes which type of memory profiling to profile
// the heap.
func MemProfileHeap(p *Profile) {
	p.memProfileType = "heap"
	p.mode = memMode
}

// MemProfileAllocs changes which type of memory to profile
// allocations.
func MemProfileAllocs(p *Profile) {
	p.memProfileType = "allocs"
	p.mode = memMode
}

// MutexProfile enables mutex profiling.
// It disables any previous profiling settings.
func MutexProfile(p *Profile) { p.mode = mutexMode }

// BlockProfile enables block (contention) profiling.
// It disables any previous profiling settings.
func BlockProfile(p *Profile) { p.mode = blockMode }

// Trace profile enables execution tracing.
// It disables any previous profiling settings.
func TraceProfile(p *Profile) { p.mode = traceMode }

// ThreadcreationProfile enables thread creation profiling..
// It disables any previous profiling settings.
func ThreadcreationProfile(p *Profile) { p.mode = threadCreateMode }

// GoroutineProfile enables goroutine profiling.
// It disables any previous profiling settings.
func GoroutineProfile(p *Profile) { p.mode = goroutineMode }

// ProfilePath controls the base path where various profiling
// files are written. If blank, the base path will be generated
// by ioutil.TempDir.
func ProfilePath(path string) func(*Profile) {
	return func(p *Profile) {
		p.path = path
	}
}

// ProfileFilename controls the the filename of the profile file
// to be written. It must not contain any path elements.
func ProfileFilename(fname string) func(*Profile) {
	return func(p *Profile) {
		p.fname = fname
	}
}

// Stop stops the profile and flushes any unwritten data.
func (p *Profile) Stop() {
	if !atomic.CompareAndSwapUint32(&p.stopped, 0, 1) {
		// someone has already called close
		return
	}
	p.closer()
	atomic.StoreUint32(&started, 0)
}

// started is non zero if a profile is running.
var started uint32

// Start starts a new profiling session.
// The caller should call the Stop method on the value returned
// to cleanly stop profiling.
func Start(options ...func(*Profile)) interface {
	Stop()
} {
	if !atomic.CompareAndSwapUint32(&started, 0, 1) {
		log.Fatal("profile: Start() already called")
	}

	var prof Profile
	for _, option := range options {
		option(&prof)
	}

	if prof.fname != "" && filepath.Base(prof.fname) != prof.fname {
		log.Fatalf("profile: filename must not contain path elements")
	}

	fname := func(defaultName string) string {
		if prof.fname != "" {
			return prof.fname
		}
		return defaultName
	}

	path, err := func() (string, error) {
		if p := prof.path; p != "" {
			return p, os.MkdirAll(p, 0777)
		}
		return ioutil.TempDir("", "profile")
	}()
	if err != nil {
		log.Fatalf("profile: could not create initial output directory: %v", err)
	}

	logf := func(format string, args ...interface{}) {
		if !prof.quiet {
			log.Printf(format, args...)
		}
	}

	if prof.memProfileType == "" {
		prof.memProfileType = "heap"
	}

	switch prof.mode {
	case cpuMode:
		fn := filepath.Join(path, fname("cpu.pprof"))
		f, err := os.Create(fn)
		if err != nil {
			log.Fatalf("profile: could not create cpu profile %q: %v", fn, err)
		}
		logf("profile: cpu profiling enabled, %s", fn)
		pprof.StartCPUProfile(f)
		prof.closer = func() {
			pprof.StopCPUProfile()
			f.Close()
			logf("profile: cpu profiling disabled, %s", fn)
		}

	case memMode:
		fn := filepath.Join(path, fname("mem.pprof"))
		f, err := os.Create(fn)
		if err != nil {
			log.Fatalf("profile: could not create memory profile %q: %v", fn, err)
		}
		old := runtime.MemProfileRate
		runtime.MemProfileRate = prof.memProfileRate
		logf("profile: memory profiling enabled (rate %d), %s", runtime.MemProfileRate, fn)
		prof.closer = func() {
			pprof.Lookup(prof.memProfileType).WriteTo(f, 0)
			f.Close()
			runtime.MemProfileRate = old
			logf("profile: memory profiling disabled, %s", fn)
		}

	case mutexMode:
		fn := filepath.Join(path, fname("mutex.pprof"))
		f, err := os.Create(fn)
		if err != nil {
			log.Fatalf("profile: could not create mutex profile %q: %v", fn, err)
		}
		runtime.SetMutexProfileFraction(1)
		logf("profile: mutex profiling enabled, %s", fn)
		prof.closer = func() {
			if mp := pprof.Lookup("mutex"); mp != nil {
				mp.WriteTo(f, 0)
			}
			f.Close()
			runtime.SetMutexProfileFraction(0)
			logf("profile: mutex profiling disabled, %s", fn)
		}

	case blockMode:
		fn := filepath.Join(path, fname("block.pprof"))
		f, err := os.Create(fn)
		if err != nil {
			log.Fatalf("profile: could not create block profile %q: %v", fn, err)
		}
		runtime.SetBlockProfileRate(1)
		logf("profile: block profiling enabled, %s", fn)
		prof.closer = func() {
			pprof.Lookup("block").WriteTo(f, 0)
			f.Close()
			runtime.SetBlockProfileRate(0)
			logf("profile: block profiling disabled, %s", fn)
		}

	case threadCreateMode:
		fn := filepath.Join(path, fname("threadcreation.pprof"))
		f, err := os.Create(fn)
		if err != nil {
			log.Fatalf("profile: could not create thread creation profile %q: %v", fn, err)
		}
		logf("profile: thread creation profiling enabled, %s", fn)
		prof.closer = func() {
			if mp := pprof.Lookup("threadcreate"); mp != nil {
				mp.WriteTo(f, 0)
			}
			f.Close()
			logf("profile: thread creation profiling disabled, %s", fn)
		}

	case traceMode:
		fn := filepath.Join(path, fname("trace.out"))
		f, err := os.Create(fn)
		if err != nil {
			log.Fatalf("profile: could not create trace output file %q: %v", fn, err)
		}
		if err := trace.Start(f); err != nil {
			log.Fatalf("profile: could not start trace: %v", err)
		}
		logf("profile: trace enabled, %s", fn)
		prof.closer = func() {
			trace.Stop()
			logf("profile: trace disabled, %s", fn)
		}

	case goroutineMode:
		fn := filepath.Join(path, fname("goroutine.pprof"))
		f, err := os.Create(fn)
		if err != nil {
			log.Fatalf("profile: could not create goroutine profile %q: %v", fn, err)
		}
		logf("profile: goroutine profiling enabled, %s", fn)
		prof.closer = func() {
			if mp := pprof.Lookup("goroutine"); mp != nil {
				mp.WriteTo(f, 0)
			}
			f.Close()
			logf("profile: goroutine profiling disabled, %s", fn)
		}
	}

	if !prof.noShutdownHook {
		go func() {
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt)
			<-c

			log.Println("profile: caught interrupt, stopping profiles")
			prof.Stop()

			os.Exit(0)
		}()
	}

	return &prof
}
