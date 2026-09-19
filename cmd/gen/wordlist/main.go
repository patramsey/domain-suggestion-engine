// Generates internal/wordlist/data/words.txt.gz from classic SCOWL 2020.12.07.
//
// Ranked words come from final/english-words.N and final/american-words.N for
// size levels N ≤ 70, each kept lowercase, purely alphabetic, with its lowest
// (most common) level.
//
// Membership-only words come from final/{british,british_z,canadian,
// australian,variant_1}-words.N and final/{english,american,british,
// british_z,canadian,australian}-{proper-names,upper}.N for N ≤ 70: words
// SCOWL knows only as a regional spelling variant (e.g. British "colour") or
// a proper noun (e.g. a place name). Entries are lowercased, then kept only
// if purely alphabetic; a word already ranked is never demoted, since a
// ranked level always wins. Abbreviations and contractions files are never
// read.
//
// Output is gzip of a header line followed by "word<TAB>level" lines sorted
// by word, where level is either a SCOWL size level (10-70) or the
// wordlist.Unranked sentinel for membership-only words. Also copies SCOWL's
// Copyright file to internal/wordlist/data/NOTICE, as its license requires.
//
// Why classic SCOWL rather than v2: v2 merges levels 10/20 into 35 and
// abbreviates regular inflections, so "balanced" would look like a typo of
// "balance". See docs/superpowers/specs/2026-09-18-quality-metrics-design.md.
//
// Usage: go run ./cmd/gen/wordlist
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/patlivet/domain-suggestion-engine/internal/wordlist"
)

const (
	sourceURL    = "https://downloads.sourceforge.net/project/wordlist/SCOWL/2020.12.07/scowl-2020.12.07.tar.gz"
	sourceSHA256 = "5587667caa20c4891390c2d42dbb4d5c4c3f41bee77af1457ece3ba23fb859cc"
	sourceName   = "scowl-2020.12.07"
	maxLevel     = 70
	outPath      = "internal/wordlist/data/words.txt.gz"
	noticePath   = "internal/wordlist/data/NOTICE"
	httpTimeout  = 2 * time.Minute
)

// rankedFamilies are the families whose words.N files contribute a ranked
// commonness level.
var rankedFamilies = map[string]bool{"english": true, "american": true}

// unrankedWordsFamilies are the families whose words.N files contribute
// membership-only (regional-spelling) words.
var unrankedWordsFamilies = map[string]bool{
	"british": true, "british_z": true, "canadian": true, "australian": true, "variant_1": true,
}

// unrankedProperFamilies are the families whose proper-names.N and upper.N
// files contribute membership-only (proper noun) words.
var unrankedProperFamilies = map[string]bool{
	"english": true, "american": true, "british": true, "british_z": true, "canadian": true, "australian": true,
}

type category int

const (
	catNone category = iota
	catRanked
	catUnranked
)

// classifyFinal parses a final/ tar entry name, e.g.
// ".../final/british-words.50" or ".../final/english-proper-names.35", and
// reports which word category (if any) its entries belong to, and its size
// level. Abbreviations and contractions files, and anything outside
// final/<family>-<type>.<N>, classify as catNone.
func classifyFinal(name string) (cat category, level int, ok bool) {
	if path.Base(path.Dir(name)) != "final" {
		return catNone, 0, false
	}
	base := path.Base(name)
	dot := strings.LastIndex(base, ".")
	if dot < 0 {
		return catNone, 0, false
	}
	level, err := strconv.Atoi(base[dot+1:])
	if err != nil {
		return catNone, 0, false
	}
	if level > maxLevel {
		return catNone, level, false
	}
	stem := base[:dot]
	dash := strings.Index(stem, "-")
	if dash < 0 {
		return catNone, level, false
	}
	family, kind := stem[:dash], stem[dash+1:]
	switch kind {
	case "words":
		switch {
		case rankedFamilies[family]:
			return catRanked, level, true
		case unrankedWordsFamilies[family]:
			return catUnranked, level, true
		}
	case "proper-names", "upper":
		if unrankedProperFamilies[family] {
			return catUnranked, level, true
		}
	}
	return catNone, level, false
}

