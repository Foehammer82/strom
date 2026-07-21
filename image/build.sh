#!/bin/sh
set -eu

VERSION=${1:-dev}
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
DIST_DIR="$REPO_ROOT/dist"
AGENT_SOURCE="$DIST_DIR/strom-agent-linux-arm64"
IMAGE_OUTPUT="$DIST_DIR/strom-node-$VERSION.img.xz"
CHECKSUM_OUTPUT="$IMAGE_OUTPUT.sha256"
PI_GEN_REF=${PI_GEN_REF:-bookworm-arm64}
TMP_PARENT=${TMPDIR:-/tmp}
QEMU_BIN=''
QEMU_HINT=''
BINFMT_HELPER_IMAGE=${BINFMT_HELPER_IMAGE:-tonistiigi/binfmt:latest}
QEMU_HELPER_IMAGE=${QEMU_HELPER_IMAGE:-multiarch/qemu-user-static:latest}
PI_GEN_CONTAINER_NAME=${CONTAINER_NAME:-pigen_work}
KEEP_PI_GEN_CONTAINER=${PRESERVE_CONTAINER:-0}

require_command() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "missing required command: $1" >&2
		exit 1
	fi
}

require_docker() {
	require_command docker

	if docker version >/dev/null 2>&1; then
		return
	fi

	docker_error=$(docker version 2>&1 || true)
	echo "docker is installed but not usable from this shell" >&2
	if [ -n "$docker_error" ]; then
		printf '%s\n' "$docker_error" >&2
	fi
	echo "enable Docker Desktop WSL integration for this distro or install a working Docker engine" >&2
	exit 1
}

resolve_qemu() {
	if command -v qemu-aarch64 >/dev/null 2>&1; then
		QEMU_BIN=$(command -v qemu-aarch64)
		return
	fi

	if command -v qemu-aarch64-static >/dev/null 2>&1; then
		QEMU_BIN=$(command -v qemu-aarch64-static)
		QEMU_HINT='static'
		return
	fi

	QEMU_HINT='docker'
}

ensure_binfmt() {
	if [ -f /proc/sys/fs/binfmt_misc/qemu-aarch64 ]; then
		return
	fi

	echo "qemu-aarch64 binfmt is not registered; attempting Docker-based registration..."
	if ! docker_output=$(docker run --privileged --rm "$BINFMT_HELPER_IMAGE" --install arm64 2>&1); then
		if [ -n "$docker_output" ]; then
			printf '%s\n' "$docker_output" >&2
		fi
		echo "failed to register qemu-aarch64 via Docker; install qemu-user-binfmt or enable binfmt_misc on the host" >&2
		exit 1
	fi

	if [ ! -f /proc/sys/fs/binfmt_misc/qemu-aarch64 ]; then
		echo "qemu-aarch64 binfmt is still unavailable after Docker registration" >&2
		exit 1
	fi
}

cleanup_stale_pigen_container() {
	if [ "${CONTINUE:-0}" = "1" ]; then
		return
	fi

	if docker container inspect "$PI_GEN_CONTAINER_NAME" >/dev/null 2>&1; then
		echo "Removing stale pi-gen work container..."
		docker rm -f -v "$PI_GEN_CONTAINER_NAME" >/dev/null
	fi
}

copy_deploy_volume_from_container() {
	container_name=$1
	destination_dir=$2

	if ! docker container inspect "$container_name" >/dev/null 2>&1; then
		return 1
	fi

	deploy_volume=$(docker inspect "$container_name" --format '{{range .Mounts}}{{if eq .Destination "/pi-gen/deploy"}}{{.Name}}{{end}}{{end}}')
	if [ -z "$deploy_volume" ]; then
		return 1
	fi

	mkdir -p "$destination_dir"
	if ! docker run --rm -v "$deploy_volume:/in:ro" -v "$destination_dir:/out" alpine sh -lc 'cp -a /in/. /out/'; then
		return 1
	fi

	find "$destination_dir" -maxdepth 1 -type f -name '*strom-node*.img.xz' | grep -q .
}

extract_qemu_from_docker() {
	container_name="strom-qemu-$$"
	echo "qemu-aarch64 not found on host; extracting a helper binary from Docker image ${QEMU_HELPER_IMAGE}..."
	if ! docker create --name "$container_name" "$QEMU_HELPER_IMAGE" >/dev/null; then
		echo "failed to create Docker helper container for qemu-aarch64" >&2
		exit 1
	fi
	if ! docker cp "$container_name:/usr/bin/qemu-aarch64-static" "$BIN_DIR/qemu-aarch64"; then
		docker rm -f "$container_name" >/dev/null 2>&1 || true
		echo "failed to extract qemu-aarch64-static from ${QEMU_HELPER_IMAGE}" >&2
		exit 1
	fi
	docker rm -f "$container_name" >/dev/null 2>&1 || true
	chmod 0755 "$BIN_DIR/qemu-aarch64"
}

