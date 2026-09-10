package jsonparser

import (
	"bytes"
	"math/rand"
	"testing"
)

// stringEndReference is the implementation stringEnd replaced, kept verbatim as
// the oracle for the equivalence and fuzz tests and as the benchmark baseline
// (so both run in the same binary, on the same inputs).
func stringEndReference(data []byte) (int, bool) {
	escaped := false
	for i, c := range data {
		if c == '"' {
			if !escaped {
				return i + 1, false
			} else {
				j := i - 1
				for {
					if j < 0 || data[j] != '\\' {
						return i + 1, true // even number of backslashes
					}
					j--
					if j < 0 || data[j] != '\\' {
						break // odd number of backslashes
					}
					j--

				}
			}
		} else if c == '\\' {
			escaped = true
		}
	}

	return -1, escaped
}

func TestStringEndEdgeCases(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantIdx int
		wantEsc bool
	}{
		{"empty", ``, -1, false},
		{"unterminated_plain", `abc`, -1, false},
		{"unterminated_with_backslash", `a\bc`, -1, true},
		{"immediate_quote", `"`, 1, false},
		{"simple", `abc"`, 4, false},
		{"escaped_quote_then_real", `\""`, 3, true},
		{"one_backslash", `\`, -1, true},
		{"two_backslashes", `\\`, -1, true},
		{"even_backslashes_then_quote", `\\"`, 3, true},
		{"odd_backslashes_then_quote", `\\\"`, -1, true},
		{"odd_then_terminated", `\\\""`, 5, true},
		{"four_backslashes_quote", `\\\\"`, 5, true},
		{"backslash_after_quote", `a"\b`, 2, false},
		{"escape_far_from_quote", `a\bcdefgh"`, 10, true},
		{"only_quotes", `""`, 1, false},
		{"json_value", `2026-09-10T18:04:22Z","next":1`, 21, false},
		{"json_escaped_value", `{stream=\"stdout\"}","x":1`, 20, true},
		{"utf8", `héllo wörld"`, 14, false},
		{"invalid_utf8", "\xff\xfe\"", 3, false},
		{"invalid_utf8_with_escape", "\xff\\\xfe\"", 4, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotIdx, gotEsc := stringEnd([]byte(tc.in))
			if gotIdx != tc.wantIdx || gotEsc != tc.wantEsc {
				t.Errorf("stringEnd(%q) = (%d, %v), want (%d, %v)",
					tc.in, gotIdx, gotEsc, tc.wantIdx, tc.wantEsc)
			}
			refIdx, refEsc := stringEndReference([]byte(tc.in))
			if refIdx != gotIdx || refEsc != gotEsc {
				t.Errorf("stringEnd(%q) = (%d, %v), reference = (%d, %v)",
					tc.in, gotIdx, gotEsc, refIdx, refEsc)
			}
		})
	}
}

func TestStringEndRandomizedEquivalence(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	// Alphabet weighted towards the bytes that drive the branches.
	alphabet := []byte(`""\\\abc{}: ` + "\xff")
	for i := 0; i < 500000; i++ {
		data := make([]byte, r.Intn(48))
		for j := range data {
			data[j] = alphabet[r.Intn(len(alphabet))]
		}
		gotIdx, gotEsc := stringEnd(data)
		refIdx, refEsc := stringEndReference(data)
		if gotIdx != refIdx || gotEsc != refEsc {
			t.Fatalf("stringEnd(%q) = (%d, %v), reference = (%d, %v)",
				data, gotIdx, gotEsc, refIdx, refEsc)
		}
	}
}

