// Package wordlist provides English word membership and commonness levels
// from classic SCOWL (license in data/NOTICE). The embedded data is generated
// by cmd/gen/wordlist; regenerate with `make gen-wordlist`.
package wordlist

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed data/words.txt.gz
var wordsGz []byte

// Unranked is the sentinel level for words known to SCOWL only as a regional
// spelling variant (e.g. British "colour") or a proper noun (e.g. a place
// name), rather than from the ranked english/american word lists. They are
// real words for membership purposes, but SCOWL gives them no commonness
// level, so they sort after every ranked level (10-70).
const Unranked = 100

var (
	levels map[string]uint8
	source string
)

func init() {
	var err error
	source, levels, err = decode(wordsGz)
	if err != nil {
		panic(fmt.Sprintf("wordlist: decode: %v", err))
	}
}

func decode(gz []byte) (string, map[string]uint8, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return "", nil, err
	}
	defer zr.Close()
	sc := bufio.NewScanner(zr)
	if !sc.Scan() {
		return "", nil, fmt.Errorf("missing header")
	}
	header, ok := strings.CutPrefix(sc.Text(), "# ")
	if !ok {
		return "", nil, fmt.Errorf("bad header %q", sc.Text())
	}
	m := make(map[string]uint8, 120_000)
	for sc.Scan() {
		word, lv, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			return "", nil, fmt.Errorf("bad line %q", sc.Text())
		}
		n, err := strconv.Atoi(lv)
		if err != nil || n < 0 || n > 255 {
			return "", nil, fmt.Errorf("bad level in %q", sc.Text())
		}
		m[word] = uint8(n)
	}
	return header, m, sc.Err()
}

// Level returns word's SCOWL size level (10 = most common … 70 = rare) and
// whether word is in the list. Membership-only words (British spellings,
// proper nouns — see Unranked) return (Unranked, true). Lookups are
// case-sensitive; pass lowercase.
func Level(word string) (int, bool) {
	l, ok := levels[word]
	return int(l), ok
}

// Len returns the number of words in the list.
func Len() int { return len(levels) }

// Source describes the dataset the embedded list was generated from.
func Source() string { return source }

// CommonMaxLevel is the highest SCOWL level counted as a very common word:
// such words are almost always registered on popular TLDs.
const CommonMaxLevel = 20

// IsCommon reports whether word is a very common English word (SCOWL level
// ≤ CommonMaxLevel). Unranked words are never common. Pass lowercase.
func IsCommon(word string) bool {
	l, ok := Level(word)
	return ok && l <= CommonMaxLevel
}

// Words returns the ranked words with minLevel ≤ level ≤ maxLevel, sorted.
// Unranked (membership-only) words are never included.
func Words(minLevel, maxLevel int) []string {
	var out []string
	for w, l := range levels {
		if int(l) == Unranked {
			continue
		}
		if int(l) >= minLevel && int(l) <= maxLevel {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}
