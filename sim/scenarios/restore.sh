#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"

while IFS= read -r fixture; do
    set_dev_value "$fixture" "ups.status" "OL"
    set_dev_value "$fixture" "battery.charge" "98"
    set_dev_value "$fixture" "battery.runtime" "3200"
    set_dev_value "$fixture" "input.voltage" "120.2"
    set_dev_value "$fixture" "output.voltage" "120.0"
    set_dev_value "$fixture" "ups.load" "34"
done < <(fixture_files)

echo "restore scenario applied"
