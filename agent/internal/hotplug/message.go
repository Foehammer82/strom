package hotplug

import "bytes"

func isRelevantMessage(message []byte) bool {
	values := parseMessage(message)
	if values["SUBSYSTEM"] != "usb" {
		return false
	}

	action := values["ACTION"]
	return action == "add" || action == "remove"
}

func parseMessage(message []byte) map[string]string {
	values := map[string]string{}
	for _, field := range bytes.Split(message, []byte{0}) {
		if len(field) == 0 {
			continue
		}

		separator := bytes.IndexByte(field, '=')
		if separator <= 0 {
			continue
		}

		values[string(field[:separator])] = string(field[separator+1:])
	}

	return values
}