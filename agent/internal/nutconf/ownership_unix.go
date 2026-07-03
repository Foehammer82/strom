//go:build !windows

package nutconf

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
)

func applyNutOwnership(path string) error {
	if os.Geteuid() != 0 {
		return nil
	}

	rootUser, err := user.Lookup("root")
	if err != nil {
		return nil
	}
	nutGroup, err := user.LookupGroup("nut")
	if err != nil {
		return nil
	}

	uid, err := strconv.Atoi(rootUser.Uid)
	if err != nil {
		return fmt.Errorf("parse root uid: %w", err)
	}
	gid, err := strconv.Atoi(nutGroup.Gid)
	if err != nil {
		return fmt.Errorf("parse nut gid: %w", err)
	}

	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}

	return nil
}
