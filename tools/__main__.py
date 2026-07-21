from __future__ import annotations

import hashlib
import os
import shutil
import subprocess
import sys
import tarfile
from pathlib import Path

import typer

from tools.versioning import compute_next_version, load_train, set_train

ROOT = Path(__file__).resolve().parents[1]
DIST_DIR = ROOT / "dist"
RELEASE_DIR = DIST_DIR / "release"
SIM_COMPOSE = ROOT / "sim" / "docker-compose.yml"
SIM_SCENARIOS = ROOT / "sim" / "scenarios"
VERSION_TOML = ROOT / ".github" / "release" / "version.toml"
UV = os.environ.get("UV", "uv")

AGENT_BIN = "strom-agent"
CONTROLLER_BIN = "strom-controller"

app = typer.Typer(no_args_is_help=True, add_completion=False)

docs_app = typer.Typer(no_args_is_help=True, add_completion=False)
sim_app = typer.Typer(no_args_is_help=True, add_completion=False)
hooks_app = typer.Typer(no_args_is_help=True, add_completion=False)
build_app = typer.Typer(no_args_is_help=True, add_completion=False)
dev_app = typer.Typer(no_args_is_help=True, add_completion=False)
check_app = typer.Typer(no_args_is_help=True, add_completion=False)
image_app = typer.Typer(no_args_is_help=True, add_completion=False)
release_app = typer.Typer(no_args_is_help=True, add_completion=False)

app.add_typer(build_app, name="build", help="Build commands")
app.add_typer(dev_app, name="dev", help="Development run commands")
app.add_typer(check_app, name="check", help="Validation commands")
app.add_typer(image_app, name="image", help="Image/container build commands")
app.add_typer(release_app, name="release", help="Release packaging commands")
app.add_typer(docs_app, name="docs", help="Docs tooling commands")
app.add_typer(sim_app, name="sim", help="Simulation lifecycle commands")
app.add_typer(hooks_app, name="hooks", help="Pre-commit hook commands")


def default_version() -> str:
    try:
        result = subprocess.run(
            ["git", "describe", "--tags", "--always", "--dirty"],
            cwd=ROOT,
            check=True,
            capture_output=True,
            text=True,
        )
    except subprocess.CalledProcessError:
        return "dev"
    return result.stdout.strip() or "dev"


def run_command(args: list[str], *, cwd: Path = ROOT, env: dict[str, str] | None = None) -> None:
    merged_env = os.environ.copy()
    if env:
        merged_env.update(env)
    subprocess.run(args, cwd=cwd, check=True, env=merged_env)


def compose_args(*args: str) -> list[str]:
    return ["docker", "compose", "-f", str(SIM_COMPOSE), *args]


def sync_docs() -> None:
    run_command([UV, "sync", "--locked", "--group", "docs"])


def sync_dev() -> None:
    run_command([UV, "sync", "--locked", "--group", "docs", "--group", "dev"])


def build_agent_binaries(version: str) -> None:
    DIST_DIR.mkdir(parents=True, exist_ok=True)
    run_command(
        [
            "go",
            "build",
            "-ldflags",
            f"-X main.version={version}",
            "-o",
            str(DIST_DIR / f"{AGENT_BIN}-linux-arm64"),
            "./agent/cmd/agent",
        ],
        env={"GOOS": "linux", "GOARCH": "arm64"},
    )
    run_command(
        [
            "go",
            "build",
            "-ldflags",
            f"-X main.version={version}",
            "-o",
            str(DIST_DIR / f"{AGENT_BIN}-linux-armv6"),
            "./agent/cmd/agent",
        ],
        env={"GOOS": "linux", "GOARCH": "arm", "GOARM": "6"},
    )


def controller_web_install() -> None:
    run_command(["npm", "install"], cwd=ROOT / "controller" / "web")


