/*
gocd is a go library for matching and parsing company designators
(like `Limited`, `LLC`, `Incorporée`) in company names.
*/

//go:generate cp -p ../company_designator/company_designator.yml data
//go:generate cp -p ../../cpan/Business-CompanyDesignator/t/t10/data.yml data/tests.yml

package gocd

import (
	"fmt"
	"io/ioutil"
	"os"
	"regexp"

	"github.com/flier/gohs/hyperscan"
	"gopkg.in/yaml.v2"
)

const DefaultDataset = "data/company_designator.yml"

var LangContinua = map[string]bool{"zh": true, "ja": true, "ko": true}

type PositionType int

const (
	None PositionType = iota
	Begin
	End
)

func (p PositionType) String() string {
	return [...]string{"none", "begin", "end"}[p]
}

type entry struct {
	LongName string
	AbbrStd  string   `yaml:"abbr_std,omitempty"`
	Abbr     []string `yaml:"abbr,omitempty"`
	Lang     string   `yaml:"lang,omitempty"`
	Lead     bool     `yaml:"lead,omitempty"`
	Doc      string   `yaml:"doc,omitempty"`
}

type dataset map[string]entry

type Parser struct {
	ds     *dataset
	dbEnd  *hyperscan.BlockDatabase
	scrEnd *hyperscan.Scratch
}

type Context struct {
	in     []byte
	from   uint64
	to     uint64
	before []byte
	match  []byte
	after  []byte
}

type Result struct {
	Input      string       // Initial input string
	Matched    bool         // True if a Designator was found
	ShortName  string       // Input with any matched Designator removed
	Designator string       // The Designator found in input, if any (verbatim)
	Position   PositionType // The Designator position, if found
}

// Various regexes
var rePeriod, reSpace, reParen *regexp.Regexp
var strEndBefore, strEndAfter string
var reEndBefore, reEndAfter *regexp.Regexp

// Initialise regexes
func init() {
	rePeriod = regexp.MustCompile(`\.`)
	reSpace = regexp.MustCompile(`\s+`)
	reParen = regexp.MustCompile(`([()])`)
	strEndBefore = `\s*[\s\pP]\s*\(?`
	strEndAfter = `\)?\s*$`
	reEndBefore = regexp.MustCompile(`^` + strEndBefore)
	reEndAfter = regexp.MustCompile(strEndAfter + `$`)
}

func loadDataset(filepath string) (*dataset, error) {
	data, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, err
	}

	ds := make(dataset)
	err = yaml.Unmarshal(data, ds)
	if err != nil {
		return nil, err
	}

	//fmt.Fprintf(os.Stderr, "+ loaded %d entries from dataset %q\n", len(ds), filepath)

	return &ds, nil
}

func compilePattern(des string, t PositionType) *hyperscan.Pattern {
	// Prep s for matching
	// Periods are treated as optional literals, with optional trailing commas
	// and/or whitespace
	des = rePeriod.ReplaceAllString(des, `\.?,?\s*`)
	// Embedded spaces can optionally include leading commas
	des = reSpace.ReplaceAllString(des, `,?\s+`)
	// Escape parentheses
	des = reParen.ReplaceAllString(des, `\$1`)

	// Wrap des appropriately for position
	var s string
	switch t {
	case End:
		s = strEndBefore + des + strEndAfter
	default:
		fmt.Fprintf(os.Stderr, "unsupported position %q\n", t.String())
		os.Exit(1)
	}

	// Compile to hyperscan pattern
	return hyperscan.NewPattern(s, hyperscan.Caseless|hyperscan.SomLeftMost)
}

func compilePatterns(ds *dataset, t PositionType) []*hyperscan.Pattern {
	var patterns []*hyperscan.Pattern

	for k, e := range *ds {
		// Add key to patterns
		patterns = append(patterns, compilePattern(k, t))

		// Add AbbrStd to patterns
		if e.AbbrStd != "" {
			patterns = append(patterns, compilePattern(e.AbbrStd, t))
		}

		// Add Abbrs to patterns
		for _, a := range e.Abbr {
			patterns = append(patterns, compilePattern(a, t))
		}
	}

	//fmt.Fprintf(os.Stderr, "+ compiled %d %q patterns from dataset\n", len(patterns), t.String())
	//fmt.Fprintf(os.Stderr, "++ %v\n", patterns)

	return patterns
}

// New returns a new Parser using the default company designator dataset
func New() (*Parser, error) {
	p := Parser{}

	ds, err := loadDataset(DefaultDataset)
	if err != nil {
		return nil, err
	}
	p.ds = ds

	// Compile End patterns
	patterns := compilePatterns(ds, End)
	//fmt.Fprintf(os.Stderr, "+ loading hyperscan db...\n")
	db, err := hyperscan.NewBlockDatabase(patterns...)
	if err != nil {
		return nil, err
	}
	p.dbEnd = &db
	//fmt.Fprintf(os.Stderr, "+ setting up scratch space...\n")
	scratch, err := hyperscan.NewScratch(db)
	if err != nil {
		return nil, err
	}
	p.scrEnd = scratch

	return &p, nil
}

// hyperscan match function - captures match elements to Context struct
func hsMatchHandler(id uint, from, to uint64, flags uint, context interface{}) error {
	ctx := context.(*Context)
	if to > 0 {
		ctx.from = from
		ctx.to = to
		ctx.match = ctx.in[from:to]
		if from > 0 {
			ctx.before = ctx.in[0:from]
		}
		if len(ctx.in) > int(to) {
			ctx.before = ctx.in[to:]
		}
		//fmt.Fprintf(os.Stderr, "+ matched: from '%d', to '%d', des: %q\n", from, to, ctx.match)
	}
	return nil
}

// Parse matches an input company name string against the company
// designator dataset and returns a Result object containing match
// results and any parsed components
func (p *Parser) Parse(input string) (*Result, error) {
	res := Result{Input: input, ShortName: input}
	ctx := Context{}
	ctx.in = []byte(input)

	// Designators are usually final, so try end matching first
	db := *(p.dbEnd)
	err := db.Scan(ctx.in, p.scrEnd, hsMatchHandler, &ctx)
	if err != nil {
		return nil, err
	}

	// If we matched, update res accordingly
	if len(ctx.match) > 0 {
		res.Matched = true
		res.ShortName = string(ctx.before)
		res.Position = End

		des := string(ctx.match)
		des = reEndBefore.ReplaceAllString(des, "")
		des = reEndAfter.ReplaceAllString(des, "")
		res.Designator = des
	}

	return &res, nil
}
