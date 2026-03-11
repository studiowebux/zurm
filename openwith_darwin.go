//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework Carbon

#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#include <stdlib.h>

static char *openWithPath = NULL;

// openDocsHandler is the Apple Event handler for kAEOpenDocuments.
// It extracts the first path via NSAppleEventDescriptor / NSURL (no FSRef).
static OSErr openDocsHandler(const AppleEvent *event, AppleEvent *reply, SRefCon refCon) {
    AEDescList docList;
    OSErr err = AEGetParamDesc(event, keyDirectObject, typeAEList, &docList);
    if (err != noErr) return err;

    long count = 0;
    AECountItems(&docList, &count);
    if (count > 0) {
        AEDesc itemDesc;
        if (AEGetNthDesc(&docList, 1, typeWildCard, NULL, &itemDesc) == noErr) {
            // NSAppleEventDescriptor takes ownership of itemDesc — do not dispose it.
            NSAppleEventDescriptor *desc =
                [[NSAppleEventDescriptor alloc] initWithAEDescNoCopy:&itemDesc];
            NSURL *url = [desc fileURLValue];
            if (url != nil) {
                const char *path = [[url path] UTF8String];
                if (path != NULL) {
                    if (openWithPath != NULL) free(openWithPath);
                    openWithPath = strdup(path);
                }
            }
        }
    }

    AEDisposeDesc(&docList);
    return noErr;
}

void zrm_registerOpenWithHandler() {
    AEInstallEventHandler(kCoreEventClass, kAEOpenDocuments,
                          openDocsHandler, 0, false);
}

void zrm_drainEvents() {
    NSRunLoop *loop = [NSRunLoop currentRunLoop];
    NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:0.15];
    [loop runUntilDate:deadline];
}

const char *zrm_getOpenWithPath() {
    return openWithPath;
}
*/
import "C"

import "unsafe"

func init() {
	C.zrm_registerOpenWithHandler()
}

// drainOpenWithEvents spins the NSRunLoop briefly to receive any pending
// Apple Events (e.g. kAEOpenDocuments from Finder "Open With") before the
// Ebitengine run loop starts. Returns the path sent by macOS, or "".
func drainOpenWithEvents() string {
	C.zrm_drainEvents()
	p := C.zrm_getOpenWithPath()
	if p == nil {
		return ""
	}
	return C.GoString((*C.char)(unsafe.Pointer(p)))
}
