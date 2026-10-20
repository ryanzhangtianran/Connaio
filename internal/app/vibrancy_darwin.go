package app

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <objc/message.h>
#include <stdlib.h>
#include <stdbool.h>

// makeVibrant lets the window's blur through: Wails adds the effect view for a translucent window
// but leaves its default, all but opaque material, and its "transparent webview" call doesn't take
// on current macOS, so the webview still paints an opaque background over it.
// placeLights centres the close button on the floating sidebar's corner arc (the sidebar sits gap
// from the window edges with corner radius r), so the traffic lights are concentric with it;
// minimise and zoom follow at their usual spacing.
static void placeLights(NSWindow *w, CGFloat gap, CGFloat r) {
	NSButton *close = [w standardWindowButton:NSWindowCloseButton];
	NSView *bar = close.superview;
	if (!bar) return;
	CGFloat step = [w standardWindowButton:NSWindowMiniaturizeButton].frame.origin.x - close.frame.origin.x;
	NSWindowButton kinds[] = {NSWindowCloseButton, NSWindowMiniaturizeButton, NSWindowZoomButton};
	for (int i = 0; i < 3; i++) {
		NSButton *b = [w standardWindowButton:kinds[i]];
		NSSize sz = b.frame.size;
		CGFloat cx = gap + r + i * step, cy = gap + r; // centre, from the window's top-left
		[b setFrameOrigin:NSMakePoint(cx - sz.width / 2, NSHeight(bar.frame) - cy - sz.height / 2)];
	}
}

// keepLights re-places them whenever AppKit lays the title bar out again.
static void keepLights(NSWindow *w, CGFloat gap, CGFloat r) {
	placeLights(w, gap, r);
	for (NSNotificationName n in @[NSWindowDidResizeNotification, NSWindowDidEndLiveResizeNotification, NSWindowDidBecomeKeyNotification,
		NSWindowDidResignKeyNotification, NSWindowDidExitFullScreenNotification, NSWindowDidBecomeMainNotification]) {
		[NSNotificationCenter.defaultCenter addObserverForName:n object:w queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note) {
			placeLights(w, gap, r);
		}];
	}
}

static void makeVibrant(void *w) {
	dispatch_async(dispatch_get_main_queue(), ^{
		keepLights((NSWindow *)w, 12, 14); // style.css: --gap and the sidebar's border-radius
		for (NSView *v in ((NSWindow *)w).contentView.subviews) {
			if ([v isKindOfClass:NSVisualEffectView.class]) {
				((NSVisualEffectView *)v).material = NSVisualEffectMaterialSidebar;
			} else if ([v isKindOfClass:WKWebView.class]) {
				[v setValue:@NO forKey:@"drawsBackground"];
				if (@available(macOS 12, *)) ((WKWebView *)v).underPageBackgroundColor = NSColor.clearColor;
			}
		}
	});
}

// makeFloating dresses the floating process monitor: the menus' translucent material, kept clear
// while the window isn't focused (as a floating one mostly isn't), cut to the page's rounded corners,
// with a shadow following them.
static void makeFloating(void *p) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *w = p;
		w.opaque = NO;
		w.backgroundColor = NSColor.clearColor;
		w.hasShadow = YES;
		for (NSView *v in w.contentView.subviews) {
			if ([v isKindOfClass:NSVisualEffectView.class]) {
				NSVisualEffectView *e = (NSVisualEffectView *)v;
				e.material = NSVisualEffectMaterialMenu;
				e.state = NSVisualEffectStateActive;
				e.wantsLayer = YES;
				e.layer.cornerRadius = 12; // style.css .monitor
				e.layer.masksToBounds = YES;
			} else if ([v isKindOfClass:WKWebView.class]) {
				[v setValue:@NO forKey:@"drawsBackground"];
				if (@available(macOS 12, *)) ((WKWebView *)v).underPageBackgroundColor = NSColor.clearColor;
			}
		}
		[w invalidateShadow];
	});
}

