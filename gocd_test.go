package gocd

import (
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	yaml "gopkg.in/yaml.v2"
)

type TestCase struct {
	Name           string `yaml:"name"`
	Before         string `yaml:"before"`
	After          string `yaml:"after"`
	Designator     string `yaml:"des"`
	DesignatorStd  string `yaml:"des_std"`
	Lang           string `yaml:"lang"`
	Position       string `yaml:"position"`
	Skip           bool   `yaml:"skip"`
	SkipUnlessLang bool   `yaml:"skip_unless_lang"`
}

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

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func loadTests() []TestCase {
	var tests []TestCase

	data, err := ioutil.ReadFile("data/tests.yml")
	if err != nil {
		fatal(err.Error())
	}
	err = yaml.Unmarshal(data, &tests)
	if err != nil {
		fatal(err.Error())
	}

	return tests
}

func loadStripTests() []TestCase {
	tests := loadTests()

	// Strip currently unsupported tests
	var tests2 []TestCase
	for _, tc := range tests {
		if tc.Position == "" {
			fatal(fmt.Sprintf("missing position for test entry %q", tc.Name))
		}
		if tc.Skip || tc.SkipUnlessLang {
			continue
		}
		// We don't handle LangContinua languages yet
		if ok := LangContinua[tc.Lang]; ok {
			continue
		}
		// We don't handle embedded matches yet
		if tc.Position == "mid" {
			continue
		}

		tests2 = append(tests2, tc)
	}

	return tests2
}

func TestFull(t *testing.T) {
	tests := loadTests()

	p, err := NewMode(RE)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(len(tests), "tests loaded")
	c := 0
	for _, tc := range tests {
		if tc.Position == "" {
			t.Fatalf("missing position for test entry %q", tc.Name)
		}
		if tc.Skip || tc.SkipUnlessLang {
			continue
		}
		// We don't handle LangContinua languages yet
		if ok := LangContinua[tc.Lang]; ok {
			continue
		}
		// We don't handle embedded matches yet
		if tc.Position == "mid" {
			continue
		}

		c++
		res, err := p.Parse(tc.Name)
		if err != nil {
			t.Fatal(err)
		}
		if tc.Before != "" {
			assert.Equal(t, tc.Name, res.Input, "Input matches")
			assert.Equal(t, tc.Before, res.ShortName, "ShortName matches")
			assert.Equal(t, tc.Designator, res.Designator, "Designator matches")
			assert.Equal(t, tc.Position, res.Position.String(), "Position matches")
		}
	}

	t.Log(c, "tests completed")
}

func BenchmarkRE(b *testing.B) {
	tests := loadStripTests()

	p, err := NewMode(RE)
	if err != nil {
		b.Fatal(err)
	}

	// Benchmark loop, iterating over tests in tests
	j := 0
	for i := 0; i < b.N; i++ {
		tc := tests[j]
		_, err := p.Parse(tc.Name)
		if err != nil {
			b.Fatal(err)
		}

		j++
		if j >= len(tests) {
			j = 0
		}
	}
}

func BenchmarkHS(b *testing.B) {
	tests := loadStripTests()

	p, err := NewMode(HS)
	if err != nil {
		b.Fatal(err)
	}

	// Benchmark loop, iterating over tests in tests
	j := 0
	for i := 0; i < b.N; i++ {
		tc := tests[j]
		_, err := p.Parse(tc.Name)
		if err != nil {
			b.Fatal(err)
		}

		j++
		if j >= len(tests) {
			j = 0
		}
	}
}
