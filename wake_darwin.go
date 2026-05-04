package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>
#include <stdatomic.h>

static _Atomic int wakeFlag       = 0;
static _Atomic int willSleepFlag  = 0;
static _Atomic int screenChangeFlag = 0;

void installWakeObserver(void) {
	NSNotificationCenter *wsCenter = [[NSWorkspace sharedWorkspace] notificationCenter];

	// System fully awake.
	[wsCenter addObserverForName:NSWorkspaceDidWakeNotification
		object:nil queue:nil
		usingBlock:^(NSNotification *note) {
			atomic_store_explicit(&wakeFlag, 1, memory_order_relaxed);
		}];

	// System is about to sleep — fires while the run loop is still live,
	// giving us a window to halt the Metal game loop before nextDrawable blocks.
	[wsCenter addObserverForName:NSWorkspaceWillSleepNotification
		object:nil queue:nil
		usingBlock:^(NSNotification *note) {
			atomic_store_explicit(&willSleepFlag, 1, memory_order_relaxed);
		}];

	// Display configuration changed (HDMI plug/unplug, lid open/close, resolution
	// change). Fires on the default NSNotificationCenter, not the workspace one.
	[[NSNotificationCenter defaultCenter]
		addObserverForName:NSApplicationDidChangeScreenParametersNotification
		object:nil queue:nil
		usingBlock:^(NSNotification *note) {
			atomic_store_explicit(&screenChangeFlag, 1, memory_order_relaxed);
		}];
}

int consumeWakeFlag(void) {
	return atomic_exchange_explicit(&wakeFlag, 0, memory_order_relaxed);
}

int consumeWillSleepFlag(void) {
	return atomic_exchange_explicit(&willSleepFlag, 0, memory_order_relaxed);
}

int consumeScreenChangeFlag(void) {
	return atomic_exchange_explicit(&screenChangeFlag, 0, memory_order_relaxed);
}
*/
import "C"

import (
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

// screenSleeping is set to 1 by the sleepWatcher goroutine when WillSleep fires
// and cleared when the watcher restores TPS on wake. Checked by handleFocus to
// decide whether to call unsuspendAndRedraw after a screen-sleep cycle.
var screenSleeping atomic.Int32

// screenWakeFlag is set by sleepWatcher after it restores TPS, so that
// handleFocus (back on the game loop) can run unsuspendAndRedraw.
var screenWakeFlag atomic.Int32

func init() {
	C.installWakeObserver()
	go sleepWatcher()
}

// sleepWatcher runs as a background goroutine for the lifetime of the process.
// It bridges two gaps that the in-loop handleFocus cannot cover:
//
//  1. WillSleep: the game loop may already be blocked inside a Metal present call
//     by the time the next Update() runs, so we must set TPS=0 here — before the
//     display actually powers off — to stop the display link from firing.
//
//  2. Wake after screen-sleep: TPS is 0, so Update()/handleFocus() never run to
//     consume consumeWake(). The watcher restores TPS to unfreeze the loop, then
//     sets screenWakeFlag for handleFocus to pick up.
func sleepWatcher() {
	for {
		time.Sleep(8 * time.Millisecond)

		if C.consumeWillSleepFlag() != 0 {
			screenSleeping.Store(1)
			ebiten.SetTPS(0)
		}

		// Only intervene on wake when we were the ones who zeroed TPS.
		if screenSleeping.Load() != 0 && C.consumeWakeFlag() != 0 {
			screenSleeping.Store(0)
			// Restore a baseline TPS so the Ebiten loop starts running again.
			// handleFocus will correct it to the user-configured value via
			// unsuspendAndRedraw() as soon as it processes screenWakeFlag.
			ebiten.SetTPS(60)
			screenWakeFlag.Store(1)
		}
	}
}

// consumeWake returns true once after each system wake event (non-screen-sleep
// path) and clears the C atomic flag. Safe to call from the game loop only.
func consumeWake() bool {
	return C.consumeWakeFlag() != 0
}

// consumeScreenWake returns true once after a screen-sleep/wake cycle where the
// sleepWatcher goroutine already restored TPS. Consumed by handleFocus.
func consumeScreenWake() bool {
	return screenWakeFlag.CompareAndSwap(1, 0)
}

// consumeScreenChange returns true once after a display configuration change
// (HDMI connect/disconnect, resolution change). Consumed by handleFocus.
func consumeScreenChange() bool {
	return C.consumeScreenChangeFlag() != 0
}
