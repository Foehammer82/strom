"""Pure helper functions for Strom release versioning.

These functions intentionally avoid subprocess/CLI concerns so they can be
unit tested directly. `tools/__main__.py` wires them into the `strom release`
commands. See CONTRIBUTING.md#release-and-maintainer-notes for the release
policy these helpers implement.
"""

from __future__ import annotations

import re
import tomllib
from dataclasses import dataclass
from pathlib import Path

TAG_RE = re.compile(r"^v(\d+)\.(\d+)\.(\d+)(?:-rc(\d+))?$")


@dataclass(frozen=True)
class Train:
    major: int
    minor: int


def load_train(path: Path) -> Train:
    """Load the active major.minor release train from `version.toml`."""
    data = tomllib.loads(path.read_text(encoding="utf-8"))
    train = data.get("train", {})
    try:
        major = int(train["major"])
        minor = int(train["minor"])
    except (KeyError, TypeError, ValueError) as exc:
        raise ValueError(f"invalid [train] section in {path}: {exc}") from exc
    if major < 0 or minor < 0:
        raise ValueError(f"[train] major/minor must be non-negative in {path}")
    return Train(major=major, minor=minor)


def render_train_toml(
    major: int,
    minor: int,
    *,
    pr_binaries: bool = True,
    image_label: str = "release-candidate",
) -> str:
    """Render a deterministic `version.toml` document."""
    if major < 0 or minor < 0:
        raise ValueError("major/minor must be non-negative")
    pr_binaries_value = "true" if pr_binaries else "false"
    return (
        "# Strom release train control.\n"
        "# See CONTRIBUTING.md#release-and-maintainer-notes for how this file is used.\n"
        "# Managed by `strom release set-train`; edit via that command or a reviewed PR.\n"
        "\n"
        "[train]\n"
        f"major = {major}\n"
        f"minor = {minor}\n"
        "\n"
        "[rc]\n"
        f"pr_binaries = {pr_binaries_value}\n"
        f'image_label = "{image_label}"\n'
    )


def set_train(path: Path, major: int, minor: int) -> None:
    """Update the major.minor release train in place, preserving `[rc]` settings."""
    existing: dict = {}
    if path.exists():
        existing = tomllib.loads(path.read_text(encoding="utf-8"))
    rc = existing.get("rc", {})
    pr_binaries = bool(rc.get("pr_binaries", True))
    image_label = str(rc.get("image_label", "release-candidate"))
    path.write_text(
        render_train_toml(major, minor, pr_binaries=pr_binaries, image_label=image_label),
        encoding="utf-8",
    )


def parse_tag(tag: str) -> tuple[int, int, int, int | None] | None:
    """Parse a `vMAJOR.MINOR.PATCH[-rcN]` tag, or return None if it doesn't match."""
    match = TAG_RE.match(tag.strip())
    if not match:
        return None
    major, minor, patch, rc = match.groups()
    return int(major), int(minor), int(patch), (int(rc) if rc is not None else None)


def compute_next_version(major: int, minor: int, existing_tags: list[str], *, rc: bool = False) -> str:
    """Compute the next release tag for the given major.minor train.

    Stable tags increment the patch number one past the highest existing
    stable patch for this train. RC tags target that same next patch: if
    `-rcN` tags already exist for it, the next RC continues that sequence;
    otherwise numbering restarts at `rc1`.
    """
    stable_patches: list[int] = []
    rc_by_patch: dict[int, list[int]] = {}
    for tag in existing_tags:
        parsed = parse_tag(tag)
        if parsed is None:
            continue
        tag_major, tag_minor, tag_patch, tag_rc = parsed
        if (tag_major, tag_minor) != (major, minor):
            continue
        if tag_rc is None:
            stable_patches.append(tag_patch)
        else:
            rc_by_patch.setdefault(tag_patch, []).append(tag_rc)

    next_patch = max(stable_patches, default=-1) + 1

    if not rc:
        return f"v{major}.{minor}.{next_patch}"

    next_rc = max(rc_by_patch.get(next_patch, []), default=0) + 1
    return f"v{major}.{minor}.{next_patch}-rc{next_rc}"
