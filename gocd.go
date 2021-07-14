/*
gocd is a go library for matching and parsing company designators
(like `Limited`, `LLC`, `Incorporée`) in company names.
*/

//go:generate cp -p ../company_designator/company_designator.yml data
//go:generate cp -p ../../cpan/Business-CompanyDesignator/t/t10/data.yml data/tests.yml
//go:generate go run assets_generate.go

package gocd

import (
	"io/ioutil"
	"regexp"
	"strings"

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
	"L.L.C.":       true, // vs. `Co. L.L.C.`
	"L.C.":         true, // vs. `L.L.C.`
	"Co.":          true, // vs. `& Co.` (ampersand matched as punct)
	"Co. L.L.C.":   true, // vs. `& Co. L.L.C.` (ampersand matched as punct)
}

const (
	DefaultDataset   = "/company_designator.yml"
	StrBeginBefore   = `^\pZ*`
	StrBeginAfter    = `[\pZ\pP]\pZ*(.+?)\pZ*$`
	StrEndBefore     = `^\pZ*(.+?)\pZ*([\pZ\pP])\pZ*`
	StrEndAfter      = `\pZ*$`
	StrEndContBefore = `^\pZ*(.+?)\pZ*`
	StrEndContAfter  = `\pZ*$`
)

type PositionType int

const (
	None PositionType = iota
	End
	EndFallback
	EndCont
	Begin
	BeginFallback
)

