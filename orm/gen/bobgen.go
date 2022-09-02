package gen

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/stephenafamo/bob/orm/gen/drivers"
	"github.com/volatiletech/strmangle"
)

var (
	// Tags must be in a format like: json, xml, etc.
	rgxValidTag = regexp.MustCompile(`[a-zA-Z_\.]+`)
	// Column names must be in format column_name or table_name.column_name
	rgxValidTableColumn = regexp.MustCompile(`^[\w]+\.[\w]+$|^[\w]+$`)
)

// State holds the global data needed by most pieces to run
type State[T any] struct {
	Config *Config[T]

	Dialect   string
	Schema    string
	Tables    []drivers.Table
	ExtraInfo T

	Templates     *templateList
	TestTemplates *templateList
}

// New creates a new state based off of the config
func New[T any](dialect string, config *Config[T]) (*State[T], error) {
	s := &State[T]{
		Config:  config,
		Dialect: dialect,
	}

	var templates []lazyTemplate

	if len(config.Generator) > 0 {
		noEditDisclaimer = []byte(
			fmt.Sprintf(noEditDisclaimerFmt, " by "+config.Generator),
		)
	}

	s.initInflections()

	err := s.initDBInfo()
	if err != nil {
		return nil, fmt.Errorf("unable to initialize tables: %w", err)
	}

	s.processTypeReplacements()

	templates, err = s.initTemplates()
	if err != nil {
		return nil, fmt.Errorf("unable to initialize templates: %w", err)
	}

	err = s.initOutFolders(templates)
	if err != nil {
		return nil, fmt.Errorf("unable to initialize the output folders: %w", err)
	}

	err = s.initTags(config.Tags)
	if err != nil {
		return nil, fmt.Errorf("unable to initialize struct tags: %w", err)
	}

	s.initAliases(&config.Aliases)

	return s, nil
}

// Run executes the templates and outputs them to files based on the
// state given.
func (s *State[T]) Run() error {
	data := &templateData[T]{
		Dialect:           s.Dialect,
		Tables:            s.Tables,
		ExtraInfo:         s.ExtraInfo,
		Aliases:           s.Config.Aliases,
		PkgName:           s.Config.PkgName,
		NoTests:           s.Config.NoTests,
		NoBackReferencing: s.Config.NoBackReferencing,
		StructTagCasing:   s.Config.StructTagCasing,
		TagIgnore:         make(map[string]struct{}),
		Tags:              s.Config.Tags,
		RelationTag:       s.Config.RelationTag,
		Schema:            s.Schema,
	}

	for _, v := range s.Config.TagIgnore {
		if !rgxValidTableColumn.MatchString(v) {
			return errors.New("Invalid column name %q supplied, only specify column name or table.column, eg: created_at, user.password")
		}
		data.TagIgnore[v] = struct{}{}
	}

	if err := generateSingletonOutput(s, data); err != nil {
		return fmt.Errorf("singleton template output: %w", err)
	}

	if !s.Config.NoTests {
		if err := generateSingletonTestOutput(s, data); err != nil {
			return fmt.Errorf("unable to generate singleton test template output: %w", err)
		}
	}

	var regularDirExtMap, testDirExtMap dirExtMap
	regularDirExtMap = groupTemplates(s.Templates)
	if !s.Config.NoTests {
		testDirExtMap = groupTemplates(s.TestTemplates)
	}

	for _, table := range s.Tables {
		data.Table = table

		// Generate the regular templates
		if err := generateOutput(s, regularDirExtMap, data); err != nil {
			return fmt.Errorf("unable to generate output: %w", err)
		}

		// Generate the test templates
		if !s.Config.NoTests && !table.IsView {
			if err := generateTestOutput(s, testDirExtMap, data); err != nil {
				return fmt.Errorf("unable to generate test output: %w", err)
			}
		}
	}

	return nil
}

// Cleanup closes any resources that must be closed
func (s *State[T]) Cleanup() error {
	// Nothing here atm, used to close the driver
	return nil
}

