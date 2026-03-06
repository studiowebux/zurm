//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

void bellPlaySound() {
	NSBeep();
}

void bellSetDockBadge() {
	dispatch_async(dispatch_get_main_queue(), ^{
		[[[NSApplication sharedApplication] dockTile] setBadgeLabel:@"!"];
	});
}

void bellClearDockBadge() {
	dispatch_async(dispatch_get_main_queue(), ^{
		[[[NSApplication sharedApplication] dockTile] setBadgeLabel:@""];
	});
}

void bellRequestAttention() {
	dispatch_async(dispatch_get_main_queue(), ^{
		[[NSApplication sharedApplication] requestUserAttention:NSInformationalRequest];
	});
}
*/
import "C"

func playBellSound() {
	C.bellPlaySound()
}

func setDockBadge() {
	C.bellSetDockBadge()
}

func clearDockBadge() {
	C.bellClearDockBadge()
}

func requestDockAttention() {
	C.bellRequestAttention()
}
