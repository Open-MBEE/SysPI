// SPDX-License-Identifier: Apache-2.0

package digest

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha3"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"sort"
	"strings"
)

// Checksum is one ECMA-427 Annex B.3.5 checksum item: a lower-case algorithm
// name and the lower-case hex of its value.
type Checksum struct {
	Algorithm, Value string
}

// String is the item as a checksum qualifier spells it, <algorithm>:<hex>.
func (c Checksum) String() string { return c.Algorithm + ":" + c.Value }

// algorithms are the Annex B.3.5 algorithms this tool can compute.
var algorithms = map[string]func() hash.Hash{
	"md5":      md5.New,
	"sha1":     sha1.New,
	"sha224":   sha256.New224,
	"sha256":   sha256.New,
	"sha384":   sha512.New384,
	"sha512":   sha512.New,
	"sha3-224": func() hash.Hash { return sha3.New224() },
	"sha3-256": func() hash.Hash { return sha3.New256() },
	"sha3-384": func() hash.Hash { return sha3.New384() },
	"sha3-512": func() hash.Hash { return sha3.New512() },
}

// Algorithms lists the supported checksum algorithms, sorted.
func Algorithms() []string {
	out := make([]string, 0, len(algorithms))
	for a := range algorithms {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// ParseChecksums reads a checksum qualifier value, <algorithm>:<hex> items
// separated by commas (Annex B.3.5), normalised to lower case and sorted by
// algorithm. An algorithm this tool cannot compute is an error: a declared
// checksum that cannot be verified is not one that passes.
func ParseChecksums(value string) ([]Checksum, error) {
	var out []Checksum
	seen := map[string]string{}
	for _, item := range strings.Split(value, ",") {
		algo, hexValue, ok := strings.Cut(item, ":")
		algo, hexValue = strings.ToLower(strings.TrimSpace(algo)), strings.ToLower(strings.TrimSpace(hexValue))
		if !ok || algo == "" || hexValue == "" {
			return nil, fmt.Errorf("checksum %q is not <algorithm>:<hex>", item)
		}
		newHash, known := algorithms[algo]
		if !known {
			return nil, fmt.Errorf("checksum algorithm %q is not supported (%s are)", algo, strings.Join(Algorithms(), ", "))
		}
		raw, err := hex.DecodeString(hexValue)
		if err != nil {
			return nil, fmt.Errorf("checksum %q: the %s value is not hex", item, algo)
		}
		if want := newHash().Size(); len(raw) != want {
			return nil, fmt.Errorf("checksum %q: a %s value is %d hex digits, not %d", item, algo, 2*want, len(hexValue))
		}
		if prev, dup := seen[algo]; dup && prev != hexValue {
			return nil, fmt.Errorf("checksum declares two different %s values", algo)
		} else if dup {
			continue
		}
		seen[algo] = hexValue
		out = append(out, Checksum{algo, hexValue})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Algorithm < out[j].Algorithm })
	return out, nil
}

// FormatChecksums is the qualifier value for the items, comma-separated.
func FormatChecksums(sums []Checksum) string {
	parts := make([]string, len(sums))
	for i, c := range sums {
		parts[i] = c.String()
	}
	return strings.Join(parts, ",")
}

// Sums computes the file's digest under each item's algorithm in one pass,
// keyed by algorithm.
func Sums(path string, sums []Checksum) (map[string]string, error) {
	out := map[string]string{}
	if len(sums) == 0 {
		return out, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hashes := map[string]hash.Hash{}
	var writers []io.Writer
	for _, c := range sums {
		if _, dup := hashes[c.Algorithm]; !dup {
			hashes[c.Algorithm] = algorithms[c.Algorithm]()
			writers = append(writers, hashes[c.Algorithm])
		}
	}
	if _, err := io.Copy(io.MultiWriter(writers...), f); err != nil {
		return nil, err
	}
	for algo, h := range hashes {
		out[algo] = hex.EncodeToString(h.Sum(nil))
	}
	return out, nil
}

// Verify checks the file's bytes against every declared checksum and reports
// the first that does not match.
func Verify(path string, sums []Checksum) error {
	got, err := Sums(path, sums)
	if err != nil {
		return err
	}
	return Compare(sums, got)
}

// Compare holds the declared checksums to digests computed from the archive,
// keyed by algorithm; an algorithm absent from got is not checked.
func Compare(sums []Checksum, got map[string]string) error {
	for _, c := range sums {
		if g, ok := got[c.Algorithm]; ok && g != c.Value {
			return fmt.Errorf("checksum mismatch: the resource declares %s %s, the archive has %s", c.Algorithm, c.Value, g)
		}
	}
	return nil
}
