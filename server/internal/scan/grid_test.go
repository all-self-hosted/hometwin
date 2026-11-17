package scan

import (
	"fmt"
	"testing"
)

func TestOutline(t *testing.T) {
	g := grid{cell: 1, nx: 8, nz: 8}
	block := func(cells ...[2]int) []bool {
		m := make([]bool, 64)
		for _, c := range cells {
			m[c[1]*8+c[0]] = true
		}
		return m
	}
	var sq [][2]int
	for j := 1; j < 6; j++ {
		for i := 1; i < 6; i++ {
			sq = append(sq, [2]int{i, j})
		}
	}
	for _, c := range []struct {
		m    []bool
		want string
	}{
		{block(sq...), "[[1 1] [6 1] [6 6] [1 6]]"},
		// a cell touching only at a corner rides along (8-connected)
		{block(append(sq, [2]int{6, 6})...), "[[1 1] [6 1] [6 6] [7 6] [7 7] [6 7] [6 6] [1 6]]"},
		// a chimney on top
		{block(append(sq[5:], [2]int{5, 1}, [2]int{5, 0})...), "[[5 0] [6 0] [6 6] [1 6] [1 2] [5 2]]"},
	} {
		if got := fmt.Sprint(g.outline(c.m)); got != c.want {
			t.Errorf("outline %s, want %s", got, c.want)
		}
	}
}
