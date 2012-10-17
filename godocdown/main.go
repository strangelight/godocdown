package main

import (
	"fmt"
	"flag"
	"go/doc"
	"go/parser"
	"go/token"
	"go/printer"
	"os"
	"strings"
	"bytes"
	"io/ioutil"
	"regexp"
	"path/filepath"
	tme "time"
	tmplate "text/template"
)

const (
	punchCardWidth = 80
	debug = false
)

// Flags
var (
	signature_flag = flag.Bool("signature", false, "Add godocdown signature to the end of the documentation")
	plain_flag = flag.Bool("plain", false, "Emit standard Markdown, rather than Github Flavored Markdown (the default)")
	heading_flag = flag.String("heading", "TitleCase1Word", "Heading detection method: 1Word, TitleCase, Title, TitleCase1Word, \"\"")
)

var (
	fset *token.FileSet

	synopsisHeading1Word_Regexp = regexp.MustCompile("(?m)^([A-Za-z0-9]+)$")
	synopsisHeadingTitleCase_Regexp = regexp.MustCompile("(?m)^((?:[A-Z][A-Za-z0-9]*)(?:[ \t]+[A-Z][A-Za-z0-9]*)*)$")
	synopsisHeadingTitle_Regexp = regexp.MustCompile("(?m)^((?:[A-Za-z0-9]+)(?:[ \t]+[A-Za-z0-9]+)*)$")
	synopsisHeadingTitleCase1Word_Regexp = regexp.MustCompile("(?m)^((?:[A-Za-z0-9]+)|(?:(?:[A-Z][A-Za-z0-9]*)(?:[ \t]+[A-Z][A-Za-z0-9]*)*))$")

	strip_Regexp = regexp.MustCompile("(?m)^\\s*// contains filtered or unexported fields\\s*\n")
	indent_Regexp = regexp.MustCompile("(?m)^([^\\n])") // Match at least one character at the start of the line
	synopsisHeading_Regexp = synopsisHeading1Word_Regexp
)

var DefaultStyle = Style{
	IncludeImport: true,

	SynopsisHeader: "###",
	SynopsisHeading: synopsisHeadingTitleCase1Word_Regexp,

	UsageHeader: "## Usage\n",

	ConstantHeader: "####",
	VariableHeader: "####",
	FunctionHeader: "####",
	TypeHeader: "####",
	TypeFunctionHeader: "####",

	IncludeSignature: false,
}
var RenderStyle = DefaultStyle

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		executable, err := os.Stat(os.Args[0])
		if err != nil {
			return
		}
		time := executable.ModTime()
		since := tme.Since(time)
		fmt.Fprintf(os.Stderr, "---\n%s (%.2f)\n", time.Format("2006-01-02 15:04 MST"), since.Minutes())
	}
}

type Style struct {
	IncludeImport bool

	SynopsisHeader string
	SynopsisHeading *regexp.Regexp

	UsageHeader string

	ConstantHeader string
	VariableHeader string
	FunctionHeader string
	TypeHeader string
	TypeFunctionHeader string

	IncludeSignature bool
}

type _document struct {
	name string
	pkg *doc.Package
	isCommand bool
	dotImport string
}

func _formatIndent(target, indent, preIndent string) string {
	var buffer bytes.Buffer
	doc.ToText(&buffer, target, indent, preIndent, punchCardWidth-2*len(indent))
	return buffer.String()
}

func space(width int) string {
	return strings.Repeat(" ", width)
}

func formatIndent(target string) string {
	return _formatIndent(target, space(0), space(4))
}

func indentCode(target string) string {
	if *plain_flag {
		return indent(target + "\n", space(4))
	}
	return fmt.Sprintf("```go\n%s\n```", target)
}

func headifySynopsis(target string) string {
	detect := RenderStyle.SynopsisHeading
	if detect == nil {
		return target
	}
	return detect.ReplaceAllStringFunc(target, func(heading string) string {
		return fmt.Sprintf("%s %s", RenderStyle.SynopsisHeader, heading)
	})
}

func headlineSynopsis(synopsis, header string, scanner *regexp.Regexp) string {
	return scanner.ReplaceAllStringFunc(synopsis, func(headline string) string {
		return fmt.Sprintf("%s %s", header, headline)
	})
}

func sourceOfNode(target interface{}) string {
	var buffer bytes.Buffer
	mode := printer.TabIndent | printer.UseSpaces
	err := (&printer.Config{Mode: mode, Tabwidth: 4}).Fprint(&buffer, fset, target)
	if err != nil {
		return ""
	}
	return strip_Regexp.ReplaceAllString(buffer.String(), "")
}

