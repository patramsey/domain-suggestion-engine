package main

import "testing"

func TestAgreement(t *testing.T) {
	human := map[string]string{"a": "good", "b": "good", "c": "okay", "d": "bad", "e": "good"}
	judge := map[string]string{"a": "good", "b": "okay", "c": "okay", "d": "okay", "e": "good"}
	st := agreement(human, judge)
	if st.N != 5 {
		t.Fatalf("N = %d, want 5", st.N)
	}
	if st.Exact != 3 { // a, c, e
		t.Errorf("Exact = %d, want 3", st.Exact)
	}
	if st.GoodVsNot != 4 { // b is good for the human, not-good for the judge
		t.Errorf("GoodVsNot = %d, want 4", st.GoodVsNot)
	}
	if st.Confusion["good"]["okay"] != 1 || st.Confusion["bad"]["okay"] != 1 {
		t.Errorf("confusion = %v", st.Confusion)
	}
	if st.JudgeGoodShare() != 0.4 || st.HumanGoodShare() != 0.6 {
		t.Errorf("good shares = judge %v human %v", st.JudgeGoodShare(), st.HumanGoodShare())
	}
}

func TestAgreementIgnoresUnjudged(t *testing.T) {
	st := agreement(map[string]string{"a": "good", "b": "bad"}, map[string]string{"a": "good"})
	if st.N != 1 || st.Missing != 1 {
		t.Errorf("N = %d, Missing = %d, want 1 and 1", st.N, st.Missing)
	}
}
