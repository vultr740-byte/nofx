package telegram

import "testing"

func TestValidateAPIKeyForProvider(t *testing.T) {
	cw := &ConfigWizard{}

	cases := []struct {
		name     string
		provider string
		key      string
		want     bool
	}{
		{
			name:     "deepseek valid",
			provider: "deepseek",
			key:      "sk-abcdef12",
			want:     true,
		},
		{
			name:     "deepseek valid uppercase prefix",
			provider: "deepseek",
			key:      "SK-abcdef12",
			want:     true,
		},
		{
			name:     "deepseek too short",
			provider: "deepseek",
			key:      "sk-abc",
			want:     false,
		},
		{
			name:     "deepseek missing prefix",
			provider: "deepseek",
			key:      "abcde12345",
			want:     false,
		},
		{
			name:     "deepseek has space",
			provider: "deepseek",
			key:      "sk-abc def",
			want:     false,
		},
		{
			name:     "deepseek has newline",
			provider: "deepseek",
			key:      "sk-abc\ndef",
			want:     false,
		},
		{
			name:     "deepseek has quote",
			provider: "deepseek",
			key:      "sk-abc\"def",
			want:     false,
		},
		{
			name:     "deepseek non ascii",
			provider: "deepseek",
			key:      "sk-你好123",
			want:     false,
		},
		{
			name:     "qwen valid",
			provider: "qwen",
			key:      "sk-abcdefghijkl",
			want:     true,
		},
		{
			name:     "qwen too short",
			provider: "qwen",
			key:      "sk-abcdefghijk",
			want:     false,
		},
		{
			name:     "qwen missing prefix",
			provider: "qwen",
			key:      "xx-abcdefghijkl",
			want:     false,
		},
		{
			name:     "qwen leading space",
			provider: "qwen",
			key:      " sk-abcdefghijkl",
			want:     false,
		},
	}

	for _, tc := range cases {
		if got := cw.validateAPIKeyForProvider(tc.provider, tc.key); got != tc.want {
			t.Fatalf("%s: validateAPIKeyForProvider(%q, %q) = %v, want %v", tc.name, tc.provider, tc.key, got, tc.want)
		}
	}
}
