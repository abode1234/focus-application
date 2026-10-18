# 0004. WebView GUI with an in-process HTMX proxy

**Context.** The popup needs a countdown and a reflection form, should be quick to style, and must open on time-up.

**Decision.** `webview_go` window (WebKitGTK). `focus gui` starts a loopback HTTP server that renders `html/template` fragments for HTMX and proxies commands and events to the daemon. A random token in the first URL becomes a cookie that every request must carry. The panel swaps only when the session state key changes, so a half-typed reflection survives unrelated events. The GUI is behind the `gui` build tag, and builds without it serve the same UI to a browser.

**Alternatives.** A native toolkit (more code for the same UI). Wails (more control, much heavier). Kept as the upgrade path.

**Consequences.** Needs CGO and WebKitGTK. `webview_go` asks for webkit2gtk-4.0, so a pkg-config shim maps it to 4.1. On Wayland the window cannot place itself, so placement is a compositor rule. The DMA-BUF renderer is disabled by default after it blanked the window on an NVIDIA GPU under Hyprland.