func FuzzStringEnd(f *testing.F) {
	for _, s := range []string{
		``, `"`, `abc"`, `\"`, `\\"`, `\\\"`, `\\\\"`, `a\b"`,
		`{stream=\"stdout\"}"`, "\xff\\\"", `no quote`, `\`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		gotIdx, gotEsc := stringEnd(data)
		refIdx, refEsc := stringEndReference(data)
		if gotIdx != refIdx || gotEsc != refEsc {
			t.Fatalf("stringEnd(%q) = (%d, %v), reference = (%d, %v)",
				data, gotIdx, gotEsc, refIdx, refEsc)
		}
		// Independent property, not just agreement with the reference: the
		// returned offset must sit just past a quote that is itself unescaped,
		// i.e. preceded by an even number of consecutive backslashes.
		if gotIdx > 0 {
			if data[gotIdx-1] != '"' {
				t.Fatalf("stringEnd(%q) = %d, which is not just past a quote", data, gotIdx)
			}
			backslashes := 0
			for j := gotIdx - 2; j >= 0 && data[j] == '\\'; j-- {
				backslashes++
			}
			if backslashes%2 != 0 {
				t.Fatalf("stringEnd(%q) = %d, but that quote is escaped by %d backslashes",
					data, gotIdx, backslashes)
			}
		}
	})
}

// ---- benchmarks ----

// stringEndCorpora mirrors how ObjectEach actually calls stringEnd: it passes
// data[offset:] — the whole remainder of the document — so the input is a long
// slice whose closing quote is usually only a few bytes in. Benchmarking short
// slices instead makes bytes.IndexByte look far better than it is, because it
// hides the cost of the (non-inlined) call relative to a very short scan.
func stringEndCorpora() []struct {
	name string
	data [][]byte
} {
	line := []byte(`{"level":"info","ts":"2026-09-10T18:04:22.147378997Z","caller":"metrics.go:81","org_id":"29","traceID":"29a0f088b047eb8c","duration":"58.126671ms","status":200,"throughput_mb":2.496547}`)

	return []struct {
		name string
		data [][]byte
	}{
		// Offsets into a real line, as ObjectEach would reach them.
		{"key_quote_at_5", [][]byte{line[2:]}},
		{"value_quote_at_4", [][]byte{line[10:]}},
		{"value_quote_at_30", [][]byte{line[22:]}},
		{"value_long_512B", [][]byte{
			append(bytes.Repeat([]byte("ab"), 256), append([]byte(`"`), line...)...),
		}},
		{"value_escaped", [][]byte{
			[]byte(`{stream=\"stdout\",pod=\"x\"}","query_type":"limited","status":200}`),
		}},
	}
}

var (
	benchIdx int
	benchEsc bool
)

// End-to-end: ObjectEach is what callers such as Loki's json parser stage use,
// and it invokes stringEnd once per key and once per string value. Run this on
// both the old and new stringEnd to see the effect in context.
func BenchmarkObjectEachLogLine(b *testing.B) {
	lines := [][]byte{
		[]byte(`{"level":"info","ts":"2026-09-10T18:04:22.147378997Z","caller":"metrics.go:81","org_id":"29","traceID":"29a0f088b047eb8c","latency":"fast","query_type":"limited","range_type":"range","length":"20s","step":"1s","duration":"58.126671ms","status":200,"throughput_mb":2.496547,"total_bytes_mb":0.145116}`),
		[]byte(`{"level":"warn","ts":"2026-09-10T18:04:23.001Z","caller":"flush.go:172","msg":"failed to flush","err":"context deadline exceeded","query":"{stream=\"stdout\",pod=\"loki-canary-xmjzp\"}","retries":3}`),
		[]byte(`{"ts":"2026-09-10T18:04:24Z","level":"debug","component":"querier","tenant":"12345","shard":"3_of_16","chunks":184,"bytes":1048576,"msg":"chunk fetch complete"}`),
	}
	var keys, vals int
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		line := lines[i%len(lines)]
		_ = ObjectEach(line, func(key, value []byte, _ ValueType, _ int) error {
			keys += len(key)
			vals += len(value)
			return nil
		})
	}
	benchIdx = keys + vals
}

func BenchmarkStringEnd(b *testing.B) {
	impls := []struct {
		name string
		fn   func([]byte) (int, bool)
	}{
		{"baseline", stringEndReference},
		{"optimized", stringEnd},
	}
	for _, c := range stringEndCorpora() {
		for _, impl := range impls {
			b.Run(c.name+"/"+impl.name, func(b *testing.B) {
				var n int64
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					d := c.data[i%len(c.data)]
					benchIdx, benchEsc = impl.fn(d)
					n += int64(len(d))
				}
				b.SetBytes(n / int64(b.N))
			})
		}
	}
}
