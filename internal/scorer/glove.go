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

// subWords splits an SLD into known vocabulary sub-words using greedy
// longest-match, skipping characters that don't start a recognised word.
// Recognises GloVe vocabulary words (length 3+) and real 2-letter words from SCOWL.
// Example: "forgeio" → ["forge", "io"].
func subWords(sld string) []string {
	var result []string
	i := 0
	for i < len(sld) {
		found := false
		end := min(i+14, len(sld))
		for l := end - i; l >= 2; l-- {
			cand := sld[i : i+l]
			if _, ok := glove.index[cand]; ok {
				result = append(result, cand)
				i += l
				found = true
				break
			}
			if l == 2 {
				if _, ok := wordlist.Level(cand); ok {
					result = append(result, cand)
					i += l
					found = true
					break
				}
			}
		}
		if !found {
			i++
		}
	}
	return result
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

