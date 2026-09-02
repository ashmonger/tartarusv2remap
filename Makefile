# tartarus — build, test and package
#
# `make` builds the binary. `make deb` produces a .deb using only dpkg-deb,
# so no debhelper toolchain is needed.

VERSION  ?= 0.1.0
REVISION ?= 1
PKG      := tartarus-remap
BINARY   := tartarus
ARCH     ?= $(shell dpkg --print-architecture 2>/dev/null || echo amd64)
GOARCH   ?= $(shell go env GOARCH)

PREFIX      ?= /usr
BINDIR      := $(PREFIX)/bin
PROFILEDIR  := $(PREFIX)/share/tartarus/profiles
DOCDIR      := $(PREFIX)/share/doc/$(PKG)

BUILD   := build
STAGE   := $(BUILD)/$(PKG)_$(VERSION)-$(REVISION)_$(ARCH)
DEB     := $(BUILD)/$(PKG)_$(VERSION)-$(REVISION)_$(ARCH).deb

# Build identity, so a binary can say which source it came from. The commit
# date is used rather than the build time, so the same commit builds identically.
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
# Scoped to what actually goes into the binary and the package, so +dirty means
# "built from modified sources" rather than "some unrelated file changed".
DIRTY       := $(shell git diff --quiet HEAD -- cmd go.mod go.sum profiles packaging Makefile 2>/dev/null || echo +dirty)
COMMIT_DATE ?= $(shell git log -1 --format=%cI 2>/dev/null || echo unknown)

# A static binary keeps the package dependency-free.
GOFLAGS_BUILD := -trimpath
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT)$(DIRTY) \
	-X main.commitDate=$(COMMIT_DATE)

.PHONY: all build test vet fmt check verify-keys deb clean install uninstall

all: build

build:
	CGO_ENABLED=0 go build $(GOFLAGS_BUILD) -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/tartarus

vet:
	go vet ./...

fmt:
	@test -z "$$(gofmt -l cmd)" || { echo "gofmt needed:"; gofmt -l cmd; exit 1; }

test:
	go test ./...

# The offline suite: unit tests, the editor driven through a pty, and the
# cross-check against the reference implementation.
check: build
	./tests/run.sh

# The key table in layout.go is generated. Fail if the kernel header disagrees,
# so it cannot drift silently.
verify-keys:
	./tests/verify_keys.py

install: build
	install -Dm755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)
	install -d $(DESTDIR)$(PROFILEDIR)
	install -Dm644 profiles/*.profile -t $(DESTDIR)$(PROFILEDIR)
	install -Dm644 README.md -t $(DESTDIR)$(DOCDIR)

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)
	rm -rf $(DESTDIR)$(PREFIX)/share/tartarus $(DESTDIR)$(DOCDIR)

deb: build
	rm -rf $(STAGE)
	$(MAKE) install DESTDIR=$(STAGE) PREFIX=/usr
	install -d $(STAGE)/DEBIAN
	# Installed-Size is in KiB and must reflect what is actually shipped.
	sed -e 's/@VERSION@/$(VERSION)/g' -e 's/@ARCH@/$(ARCH)/g' \
	    -e "s/@INSTALLED_SIZE@/$$(du -ks --exclude=DEBIAN $(STAGE) | cut -f1)/" \
	    packaging/deb/control > $(STAGE)/DEBIAN/control
	install -m644 packaging/deb/copyright $(STAGE)/$(DOCDIR)/copyright
	gzip -9n -c packaging/deb/changelog > $(STAGE)/$(DOCDIR)/changelog.Debian.gz
	install -m755 packaging/deb/postinst $(STAGE)/DEBIAN/postinst
	# md5sums for every shipped file, as dpkg expects.
	cd $(STAGE) && find . -type f ! -path './DEBIAN/*' -printf '%P\0' \
	    | xargs -0 md5sum > DEBIAN/md5sums
	fakeroot dpkg-deb --build $(STAGE) $(DEB)
	@echo
	@dpkg-deb --info $(DEB)
	@dpkg-deb --contents $(DEB)

clean:
	rm -rf $(BINARY) $(BUILD)