// initTemplates loads all template folders into the state object.
//
// If TemplateDirs is set it uses those, else it pulls from assets.
// Then it allows drivers to override, followed by replacements. Any
// user functions passed in by library users will be merged into the
// template.FuncMap.
//
// Because there's the chance for windows paths to jumped in
// all paths are converted to the native OS's slash style.
//
// Later, in order to properly look up imports the paths will
// be forced back to linux style paths.
func (s *State[T]) initTemplates() ([]lazyTemplate, error) {
	var err error

	templates := make(map[string]templateLoader)
	if len(s.Config.Templates) == 0 {
		return nil, errors.New("No templates defined")
	}

	for _, tempFS := range s.Config.Templates {
		err := fs.WalkDir(tempFS, ".", func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if entry.IsDir() {
				return nil
			}

			name := entry.Name()
			if filepath.Ext(name) == ".tpl" {
				templates[normalizeSlashes(path)] = assetLoader{fs: tempFS, name: path}
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// For stability, sort keys to traverse the map and turn it into a slice
	keys := make([]string, 0, len(templates))
	for k := range templates {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	lazyTemplates := make([]lazyTemplate, 0, len(templates))
	for _, k := range keys {
		lazyTemplates = append(lazyTemplates, lazyTemplate{
			Name:   k,
			Loader: templates[k],
		})
	}

	s.Templates, err = loadTemplates(lazyTemplates, false, s.Config.CustomTemplateFuncs)
	if err != nil {
		return nil, err
	}

	if !s.Config.NoTests {
		s.TestTemplates, err = loadTemplates(lazyTemplates, true, s.Config.CustomTemplateFuncs)
		if err != nil {
			return nil, err
		}
	}

	return lazyTemplates, nil
}

type dirExtMap map[string]map[string][]string

// groupTemplates takes templates and groups them according to their output directory
// and file extension.
func groupTemplates(templates *templateList) dirExtMap {
	tplNames := templates.Templates()
	dirs := make(map[string]map[string][]string)
	for _, tplName := range tplNames {
		normalized, isSingleton, _, _ := outputFilenameParts(tplName)
		if isSingleton {
			continue
		}

		dir := filepath.Dir(normalized)
		if dir == "." {
			dir = ""
		}

		extensions, ok := dirs[dir]
		if !ok {
			extensions = make(map[string][]string)
			dirs[dir] = extensions
		}

		ext := getLongExt(tplName)
		ext = strings.TrimSuffix(ext, ".tpl")
		slice := extensions[ext]
		extensions[ext] = append(slice, tplName)
	}

	return dirs
}

// initDBInfo retrieves information about the database
func (s *State[T]) initDBInfo() error {
	dbInfo, err := s.Config.Driver.Assemble()
	if err != nil {
		return fmt.Errorf("unable to fetch table data: %w", err)
	}

	if len(dbInfo.Tables) == 0 {
		return errors.New("no tables found in database")
	}

	if err := checkPKeys(dbInfo.Tables); err != nil {
		return err
	}

	s.Schema = dbInfo.Schema
	s.Tables = dbInfo.Tables
	s.ExtraInfo = dbInfo.ExtraInfo

	return nil
}

// processTypeReplacements checks the config for type replacements
// and performs them.
func (s *State[T]) processTypeReplacements() {
	for _, r := range s.Config.Replacements {
		for i := range s.Tables {
			t := s.Tables[i]

			if !shouldReplaceInTable(t, r) {
				continue
			}

			for j := range t.Columns {
				c := t.Columns[j]
				if matchColumn(c, r.Match) {
					t.Columns[j] = columnMerge(c, r.Replace)
				}
			}
		}
	}
}

// matchColumn checks if a column 'c' matches specifiers in 'm'.
// Anything defined in m is checked against a's values, the
// match is a done using logical and (all specifiers must match).
// Bool fields are only checked if a string type field matched first
// and if a string field matched they are always checked (must be defined).
//
// Doesn't care about Unique columns since those can vary independent of type.
func matchColumn(c, m drivers.Column) bool {
	matchedSomething := false

	// return true if we matched, or we don't have to match
	// if we actually matched against something, then additionally set
	// matchedSomething so we can check boolean values too.
	matches := func(matcher, value string) bool {
		if len(matcher) != 0 && matcher != value {
			return false
		}
		matchedSomething = true
		return true
	}

	if !matches(m.Name, c.Name) {
		return false
	}
	if !matches(m.Type, c.Type) {
		return false
	}
	if !matches(m.DBType, c.DBType) {
		return false
	}
	if !matches(m.UDTName, c.UDTName) {
		return false
	}
	if !matches(m.FullDBType, c.FullDBType) {
		return false
	}
	if m.ArrType != nil && (c.ArrType == nil || !matches(*m.ArrType, *c.ArrType)) {
		return false
	}
	if m.DomainName != nil && (c.DomainName == nil || !matches(*m.DomainName, *c.DomainName)) {
		return false
	}

	if !matchedSomething {
		return false
	}

	if m.Generated != c.Generated {
		return false
	}
	if m.Nullable != c.Nullable {
		return false
	}

	return true
}

// columnMerge merges values from src into dst. Bools are copied regardless
// strings are copied if they have values. Name is excluded because it doesn't make
// sense to non-programatically replace a name.
func columnMerge(dst, src drivers.Column) drivers.Column {
	ret := dst
	if len(src.Type) != 0 {
		ret.Type = src.Type
		ret.Imports = src.Imports
	}
	if len(src.Imports) != 0 {
		ret.Imports = src.Imports
	}
	if len(src.DBType) != 0 {
		ret.DBType = src.DBType
	}
	if len(src.UDTName) != 0 {
		ret.UDTName = src.UDTName
	}
	if len(src.FullDBType) != 0 {
		ret.FullDBType = src.FullDBType
	}
	if src.ArrType != nil && len(*src.ArrType) != 0 {
		ret.ArrType = new(string)
		*ret.ArrType = *src.ArrType
	}

	return ret
}

// shouldReplaceInTable checks if tables were specified in types.match in the config.
// If tables were set, it checks if the given table is among the specified tables.
func shouldReplaceInTable(t drivers.Table, r Replace) bool {
	if len(r.Tables) == 0 {
		return true
	}

	for _, replaceInTable := range r.Tables {
		if replaceInTable == t.Name {
			return true
		}
	}

	return false
}

// initOutFolders creates the folders that will hold the generated output.
func (s *State[T]) initOutFolders(lazyTemplates []lazyTemplate) error {
	if s.Config.Wipe {
		if err := os.RemoveAll(s.Config.OutFolder); err != nil {
			return err
		}
	}

	newDirs := make(map[string]struct{})
	for _, t := range lazyTemplates {
		// templates/js/00_struct.js.tpl
		// templates/js/singleton/00_struct.js.tpl
		// we want the js part only
		fragments := strings.Split(t.Name, string(os.PathSeparator))

		// Throw away the root dir and filename
		fragments = fragments[1 : len(fragments)-1]
		if len(fragments) != 0 && fragments[len(fragments)-1] == "singleton" {
			fragments = fragments[:len(fragments)-1]
		}

		if len(fragments) == 0 {
			continue
		}

		newDirs[strings.Join(fragments, string(os.PathSeparator))] = struct{}{}
	}

	if err := os.MkdirAll(s.Config.OutFolder, os.ModePerm); err != nil {
		return err
	}

	for d := range newDirs {
		if err := os.MkdirAll(filepath.Join(s.Config.OutFolder, d), os.ModePerm); err != nil {
			return err
		}
	}

	return nil
}

// initInflections adds custom inflections to strmangle's ruleset
func (s *State[T]) initInflections() {
	ruleset := strmangle.GetBoilRuleset()

	for k, v := range s.Config.Inflections.Plural {
		ruleset.AddPlural(k, v)
	}
	for k, v := range s.Config.Inflections.PluralExact {
		ruleset.AddPluralExact(k, v, true)
	}

	for k, v := range s.Config.Inflections.Singular {
		ruleset.AddSingular(k, v)
	}
	for k, v := range s.Config.Inflections.SingularExact {
		ruleset.AddSingularExact(k, v, true)
	}

	for k, v := range s.Config.Inflections.Irregular {
		ruleset.AddIrregular(k, v)
	}
}

// initTags removes duplicate tags and validates the format
// of all user tags are simple strings without quotes: [a-zA-Z_\.]+
func (s *State[T]) initTags(tags []string) error {
	s.Config.Tags = strmangle.RemoveDuplicates(tags)
	for _, v := range s.Config.Tags {
		if !rgxValidTag.MatchString(v) {
			return errors.New("Invalid tag format %q supplied, only specify name, eg: xml")
		}
	}

	return nil
}

func (s *State[T]) initAliases(a *Aliases) {
	FillAliases(a, s.Tables)
}

// checkPKeys ensures every table has a primary key column
func checkPKeys(tables []drivers.Table) error {
	var missingPkey []string
	for _, t := range tables {
		if !t.IsView && t.PKey == nil {
			missingPkey = append(missingPkey, t.Name)
		}
	}

	if len(missingPkey) != 0 {
		return fmt.Errorf("primary key missing in tables (%s)", strings.Join(missingPkey, ", "))
	}

	return nil
}

// normalizeSlashes takes a path that was made on linux or windows and converts it
// to a native path.
func normalizeSlashes(path string) string {
	path = strings.ReplaceAll(path, `/`, string(os.PathSeparator))
	path = strings.ReplaceAll(path, `\`, string(os.PathSeparator))
	return path
}
