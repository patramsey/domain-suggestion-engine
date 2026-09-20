package scorer

import (
	"slices"
	"testing"
)

func TestExtractNGrams(t *testing.T) {
	grams := ExtractNGrams("tech", 3, 3)
	// Bounded form: "<tech>"
	// 3-grams: "<te", "tec", "ech", "ch>"
	want := []string{"<te", "tec", "ech", "ch>"}
	for _, w := range want {
		if !slices.Contains(grams, w) {
			t.Errorf("expected %q in extracted n-grams %v", w, grams)
		}
	}
}

func TestHashNGramDeterministic(t *testing.T) {
	buckets := 1000
	h1 := HashNGram("tec", buckets)
	h2 := HashNGram("tec", buckets)
	if h1 != h2 {
		t.Errorf("HashNGram non-deterministic: %d != %d", h1, h2)
	}
	if h1 < 0 || h1 >= buckets {
		t.Errorf("hash out of bounds: %d", h1)
	}
}

func TestSubwordModelCoinedWordEmbedding(t *testing.T) {
	m := NewSubwordModel(4, 100)

	// Simulate n-gram vectors for "tech"
	idx := HashNGram("tec", 100)
	m.gramVec[idx] = []float32{1.0, 0.0, 0.0, 0.0}

	// "techify" is not in wordVec, but should produce an embedding from its n-grams
	vec := m.Embed("techify")
	if vec == nil {
		t.Fatal("expected non-nil embedding for coined word with matching n-grams")
	}
	if len(vec) != 4 {
		t.Errorf("expected 4 dims, got %d", len(vec))
	}
	if vec[0] <= 0 {
		t.Errorf("expected positive component along tech dimension, got %v", vec)
	}
}

func TestCosineSimilarityOrthogonalAndIdentical(t *testing.T) {
	v1 := []float32{1.0, 0.0}
	v2 := []float32{1.0, 0.0}
	v3 := []float32{0.0, 1.0}

	if sim := CosineSimilarity(v1, v2); sim < 0.99 {
		t.Errorf("expected ~1.0 for identical vectors, got %f", sim)
	}
	if sim := CosineSimilarity(v1, v3); sim > 0.6 || sim < 0.4 {
		// (0 + 1) / 2 = 0.5 for orthogonal vectors
		t.Errorf("expected 0.5 for orthogonal vectors, got %f", sim)
	}
}

func TestQuantizedFastTextLoaded(t *testing.T) {
	if fasttext == nil {
		t.Fatal("expected fasttext model to be loaded via init")
	}
	if fasttext.buckets != 65536 {
		t.Errorf("expected 65536 buckets, got %d", fasttext.buckets)
	}
}

func TestQuantizedFastTextSemanticCoherence(t *testing.T) {
	if fasttext == nil {
		t.Skip("fasttext model not initialized")
	}
	vTechify, ok1 := fasttext.EmbedWord("techify")
	vTech, ok2 := fasttext.EmbedWord("technology")
	vBanana, ok3 := fasttext.EmbedWord("banana")

	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("EmbedWord failed: ok1=%v, ok2=%v, ok3=%v", ok1, ok2, ok3)
	}

	cosTech := cosine(vTechify, vTech)
	cosBanana := cosine(vTechify, vBanana)

	if cosTech <= cosBanana {
		t.Errorf("expected techify to be closer to technology (%f) than banana (%f)", cosTech, cosBanana)
	}
}

func TestConceptRelevanceFastTextEnrichment(t *testing.T) {
	// "techify" has subword "tech" but is a coined word not in GloVe vocab.
	// ConceptRelevance should compute a higher score for "technology" than "banana".
	relTech, okTech := ConceptRelevance("techify", []string{"technology"})
	relBanana, okBanana := ConceptRelevance("techify", []string{"banana"})

	if !okTech || !okBanana {
		t.Fatalf("expected ConceptRelevance to succeed for techify: okTech=%v, okBanana=%v", okTech, okBanana)
	}
	if relTech <= relBanana {
		t.Errorf("expected ConceptRelevance(techify, technology) (%f) > ConceptRelevance(techify, banana) (%f)", relTech, relBanana)
	}
}