func indent(target string, indent string) string {
	return indent_Regexp.ReplaceAllString(target, indent + "$1")
}

func trimSpace(buffer *bytes.Buffer) {
	tmp := bytes.TrimSpace(buffer.Bytes())
	buffer.Reset()
	buffer.Write(tmp)
}

func loadDocument(path string) (*_document, error) {
	fset = token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, path, func(file os.FileInfo) bool {
		name := file.Name()
		if name[0] != '.' && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			return true
		}
		return false
	}, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("Could not parse \"%s\": %v", path, err)
	}

	dotImport := ""
	if read, err := ioutil.ReadFile(filepath.Join(path, ".import")); err == nil {
		dotImport = strings.TrimSpace(strings.Split(string(read), "\n")[0])
	}

	for _, pkg := range pkgs {
		isCommand := false
		name := ""
		pkg := doc.New(pkg, ".", 0)
		switch pkg.Name {
		case "main":
			// We're probably a command, but by convention, documentation
			// should be in the documentation package:
			// http://golang.org/doc/articles/godoc_documenting_go_code.html
			continue
		case "documentation":
			// We're a command, this package/file contains the documentation
			// path is used to get the containing directory in the case of
			// command documentation
			path, err := filepath.Abs(path)
			if err != nil {
				panic(err)
			}
			_, name = filepath.Split(path)
			isCommand = true
		default:
			name = pkg.Name
			// Just a regular package
		}

		document := &_document{
			name: name,
			pkg: pkg,
			isCommand: isCommand,
			dotImport: dotImport,
		}

		return document, nil
	}

	return nil, nil
}

func (self *_document) Emit(buffer *bytes.Buffer) {

	// Header
	renderHeaderTo(buffer, self)

	// Synopsis
	renderSynopsisTo(buffer, self)

	// Usage
	if !self.isCommand {
		renderUsageTo(buffer, self)
	}

	trimSpace(buffer)
}

func (self *_document) EmitSignature(buffer *bytes.Buffer) {

	renderSignatureTo(buffer)

	trimSpace(buffer)
}

func loadTemplate(document *_document, path string) *tmplate.Template {
	templatePath := filepath.Join(path, ".godocdown.markdown")
	{
		_, err := os.Stat(templatePath)
		if err != nil {
			if os.IsExist(err) {
				return nil
			}
			return nil // Other error reporting?
		}
	}

	template := tmplate.New("").Funcs(tmplate.FuncMap{
		"Emit": func() string {
			var buffer bytes.Buffer
			document.Emit(&buffer)
			return buffer.String()
		},
		"EmitSignature": func() string {
			var buffer bytes.Buffer
			document.EmitSignature(&buffer)
			return buffer.String()
		},
	})
	template, err := template.ParseFiles(templatePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing template \"%s\": %v", templatePath, err)
		os.Exit(64)
	}
	return template
}

func main() {
	flag.Parse()
	path := flag.Arg(0)
	if path == "" {
		path = "."
	}

	RenderStyle.IncludeSignature = *signature_flag

	switch *heading_flag {
	case "1Word":
		RenderStyle.SynopsisHeading = synopsisHeading1Word_Regexp
	case "TitleCase":
		RenderStyle.SynopsisHeading = synopsisHeadingTitleCase_Regexp
	case "Title":
		RenderStyle.SynopsisHeading = synopsisHeadingTitle_Regexp
	case "TitleCase1Word":
		RenderStyle.SynopsisHeading = synopsisHeadingTitleCase1Word_Regexp
	case "", "-":
		RenderStyle.SynopsisHeading = nil
	}

	document, err := loadDocument(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
	}
	if document == nil {
		// Nothing found.
		rootPath, _ := filepath.Abs(path)
		fmt.Fprintf(os.Stderr, "No package/documentation found in %s (%s)\n", path, rootPath)
		os.Exit(64)
	}

	template := loadTemplate(document, path)

	var buffer bytes.Buffer
	if template == nil {
		document.Emit(&buffer)
		document.EmitSignature(&buffer)
	} else {
		err := template.Templates()[0].Execute(&buffer, document)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error running template: %v", err)
			os.Exit(64)
		}
	}

	if debug {
		// Skip printing if we're debugging
		return
	}

	documentation := buffer.String()
	documentation = strings.TrimSpace(documentation)
	fmt.Println(documentation)
}
