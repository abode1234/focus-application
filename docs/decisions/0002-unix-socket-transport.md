# 0002. HTTP+JSON over a Unix socket, SSE for events

**Context.** Single user, local only, no network exposure.

**Decision.** `net/http` served on `$XDG_RUNTIME_DIR/focus/focusd.sock` with mode 0600. Live updates use Server-Sent Events with a 15s heartbeat. Times on the wire are Unix seconds (`internal/wire`).

**Alternatives.** TCP on 127.0.0.1 (any local user could connect). gRPC (heavier than needed). WebSocket (two-way is not needed).

**Consequences.** File permissions are the access control. `curl --unix-socket` is enough to debug. A WebView cannot reach the socket, see 0004.
