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
	"strings"

	"github.com/flier/gohs/hyperscan"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v2"
)

// In languages with continuous scripts, we don't require a word
// break ([\pZ\pP] before/after designators
var LangContinua = map[string]bool{
	"zh": true,
	"ja": true,
	"ko": true,
}

// The standard/perl RE engine in Go doesn't use POSIX-style
// longest match semantics, which bites us where we have proper
// subset alternates e.g. `Vennootschap` vs `Vennootschap Onder Firma`.
// We can workaround this by blacklisting the shorter variant and
// doing a second pass match if the first one fails.
var EndDesignatorBlacklist = map[string]bool{
	"Vennootschap": true, // vs. `Vennootschap Onder Firma`
	"Co.":          true, // vs. `& Co.` (perhaps an RE2 bug?)
	"Co. L.L.C.":   true, // vs. `& Co. L.L.C.` (ditto RE2 bug?)
	"L.L.C.":       true, // vs. `Co. L.L.C.`
	"L.C.":         true, // vs. `L.L.C.`
}

const (
	DefaultDataset = "data/company_designator.yml"
	// TODO: should the space/punct character class here have '+'?
	StrBeginBefore   = `^\pZ*\(?`
	StrBeginAfter    = `\)?[\pZ\pP]\pZ*(.*?)\pZ*$`
	StrEndBefore     = `^\pZ*(.*?)\pZ*[\pZ\pP]\pZ*\(?`
	StrEndAfter      = `\)?\pZ*$`
	StrEndContBefore = `^\pZ*(.*?)\pZ*\(?`
	StrEndContAfter  = `\)?\pZ*$`
)

type PositionType int

const (
	None PositionType = iota
	Begin
	End
	EndFallback
	EndCont
)

type Mode int

const (
	Null Mode = iota
	HS
	RE
)

func (p PositionType) String() string {
	return [...]string{"none", "begin", "end", "end_fallback", "end_cont"}[p]
}

type entry struct {
	LongName string
	AbbrStd  string   `yaml:"abbr_std"`
	Abbr     []string `yaml:"abbr"`
	Lang     string   `yaml:"lang"`
	Lead     bool     `yaml:"lead"`
	Doc      string   `yaml:"doc"`
}

type Remap map[string]*regexp.Regexp
type dataset map[string]entry

