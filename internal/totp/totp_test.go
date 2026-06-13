package totp

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateRFC6238SHA1Vectors(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := []struct {
		at   int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, tc := range cases {
		got, err := Generate(secret, time.Unix(tc.at, 0), 30, 8)
		if err != nil {
			t.Fatalf("Generate(%d): %v", tc.at, err)
		}
		if got.Value != tc.want {
			t.Fatalf("Generate(%d) = %s, want %s", tc.at, got.Value, tc.want)
		}
	}
}

func TestSecretFromPassContent(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
		wantErr string
	}{
		{name: "single line", content: "abcd efgh\n", want: "abcdefgh"},
		{name: "blank trailing lines", content: "abcd\n\n \t\n", want: "abcd"},
		{name: "empty", content: "\n", wantErr: "empty"},
		{name: "extra line", content: "abcd\nissuer=example\n", wantErr: "exactly one"},
		{name: "uri", content: "otpauth://totp/example\n", wantErr: "otpauth"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SecretFromPassContent([]byte(tc.content))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("SecretFromPassContent: %v", err)
			}
			if got != tc.want {
				t.Fatalf("secret = %q, want %q", got, tc.want)
			}
		})
	}
}