def controller_web_build() -> None:
    run_command(["npm", "run", "build"], cwd=ROOT / "controller" / "web")
    assets_dir = ROOT / "controller" / "cmd" / "controller" / "assets"
    dist_web = ROOT / "controller" / "web" / "dist"
    if assets_dir.exists():
        shutil.rmtree(assets_dir)
    assets_dir.mkdir(parents=True, exist_ok=True)
    for src in dist_web.iterdir():
        dst = assets_dir / src.name
        if src.is_dir():
            shutil.copytree(src, dst, dirs_exist_ok=True)
        else:
            shutil.copy2(src, dst)


def build_controller_binary(version: str) -> None:
    DIST_DIR.mkdir(parents=True, exist_ok=True)
    controller_web_build()
    run_command(
        [
            "go",
            "build",
            "-ldflags",
            f"-X main.version={version}",
            "-o",
            str(DIST_DIR / CONTROLLER_BIN),
            "./controller/cmd/controller",
        ]
    )


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def release_agent_artifacts(version: str) -> None:
    build_agent_binaries(version)
    if RELEASE_DIR.exists():
        shutil.rmtree(RELEASE_DIR)
    RELEASE_DIR.mkdir(parents=True, exist_ok=True)

    checksums: list[str] = []
    arches = ("linux-arm64", "linux-armv6")
    for arch in arches:
        stage_dir = RELEASE_DIR / f"{AGENT_BIN}-{version}-{arch}"
        deploy_dir = stage_dir / "deploy"
        deploy_dir.mkdir(parents=True, exist_ok=True)

        shutil.copy2(DIST_DIR / f"{AGENT_BIN}-{arch}", stage_dir / AGENT_BIN)
        os.chmod(stage_dir / AGENT_BIN, 0o755)
        shutil.copy2(ROOT / "agent" / "README.md", stage_dir / "README.md")

        install_script = deploy_dir / "install.sh"
        shutil.copy2(ROOT / "deploy" / "install.sh", install_script)
        os.chmod(install_script, 0o755)
        shutil.copy2(ROOT / "deploy" / "strom-agent.service", deploy_dir / "strom-agent.service")
        shutil.copy2(ROOT / "deploy" / "99-strom-agent.rules", deploy_dir / "99-strom-agent.rules")

        archive_name = f"{AGENT_BIN}-{version}-{arch}.tar.gz"
        archive_path = RELEASE_DIR / archive_name
        with tarfile.open(archive_path, "w:gz") as tar:
            tar.add(stage_dir, arcname=stage_dir.name)

        shutil.rmtree(stage_dir)
        checksums.append(f"{sha256_file(archive_path)}  {archive_name}")

    (RELEASE_DIR / "SHA256SUMS").write_text("\n".join(checksums) + "\n", encoding="utf-8")


def build_image(version: str, continue_build: bool) -> None:
    build_agent_binaries(version)
    env = {"CONTINUE": "1"} if continue_build else None
    run_command(["./image/build.sh", version], env=env)


