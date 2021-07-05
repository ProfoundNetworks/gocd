package gocd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBasic(t *testing.T) {
	tests := []struct {
		input string
		short string
		des   string
		pos   string
	}{
		{"Profound Networks LLC", "Profound Networks", "LLC", "end"},
		{"Profound Networks LLC (Seattle)", "", "", ""},
	}

	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	//t.Log("parser instantiated - starting tests")

	for _, tc := range tests {
		res, err := p.Parse(tc.input)
		if err != nil {
			t.Fatal(err)
		}
		if tc.short != "" {
			assert.Equal(t, tc.input, res.Input, "Input matches")
			assert.Equal(t, tc.input != tc.short, res.Matched, "Matched matches")
			assert.Equal(t, tc.short, res.ShortName, "ShortName matches")
			assert.Equal(t, tc.des, res.Designator, "Designator matches")
			assert.Equal(t, tc.pos, res.Position.String(), "Position matches")
		} else {
			assert.Equal(t, tc.input, res.ShortName, "ShortName matches Input")
		}
	}
}
