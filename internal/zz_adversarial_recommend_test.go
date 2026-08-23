package internal

import (
	"testing"
)

// G4: recQueries паникует на малых наборах весов из-за безусловного доступа
// к weights[i] / rare[i]. Достаточно 5 лайков с 1..4 «весомыми» тегами,
// чтобы /api/recommend?page=1|2|3 превратился в 500 (паника в gin.Recovery).
//
// page 1 (slice 0): weights[1] при len==1  → index out of range
// page 2 (slice 1): weights[3] при len==3  → index out of range
// page 3 (slice 2): rare[1]  при len==4    → index out of range
func TestAdversarialRecQueriesPanicOnShortWeights(t *testing.T) {
	cases := []struct {
		name    string
		weights []RecTagWeight
		page    int
	}{
		{"1_вес_page_1", []RecTagWeight{{Tag: "a", Weight: 1}}, 1},
		{"3_веса_page_2", []RecTagWeight{{Tag: "a", Weight: 3}, {Tag: "b", Weight: 2}, {Tag: "c", Weight: 1}}, 2},
		{"4_веса_page_3", []RecTagWeight{{Tag: "a", Weight: 4}, {Tag: "b", Weight: 3}, {Tag: "c", Weight: 2}, {Tag: "d", Weight: 1}}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("BUG G4: recQueries(%d весов, page=%d) паникует: %v", len(tc.weights), tc.page, r)
				}
			}()
			recQueries(tc.weights, tc.page, nil, nil)
		})
	}
}

// Контрольный прогон: граничные наборы, которые НЕ должны паниковать.
func TestAdversarialRecQueriesValidShapes(t *testing.T) {
	ok := func(weights []RecTagWeight, page int) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("recQueries(%d весов, page=%d) неожиданно паникует: %v", len(weights), page, r)
			}
		}()
		recQueries(weights, page, nil, nil)
	}
	one := []RecTagWeight{{Tag: "a", Weight: 1}}
	ok(one, 2)                                                                                                                               // slice 1, len<=2 → только weights[0]
	ok(one, 3)                                                                                                                               // slice 2, rare пуст → fallback weights[0]
	ok([]RecTagWeight{{Tag: "a", Weight: 2}, {Tag: "b", Weight: 1}}, 1)                                                                      // slice 0, len==2
	ok([]RecTagWeight{{Tag: "a", Weight: 3}, {Tag: "b", Weight: 2}, {Tag: "c", Weight: 1}}, 3)                                               // slice 2, rare=1
	ok([]RecTagWeight{{Tag: "a", Weight: 5}, {Tag: "b", Weight: 4}, {Tag: "c", Weight: 3}, {Tag: "d", Weight: 2}, {Tag: "e", Weight: 1}}, 3) // slice 2, rare=2
}
