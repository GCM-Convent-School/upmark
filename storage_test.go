package main

import "testing"

func TestClassifyMount(t *testing.T) {
	base := "100 90 0:50 / / rw,relatime master:1 - overlay overlay rw\n" +
		"101 100 0:51 / /proc rw - proc proc rw\n"
	anon := base + "120 100 8:1 /var/lib/docker/volumes/3f5c0a9e1b2d4c6f8a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f/_data /data rw,relatime - ext4 /dev/sda1 rw\n"
	named := base + "120 100 8:1 /var/lib/docker/volumes/bn5dcijugnhqesktf78bwv1m-upmark-data/_data /data rw,relatime - ext4 /dev/sda1 rw\n"
	bind := base + "120 100 8:1 /srv/upmark /data rw,relatime - ext4 /dev/sda1 rw\n"
	for _, c := range []struct {
		name, info, dir string
		ok              bool
	}{
		{"inside the container", base, "/data", false},
		{"unnamed volume (Dockerfile VOLUME only)", anon, "/data", false},
		{"named volume", named, "/data", true},
		{"bind mount", bind, "/data", true},
		{"sub-folder of a named volume", named, "/data/db", true},
		{"similar prefix is not the mount", named, "/database", false},
	} {
		if got := classifyMount(c.info, c.dir); got.OK != c.ok {
			t.Errorf("%s: ok=%v want %v (%s)", c.name, got.OK, c.ok, got.Detail)
		}
	}
}
