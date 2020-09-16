package template

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Blesmol/pfscf/pfscf/args"
	"github.com/Blesmol/pfscf/pfscf/content"
	"github.com/Blesmol/pfscf/pfscf/csv"
	"github.com/Blesmol/pfscf/pfscf/param"
	"github.com/Blesmol/pfscf/pfscf/preset"
	"github.com/Blesmol/pfscf/pfscf/stamp"
	"github.com/Blesmol/pfscf/pfscf/utils"
)

const (
	floatGroupPattern  = `(\d+(?:\.(?:(?:\d*))?)?)` // should match floating point numbers, e.g. 2, 1., 45.123
	aspectRatioPattern = `^\s*` + floatGroupPattern + `\s*:\s*` + floatGroupPattern + `\s*$`
)

var (
	regexAspectRatio = regexp.MustCompile(aspectRatioPattern)
)

// Chronicle is the new approach for the Chronicle Template
type Chronicle struct {
	ID          string
	Description string
	Inherit     string
	Aspectratio string
	Parameters  param.Store
	Presets     preset.Store
	Content     content.ListStore

	filename string // filename of the originating yaml file
}

// NewChronicleTemplate returns a new ChronicleTemplate object.
func NewChronicleTemplate(filename string) (ct Chronicle) {
	ct.filename = filename
	return ct
}

func templateErr(ct *Chronicle, errIn error) (errOut error) {
	return fmt.Errorf("Template %v: %v", ct.ID, errIn)
}

func templateErrf(ct *Chronicle, msg string, args ...interface{}) (errOut error) {
	return fmt.Errorf("Template '%v': "+msg, ct.ID, args)
}

// ensureStoresAreInitialized is a workaround for the behavior of the stupid f... yaml library.
// If a section like "parameters:" is present, but empty, it will not be unmarshalled, and the
// underlying data structure will be ZEROed. So the stores will be uninitialized. Even if they
// were initialized before the unmarshalling. Yeah, great.
// See https://github.com/go-yaml/yaml/issues/395 , might be fixed with go-yaml v3 in the future.
func (ct *Chronicle) ensureStoresAreInitialized() {
	if ct.Parameters == nil {
		ct.Parameters = param.NewStore()
	}
	if ct.Presets == nil {
		ct.Presets = preset.NewStore()
	}
	if ct.Content == nil {
		ct.Content = content.NewListStore()
	}
}

// GetExampleArguments returns an array containing all keys and example values for all parameters.
// The result can be passed to the ArgStore.
func (ct *Chronicle) GetExampleArguments() (result []string) {
	return ct.Parameters.GetExampleArguments()
}

// inheritFrom inherits entries from multiple sections from another
// ChronicleTemplate object. An error is returned in case a content
// entry from sections 'parameters' or 'content' exists in both objects.
// In case a preset entry exists in both objects, then the one from the original
// object takes precedence.
func (ct *Chronicle) inheritFrom(otherCT *Chronicle) (err error) {
	err = ct.Parameters.InheritFrom(&otherCT.Parameters)
	if err != nil {
		return err
	}

	ct.Presets.InheritFrom(otherCT.Presets)

	ct.Content.InheritFrom(otherCT.Content)

	if !utils.IsSet(ct.Aspectratio) {
		ct.Aspectratio = otherCT.Aspectratio
	}

	return nil
}

// resolve resolves this template. This means that preset dependencies are resolved
// and after that the preset dependencies on content side. Currently nothing needs
// to be done for parameters.
func (ct *Chronicle) resolve() (err error) {
	if err = ct.Presets.Resolve(); err != nil {
		return err
	}

	if err = ct.Content.Resolve(ct.Presets); err != nil {
		return err
	}
	return nil
}

// WriteToCsvFile creates a CSV file out of the current chronicle template than can be used
// as input for the "batch fill" command
func (ct *Chronicle) WriteToCsvFile(filename string, separator rune, as *args.Store) (err error) {
	const numPlayers = 7

	records := [][]string{
		{"#ID", ct.ID},
		{"#Description", ct.Description},
		{"#"},
		{"#Players"}, // will be filled below with labels
	}
	for idx := 1; idx <= numPlayers; idx++ {
		outerIdx := len(records) - 1
		records[outerIdx] = append(records[outerIdx], fmt.Sprintf("Player %d", idx))
	}

	for _, contentID := range ct.Parameters.GetSortedKeys() {
		// entry should be large enough for id column + 7 players
		entry := make([]string, numPlayers+1)

		entry[0] = contentID

		// check if some value was provided on the cmd line that should be filled in everywhere
		if val, exists := as.Get(contentID); exists {
			for colIdx := 1; colIdx <= numPlayers; colIdx++ {
				entry[colIdx] = val
			}
		}

		records = append(records, entry)
	}

	err = csv.WriteFile(filename, separator, records)
	if err != nil {
		return err
	}

	return nil
}

