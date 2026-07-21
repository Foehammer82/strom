from __future__ import annotations

from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
FIRSTBOOT_SCRIPT = ROOT / "image" / "stage-strom" / "03-firstboot" / "files" / "usr" / "local" / "libexec" / "strom-firstboot.sh"
FIRSTBOOT_STAGE = ROOT / "image" / "stage-strom" / "03-firstboot" / "00-run.sh"
IMAGE_CONFIG = ROOT / "image" / "config"
EXPORT_PRERUN = ROOT / "image" / "stage-strom" / "export-image" / "prerun.sh"
AGENT_SERVICE = ROOT / "deploy" / "strom-agent.service"


def test_firstboot_enables_overlayfs_after_mounting_persistent_state() -> None:
    script = FIRSTBOOT_SCRIPT.read_text(encoding="utf-8")

    assert 'state_dir=/var/lib/strom' in script
    assert 'mountpoint -q "$state_dir"' in script
    assert 'touch "$state_dir/.firstboot-complete"' in script
    assert "raspi-config nonint do_overlayfs 0" in script


def test_image_creates_and_mounts_persistent_state_partition() -> None:
    stage = FIRSTBOOT_STAGE.read_text(encoding="utf-8")
    config = IMAGE_CONFIG.read_text(encoding="utf-8")
    export = EXPORT_PRERUN.read_text(encoding="utf-8")

    assert "LABEL=strom-state /var/lib/strom ext4 defaults,nofail" in stage
    assert "STROM_STATE_PART_SIZE_MB" in config
    assert "EXPORT_CONFIG_DIR" in config
    assert "STATE_DEV=\"${LOOP_DEV}p3\"" in export
    assert "mkfs.ext4 -L strom-state" in export


def test_image_state_partition_ends_at_the_device_boundary() -> None:
    export = EXPORT_PRERUN.read_text(encoding="utf-8")

    assert 'mkpart primary ext4 "${STATE_PART_START}" "100%"' in export


def test_agent_requires_persistent_state_mount() -> None:
    service = AGENT_SERVICE.read_text(encoding="utf-8")

    assert "RequiresMountsFor=/var/lib/strom" in service
