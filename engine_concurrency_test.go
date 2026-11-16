package main

import (
	"bytes"
	"errors"
	"ibus-bamboo/config"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newQueuedEngine builds an engine that queues keys like the real one does.
func newQueuedEngine(inputMode int) (*IBusBambooEngine, *fakeEngine) {
	e := newTestEngine(inputMode)
	e.shouldEnqueuKeyStrokes = true
	return e, e.IEngine.(*fakeEngine)
}

func typeKeys(e *IBusBambooEngine, keys string) {
	for _, key := range keys {
		e.ProcessKeyEvent(uint32(key), 0, 0)
	}
}

func waitForIdle(t *testing.T, engines ...*IBusBambooEngine) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for _, e := range engines {
		for atomic.LoadInt32(&e.pendingKeys) != 0 {
			if time.Now().After(deadline) {
				t.Fatalf("key queue did not drain: %d keys pending", atomic.LoadInt32(&e.pendingKeys))
			}
			time.Sleep(time.Millisecond)
		}
	}
}

func runWithTimeout(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("%s did not finish: deadlock?", what)
	}
}

// Two engines used to share one key queue and one handler, so the engine
// created last handled the keys typed into the other one.
func TestKeyQueueIsPerEngine(t *testing.T) {
	first, firstOut := newQueuedEngine(config.BackspaceForwardingIM)
	second, secondOut := newQueuedEngine(config.BackspaceForwardingIM)
	const firstKeys, secondKeys = "bcg bcg bcg", "klm klm klm"

	runWithTimeout(t, "typing on two engines", func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); typeKeys(first, firstKeys) }()
		go func() { defer wg.Done(); typeKeys(second, secondKeys) }()
		wg.Wait()
		waitForIdle(t, first, second)
	})

	if got := firstOut.committedText(); got != firstKeys {
		t.Errorf("first engine committed %q, want %q", got, firstKeys)
	}
	if got := secondOut.committedText(); got != secondKeys {
		t.Errorf("second engine committed %q, want %q", got, secondKeys)
	}
}

func TestKeyQueueKeepsOrderWhenFull(t *testing.T) {
	e, out := newQueuedEngine(config.BackspaceForwardingIM)
	keys := strings.Repeat("bcg ", keyQueueSize)

	runWithTimeout(t, "typing more keys than the queue holds", func() {
		typeKeys(e, keys)
		waitForIdle(t, e)
	})
	if got := out.committedText(); got != keys {
		t.Errorf("committed %d runes, want the %d typed ones in order", len(got), len(keys))
	}
}

// Engine methods are called from one goroutine per D-Bus message while the
// queue goroutine runs. Run with -race.
func TestEngineMethodsAreSafeToCallConcurrently(t *testing.T) {
	for _, mode := range []int{config.PreeditIM, config.SurroundingTextIM, config.BackspaceForwardingIM} {
		e, _ := newQueuedEngine(mode)
		e.propList = GetPropListByConfig(e.config)
		surrounding := ibusText("xin chao")

		runWithTimeout(t, "concurrent engine calls", func() {
			var wg sync.WaitGroup
			stop := make(chan struct{})
			background := []func(){
				func() { e.FocusIn(); e.FocusOut() },
				func() { e.Reset(); e.Enable(); e.Disable() },
				func() { e.SetSurroundingText(surrounding, 3, 3) },
				func() { e.CursorDown(); e.CursorUp(); e.PageDown(); e.PageUp(); e.CandidateClicked(0, 1, 0) },
				func() { e.SetCapabilities(IBusCapSurroundingText); e.PropertyActivate("-", 0) },
			}
			for _, call := range background {
				wg.Add(1)
				go func(call func()) {
					defer wg.Done()
					for {
						select {
						case <-stop:
							return
						default:
							call()
						}
					}
				}(call)
			}
			for i := 0; i < 30; i++ {
				typeKeys(e, "tieengs vieetj ")
				e.ProcessKeyEvent(IBusLeft, 0, 0)
				e.ProcessKeyEvent(IBusBackSpace, 0, 0)
			}
			close(stop)
			wg.Wait()
			waitForIdle(t, e)
		})
	}
}

