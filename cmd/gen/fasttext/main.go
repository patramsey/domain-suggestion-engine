// Generates internal/scorer/data/fasttext.bin containing quantized subword embeddings.
//
// Trains character n-gram bucket representations (FastText architecture) from the 50d
// GloVe vocabulary vectors using stochastic gradient descent. Quantises float32 → int8
// and writes a compact binary (~3.2 MB) embedded via internal/scorer/fasttext_embed.go.
//
// Binary format:
//   [4]byte  magic "FSTX"
//   uint8    version=1
//   uint8    dims=50
//   uint8    minN=3
//   uint8    maxN=6
//   uint32   buckets (little-endian, default 65536)
//   [dims]float32 scales  (little-endian, per-dimension dequantise factor)
//   [dims]float32 offsets (little-endian, per-dimension dequantise base)
//   [buckets * dims]int8  quantised bucket vectors
//
// Usage: go run ./cmd/gen/fasttext
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

const (
	defaultInPath  = "internal/scorer/data/glove.bin"
	defaultOutPath = "internal/scorer/data/fasttext.bin"
	dims           = 50
	defaultBuckets = 65536 // 64K buckets = ~3.28 MB
	defaultMinN    = 3
	defaultMaxN    = 6
	defaultEpochs  = 15
	defaultLR      = 0.05
)

