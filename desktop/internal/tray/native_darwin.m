#import <Cocoa/Cocoa.h>
#import <dispatch/dispatch.h>

extern void kivoTrayReady(int);
extern void kivoTrayAction(int);

// 只创建 NSStatusItem，不创建第二个应用或主事件循环。
@interface KivoTrayTarget : NSObject
@property(nonatomic, strong) NSStatusItem *item;
@property(nonatomic, strong) NSMenu *menu;
- (void)clicked:(id)sender;
- (void)selected:(NSMenuItem *)sender;
@end

@implementation KivoTrayTarget
- (void)clicked:(id)sender {
    NSEvent *event = [NSApp currentEvent];
    if (event.type == NSEventTypeRightMouseUp || (event.modifierFlags & NSEventModifierFlagControl)) {
        [self.menu popUpMenuPositioningItem:nil atLocation:NSMakePoint(0, NSHeight(self.item.button.bounds)) inView:self.item.button];
    } else {
        kivoTrayAction(1);
    }
}
- (void)selected:(NSMenuItem *)sender { kivoTrayAction((int)sender.tag); }
@end

static KivoTrayTarget *target;

void kivoTrayStart(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        target = [KivoTrayTarget new];
        target.item = [[NSStatusBar systemStatusBar] statusItemWithLength:NSSquareStatusItemLength];
        target.menu = [NSMenu new];
        target.menu.autoenablesItems = NO;
        NSStatusBarButton *button = target.item.button;
        button.target = target;
        button.action = @selector(clicked:);
        button.toolTip = @"Kivo · 正在连接后台";
        button.title = @"K";
        [button sendActionOn:NSEventMaskLeftMouseUp | NSEventMaskRightMouseUp];
        kivoTrayReady(button != nil ? 1 : 0);
    });
}

void kivoTrayBegin(char *tip, void *bytes, int length) {
    NSString *tooltip = [[NSString alloc] initWithUTF8String:tip];
    NSData *data = [NSData dataWithBytes:bytes length:(NSUInteger)length];
    dispatch_async(dispatch_get_main_queue(), ^{
        if (!target) return;
        [target.menu removeAllItems];
        NSImage *image = [[NSImage alloc] initWithData:data];
        image.size = NSMakeSize(18, 18);
        target.item.button.image = image;
        target.item.button.title = @"";
        target.item.button.toolTip = tooltip;
    });
}

void kivoTrayItem(int action, char *text, int enabled, int separator) {
    NSString *label = [[NSString alloc] initWithUTF8String:text];
    dispatch_async(dispatch_get_main_queue(), ^{
        if (!target) return;
        if (separator) { [target.menu addItem:[NSMenuItem separatorItem]]; return; }
        NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:label action:@selector(selected:) keyEquivalent:@""];
        item.target = target;
        item.tag = action;
        item.enabled = enabled != 0;
        [target.menu addItem:item];
    });
}

void kivoTrayStop(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (target.item) [[NSStatusBar systemStatusBar] removeStatusItem:target.item];
        target = nil;
    });
}
