// SPDX-License-Identifier: Apache-2.0

package digest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseChecksums(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	md := strings.Repeat("CD", 16)
	got, err := ParseChecksums("sha256:" + sha + ",md5:" + md + ",sha256:" + strings.ToUpper(sha))
	if err != nil {
		t.Fatal(err)
	}
	if FormatChecksums(got) != "md5:"+strings.ToLower(md)+",sha256:"+sha {
		t.Fatalf("got %s", FormatChecksums(got))
	}
	for _, tc := range []struct{ in, want string }{
		{"sha256", "is not <algorithm>:<hex>"},
		{"sha256:", "is not <algorithm>:<hex>"},
		{":" + sha, "is not <algorithm>:<hex>"},
		{"crc32:deadbeef", `algorithm "crc32" is not supported`},
		{"sha256:xyz", "is not hex"},
		{"sha256:abcd", "a sha256 value is 64 hex digits, not 4"},
		{"sha256:" + sha + ",sha256:" + strings.Repeat("00", 32), "two different sha256 values"},
	} {
		if _, err := ParseChecksums(tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: got %v, want %q", tc.in, err, tc.want)
		}
	}
}

func TestVerify(t *testing.T) {
	f := filepath.Join(t.TempDir(), "x.kpar")
	os.WriteFile(f, []byte("hello"), 0o644)
	want, _ := File(f)
	sums, err := ParseChecksums("sha256:" + want + ",sha1:aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d,sha3-256:3338be694f50c5f338814986cdf0686453a888b84f424d792af4b9202398f392")
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(f, sums); err != nil {
		t.Fatal(err)
	}
	if err := Verify(f, nil); err != nil {
		t.Fatal(err)
	}
	bad, _ := ParseChecksums("sha256:" + strings.Repeat("00", 32))
	if err := Verify(f, bad); err == nil || !strings.Contains(err.Error(), "checksum mismatch: the resource declares sha256 "+strings.Repeat("00", 32)+", the archive has "+want) {
		t.Fatalf("got %v", err)
	}
}
