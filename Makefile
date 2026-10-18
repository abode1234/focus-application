BIN     ?= bin
PREFIX  ?= $(HOME)/.local
BINDIR  := $(PREFIX)/bin
UNITDIR ?= $(HOME)/.config/systemd/user
APPDIR  ?= $(PREFIX)/share/applications
ICONDIR ?= $(PREFIX)/share/icons/hicolor
DEVDIR  ?= /tmp/focus-dev

ICON_SIZES := 16 32 48 64 128 256
ICON_PNGS  := $(foreach n,$(ICON_SIZES),packaging/icons/$(n).png)
ICONS      := scalable/apps/focus.svg $(foreach n,$(ICON_SIZES),$(n)x$(n)/apps/focus.png)
# webview_go asks pkg-config for webkit2gtk-4.0; distros now ship 4.1, which
# is API-compatible for our use. The shim maps one to the other.
ifeq ($(shell pkg-config --exists webkit2gtk-4.0 && echo yes),)
export PKG_CONFIG_PATH := $(CURDIR)/packaging/pkgconfig$(if $(PKG_CONFIG_PATH),:$(PKG_CONFIG_PATH))
endif

.PHONY: build build-nogui test lint check run-daemon icons install uninstall clean

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
	@if command -v desktop-file-validate >/dev/null; then desktop-file-validate packaging/focus.desktop; fi

check: lint test

run-daemon:
	CGO_ENABLED=0 go build -o $(BIN)/focusd ./cmd/focusd
	@mkdir -p $(DEVDIR)/data $(DEVDIR)/config $(DEVDIR)/run
	@chmod 700 $(DEVDIR)/run
	@echo "dev daemon; in another shell: export FOCUS_SOCKET=$(DEVDIR)/run/focus/focusd.sock"
	XDG_DATA_HOME=$(DEVDIR)/data XDG_CONFIG_HOME=$(DEVDIR)/config XDG_RUNTIME_DIR=$(DEVDIR)/run $(BIN)/focusd

# The PNGs are committed so installing needs no SVG renderer; rerun this
# after editing focus.svg.
icons: $(ICON_PNGS)

packaging/icons/%.png: packaging/icons/focus.svg
	rsvg-convert -w $* -h $* $< -o $@

install: build
	install -Dm755 $(BIN)/focusd $(BINDIR)/focusd
	install -Dm755 $(BIN)/focus $(BINDIR)/focus
	install -d $(UNITDIR) $(APPDIR)
	sed 's|@BINDIR@|$(BINDIR)|g' packaging/focusd.service > $(UNITDIR)/focusd.service
	sed 's|@BINDIR@|$(BINDIR)|g' packaging/focus.desktop > $(APPDIR)/focus.desktop
	install -Dm644 packaging/icons/focus.svg $(ICONDIR)/scalable/apps/focus.svg
	$(foreach n,$(ICON_SIZES),install -Dm644 packaging/icons/$(n).png $(ICONDIR)/$(n)x$(n)/apps/focus.png;)
	-gtk-update-icon-cache -q -t -f $(ICONDIR) 2>/dev/null
	-update-desktop-database -q $(APPDIR) 2>/dev/null
	systemctl --user daemon-reload
	systemctl --user enable focusd
	systemctl --user restart focusd
	@echo "installed; open Focus from your app menu, or run: focus start \"something\" 25m"

uninstall:
	-systemctl --user disable --now focusd
	rm -f $(UNITDIR)/focusd.service $(APPDIR)/focus.desktop $(BINDIR)/focusd $(BINDIR)/focus
	rm -f $(addprefix $(ICONDIR)/,$(ICONS))
	-gtk-update-icon-cache -q -t -f $(ICONDIR) 2>/dev/null
	-update-desktop-database -q $(APPDIR) 2>/dev/null
	-systemctl --user daemon-reload
	@echo "removed; your history is kept in $${XDG_DATA_HOME:-$$HOME/.local/share}/focus"

clean:
	rm -rf $(BIN)
