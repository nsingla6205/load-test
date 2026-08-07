// The following directive is necessary to make the package coherent:
//go:build ignore
// +build ignore

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"text/template"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// This program generates "../../database/vcp/sewrapper.go". It can be invoked by running:
// 1. "go run main.go" from the current directory
// 2. "go generate" from ../../database

var (
	logger      = log.NewLogger()
	funList     = make([]*functionSignature, 0)
	funSig      *functionSignature
	offset      token.Pos
	src         string
	parsed      = false
	directory   string
	packageName string = "core" // Default package name, can be overridden by command line argument
)

type param struct {
	Name string
	Type string
}

type functionAggregate struct {
	Full      []*functionSignature
	Modified  []*functionSignature
	Unchanged []*functionSignature
	Package   string
}

func check(e error) {
	if e != nil {
		panic(e)
	}
}

type functionSignature struct {
	Name      string
	InParams  []*param
	RetVals   []*param
	Input     string   // parameter string in the function signature
	InputTl   string   // parameter string in the function call
	Output    string   // return value string in the function signature
	VarNames  []string // variable names for closure
	RetParams string   // retval receiver
}

type FileVisitor struct {
}

type FuncVisitor struct {
}

func (v *FuncVisitor) Visit(node ast.Node) (w ast.Visitor) {
	switch t := node.(type) {
	case *ast.InterfaceType:
		for _, method := range t.Methods.List {
			name := method.Names[0].Name
			funSig = &functionSignature{Name: name, InParams: make([]*param, 0), RetVals: make([]*param, 0)}
			funList = append(funList, funSig)
			switch mt := method.Type.(type) {
			case *ast.FuncType:
				for _, p := range mt.Params.List {
					pNames := ""
					for i, n := range p.Names {
						if i > 0 && i < len(p.Names) {
							pNames = pNames + ", "
						}
						pNames = pNames + n.Name
					}
					pType := src[p.Type.Pos()-offset : p.Type.End()-offset]
					funSig.InParams = append(funSig.InParams, &param{Name: pNames, Type: pType})
				}
				if mt.Results != nil {
					for _, p := range mt.Results.List {
						pNames := ""
						for i, n := range p.Names {
							if i > 0 && i < len(p.Names) {
								pNames = pNames + ", "
							}
							pNames = pNames + n.Name
						}
						pType := src[p.Type.Pos()-offset : p.Type.End()-offset]
						funSig.RetVals = append(funSig.RetVals, &param{Name: pNames, Type: pType})
					}
				}
			}
		}
	}
	return nil
}

func (v *FileVisitor) Visit(node ast.Node) (w ast.Visitor) {
	switch t := node.(type) {
	case *ast.Ident:
		s := t.Name
		if s == "DataStore" && !parsed {
			logger.Info("Parsing DataStore interface...")
			decl := t.Obj.Decl
			switch y := decl.(type) {
			case *ast.TypeSpec:
				switch z := y.Type.(type) {
				case *ast.InterfaceType:
					logger.Info("Processing functions...", "len(z.Methods.List)", len(z.Methods.List))
					ast.Walk(&FuncVisitor{}, z)
				}
			}
			parsed = true
		}
	}
	return v
}

