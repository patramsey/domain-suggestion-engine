package scorer
import ("fmt";"testing")
func TestMemCheck(t *testing.T) {
	words := []string{"forge","facebook","airbnb","coffee","zymmo","xkqvz","rhythm","stripe","ember","cedar","stud"}
	for _, w := range words {
		fmt.Printf("%-12s  mem=%.2f  brand=%.3f\n", w, memorability(w), brandability(w))
	}
}
