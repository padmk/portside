package supervisor

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// processStart identifies a process by its start time, as ps prints it in
// the C locale so that the text does not depend on the daemon's locale. It
// returns "" when the process does not exist.
func processStart(pid int) string { return psStart(pid, cLocaleEnv()) }

func psStart(pid int, env []string) string {
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.Join(strings.Fields(string(out)), " ")
}

func cLocaleEnv() []string {
	return append(os.Environ(), "LC_ALL=C", "LC_TIME=C", "LANG=C")
}

const startLayout = "Mon Jan 2 15:04:05 2006"

// startTime is the start time of pid, to the second.
func startTime(pid int) (time.Time, bool) {
	t, err := time.ParseInLocation(startLayout, processStart(pid), time.Local)
	return t, err == nil
}

// sameStart reports whether the live process pid has the start time saved
// for it earlier. Older daemons saved it in their own locale; such a value
// is compared with ps in the current locale, so a process whose saved time
// cannot be confirmed never counts as the same.
func sameStart(pid int, saved string) bool {
	now := processStart(pid)
	if now == "" || saved == "" {
		return false
	}
	saved = strings.Join(strings.Fields(saved), " ")
	if now == saved {
		return true
	}
	if _, err := time.Parse(startLayout, saved); err == nil {
		return false
	}
	return psStart(pid, os.Environ()) == saved
}
