package version

import "testing"

func TestNormalizeBuildVersion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"tagged install strips leading v", "v1.0.2", "1.0.2"},
		{"already without v", "1.0.2", "1.0.2"},
		{"local devel placeholder", "(devel)", ""},
		{"empty when no build info", "", ""},
		{"pseudo-version", "v0.0.0-20250101000000-abcdef123456", "0.0.0-20250101000000-abcdef123456"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeBuildVersion(tt.in); got != tt.want {
				t.Errorf("normalizeBuildVersion(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