// GenerateOutput adds the content of this chronicle template to the provided stamp.
func (ct *Chronicle) GenerateOutput(stamp *stamp.Stamp, argStore *args.Store) (err error) {
	// as we add new entries to the argStore, create a local store and set the
	// original store as parent.
	localArgStore, err := args.NewStore(args.StoreInit{Parent: argStore})
	if err != nil {
		return err
	}

	// check argStore values against parameter definitions
	if err = ct.Parameters.ValidateAndProcessArgs(localArgStore); err != nil {
		return err
	}

	if utils.IsSet(ct.Aspectratio) {
		xMarginPct, yMarginPct, err := ct.guessMarginsFromAspectRatio(stamp)
		if err != nil {
			return err
		}

		stamp.AddCanvas(0.0+xMarginPct/2, 0.0+yMarginPct/2, 100.0-xMarginPct/2, 100-yMarginPct/2)
		defer stamp.RemoveCanvas()
	}

	// pass to content store to generate output
	if err = ct.Content.GenerateOutput(stamp, localArgStore); err != nil {
		return err
	}

	return nil
}

// IsValid checks whether a given chronicle is valid. This should only be called
// after resolve() was called on this template.
func (ct *Chronicle) IsValid() (err error) {
	if utils.IsSet(ct.Aspectratio) {
		if _, _, err = parseAspectRatio(ct.Aspectratio); err != nil {
			return templateErr(ct, err)
		}
	}

	if !utils.IsSet(ct.Description) {
		return templateErrf(ct, "Missing description")
	}

	if err = ct.Parameters.IsValid(); err != nil {
		return templateErr(ct, err)
	}

	if err = ct.Content.IsValid(&ct.Parameters); err != nil {
		return templateErr(ct, err)
	}

	return nil
}

// Describe returns a short textual description of a single chronicle template.
// It returns the description as a multi-line string.
func (ct *Chronicle) Describe(verbose bool) (result string) {
	var sb strings.Builder

	if !verbose {
		fmt.Fprintf(&sb, "- %v", ct.ID)
		if utils.IsSet(ct.Description) {
			fmt.Fprintf(&sb, ": %v", ct.Description)
		}
	} else {
		fmt.Fprintf(&sb, "- %v\n", ct.ID)
		fmt.Fprintf(&sb, "\tDescription: %v\n", ct.Description)
		fmt.Fprintf(&sb, "\tFile: %v\n", ct.filename)
	}

	return sb.String()
}

// DescribeParams returns a textual description of the parameters expected by
// this chronicle template. It returns the description as a multi-line string.
func (ct *Chronicle) DescribeParams(verbose bool) (result string) {
	return ct.Parameters.Describe(verbose)
}

func parseAspectRatio(input string) (x, y float64, err error) {
	match := regexAspectRatio.FindStringSubmatch(input)
	if len(match) == 0 {
		err = fmt.Errorf("Provided aspect ratio does not follow pattern '<x>:<y>': %v", input)
		return
	}

	if x, err = strconv.ParseFloat(match[1], 64); err != nil {
		err = fmt.Errorf("Error parsing X part of aspect ratio '%v': %v", match[1], err)
	}

	if y, err = strconv.ParseFloat(match[2], 64); err != nil {
		err = fmt.Errorf("Error parsing Y part of aspect ratio '%v': %v", match[2], err)
	}

	return x, y, nil
}

// guessMarginsFromAspectRatio tries to calculate possible document margins from the provided
// aspect ratio. Assumption is that the PDF content will not be squeezed or stretched in any
// direction. So if the aspect ration differs this must mean than margins were added somewhere.
// The following function should correctly calculate the margins from the aspect ratio if
// margins were only added on the x axis OR the y axis, not on both.
func (ct *Chronicle) guessMarginsFromAspectRatio(stamp *stamp.Stamp) (xMarginPct, yMarginPct float64, err error) {
	sx, sy := stamp.GetDimensions()
	arx, ary, err := parseAspectRatio(ct.Aspectratio)
	if err != nil {
		return
	}

	haveAR := sx / sy
	wantAR := arx / ary

	switch {
	case wantAR > haveAR: // y axis has a margin
		f := sx * ary / arx
		g := (100.0 * f) / sy
		marginPct := 100 - g
		return 0.0, marginPct, nil
	case wantAR < haveAR: // x axis has a margin
		f := arx * sy / ary
		g := (100.0 * f) / sx
		marginPct := 100.0 - g
		//fmt.Printf("f: %.6f, g: %.6f, h: %.6f, test: %.6f\n", f, g, h, 100.0*609.9/612.0)
		return marginPct, 0.0, nil
	}

	return 0.0, 0.0, nil // no margins, fits perfect
}