// The queue goroutine holds the engine lock while it waits for the XTest
// backspaces it injected. Those must be acknowledged without the lock.
func TestFakeBackspaceDoesNotNeedEngineLock(t *testing.T) {
	e, _ := newQueuedEngine(config.XTestFakeKeyEventIM)
	e.setFakeBackspace(2)

	e.Lock()
	runWithTimeout(t, "fake backspace while the engine is locked", func() {
		if handled, _ := e.ProcessKeyEvent(IBusBackSpace, 0, 0); handled {
			t.Errorf("a fake backspace must be passed on to the application")
		}
	})
	e.Unlock()

	if got := e.getFakeBackspace(); got != 1 {
		t.Errorf("fake backspaces left = %d, want 1", got)
	}
	e.setFakeBackspace(0)
	if e.consumeFakeBackspace() {
		t.Errorf("no fake backspace is pending")
	}
}

func TestFifoGateAdmitsInArrivalOrder(t *testing.T) {
	const arrivals = 50
	gate := newFifoGate()
	gate.enter() // hold the gate while the others line up

	var order []int
	var wg sync.WaitGroup
	for i := 0; i < arrivals; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			gate.enter()
			order = append(order, i) // serialized by the gate
			gate.leave()
		}(i)
		// Wait until this arrival holds its ticket before starting the next.
		for {
			gate.mu.Lock()
			queued := gate.next == uint64(i+2)
			gate.mu.Unlock()
			if queued {
				break
			}
			time.Sleep(50 * time.Microsecond)
		}
	}
	gate.leave()
	runWithTimeout(t, "draining the gate", wg.Wait)

	for i, got := range order {
		if got != i {
			t.Fatalf("admission order = %v, want arrival order", order)
		}
	}
	if len(order) != arrivals {
		t.Fatalf("%d goroutines got through, want %d", len(order), arrivals)
	}
}

func TestDestroyStopsKeyQueue(t *testing.T) {
	e, _ := newQueuedEngine(config.PreeditIM)
	e.Destroy()
	e.Destroy()
	select {
	case <-e.done:
	default:
		t.Fatalf("Destroy should stop the key queue goroutine")
	}
}

func TestGnomeEvalIsSkippedOnceUnavailable(t *testing.T) {
	realEval := gnomeShellEval
	defer func() {
		gnomeShellEval = realEval
		gnomeEvalBlockedUntil = time.Time{}
	}()

	cases := []struct {
		name      string
		eval      func(script string) (bool, string, error)
		want      string
		wantErr   bool
		wantCalls int // Eval calls made by two consecutive lookups
	}{
		{"blocked by the shell", func(string) (bool, string, error) { return false, "", nil }, "", true, 1},
		{"dbus error", func(string) (bool, string, error) { return false, "", errors.New("access denied") }, "", true, 1},
		{"window class", func(string) (bool, string, error) { return true, "firefox", nil }, "firefox", false, 2},
		{"overview", func(script string) (bool, string, error) {
			if script == gnomeOverviewScript {
				return true, "true", nil
			}
			return false, "TypeError: no focused window", nil
		}, "org.gnome.Overview", false, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gnomeEvalBlockedUntil = time.Time{}
			calls := 0
			gnomeShellEval = func(script string) (bool, string, error) {
				calls++
				return tc.eval(script)
			}
			for i := 0; i < 2; i++ {
				got, err := gnomeGetFocusWindowClass()
				if got != tc.want || (err != nil) != tc.wantErr {
					t.Errorf("lookup %d = %q, %v; want %q, error=%v", i+1, got, err, tc.want, tc.wantErr)
				}
			}
			if calls != tc.wantCalls {
				t.Errorf("Eval was called %d times, want %d", calls, tc.wantCalls)
			}
		})
	}
}

func TestTypedTextIsOnlyLoggedInDebugMode(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	wasEnabled := debugEnabled
	defer func() { debugEnabled = wasEnabled }()

	for _, enabled := range []bool{false, true} {
		buf.Reset()
		debugEnabled = enabled
		e := newTestEngine(config.PreeditIM)
		typeKeys(e, "matkhau ")
		if logged := strings.Contains(buf.String(), "matkhau"); logged != enabled {
			t.Errorf("debug=%v: typed text in the log = %v\n%s", enabled, logged, buf.String())
		}
	}
}
