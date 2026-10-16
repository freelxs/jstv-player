import AppKit
import WebKit

let input = URL(fileURLWithPath: CommandLine.arguments[1])
let output = URL(fileURLWithPath: CommandLine.arguments[2])
let html = try String(contentsOf: input, encoding: .utf8)
let app = NSApplication.shared
app.setActivationPolicy(.regular)
let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1280, height: 820), styleMask: [], backing: .buffered, defer: false)
let webView = WKWebView(frame: NSRect(x: 0, y: 0, width: 1280, height: 820))
window.contentView = webView
app.activate(ignoringOtherApps: true)
window.makeKeyAndOrderFront(nil)
webView.loadHTMLString(html, baseURL: input.deletingLastPathComponent())
RunLoop.main.run(until: Date(timeIntervalSinceNow: 1.5))
var finished = false
webView.takeSnapshot(with: nil) { image, error in
    defer { finished = true }
    if let error { fputs("snapshot failed: \(error)\n", stderr); return }
    guard let image, let tiff = image.tiffRepresentation,
          let bitmap = NSBitmapImageRep(data: tiff),
          let png = bitmap.representation(using: .png, properties: [:]) else {
        fputs("snapshot did not contain a bitmap\n", stderr); return
    }
    do { try png.write(to: output) } catch { fputs("write failed: \(error)\n", stderr) }
}
let deadline = Date(timeIntervalSinceNow: 10)
while !finished && Date() < deadline {
    RunLoop.main.run(mode: .default, before: Date(timeIntervalSinceNow: 0.05))
}
if !finished { fputs("snapshot timed out\n", stderr) }
window.close()