func main() {
	data, err := fetch(sourceURL)
	if err != nil {
		fail("fetch: %v", err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != sourceSHA256 {
		fail("checksum mismatch: got %s, want %s", got, sourceSHA256)
	}
	ranked, unranked, notice, err := extract(data)
	if err != nil {
		fail("extract: %v", err)
	}
	if len(notice) == 0 {
		fail("Copyright file not found in tarball")
	}
	total, err := writeWords(outPath, ranked, unranked)
	if err != nil {
		fail("write words: %v", err)
	}
	if err := os.WriteFile(noticePath, notice, 0o644); err != nil {
		fail("write notice: %v", err)
	}
	fmt.Printf("Wrote %s (%d words: %d ranked, %d unranked) and %s\n",
		outPath, total, len(ranked), total-len(ranked), noticePath)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func fetch(url string) ([]byte, error) {
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// lowerAlphaOnly returns w lowercased, and whether the result is purely
// a-z. Used for membership-only sources (proper names, upper, regional
// spelling variants), which are not already lowercase in SCOWL.
func lowerAlphaOnly(w string) (string, bool) {
	lw := strings.ToLower(w)
	for i := 0; i < len(lw); i++ {
		if lw[i] < 'a' || lw[i] > 'z' {
			return "", false
		}
	}
	return lw, lw != ""
}

// isLowerAlpha reports whether w is already purely lowercase a-z. Used for
// ranked sources (final/english-words.N, final/american-words.N), whose
// case is preserved rather than normalised.
func isLowerAlpha(w string) bool {
	if w == "" {
		return false
	}
	for i := 0; i < len(w); i++ {
		if w[i] < 'a' || w[i] > 'z' {
			return false
		}
	}
	return true
}

// extract returns ranked words (word → lowest level), the set of
// membership-only words, and the Copyright file contents.
func extract(tgz []byte) (map[string]int, map[string]struct{}, []byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, nil, nil, err
	}
	tr := tar.NewReader(gz)
	ranked := make(map[string]int, 120_000)
	unranked := make(map[string]struct{}, 20_000)
	var notice []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, nil, err
		}
		if hdr.Name == sourceName+"/Copyright" {
			if notice, err = io.ReadAll(tr); err != nil {
				return nil, nil, nil, err
			}
			continue
		}
		cat, level, ok := classifyFinal(hdr.Name)
		if !ok {
			continue
		}
		sc := bufio.NewScanner(tr)
		for sc.Scan() {
			raw := strings.TrimSpace(sc.Text())
			switch cat {
			case catRanked:
				if !isLowerAlpha(raw) {
					continue // skips capitalised, possessive and non-ASCII entries
				}
				if old, ok := ranked[raw]; !ok || level < old {
					ranked[raw] = level
				}
			case catUnranked:
				w, ok := lowerAlphaOnly(raw)
				if !ok {
					continue // skips possessive and non-ASCII entries
				}
				unranked[w] = struct{}{}
			}
		}
		if err := sc.Err(); err != nil {
			return nil, nil, nil, err
		}
	}
	return ranked, unranked, notice, nil
}

// writeWords writes the sorted, merged list gzipped. A ranked level always
// wins over Unranked. A zero gzip header keeps the output byte-identical
// across regenerations. Returns the total word count.
func writeWords(path string, ranked map[string]int, unranked map[string]struct{}) (int, error) {
	levels := make(map[string]int, len(ranked)+len(unranked))
	for w, l := range ranked {
		levels[w] = l
	}
	for w := range unranked {
		if _, ok := levels[w]; !ok {
			levels[w] = wordlist.Unranked
		}
	}
	words := make([]string, 0, len(levels))
	for w := range levels {
		words = append(words, w)
	}
	sort.Strings(words)

	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(zw, "# source=%s sha256=%s max_level=%d words=%d ranked=%d unranked=%d\n",
		sourceName, sourceSHA256, maxLevel, len(words), len(ranked), len(unranked)-overlapCount(ranked, unranked))
	for _, w := range words {
		fmt.Fprintf(zw, "%s\t%d\n", w, levels[w])
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return 0, err
	}
	return len(words), nil
}

// overlapCount returns how many unranked words are also ranked (and so were
// not counted in the final unranked total, since a ranked level always wins).
func overlapCount(ranked map[string]int, unranked map[string]struct{}) int {
	n := 0
	for w := range unranked {
		if _, ok := ranked[w]; ok {
			n++
		}
	}
	return n
}
