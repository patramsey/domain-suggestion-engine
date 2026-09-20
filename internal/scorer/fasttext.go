package scorer

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// QuantizedFastText holds pre-trained quantized subword bucket vectors.
type QuantizedFastText struct {
	dims    int
	minN    int
	maxN    int
	buckets int
	scales  [gloveDims]float32
	offs    [gloveDims]float32
	vecs    [][gloveDims]int8
}

var fasttext *QuantizedFastText

func init() {
	var err error
	fasttext, err = decodeFastText(fasttextBin)
	if err != nil {
		panic(fmt.Sprintf("scorer: decode FastText: %v", err))
	}
}

func decodeFastText(data []byte) (*QuantizedFastText, error) {
	if len(data) < 12 || string(data[:4]) != "FSTX" || data[4] != 1 {
		return nil, fmt.Errorf("invalid FastText binary header")
	}
	d := data[5:]
	fileDims := int(d[0])
	if fileDims != gloveDims {
		return nil, fmt.Errorf("expected %d dims, got %d", gloveDims, fileDims)
	}
	minN := int(d[1])
	maxN := int(d[2])
	d = d[3:]

	buckets := int(binary.LittleEndian.Uint32(d[:4]))
	d = d[4:]

	floatBytes := gloveDims * 4
	if len(d) < floatBytes*2+buckets*gloveDims {
		return nil, fmt.Errorf("truncated FastText binary")
	}

	var scales, offs [gloveDims]float32
	for i := range scales {
		scales[i] = math.Float32frombits(binary.LittleEndian.Uint32(d[i*4:]))
	}
	d = d[floatBytes:]
	for i := range offs {
		offs[i] = math.Float32frombits(binary.LittleEndian.Uint32(d[i*4:]))
	}
	d = d[floatBytes:]

	vecs := make([][gloveDims]int8, buckets)
	for b := 0; b < buckets; b++ {
		for j := 0; j < gloveDims; j++ {
			vecs[b][j] = int8(d[j])
		}
		d = d[gloveDims:]
	}

	return &QuantizedFastText{
		dims:    gloveDims,
		minN:    minN,
		maxN:    maxN,
		buckets: buckets,
		scales:  scales,
		offs:    offs,
		vecs:    vecs,
	}, nil
}

// EmbedWord decomposes a word into character n-grams and computes its unit-normalized embedding.
func (m *QuantizedFastText) EmbedWord(word string) ([gloveDims]float32, bool) {
	grams := ExtractNGrams(word, m.minN, m.maxN)
	if len(grams) == 0 {
		return [gloveDims]float32{}, false
	}
	var sum [gloveDims]float32
	for _, g := range grams {
		b := HashNGram(g, m.buckets)
		q := m.vecs[b]
		for d := 0; d < gloveDims; d++ {
			sum[d] += float32(int(q[d])+127)*m.scales[d] + m.offs[d]
		}
	}
	var norm float32
	for _, v := range sum {
		norm += v * v
	}
	if norm == 0 {
		return [gloveDims]float32{}, false
	}
	inv := float32(1.0 / math.Sqrt(float64(norm)))
	for d := range sum {
		sum[d] *= inv
	}
	return sum, true
}

// EmbedTokens computes the unit-normalized mean embedding for multiple tokens.
func (m *QuantizedFastText) EmbedTokens(tokens []string) ([gloveDims]float32, bool) {
	var sum [gloveDims]float32
	n := 0
	for _, tok := range tokens {
		v, ok := m.EmbedWord(tok)
		if !ok {
			continue
		}
		for d := range sum {
			sum[d] += v[d]
		}
		n++
	}
	if n == 0 {
		return [gloveDims]float32{}, false
	}
	var norm float32
	for _, v := range sum {
		norm += v * v
	}
	if norm == 0 {
		return [gloveDims]float32{}, false
	}
	inv := float32(1.0 / math.Sqrt(float64(norm)))
	for d := range sum {
		sum[d] *= inv
	}
	return sum, true
}