type Parser struct {
	re            Remap
	ds            *dataset
	mode          Mode
	reBegin       *regexp.Regexp
	reEnd         *regexp.Regexp
	reEndFallback *regexp.Regexp
	reEndCont     *regexp.Regexp
	dbEnd         *hyperscan.BlockDatabase
	scrEnd        *hyperscan.Scratch
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

func escapeDes(des string, re Remap) string {
	// Periods are treated as optional literals, with optional trailing commas
	// and/or whitespace
	des = re["Period"].ReplaceAllString(des, `\.?,?\pZ*`)
	// Embedded spaces can optionally include leading commas
	des = re["Space"].ReplaceAllString(des, `,?\pZ+`)
	// Escape parentheses
	des = re["Paren"].ReplaceAllString(des, `\$1`)
	return des
}

func addPattern(patterns []string, s string, t PositionType, re Remap) []string {
	// Skip End strings if they are blacklisted
	if t == End && EndDesignatorBlacklist[s] {
		return patterns
	}
	// Skip EndFallback strings *unless* they are blacklisted
	if t == EndFallback && !EndDesignatorBlacklist[s] {
		return patterns
	}

	// Normalise s to NFD before adding
	s = norm.NFD.String(s)

	// Do our standard designator escaping
	s = escapeDes(s, re)

	// Add s to patterns
	patterns = append(patterns, s)

	// If s contains unicode diacritics, also add a stripped version
	s2 := re["UnicodeMarks"].ReplaceAllString(s, "")
	if s2 != s {
		patterns = append(patterns, s2)
	}

	return patterns
}

func compileREPatterns(ds *dataset, t PositionType, re Remap) string {
	var patterns []string

	for long, e := range *ds {
		// FIXME: dev
		/*
			if long != "Company" {
				continue
			}
		*/
		// If t is Begin, restrict to entries with 'Lead' set
		if t == Begin && !e.Lead {
			continue
		}
		// If t is EndCont, restrict to languages in LangContinua
		if t == EndCont && !LangContinua[e.Lang] {
			continue
		}

		// Add long to patterns
		patterns = addPattern(patterns, long, t, re)

		// Add AbbrStd to patterns
		/*
			if e.AbbrStd != "" {
				patterns = addPattern(patterns, e.AbbrStd, t, re)
			}
		*/

		// Add Abbrs to patterns
		for _, a := range e.Abbr {
			// Only add non-ASCII abbreviations as continuous
			if t == EndCont && re["ASCII"].MatchString(a) {
				continue
			}
			patterns = addPattern(patterns, a, t, re)
		}
	}

	pattern := strings.Join(patterns, "|")

	//fmt.Fprintf(os.Stderr, "+ compiled %d %q patterns from dataset\n", len(patterns), t.String())
	//fmt.Fprintf(os.Stderr, "++ %s\n", pattern)

	return pattern
}

func compileHSPattern(des string, t PositionType, re Remap) *hyperscan.Pattern {
	// Wrap des appropriately for position
	var s string
	switch t {
	case End:
		s = StrEndBefore + escapeDes(des, re) + StrEndAfter
	default:
		fmt.Fprintf(os.Stderr, "unsupported position %q\n", t.String())
		os.Exit(1)
	}

	// Compile to hyperscan pattern
	return hyperscan.NewPattern(s, hyperscan.Caseless|hyperscan.SomLeftMost)
}

func compileHSPatterns(ds *dataset, t PositionType, re Remap) []*hyperscan.Pattern {
	var patterns []*hyperscan.Pattern

	for k, e := range *ds {
		// Add key to patterns
		patterns = append(patterns, compileHSPattern(k, t, re))

		// Add AbbrStd to patterns
		/*
			if e.AbbrStd != "" {
				patterns = append(patterns, compileHSPattern(e.AbbrStd, t, re))
			}
		*/

		// Add Abbrs to patterns
		for _, a := range e.Abbr {
			patterns = append(patterns, compileHSPattern(a, t, re))
		}
	}

	//fmt.Fprintf(os.Stderr, "+ compiled %d %q patterns from dataset\n", len(patterns), t.String())
	//fmt.Fprintf(os.Stderr, "++ %v\n", patterns)

	return patterns
}

// New returns a new Parser using the default company designator dataset
func New() (*Parser, error) {
	return NewMode(RE)
}

// New returns a new Parser using the default company designator dataset
func NewMode(mode Mode) (*Parser, error) {
	p := Parser{}

	re := make(Remap)
	re["Period"] = regexp.MustCompile(`\.`)
	re["Space"] = regexp.MustCompile(`\pZ+`)
	re["Paren"] = regexp.MustCompile("([()\uff08\uff09])")
	re["LeftParen"] = regexp.MustCompile(`\(`)
	re["RightParen"] = regexp.MustCompile(`\)`)
	re["UnicodeMarks"] = regexp.MustCompile(`\pM`)
	re["ASCII"] = regexp.MustCompile("^[[:ascii:]]+$")
	// HS-only below
	re["EndBefore"] = regexp.MustCompile(StrEndBefore)
	re["EndAfter"] = regexp.MustCompile(StrEndAfter)
	p.re = re

	ds, err := loadDataset(DefaultDataset)
	if err != nil {
		return nil, err
	}
	p.ds = ds

	// Compile End patterns
	switch mode {
	case RE:
		p.mode = RE
		endPattern := compileREPatterns(ds, End, re)
		//fmt.Fprintf(os.Stderr, "+ endPattern: %s\n", endPattern)
		endFallbackPattern := compileREPatterns(ds, EndFallback, re)
		//fmt.Fprintf(os.Stderr, "+ endFallbackPattern: %s\n", endFallbackPattern)
		endContPattern := compileREPatterns(ds, EndCont, re)
		//fmt.Fprintf(os.Stderr, "+ endContPattern: %s\n", endContPattern)
		beginPattern := compileREPatterns(ds, Begin, re)
		//fmt.Fprintf(os.Stderr, "+ beginPattern: %s\n", beginPattern)

		p.reEnd = regexp.MustCompile(`(?i)` +
			StrEndBefore + `(` + endPattern + `)` + StrEndAfter)
		//fmt.Fprintf(os.Stderr, "+ reEnd: %s\n", p.reEnd)
		p.reEndFallback = regexp.MustCompile(`(?i)` +
			StrEndBefore + `(` + endFallbackPattern + `)` + StrEndAfter)
		//fmt.Fprintf(os.Stderr, "+ reEndFallback: %s\n", p.reEndFallback)
		p.reBegin = regexp.MustCompile(`(?i)` +
			StrBeginBefore + `(` + beginPattern + `)` + StrBeginAfter)
		//fmt.Fprintf(os.Stderr, "+ reBegin: %s\n", p.reBegin)
		p.reEndCont = regexp.MustCompile(`(?i)` +
			StrEndContBefore + `(` + endContPattern + `)` + StrEndContAfter)
		//fmt.Fprintf(os.Stderr, "+ reEndCont: %s\n", p.reEndCont)

	case HS:
		p.mode = HS
		patterns := compileHSPatterns(ds, End, re)
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
	}

	return &p, nil
}

// Parse matches an input company name string against the company
// designator dataset and returns a Result object containing match
// results and any parsed components
func (p *Parser) Parse(input string) (*Result, error) {
	if p.mode == RE {
		return p.ParseRE(input)
	}

	return p.ParseHyperscan(input)
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

func (p *Parser) ParseHyperscan(input string) (*Result, error) {
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
		des = p.re["EndBefore"].ReplaceAllString(des, "")
		des = p.re["EndAfter"].ReplaceAllString(des, "")
		// Handle corner case where a left-parenthesis is wrongly stripped
		if p.re["RightParen"].MatchString(des) && !p.re["LeftParen"].MatchString(des) {
			des = "(" + des
		}
		res.Designator = des
	}

	return &res, nil
}

func (p *Parser) ParseRE(input string) (*Result, error) {
	inputNFD := norm.NFD.String(input)
	inputNFC := norm.NFC.String(input)
	res := Result{Input: inputNFC, ShortName: inputNFC}
	ctx := Context{}
	ctx.in = []byte(inputNFD)

	// Designators are usually final, so try end matching first
	matches := p.reEnd.FindStringSubmatch(inputNFD)
	if matches != nil {
		res.Matched = true
		res.ShortName = norm.NFC.String(matches[1])
		res.Designator = norm.NFC.String(matches[2])
		res.Position = End
		return &res, nil
	}

	// No final designator - retry using the fallback endings we blacklisted
	// for the initial run
	matches = p.reEndFallback.FindStringSubmatch(inputNFD)
	if matches != nil {
		res.Matched = true
		res.ShortName = norm.NFC.String(matches[1])
		res.Designator = norm.NFC.String(matches[2])
		// Note we deliberately use End here rather than EndFallback
		res.Position = End
		return &res, nil
	}

	// No final designator - retry without a word break for the subset of
	// languages that use continuous scripts (see LangContinua above)
	// Strip all parentheses for continuous script matches
	inputNFDStripped := p.re["Paren"].ReplaceAllString(inputNFD, "")
	matches = p.reEndCont.FindStringSubmatch(inputNFDStripped)
	if matches != nil {
		res.Matched = true
		res.ShortName = norm.NFC.String(matches[1])
		res.Designator = norm.NFC.String(matches[2])
		// Note we deliberately use End here rather than EndCont
		res.Position = End
		return &res, nil
	}

	// No final designator - check for a lead designator instead (e.g. ru, nl, etc.)
	matches = p.reBegin.FindStringSubmatch(inputNFD)
	if matches != nil {
		res.Matched = true
		res.ShortName = norm.NFC.String(matches[2])
		res.Designator = norm.NFC.String(matches[1])
		res.Position = Begin
		return &res, nil
	}

	return &res, nil
}