// pngOf draws img into a px×px PNG, centred and scaled so its longer side fills it, in a malloc'd
// buffer of *n bytes, or NULL.
static void *pngOf(NSImage *img, int px, int *n) {
	if (!img) return NULL;
	NSBitmapImageRep *rep = [[[NSBitmapImageRep alloc] initWithBitmapDataPlanes:NULL pixelsWide:px pixelsHigh:px
		bitsPerSample:8 samplesPerPixel:4 hasAlpha:YES isPlanar:NO colorSpaceName:NSDeviceRGBColorSpace bytesPerRow:0 bitsPerPixel:0] autorelease];
	[NSGraphicsContext saveGraphicsState];
	NSGraphicsContext.currentContext = [NSGraphicsContext graphicsContextWithBitmapImageRep:rep];
	CGFloat k = MIN(px / img.size.width, px / img.size.height);
	NSSize s = NSMakeSize(img.size.width * k, img.size.height * k);
	[img drawInRect:NSMakeRect((px - s.width) / 2, (px - s.height) / 2, s.width, s.height)];
	[NSGraphicsContext restoreGraphicsState];
	NSData *png = [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
	if (!png) return NULL;
	*n = (int)png.length;
	void *out = malloc(png.length);
	memcpy(out, png.bytes, png.length);
	return out;
}

// setDockVisible shows the app in the Dock (a regular app, brought forward) or takes it out, so a
// closed window leaves only the menu bar item running.
static void setDockVisible(bool visible) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp setActivationPolicy:visible ? NSApplicationActivationPolicyRegular : NSApplicationActivationPolicyAccessory];
		if (visible) [NSApp activateIgnoringOtherApps:YES];
	});
}

// freeWebView ends a closing window's page, and with it WebKit's process for it: Wails allocates
// the view and never releases it, so the page would run on behind the closed window. The view is
// not released here either (AppKit and Wails may still reach it as the window closes); its page is
// closed instead, and Wails' unretained webView properties on the window and its delegate cleared,
// so nothing calls into it again. Runs on the main thread, before Wails closes the window.
// ponytail: the view object itself stays allocated, a few KB per closed window
@protocol WailsWebViewHolder
@property (assign) WKWebView *webView;
@end

static void freeWebView(void *p) {
	NSWindow *w = p;
	if (![w respondsToSelector:@selector(webView)]) return;
	WKWebView *view = ((id<WailsWebViewHolder>)w).webView;
	if (!view) return;
	((id<WailsWebViewHolder>)w).webView = nil;
	if ([w.delegate respondsToSelector:@selector(setWebView:)]) ((id<WailsWebViewHolder>)w.delegate).webView = nil;
	[w makeFirstResponder:nil];
	[view stopLoading];
	[view removeFromSuperview];
	SEL closePage = NSSelectorFromString(@"_close"); // WebKit's own: ends the page and its process
	if ([view respondsToSelector:closePage]) {
		((void (*)(id, SEL))objc_msgSend)(view, closePage);
	} else {
		[view loadHTMLString:@"" baseURL:nil]; // at least the page's memory goes
	}
}

// iconPNG is the Finder icon of path as a 64 px PNG.
static void *iconPNG(const char *path, int *n) {
	@autoreleasepool {
		return pngOf([[NSWorkspace sharedWorkspace] iconForFile:@(path)], 64, n);
	}
}
*/
import "C"

import (
	"encoding/base64"
	"sync"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func setDockVisible(visible bool) { C.setDockVisible(C.bool(visible)) }

func makeVibrant(w *application.WebviewWindow) {
	if p := w.NativeWindow(); p != nil {
		C.makeVibrant(p)
	}
}

// freeWebView frees a closing window's web view, which Wails would leave running (see the C side).
func freeWebView(w *application.WebviewWindow) {
	if p := w.NativeWindow(); p != nil {
		application.InvokeSync(func() { C.freeWebView(p) })
	}
}

func makeFloating(w *application.WebviewWindow) {
	if p := w.NativeWindow(); p != nil {
		C.makeFloating(p)
	}
}

var icons sync.Map // key -> data URL

// AppIcon is the Finder icon of path (an app bundle, or an executable's generic icon) as a PNG data
// URL, for the activity page's client list.
func (s *SettingsService) AppIcon(path string) string {
	return cachedPNG(path, func(n *C.int) unsafe.Pointer {
		cs := C.CString(path)
		defer C.free(unsafe.Pointer(cs))
		return C.iconPNG(cs, n)
	})
}

// cachedPNG is render's malloc'd PNG as a data URL, cached under key; "" when render gives NULL.
func cachedPNG(key string, render func(n *C.int) unsafe.Pointer) string {
	if v, ok := icons.Load(key); ok {
		return v.(string)
	}
	var n C.int
	p := render(&n)
	if p == nil {
		return ""
	}
	defer C.free(p)
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(C.GoBytes(p, n))
	icons.Store(key, url)
	return url
}