func (p PositionType) String() string {
	return [...]string{
		"none", "end", "end_fallback", "end_cont", "begin", "begin_fallback",
	}[p]
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
	re              Remap
	ds              *dataset
	reEnd           *regexp.Regexp
	reEndFallback   *regexp.Regexp
	reEndCont       *regexp.Regexp
	reBegin         *regexp.Regexp
	reBeginFallback *regexp.Regexp
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

func loadDataset() (*dataset, error) {
	fh, err := assets.Open(DefaultDataset)
	if err != nil {
		return nil, err
	}
	data, err := ioutil.ReadAll(fh)
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

// escapeDes does some standard escaping of designators
func escapeDes(des string, re Remap) string {
	// Allow ampersands to match more broadly
	des = re["Ampersand"].ReplaceAllString(des, `\s*[&+]\s*`)
	// Escape parentheses in the designator itself
	des = re["Paren"].ReplaceAllString(des, `\$1`)
	// Periods are treated as optional literals, with optional trailing stff
	des = re["PeriodSpace"].ReplaceAllString(des, `\.*[\pZ,()-]*`)
	// Interpret embedded spaces in designators pretty liberally
	des = re["Space"].ReplaceAllString(des, `[\pZ,()-]+`)
	return des
}

func addPattern(patterns []string, s string, t PositionType, re Remap) []string {
	// Skip Begin/End strings if they are blacklisted
	if (t == End || t == Begin) && EndDesignatorBlacklist[s] {
		return patterns
	}
	// Skip BeginFallback/EndFallback strings *unless* they are blacklisted
	if (t == EndFallback || t == BeginFallback) && !EndDesignatorBlacklist[s] {
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
		// If t is Begin or BeginFallback, restrict to entries with 'Lead' set
		if (t == Begin || t == BeginFallback) && !e.Lead {
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
	if len(patterns) == 0 {
		return ""
	}

	// Join patterns as alternates, and always allow outer parentheses
	pattern := `\(?(?:` + strings.Join(patterns, "|") + `)\)?`

	//fmt.Fprintf(os.Stderr, "+ compiled %d %q patterns from dataset\n", len(patterns), t.String())
	//fmt.Fprintf(os.Stderr, "++ %s\n", pattern)

	return pattern
}

// New returns a new Parser using the default company designator dataset
func New() (*Parser, error) {
	p := Parser{}

	re := make(Remap)
	re["PeriodSpace"] = regexp.MustCompile(`\.\pZ*`)
	re["Space"] = regexp.MustCompile(`\pZ+`)
	re["SpaceDotSpace"] = regexp.MustCompile(`\pZ+\.\pZ*`)
	re["Ampersand"] = regexp.MustCompile(`\pZ*&\pZ*`)
	re["Paren"] = regexp.MustCompile("([()\uff08\uff09])")
	re["ParenSpace"] = regexp.MustCompile("\\pZ*[()\uff08\uff09]\\pZ*")
	re["UnicodeMarks"] = regexp.MustCompile(`\pM`)
	re["ASCII"] = regexp.MustCompile("^[[:ascii:]]+$")
	p.re = re

	ds, err := loadDataset()
	if err != nil {
		return nil, err
	}
	p.ds = ds

	// Compile End patterns
	endPattern := compileREPatterns(ds, End, re)
	//fmt.Fprintf(os.Stderr, "+ endPattern: %s\n", endPattern)
	endFallbackPattern := compileREPatterns(ds, EndFallback, re)
	//fmt.Fprintf(os.Stderr, "+ endFallbackPattern: %s\n", endFallbackPattern)
	endContPattern := compileREPatterns(ds, EndCont, re)
	//fmt.Fprintf(os.Stderr, "+ endContPattern: %s\n", endContPattern)
	beginPattern := compileREPatterns(ds, Begin, re)
	//fmt.Fprintf(os.Stderr, "+ beginPattern: %s\n", beginPattern)
	beginFallbackPattern := compileREPatterns(ds, BeginFallback, re)
	//fmt.Fprintf(os.Stderr, "+ beginFallbackPattern: %s\n", beginFallbackPattern)

	if endPattern != "" {
		p.reEnd = regexp.MustCompile(`(?i)` +
			StrEndBefore + `(` + endPattern + `)` + StrEndAfter)
		//fmt.Fprintf(os.Stderr, "+ reEnd: %s\n", p.reEnd)
	}
	if endFallbackPattern != "" {
		p.reEndFallback = regexp.MustCompile(`(?i)` +
			StrEndBefore + `(` + endFallbackPattern + `)` + StrEndAfter)
		//fmt.Fprintf(os.Stderr, "+ reEndFallback: %s\n", p.reEndFallback)
	}
	if endContPattern != "" {
		p.reEndCont = regexp.MustCompile(`(?i)` +
			StrEndContBefore + `(` + endContPattern + `)` + StrEndContAfter)
		//fmt.Fprintf(os.Stderr, "+ reEndCont: %s\n", p.reEndCont)
	}
	if beginPattern != "" {
		p.reBegin = regexp.MustCompile(`(?i)` +
			StrBeginBefore + `(` + beginPattern + `)` + StrBeginAfter)
	}
	//fmt.Fprintf(os.Stderr, "+ reBegin: %s\n", p.reBegin)
	if beginFallbackPattern != "" {
		p.reBeginFallback = regexp.MustCompile(`(?i)` +
			StrBeginBefore + `(` + beginFallbackPattern + `)` + StrBeginAfter)
		//fmt.Fprintf(os.Stderr, "+ reBeginFallback: %s\n", p.reBeginFallback)
	}

	return &p, nil
}

// checkDesPunct handles the reEnd situation where our breaking
// punctuation character before the designator might be something
// we should include in the designator e.g. '&' or '('
func (p *Parser) checkDesPunct(punct, des string) string {
	if punct != "(" {
		return des
	}
	return punct + des
}

// Parse matches an input company name string against the company
// designator dataset and returns a Result object containing match
// results and any parsed components
func (p *Parser) Parse(input string) (*Result, error) {
	inputNFD := norm.NFD.String(input)
	inputNFC := norm.NFC.String(input)
	res := Result{Input: inputNFC, ShortName: inputNFC}
	ctx := Context{}
	ctx.in = []byte(inputNFD)

	// Minimal preprocessing
	// Try and normalise strange dot-space pattern with initials e.g. P .J . S . C
	inputNFD = p.re["SpaceDotSpace"].ReplaceAllString(inputNFD, ". ")

	// Designators are usually final, so try end matching first
	var matches []string
	if p.reEnd != nil {
		matches = p.reEnd.FindStringSubmatch(inputNFD)
		if matches != nil {
			//fmt.Printf("+ reEnd matches: %q %q %q\n", matches[1], matches[2], matches[3])
			res.Matched = true
			res.ShortName = norm.NFC.String(matches[1])
			res.Designator = norm.NFC.String(p.checkDesPunct(matches[2], matches[3]))
			res.Position = End
			return &res, nil
		}
	}

	// No final designator - retry using the fallback endings we blacklisted
	// for the previous run
	if p.reEndFallback != nil {
		matches = p.reEndFallback.FindStringSubmatch(inputNFD)
		if matches != nil {
			//fmt.Printf("+ reEndFallback matches: %q %q %q\n", matches[1], matches[2], matches[3])
			res.Matched = true
			res.ShortName = norm.NFC.String(matches[1])
			res.Designator = norm.NFC.String(p.checkDesPunct(matches[2], matches[3]))
			// Note we use End here rather than EndFallback
			res.Position = End
			return &res, nil
		}
	}

	// No final designator - retry without a word break for the subset of
	// languages that use continuous scripts (see LangContinua above)
	// Strip all parentheses for continuous script matches
	if p.reEndCont != nil {
		inputNFDStripped := p.re["ParenSpace"].ReplaceAllString(inputNFD, "")
		matches = p.reEndCont.FindStringSubmatch(inputNFDStripped)
		if matches != nil {
			res.Matched = true
			res.ShortName = norm.NFC.String(matches[1])
			res.Designator = norm.NFC.String(matches[2])
			// Note we use End here rather than EndCont
			res.Position = End
			return &res, nil
		}
	}

	// No final designator - check for a lead designator instead (e.g. ru, nl, etc.)
	if p.reBegin != nil {
		matches = p.reBegin.FindStringSubmatch(inputNFD)
		if matches != nil {
			res.Matched = true
			res.ShortName = norm.NFC.String(matches[2])
			res.Designator = norm.NFC.String(matches[1])
			res.Position = Begin
			return &res, nil
		}
	}

	// No lead designator either - retry using the fallback endings we
	// blacklisted for the previous run
	if p.reBeginFallback != nil {
		matches = p.reBeginFallback.FindStringSubmatch(inputNFD)
		if matches != nil {
			res.Matched = true
			res.ShortName = norm.NFC.String(matches[2])
			res.Designator = norm.NFC.String(matches[1])
			// Note we use Begin here rather than BeginFallback
			res.Position = Begin
			return &res, nil
		}
	}

	return &res, nil
}