func main() {
	if len(os.Args) > 2 {
		// If an argument is provided, use it as the directory
		directory = os.Args[1]
		packageName = os.Args[2]
	} else {
		// Default to "vcp" if no argument is provided
		directory = "vcp"
	}

	// Read source
	dat, err := ioutil.ReadFile("../../database/" + directory + "/interface.go")
	check(err)
	src = string(dat)

	logger.Info("Generating an interface wrapper...")

	fset := token.NewFileSet() // positions are relative to fset

	f, err := parser.ParseFile(fset, "../../database/"+directory+"/interface.go", src, parser.AllErrors)
	check(err)
	offset = f.Pos()

	v := &FileVisitor{}
	ast.Walk(v, f)

	filteredList := make([]*functionSignature, 0)
	nochangeList := make([]*functionSignature, 0)
	copy(filteredList, funList)
	for _, sig := range funList {
		logger.Info("Analysing function...", "name", sig.Name)
		if postProcess(sig) {
			// Skip functions that do not return an error
			filteredList = append(filteredList, sig)
		} else {
			nochangeList = append(nochangeList, sig)
		}
	}
	aggrList := functionAggregate{
		Full:      funList,
		Modified:  filteredList,
		Unchanged: nochangeList,
		Package:   packageName,
	}
	fname := "../../database/" + directory + "/sewrapper.go"
	logger.Info("Generating", "fname", fname)
	f1, err := os.Create(fname)
	check(err)
	defer f1.Close()
	logger.Info("Writing functions...")
	packageTemplate.Execute(f1, aggrList)
	logger.Info("Code generation complete!")
}

func postProcess(sig *functionSignature) bool {
	errFound := false

	for i, in := range sig.InParams {
		if i > 0 {
			sig.Input = sig.Input + ", "
		}
		sig.Input = sig.Input + in.Name + " " + in.Type
	}
	// Prepare the typeless input parameter string
	for i, in := range sig.InParams {
		if i > 0 {
			sig.InputTl = sig.InputTl + ", "
		}
		sig.InputTl = sig.InputTl + in.Name
		if strings.HasPrefix(in.Type, "...") {
			sig.InputTl = sig.InputTl + "..."
		}
	}

	sig.VarNames = make([]string, 0)
	// Prepare the output parameter string
	for i, out := range sig.RetVals {
		if i > 0 {
			sig.Output = sig.Output + ", "
		}
		if out.Name == "" {
			sig.Output = sig.Output + out.Type
		} else {
			sig.Output = sig.Output + out.Name + " " + out.Type
		}
		if out.Type != "error" {
			// Prepare the output receivers
			vname := "var" + strconv.Itoa(i)
			sig.RetParams = sig.RetParams + vname
			sig.RetParams = sig.RetParams + ", "
			sig.VarNames = append(sig.VarNames, vname+" "+out.Type)
		} else {
			logger.Info("Wrapping function...", "name", sig.Name)
			errFound = true
		}
	}

	if len(sig.RetVals) == 0 {
		sig.Output = " "
	}
	if len(sig.RetVals) == 1 {
		sig.Output = " " + sig.Output + " "
	}
	if len(sig.RetVals) > 1 {
		sig.Output = " (" + sig.Output + ") "
	}

	return errFound
}

var packageTemplate = template.Must(template.New("").Parse(`// Code generated by go generate; DO NOT EDIT.
// This file was generated automatically
//
//go:generate go run ../../cmd/retry-engine-generator/main.go
package database

import (
	"context"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/{{ printf "%s" .Package }}/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
{{- if eq .Package "database" }}
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
{{- end }}
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/retry"
)
{{- range .Modified }}

func (re *retryEngine) {{ printf "%s" .Name }}({{ printf "%s" .Input }}){{ printf "%s" .Output }}{
{{- range $index, $element := .VarNames }}
	var {{ printf "%s" $element }}
{{- end }}
	err := retry.Do(func(attempt int) (bool, error) {
		var err error
		{{ printf "%s" .RetParams }}err = re.dataStore.{{ printf "%s" .Name }}({{ printf "%s" .InputTl }})
		if err != nil {
			re.logError("{{ printf "%s" .Name }}", err)
			if !dbutils.IsTransientErr(err) {
				return false, err
			}
		}
		return true, err
	})
	return {{ printf "%s" .RetParams }}err
}
{{- end }}
{{- range .Unchanged }}

func (re *retryEngine) {{ printf "%s" .Name }}({{ printf "%s" .Input }}){{ printf "%s" .Output }}{
	{{if (ne .Output " ") }}return {{end}}re.DataStore.{{ printf "%s" .Name }}({{ printf "%s" .InputTl }})
}
{{- end }}
`))
