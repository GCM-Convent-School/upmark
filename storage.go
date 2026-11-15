package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// storageCheck says whether the database folder survives a rebuild of the container.
// A database inside the container's own filesystem, or on Docker's anonymous volume (what a
// Dockerfile VOLUME line gives when the host mounts nothing), is replaced by an empty one on
// every deploy. Outside a container there is nothing to check.
type storageCheck struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

var anonVolume = regexp.MustCompile(`/volumes/[0-9a-f]{64}/_data$`)

func checkStorage(dbPath string) storageCheck {
	if !inContainer() {
		return storageCheck{true, "Not running in a container"}
	}
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return storageCheck{true, "Could not read mounts"}
	}
	dir, _ := filepath.Abs(filepath.Dir(dbPath))
	return classifyMount(string(b), dir)
}

func inContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	return false
}

// classifyMount finds the mount that holds dir (longest mount point that is a prefix of it)
// and judges it from /proc/self/mountinfo, where field 4 is the mount's source path and field 5
// its mount point.
func classifyMount(mountinfo, dir string) storageCheck {
	best, root := "", ""
	for _, line := range strings.Split(mountinfo, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		mp := f[4]
		if (dir == mp || mp == "/" || strings.HasPrefix(dir, mp+"/")) && len(mp) > len(best) {
			best, root = mp, f[3]
		}
	}
	switch {
	case best == "" || best == "/":
		return storageCheck{false, "The database at " + dir + " is inside the container. Every deploy starts it empty. Mount a persistent volume at " + dir + "."}
	case anonVolume.MatchString(root):
		return storageCheck{false, "The database at " + dir + " is on an unnamed Docker volume. Every deploy starts it empty. Mount a named persistent volume at " + dir + "."}
	}
	return storageCheck{true, "The database at " + dir + " is on a persistent volume"}
}
