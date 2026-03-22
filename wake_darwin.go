package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>
#include <stdatomic.h>

static _Atomic int wakeFlag = 0;

void installWakeObserver(void) {
	[[[NSWorkspace sharedWorkspace] notificationCenter]
		addObserverForName:NSWorkspaceDidWakeNotification
		object:nil
		queue:nil
		usingBlock:^(NSNotification *note) {
			atomic_store_explicit(&wakeFlag, 1, memory_order_relaxed);
		}];
}

int consumeWakeFlag(void) {
	return atomic_exchange_explicit(&wakeFlag, 0, memory_order_relaxed);
}
*/
import "C"

func init() {
	C.installWakeObserver()
}

// consumeWake returns true once after each system wake event and clears the flag.
// Safe to call from any goroutine.
func consumeWake() bool {
	return C.consumeWakeFlag() != 0
}
