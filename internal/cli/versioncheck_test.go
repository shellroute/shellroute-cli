package cli

import "testing"

func TestCleanVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"0.1.0", "0.1.0"},
		{"v0.1.0", "0.1.0"},
		{"v0.1.0-dirty", "0.1.0"},
		{"v0.1.0-3-gabcdef", "0.1.0"},
		{"v0.1.0-3-gabcdef-dirty", "0.1.0"},
		{"0.2.0-rc1", "0.2.0"},
		{"dev", "dev"},
		{"v1.0.0", "1.0.0"},
		{"", ""},
		{"v", ""},
	}
	for _, tt := range tests {
		got := cleanVersion(tt.input)
		if got != tt.want {
			t.Errorf("cleanVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input string
		want  [3]int
	}{
		{"0.1.0", [3]int{0, 1, 0}},
		{"1.2.3", [3]int{1, 2, 3}},
		{"v0.2.0", [3]int{0, 2, 0}},
		{"v0.1.0-dirty", [3]int{0, 1, 0}},
		{"v0.1.0-3-gabcdef", [3]int{0, 1, 0}},
		{"dev", [3]int{0, 0, 0}},
		{"0.1", [3]int{0, 1, 0}},
		{"1", [3]int{1, 0, 0}},
	}
	for _, tt := range tests {
		got := parseSemver(tt.input)
		if got != tt.want {
			t.Errorf("parseSemver(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.2.0", -1},
		{"0.2.0", "0.1.0", 1},
		{"1.0.0", "0.9.9", 1},
		{"0.1.0", "0.1.1", -1},
		{"1.2.3", "1.2.3", 0},
		{"0.10.0", "0.9.0", 1},
		{"v0.2.0", "0.2.0", 0},
		{"v0.1.0-dirty", "0.1.0", 0},
		{"v0.1.0-3-gabcdef", "0.1.0", 0},
	}
	for _, tt := range tests {
		got := compareVersions(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestVersionFunctionReturnsClean(t *testing.T) {
	old := rawVersion
	defer func() { rawVersion = old }()

	rawVersion = "v0.3.0-5-g1234567-dirty"
	if got := Version(); got != "0.3.0" {
		t.Errorf("Version() = %q, want %q", got, "0.3.0")
	}

	rawVersion = "dev"
	if got := Version(); got != "dev" {
		t.Errorf("Version() = %q, want %q", got, "dev")
	}
}

func TestCheckVersionDevSkips(t *testing.T) {
	old := rawVersion
	rawVersion = "dev"
	defer func() { rawVersion = old }()

	if !checkVersion(nil) {
		t.Error("dev version should skip check")
	}
}