def list_git_tags() -> list[str]:
    result = subprocess.run(
        ["git", "tag", "--list", "v*"],
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    return [line.strip() for line in result.stdout.splitlines() if line.strip()]


def next_release_version(rc: bool) -> str:
    train = load_train(VERSION_TOML)
    tags = list_git_tags()
    version = compute_next_version(train.major, train.minor, tags, rc=rc)
    if version in tags:
        raise RuntimeError(f"computed release tag {version} already exists")
    return version


def sim_up(replicas: int, version: str, include_ha: bool) -> None:
    args = compose_args()
    if include_ha:
        args.extend(["--profile", "ha"])
    args.extend(["up", "-d", "--build", "--scale", f"strom-agent={replicas}"])
    discovery_seeds = ",".join(f"sim-strom-agent-{index}:80" for index in range(1, replicas + 1))
    run_command(args, env={"VERSION": version, "DISCOVERY_SEEDS": discovery_seeds})


def sim_down(remove_volumes: bool) -> None:
    args = compose_args("down", "--remove-orphans")
    if remove_volumes:
        args.append("-v")
    run_command(args)


def sim_ps() -> None:
    run_command(compose_args("ps"))


def sim_logs(service: str | None, tail: int, follow: bool, since: str | None) -> None:
    args = compose_args("logs", "--no-color", "--tail", str(tail))
    if follow:
        args.append("--follow")
    if since:
        args.extend(["--since", since])
    if service:
        args.append(service)
    run_command(args)


def run_scenario(name: str, replicas: int, strict: bool, timeout_seconds: int | None) -> None:
    script = SIM_SCENARIOS / f"{name}.sh"
    if not script.exists():
        raise FileNotFoundError(f"scenario not found: {script}")
    env = {"AGENT_REPLICAS": str(replicas)}
    if strict:
        env["REQUIRE_FULL_ADOPTION"] = "1"
    if timeout_seconds is None:
        run_command(["bash", str(script)], env=env)
        return
    run_command(["timeout", str(timeout_seconds), "bash", str(script)], env=env)


def smoke(
    replicas: int,
    strict: bool,
    keep_running: bool,
    timeout_seconds: int | None,
    version: str,
    include_ha: bool,
) -> None:
    sim_up(replicas=replicas, version=version, include_ha=include_ha)
    try:
        run_scenario("ci-smoke", replicas=replicas, strict=strict, timeout_seconds=timeout_seconds)
    finally:
        if not keep_running:
            sim_down(remove_volumes=True)


@build_app.command("agent")
def build_agent(
    version: str = typer.Option(default_factory=default_version, help="Version string to inject"),
) -> None:
    """Build cross-compiled agent binaries."""
    build_agent_binaries(version)


@build_app.command("controller")
def build_controller(
    version: str = typer.Option(default_factory=default_version, help="Version string to inject"),
) -> None:
    """Build controller binary and bundled web assets."""
    build_controller_binary(version)


@build_app.command("controller-web")
def build_controller_web() -> None:
    """Build controller web assets into embedded assets directory."""
    controller_web_build()


@build_app.command("controller-web-install")
def build_controller_web_install() -> None:
    """Install controller web dependencies."""
    controller_web_install()


@dev_app.command("controller")
def dev_controller() -> None:
    """Run controller in local development mode."""
    run_command(["go", "run", "./controller/cmd/controller", "--data-dir", "./controller/dist/data", "--listen", ":9000"])


@dev_app.command("node-ui")
def dev_node_ui(
    listen: str = typer.Option("127.0.0.1:8080", help="Listen address for local UI"),
    flags: str = typer.Option("", help="Additional flags passed to the agent"),
) -> None:
    """Run agent in local dev-ui mode."""
    args = ["go", "run", "./agent/cmd/agent", "--dev-ui", "--listen", listen]
    if flags.strip():
        args.extend(flags.strip().split())
    run_command(args)


@dev_app.command("node-ui-open")
def dev_node_ui_open(
    listen: str = typer.Option("127.0.0.1:8080", help="Listen address for local UI"),
    flags: str = typer.Option("", help="Additional flags passed to the agent"),
) -> None:
    """Run agent dev-ui mode with auth disabled."""
    args = ["go", "run", "./agent/cmd/agent", "--dev-ui", "--listen", listen, "--http-auth=false"]
    if flags.strip():
        args.extend(flags.strip().split())
    run_command(args)


@check_app.command("test")
def check_test() -> None:
    """Run repository Go tests."""
    run_command(["go", "test", "./..."], cwd=ROOT / "agent")
    run_command(["go", "test", "./..."], cwd=ROOT / "controller")


@check_app.command("lint")
def check_lint() -> None:
    """Run golangci-lint for agent and controller."""
    run_command(["golangci-lint", "run", "./..."], cwd=ROOT / "agent")
    run_command(["golangci-lint", "run", "./..."], cwd=ROOT / "controller")


@check_app.command("tools")
def check_tools() -> None:
    """Run pytest for repo tooling (tools/)."""
    sync_dev()
    run_command([UV, "run", "pytest", "tools/tests"])


@image_app.command("node")
def image_node(
    version: str = typer.Option(default_factory=default_version, help="Image version tag"),
    continue_build: bool = typer.Option(False, "--continue", help="Reuse preserved pi-gen state"),
) -> None:
    """Build Strom node image artifacts."""
    build_image(version, continue_build)


@image_app.command("controller")
def image_controller(
    version: str = typer.Option(default_factory=default_version, help="Version tag injected into image"),
    image: str | None = typer.Option(None, help="Image name override"),
) -> None:
    """Build single-arch controller image."""
    image_name = image or f"strom-controller:{version}"
    run_command(
        [
            "docker",
            "build",
            "--build-arg",
            f"VERSION={version}",
            "-f",
            "controller/Dockerfile",
            "-t",
            image_name,
            ".",
        ]
    )


@image_app.command("controller-multiarch")
def image_controller_multiarch(
    version: str = typer.Option(default_factory=default_version, help="Version tag injected into image"),
    image: str | None = typer.Option(None, help="Image name override"),
) -> None:
    """Build multi-arch controller image with buildx."""
    image_name = image or f"ghcr.io/foehammer82/strom-controller:{version}"
    run_command(
        [
            "docker",
            "buildx",
            "build",
            "--platform",
            "linux/amd64,linux/arm64",
            "--build-arg",
            f"VERSION={version}",
            "-f",
            "controller/Dockerfile",
            "-t",
            image_name,
            ".",
        ]
    )


@image_app.command("agent")
def image_agent(
    version: str = typer.Option(default_factory=default_version, help="Version tag injected into image"),
    image: str | None = typer.Option(None, help="Image name override"),
) -> None:
    """Build single-arch agent image."""
    image_name = image or f"strom-agent:{version}"
    run_command(
        [
            "docker",
            "build",
            "--build-arg",
            f"VERSION={version}",
            "-f",
            "agent/Dockerfile",
            "-t",
            image_name,
            ".",
        ]
    )


@image_app.command("agent-multiarch")
def image_agent_multiarch(
    version: str = typer.Option(default_factory=default_version, help="Version tag injected into image"),
    image: str | None = typer.Option(None, help="Image name override"),
) -> None:
    """Build multi-arch agent image with buildx."""
    image_name = image or f"ghcr.io/foehammer82/strom-agent:{version}"
    run_command(
        [
            "docker",
            "buildx",
            "build",
            "--platform",
            "linux/amd64,linux/arm64",
            "--build-arg",
            f"VERSION={version}",
            "-f",
            "agent/Dockerfile",
            "-t",
            image_name,
            ".",
        ]
    )


@release_app.command("agent")
def release_agent(
    version: str = typer.Option(default_factory=default_version, help="Release version tag"),
) -> None:
    """Build release tarballs and checksums for agent binaries."""
    release_agent_artifacts(version)


@release_app.command("next-version")
def release_next_version_command(
    rc: bool = typer.Option(False, help="Compute the next release-candidate tag instead of a stable tag"),
    github_output: bool = typer.Option(
        False, "--github-output", help="Also append version/prerelease to $GITHUB_OUTPUT"
    ),
) -> None:
    """Compute the next release tag for the configured major.minor train."""
    version = next_release_version(rc)
    typer.echo(version)
    if github_output:
        output_path = os.environ.get("GITHUB_OUTPUT")
        if not output_path:
            raise RuntimeError("--github-output requires GITHUB_OUTPUT to be set")
        prerelease = "-" in version
        with open(output_path, "a", encoding="utf-8") as handle:
            handle.write(f"version={version}\n")
            handle.write(f"prerelease={'true' if prerelease else 'false'}\n")


@release_app.command("set-train")
def release_set_train_command(
    major: int = typer.Option(..., help="Major version for the active release train"),
    minor: int = typer.Option(..., help="Minor version for the active release train"),
) -> None:
    """Update the major.minor release train in .github/release/version.toml."""
    set_train(VERSION_TOML, major, minor)
    typer.echo(f"release train set to {major}.{minor}")


@docs_app.callback(invoke_without_command=True)
def docs_default(ctx: typer.Context) -> None:
    """Serve docs by default when no subcommand is provided."""
    if ctx.invoked_subcommand is None:
        docs_serve(dev_addr=None)


@docs_app.command("setup")
def docs_setup() -> None:
    """Sync docs dependency group."""
    sync_docs()


@docs_app.command("build")
def docs_build(
    strict: bool = typer.Option(False, help="Build with mkdocs --strict"),
) -> None:
    """Build documentation site."""
    sync_docs()
    args = [UV, "run", "mkdocs", "build"]
    if strict:
        args.append("--strict")
    run_command(args)


@docs_app.command("serve")
def docs_serve(
    dev_addr: str | None = typer.Option(None, help="Bind address for mkdocs serve"),
) -> None:
    """Serve documentation locally."""
    sync_docs()
    args = [UV, "run", "mkdocs", "serve"]
    if dev_addr:
        args.extend(["--dev-addr", dev_addr])
    run_command(args)


@sim_app.command("up")
def sim_up_command(
    replicas: int = typer.Option(2, help="Agent replica count"),
    version: str = typer.Option(default_factory=default_version, help="Build VERSION for compose services"),
    ha: bool = typer.Option(False, help="Enable Home Assistant profile"),
) -> None:
    """Bring up simulation rig."""
    sim_up(replicas=replicas, version=version, include_ha=ha)


@sim_app.command("down")
def sim_down_command(
    keep_volumes: bool = typer.Option(False, help="Keep compose volumes"),
) -> None:
    """Stop simulation rig."""
    sim_down(remove_volumes=not keep_volumes)


@sim_app.command("ps")
def sim_ps_command() -> None:
    """Show simulation container status."""
    sim_ps()


@sim_app.command("logs")
def sim_logs_command(
    service: str | None = typer.Option(None, help="Specific service to show"),
    tail: int = typer.Option(200, help="Tail line count"),
    follow: bool = typer.Option(False, help="Follow output"),
    since: str | None = typer.Option(None, help="Show logs since duration/time"),
) -> None:
    """Show simulation logs."""
    sim_logs(service=service, tail=tail, follow=follow, since=since)


@sim_app.command("scenario")
def sim_scenario_command(
    name: str,
    replicas: int = typer.Option(2, help="Agent replica count"),
    strict: bool = typer.Option(False, help="Require full adoption convergence"),
    timeout_seconds: int | None = typer.Option(None, help="Scenario timeout seconds"),
) -> None:
    """Run a named simulation scenario."""
    run_scenario(name=name, replicas=replicas, strict=strict, timeout_seconds=timeout_seconds)


@sim_app.command("smoke")
def sim_smoke_command(
    replicas: int = typer.Option(2, help="Agent replica count"),
    strict: bool = typer.Option(False, help="Require full adoption convergence"),
    timeout_seconds: int | None = typer.Option(None, help="Scenario timeout seconds"),
    keep_running: bool = typer.Option(False, help="Keep simulation stack running"),
    ha: bool = typer.Option(False, help="Enable Home Assistant profile"),
    version: str = typer.Option(default_factory=default_version, help="Build VERSION for compose services"),
) -> None:
    """Run ci-smoke scenario against simulation rig."""
    smoke(
        replicas=replicas,
        strict=strict,
        keep_running=keep_running,
        timeout_seconds=timeout_seconds,
        version=version,
        include_ha=ha,
    )


@hooks_app.command("install")
def hooks_install() -> None:
    """Install pre-commit hooks."""
    sync_dev()
    run_command([UV, "run", "pre-commit", "install"])


@hooks_app.command("run")
def hooks_run() -> None:
    """Run pre-commit checks over the full tree."""
    sync_dev()
    run_command([UV, "run", "pre-commit", "run", "--all-files"])


def run_app(argv: list[str], prog_name: str) -> int:
    try:
        app(args=argv, prog_name=prog_name, standalone_mode=False)
    except typer.Exit as exc:
        return int(exc.exit_code or 0)
    return 0


def strom_entrypoint() -> int:
    return run_app(list(sys.argv[1:]), "strom")


def main(argv: list[str] | None = None) -> int:
    args = list(sys.argv[1:] if argv is None else argv)
    prog_name = "python -m tools"
    if args and args[0] == "strom":
        args = args[1:]
        prog_name = "strom"
    return run_app(args, prog_name)


if __name__ == "__main__":
    raise SystemExit(main())
