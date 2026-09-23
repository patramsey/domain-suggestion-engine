package scorer

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/patlivet/domain-suggestion-engine/internal/wordlist"
)

const gloveDims = 50

// gloveModel holds the decoded vocabulary and quantised vectors.
type gloveModel struct {
	index  map[string]int // word → row index
	vecs   [][gloveDims]int8
	scales [gloveDims]float32
	offs   [gloveDims]float32
}

var glove *gloveModel

func init() {
	var err error
	glove, err = decodeGlove(gloveBin)
	if err != nil {
		panic(fmt.Sprintf("scorer: decode GloVe: %v", err))
	}
}

func decodeGlove(data []byte) (*gloveModel, error) {
	if len(data) < 10 || string(data[:4]) != "GLVE" || data[4] != 1 {
		return nil, fmt.Errorf("invalid GloVe binary header")
	}
	d := data[4:]
	fileDims := int(d[1])
	if fileDims != gloveDims {
		return nil, fmt.Errorf("expected %d dims, got %d", gloveDims, fileDims)
	}
	d = d[2:]

	vocabSize := int(binary.LittleEndian.Uint32(d[:4]))
	d = d[4:]

	// scales and offsets: dims × float32 each
	floatBytes := gloveDims * 4
	if len(d) < floatBytes*2 {
		return nil, fmt.Errorf("truncated scales/offsets")
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

	index := make(map[string]int, vocabSize)
	vecs := make([][gloveDims]int8, 0, vocabSize)

	for i := 0; i < vocabSize; i++ {
		if len(d) < 1 {
			return nil, fmt.Errorf("truncated at word %d", i)
		}
		wLen := int(d[0])
		d = d[1:]
		if len(d) < wLen+gloveDims {
			return nil, fmt.Errorf("truncated word entry %d", i)
		}
		word := string(d[:wLen])
		d = d[wLen:]
		var vec [gloveDims]int8
		for j := range vec {
			vec[j] = int8(d[j])
		}
		d = d[gloveDims:]
		index[word] = i
		vecs = append(vecs, vec)
	}
	return &gloveModel{index, vecs, scales, offs}, nil
}

// vecFor returns the dequantised float32 vector for a word, or false if not found.
func (m *gloveModel) vecFor(word string) ([gloveDims]float32, bool) {
	idx, ok := m.index[word]
	if !ok {
		return [gloveDims]float32{}, false
	}
	q := m.vecs[idx]
	var v [gloveDims]float32
	for d := range v {
		v[d] = float32(int(q[d])+127)*m.scales[d] + m.offs[d]
	}
	return v, true
}

// maxSubWord is the longest sub-word considered; no English word in the
// vocabulary that matters for brand names runs longer.
const maxSubWord = 14

// isSubWord reports whether cand is a usable sub-word. Words of 3+ letters
// come from the GloVe vocabulary; 2-letter words must also be in SCOWL or
// GloVe, and callers restrict where those may be used (see subWords).
func isSubWord(cand string) bool {
	if len(cand) < 2 {
		return false
	}
	if _, ok := glove.index[cand]; ok {
		return true
	}
	if len(cand) == 2 {
		_, ok := wordlist.Level(cand)
		return ok
	}
	return false
}

// subWords splits an SLD into known vocabulary sub-words.
//
// It first looks for a segmentation covering every letter, which is what a
// real compound looks like ("aidrive" → "ai" + "drive", "duskbrew" → "dusk" +
// "brew"), preferring the one with the fewest parts. Failing that it falls
// back to the longest-covering segmentation of 3+ letter words, skipping
// letters that start nothing recognisable ("iodesk" → "desk").
//
// Two-letter parts are only accepted in the full-coverage pass: obscure ones
// (od, es, qi) would otherwise chop coined names into nonsense and inflate
// their memorability — see issue #19.
func subWords(sld string) []string {
	if words, ok := segmentFull(sld); ok {
		return words
	}
	return segmentPartial(sld)
}

// maxShortParts is how many 2-letter parts a full-coverage segmentation may
// use. One is enough for real compounds ("ai" + "drive", "go" + "fast");
// allowing two lets nonsense through, e.g. "asan" as "as" + "an".
const maxShortParts = 1

// segmentFull returns the fewest-part segmentation that covers all of sld,
// using at most maxShortParts 2-letter words, or ok=false when there is none.
func segmentFull(sld string) ([]string, bool) {
	n := len(sld)
	if n == 0 {
		return nil, false
	}
	// parts[i][k] = fewest parts covering sld[i:] with k 2-letter words left
	// to spend; cut[i][k] = where the first of those parts ends.
	const unreachable = math.MaxInt
	parts := make([][maxShortParts + 1]int, n+1)
	cut := make([][maxShortParts + 1]int, n+1)
	for i := range n {
		for k := range parts[i] {
			parts[i][k] = unreachable
		}
	}
	for i := n - 1; i >= 0; i-- {
		for k := range parts[i] {
			for l := min(maxSubWord, n-i); l >= 2; l-- {
				spend := 0
				if l == 2 {
					spend = 1
				}
				if k < spend || !isSubWord(sld[i:i+l]) {
					continue
				}
				rest := parts[i+l][k-spend]
				if rest != unreachable && 1+rest < parts[i][k] {
					parts[i][k], cut[i][k] = 1+rest, i+l
				}
			}
		}
	}
	if parts[0][maxShortParts] == unreachable {
		return nil, false
	}
	var out []string
	for i, k := 0, maxShortParts; i < n; {
		end := cut[i][k]
		out = append(out, sld[i:end])
		if end-i == 2 {
			k--
		}
		i = end
	}
	return out, true
}

// segmentPartial returns the segmentation of 3+ letter words covering the
// most letters, preferring fewer parts, and skipping the rest.
func segmentPartial(sld string) []string {
	n := len(sld)
	covered := make([]int, n+1)
	parts := make([]int, n+1)
	take := make([]int, n+1) // length of the word taken at i, 0 = skip a letter
	for i := n - 1; i >= 0; i-- {
		covered[i], parts[i], take[i] = covered[i+1], parts[i+1], 0
		for l := min(maxSubWord, n-i); l >= 3; l-- {
			if !isSubWord(sld[i : i+l]) {
				continue
			}
			c, p := l+covered[i+l], 1+parts[i+l]
			if c > covered[i] || (c == covered[i] && p < parts[i]) {
				covered[i], parts[i], take[i] = c, p, l
			}
		}
	}
	var out []string
	for i := 0; i < n; {
		if take[i] == 0 {
			i++
			continue
		}
		out = append(out, sld[i:i+take[i]])
		i += take[i]
	}
	return out
}

// avgVec computes the mean vector over a list of words, ignoring unknowns.
// Returns (vec, true) if at least one word was found.
func avgVec(words []string) ([gloveDims]float32, bool) {
	var sum [gloveDims]float32
	n := 0
	for _, w := range words {
		v, ok := glove.vecFor(w)
		if !ok {
			continue
		}
		for d := range sum {
			sum[d] += v[d]
		}
		n++
	}
	if n == 0 {
		return sum, false
	}
	fn := float32(n)
	for d := range sum {
		sum[d] /= fn
	}
	return sum, true
}

func cosine(a, b [gloveDims]float32) float32 {
	var dot, na, nb float32
	for d := range a {
		dot += a[d] * b[d]
		na += a[d] * a[d]
		nb += b[d] * b[d]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (float32(math.Sqrt(float64(na))) * float32(math.Sqrt(float64(nb))))
}

// memorability returns the fraction of the SLD covered by recognised English
// sub-words (greedy longest-match over the GloVe vocabulary). Range [0, 1].
// Examples: "facebook" → 1.0 (face+book), "airbnb" → 0.5 (air), "xkqvz" → 0.0.
func memorability(sld string) float64 {
	n := len(sld)
	if n == 0 {
		return 0
	}
	// 1-2 letter names are memorable by definition, and too short to split.
	// This also covers two-letter names missing from the vocabulary, such as
	// "io" (issue #19).
	if n <= 2 {
		return 1
	}
	// A whole dictionary word is maximally recognisable, even when the
	// vocabulary cannot split it ("stillness", "solstice").
	if _, ok := wordlist.Level(sld); ok {
		return 1
	}
	matched := 0
	for _, w := range subWords(sld) {
		matched += len(w)
	}
	return float64(matched) / float64(n)
}

// ConceptRelevance returns the semantic relevance of sld to the query tokens,
// in [0.1, 1.0]. ok is false when it cannot be computed (e.g. no tokens or no
// recognizable subwords).
//
// Hybrid Architecture:
// 1. Attempts exact GloVe subword lookup on the SLD and query tokens.
// 2. If any part cannot be resolved (coined brand names, neologisms, or OOV
//    query tokens), it falls back to FastText quantized subword character
//    n-grams to compute embeddings in the same coordinate space.
func ConceptRelevance(sld string, tokens []string) (float64, bool) {
	if len(tokens) == 0 {
		return 0, false
	}
	sw := subWords(sld)
	if len(sw) == 0 {
		return 0.5, false
	}

	sldVec, sldOK := avgVec(sw)
	qVec, qOK := avgVec(tokens)
	if sldOK && qOK {
		cos := float64(cosine(sldVec, qVec))
		return math.Max(0.1, math.Min(1.0, 0.5+cos*0.8)), true
	}

	if fasttext != nil {
		var sVec, qvVec [gloveDims]float32
		var sFound, qFound bool

		if sldOK {
			sVec, sFound = sldVec, true
		} else {
			sVec, sFound = fasttext.EmbedWord(sld)
		}

		if qOK {
			qvVec, qFound = qVec, true
		} else {
			qvVec, qFound = fasttext.EmbedTokens(tokens)
		}

		if sFound && qFound {
			cos := float64(cosine(sVec, qvVec))
			return math.Max(0.1, math.Min(1.0, 0.5+cos*0.8)), true
		}
	}

	return 0.5, false
}

// conceptRelevance is ConceptRelevance with a neutral 0.5 when relevance
// cannot be computed, which is what scoring uses.
func conceptRelevance(sld string, tokens []string) float64 {
	if r, ok := ConceptRelevance(sld, tokens); ok {
		return r
	}
	return 0.5
}

