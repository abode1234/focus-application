BIN     ?= bin
PREFIX  ?= $(HOME)/.local
BINDIR  := $(PREFIX)/bin
UNITDIR ?= $(HOME)/.config/systemd/user
APPDIR  ?= $(PREFIX)/share/applications
DEVDIR  ?= /tmp/focus-dev

# webview_go asks pkg-config for webkit2gtk-4.0; distros now ship 4.1, which
# is API-compatible for our use. The shim maps one to the other.
ifeq ($(shell pkg-config --exists webkit2gtk-4.0 && echo yes),)
export PKG_CONFIG_PATH := $(CURDIR)/packaging/pkgconfig$(if $(PKG_CONFIG_PATH),:$(PKG_CONFIG_PATH))
endif

.PHONY: build build-nogui test lint check run-daemon install uninstall clean

build:
	CGO_ENABLED=0 go build -o $(BIN)/focusd ./cmd/focusd
	go build -tags gui -o $(BIN)/focus ./cmd/focus

build-nogui:
	CGO_ENABLED=0 go build -o $(BIN)/focusd ./cmd/focusd
	CGO_ENABLED=0 go build -o $(BIN)/focus ./cmd/focus

test:
	go test -race ./...

lint:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	go vet ./...
	go vet -tags gui ./...
	@if command -v golangci-lint >/dev/null; then golangci-lint run ./...; else echo "golangci-lint not installed; ran go vet only"; fi

check: lint test

run-daemon:
	CGO_ENABLED=0 go build -o $(BIN)/focusd ./cmd/focusd
	@mkdir -p $(DEVDIR)/data $(DEVDIR)/config $(DEVDIR)/run
	@chmod 700 $(DEVDIR)/run
	@echo "dev daemon; in another shell: export FOCUS_SOCKET=$(DEVDIR)/run/focus/focusd.sock"
	XDG_DATA_HOME=$(DEVDIR)/data XDG_CONFIG_HOME=$(DEVDIR)/config XDG_RUNTIME_DIR=$(DEVDIR)/run $(BIN)/focusd

install: build
	install -Dm755 $(BIN)/focusd $(BINDIR)/focusd
	install -Dm755 $(BIN)/focus $(BINDIR)/focus
	install -d $(UNITDIR) $(APPDIR)
	sed 's|@BINDIR@|$(BINDIR)|g' packaging/focusd.service > $(UNITDIR)/focusd.service
	sed 's|@BINDIR@|$(BINDIR)|g' packaging/focus.desktop > $(APPDIR)/focus.desktop
	systemctl --user daemon-reload
	systemctl --user enable focusd
	systemctl --user restart focusd
	@echo "installed; try: focus start \"something\" 25m"

uninstall:
	-systemctl --user disable --now focusd
	rm -f $(UNITDIR)/focusd.service $(APPDIR)/focus.desktop $(BINDIR)/focusd $(BINDIR)/focus
	-systemctl --user daemon-reload
	@echo "removed; your history is kept in $${XDG_DATA_HOME:-$$HOME/.local/share}/focus"

clean:
	rm -rf $(BIN)
