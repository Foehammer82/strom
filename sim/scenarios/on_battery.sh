#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/common.sh"

while IFS= read -r fixture; do
    set_dev_value "$fixture" "ups.status" "OB DISCHRG"
    set_dev_value "$fixture" "battery.charge" "62"
    set_dev_value "$fixture" "battery.runtime" "1320"
    set_dev_value "$fixture" "input.voltage" "0.0"
    set_dev_value "$fixture" "output.voltage" "119.5"
    set_dev_value "$fixture" "ups.load" "55"
done < <(fixture_files)

echo "on_battery scenario applied"