// SubwordModel provides character n-gram subword embeddings (FastText architecture).
// Unlike GloVe, which only represents exact dictionary words and falls back to neutral
// 0.50 for coined brand names, SubwordModel decomposes any unknown word or coined name
// into character n-grams (e.g. "<te", "tec", "ech", "chi", "hif", "ify", "fy>").
//
// Comparison Note (FastText vs. GloVe 50d):
// 1. Vocabulary Coverage:
//    - GloVe 50d: Limited to ~20,000 exact words. Coined names (Spotify, Zapier, Lumora)
//      are missing and receive neutral 0.50 scores.
//    - FastText: Full subword coverage. Any coined word inherits the semantic orientation
//      of its component morphemes.
// 2. Misspellings & Creative Alterations:
//    - GloVe: "lyft" or "fiverr" fail dictionary lookup.
//    - FastText: "lyft" shares n-grams with "lift"; "fiverr" shares n-grams with "five".
// 3. Runtime Performance:
//    - Both run in pure Go in microseconds (vector summation + cosine similarity).
// 4. Memory Footprint:
//    - GloVe 50d: ~1.0 MB (20,000 words × 50 dims @ int8).
//    - FastText (quantized): ~8–12 MB with 50,000 n-gram buckets.
type SubwordModel struct {
	dims    int
	minN    int
	maxN    int
	buckets int
	wordVec map[string][]float32
	gramVec [][]float32
}

// NewSubwordModel creates an in-memory subword model with the given dimensions.
func NewSubwordModel(dims, buckets int) *SubwordModel {
	return &SubwordModel{
		dims:    dims,
		minN:    3,
		maxN:    6,
		buckets: buckets,
		wordVec: make(map[string][]float32),
		gramVec: make([][]float32, buckets),
	}
}

// ExtractNGrams returns all character n-grams for word bounded by '<' and '>'.
// e.g. "tech" with minN=3, maxN=4 → ["<te", "tec", "ech", "ch>", "<tec", "tech", "ech>"]
func ExtractNGrams(word string, minN, maxN int) []string {
	bounded := "<" + strings.ToLower(word) + ">"
	runes := []rune(bounded)
	var grams []string
	for n := minN; n <= maxN; n++ {
		for i := 0; i+n <= len(runes); i++ {
			gram := string(runes[i : i+n])
			// omit the full word bounded token itself from the subword slice
			if gram == bounded {
				continue
			}
			grams = append(grams, gram)
		}
	}
	return grams
}

// HashNGram computes FastText's 32-bit FNV hash bucket for an n-gram.
func HashNGram(gram string, buckets int) int {
	var h uint32 = 2166136261
	for _, b := range []byte(gram) {
		h ^= uint32(b)
		h *= 16777619
	}
	return int(h % uint32(buckets))
}

// Embed computes the composite embedding for a word by summing its full-word vector (if known)
// with all its constituent character n-gram vectors.
func (m *SubwordModel) Embed(word string) []float32 {
	vec := make([]float32, m.dims)
	count := 0

	// 1. Full word vector if present in vocabulary
	if wv, ok := m.wordVec[word]; ok {
		for i := 0; i < m.dims; i++ {
			vec[i] += wv[i]
		}
		count++
	}

	// 2. Character n-gram vectors
	grams := ExtractNGrams(word, m.minN, m.maxN)
	for _, g := range grams {
		idx := HashNGram(g, m.buckets)
		if gv := m.gramVec[idx]; len(gv) == m.dims {
			for i := 0; i < m.dims; i++ {
				vec[i] += gv[i]
			}
			count++
		}
	}

	if count == 0 {
		return nil
	}

	// Normalize
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm > 0 {
		inv := float32(1.0 / math.Sqrt(norm))
		for i := range vec {
			vec[i] *= inv
		}
	}
	return vec
}

// CosineSimilarity computes cosine similarity between two unit-normalized vectors.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return math.Max(0, math.Min(1, (dot+1)/2)) // map [-1, 1] to [0, 1]
}

// String summarizes the model.
func (m *SubwordModel) String() string {
	return fmt.Sprintf("FastText-Subword(dims=%d, vocab=%d, buckets=%d, ngrams=%d-%d)",
		m.dims, len(m.wordVec), m.buckets, m.minN, m.maxN)
}
