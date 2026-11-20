package main

import (
	"testing"
	"time"
)

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

func TestFreeSlot(t *testing.T) {
	plan := func(f, to string) frappeDoc { return frappeDoc{"from_time": f, "to_time": to} }
	for _, c := range []struct {
		busy     []frappeDoc
		from, to string
	}{
		{nil, "09:00:00", "10:00:00"},
		{[]frappeDoc{plan("09:00:00", "10:00:00")}, "10:00:00", "11:00:00"},
		{[]frappeDoc{plan("9:00:00", "10:00:00"), plan("10:00:00.000000", "11:30:00")}, "11:30:00", "12:30:00"},
		{[]frappeDoc{plan("09:30:00", "10:15:00")}, "10:15:00", "11:15:00"},
	} {
		if f, to := freeSlot(c.busy); f != c.from || to != c.to {
			t.Errorf("busy %v: got %s-%s want %s-%s", c.busy, f, to, c.from, c.to)
		}
	}
}

func TestDriveDue(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	for _, c := range []struct {
		last string
		due  bool
	}{{"", true}, {"garbage", true}, {at(2 * time.Hour), false}, {at(23 * time.Hour), false}, {at(23*time.Hour + 56*time.Minute), true}, {at(30 * time.Hour), true}} {
		if got := driveDueAt(c.last, now); got != c.due {
			t.Errorf("last %q: due=%v want %v", c.last, got, c.due)
		}
	}
	for s, ok := range map[string]bool{
		"https://script.google.com/macros/s/AKfy123/exec": true,
		"http://script.google.com/macros/s/AKfy123/exec":  false,
		"https://evil.test/macros/s/AKfy123/exec":         false,
		"https://script.google.com/macros/s/AKfy123/dev":  false,
	} {
		if validDriveURL(s) != ok {
			t.Errorf("validDriveURL(%q) want %v", s, ok)
		}
	}
}
