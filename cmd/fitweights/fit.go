package main

import (
	"math"
	"sort"
)

// fit learns weights for a pairwise ranker by logistic regression on feature
// differences: each row is (features of the better name − features of the
// worse one), so the label is always 1 and the model learns which differences
// predict "better". No intercept — a constant cannot order a pair.
func fit(diffs [][]float64, epochs int, lr, l2 float64) []float64 {
	if len(diffs) == 0 {
		return nil
	}
	w := make([]float64, len(diffs[0]))
	for range epochs {
		grad := make([]float64, len(w))
		for _, d := range diffs {
			p := 1 / (1 + math.Exp(-dot(w, d))) // P(better wins)
			for j, x := range d {
				grad[j] += (1 - p) * x // ascend the log-likelihood
			}
		}
		n := float64(len(diffs))
		for j := range w {
			w[j] += lr * (grad[j]/n - l2*w[j])
		}
	}
	return w
}

func dot(w, x []float64) float64 {
	var s float64
	for i := range w {
		s += w[i] * x[i]
	}
	return s
}

// pairAccuracy is the share of pairs the weights order correctly. Ties count
// as half, so an all-zero vector scores 0.5 rather than 0.
func pairAccuracy(diffs [][]float64, w []float64) float64 {
	if len(diffs) == 0 {
		return 0
	}
	var right float64
	for _, d := range diffs {
		switch s := dot(w, d); {
		case s > 0:
			right++
		case s == 0:
			right += 0.5
		}
	}
	return right / float64(len(diffs))
}

// foldsByQuery splits queries into k groups. Folds hold whole queries so a
// name never trains and tests on the same business.
func foldsByQuery(queries []string, k int) [][]string {
	sorted := append([]string{}, queries...)
	sort.Strings(sorted)
	if k > len(sorted) {
		k = len(sorted)
	}
	folds := make([][]string, k)
	for i, q := range sorted {
		folds[i%k] = append(folds[i%k], q)
	}
	return folds
}

// normalize scales weights to sum to 1 over their absolute values, so they
// can be read next to the current hand-set weights.
func normalize(w []float64) []float64 {
	var sum float64
	for _, v := range w {
		sum += math.Abs(v)
	}
	if sum == 0 {
		return w
	}
	out := make([]float64, len(w))
	for i, v := range w {
		out[i] = v / sum
	}
	return out
}
