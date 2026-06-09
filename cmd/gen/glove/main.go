// Generates internal/scorer/data/glove.bin from GloVe 6B 50-dimensional vectors.
//
// Keeps the top-20K most frequent vocabulary entries (single lowercase words,
// 3–14 chars, a-z only), quantises float32 → int8, and writes a compact binary
// (~1 MB) embedded via internal/scorer/glove_embed.go.
//
// Binary format:
//   [4]byte  magic "GLVE"
//   uint8    version=1
//   uint8    dims=50
//   uint32   vocabSize (little-endian)
//   [dims]float32  scales   (little-endian, per-dimension dequantise factor)
//   [dims]float32  offsets  (little-endian, per-dimension dequantise base)
//   per-word: uint8 wordLen, [wordLen]byte word, [dims]int8 quantised vector
//
// Data source: Stanford NLP GloVe 6B (CC-BY 4.0)
//   https://nlp.stanford.edu/data/glove.6B.zip
//
// Usage: go run ./cmd/gen/glove
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	gloveURL   = "https://nlp.stanford.edu/data/glove.6B.zip"
	targetFile = "glove.6B.50d.txt"
	outPath    = "internal/scorer/data/glove.bin"
	maxVocab   = 20_000
	dims       = 50
	httpTTL    = 10 * time.Minute
)

func main() {
	fmt.Printf("Fetching %s (may take a few minutes)...\n", gloveURL)
	rawVecs, err := fetchGlove()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Loaded %d raw entries\n", len(rawVecs))

	vocab, vecs := filterVocab(rawVecs)
	fmt.Printf("Kept %d words after filtering\n", len(vocab))

	scales, offsets := computeScaling(vecs)
	quant := quantise(vecs, scales, offsets)

	if err := writeBinary(outPath, vocab, quant, scales, offsets); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
	info, _ := os.Stat(outPath)
	fmt.Printf("Wrote %s (%.1f KB)\n", outPath, float64(info.Size())/1024)
}

type wv struct {
	word string
	vec  [dims]float32
}

func fetchGlove() ([]wv, error) {
	client := &http.Client{Timeout: httpTTL}
	resp, err := client.Get(gloveURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Downloaded %.0f MB\n", float64(len(body))/1e6)

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("unzip: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != targetFile {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return parseGlove(rc)
	}
	return nil, fmt.Errorf("%s not found in zip", targetFile)
}

func parseGlove(r io.Reader) ([]wv, error) {
	const readLimit = maxVocab * 4 // read 4× budget then stop
	var out []wv
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() && len(out) < readLimit {
		fields := strings.Fields(scanner.Text())
		if len(fields) != dims+1 {
			continue
		}
		var v [dims]float32
		ok := true
		for i := range v {
			f, err := strconv.ParseFloat(fields[i+1], 32)
			if err != nil {
				ok = false
				break
			}
			v[i] = float32(f)
		}
		if ok {
			out = append(out, wv{fields[0], v})
		}
	}
	return out, scanner.Err()
}

func filterVocab(data []wv) ([]string, [][dims]float32) {
	vocab := make([]string, 0, maxVocab)
	vecs := make([][dims]float32, 0, maxVocab)
	for _, entry := range data {
		if len(vocab) >= maxVocab {
			break
		}
		w := entry.word
		if len(w) < 3 || len(w) > 14 || !isLowerAlpha(w) {
			continue
		}
		vocab = append(vocab, w)
		vecs = append(vecs, entry.vec)
	}
	return vocab, vecs
}

func isLowerAlpha(s string) bool {
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func computeScaling(vecs [][dims]float32) (scales, offsets [dims]float32) {
	dMin := [dims]float32{}
	dMax := [dims]float32{}
	for i := range dMin {
		dMin[i] = math.MaxFloat32
		dMax[i] = -math.MaxFloat32
	}
	for _, v := range vecs {
		for d := range v {
			if v[d] < dMin[d] {
				dMin[d] = v[d]
			}
			if v[d] > dMax[d] {
				dMax[d] = v[d]
			}
		}
	}
	for d := range scales {
		rng := dMax[d] - dMin[d]
		if rng == 0 {
			rng = 1
		}
		scales[d] = rng / 254.0
		offsets[d] = dMin[d]
	}
	return
}

func quantise(vecs [][dims]float32, scales, offsets [dims]float32) [][dims]int8 {
	out := make([][dims]int8, len(vecs))
	for i, v := range vecs {
		for d := range v {
			q := int((v[d]-offsets[d])/scales[d]) - 127
			if q < -127 {
				q = -127
			} else if q > 127 {
				q = 127
			}
			out[i][d] = int8(q)
		}
	}
	return out
}

func writeBinary(path string, vocab []string, quant [][dims]int8, scales, offsets [dims]float32) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	le := binary.LittleEndian
	write := func(v any) {
		if err == nil {
			err = binary.Write(f, le, v)
		}
	}

	// header
	f.Write([]byte("GLVE"))
	f.Write([]byte{1, dims})
	write(uint32(len(vocab)))
	write(scales)
	write(offsets)

	// per-word entries
	for i, w := range vocab {
		if err != nil {
			break
		}
		f.Write([]byte{byte(len(w))})
		f.WriteString(w)
		write(quant[i])
	}
	return err
}