require_docker
require_command git
require_command install
require_command mktemp
require_command sha256sum
resolve_qemu
ensure_binfmt
cleanup_stale_pigen_container

if [ ! -f "$AGENT_SOURCE" ]; then
	echo "expected agent binary at $AGENT_SOURCE; run uv run strom build agent first" >&2
	exit 1
fi

WORK_ROOT=$(mktemp -d "$TMP_PARENT/strom-pi-gen.XXXXXX")
cleanup() {
	if [ "${PRESERVE_PIGEN_WORK:-0}" = "1" ]; then
		echo "preserving pi-gen work directory at $WORK_ROOT" >&2
		return
	fi
	rm -rf "$WORK_ROOT"
}
trap cleanup EXIT INT TERM

PI_GEN_DIR="$WORK_ROOT/pi-gen"
WORKSPACE_DIR="$WORK_ROOT/workspace"
STAGE_DIR="$PI_GEN_DIR/stage-strom"
BIN_DIR="$WORK_ROOT/bin"

echo "Cloning pi-gen ($PI_GEN_REF) into temporary build workspace..."
git clone --depth 1 --branch "$PI_GEN_REF" https://github.com/RPi-Distro/pi-gen.git "$PI_GEN_DIR"

mkdir -p "$WORKSPACE_DIR"
mkdir -p "$BIN_DIR"
cp "$SCRIPT_DIR/config" "$WORKSPACE_DIR/config"
cp -R "$SCRIPT_DIR/stage-strom" "$STAGE_DIR"

if [ "$QEMU_HINT" = 'static' ]; then
	ln -sf "$QEMU_BIN" "$BIN_DIR/qemu-aarch64"
	ln -sf "$QEMU_BIN" "$BIN_DIR/qemu-aarch64-static"
	QEMU_PATH_PREFIX="$BIN_DIR:$PATH"
elif [ "$QEMU_HINT" = 'docker' ]; then
	extract_qemu_from_docker
	QEMU_PATH_PREFIX="$BIN_DIR:$PATH"
else
	QEMU_PATH_PREFIX=$PATH
fi

install -D -m 0755 "$AGENT_SOURCE" "$STAGE_DIR/01-agent/files/usr/local/bin/strom-agent"
install -D -m 0644 "$REPO_ROOT/deploy/strom-agent.service" "$STAGE_DIR/01-agent/files/etc/systemd/system/strom-agent.service"
install -D -m 0644 "$REPO_ROOT/deploy/99-strom-agent.rules" "$STAGE_DIR/01-agent/files/etc/udev/rules.d/99-strom-agent.rules"

touch "$PI_GEN_DIR/stage2/SKIP_IMAGES"

rm -f "$IMAGE_OUTPUT" "$CHECKSUM_OUTPUT"

echo "Running pi-gen Docker build..."

build_succeeded=1
if ! (
	cd "$PI_GEN_DIR"
	PATH="$QEMU_PATH_PREFIX" CONTAINER_NAME="$PI_GEN_CONTAINER_NAME" PRESERVE_CONTAINER=1 STROM_STAGE_DIR='stage-strom' ./build-docker.sh -c "$WORKSPACE_DIR/config"
); then
	build_succeeded=0
fi

if [ "$build_succeeded" -ne 1 ]; then
	echo "pi-gen Docker wrapper returned a non-zero status; attempting to recover artifacts from the preserved deploy volume..."
	if ! copy_deploy_volume_from_container "$PI_GEN_CONTAINER_NAME" "$PI_GEN_DIR/deploy"; then
		echo "failed to recover pi-gen deploy artifacts from container $PI_GEN_CONTAINER_NAME" >&2
		exit 1
	fi
fi

FOUND_IMAGE=$(find "$PI_GEN_DIR/deploy" -maxdepth 1 -type f -name '*strom-node*.img.xz' | head -n 1)
if [ -z "$FOUND_IMAGE" ]; then
	echo "pi-gen completed without producing an image artifact" >&2
	exit 1
fi

install -D -m 0644 "$FOUND_IMAGE" "$IMAGE_OUTPUT"
(
	cd "$DIST_DIR"
	sha256sum "$(basename "$IMAGE_OUTPUT")" > "$(basename "$CHECKSUM_OUTPUT")"
)

if [ "$KEEP_PI_GEN_CONTAINER" != "1" ]; then
	docker rm -f -v "$PI_GEN_CONTAINER_NAME" >/dev/null 2>&1 || true
fi

echo "Image written to $IMAGE_OUTPUT"
echo "Checksum written to $CHECKSUM_OUTPUT"
