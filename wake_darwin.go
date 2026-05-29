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

// modifierFlagsHardware returns the current hardware modifier key state as a
// bitmask of NSEventModifierFlags. Using NSEvent.modifierFlags queries the actual
// physical key state from the kernel, bypassing GLFW's event-driven cache which
// can get stuck if a key-up event fires while another window owns focus.
unsigned long modifierFlagsHardware(void) {
	return (unsigned long)[NSEvent modifierFlags];
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
			// Restore the user-configured TPS so the Ebiten loop starts running
			// again. handleFocus re-applies it via unsuspendAndRedraw() as soon
			// as it processes screenWakeFlag — this just unfreezes the loop.
			restoreConfiguredTPS()
			screenWakeFlag.Store(1)
		}
	}
}

// NSEventModifierFlags bitmasks (from NSEvent.h).
const (
	nsModShift   = 0x020000 // NSEventModifierFlagShift
	nsModControl = 0x040000 // NSEventModifierFlagControl
	nsModOption  = 0x080000 // NSEventModifierFlagOption  (Alt/Option)
	nsModCommand = 0x100000 // NSEventModifierFlagCommand (Meta/Cmd)
)

// hardwareModifiers returns the physical modifier key state by querying
// NSEvent.modifierFlags directly from the kernel. This is immune to GLFW's
// event-cache getting stuck when key-up events fire while another window owns focus.
func hardwareModifiers() (cmd, ctrl, shift, alt bool) {
	flags := uint(C.modifierFlagsHardware())
	return flags&nsModCommand != 0,
		flags&nsModControl != 0,
		flags&nsModShift != 0,
		flags&nsModOption != 0
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
