//go:build darwin && cgo

// Package darwin holds optional macOS API shims that need cgo. It never runs
// external commands and never changes anything.
package darwin

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>

// macdoc_purgeable stores the purgeable bytes of the boot volume in *out and
// returns 1, or returns 0 if the values are unavailable. All objects are
// autoreleased inside the pool.
static int macdoc_purgeable(long long *out) {
	int ok = 0;
	@autoreleasepool {
		NSURL *url = [NSURL fileURLWithPath:@"/" isDirectory:YES];
		NSArray *keys = @[NSURLVolumeAvailableCapacityForImportantUsageKey, NSURLVolumeAvailableCapacityKey];
		NSDictionary *vals = [url resourceValuesForKeys:keys error:nil];
		NSNumber *important = vals[NSURLVolumeAvailableCapacityForImportantUsageKey];
		NSNumber *available = vals[NSURLVolumeAvailableCapacityKey];
		if (important != nil && available != nil) {
			long long d = [important longLongValue] - [available longLongValue];
			*out = d < 0 ? 0 : d;
			ok = 1;
		}
	}
	return ok;
}
*/
import "C"

// PurgeableBytes returns the purgeable space on the boot volume: available
// capacity for important usage minus plain available capacity, clamped at 0.
// ok is false if Foundation does not report both values.
func PurgeableBytes() (int64, bool) {
	var n C.longlong
	if C.macdoc_purgeable(&n) == 0 {
		return 0, false
	}
	return int64(n), true
}