func main() {
	inPath := flag.String("in", defaultInPath, "Input glove.bin path")
	outPath := flag.String("out", defaultOutPath, "Output fasttext.bin path")
	buckets := flag.Int("buckets", defaultBuckets, "Number of hash buckets")
	minN := flag.Int("min-n", defaultMinN, "Minimum n-gram length")
	maxN := flag.Int("max-n", defaultMaxN, "Maximum n-gram length")
	epochs := flag.Int("epochs", defaultEpochs, "SGD training epochs")
	lr := flag.Float64("lr", defaultLR, "Initial learning rate")
	flag.Parse()

	fmt.Printf("Loading word vectors from %s...\n", *inPath)
	words, vecs, err := loadGlove(*inPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load glove: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Loaded %d words (dims=%d)\n", len(words), dims)

	fmt.Printf("Extracting character %d-%d grams into %d buckets...\n", *minN, *maxN, *buckets)
	type wordItem struct {
		indices []int
		vec     [dims]float32
	}
	corpus := make([]wordItem, len(words))
	for i, w := range words {
		grams := extractNGrams(w, *minN, *maxN)
		idxs := make([]int, len(grams))
		for j, g := range grams {
			idxs[j] = hashNGram(g, *buckets)
		}
		corpus[i] = wordItem{indices: idxs, vec: vecs[i]}
	}

	fmt.Printf("Training subword embeddings (%d epochs, lr=%.3f)...\n", *epochs, *lr)
	t0 := time.Now()
	gramVecs := make([][dims]float32, *buckets)
	currentLR := float32(*lr)

	for epoch := 0; epoch < *epochs; epoch++ {
		var totalLoss float32
		for _, item := range corpus {
			k := len(item.indices)
			if k == 0 {
				continue
			}
			invK := float32(1.0 / float32(k))
			var pred [dims]float32
			for _, b := range item.indices {
				for d := 0; d < dims; d++ {
					pred[d] += gramVecs[b][d]
				}
			}
			for d := 0; d < dims; d++ {
				pred[d] *= invK
			}

			for d := 0; d < dims; d++ {
				diff := pred[d] - item.vec[d]
				totalLoss += diff * diff
				grad := diff * invK * currentLR
				for _, b := range item.indices {
					gramVecs[b][d] -= grad
				}
			}
		}
		currentLR *= 0.85
		if (epoch+1)%5 == 0 || epoch == *epochs-1 {
			fmt.Printf("  Epoch %2d/%2d: avg MSE = %.6f (elapsed %v)\n",
				epoch+1, *epochs, totalLoss/float32(len(corpus)*dims), time.Since(t0).Round(time.Millisecond))
		}
	}

	fmt.Println("Quantising bucket vectors to int8...")
	scales, offsets, quant := quantise(gramVecs, *buckets)

	// Quality verification
	verifyModel(quant, scales, offsets, *buckets, *minN, *maxN)

	fmt.Printf("Writing binary to %s...\n", *outPath)
	if err := writeBinary(*outPath, quant, scales, offsets, *buckets, *minN, *maxN); err != nil {
		fmt.Fprintf(os.Stderr, "write binary: %v\n", err)
		os.Exit(1)
	}

	info, _ := os.Stat(*outPath)
	fmt.Printf("Successfully generated %s (%.2f MB)\n", *outPath, float64(info.Size())/(1024*1024))
}

func extractNGrams(word string, minN, maxN int) []string {
	bounded := "<" + strings.ToLower(word) + ">"
	runes := []rune(bounded)
	var grams []string
	for n := minN; n <= maxN; n++ {
		for i := 0; i+n <= len(runes); i++ {
			gram := string(runes[i : i+n])
			if gram == bounded {
				continue
			}
			grams = append(grams, gram)
		}
	}
	return grams
}

func hashNGram(gram string, numBuckets int) int {
	var h uint32 = 2166136261
	for _, b := range []byte(gram) {
		h ^= uint32(b)
		h *= 16777619
	}
	return int(h % uint32(numBuckets))
}

func loadGlove(path string) ([]string, [][dims]float32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(data) < 10 || string(data[:4]) != "GLVE" || data[4] != 1 {
		return nil, nil, fmt.Errorf("invalid glove binary")
	}
	d := data[6:]
	vocabSize := int(binary.LittleEndian.Uint32(d[:4]))
	d = d[4:]

	floatBytes := dims * 4
	var scales, offs [dims]float32
	for i := range scales {
		scales[i] = math.Float32frombits(binary.LittleEndian.Uint32(d[i*4:]))
	}
	d = d[floatBytes:]
	for i := range offs {
		offs[i] = math.Float32frombits(binary.LittleEndian.Uint32(d[i*4:]))
	}
	d = d[floatBytes:]

	words := make([]string, 0, vocabSize)
	vecs := make([][dims]float32, 0, vocabSize)

	for i := 0; i < vocabSize; i++ {
		wLen := int(d[0])
		d = d[1:]
		word := string(d[:wLen])
		d = d[wLen:]
		var vec [dims]float32
		for j := range vec {
			q := int8(d[j])
			vec[j] = float32(int(q)+127)*scales[j] + offs[j]
		}
		d = d[dims:]
		words = append(words, word)
		vecs = append(vecs, vec)
	}
	return words, vecs, nil
}

func quantise(vecs [][dims]float32, buckets int) ([dims]float32, [dims]float32, [][dims]int8) {
	var scales, offsets [dims]float32
	for d := 0; d < dims; d++ {
		minV := float32(math.MaxFloat32)
		maxV := float32(-math.MaxFloat32)
		for b := 0; b < buckets; b++ {
			if vecs[b][d] < minV {
				minV = vecs[b][d]
			}
			if vecs[b][d] > maxV {
				maxV = vecs[b][d]
			}
		}
		rng := maxV - minV
		if rng == 0 {
			rng = 1
		}
		scales[d] = rng / 254.0
		offsets[d] = minV
	}

	quant := make([][dims]int8, buckets)
	for b := 0; b < buckets; b++ {
		for d := 0; d < dims; d++ {
			q := int((vecs[b][d]-offsets[d])/scales[d]) - 127
			if q < -127 {
				q = -127
			} else if q > 127 {
				q = 127
			}
			quant[b][d] = int8(q)
		}
	}
	return scales, offsets, quant
}

func verifyModel(quant [][dims]int8, scales, offsets [dims]float32, buckets, minN, maxN int) {
	embed := func(w string) [dims]float32 {
		grams := extractNGrams(w, minN, maxN)
		var sum [dims]float32
		for _, g := range grams {
			b := hashNGram(g, buckets)
			q := quant[b]
			for d := 0; d < dims; d++ {
				sum[d] += float32(int(q[d])+127)*scales[d] + offsets[d]
			}
		}
		var norm float32
		for _, v := range sum {
			norm += v * v
		}
		if norm > 0 {
			inv := float32(1.0 / math.Sqrt(float64(norm)))
			for d := range sum {
				sum[d] *= inv
			}
		}
		return sum
	}

	cos := func(a, b [dims]float32) float32 {
		var dot float32
		for d := 0; d < dims; d++ {
			dot += a[d] * b[d]
		}
		return dot
	}

	pairs := [][2]string{
		{"techify", "technology"},
		{"techify", "banana"},
		{"coffeely", "coffee"},
		{"coffeely", "banana"},
	}
	fmt.Println("Verification sample cosine similarities:")
	for _, p := range pairs {
		v1 := embed(p[0])
		v2 := embed(p[1])
		fmt.Printf("  cos(%s, %s) = %.3f\n", p[0], p[1], cos(v1, v2))
	}
}

func writeBinary(path string, quant [][dims]int8, scales, offsets [dims]float32, buckets, minN, maxN int) error {
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

	// Header: magic "FSTX", ver=1, dims=50, minN=3, maxN=6, buckets uint32
	f.Write([]byte("FSTX"))
	f.Write([]byte{1, dims, byte(minN), byte(maxN)})
	write(uint32(buckets))
	write(scales)
	write(offsets)

	// Quantized vectors: buckets * dims int8
	for b := 0; b < buckets; b++ {
		if err != nil {
			break
		}
		write(quant[b])
	}
	return err
}
