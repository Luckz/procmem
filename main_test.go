package main

import (
	"testing"
)

func TestParseName(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		forceExact bool
		want       matcher
	}{
		{"bare input is substring", "notepad", false, matcher{pattern: "notepad"}},
		{"input is lowercased", "NoTePaD", false, matcher{pattern: "notepad"}},
		{".exe suffix is exact", "notepad.exe", false, matcher{exact: "notepad.exe"}},
		{".exe suffix is lowercased", "NOTEPAD.EXE", false, matcher{exact: "notepad.exe"}},
		{"explicit star forces substring", "*notepad.exe", false, matcher{pattern: "notepad"}},
		{"star is stripped", "*team*", false, matcher{pattern: "team"}},
		{"star and .exe are stripped", "*TEAM*.exe", false, matcher{pattern: "team"}},
		{"forced exact keeps .exe", "steam.exe", true, matcher{exact: "steam.exe"}},
		{"forced exact without .exe gets alt name", "system", true, matcher{exact: "system.exe", exactAlt: "system"}},
	}
	for _, c := range cases {
		if got := parseName(c.raw, c.forceExact); got != c.want {
			t.Errorf("%s: parseName(%q, %v) = %+v, want %+v", c.name, c.raw, c.forceExact, got, c.want)
		}
	}
}

func TestMatcherMatches(t *testing.T) {
	exact := parseName("steam.exe", false)
	if !exact.matches("Steam.exe") {
		t.Error("exact matcher must be case-insensitive")
	}
	if exact.matches("steam_helper.exe") {
		t.Error("exact matcher must not match a longer name")
	}

	alt := parseName("System", true)
	if !alt.matches("System") {
		t.Error("forced exact without .exe must match the extensionless pseudo-process")
	}
	if !alt.matches("SYSTEM.EXE") {
		t.Error("forced exact without .exe must also match <name>.exe")
	}
	if alt.matches("System Idle Process") {
		t.Error("forced exact must not match by substring")
	}

	sub := parseName("*team*", false)
	if !sub.matches("ms-teams.exe") {
		t.Error("fuzzy matcher must match anywhere in the name")
	}
	if !sub.matches("steam.exe") {
		t.Error("fuzzy matcher matches substrings anywhere, including inside steam.exe")
	}
	if sub.matches("firefox.exe") {
		t.Error("fuzzy matcher must not match an unrelated name")
	}
}

func TestFormatCPU(t *testing.T) {
	const sec = 10000000
	cases := []struct {
		ticks uint64
		want  string
	}{
		{0, "00:00:00"},
		{59 * sec, "00:00:59"},
		{3661 * sec, "01:01:01"},
		{86399 * sec, "23:59:59"},
		{86400 * sec, "1:00:00:00"},
		{90061 * sec, "1:01:01:01"},
	}
	for _, c := range cases {
		if got := formatCPU(c.ticks); got != c.want {
			t.Errorf("formatCPU(%d) = %q, want %q", c.ticks, got, c.want)
		}
	}
}

func TestGroup(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0", "0"},
		{"123", "123"},
		{"1234", "1,234"},
		{"1234567", "1,234,567"},
		{"-1234567", "-1,234,567"},
		{"1234.5", "1,234.5"},
		{"123.5", "123.5"},
	}
	for _, c := range cases {
		if got := group(c.in); got != c.want {
			t.Errorf("group(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCommas(t *testing.T) {
	if got := commasI(1234567); got != "1,234,567" {
		t.Errorf("commasI(1234567) = %q", got)
	}
	if got := commasF(mb(2 << 20)); got != "2.0" {
		t.Errorf("commasF(2 MiB) = %q, want 2.0", got)
	}
	if got := commasF(mb(1536 << 20)); got != "1,536.0" {
		t.Errorf("commasF(1536 MiB) = %q, want 1,536.0", got)
	}
	if got := mb(1 << 30); got != 1024 {
		t.Errorf("mb(1 GiB) = %v, want 1024", got)
	}
}
