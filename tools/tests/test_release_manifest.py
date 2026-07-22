from __future__ import annotations

import json
from pathlib import Path

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey, Ed25519PublicKey

import tools.__main__ as tools_main
from tools.__main__ import (
    RELEASE_KEY_ID,
    RELEASE_MANIFEST_SCHEMA_VERSION,
    build_release_manifest,
    release_manifest_artifact,
    sign_release_manifest,
    write_release_manifest,
)


def test_release_manifest_artifact_omits_goarm_when_absent() -> None:
    artifact = release_manifest_artifact(
        os_name="linux",
        arch="arm64",
        goarm="",
        filename="strom-agent-v1.2.3-linux-arm64.tar.gz",
        size=123,
        sha256="a" * 64,
        binary_sha256="b" * 64,
    )

    assert "goarm" not in artifact
    assert artifact == {
        "os": "linux",
        "arch": "arm64",
        "filename": "strom-agent-v1.2.3-linux-arm64.tar.gz",
        "size": 123,
        "sha256": "a" * 64,
        "binary_sha256": "b" * 64,
    }


def test_release_manifest_artifact_includes_goarm_when_present() -> None:
    artifact = release_manifest_artifact(
        os_name="linux",
        arch="arm",
        goarm="6",
        filename="strom-agent-v1.2.3-linux-armv6.tar.gz",
        size=456,
        sha256="c" * 64,
        binary_sha256="d" * 64,
    )

    assert artifact["goarm"] == "6"


def test_build_release_manifest_shape() -> None:
    artifacts = [
        release_manifest_artifact(
            os_name="linux",
            arch="arm64",
            goarm="",
            filename="strom-agent-v1.2.3-linux-arm64.tar.gz",
            size=123,
            sha256="a" * 64,
            binary_sha256="b" * 64,
        )
    ]

    manifest = build_release_manifest("v1.2.3", artifacts)

    assert manifest["schema_version"] == RELEASE_MANIFEST_SCHEMA_VERSION == 1
    assert manifest["version"] == "v1.2.3"
    assert manifest["key_id"] == RELEASE_KEY_ID
    assert manifest["artifacts"] == artifacts

    # Must round-trip through JSON deterministically (sorted keys) so
    # `strom release agent` produces reproducible diffs across runs.
    encoded = json.dumps(manifest, indent=2, sort_keys=True)
    assert json.loads(encoded) == manifest


def test_sign_release_manifest_produces_a_verifiable_ed25519_signature(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(tools_main, "RELEASE_DIR", tmp_path)
    artifacts = [
        release_manifest_artifact(
            os_name="linux",
            arch="arm64",
            goarm="",
            filename="strom-agent-v1.2.3-linux-arm64.tar.gz",
            size=123,
            sha256="a" * 64,
            binary_sha256="b" * 64,
        )
    ]
    manifest_path = write_release_manifest("v1.2.3", artifacts)

    private_key = Ed25519PrivateKey.generate()
    private_pem = private_key.private_bytes(
        encoding=tools_main.serialization.Encoding.PEM,
        format=tools_main.serialization.PrivateFormat.PKCS8,
        encryption_algorithm=tools_main.serialization.NoEncryption(),
    ).decode("ascii")

    signature_path = sign_release_manifest(private_pem)

    assert signature_path == tmp_path / "strom-agent-manifest.json.sig"
    signature = signature_path.read_bytes()
    assert len(signature) == 64  # raw Ed25519 signature, matching Go's ed25519.Sign

    public_key: Ed25519PublicKey = private_key.public_key()
    public_key.verify(signature, manifest_path.read_bytes())


def test_sign_release_manifest_requires_existing_manifest(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(tools_main, "RELEASE_DIR", tmp_path)
    private_key = Ed25519PrivateKey.generate()
    private_pem = private_key.private_bytes(
        encoding=tools_main.serialization.Encoding.PEM,
        format=tools_main.serialization.PrivateFormat.PKCS8,
        encryption_algorithm=tools_main.serialization.NoEncryption(),
    ).decode("ascii")

    with pytest.raises(RuntimeError, match="does not exist"):
        sign_release_manifest(private_pem)
