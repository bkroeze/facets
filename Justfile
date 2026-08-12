set shell := ["sh", "-eu", "-c"]

app := "facets"

# List available project commands.
default:
    @just --list

# Show the Facets CLI reference.
help:
    go run ./cmd/facets help

# Run the Facets CLI with arbitrary arguments.
run *ARGS:
    go run ./cmd/facets {{ARGS}}

# Start the web dashboard; override the address with FACETS_ADDR.
serve:
    go run ./cmd/facets serve

# Format all Go packages.
fmt:
    go fmt ./...

# Fail when Go source files are not formatted.
fmt-check:
    @test -z "$(gofmt -l .)"

# Run all tests.
test:
    go test ./...

# Run Go's static analyzer.
vet:
    go vet ./...

# Build the Facets binary under bin/.
build:
    mkdir -p bin
    go build -trimpath -o bin/{{ app }} ./cmd/facets

# Install the Facets binary into GOBIN or GOPATH/bin.
install:
    go install ./cmd/facets

# Install and enable the loopback-only Tailnet service.
tailnet-install:
    @set -eu; \
      bin_dir="$HOME/.local/bin"; \
      config_home="${XDG_CONFIG_HOME:-$HOME/.config}"; \
      mkdir -p "$bin_dir" "$config_home/systemd/user"; \
      GOBIN="$bin_dir" go install ./cmd/facets; \
      install -m 0644 deployment/facets.service "$config_home/systemd/user/facets.service"; \
      systemctl --user daemon-reload; \
      systemctl --user enable --now facets.service

# Verify a Tailnet Serve endpoint from an enrolled device.
tailnet-verify host:
    @set -eu; \
      systemctl --user is-active --quiet facets.service; \
      tailscale serve status; \
      curl --fail --silent --show-error "https://{{ host }}/healthz"; \
      curl --fail --silent --show-error "https://{{ host }}/api/v1"

# Disable and remove the Tailnet service and its Serve mapping.
tailnet-uninstall:
    @set -eu; \
      systemctl --user disable --now facets.service 2>/dev/null || true; \
      tailscale serve reset; \
      config_home="${XDG_CONFIG_HOME:-$HOME/.config}"; \
      rm -f "$config_home/systemd/user/facets.service"; \
      systemctl --user daemon-reload; \
      rm -f "$HOME/.local/bin/facets"

# Install the Quickshell GUI and icon into user XDG directories.
install-gui:
    @config_home="${XDG_CONFIG_HOME:-$HOME/.config}"; \
      data_home="${XDG_DATA_HOME:-$HOME/.local/share}"; \
      install -Dm644 quickshell/facets/shell.qml "$config_home/quickshell/facets/shell.qml"; \
      install -Dm644 assets/facets.svg "$data_home/facets/facets.svg"; \
      sed 's/<svg /<svg fill="#ffffff" /' assets/facets.svg > "$data_home/facets/facets-waybar.svg"; \
      chmod 644 "$data_home/facets/facets-waybar.svg"; \
      printf 'Installed Facets GUI to %s and icons to %s\n' \
        "$config_home/quickshell/facets" "$data_home/facets"

# Run formatting, tests, static analysis, and a build.
check: fmt-check test vet build

# Remove generated local build artifacts.
clean:
    rm -rf bin

# Build, lint, and run Android JVM tests without changing the default Go check.
android-check:
    cd android && ./gradlew :app:assembleDebug :app:lintDebug test

# Build the Android debug APK only.
android-debug:
    cd android && ./gradlew :app:assembleDebug

# Run Android instrumentation tests on an attached device or emulator.
android-instrumentation:
    cd android && ./gradlew :app:connectedDebugAndroidTest
